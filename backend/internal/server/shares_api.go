package server

import (
	"fmt"
	"net/http"
	"net/netip"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/mail"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

type shareJSON struct {
	ID           string             `json:"id"`
	URL          string             `json:"url"`
	Name         string             `json:"name"`
	Path         string             `json:"path"`
	Place        string             `json:"place,omitempty"` // the viewer's place containing it
	DriveName    string             `json:"driveName"`
	IsDir        bool               `json:"isDir"`
	HasPassword  bool               `json:"hasPassword"`
	ExpiresAt    int64              `json:"expiresAt"`
	MaxDownloads int64              `json:"maxDownloads"`
	Downloads    int64              `json:"downloads"`
	CanView      bool               `json:"canView"`
	CanDownload  bool               `json:"canDownload"`
	CanUpload    bool               `json:"canUpload"`
	Disabled     bool               `json:"disabled"`
	Title        string             `json:"title"`
	Message      string             `json:"message"`
	Note         string             `json:"note"`
	UploadMaxMB  int64              `json:"uploadMaxMB"`
	UploadExts   string             `json:"uploadExts"`
	UploadNamed  bool               `json:"uploadNamed"`
	Notify       int                `json:"notify"`
	AllowedNets  string             `json:"allowedNets"`
	ViewMode     string             `json:"viewMode"`
	Access       string             `json:"access"`
	CanComment   bool               `json:"canComment"`
	Latest       bool               `json:"latest"`
	Watermark    bool               `json:"watermark"`
	Collection   string             `json:"collection,omitempty"` // collection id
	Recipients   []*store.Recipient `json:"recipients"`
	Owner        string             `json:"owner"`
	CreatedAt    int64              `json:"createdAt"`
	LastAccess   int64              `json:"lastAccess"`
	Active       bool               `json:"active"`
	Missing      bool               `json:"missing"`
}

func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	scheme := "http"
	if isSecure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) shareOut(r *http.Request, ps []*access, sh *store.Share) shareJSON {
	token, _ := s.keys.Decrypt(sh.TokenEnc)
	out := shareJSON{
		ID: sh.ID, URL: s.baseURL(r) + "/s/" + string(token), Name: path.Base(sh.Path), Path: "/" + strings.TrimPrefix(sh.Path, "."),
		IsDir: sh.IsDir, HasPassword: sh.PasswordHash != "", ExpiresAt: sh.ExpiresAt, MaxDownloads: sh.MaxDownloads,
		Downloads: sh.Downloads, CanView: sh.CanView, CanDownload: sh.CanDownload, CanUpload: sh.CanUpload,
		Disabled: sh.Disabled, Title: sh.Title, Message: sh.Message, Note: sh.Note, UploadMaxMB: sh.UploadMaxMB,
		UploadExts: sh.UploadExts, UploadNamed: sh.UploadNamed, Notify: sh.Notify, AllowedNets: sh.AllowedNets,
		ViewMode: sh.ViewMode, Owner: sh.OwnerName, CreatedAt: sh.CreatedAt, LastAccess: sh.LastAccess,
		Active: sh.Active(time.Now().Unix()), Missing: true, Access: sh.Access, CanComment: sh.CanComment,
		Latest: sh.Latest, Watermark: sh.Watermark, Collection: sh.CollectionID,
	}
	out.Recipients, _ = s.store.Recipients(r.Context(), sh.ID)
	if out.Recipients == nil {
		out.Recipients = []*store.Recipient{}
	}
	if sh.CollectionID != "" {
		if c, err := s.store.CollectionByID(r.Context(), sh.CollectionID); err == nil {
			out.Name, out.Path, out.DriveName, out.Missing = c.Name, "", "Collection", false
		}
		return out
	}
	if m, err := s.drive(sh.DriveID); err == nil {
		out.DriveName = m.info.Name
		out.Missing = !m.d.Exists(r.Context(), sh.Path)
	}
	for _, a := range ps {
		if a.DriveID == sh.DriveID && files.Within(a.Root, sh.Path) {
			out.Place, out.Path = a.ID, a.rel(sh.Path)
			break
		}
	}
	return out
}

func (s *Server) handleSharesList(w http.ResponseWriter, r *http.Request, u *store.User) {
	owner := u.ID
	if u.IsAdmin() && r.URL.Query().Get("all") == "1" {
		owner = 0
	}
	list, err := s.store.Shares(r.Context(), owner)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ps, _ := s.placesFor(r.Context(), u)
	out := make([]shareJSON, 0, len(list))
	for _, sh := range list {
		if p := r.URL.Query().Get("place"); p != "" {
			// Only shares inside one folder of one place (used by the share dialog).
			a, err := s.placeByID(r.Context(), u, p)
			if err != nil {
				continue
			}
			target, err := a.resolve(r.URL.Query().Get("path"))
			if err != nil || sh.DriveID != a.DriveID || sh.Path != target {
				continue
			}
		}
		out = append(out, s.shareOut(r, ps, sh))
	}
	writeJSON(w, http.StatusOK, out)
}

type shareReq struct {
	Place        *string `json:"place"`
	Path         *string `json:"path"`
	Password     *string `json:"password"`  // "" removes it
	ExpiresAt    *int64  `json:"expiresAt"` // unix seconds, 0 = never
	MaxDownloads *int64  `json:"maxDownloads"`
	CanView      *bool   `json:"canView"`
	CanDownload  *bool   `json:"canDownload"`
	CanUpload    *bool   `json:"canUpload"`
	Disabled     *bool   `json:"disabled"`
	Title        *string `json:"title"`
	Message      *string `json:"message"`
	Note         *string `json:"note"`
	UploadMaxMB  *int64  `json:"uploadMaxMB"`
	UploadExts   *string `json:"uploadExts"`
	UploadNamed  *bool   `json:"uploadNamed"`
	Notify       *int    `json:"notify"`
	AllowedNets  *string `json:"allowedNets"`
	ViewMode     *string `json:"viewMode"`
	Access       *string `json:"access"`
	CanComment   *bool   `json:"canComment"`
	Latest       *bool   `json:"latest"`
	Watermark    *bool   `json:"watermark"`
	Collection   *string `json:"collection"` // create only: share a collection instead of place + path
	Sent         bool    `json:"sent"`       // create only: made by Send files, for a folder under Sent
	// Create only: people to email the link to.
	Invite *inviteReq `json:"invite"`
}

type inviteReq struct {
	Emails  []string `json:"emails"`
	Message string   `json:"message"` // personal note in the email
	Request bool     `json:"request"` // phrase it as asking for files
}

var extRe = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// applyShare validates req against the settings and copies it onto sh.
// canWrite is whether the owner may write where the share points.
func (s *Server) applyShare(w http.ResponseWriter, sh *store.Share, req shareReq, canWrite bool) bool {
	st := s.cur()
	bad := func(msg string) bool { badRequest(w, msg); return false }
	if req.Password != nil {
		if *req.Password == "" {
			sh.PasswordHash = ""
		} else {
			if len(*req.Password) < 10 || len(*req.Password) > 1024 {
				return bad("link passwords must be at least 10 characters")
			}
			h, err := security.HashPassword(*req.Password)
			if err != nil {
				return bad("could not set password")
			}
			sh.PasswordHash = h
		}
	}
	if st.ShareRequirePassword && sh.PasswordHash == "" {
		return bad("your administrator requires a password on every link")
	}
	if req.ExpiresAt != nil {
		sh.ExpiresAt = max(0, *req.ExpiresAt)
	}
	if st.ShareMaxExpiryDays > 0 {
		limit := time.Now().AddDate(0, 0, st.ShareMaxExpiryDays).Unix() + 3600
		if sh.ExpiresAt == 0 || sh.ExpiresAt > limit {
			return bad(fmt.Sprintf("links must expire within %d days", st.ShareMaxExpiryDays))
		}
	}
	if req.MaxDownloads != nil {
		sh.MaxDownloads = max(0, *req.MaxDownloads)
	}
	if req.CanView != nil {
		sh.CanView = *req.CanView
	}
	if req.CanDownload != nil {
		sh.CanDownload = *req.CanDownload
	}
	if req.CanUpload != nil {
		sh.CanUpload = *req.CanUpload
	}
	if req.Disabled != nil {
		sh.Disabled = *req.Disabled
	}
	if sh.CanUpload && (!sh.IsDir || !canWrite) {
		return bad("uploads can only be allowed on folders you can write to")
	}
	if !sh.CanView && !sh.CanDownload && !sh.CanUpload {
		return bad("a link must allow viewing, downloading or uploading")
	}
	text := func(dst *string, src *string, n int, what string) bool {
		if src == nil {
			return true
		}
		v := strings.TrimSpace(*src)
		if len(v) > n {
			badRequest(w, fmt.Sprintf("the %s can be at most %d characters", what, n))
			return false
		}
		*dst = v
		return true
	}
	if !text(&sh.Title, req.Title, 120, "title") || !text(&sh.Message, req.Message, 2000, "message") || !text(&sh.Note, req.Note, 500, "note") {
		return false
	}
	if req.UploadMaxMB != nil {
		sh.UploadMaxMB = max(0, *req.UploadMaxMB)
	}
	if req.UploadExts != nil {
		var exts []string
		for _, e := range strings.FieldsFunc(strings.ToLower(*req.UploadExts), func(r rune) bool { return r == ',' || r == ' ' }) {
			e = strings.TrimPrefix(e, ".")
			if !extRe.MatchString(e) {
				return bad("file types are extensions like pdf, mov, zip")
			}
			exts = append(exts, e)
		}
		if len(exts) > 50 {
			return bad("list at most 50 file types")
		}
		sh.UploadExts = strings.Join(exts, ",")
	}
	if req.UploadNamed != nil {
		sh.UploadNamed = *req.UploadNamed
	}
	if req.Notify != nil {
		sh.Notify = *req.Notify & (store.NotifyOpen | store.NotifyDownload | store.NotifyUpload)
	}
	if req.AllowedNets != nil {
		var nets []string
		for _, n := range strings.FieldsFunc(*req.AllowedNets, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
			if _, err := netip.ParsePrefix(n); err != nil {
				if _, err := netip.ParseAddr(n); err != nil {
					return bad(fmt.Sprintf("%q is not an IP address or network like 203.0.113.0/24", n))
				}
			}
			nets = append(nets, n)
		}
		sh.AllowedNets = strings.Join(nets, ",")
	}
	if req.ViewMode != nil {
		if *req.ViewMode != "list" && *req.ViewMode != "grid" && *req.ViewMode != "gallery" {
			return bad("view mode must be list, grid or gallery")
		}
		sh.ViewMode = *req.ViewMode
	}
	if req.Access != nil {
		if *req.Access != "link" && *req.Access != "email" && *req.Access != "invited" {
			return bad("access must be link, email or invited")
		}
		sh.Access = *req.Access
	}
	if sh.Access != "link" && !s.mailEnabled() {
		return bad("email confirmation needs outgoing email; an administrator can set it up in App settings")
	}
	if req.CanComment != nil {
		sh.CanComment = *req.CanComment
	}
	if req.Latest != nil {
		sh.Latest = *req.Latest && !sh.IsDir
	}
	if req.Watermark != nil {
		sh.Watermark = *req.Watermark
	}
	if sh.CollectionID != "" && sh.CanUpload {
		return bad("collections cannot accept uploads")
	}
	return true
}

func (s *Server) handleShareCreate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req shareReq
	if !decodeJSON(w, r, &req) {
		return
	}
	token := security.RandomToken(32)
	sh := &store.Share{
		ID: security.RandomToken(9), TokenHash: security.HashToken(token), TokenEnc: s.keys.Encrypt([]byte(token)),
		OwnerID: u.ID, OwnerName: u.Username, CanView: true, CanDownload: true, ViewMode: "list", Access: "link",
	}
	var ps []*access
	canWrite := false
	if req.Collection != nil {
		c, err := s.store.CollectionByID(r.Context(), *req.Collection)
		if err != nil || c.OwnerID != u.ID {
			writeErr(w, http.StatusNotFound, "not_found", "collection not found")
			return
		}
		if !u.CanShare && !u.IsAdmin() {
			forbidden(w, "you are not allowed to create links")
			return
		}
		sh.CollectionID, sh.IsDir, sh.DriveID = c.ID, true, ""
		ps, _ = s.placesFor(r.Context(), u)
	} else {
		if req.Place == nil || req.Path == nil {
			badRequest(w, "place and path are required")
			return
		}
		a, ok := s.place(w, r, u, *req.Place)
		if !ok {
			return
		}
		if !a.CanShare {
			forbidden(w, "you are not allowed to create links here")
			return
		}
		p, err := a.resolve(*req.Path)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if p == "." {
			badRequest(w, "a whole drive cannot be shared by link; pick a folder")
			return
		}
		e, err := a.d().Stat(r.Context(), p)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		sh.DriveID, sh.Path, sh.IsDir = a.DriveID, p, e.IsDir
		ps, canWrite = []*access{a}, a.CanWrite
	}
	if req.ExpiresAt == nil && s.cur().ShareDefaultExpiryDays > 0 {
		exp := time.Now().AddDate(0, 0, s.cur().ShareDefaultExpiryDays).Unix()
		req.ExpiresAt = &exp
	}
	if !s.applyShare(w, sh, req, canWrite) {
		return
	}
	var emails []string
	if req.Invite != nil && len(req.Invite.Emails) > 0 {
		var ok bool
		if emails, ok = s.validInvites(w, req.Invite); !ok {
			return
		}
	}
	if err := s.store.CreateShare(r.Context(), sh); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, sh.ID, "share.create", sh.DriveID+":"+sh.Path, shareSummary(sh))
	// Only someone who could delete the folder anyway gets it pruned later.
	if req.Sent && sh.CollectionID == "" && len(ps) == 1 && ps[0].CanDelete {
		s.markSent(r.Context(), sh)
	}
	if len(emails) > 0 {
		s.invite(r, u, sh, token, emails, req.Invite)
	}
	writeJSON(w, http.StatusCreated, s.shareOut(r, ps, sh))
}

// Recipients and invitations.

func (s *Server) validInvites(w http.ResponseWriter, req *inviteReq) ([]string, bool) {
	if !s.mailEnabled() {
		writeErr(w, http.StatusServiceUnavailable, "mail_off", "email is not set up; an administrator can add it in App settings")
		return nil, false
	}
	if len(req.Emails) > 50 {
		badRequest(w, "invite at most 50 people at a time")
		return nil, false
	}
	if len(req.Message) > 2000 {
		badRequest(w, "the message can be at most 2000 characters")
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range req.Emails {
		addr, err := mail.ParseAddress(e)
		if err != nil {
			badRequest(w, err.Error())
			return nil, false
		}
		if !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	return out, true
}

// invite records each address as a recipient and emails them the link.
func (s *Server) invite(r *http.Request, u *store.User, sh *store.Share, token string, emails []string, req *inviteReq) {
	name := sh.Title
	if name == "" {
		if sh.CollectionID != "" {
			if c, err := s.store.CollectionByID(r.Context(), sh.CollectionID); err == nil {
				name = c.Name
			}
		} else {
			name = path.Base(sh.Path)
		}
	}
	link := s.baseURL(r) + "/s/" + token
	for _, e := range emails {
		rc, err := s.store.UpsertRecipient(r.Context(), security.RandomToken(9), sh.ID, e, "", true)
		if err != nil {
			s.log.Error("add recipient", "err", err)
			continue
		}
		url := link
		if sh.Access != "link" {
			url += "?invite=" + s.inviteToken(sh, rc)
		}
		msg := mail.Message{To: e, ReplyTo: u.Email}
		d := s.emailBase(r)
		d.Link, d.Quote, d.QuoteBy = url, strings.TrimSpace(req.Message), u.Username
		if d.Quote == "" {
			d.QuoteBy = ""
		}
		if req.Request {
			msg.Subject = u.Username + " is asking you to upload files"
			d.Heading = u.Username + " is asking you for files"
			d.Lines = []string{"Upload them to “" + name + "” with the button below. You don't need an account."}
			d.Button = "Upload files"
		} else {
			msg.Subject = u.Username + " shared “" + name + "” with you"
			d.Heading = u.Username + " shared “" + name + "” with you"
			d.Lines = []string{"You can open it in your browser. You don't need an account."}
			d.Button = "Open"
			if sh.Latest {
				d.Lines = append(d.Lines, "This link always shows the newest version.")
			}
		}
		if sh.ExpiresAt != 0 {
			d.Footnote = "The link works until " + time.Unix(sh.ExpiresAt, 0).Format("2 January 2006") + "."
		}
		if sh.PasswordHash != "" {
			d.Footnote = strings.TrimSpace(d.Footnote + " It is password protected; " + u.Username + " will give you the password.")
		}
		renderEmail(&msg, d)
		s.sendMail(msg, "invite:"+sh.ID)
	}
	s.audit(r, u, sh.ID, "share.invite", sh.DriveID+":"+sh.Path, strings.Join(emails, ", "))
}

func (s *Server) handleShareInvite(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req inviteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	sh, ok := s.ownedShare(w, r, u)
	if !ok {
		return
	}
	emails, ok := s.validInvites(w, &req)
	if !ok {
		return
	}
	token, err := s.keys.Decrypt(sh.TokenEnc)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.invite(r, u, sh, string(token), emails, &req)
	ps, _ := s.placesFor(r.Context(), u)
	writeJSON(w, http.StatusOK, s.shareOut(r, ps, sh))
}

func (s *Server) handleRecipientUpdate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Revoked *bool `json:"revoked"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sh, ok := s.ownedShare(w, r, u)
	if !ok {
		return
	}
	rc, err := s.store.RecipientByID(r.Context(), sh.ID, r.PathValue("rid"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "recipient not found")
		return
	}
	if req.Revoked != nil {
		if err := s.store.SetRecipientRevoked(r.Context(), sh.ID, rc.ID, *req.Revoked); err != nil {
			s.fail(w, r, err)
			return
		}
		action := "share.recipient_restored"
		if *req.Revoked {
			action = "share.recipient_revoked"
		}
		s.audit(r, u, sh.ID, action, sh.DriveID+":"+sh.Path, rc.Email)
	}
	ps, _ := s.placesFor(r.Context(), u)
	writeJSON(w, http.StatusOK, s.shareOut(r, ps, sh))
}

func (s *Server) handleRecipientDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	sh, ok := s.ownedShare(w, r, u)
	if !ok {
		return
	}
	if err := s.store.DeleteRecipient(r.Context(), sh.ID, r.PathValue("rid")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func shareSummary(sh *store.Share) string {
	var parts []string
	if sh.PasswordHash != "" {
		parts = append(parts, "password")
	}
	if sh.ExpiresAt != 0 {
		parts = append(parts, "expires "+time.Unix(sh.ExpiresAt, 0).UTC().Format(time.DateOnly))
	}
	if sh.CanUpload {
		parts = append(parts, "uploads")
	}
	if !sh.CanDownload {
		parts = append(parts, "no downloads")
	}
	if !sh.CanView {
		parts = append(parts, "no viewing")
	}
	if sh.AllowedNets != "" {
		parts = append(parts, "networks "+sh.AllowedNets)
	}
	if sh.Disabled {
		parts = append(parts, "paused")
	}
	return strings.Join(parts, ", ")
}

func (s *Server) ownedShare(w http.ResponseWriter, r *http.Request, u *store.User) (*store.Share, bool) {
	sh, err := s.store.ShareByID(r.Context(), r.PathValue("id"))
	if err != nil || (sh.OwnerID != u.ID && !u.IsAdmin()) {
		writeErr(w, http.StatusNotFound, "not_found", "link not found")
		return nil, false
	}
	return sh, true
}

func (s *Server) handleShareUpdate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req shareReq
	if !decodeJSON(w, r, &req) {
		return
	}
	sh, ok := s.ownedShare(w, r, u)
	if !ok {
		return
	}
	if req.Path != nil || req.Place != nil {
		badRequest(w, "the shared item cannot be changed; create a new link")
		return
	}
	owner, err := s.store.UserByID(r.Context(), sh.OwnerID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	canWrite := sh.CollectionID == "" && s.canReach(r.Context(), owner, sh.DriveID, sh.Path, false, true, 3)
	if !s.applyShare(w, sh, req, canWrite) {
		return
	}
	if err := s.store.UpdateShare(r.Context(), sh); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, sh.ID, "share.update", sh.DriveID+":"+sh.Path, shareSummary(sh))
	ps, _ := s.placesFor(r.Context(), u)
	writeJSON(w, http.StatusOK, s.shareOut(r, ps, sh))
}

func (s *Server) handleShareDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	sh, ok := s.ownedShare(w, r, u)
	if !ok {
		return
	}
	if err := s.store.DeleteShare(r.Context(), sh.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, sh.ID, "share.revoke", sh.DriveID+":"+sh.Path, "")
	w.WriteHeader(http.StatusNoContent)
}
