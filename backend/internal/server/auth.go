package server

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

const (
	cookiePlain  = "coffer_sid"
	cookieSecure = "__Host-coffer_sid" // __Host- pins it to this host, path / and HTTPS
)

// sessionToken reads the cookie setSessionCookie would have set. Over HTTPS
// only the __Host- cookie counts: a plain one may have been planted over HTTP
// or from a sibling host to sign the browser into someone else's account.
func (s *Server) sessionToken(r *http.Request) string {
	name := cookiePlain
	if isSecure(r) {
		name = cookieSecure
	}
	if c, err := r.Cookie(name); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	name := cookiePlain
	if isSecure(r) {
		name = cookieSecure
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteStrictMode,
	})
}

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

func sessionFrom(r *http.Request) *store.Session {
	sess, _ := r.Context().Value(sessionKey).(*store.Session)
	return sess
}

// authed requires a valid session.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := s.sessionToken(r)
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "unauthenticated", "please sign in")
			return
		}
		ctx := r.Context()
		sess, err := s.store.SessionByToken(ctx, security.HashToken(token))
		now := time.Now().Unix()
		if err != nil || now >= sess.ExpiresAt || now-sess.LastSeen > int64(s.cfg.SessionIdle.Seconds()) {
			s.setSessionCookie(w, r, "", -1)
			writeErr(w, http.StatusUnauthorized, "unauthenticated", "your session has expired, please sign in")
			return
		}
		u, err := s.store.UserByID(ctx, sess.UserID)
		if err != nil || u.Disabled {
			writeErr(w, http.StatusUnauthorized, "unauthenticated", "please sign in")
			return
		}
		if now-sess.LastSeen > 60 || sess.IP != ipString(r) {
			_ = s.store.TouchSession(ctx, sess.ID, ipString(r), now)
		}
		if s.mustEnroll(u) && !allowedWhileEnrolling(r.URL.Path) {
			writeErr(w, http.StatusForbidden, "2fa_required", "set up two-factor authentication to continue")
			return
		}
		ctx = context.WithValue(ctx, userKey, u)
		ctx = context.WithValue(ctx, sessionKey, sess)
		h(w, r.WithContext(ctx), u)
	}
}

func (s *Server) admin(h func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, u *store.User) {
		if !u.IsAdmin() {
			forbidden(w, "administrators only")
			return
		}
		h(w, r, u)
	})
}

type meJSON struct {
	*store.User
	MustEnroll2FA bool            `json:"mustEnroll2fa"`
	RecoveryLeft  int             `json:"recoveryLeft"`
	Prefs         json.RawMessage `json:"prefs"`
	QuotaUsed     int64           `json:"quotaUsed"`
	MailReady     bool            `json:"mailReady"` // invitations by email can be sent
}

func (s *Server) me(ctx context.Context, u *store.User) meJSON {
	out := meJSON{User: u, MustEnroll2FA: s.mustEnroll(u), Prefs: json.RawMessage(u.Prefs)}
	if !json.Valid(out.Prefs) {
		out.Prefs = json.RawMessage("{}")
	}
	if u.TOTPEnabled {
		out.RecoveryLeft, _ = s.store.RecoveryCodesLeft(ctx, u.ID)
	}
	if u.QuotaBytes > 0 {
		out.QuotaUsed = s.quotaUsed(ctx, u)
	}
	out.MailReady = s.mailEnabled()
	return out
}

// mustEnroll reports whether policy requires u to set up two-factor first.
func (s *Server) mustEnroll(u *store.User) bool {
	switch s.cur().Require2FA {
	case "all":
		return !u.TOTPEnabled
	case "admins":
		return !u.TOTPEnabled && u.IsAdmin()
	}
	return false
}

func allowedWhileEnrolling(p string) bool {
	return strings.HasPrefix(p, "/api/me") || p == "/api/auth/logout" || p == "/api/notifications"
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip := ipString(r)
	if !s.loginLimit.Allow(ip) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many sign-in attempts, try again in a minute")
		return
	}
	key := "login:" + strings.ToLower(strings.TrimSpace(req.Username))
	if wait := s.backoff.Wait(key); wait > 0 {
		secs := int(math.Ceil(wait.Seconds()))
		w.Header().Set("Retry-After", fmt.Sprint(secs))
		writeErr(w, http.StatusTooManyRequests, "locked", fmt.Sprintf("too many failed attempts, try again in %d seconds", secs))
		return
	}

	ctx := r.Context()
	u, err := s.store.UserByName(ctx, req.Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	ok := false
	rehash := false
	if u == nil {
		security.DummyVerify(req.Password)
	} else {
		ok, rehash, _ = security.VerifyPassword(req.Password, u.PasswordHash)
	}
	if !ok || u.Disabled {
		s.backoff.Fail(key)
		s.audit(r, nil, "", "login.failed", "", "username="+truncate(req.Username, 64))
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	if u.TOTPEnabled {
		if strings.TrimSpace(req.Code) == "" {
			writeErr(w, http.StatusUnauthorized, "totp_required", "enter the code from your authenticator app")
			return
		}
		valid := false
		if code := strings.ReplaceAll(strings.TrimSpace(req.Code), " ", ""); len(code) == 6 {
			secret, err := s.keys.Decrypt(u.TOTPSecret)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			var step int64
			if step, valid = security.VerifyTOTP(string(secret), code, time.Now(), u.TOTPLast); valid {
				// Claim the step atomically: a parallel sign-in with the same
				// code read the same TOTPLast and must not succeed too.
				if valid, err = s.store.UseTOTPStep(ctx, u.ID, step); err != nil {
					s.fail(w, r, err)
					return
				}
			}
		} else if used, _ := s.store.UseRecoveryCode(ctx, u.ID, security.HashToken(normalizeRecovery(code))); used {
			valid = true
			s.audit(r, u, "", "2fa.recovery_used", "", "")
			s.notify(u.ID, "security", "A recovery code was used to sign in", "Generate new codes if you are running low.", "/account")
		}
		if !valid {
			s.backoff.Fail(key)
			s.audit(r, u, "", "login.failed", "", "bad 2fa code")
			writeErr(w, http.StatusUnauthorized, "invalid_code", "that code is not valid")
			return
		}
	}
	if rehash {
		if h, err := security.HashPassword(req.Password); err == nil {
			if ok, _ := s.store.SetPassword(ctx, u.ID, u.PasswordHash, h); ok {
				u.PasswordHash = h
			}
		}
	}
	s.backoff.Reset(key)

	token := security.RandomToken(32)
	now := time.Now()
	sess := &store.Session{
		ID: security.RandomToken(12), UserID: u.ID, CreatedAt: now.Unix(), LastSeen: now.Unix(),
		ExpiresAt: now.Add(s.cfg.SessionMax).Unix(), IP: ip, UserAgent: truncate(r.UserAgent(), 256),
	}
	if err := s.store.CreateSession(ctx, sess, security.HashToken(token), u); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Disabled or reset while we were checking the password.
			writeErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
			return
		}
		s.fail(w, r, err)
		return
	}
	// Replace, rather than orphan, a session this browser already had.
	if old := s.sessionToken(r); old != "" {
		if prev, err := s.store.SessionByToken(ctx, security.HashToken(old)); err == nil {
			_ = s.store.DeleteSession(ctx, prev.UserID, prev.ID)
		}
	}
	s.setSessionCookie(w, r, token, int(s.cfg.SessionMax.Seconds()))
	s.audit(r, u, "", "login", "", "")
	writeJSON(w, http.StatusOK, s.me(r.Context(), u))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := s.sessionToken(r); token != "" {
		if sess, err := s.store.SessionByToken(r.Context(), security.HashToken(token)); err == nil {
			_ = s.store.DeleteSession(r.Context(), sess.UserID, sess.ID)
		}
	}
	s.setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, http.StatusOK, s.me(r.Context(), u))
}

func (s *Server) checkPassword(w http.ResponseWriter, r *http.Request, u *store.User, password string) bool {
	key := fmt.Sprintf("reauth:%d", u.ID)
	if wait := s.backoff.Wait(key); wait > 0 {
		writeErr(w, http.StatusTooManyRequests, "locked", "too many failed attempts, try again later")
		return false
	}
	if ok, _, _ := security.VerifyPassword(password, u.PasswordHash); !ok {
		s.backoff.Fail(key)
		writeErr(w, http.StatusForbidden, "invalid_password", "current password is incorrect")
		return false
	}
	s.backoff.Reset(key)
	return true
}

func validPassword(w http.ResponseWriter, p string) bool {
	if len([]rune(p)) < security.MinPasswordLength || len(p) > 1024 {
		badRequest(w, fmt.Sprintf("passwords must be at least %d characters", security.MinPasswordLength))
		return false
	}
	return true
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeJSON(w, r, &req) || !validPassword(w, req.New) || !s.checkPassword(w, r, u, req.Current) {
		return
	}
	h, err := security.HashPassword(req.New)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ok, err := s.store.SetPassword(r.Context(), u.ID, u.PasswordHash, h)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusConflict, "changed", "your password was just changed elsewhere, sign in again")
		return
	}
	u.PasswordHash = h
	_ = s.store.DeleteUserSessions(r.Context(), u.ID, sessionFrom(r).ID)
	// App passwords were made with the old password; whoever knew it may
	// have made one.
	revoked, _ := s.store.DeleteAppTokens(r.Context(), u.ID)
	s.audit(r, u, "", "password.change", "", fmt.Sprintf("%d app passwords revoked", revoked))
	writeJSON(w, http.StatusOK, map[string]int{"appPasswordsRevoked": revoked})
}

// handleTOTPSetup starts two-factor setup. It asks for the password, so a
// stolen session can't tie the account to someone else's authenticator.
func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.checkPassword(w, r, u, req.Password) {
		return
	}
	if u.TOTPEnabled {
		badRequest(w, "two-factor authentication is already on")
		return
	}
	secret := security.NewTOTPSecret()
	u.TOTPSecret = s.keys.Encrypt([]byte(secret))
	if err := s.store.SetTOTP(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret": secret,
		"uri":    security.TOTPURI(s.cur().AppName, u.Username, secret),
	})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.checkPassword(w, r, u, req.Password) {
		return
	}
	if u.TOTPEnabled || u.TOTPSecret == "" {
		badRequest(w, "start two-factor setup first")
		return
	}
	secret, err := s.keys.Decrypt(u.TOTPSecret)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	step, ok := security.VerifyTOTP(string(secret), req.Code, time.Now(), 0)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_code", "that code is not valid, check your device clock")
		return
	}
	u.TOTPEnabled, u.TOTPLast = true, step
	if err := s.store.SetTOTP(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	codes, err := s.newRecoveryCodes(r.Context(), u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.store.DeleteUserSessions(r.Context(), u.ID, sessionFrom(r).ID)
	s.audit(r, u, "", "2fa.enable", "", "")
	// App passwords keep working (WebDAV has no second factor); list them so
	// the user can remove any they don't recognise.
	apps := []string{}
	if list, err := s.store.AppTokens(r.Context(), u.ID); err == nil {
		for _, t := range list {
			apps = append(apps, t.Name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"me": s.me(r.Context(), u), "recoveryCodes": codes, "appPasswords": apps})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.checkPassword(w, r, u, req.Password) {
		return
	}
	if p := s.cur().Require2FA; p == "all" || (p == "admins" && u.IsAdmin()) {
		forbidden(w, "your administrator requires two-factor authentication")
		return
	}
	u.TOTPEnabled, u.TOTPSecret, u.TOTPLast = false, "", 0
	_ = s.store.ReplaceRecoveryCodes(r.Context(), u.ID, nil)
	if err := s.store.SetTOTP(r.Context(), u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "2fa.disable", "", "")
	writeJSON(w, http.StatusOK, s.me(r.Context(), u))
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := s.store.SessionsForUser(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type sessJSON struct {
		*store.Session
		Current bool `json:"current"`
	}
	cur := sessionFrom(r).ID
	out := make([]sessJSON, 0, len(list))
	for _, sess := range list {
		out = append(out, sessJSON{sess, sess.ID == cur})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	if err := s.store.DeleteSession(r.Context(), u.ID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func normalizeRecovery(c string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(c))
}

var recoveryAlphabet = base32.NewEncoding("abcdefghjkmnpqrstuvwxyz234567890").WithPadding(base32.NoPadding)

// newRecoveryCodes replaces u's recovery codes and returns them once.
func (s *Server) newRecoveryCodes(ctx context.Context, u *store.User) ([]string, error) {
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		b := make([]byte, 7)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		c := recoveryAlphabet.EncodeToString(b)[:10]
		codes[i] = c[:5] + "-" + c[5:]
		hashes[i] = security.HashToken(c)
	}
	return codes, s.store.ReplaceRecoveryCodes(ctx, u.ID, hashes)
}

func (s *Server) handleRecoveryCodes(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.checkPassword(w, r, u, req.Password) {
		return
	}
	if !u.TOTPEnabled {
		badRequest(w, "turn on two-factor authentication first")
		return
	}
	codes, err := s.newRecoveryCodes(r.Context(), u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "2fa.recovery_regenerated", "", "")
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// App passwords let WebDAV clients sign in without the account password
// (and without two-factor prompts they cannot show).

func (s *Server) handleTokensList(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := s.store.AppTokens(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.checkPassword(w, r, u, req.Password) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 60 {
		badRequest(w, "name the app password, e.g. “Studio Mac Finder”")
		return
	}
	secret := security.RandomToken(24)
	t := &store.AppToken{ID: security.RandomToken(9), UserID: u.ID, Name: name}
	if err := s.store.CreateAppToken(r.Context(), t, security.HashToken(secret)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "apptoken.create", "", name)
	writeJSON(w, http.StatusCreated, map[string]any{"token": t, "secret": secret, "username": u.Username})
}

func (s *Server) handleTokenDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	if err := s.store.DeleteAppToken(r.Context(), u.ID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "apptoken.delete", "", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}
