package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net/http"
	"net/netip"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/mail"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// Public share access.
//
// A link points at one file or folder, or at a collection of items from
// anywhere. Paths sent by recipients are relative to the link: "/" is the
// shared folder, or for a collection the list of its items, and
// "/<item>/sub/file" is inside one item.

type shareItem struct {
	Name  string // top-level name inside the link
	m     *mounted
	Path  string // on the drive
	IsDir bool
}

type shareCtx struct {
	sh       *store.Share
	owner    *store.User
	token    string
	canWrite bool
	items    []*shareItem
	coll     *store.Collection
	viewer   *store.Recipient // the verified visitor, when the link asks for an email
}

func (sc *shareCtx) isColl() bool { return sc.sh.CollectionID != "" }

var errShareRoot = errors.New("the collection itself is not a file")

// resolve maps a link-relative path to an item and a drive path. Hidden
// files and folders inside the link (dotfiles, desktop.ini…) stay out of
// reach, as they are left out of its listings.
func (sc *shareCtx) resolve(p string) (*shareItem, string, error) {
	c, err := files.Clean(p)
	if err != nil {
		return nil, "", err
	}
	if !sc.isColl() {
		it := sc.items[0]
		if !it.IsDir {
			return it, it.Path, nil
		}
		if hiddenIn(c) {
			return nil, "", fs.ErrNotExist
		}
		abs, err := it.confine(p)
		return it, abs, err
	}
	if c == "." {
		return nil, "", errShareRoot
	}
	first, rest, _ := strings.Cut(c, "/")
	for _, it := range sc.items {
		if it.Name != first {
			continue
		}
		if rest == "" {
			return it, it.Path, nil
		}
		if !it.IsDir || hiddenIn(rest) {
			return nil, "", fs.ErrNotExist
		}
		abs, err := it.confine(rest)
		return it, abs, err
	}
	return nil, "", fs.ErrNotExist
}

// hiddenIn reports whether any part of the cleaned relative path c is a
// hidden name.
func hiddenIn(c string) bool {
	if c == "." {
		return false
	}
	for _, seg := range strings.Split(c, "/") {
		if files.Hidden(seg, true) {
			return true
		}
	}
	return false
}

// confine joins p to the item's folder; symlinks must not lead out of it.
func (it *shareItem) confine(p string) (string, error) {
	return files.Confine(it.m.d, it.Path, p)
}

// visible drops listed symlinks that lead out of the item.
func (it *shareItem) visible(es []files.Entry) []files.Entry {
	out := es[:0]
	for _, e := range es {
		if e.Symlink {
			if _, err := it.confine(files.Rel(it.Path, e.Path)); err != nil {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// relOut is the link-relative form of drive path abs inside item it.
func (sc *shareCtx) relOut(it *shareItem, abs string) string {
	if !sc.isColl() {
		if !it.IsDir {
			return "/"
		}
		return files.Rel(it.Path, abs)
	}
	r := files.Rel(it.Path, abs)
	if r == "/" {
		return "/" + it.Name
	}
	return "/" + it.Name + r
}

type shareHandlerFunc func(http.ResponseWriter, *http.Request, *shareCtx)

func netsAllow(nets string, ip netip.Addr) bool {
	if nets == "" {
		return true
	}
	for _, n := range strings.Split(nets, ",") {
		if p, err := netip.ParsePrefix(n); err == nil && p.Contains(ip) {
			return true
		}
		if a, err := netip.ParseAddr(n); err == nil && a.Unmap() == ip {
			return true
		}
	}
	return false
}

// untrustedProxy reports a request relayed by a proxy that this listener
// doesn't trust, such as a tunnel left out of COFFER_TRUSTED_PROXIES. The
// address we see is then the proxy's, often a private one, so it can't
// satisfy a network allowlist.
func (s *Server) untrustedProxy(r *http.Request) bool {
	v, ok := r.Context().Value(untrustedKey).(bool)
	return v || !ok
}

// shareHandler resolves the token in the URL. Unknown, revoked or orphaned
// links all look the same: not found.
func (s *Server) shareHandler(h shareHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mon.ShareHits.Add(1)
		token := r.PathValue("token")
		miss := func() {
			if !s.shareMiss.Allow(ipString(r)) {
				w.Header().Set("Retry-After", "60")
				writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
			writeErr(w, http.StatusNotFound, "share_not_found", "this link does not exist or is no longer available")
		}
		if len(token) != 43 {
			miss()
			return
		}
		ctx := r.Context()
		sh, err := s.store.ShareByToken(ctx, security.HashToken(token))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.fail(w, r, err)
				return
			}
			miss()
			return
		}
		if sh.Disabled {
			writeErr(w, http.StatusGone, "share_paused", "this link has been paused by its owner")
			return
		}
		if !sh.Active(time.Now().Unix()) {
			writeErr(w, http.StatusGone, "share_expired", "this link has expired")
			return
		}
		if sh.AllowedNets != "" && (s.untrustedProxy(r) || !netsAllow(sh.AllowedNets, clientIP(r))) {
			writeErr(w, http.StatusForbidden, "share_network", "this link cannot be opened from your network")
			return
		}
		owner, err := s.store.UserByID(ctx, sh.OwnerID)
		if err != nil || owner.Disabled {
			miss()
			return
		}
		sc := &shareCtx{sh: sh, owner: owner, token: token}
		if sh.CollectionID != "" {
			if !s.loadCollectionItems(ctx, sc) {
				miss()
				return
			}
		} else {
			if !s.canReach(ctx, owner, sh.DriveID, sh.Path, true, false, 3) {
				miss()
				return
			}
			m, err := s.drive(sh.DriveID)
			if err != nil {
				writeErr(w, http.StatusServiceUnavailable, "drive_offline", "this link is temporarily unavailable")
				return
			}
			it := &shareItem{Name: path.Base(sh.Path), m: m, Path: sh.Path, IsDir: sh.IsDir}
			if sh.Latest && !sh.IsDir {
				if e, err := files.LatestVersion(ctx, m.d, sh.Path); err == nil {
					if p, err := files.Join(files.Parent(sh.Path), e.Name); err == nil && s.canReach(ctx, owner, sh.DriveID, p, true, false, 3) {
						it.Path, it.Name = p, e.Name
					}
				}
			}
			sc.items = []*shareItem{it}
			sc.canWrite = sh.IsDir && s.canReach(ctx, owner, sh.DriveID, sh.Path, false, true, 3)
		}
		if !sc.canWrite {
			sc.sh.CanUpload = false
		}
		sc.viewer = s.shareViewer(r, sc)
		h(w, r, sc)
	}
}

// loadCollectionItems fills in the items the owner can still reach, giving
// each a unique top-level name.
func (s *Server) loadCollectionItems(ctx context.Context, sc *shareCtx) bool {
	c, err := s.store.CollectionByID(ctx, sc.sh.CollectionID)
	if err != nil || c.OwnerID != sc.owner.ID {
		return false
	}
	sc.coll = c
	used := map[string]int{}
	for _, ci := range c.Items {
		m, err := s.drive(ci.DriveID)
		if err != nil || !s.canReach(ctx, sc.owner, ci.DriveID, ci.Path, true, false, 3) {
			continue
		}
		name := path.Base(ci.Path)
		if n := used[strings.ToLower(name)]; n > 0 {
			ext := path.Ext(name)
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n+1, ext)
		}
		used[strings.ToLower(path.Base(ci.Path))]++
		sc.items = append(sc.items, &shareItem{Name: name, m: m, Path: ci.Path, IsDir: ci.IsDir})
	}
	return true
}

// Password and email gates.

func shareCookieName(sh *store.Share) string  { return "coffer_share_" + sh.ID }
func viewerCookieName(sh *store.Share) string { return "coffer_sv_" + sh.ID }

func (s *Server) unlocked(r *http.Request, sh *store.Share) bool {
	if sh.PasswordHash == "" {
		return true
	}
	c, err := r.Cookie(shareCookieName(sh))
	if err != nil {
		return false
	}
	exp, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	t, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > t {
		return false
	}
	return s.keys.Verify(sig, "share", sh.ID, exp, sh.PasswordHash)
}

// shareViewer returns the recipient whose verification cookie the request
// carries, or nil.
func (s *Server) shareViewer(r *http.Request, sc *shareCtx) *store.Recipient {
	c, err := r.Cookie(viewerCookieName(sc.sh))
	if err != nil {
		return nil
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return nil
	}
	rid, exp, sig := parts[0], parts[1], parts[2]
	t, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > t || !s.keys.Verify(sig, "share-viewer", sc.sh.ID, rid, exp) {
		return nil
	}
	rc, err := s.store.RecipientByID(r.Context(), sc.sh.ID, rid)
	if err != nil {
		return nil
	}
	return rc
}

func (s *Server) setViewerCookie(w http.ResponseWriter, r *http.Request, sc *shareCtx, rc *store.Recipient) {
	life := 30 * 24 * time.Hour
	if sc.sh.ExpiresAt != 0 {
		life = min(life, time.Until(time.Unix(sc.sh.ExpiresAt, 0)))
	}
	exp := strconv.FormatInt(time.Now().Add(life).Unix(), 10)
	http.SetCookie(w, &http.Cookie{
		Name: viewerCookieName(sc.sh), Value: rc.ID + "." + exp + "." + s.keys.Sign("share-viewer", sc.sh.ID, rc.ID, exp),
		Path: "/api/s/" + sc.token, MaxAge: int(life.Seconds()), HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteLaxMode,
	})
}

func (sc *shareCtx) needsEmail() bool { return sc.sh.Access == "email" || sc.sh.Access == "invited" }

// gate reports why the visitor cannot see the content yet, or "".
func (s *Server) gate(r *http.Request, sc *shareCtx) string {
	if sc.needsEmail() {
		if sc.viewer == nil {
			return "share_email"
		}
		// Someone who confirmed their own address while the link was open to
		// any email loses access when it becomes invite-only.
		if sc.viewer.Revoked || (sc.sh.Access == "invited" && sc.viewer.InvitedAt == 0) {
			return "share_revoked"
		}
	}
	if !s.unlocked(r, sc.sh) {
		return "share_locked"
	}
	return ""
}

func (s *Server) requireUnlocked(w http.ResponseWriter, r *http.Request, sc *shareCtx) bool {
	switch s.gate(r, sc) {
	case "":
		return true
	case "share_email":
		writeErr(w, http.StatusUnauthorized, "share_email", "confirm your email address to open this link")
	case "share_revoked":
		writeErr(w, http.StatusForbidden, "share_revoked", "your access to this link has been removed")
	default:
		writeErr(w, http.StatusUnauthorized, "share_locked", "this link is password protected")
	}
	return false
}

// who describes the visitor for audit entries and notifications.
func (sc *shareCtx) who(r *http.Request) string {
	if sc.viewer != nil {
		if sc.viewer.Name != "" {
			return sc.viewer.Name + " (" + sc.viewer.Email + ")"
		}
		return sc.viewer.Email
	}
	return "someone at " + ipString(r)
}

func (s *Server) shareAudit(r *http.Request, sc *shareCtx, action, p, detail string) {
	e := store.AuditEntry{ShareID: sc.sh.ID, IP: ipString(r), Action: action, Path: p, Detail: detail}
	if sc.viewer != nil {
		e.Username = sc.viewer.Email
	}
	if err := s.store.Audit(context.WithoutCancel(r.Context()), e); err != nil {
		s.log.Error("audit write failed", "err", err)
	}
}

func (s *Server) shareName(sc *shareCtx) string {
	if sc.sh.Title != "" {
		return sc.sh.Title
	}
	if sc.coll != nil {
		return sc.coll.Name
	}
	return sc.items[0].Name
}

func (s *Server) uploadLimitMB(sh *store.Share) int64 {
	g := s.cur().ShareUploadMaxMB
	switch {
	case sh.UploadMaxMB == 0:
		return g
	case g == 0:
		return sh.UploadMaxMB
	default:
		return min(g, sh.UploadMaxMB)
	}
}

func (s *Server) handleShareMeta(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	sh := sc.sh
	st := s.cur()
	out := map[string]any{
		"title": sh.Title, "message": "", "needsPassword": sh.PasswordHash != "", "unlocked": false,
		"canView": sh.CanView, "canDownload": sh.CanDownload, "canUpload": sh.CanUpload, "canComment": sh.CanComment,
		"expiresAt": sh.ExpiresAt, "uploadMaxMB": s.uploadLimitMB(sh), "uploadExts": sh.Exts(),
		"uploadNamed": sh.UploadNamed, "viewMode": sh.ViewMode, "access": sh.Access, "watermark": sh.Watermark,
		"collection": sc.isColl(), "from": sc.owner.Username, "mailReady": s.mailEnabled(),
		"footer": st.ShareFooter, "website": st.ShareWebsite, "hidePowered": st.ShareHidePowered,
		// legacy names used by older clients
		"allowDownload": sh.CanDownload, "allowUpload": sh.CanUpload,
	}
	if sc.isColl() {
		out["name"], out["isDir"] = sc.coll.Name, true
	} else {
		it := sc.items[0]
		e, err := it.m.d.Stat(r.Context(), it.Path)
		if err != nil {
			writeErr(w, http.StatusNotFound, "share_not_found", "this link does not exist or is no longer available")
			return
		}
		out["name"], out["isDir"] = e.Name, e.IsDir
		if s.gate(r, sc) == "" {
			out["size"], out["modTime"], out["mime"], out["kind"] = e.Size, e.ModTime, e.Mime, e.Kind
			if e.IsDir && !sh.CanView {
				// A drop box doesn't say how much is already in the folder.
				delete(out, "size")
			} else if e.IsDir {
				if fsz, ok := s.index.Size(r.Context(), sh.DriveID, it.Path); ok {
					out["size"], out["items"] = fsz.Size, fsz.Items
				}
			}
			if _, v, ok := files.VersionOf(e.Name); ok && sh.Latest {
				out["version"] = v
			}
		}
	}
	gate := s.gate(r, sc)
	if sc.viewer != nil {
		out["viewer"] = map[string]any{"email": sc.viewer.Email, "name": sc.viewer.Name, "revoked": sc.viewer.Revoked || gate == "share_revoked"}
	}
	out["gate"] = gate
	if gate == "" {
		out["unlocked"] = true
		out["message"] = sh.Message
		if sc.isColl() {
			out["description"] = sc.coll.Description
		}
		if sc.viewer != nil {
			_ = s.store.TouchRecipient(context.WithoutCancel(r.Context()), sc.viewer.ID, false)
		}
		s.shareEvent(sc, store.NotifyOpen, "Your link “"+s.shareName(sc)+"” was opened", "by "+sc.who(r))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleShareAuth(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sh := sc.sh
	if sh.PasswordHash == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	key := "share:" + sh.ID + ":" + ipString(r)
	if wait := s.backoff.Wait(key); wait > 0 || !s.shareAuth.Allow(sh.ID) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "locked", "too many attempts, try again later")
		return
	}
	if ok, _, _ := security.VerifyPassword(req.Password, sh.PasswordHash); !ok {
		s.backoff.Fail(key)
		s.shareAudit(r, sc, "share.password_failed", sh.DriveID+":"+sh.Path, "")
		writeErr(w, http.StatusUnauthorized, "invalid_password", "incorrect password")
		return
	}
	s.backoff.Reset(key)
	exp := strconv.FormatInt(time.Now().Add(12*time.Hour).Unix(), 10)
	http.SetCookie(w, &http.Cookie{
		Name: shareCookieName(sh), Value: exp + "." + s.keys.Sign("share", sh.ID, exp, sh.PasswordHash),
		Path: "/api/s/" + sc.token, MaxAge: 12 * 3600, HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// Email verification. A visitor enters their address, gets a six-digit
// code, and is remembered on that browser. Invitation emails carry a
// signed token that skips the code.

const codeLife = 15 * time.Minute

func sixDigits() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}

func (s *Server) handleShareVerifyStart(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !sc.needsEmail() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	email, err := mail.ParseAddress(req.Email)
	if err != nil {
		badRequest(w, "enter a valid email address")
		return
	}
	if !s.mailEnabled() {
		writeErr(w, http.StatusServiceUnavailable, "mail_off", "this link needs email confirmation, but email is not set up on this server")
		return
	}
	ip := ipString(r)
	if !s.shareAuth.Allow(sc.sh.ID+":code") || s.backoff.Wait("code:"+ip) > 0 {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests, try again in a minute")
		return
	}
	s.backoff.Fail("code:" + ip) // spaces out code requests from one address
	if c, err := s.store.ShareCode(r.Context(), sc.sh.ID, email); err == nil && time.Since(time.Unix(c.SentAt, 0)) < 30*time.Second {
		w.WriteHeader(http.StatusNoContent) // a code was just sent
		return
	}
	if !codeAllowed(sc.sh.ID, email) {
		w.Header().Set("Retry-After", "600")
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many codes were sent, try again later")
		return
	}
	if sc.sh.Access == "invited" {
		// Only invited people get a code. The reply is the same either way,
		// so the list of recipients can't be probed.
		rc, err := s.store.RecipientByEmail(r.Context(), sc.sh.ID, email)
		if err != nil || rc.Revoked {
			s.shareAudit(r, sc, "share.code_refused", sc.sh.DriveID+":"+sc.sh.Path, email)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	code := sixDigits()
	if err := s.store.PutShareCode(r.Context(), sc.sh.ID, email, s.keys.Sign("share-code", sc.sh.ID, email, code), time.Now().Add(codeLife).Unix()); err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 80 {
		name = name[:80]
	}
	msg := mail.Message{To: email, ToName: name, Subject: code + " is your code for “" + s.shareName(sc) + "”"}
	var base *http.Request
	if s.cfg.PublicURL != "" {
		base = r // otherwise the logo URL would come from the visitor's Host header
	}
	d := s.emailBase(base)
	d.Heading = "Your code"
	d.Lines = []string{"Enter this code to open “" + s.shareName(sc) + "”, shared by " + sc.owner.Username + "."}
	d.Code = code
	d.Footnote = "It expires in 15 minutes. If you didn't ask for it, you can ignore this email."
	renderEmail(&msg, d)
	codeSent(sc.sh.ID)
	s.sendMail(msg, "share-code:"+sc.sh.ID)
	s.shareAudit(r, sc, "share.code_sent", sc.sh.DriveID+":"+sc.sh.Path, email)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleShareVerifyCheck(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Code  string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email, err := mail.ParseAddress(req.Email)
	if err != nil || !sc.needsEmail() {
		badRequest(w, "enter a valid email address")
		return
	}
	// Failures back off per address as well as per IP, so guesses spread
	// over many IPs and fresh codes stay slow.
	key, addrKey := "verify:"+sc.sh.ID+":"+ipString(r), "verify:"+sc.sh.ID+":"+email
	if s.backoff.Wait(key) > 0 || s.backoff.Wait(addrKey) > 0 {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "locked", "too many attempts, try again later")
		return
	}
	c, err := s.store.TryShareCode(r.Context(), sc.sh.ID, email, 5)
	code := strings.ReplaceAll(strings.TrimSpace(req.Code), " ", "")
	if err != nil || !s.keys.Verify(c.Hash, "share-code", sc.sh.ID, email, code) {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
		s.backoff.Fail(key)
		s.backoff.Fail(addrKey)
		writeErr(w, http.StatusUnauthorized, "invalid_code", "that code is wrong or has expired")
		return
	}
	s.backoff.Reset(key)
	s.backoff.Reset(addrKey)
	_ = s.store.DeleteShareCode(r.Context(), sc.sh.ID, email)
	name := strings.TrimSpace(req.Name)
	if len(name) > 80 {
		name = name[:80]
	}
	rc, err := s.store.UpsertRecipient(r.Context(), security.RandomToken(9), sc.sh.ID, email, name, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if rc.Revoked || (sc.sh.Access == "invited" && rc.InvitedAt == 0) {
		writeErr(w, http.StatusForbidden, "share_revoked", "this email address doesn't have access to the link")
		return
	}
	s.setViewerCookie(w, r, sc, rc)
	s.shareAudit(r, sc, "share.verified", sc.sh.DriveID+":"+sc.sh.Path, email)
	w.WriteHeader(http.StatusNoContent)
}

// inviteToken is put in invitation emails so the recipient skips the code.
func (s *Server) inviteToken(sh *store.Share, rc *store.Recipient) string {
	exp := strconv.FormatInt(time.Now().Add(60*24*time.Hour).Unix(), 10)
	return rc.ID + "." + exp + "." + s.keys.Sign("share-invite", sh.ID, rc.ID, exp)
}

func (s *Server) handleShareVerifyInvite(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	var req struct {
		Invite string `json:"invite"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	parts := strings.Split(req.Invite, ".")
	bad := func() {
		writeErr(w, http.StatusUnauthorized, "invalid_invite", "this invitation link is no longer valid")
	}
	if len(parts) != 3 {
		bad()
		return
	}
	t, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > t || !s.keys.Verify(parts[2], "share-invite", sc.sh.ID, parts[0], parts[1]) {
		bad()
		return
	}
	rc, err := s.store.RecipientByID(r.Context(), sc.sh.ID, parts[0])
	if err != nil || rc.Revoked {
		writeErr(w, http.StatusForbidden, "share_revoked", "your access to this link has been removed")
		return
	}
	s.setViewerCookie(w, r, sc, rc)
	s.shareAudit(r, sc, "share.invite_opened", sc.sh.DriveID+":"+sc.sh.Path, rc.Email)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleShareForget(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	http.SetCookie(w, &http.Cookie{Name: viewerCookieName(sc.sh), Value: "", Path: "/api/s/" + sc.token, MaxAge: -1, HttpOnly: true, Secure: isSecure(r)})
	w.WriteHeader(http.StatusNoContent)
}

// Browsing and downloads.

func (s *Server) handleShareList(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	if !s.requireUnlocked(w, r, sc) {
		return
	}
	if !sc.sh.CanView || (!sc.isColl() && !sc.sh.IsDir) {
		forbidden(w, "browsing is not available for this link")
		return
	}
	ctx := r.Context()
	reqPath := r.URL.Query().Get("path")
	if sc.isColl() {
		if c, _ := files.Clean(reqPath); c == "." {
			es := make([]files.Entry, 0, len(sc.items))
			for _, it := range sc.items {
				e, err := it.m.d.Stat(ctx, it.Path)
				if err != nil {
					continue
				}
				if e.IsDir {
					e.Size = -1
					if fsz, ok := s.index.Size(ctx, it.m.info.ID, it.Path); ok {
						e.Size, e.Items = fsz.Size, fsz.Items
					}
				}
				e.Name, e.Path = it.Name, "/"+it.Name
				es = append(es, e)
			}
			writeJSON(w, http.StatusOK, map[string]any{"path": "/", "entries": s.decorateShare(ctx, sc, nil, ".", es)})
			return
		}
	}
	it, p, err := sc.resolve(reqPath)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	es, err := it.m.d.List(ctx, p, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	es = it.visible(es)
	sizes, _ := s.index.FolderSizes(ctx, it.m.info.ID, p)
	for i := range es {
		if es[i].IsDir {
			if fsz, ok := sizes[es[i].Path]; ok {
				es[i].Size, es[i].Items = fsz.Size, fsz.Items
			} else {
				es[i].Size = -1
			}
		}
	}
	_ = s.store.TouchShare(ctx, sc.sh.ID)
	writeJSON(w, http.StatusOK, map[string]any{"path": sc.relOut(it, p), "entries": s.decorateShare(ctx, sc, it, p, es)})
}

// decorateShare maps drive paths into the link and adds review status when
// the link allows comments.
func (s *Server) decorateShare(ctx context.Context, sc *shareCtx, it *shareItem, dir string, es []files.Entry) []shareEntry {
	var reviews map[string]*store.ReviewSummary
	if sc.sh.CanComment && it != nil {
		reviews, _ = s.store.PublicReviewsIn(ctx, it.m.info.ID, dir, sc.sh.ID)
	}
	out := make([]shareEntry, len(es))
	for i, e := range es {
		out[i].Entry = e
		if it != nil {
			if rv := reviews[e.Path]; rv != nil {
				rv.By = publicAuthor(rv.By)
				out[i].Review = rv
			}
			out[i].Path = sc.relOut(it, e.Path)
		}
	}
	return out
}

type shareEntry struct {
	files.Entry
	Review *store.ReviewSummary `json:"review,omitempty"`
}

// countsAsDownload is true for whole-file requests and the first range of a
// download, so resuming or fetching in parts counts once. Only a
// single "bytes=N-" range with N > 0 is free: suffix ranges ("bytes=-N"),
// several ranges and If-Range (which turns a range into the whole file when
// it doesn't match) can all fetch the file from the start.
func countsAsDownload(r *http.Request) bool {
	rg := strings.TrimSpace(r.Header.Get("Range"))
	if rg == "" || r.Header.Get("If-Range") != "" {
		return true
	}
	spec, ok := strings.CutPrefix(rg, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return true
	}
	start, _, ok := strings.Cut(spec, "-")
	n, err := strconv.ParseInt(strings.TrimSpace(start), 10, 64)
	return !ok || err != nil || n <= 0
}

// viewOnlyOK reports whether a link without downloads may still send the
// file itself for an in-browser preview. Photos browsers can't show are
// previewed from a generated still, and other kinds have no preview at all,
// so their originals are downloads.
func viewOnlyOK(p string) bool {
	if files.NeedsStill(p) && !files.IsVideo(p) {
		return false
	}
	switch files.KindOf(p, false) {
	case "image":
		// Only pictures the server can watermark and shrink; an SVG or EXR
		// would go out as the original.
		return files.Thumbable(p)
	case "video", "audio", "pdf", "document", "spreadsheet", "presentation", "text", "code":
		return true
	}
	return false
}

func (s *Server) handleShareDownload(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	if !s.requireUnlocked(w, r, sc) {
		return
	}
	inline := r.URL.Query().Get("inline") == "1"
	if (inline && !sc.sh.CanView) || (!inline && !sc.sh.CanDownload) {
		forbidden(w, "that is not available for this link")
		return
	}
	it, p, err := sc.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if inline && !sc.sh.CanDownload && !viewOnlyOK(p) {
		forbidden(w, "downloads are not available for this link")
		return
	}
	// Check it is a file first, so a bad path doesn't use up a download.
	if e, err := it.m.d.Stat(r.Context(), p); err != nil || e.IsDir {
		if err == nil {
			err = files.ErrIsDir
		}
		s.fail(w, r, err)
		return
	}
	// Previews (inline=1, including the player's range requests) never use
	// up a download; asking for a "preview" of a file that has none is a
	// download. A HEAD request fetches nothing.
	download := !inline || !viewOnlyOK(p)
	if download && r.Method != http.MethodHead && countsAsDownload(r) {
		ok, err := s.store.CountDownload(r.Context(), sc.sh.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !ok {
			writeErr(w, http.StatusGone, "share_expired", "this link has reached its download limit")
			return
		}
		s.shareAudit(r, sc, "share.download", it.m.info.ID+":"+p, "")
		if sc.viewer != nil {
			_ = s.store.TouchRecipient(context.WithoutCancel(r.Context()), sc.viewer.ID, true)
		}
		s.shareEvent(sc, store.NotifyDownload, "“"+path.Base(p)+"” was downloaded", "by "+sc.who(r)+", via your link “"+s.shareName(sc)+"”")
	}
	if inline && sc.sh.Watermark && files.Thumbable(p) {
		s.serveWatermarked(w, r, it.m, p, s.watermarkText(sc))
		return
	}
	s.sendFile(w, r, it.m, p, inline)
}

func (s *Server) watermarkText(sc *shareCtx) string {
	who := "Preview"
	if sc.viewer != nil {
		who = sc.viewer.Email
	}
	return who + " · " + time.Now().Format(time.DateOnly)
}

func (s *Server) handleShareThumb(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	if !s.requireUnlocked(w, r, sc) {
		return
	}
	if !sc.sh.CanView {
		forbidden(w, "previews are not available for this link")
		return
	}
	it, p, err := sc.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if sc.sh.Watermark {
		s.serveWatermarkedThumb(w, r, it.m, p, s.watermarkText(sc))
		return
	}
	s.serveThumbFor(w, r, it.m, p)
}

// serveWatermarkedThumb is serveThumbFor with the watermark stamped on, so
// thumbnails aren't a clean copy of a watermarked photo.
func (s *Server) serveWatermarkedThumb(w http.ResponseWriter, r *http.Request, m *mounted, p, text string) {
	tp, err := s.thumbs.Get(r.Context(), m.info.ID, m.d, p)
	if err != nil && files.NeedsStill(p) {
		var file string
		if file, _, err = s.mediaFor(r.Context(), m, p, store.MediaStill, true); err == nil && file != "" {
			tp, err = s.thumbs.FromFile(path.Base(file), file)
		} else if err == nil {
			err = files.ErrNoThumb
		}
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, "no_thumb", "no thumbnail")
		return
	}
	s.sendWatermarked(w, r, "file\x00"+tp+"\x00"+text, func() ([]byte, error) { return files.WatermarkFile(tp, text) })
}

func (s *Server) handleShareMedia(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	if !s.requireUnlocked(w, r, sc) {
		return
	}
	if !sc.sh.CanView {
		forbidden(w, "previews are not available for this link")
		return
	}
	it, p, err := sc.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if q := r.URL.Query(); q.Has("retry") {
		// Visitors can't make the worker redo a finished rendition.
		q.Del("retry")
		r.URL.RawQuery = q.Encode()
	}
	if sc.sh.Watermark && r.URL.Query().Get("kind") == "still" {
		s.serveWatermarkedStill(w, r, it.m, p, s.watermarkText(sc))
		return
	}
	s.serveMedia(w, r, it.m, p)
}

func (s *Server) handleShareZip(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	if !s.requireUnlocked(w, r, sc) {
		return
	}
	if !sc.sh.CanDownload || (!sc.isColl() && !sc.sh.IsDir) {
		forbidden(w, "downloads are not available for this link")
		return
	}
	names := r.URL.Query()["name"]
	if len(names) > 5000 {
		badRequest(w, "too many items")
		return
	}
	if len(names) > 0 && !sc.sh.CanView {
		forbidden(w, "only the whole folder can be downloaded")
		return
	}
	reqPath := r.URL.Query().Get("path")
	// Hidden files never leave through a link, whatever the admin setting.
	const hide = true
	var srcs []files.ZipSource
	zipName := s.shareName(sc)
	if c, _ := files.Clean(reqPath); sc.isColl() && c == "." {
		// The whole collection, or some of its items.
		want := map[string]bool{}
		for _, n := range names {
			want[n] = true
		}
		for _, it := range sc.items {
			if len(want) > 0 && !want[it.Name] {
				continue
			}
			src := files.ZipSource{Drive: it.m.d, Dir: files.Parent(it.Path), Names: []string{path.Base(it.Path)}}
			if it.Name != path.Base(it.Path) {
				// Renamed to keep names unique: zip the folder's contents
				// under the new name.
				if it.IsDir {
					es, _ := it.m.d.List(r.Context(), it.Path, hide)
					src = files.ZipSource{Drive: it.m.d, Dir: it.Path, Prefix: it.Name}
					for _, e := range it.visible(es) {
						src.Names = append(src.Names, e.Name)
					}
				}
			}
			srcs = append(srcs, src)
		}
	} else {
		it, dir, err := sc.resolve(reqPath)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !it.IsDir {
			forbidden(w, "that is a file; download it directly")
			return
		}
		if len(names) == 0 {
			es, err := it.m.d.List(r.Context(), dir, hide)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			for _, e := range it.visible(es) {
				names = append(names, e.Name)
			}
		}
		if len(names) > 5000 {
			badRequest(w, "too many items")
			return
		}
		for _, n := range names {
			if !files.ValidEntryName(n) || files.Hidden(n, true) || !it.m.d.Exists(r.Context(), path.Join(dir, n)) {
				s.fail(w, r, fs.ErrNotExist)
				return
			}
			if _, err := it.confine(files.Rel(it.Path, path.Join(dir, n))); err != nil {
				s.fail(w, r, fs.ErrNotExist)
				return
			}
		}
		srcs = []files.ZipSource{{Drive: it.m.d, Dir: dir, Names: names}}
		if dir != it.Path {
			zipName = path.Base(dir)
		}
	}
	if r.Method == http.MethodHead {
		zipHeaders(w, zipName)
		return
	}
	ok, err := s.store.CountDownload(r.Context(), sc.sh.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusGone, "share_expired", "this link has reached its download limit")
		return
	}
	s.shareAudit(r, sc, "share.download_zip", sc.sh.DriveID+":"+sc.sh.Path, reqPath)
	if sc.viewer != nil {
		_ = s.store.TouchRecipient(context.WithoutCancel(r.Context()), sc.viewer.ID, true)
	}
	s.shareEvent(sc, store.NotifyDownload, "“"+zipName+"” was downloaded as a zip", "by "+sc.who(r)+", via your link “"+s.shareName(sc)+"”")
	zipHeaders(w, zipName)
	if err := files.WriteZipFrom(r.Context(), w, srcs, hide); err != nil && r.Context().Err() == nil {
		s.log.Warn("zip stream failed", "err", err)
		// The status line is gone; cut the connection so the zip doesn't
		// look complete.
		panic(http.ErrAbortHandler)
	}
}

// Uploads.

func (s *Server) shareUploadAllowed(w http.ResponseWriter, r *http.Request, sc *shareCtx) bool {
	if !s.requireUnlocked(w, r, sc) {
		return false
	}
	if !sc.sh.CanUpload || sc.isColl() {
		forbidden(w, "uploads are not allowed for this link")
		return false
	}
	return true
}

func shareOwns(sc *shareCtx) func(*store.Upload) bool {
	return func(up *store.Upload) bool { return up.ShareID == sc.sh.ID }
}

// uploaderFolder turns an uploader's name into a folder name.
func uploaderFolder(name string) (string, error) {
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool {
		return r == '/' || r == '\\' || r < 32 || r == 0x7f
	}), " ")
	name = strings.Trim(strings.TrimSpace(name), ".")
	if name == "" || len(name) > 60 {
		return "", errors.New("enter your name (up to 60 characters)")
	}
	folder := name + " – " + time.Now().Format(time.DateOnly)
	return folder, files.ValidName(folder)
}

func (s *Server) handleShareUploadCreate(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	var req uploadReq
	if !decodeJSON(w, r, &req) || !s.shareUploadAllowed(w, r, sc) {
		return
	}
	// Visitors can't add files or folders the owner wouldn't see (".x",
	// "_coffer", "desktop.ini"…), anywhere in the uploaded path.
	if c, err := files.Clean(req.Name); err != nil || c == "." || hiddenIn(c) {
		badRequest(w, "this file name isn't allowed on a link")
		return
	}
	if exts := sc.sh.Exts(); len(exts) > 0 {
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(req.Name)), ".")
		allowed := false
		for _, e := range exts {
			allowed = allowed || e == ext
		}
		if !allowed {
			writeErr(w, http.StatusUnsupportedMediaType, "file_type", "this link only accepts "+strings.Join(exts, ", ")+" files")
			return
		}
	}
	it, dir, err := sc.resolve(req.Dir)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.uploadSlot(w, r, sc) {
		return
	}
	if !sc.sh.CanView {
		// A drop box takes files at its top, so uploaders can't probe which
		// folders exist inside it.
		dir = it.Path
	}
	if sc.sh.UploadNamed {
		who := req.Uploader
		if strings.TrimSpace(who) == "" && sc.viewer != nil {
			who = sc.viewer.Name
			if who == "" {
				who = sc.viewer.Email
			}
		}
		folder, err := uploaderFolder(who)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		dir = path.Join(it.Path, folder)
		if err := it.m.d.MkdirAll(r.Context(), dir); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	up := &store.Upload{ShareID: sc.sh.ID, Dir: dir, Name: req.Name, Size: req.Size}
	if s.startUpload(w, r, it.m, up, s.uploadLimitMB(sc.sh)<<20) {
		rememberUpload(ipString(r), up.ID)
	}
}

func (s *Server) shareUpload(w http.ResponseWriter, r *http.Request, sc *shareCtx) (*store.Upload, *mounted, bool) {
	if !s.shareUploadAllowed(w, r, sc) {
		return nil, nil, false
	}
	return s.loadUpload(w, r, shareOwns(sc))
}

func (s *Server) handleShareUploadStatus(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	if up, m, ok := s.shareUpload(w, r, sc); ok {
		s.uploadStatus(w, r, up, m)
	}
}

func (s *Server) handleShareUploadChunk(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	if up, m, ok := s.shareUpload(w, r, sc); ok {
		s.uploadChunk(w, r, up, m)
	}
}

func (s *Server) handleShareUploadComplete(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	up, m, ok := s.shareUpload(w, r, sc)
	if !ok {
		return
	}
	final, ok := s.finishUpload(w, r, up, m, nil)
	if !ok {
		return
	}
	forgetUpload(ipString(r), up.ID)
	s.shareAudit(r, sc, "share.upload", sc.sh.DriveID+":"+final, fmt.Sprintf("%d bytes", up.Size))
	s.shareEvent(sc, store.NotifyUpload, "New upload: “"+path.Base(final)+"”", "from "+sc.who(r)+", to your link “"+s.shareName(sc)+"”")
	_ = s.store.TouchShare(context.WithoutCancel(r.Context()), sc.sh.ID)
	name := path.Base(final)
	if !sc.sh.CanView {
		name = path.Base(up.Name) // a drop box must not reveal what else is in the folder
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (s *Server) handleShareUploadAbort(w http.ResponseWriter, r *http.Request, sc *shareCtx) {
	if up, m, ok := s.shareUpload(w, r, sc); ok {
		_ = m.d.AbortUpload(r.Context(), uploadState(up))
		_ = s.store.DeleteUpload(r.Context(), up.ID)
		forgetUpload(ipString(r), up.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}
