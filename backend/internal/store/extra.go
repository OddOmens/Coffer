package store

import "context"

// App passwords for WebDAV clients (Finder, Explorer, Nautilus, rclone).

type AppToken struct {
	ID        string `json:"id"`
	UserID    int64  `json:"-"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	LastUsed  int64  `json:"lastUsed"`
	LastIP    string `json:"lastIp"`
}

func (s *Store) CreateAppToken(ctx context.Context, t *AppToken, hash string) error {
	t.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, "INSERT INTO app_tokens (id, user_id, name, token_hash, created_at) VALUES (?, ?, ?, ?, ?)",
		t.ID, t.UserID, t.Name, hash, t.CreatedAt)
	return err
}

func (s *Store) AppTokenByHash(ctx context.Context, hash string) (*AppToken, error) {
	t := &AppToken{}
	err := s.db.QueryRowContext(ctx, "SELECT id, user_id, name, created_at, last_used, last_ip FROM app_tokens WHERE token_hash = ?", hash).
		Scan(&t.ID, &t.UserID, &t.Name, &t.CreatedAt, &t.LastUsed, &t.LastIP)
	return t, notFound(err)
}

func (s *Store) AppTokens(ctx context.Context, userID int64) ([]*AppToken, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, user_id, name, created_at, last_used, last_ip FROM app_tokens WHERE user_id = ? ORDER BY created_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AppToken{}
	for rows.Next() {
		t := &AppToken{}
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.CreatedAt, &t.LastUsed, &t.LastIP); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TouchAppToken(ctx context.Context, id, ip string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE app_tokens SET last_used = ?, last_ip = ? WHERE id = ?", now(), ip, id)
	return err
}

func (s *Store) DeleteAppToken(ctx context.Context, userID int64, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM app_tokens WHERE id = ? AND user_id = ?", id, userID)
	return err
}

// DeleteAppTokens revokes all of a user's app passwords and returns how
// many there were.
func (s *Store) DeleteAppTokens(ctx context.Context, userID int64) (int, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM app_tokens WHERE user_id = ?", userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Two-factor recovery codes, stored hashed and single use.

func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM recovery_codes WHERE user_id = ?", userID); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)", userID, h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UseRecoveryCode marks a code used; it reports false if it was unknown or spent.
func (s *Store) UseRecoveryCode(ctx context.Context, userID int64, hash string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at = 0",
		now(), userID, hash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) RecoveryCodesLeft(ctx context.Context, userID int64) (n int, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at = 0", userID).Scan(&n)
	return
}

// Notifications.

type Notification struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"-"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Link      string `json:"link"`
	CreatedAt int64  `json:"createdAt"`
	ReadAt    int64  `json:"readAt"`
}

func (s *Store) Notify(ctx context.Context, n *Notification) error {
	n.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, "INSERT INTO notifications (user_id, kind, title, body, link, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		n.UserID, n.Kind, n.Title, n.Body, n.Link, n.CreatedAt)
	return err
}

func (s *Store) Notifications(ctx context.Context, userID int64, limit int) ([]*Notification, int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, user_id, kind, title, body, link, created_at, read_at FROM notifications WHERE user_id = ? ORDER BY id DESC LIMIT ?", userID, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Notification{}
	for rows.Next() {
		n := &Notification{}
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &n.Title, &n.Body, &n.Link, &n.CreatedAt, &n.ReadAt); err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	var unread int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at = 0", userID).Scan(&unread)
	return out, unread, err
}

func (s *Store) MarkNotificationsRead(ctx context.Context, userID int64, ids []int64) error {
	if len(ids) == 0 {
		_, err := s.db.ExecContext(ctx, "UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at = 0", now(), userID)
		return err
	}
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, "UPDATE notifications SET read_at = ? WHERE user_id = ? AND id = ?", now(), userID, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) PurgeNotifications(ctx context.Context, before int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM notifications WHERE created_at < ?", before)
	return err
}

func (s *Store) Admins(ctx context.Context) ([]*User, error) {
	users, err := s.Users(ctx)
	if err != nil {
		return nil, err
	}
	var out []*User
	for _, u := range users {
		if u.IsAdmin() && !u.Disabled {
			out = append(out, u)
		}
	}
	return out, nil
}

func (s *Store) CountSessions(ctx context.Context) (n int, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE expires_at > ?", now()).Scan(&n)
	return
}

func (s *Store) AuditCountSince(ctx context.Context, since int64, prefix string) (n int, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit WHERE ts >= ? AND action LIKE ?", since, prefix+"%").Scan(&n)
	return
}
