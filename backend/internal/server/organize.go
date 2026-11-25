package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// findPlace returns the viewer's place that contains drive path p, the
// most specific one when several do. Places on offline drives are skipped.
func findPlace(ps []*access, drive, p string) *access {
	var best *access
	for _, a := range ps {
		if a.DriveID == drive && a.Online && a.m != nil && files.Within(a.Root, p) && (best == nil || len(a.Root) > len(best.Root)) {
			best = a
		}
	}
	return best
}

// itemJSON is a file or folder somewhere in the viewer's places.
type itemJSON struct {
	Place     string `json:"place"`
	PlaceName string `json:"placeName"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
	ModTime   int64  `json:"modTime"`
	Kind      string `json:"kind"`
	Missing   bool   `json:"missing,omitempty"`
}

func (s *Server) itemOut(ctx context.Context, a *access, p string, isDir bool) itemJSON {
	it := itemJSON{Place: a.ID, PlaceName: a.Name, Path: a.rel(p), Name: path.Base(p), IsDir: isDir, Kind: files.KindOf(path.Base(p), isDir)}
	if p == "." {
		it.Name = a.Name
	}
	e, err := a.d().Stat(ctx, p)
	if err != nil {
		it.Missing = true
		return it
	}
	it.Size, it.ModTime, it.IsDir, it.Kind = e.Size, e.ModTime, e.IsDir, e.Kind
	if e.IsDir {
		it.Size = -1
		if fsz, ok := s.index.Size(ctx, a.DriveID, p); ok {
			it.Size = fsz.Size
		}
	}
	return it
}

// Collections: hand-picked files and folders from anywhere, shared as one link.

type collectionJSON struct {
	*store.Collection
	Items []itemJSON `json:"items"`
	Links int        `json:"links"`
}

func (s *Server) collectionOut(ctx context.Context, ps []*access, c *store.Collection) collectionJSON {
	out := collectionJSON{Collection: c, Items: []itemJSON{}}
	for _, ci := range c.Items {
		if a := findPlace(ps, ci.DriveID, ci.Path); a != nil {
			out.Items = append(out.Items, s.itemOut(ctx, a, ci.Path, ci.IsDir))
		}
	}
	if shares, err := s.store.Shares(ctx, c.OwnerID); err == nil {
		for _, sh := range shares {
			if sh.CollectionID == c.ID {
				out.Links++
			}
		}
	}
	return out
}

func (s *Server) handleCollectionsList(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := s.store.Collections(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ps, _ := s.placesFor(r.Context(), u)
	out := make([]collectionJSON, 0, len(list))
	for _, c := range list {
		out = append(out, s.collectionOut(r.Context(), ps, c))
	}
	writeJSON(w, http.StatusOK, out)
}

type collectionReq struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Place       string   `json:"place"` // with Paths: items to add on create
	Paths       []string `json:"paths"`
}

func applyCollection(w http.ResponseWriter, c *store.Collection, req collectionReq) bool {
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" || len(n) > 120 {
			badRequest(w, "give the collection a name (up to 120 characters)")
			return false
		}
		c.Name = n
	}
	if req.Description != nil {
		if len(*req.Description) > 2000 {
			badRequest(w, "the description can be at most 2000 characters")
			return false
		}
		c.Description = strings.TrimSpace(*req.Description)
	}
	return true
}

func (s *Server) handleCollectionCreate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req collectionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil {
		badRequest(w, "give the collection a name")
		return
	}
	c := &store.Collection{ID: security.RandomToken(9), OwnerID: u.ID}
	if !applyCollection(w, c, req) {
		return
	}
	if err := s.store.CreateCollection(r.Context(), c); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Paths) > 0 && !s.addToCollection(w, r, u, c, req.Place, req.Paths) {
		return
	}
	s.audit(r, u, "", "collection.create", "", c.Name)
	c, _ = s.store.CollectionByID(r.Context(), c.ID)
	ps, _ := s.placesFor(r.Context(), u)
	writeJSON(w, http.StatusCreated, s.collectionOut(r.Context(), ps, c))
}

func (s *Server) ownedCollection(w http.ResponseWriter, r *http.Request, u *store.User) (*store.Collection, bool) {
	c, err := s.store.CollectionByID(r.Context(), r.PathValue("id"))
	if err != nil || c.OwnerID != u.ID {
		writeErr(w, http.StatusNotFound, "not_found", "collection not found")
		return nil, false
	}
	return c, true
}

func (s *Server) handleCollectionUpdate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req collectionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	c, ok := s.ownedCollection(w, r, u)
	if !ok || !applyCollection(w, c, req) {
		return
	}
	if err := s.store.UpdateCollection(r.Context(), c); err != nil {
		s.fail(w, r, err)
		return
	}
	ps, _ := s.placesFor(r.Context(), u)
	writeJSON(w, http.StatusOK, s.collectionOut(r.Context(), ps, c))
}

func (s *Server) handleCollectionDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	c, ok := s.ownedCollection(w, r, u)
	if !ok {
		return
	}
	if err := s.store.DeleteCollection(r.Context(), c.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "collection.delete", "", c.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addToCollection(w http.ResponseWriter, r *http.Request, u *store.User, c *store.Collection, place string, paths []string) bool {
	a, ok := s.place(w, r, u, place)
	if !ok {
		return false
	}
	if !a.CanShare {
		forbidden(w, "you are not allowed to share from here")
		return false
	}
	if len(c.Items)+len(paths) > 500 {
		badRequest(w, "a collection can hold at most 500 items")
		return false
	}
	for _, rel := range paths {
		p, err := a.resolve(rel)
		if err != nil {
			s.fail(w, r, err)
			return false
		}
		if p == "." {
			badRequest(w, "a whole drive cannot be added; pick a folder")
			return false
		}
		e, err := a.d().Stat(r.Context(), p)
		if err != nil {
			s.fail(w, r, err)
			return false
		}
		if err := s.store.AddCollectionItem(r.Context(), c.ID, &store.CollectionItem{DriveID: a.DriveID, Path: p, IsDir: e.IsDir}); err != nil {
			s.fail(w, r, err)
			return false
		}
	}
	return true
}

func (s *Server) handleCollectionAdd(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req collectionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	c, ok := s.ownedCollection(w, r, u)
	if !ok || !s.addToCollection(w, r, u, c, req.Place, req.Paths) {
		return
	}
	c, _ = s.store.CollectionByID(r.Context(), c.ID)
	ps, _ := s.placesFor(r.Context(), u)
	writeJSON(w, http.StatusOK, s.collectionOut(r.Context(), ps, c))
}

func (s *Server) handleCollectionRemove(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place string `json:"place"`
		Path  string `json:"path"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	c, ok := s.ownedCollection(w, r, u)
	if !ok {
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
	if err := s.store.RemoveCollectionItem(r.Context(), c.ID, a.DriveID, p); err != nil {
		s.fail(w, r, err)
		return
	}
	c, _ = s.store.CollectionByID(r.Context(), c.ID)
	ps, _ := s.placesFor(r.Context(), u)
	writeJSON(w, http.StatusOK, s.collectionOut(r.Context(), ps, c))
}

// Favorites.

func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := s.store.Favorites(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ps, _ := s.placesFor(r.Context(), u)
	out := []itemJSON{}
	for _, f := range list {
		if a := findPlace(ps, f.DriveID, f.Path); a != nil {
			out = append(out, s.itemOut(r.Context(), a, f.Path, f.IsDir))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFavoriteSet(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place string `json:"place"`
		Path  string `json:"path"`
		On    bool   `json:"on"`
	}
	if !decodeJSON(w, r, &req) {
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
	isDir := true
	if req.On {
		e, err := a.d().Stat(r.Context(), p)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		isDir = e.IsDir
	}
	if err := s.store.SetFavorite(r.Context(), u.ID, a.DriveID, p, isDir, req.On); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Tags.

var tagRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._&+-]{0,39}$`)

func cleanTag(t string) (string, bool) {
	t = strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(t), "#")), " ")
	return t, tagRe.MatchString(t)
}

type tagJSON struct {
	Tag   string `json:"tag"`
	Color string `json:"color"`
	Count int    `json:"count"`
}

func (s *Server) handleTagsList(w http.ResponseWriter, r *http.Request, u *store.User) {
	all, err := s.store.AllTags(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	colors, _ := s.store.TagColors(r.Context())
	ps, _ := s.placesFor(r.Context(), u)
	counts := map[string]*tagJSON{}
	var order []string
	for _, t := range all {
		if findPlace(ps, t.DriveID, t.Path) == nil {
			continue
		}
		k := strings.ToLower(t.Tag)
		if counts[k] == nil {
			counts[k] = &tagJSON{Tag: t.Tag, Color: colors[k]}
			order = append(order, k)
		}
		counts[k].Count++
	}
	out := []tagJSON{}
	for _, k := range order {
		out = append(out, *counts[k])
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Tag) < strings.ToLower(out[j].Tag) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTagItems(w http.ResponseWriter, r *http.Request, u *store.User) {
	tag, ok := cleanTag(r.PathValue("tag"))
	if !ok {
		badRequest(w, "that is not a tag")
		return
	}
	list, err := s.store.Tagged(r.Context(), tag)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ps, _ := s.placesFor(r.Context(), u)
	out := []itemJSON{}
	for _, t := range list {
		if a := findPlace(ps, t.DriveID, t.Path); a != nil {
			if it := s.itemOut(r.Context(), a, t.Path, t.IsDir); !it.Missing {
				out = append(out, it)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTagsEdit(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place  string   `json:"place"`
		Paths  []string `json:"paths"`
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok || !requireWrite(w, a) {
		return
	}
	if len(req.Paths) > 1000 || len(req.Add)+len(req.Remove) > 50 {
		badRequest(w, "too many items or tags at once")
		return
	}
	var add, remove []string
	for _, list := range []struct {
		in  []string
		out *[]string
	}{{req.Add, &add}, {req.Remove, &remove}} {
		for _, t := range list.in {
			c, ok := cleanTag(t)
			if !ok {
				badRequest(w, "tags use letters, numbers and spaces (up to 40 characters)")
				return
			}
			*list.out = append(*list.out, c)
		}
	}
	for _, rel := range req.Paths {
		p, err := a.resolve(rel)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		e, err := a.d().Stat(r.Context(), p)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, t := range add {
			if err := s.store.AddTag(r.Context(), a.DriveID, p, t, e.IsDir, u.ID); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		for _, t := range remove {
			_ = s.store.RemoveTag(r.Context(), a.DriveID, p, t)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

var tagColors = map[string]bool{"gray": true, "red": true, "orange": true, "amber": true, "green": true, "teal": true, "blue": true, "violet": true, "pink": true}

func (s *Server) handleTagColor(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tag, ok := cleanTag(r.PathValue("tag"))
	if !ok || !tagColors[req.Color] {
		badRequest(w, "pick one of the tag colours")
		return
	}
	if err := s.store.SetTagColor(r.Context(), tag, req.Color); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Locks.

// smbOpen lists files open over SMB, as written by the Samba sidecar every
// few seconds to DATA/smb/status.json (smbstatus --json).
func (s *Server) smbOpen() map[string]*store.Lock {
	f := filepath.Join(s.cfg.DataDir, "smb", "status.json")
	info, err := os.Stat(f)
	if err != nil || time.Since(info.ModTime()) > 2*time.Minute {
		return nil
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return nil
	}
	var st struct {
		Sessions map[string]struct {
			UID      json.Number `json:"uid"`
			Username string      `json:"username"`
		} `json:"sessions"`
		OpenFiles map[string]struct {
			ServicePath string `json:"service_path"`
			Filename    string `json:"filename"`
			Opens       map[string]struct {
				UID      json.Number `json:"uid"`
				OpenedAt string      `json:"opened_at"`
			} `json:"opens"`
		} `json:"open_files"`
	}
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	users := map[string]string{}
	for _, se := range st.Sessions {
		users[se.UID.String()] = strings.TrimPrefix(se.Username, "WORKGROUP\\")
	}
	out := map[string]*store.Lock{}
	for _, of := range st.OpenFiles {
		drive := s.smbDrive(of.ServicePath)
		if drive == "" || of.Filename == "" || of.Filename == "." {
			continue
		}
		p, err := files.Clean(of.Filename)
		if err != nil {
			continue
		}
		l := &store.Lock{DriveID: drive, Path: p, Source: "smb", Note: "Open over SMB"}
		for _, o := range of.Opens {
			if n := users[o.UID.String()]; n != "" {
				l.Username = n
			}
			if t, err := time.Parse("2006-01-02T15:04:05.999999-0700", o.OpenedAt); err == nil && (l.CreatedAt == 0 || t.Unix() < l.CreatedAt) {
				l.CreatedAt = t.Unix()
			}
		}
		if l.Username == "" {
			l.Username = "someone"
		}
		out[drive+":"+p] = l
	}
	return out
}

// smbDrive maps a Samba share path to a drive: /share is the main drive and
// /shares/<Name> is the local drive with that folder or drive name.
func (s *Server) smbDrive(sp string) string {
	if sp == "/share" {
		return "files"
	}
	name, ok := strings.CutPrefix(sp, "/shares/")
	if !ok {
		return ""
	}
	for _, m := range s.driveList() {
		if m.info.Kind == "local" && (strings.EqualFold(m.info.Name, name) || strings.EqualFold(filepath.Base(m.path), name)) {
			return m.info.ID
		}
	}
	return ""
}

// lockOn returns the lock on drive path p (Coffer's own, or an SMB open).
func (s *Server) lockOn(ctx context.Context, drive, p string) *store.Lock {
	if l, err := s.store.LockOf(ctx, drive, p); err == nil {
		return l
	}
	return s.smbOpen()[drive+":"+p]
}

var errLocked = errors.New("locked")

// checkUnlocked refuses changes to p (or, for a folder, anything inside it)
// while someone else has it locked in Coffer. SMB opens are only shown, not
// enforced: Samba has its own locking.
func (s *Server) checkUnlocked(w http.ResponseWriter, r *http.Request, u *store.User, drive, p string) bool {
	if err := s.lockedErr(r.Context(), u, drive, p); errors.Is(err, errLocked) {
		writeErr(w, http.StatusLocked, "locked", err.Error())
		return false
	} else if err != nil {
		s.fail(w, r, err)
		return false
	}
	return true
}

// lockedErr fails closed: when the locks can't be read, nothing changes.
func (s *Server) lockedErr(ctx context.Context, u *store.User, drive, p string) error {
	locks, err := s.store.LocksUnder(ctx, drive, p)
	if err != nil {
		return fmt.Errorf("checking for locks: %w", err)
	}
	for _, l := range locks {
		if u == nil || l.UserID != u.ID {
			name := path.Base(l.Path)
			return &lockError{name: name, by: l.Username}
		}
	}
	return nil
}

type lockError struct{ name, by string }

func (e *lockError) Error() string { return "“" + e.name + "” is locked by " + e.by }
func (e *lockError) Unwrap() error { return errLocked }

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place string `json:"place"`
		Path  string `json:"path"`
		Note  string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok || !requireWrite(w, a) {
		return
	}
	p, err := a.resolve(req.Path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	e, err := a.d().Stat(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if e.IsDir {
		badRequest(w, "only files can be locked")
		return
	}
	if l, err := s.store.LockOf(r.Context(), a.DriveID, p); err == nil {
		if l.UserID == u.ID {
			writeJSON(w, http.StatusOK, l)
			return
		}
		writeErr(w, http.StatusLocked, "locked", "already locked by "+l.Username)
		return
	}
	l := &store.Lock{DriveID: a.DriveID, Path: p, UserID: u.ID, Username: u.Username, Note: truncate(strings.TrimSpace(req.Note), 200), Source: "coffer"}
	if err := s.store.PutLock(r.Context(), l); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "lock", a.DriveID+":"+p, l.Note)
	writeJSON(w, http.StatusCreated, l)
}

func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	p, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	l, err := s.store.LockOf(r.Context(), a.DriveID, p)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if l.UserID != u.ID && !u.IsAdmin() {
		forbidden(w, "only "+l.Username+" or an administrator can unlock this")
		return
	}
	if err := s.store.DeleteLock(r.Context(), a.DriveID, p); err != nil {
		s.fail(w, r, err)
		return
	}
	detail := ""
	if l.UserID != u.ID {
		detail = "lock held by " + l.Username
		s.notify(l.UserID, "lock", u.Username+" unlocked “"+path.Base(p)+"”", "Your lock was removed.", "")
	}
	s.audit(r, u, "", "unlock", a.DriveID+":"+p, detail)
	w.WriteHeader(http.StatusNoContent)
}

// Activity.

type activityJSON struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	Who    string `json:"who"`
	Guest  bool   `json:"guest"`
	Action string `json:"action"`
	Path   string `json:"path"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// movedTo lists the audit actions whose detail is "to <destination>".
var movedTo = map[string]bool{"rename": true, "move": true, "copy": true, "backup.create": true}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	p, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	list, err := s.store.Activity(r.Context(), a.DriveID, p, before, 50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]activityJSON, 0, len(list))
	for _, e := range list {
		_, ep, _ := strings.Cut(e.Path, ":")
		if !files.Within(a.Root, ep) {
			continue
		}
		who := e.Username
		guest := e.UserID == 0
		if who == "" && guest {
			who = "Link visitor"
		}
		detail := e.Detail
		// Moves and renames say "to <path>" or "to <drive>:<path>"; show
		// the destination as the viewer sees it, and never name a folder
		// outside the viewer's place.
		if dst, ok := strings.CutPrefix(detail, "to "); ok && movedTo[e.Action] {
			dst = strings.TrimSuffix(dst, " (WebDAV)")
			here := true
			if d, rest, ok := strings.Cut(dst, ":"); ok {
				if m, _ := s.drive(d); m != nil {
					here, dst = d == a.DriveID, rest
				}
			}
			if here && files.Within(a.Root, dst) {
				detail = "to " + a.rel(dst)
			} else {
				detail = "to another folder"
			}
		}
		out = append(out, activityJSON{ID: e.ID, TS: e.TS, Who: who, Guest: guest, Action: e.Action, Path: a.rel(ep), Name: path.Base(ep), Detail: detail})
	}
	writeJSON(w, http.StatusOK, out)
}

// List decorations: favorites, tags, locks and review state for a folder.

type listEntry struct {
	files.Entry
	Favorite bool                 `json:"favorite,omitempty"`
	Tags     []string             `json:"tags,omitempty"`
	Lock     *store.Lock          `json:"lock,omitempty"`
	Review   *store.ReviewSummary `json:"review,omitempty"`
	Backup   *backupMark          `json:"backup,omitempty"`
}

func (s *Server) decorate(ctx context.Context, u *store.User, a *access, dir string, es []files.Entry, covers []backupCover) []listEntry {
	favs := map[string]bool{}
	if list, err := s.store.Favorites(ctx, u.ID); err == nil {
		for _, f := range list {
			if f.DriveID == a.DriveID {
				favs[f.Path] = true
			}
		}
	}
	tags, _ := s.store.TagsIn(ctx, a.DriveID, dir)
	reviews, _ := s.store.ReviewsIn(ctx, a.DriveID, dir)
	locks := map[string]*store.Lock{}
	if ls, err := s.store.LocksUnder(ctx, a.DriveID, dir); err == nil {
		for _, l := range ls {
			locks[l.Path] = l
		}
	}
	for k, l := range s.smbOpen() {
		if _, p, _ := strings.Cut(k, ":"); l.DriveID == a.DriveID && files.Parent(p) == dir && locks[p] == nil {
			locks[p] = l
		}
	}
	out := make([]listEntry, len(es))
	for i, e := range es {
		p := e.Path // still the drive path here
		out[i] = listEntry{Entry: e, Favorite: favs[p], Tags: tags[p], Lock: locks[p], Review: reviews[p],
			Backup: markFor(covers, p, e.IsDir, e.Size, e.ModTime)}
		out[i].Path = a.rel(p)
	}
	return out
}
