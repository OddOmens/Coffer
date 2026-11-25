package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (s *Server) handleUsersList(w http.ResponseWriter, r *http.Request, _ *store.User) {
	users, err := s.store.Users(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type userOut struct {
		meJSON
		Places []*store.Place `json:"places"`
	}
	out := make([]userOut, 0, len(users))
	for _, u := range users {
		ps, _ := s.store.PlacesForUser(r.Context(), u.ID)
		out = append(out, userOut{s.me(r.Context(), u), ps})
	}
	writeJSON(w, http.StatusOK, out)
}

type userReq struct {
	Username  *string `json:"username"`
	Password  *string `json:"password"`
	Role      *string `json:"role"`
	Disabled  *bool   `json:"disabled"`
	ResetTOTP bool    `json:"resetTotp"`
	Email     *string `json:"email"`
	QuotaMB   *int64  `json:"quotaMB"` // 0 = unlimited
	// Home, on create only: a folder on a drive the new user gets.
	Home *placeReq `json:"home"`
}

// applyUser validates req and copies it onto u. It reports whether existing
// sessions should be revoked.
func (s *Server) applyUser(w http.ResponseWriter, r *http.Request, u *store.User, req userReq) (revoke, ok bool) {
	if req.Username != nil {
		name := strings.TrimSpace(*req.Username)
		if !usernameRe.MatchString(name) {
			badRequest(w, "usernames use letters, numbers, dot, dash and underscore (max 64)")
			return false, false
		}
		u.Username = name
	}
	if req.Password != nil {
		if !validPassword(w, *req.Password) {
			return false, false
		}
		h, err := security.HashPassword(*req.Password)
		if err != nil {
			s.fail(w, r, err)
			return false, false
		}
		u.PasswordHash, revoke = h, true
	}
	if req.Role != nil {
		if *req.Role != "admin" && *req.Role != "user" {
			badRequest(w, "role must be admin or user")
			return false, false
		}
		revoke = revoke || u.Role != *req.Role
		u.Role = *req.Role
	}
	if req.Disabled != nil {
		revoke = revoke || *req.Disabled
		u.Disabled = *req.Disabled
	}
	if req.Email != nil {
		e := strings.TrimSpace(*req.Email)
		if e != "" && !validEmail(e) {
			badRequest(w, "that is not an email address")
			return false, false
		}
		u.Email = strings.ToLower(e)
	}
	if req.QuotaMB != nil {
		if *req.QuotaMB < 0 {
			badRequest(w, "the storage limit cannot be negative")
			return false, false
		}
		u.QuotaBytes = *req.QuotaMB << 20
	}
	if req.ResetTOTP {
		// Recovery codes are cleared by the caller once the user is saved.
		u.TOTPEnabled, u.TOTPSecret, u.TOTPLast = false, "", 0
		revoke = true
	}
	return revoke, true
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request, admin *store.User) {
	var req userReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == nil || req.Password == nil {
		badRequest(w, "username and password are required")
		return
	}
	u := &store.User{Role: "user", Scope: ".", CanWrite: true, CanDelete: true, CanShare: true}
	if _, ok := s.applyUser(w, r, u, req); !ok {
		return
	}
	var home string
	var homeDrive *mounted
	if req.Home != nil && u.Role != "admin" {
		var ok bool
		if home, homeDrive, ok = s.validPlace(w, r, *req.Home); !ok {
			return
		}
	}
	if _, err := s.store.UserByName(r.Context(), u.Username); err == nil {
		writeErr(w, http.StatusConflict, "exists", "that username is taken")
		return
	}
	if err := s.store.CreateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	if homeDrive != nil {
		pl := &store.Place{ID: security.RandomToken(9), UserID: u.ID, DriveID: homeDrive.info.ID, Path: home,
			Name: strings.TrimSpace(req.Home.Name), CanWrite: req.Home.CanWrite, CanDelete: req.Home.CanDelete, CanShare: req.Home.CanShare}
		if err := s.store.CreatePlace(r.Context(), pl); err != nil {
			// Undo the account, so the admin can simply try again.
			s.log.Error("creating a home folder failed", "user", u.Username, "err", err)
			if derr := s.store.DeleteUser(context.WithoutCancel(r.Context()), u.ID); derr != nil {
				s.log.Error("removing the half-made account failed", "user", u.Username, "err", derr)
				writeErr(w, http.StatusInternalServerError, "partial", "the account “"+u.Username+"” was made, but its home folder wasn't: add the folder from the user's settings")
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal", "the home folder couldn't be set up, so the account wasn't made; try again")
			return
		}
	}
	s.audit(r, admin, "", "user.create", "", u.Username)
	writeJSON(w, http.StatusCreated, s.me(r.Context(), u))
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request, admin *store.User) {
	var req userReq
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := s.store.UserByID(r.Context(), parseID(r.PathValue("id")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	wasActiveAdmin := u.IsAdmin() && !u.Disabled
	prevName := u.Username
	revoke, ok := s.applyUser(w, r, u, req)
	if !ok {
		return
	}
	if wasActiveAdmin && (!u.IsAdmin() || u.Disabled) {
		if n, err := s.store.CountActiveAdmins(r.Context()); err != nil || n <= 1 {
			badRequest(w, "there must always be at least one active administrator")
			return
		}
	}
	if !strings.EqualFold(prevName, u.Username) {
		if _, err := s.store.UserByName(r.Context(), u.Username); err == nil {
			writeErr(w, http.StatusConflict, "exists", "that username is taken")
			return
		}
	}
	if err := s.store.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.ResetTOTP {
		_ = s.store.ReplaceRecoveryCodes(r.Context(), u.ID, nil)
	}
	if req.Password != nil {
		_, _ = s.store.DeleteAppTokens(r.Context(), u.ID)
	}
	if revoke {
		keep := ""
		if u.ID == admin.ID {
			keep = sessionFrom(r).ID
		}
		_ = s.store.DeleteUserSessions(r.Context(), u.ID, keep)
	}
	s.audit(r, admin, "", "user.update", "", u.Username)
	writeJSON(w, http.StatusOK, s.me(r.Context(), u))
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request, admin *store.User) {
	id := parseID(r.PathValue("id"))
	if id == admin.ID {
		badRequest(w, "you cannot delete your own account")
		return
	}
	u, err := s.store.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, admin, "", "user.delete", "", u.Username+" (and their share links)")
	w.WriteHeader(http.StatusNoContent)
}

type settingsJSON struct {
	store.Settings
	SMTPHasPassword bool    `json:"smtpHasPassword"`
	SMTPPassword    *string `json:"smtpPassword,omitempty"` // write only; "" clears it
	MailReady       bool    `json:"mailReady"`
	PublicURL       string  `json:"publicUrl"`
}

func (s *Server) settingsOut() settingsJSON {
	st := s.cur()
	out := settingsJSON{Settings: st, SMTPHasPassword: st.SMTPSecret != "", MailReady: s.mailEnabled(), PublicURL: s.cfg.PublicURL}
	out.SMTPSecret = ""
	return out
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request, _ *store.User) {
	writeJSON(w, http.StatusOK, s.settingsOut())
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req settingsJSON
	if !decodeJSON(w, r, &req) {
		return
	}
	st := req.Settings
	st.SMTPSecret = s.cur().SMTPSecret
	if req.SMTPPassword != nil {
		st.SMTPSecret = ""
		if *req.SMTPPassword != "" {
			st.SMTPSecret = s.keys.Encrypt([]byte(*req.SMTPPassword))
		}
	}
	st.SMTPHost = strings.TrimSpace(st.SMTPHost)
	st.SMTPFrom = strings.TrimSpace(st.SMTPFrom)
	st.ShareWebsite = strings.TrimSpace(st.ShareWebsite)
	if st.SMTPPort == 0 {
		st.SMTPPort = 587
	}
	if st.ProxyHeight == 0 {
		st.ProxyHeight = 1080
	}
	if st.SelfBackup == "" {
		st.SelfBackup = "auto"
	}
	st.AppName = strings.TrimSpace(st.AppName)
	switch {
	case st.AppName == "" || len(st.AppName) > 60:
		badRequest(w, "the app name must be 1-60 characters")
	case !hexColor.MatchString(st.AccentColor):
		badRequest(w, "the accent color must look like #3b5bdb")
	case len(st.LoginMessage) > 500:
		badRequest(w, "the sign-in message must be at most 500 characters")
	case st.ShareDefaultExpiryDays < 0 || st.ShareDefaultExpiryDays > 3650,
		st.ShareMaxExpiryDays < 0 || st.ShareMaxExpiryDays > 3650,
		st.TrashDays < 0 || st.TrashDays > 3650:
		badRequest(w, "day values must be between 0 and 3650")
	case st.ShareUploadMaxMB < 0:
		badRequest(w, "the upload limit cannot be negative")
	case st.ShareMaxExpiryDays > 0 && (st.ShareDefaultExpiryDays == 0 || st.ShareDefaultExpiryDays > st.ShareMaxExpiryDays):
		badRequest(w, "the default expiry must be within the maximum expiry")
	case st.Require2FA != "off" && st.Require2FA != "admins" && st.Require2FA != "all":
		badRequest(w, "two-factor policy must be off, admins or all")
	case st.ScanMinutes < 0 || st.ScanMinutes > 10080 || st.ScanMinutesS3 < 0 || st.ScanMinutesS3 > 10080:
		badRequest(w, "scan intervals must be between 0 and 10080 minutes")
	case st.DefaultTheme != "light" && st.DefaultTheme != "dark" && st.DefaultTheme != "system":
		badRequest(w, "default theme must be light, dark or system")
	case st.DefaultDensity != "compact" && st.DefaultDensity != "comfortable":
		badRequest(w, "density must be compact or comfortable")
	case st.SMTPSecurity != "starttls" && st.SMTPSecurity != "tls" && st.SMTPSecurity != "none":
		badRequest(w, "mail security must be starttls, tls or none")
	case st.SMTPPort < 1 || st.SMTPPort > 65535:
		badRequest(w, "the mail port must be between 1 and 65535")
	case strings.ContainsAny(st.SMTPHost, " /\\\r\n") || len(st.SMTPHost) > 253:
		badRequest(w, "the mail server should be a host name like smtp.example.com")
	case st.SMTPFrom != "" && !validEmail(st.SMTPFrom):
		badRequest(w, "the sender must be an email address")
	case len(st.SMTPFromName) > 80 || len(st.ShareFooter) > 300:
		badRequest(w, "the sender name or share page footer is too long")
	case st.ShareWebsite != "" && !strings.HasPrefix(st.ShareWebsite, "https://") && !strings.HasPrefix(st.ShareWebsite, "http://"):
		badRequest(w, "the website must start with https://")
	case st.ProxyHeight != 720 && st.ProxyHeight != 1080:
		badRequest(w, "preview video height must be 720 or 1080")
	case st.SelfBackup != "auto" && st.SelfBackup != "on" && st.SelfBackup != "off":
		badRequest(w, "copying Coffer's database into backups must be auto, on or off")
	case st.MediaCacheGB < 0 || st.MediaCacheGB > 100000:
		badRequest(w, "the preview cache limit must be between 0 and 100000 GB")
	default:
		st.LogoType = s.cur().LogoType
		if err := s.store.SaveSettings(r.Context(), st); err != nil {
			s.fail(w, r, err)
			return
		}
		s.settings.Store(&st)
		s.audit(r, u, "", "settings.update", "", "")
		writeJSON(w, http.StatusOK, s.settingsOut())
	}
}

func (s *Server) logoPath() string { return filepath.Join(s.cfg.DataDir, "branding", "logo") }

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	st := s.cur()
	if st.LogoType == "" {
		writeErr(w, http.StatusNotFound, "not_found", "no logo")
		return
	}
	f, err := os.Open(s.logoPath())
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "no logo")
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", st.LogoType)
	w.Header().Set("Content-Security-Policy", fileCSP)
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) handleLogoUpload(w http.ResponseWriter, r *http.Request, u *store.User) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "logos must be under 1 MB")
		return
	}
	ctype := http.DetectContentType(data)
	switch {
	case ctype == "image/png", ctype == "image/jpeg", ctype == "image/webp":
	case bytes.Contains(data[:min(len(data), 1024)], []byte("<svg")):
		ctype = "image/svg+xml" // rendered via <img> and sandboxed, so scripts cannot run
	default:
		badRequest(w, "upload a PNG, JPEG, WebP or SVG image")
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.logoPath()), 0o700); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := os.WriteFile(s.logoPath(), data, 0o600); err != nil {
		s.fail(w, r, err)
		return
	}
	st := s.cur()
	st.LogoType = ctype
	if err := s.store.SaveSettings(r.Context(), st); err != nil {
		s.fail(w, r, err)
		return
	}
	s.settings.Store(&st)
	s.audit(r, u, "", "settings.logo", "", ctype)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogoDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	if err := os.Remove(s.logoPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.fail(w, r, err)
		return
	}
	st := s.cur()
	st.LogoType = ""
	if err := s.store.SaveSettings(r.Context(), st); err != nil {
		s.fail(w, r, err)
		return
	}
	s.settings.Store(&st)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, _ *store.User) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	entries, err := s.store.AuditLog(r.Context(), before, limit, q.Get("action"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
