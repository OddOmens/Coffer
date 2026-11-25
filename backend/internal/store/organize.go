package store

import (
	"context"
	"strings"
)

// Favorites, tags, locks and folder activity.

type Favorite struct {
	DriveID   string `json:"-"`
	Path      string `json:"-"`
	IsDir     bool   `json:"isDir"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *Store) SetFavorite(ctx context.Context, userID int64, drive, p string, isDir, on bool) error {
	var err error
	if on {
		_, err = s.db.ExecContext(ctx, "INSERT OR IGNORE INTO favorites (user_id, drive_id, path, is_dir, created_at) VALUES (?, ?, ?, ?, ?)",
			userID, drive, p, b2i(isDir), now())
	} else {
		_, err = s.db.ExecContext(ctx, "DELETE FROM favorites WHERE user_id = ? AND drive_id = ? AND path = ?", userID, drive, p)
	}
	return err
}

func (s *Store) Favorites(ctx context.Context, userID int64) ([]*Favorite, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT drive_id, path, is_dir, created_at FROM favorites WHERE user_id = ? ORDER BY created_at", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Favorite{}
	for rows.Next() {
		f := &Favorite{}
		if err := rows.Scan(&f.DriveID, &f.Path, &f.IsDir, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Tags are shared by everyone who can see the item.

type TaggedItem struct {
	DriveID string
	Path    string
	IsDir   bool
	Tag     string
}

func (s *Store) AddTag(ctx context.Context, drive, p, tag string, isDir bool, by int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO tags (drive_id, path, tag, is_dir, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		drive, p, tag, b2i(isDir), by, now())
	return err
}

func (s *Store) RemoveTag(ctx context.Context, drive, p, tag string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM tags WHERE drive_id = ? AND path = ? AND tag = ?", drive, p, tag)
	return err
}

// TagsIn returns the tags of the direct children of dir.
func (s *Store) TagsIn(ctx context.Context, drive, dir string) (map[string][]string, error) {
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	rows, err := s.db.QueryContext(ctx, "SELECT path, tag FROM tags WHERE drive_id = ? AND path LIKE ? ESCAPE '\\' ORDER BY tag",
		drive, likeEscape(prefix)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var p, t string
		if err := rows.Scan(&p, &t); err != nil {
			return nil, err
		}
		if !strings.Contains(p[len(prefix):], "/") {
			out[p] = append(out[p], t)
		}
	}
	return out, rows.Err()
}

func (s *Store) TagsOf(ctx context.Context, drive, p string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT tag FROM tags WHERE drive_id = ? AND path = ? ORDER BY tag", drive, p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Tagged lists every item carrying tag.
func (s *Store) Tagged(ctx context.Context, tag string) ([]*TaggedItem, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT drive_id, path, is_dir, tag FROM tags WHERE tag = ? ORDER BY path", tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TaggedItem
	for rows.Next() {
		t := &TaggedItem{}
		if err := rows.Scan(&t.DriveID, &t.Path, &t.IsDir, &t.Tag); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AllTags lists every tag with every item using it, for counting per viewer.
func (s *Store) AllTags(ctx context.Context) ([]*TaggedItem, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT drive_id, path, is_dir, tag FROM tags ORDER BY tag")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TaggedItem
	for rows.Next() {
		t := &TaggedItem{}
		if err := rows.Scan(&t.DriveID, &t.Path, &t.IsDir, &t.Tag); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TagColors(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT tag, color FROM tag_colors")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		out[strings.ToLower(t)] = c
	}
	return out, rows.Err()
}

func (s *Store) SetTagColor(ctx context.Context, tag, color string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO tag_colors (tag, color) VALUES (?, ?) ON CONFLICT(tag) DO UPDATE SET color = excluded.color", tag, color)
	return err
}

// Locks mark a file as being worked on, so others don't edit it at the same time.

type Lock struct {
	DriveID   string `json:"-"`
	Path      string `json:"-"`
	UserID    int64  `json:"userId"`
	Username  string `json:"username"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"createdAt"`
	Source    string `json:"source"` // coffer | smb
}

func (s *Store) PutLock(ctx context.Context, l *Lock) error {
	l.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, "INSERT INTO locks (drive_id, path, user_id, username, note, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		l.DriveID, l.Path, l.UserID, l.Username, l.Note, l.CreatedAt)
	return err
}

func (s *Store) LockOf(ctx context.Context, drive, p string) (*Lock, error) {
	l := &Lock{DriveID: drive, Path: p, Source: "coffer"}
	err := s.db.QueryRowContext(ctx, "SELECT user_id, username, note, created_at FROM locks WHERE drive_id = ? AND path = ?", drive, p).
		Scan(&l.UserID, &l.Username, &l.Note, &l.CreatedAt)
	return l, notFound(err)
}

// LocksUnder returns locks on p and everything below it.
func (s *Store) LocksUnder(ctx context.Context, drive, p string) ([]*Lock, error) {
	q := "SELECT path, user_id, username, note, created_at FROM locks WHERE drive_id = ?"
	args := []any{drive}
	if p != "." {
		q += " AND (path = ? OR path LIKE ? ESCAPE '\\')"
		args = append(args, p, likeEscape(p)+"/%")
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Lock
	for rows.Next() {
		l := &Lock{DriveID: drive, Source: "coffer"}
		if err := rows.Scan(&l.Path, &l.UserID, &l.Username, &l.Note, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) DeleteLock(ctx context.Context, drive, p string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM locks WHERE drive_id = ? AND path = ?", drive, p)
	return err
}

// Activity returns audit entries about p or anything below it on drive,
// newest first. Audit paths are written as "drive:path".
func (s *Store) Activity(ctx context.Context, drive, p string, beforeID int64, limit int) ([]AuditEntry, error) {
	key := drive + ":" + p
	q := "SELECT id, ts, user_id, username, share_id, ip, action, path, detail FROM audit WHERE "
	args := []any{}
	if p == "." {
		// Everything on the drive: "drive:" sorts just before "drive;".
		q += "path >= ? AND path < ?"
		args = append(args, drive+":", drive+";")
	} else {
		q += "(path = ? OR (path >= ? AND path < ?))"
		args = append(args, key, key+"/", key+"0")
	}
	if beforeID > 0 {
		q += " AND id < ?"
		args = append(args, beforeID)
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.UserID, &e.Username, &e.ShareID, &e.IP, &e.Action, &e.Path, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
