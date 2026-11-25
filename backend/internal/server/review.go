package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/oddomens/coffer/internal/store"
)

// Review: comments pinned to a video timecode or a spot on an image, and
// decisions (approved / changes requested). Team members comment from the
// app; link visitors comment when the link allows it.

type commentReq struct {
	Place    string   `json:"place"`
	Path     string   `json:"path"`
	Body     string   `json:"body"`
	Kind     string   `json:"kind"` // comment | approved | changes
	Timecode *float64 `json:"timecode"`
	X        *float64 `json:"x"`
	Y        *float64 `json:"y"`
	ParentID int64    `json:"parentId"`
	Internal bool     `json:"internal"`
	Name     string   `json:"name"` // link visitors without a verified email
}

func (s *Server) validComment(w http.ResponseWriter, req *commentReq) bool {
	req.Body = strings.TrimSpace(req.Body)
	if req.Kind == "" {
		req.Kind = store.CommentNote
	}
	switch {
	case req.Kind != store.CommentNote && req.Kind != store.CommentApproved && req.Kind != store.CommentChanges:
		badRequest(w, "kind must be comment, approved or changes")
	case req.Kind == store.CommentNote && req.Body == "":
		badRequest(w, "write a comment first")
	case len(req.Body) > 5000:
		badRequest(w, "comments can be at most 5000 characters")
	case req.Timecode != nil && (math.IsNaN(*req.Timecode) || *req.Timecode < 0 || *req.Timecode > 1e6):
		badRequest(w, "that timecode is out of range")
	case (req.X == nil) != (req.Y == nil),
		req.X != nil && (math.IsNaN(*req.X) || *req.X < 0 || *req.X > 1 || math.IsNaN(*req.Y) || *req.Y < 0 || *req.Y > 1):
		badRequest(w, "markers need x and y between 0 and 1")
	default:
		return true
	}
	return false
}

func (s *Server) validParent(w http.ResponseWriter, r *http.Request, drive, p string, id int64) bool {
	if id == 0 {
		return true
	}
	c, err := s.store.CommentByID(r.Context(), id)
	if err != nil || c.DriveID != drive || c.Path != p || c.ParentID != 0 {
		badRequest(w, "you can only reply to a comment on this file")
		return false
	}
	return true
}

func (s *Server) handleCommentsList(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	p, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.store.Comments(r.Context(), a.DriveID, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCommentCreate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req commentReq
	if !decodeJSON(w, r, &req) || !s.validComment(w, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok {
		return
	}
	p, err := a.resolve(req.Path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !a.d().Exists(r.Context(), p) || !s.validParent(w, r, a.DriveID, p, req.ParentID) {
		if !a.d().Exists(r.Context(), p) {
			writeErr(w, http.StatusNotFound, "not_found", "file not found")
		}
		return
	}
	c := &store.Comment{DriveID: a.DriveID, Path: p, ParentID: req.ParentID, UserID: u.ID, Author: u.Username,
		Kind: req.Kind, Body: req.Body, Timecode: req.Timecode, X: req.X, Y: req.Y, Internal: req.Internal}
	if err := s.store.AddComment(r.Context(), c); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "review."+c.Kind, a.DriveID+":"+p, truncate(c.Body, 120))
	go s.reviewNotify(c, u.ID, "")
	writeJSON(w, http.StatusCreated, c)
}

// commentFor loads a comment the user may act on.
func (s *Server) commentFor(w http.ResponseWriter, r *http.Request, u *store.User) (*store.Comment, bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, err := s.store.CommentByID(r.Context(), id)
	if err != nil || !s.canReach(r.Context(), u, c.DriveID, c.Path, false, false, 3) {
		writeErr(w, http.StatusNotFound, "not_found", "comment not found")
		return nil, false
	}
	return c, true
}

func (s *Server) handleCommentUpdate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Resolved *bool `json:"resolved"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	c, ok := s.commentFor(w, r, u)
	if !ok {
		return
	}
	if req.Resolved != nil {
		if err := s.store.ResolveComment(r.Context(), c.ID, *req.Resolved); err != nil {
			s.fail(w, r, err)
			return
		}
		c.Resolved = *req.Resolved
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleCommentDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	c, ok := s.commentFor(w, r, u)
	if !ok {
		return
	}
	// Your own comments; guests' comments by anyone who can edit the file.
	if c.UserID != u.ID && !u.IsAdmin() && !(c.UserID == 0 && s.canReach(r.Context(), u, c.DriveID, c.Path, false, true, 3)) {
		forbidden(w, "you can only delete your own comments")
		return
	}
	if err := s.store.DeleteComment(r.Context(), c.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Link visitors.

func (s *Server) shareCommentFile(w http.ResponseWriter, r *http.Request, sc *shareCtx, p string) (*shareItem, string, bool) {
	if !s.requireUnlocked(w, r, sc) {
		return nil, "", false
	}
	if !sc.sh.CanComment || !sc.sh.CanView {
		forbidden(w, "comments are not enabled for this link")
		return nil, "", false
	}
	it, abs, err := sc.resolve(p)
	if err != nil {
		s.fail(w, r, err)
		return nil, "", false
	}
	if e, err := it.m.d.Stat(r.Context(), abs); err != nil || e.IsDir {
		writeErr(w, http.StatusNotFound, "not_found", "file not found")
		return nil, "", false
	}
	return it, abs, true
}

func (s *Server) handleShareComments(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	it, p, ok := s.shareCommentFile(w, r, sc, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	list, err := s.store.Comments(r.Context(), it.m.info.ID, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]*store.Comment, 0, len(list))
	for _, c := range list {
		// Only threads from this link or the team, without internal notes
		// (or replies under them): another link's visitors are other people.
		if !linkSees(c, list, sc.sh.ID) {
			continue
		}
		if sc.viewer == nil || !strings.EqualFold(c.Email, sc.viewer.Email) {
			// Don't show visitors each other's addresses, including as the
			// name of someone who gave none.
			c.Email = ""
			if c.Guest {
				c.Author = publicAuthor(c.Author)
			}
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

// linkSees reports whether visitors of link shareID may see c, one of the
// file's comments in list: a thread started through that link or by the
// team outside any link, never an internal note or a reply under one.
func linkSees(c *store.Comment, list []*store.Comment, shareID string) bool {
	if c.Internal {
		return false
	}
	if c.ParentID != 0 {
		for _, p := range list {
			if p.ID == c.ParentID {
				return linkSees(p, list, shareID)
			}
		}
		return false
	}
	return c.ShareID == shareID || (c.UserID != 0 && c.ShareID == "")
}

// publicAuthor shows only the part before the @ of an email address used
// as a guest's name.
func publicAuthor(name string) string {
	if local, _, ok := strings.Cut(name, "@"); ok && local != "" {
		return local
	}
	return name
}

func (s *Server) handleShareCommentCreate(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	var req commentReq
	if !decodeJSON(w, r, &req) || !s.validComment(w, &req) {
		return
	}
	it, p, ok := s.shareCommentFile(w, r, sc, req.Path)
	if !ok || !s.validParent(w, r, it.m.info.ID, p, req.ParentID) {
		return
	}
	if req.ParentID != 0 {
		// Visitors can only reply to threads they can see: not internal
		// notes, nor threads from another link.
		if parent, err := s.store.CommentByID(r.Context(), req.ParentID); err != nil || !linkSees(parent, nil, sc.sh.ID) {
			badRequest(w, "you can only reply to a comment on this file")
			return
		}
	}
	if !s.shareMiss.Allow("comment:" + ipString(r)) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many comments, slow down a little")
		return
	}
	c := &store.Comment{DriveID: it.m.info.ID, Path: p, ShareID: sc.sh.ID, ParentID: req.ParentID,
		Kind: req.Kind, Body: req.Body, Timecode: req.Timecode, X: req.X, Y: req.Y}
	if sc.viewer != nil {
		c.Author, c.Email = sc.viewer.Name, sc.viewer.Email
		if c.Author == "" {
			c.Author = sc.viewer.Email
		}
	} else {
		c.Author = strings.Join(strings.Fields(req.Name), " ")
		if c.Author == "" || len(c.Author) > 60 {
			badRequest(w, "add your name so they know who is commenting")
			return
		}
	}
	if err := s.store.AddComment(r.Context(), c); err != nil {
		s.fail(w, r, err)
		return
	}
	s.shareAudit(r, sc, "review."+c.Kind, it.m.info.ID+":"+p, truncate(c.Author+": "+c.Body, 160))
	go s.reviewNotify(c, 0, sc.sh.ID)
	if sc.viewer == nil {
		c.Email = ""
	}
	writeJSON(w, http.StatusCreated, c)
}

// reviewNotify tells the link owner (for comments through a link) and
// everyone on the team who commented on the file, except the author.
func (s *Server) reviewNotify(c *store.Comment, authorID int64, shareID string) {
	ctx := context.Background()
	notified := map[int64]bool{authorID: true}
	name := path.Base(c.Path)
	var title string
	switch c.Kind {
	case store.CommentApproved:
		title = c.Author + " approved “" + name + "”"
	case store.CommentChanges:
		title = c.Author + " asked for changes to “" + name + "”"
	default:
		title = c.Author + " commented on “" + name + "”"
	}
	body := c.Body
	if c.Timecode != nil {
		body = fmt.Sprintf("At %s: %s", timecode(*c.Timecode), body)
	}
	link := "/files"
	if shareID != "" {
		if sh, err := s.store.ShareByID(ctx, shareID); err == nil {
			notified[sh.OwnerID] = true
			s.notifyShareOwner(shareID, sh.OwnerID, "review", title, body)
		}
	}
	if c.Internal {
		return
	}
	ids, _ := s.store.CommentAuthors(ctx, c.DriveID, c.Path)
	for _, id := range ids {
		if !notified[id] {
			notified[id] = true
			s.notify(id, "review", title, body, link)
		}
	}
}

func timecode(sec float64) string {
	t := int(sec)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}
