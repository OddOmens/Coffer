package store

import (
	"context"
	"database/sql"
	"strings"
)

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	Scope        string `json:"scope"`
	CanWrite     bool   `json:"canWrite"`
	CanDelete    bool   `json:"canDelete"`
	CanShare     bool   `json:"canShare"`
	TOTPSecret   string `json:"-"` // sealed with security.Keys
	TOTPEnabled  bool   `json:"totpEnabled"`
	TOTPLast     int64  `json:"-"`
	Disabled     bool   `json:"disabled"`
	Prefs        string `json:"-"`
	QuotaBytes   int64  `json:"quotaBytes"` // 0 = unlimited
	Email        string `json:"email"`      // for notifications and as Reply-To on invitations
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

func (u *User) IsAdmin() bool { return u.Role == "admin" }

const userCols = `id, username, password_hash, role, scope, can_write, can_delete, can_share,
	totp_secret, totp_enabled, totp_last, disabled, created_at, updated_at, prefs, quota_bytes, email`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Scope, &u.CanWrite, &u.CanDelete,
		&u.CanShare, &u.TOTPSecret, &u.TOTPEnabled, &u.TOTPLast, &u.Disabled, &u.CreatedAt, &u.UpdatedAt, &u.Prefs, &u.QuotaBytes, &u.Email)
	if err != nil {
		return nil, notFound(err)
	}
	return u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id))
}

func (s *Store) UserByName(ctx context.Context, name string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE username = ?", strings.TrimSpace(name)))
}

func (s *Store) Users(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+userCols+" FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (n int, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return
}

func (s *Store) CountActiveAdmins(ctx context.Context) (n int, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0").Scan(&n)
	return
}

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	t := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, scope, can_write,
		can_delete, can_share, disabled, created_at, updated_at, quota_bytes, email) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Username, u.PasswordHash, u.Role, u.Scope, b2i(u.CanWrite), b2i(u.CanDelete), b2i(u.CanShare),
		b2i(u.Disabled), t, t, u.QuotaBytes, u.Email)
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	u.CreatedAt, u.UpdatedAt = t, t
	return nil
}

// UpdateUser writes every mutable column of u.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	u.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, `UPDATE users SET username = ?, password_hash = ?, role = ?, scope = ?,
		can_write = ?, can_delete = ?, can_share = ?, totp_secret = ?, totp_enabled = ?, totp_last = ?,
		disabled = ?, updated_at = ?, prefs = ?, quota_bytes = ?, email = ? WHERE id = ?`,
		u.Username, u.PasswordHash, u.Role, u.Scope, b2i(u.CanWrite), b2i(u.CanDelete), b2i(u.CanShare),
		u.TOTPSecret, b2i(u.TOTPEnabled), u.TOTPLast, b2i(u.Disabled), u.UpdatedAt, prefsOrEmpty(u.Prefs), u.QuotaBytes, u.Email, u.ID)
	return err
}

// The setters below write only their own columns, so a slow request holding
// an older copy of the user (sign-in, password checks) cannot undo an admin's
// concurrent disable, role change or password reset.

// SetPassword replaces the password hash if it is still oldHash.
func (s *Store) SetPassword(ctx context.Context, id int64, oldHash, newHash string) (bool, error) {
	return changed(s.db.ExecContext(ctx, "UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ? AND password_hash = ?",
		newHash, now(), id, oldHash))
}

// SetTOTP writes the two-factor columns of u.
func (s *Store) SetTOTP(ctx context.Context, u *User) error {
	u.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, "UPDATE users SET totp_secret = ?, totp_enabled = ?, totp_last = ?, updated_at = ? WHERE id = ?",
		u.TOTPSecret, b2i(u.TOTPEnabled), u.TOTPLast, u.UpdatedAt, u.ID)
	return err
}

// UseTOTPStep records step as the last accepted code. It reports false when
// that step or a later one was already used, so a code works once even when
// two sign-ins race.
func (s *Store) UseTOTPStep(ctx context.Context, userID, step int64) (bool, error) {
	return changed(s.db.ExecContext(ctx, "UPDATE users SET totp_last = ? WHERE id = ? AND totp_last < ?", step, userID, step))
}

func (s *Store) SetPrefs(ctx context.Context, userID int64, prefs string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET prefs = ? WHERE id = ?", prefsOrEmpty(prefs), userID)
	return err
}

// SetEmail writes the notification address of a user.
func (s *Store) SetEmail(ctx context.Context, userID int64, email string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET email = ?, updated_at = ? WHERE id = ?", email, now(), userID)
	return err
}

func changed(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	return err
}

type Session struct {
	ID        string `json:"id"`
	UserID    int64  `json:"-"`
	CreatedAt int64  `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen"`
	ExpiresAt int64  `json:"expiresAt"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
}

// CreateSession signs u in. It returns ErrNotFound when u was disabled or its
// password or two-factor secret changed since u was read, so a sign-in that
// races a password reset cannot outlive the reset's session revocation.
func (s *Store) CreateSession(ctx context.Context, sess *Session, tokenHash string, u *User) error {
	ok, err := changed(s.db.ExecContext(ctx, `INSERT INTO sessions (id, token_hash, user_id, created_at, last_seen,
		expires_at, ip, user_agent) SELECT ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users
		WHERE id = ? AND disabled = 0 AND password_hash = ? AND totp_secret = ?)`,
		sess.ID, tokenHash, sess.UserID, sess.CreatedAt, sess.LastSeen, sess.ExpiresAt, sess.IP, sess.UserAgent,
		u.ID, u.PasswordHash, u.TOTPSecret))
	if err == nil && !ok {
		err = ErrNotFound
	}
	return err
}

func (s *Store) SessionByToken(ctx context.Context, tokenHash string) (*Session, error) {
	sess := &Session{}
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, created_at, last_seen, expires_at, ip, user_agent
		FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.LastSeen, &sess.ExpiresAt, &sess.IP, &sess.UserAgent)
	return sess, notFound(err)
}

func (s *Store) TouchSession(ctx context.Context, id, ip string, t int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET last_seen = ?, ip = ? WHERE id = ?", t, ip, id)
	return err
}

func (s *Store) SessionsForUser(ctx context.Context, userID int64) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, created_at, last_seen, expires_at, ip, user_agent
		FROM sessions WHERE user_id = ? ORDER BY last_seen DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		sess := &Session{}
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.LastSeen, &sess.ExpiresAt, &sess.IP, &sess.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSession(ctx context.Context, userID int64, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ? AND user_id = ?", id, userID)
	return err
}

// DeleteUserSessions removes every session of a user except keepID.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, keepID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND id != ?", userID, keepID)
	return err
}

func (s *Store) PurgeSessions(ctx context.Context, idleBefore int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ? OR last_seen < ?", now(), idleBefore)
	return err
}

func prefsOrEmpty(p string) string {
	if p == "" {
		return "{}"
	}
	return p
}
