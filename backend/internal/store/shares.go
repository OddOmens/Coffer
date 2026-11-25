package store

import (
	"context"
	"strings"
)

// Share notification flags.
const (
	NotifyOpen     = 1
	NotifyDownload = 2
	NotifyUpload   = 4
)

type Share struct {
	ID           string
	TokenHash    string
	TokenEnc     string
	OwnerID      int64
	OwnerName    string
	DriveID      string
	Path         string // drive-relative
	IsDir        bool
	PasswordHash string
	ExpiresAt    int64 // 0 = never
	MaxDownloads int64 // 0 = unlimited
	Downloads    int64
	CanView      bool // list and preview
	CanDownload  bool // download originals and zips
	CanUpload    bool
	Disabled     bool
	Title        string // shown to recipients instead of the item name
	Message      string // shown to recipients
	Note         string // only the owner sees this
	UploadMaxMB  int64  // 0 = global limit
	UploadExts   string // comma separated, empty = any
	UploadNamed  bool   // uploaders give their name; files go to a folder named after them
	Notify       int    // Notify* flags
	AllowedNets  string // comma separated CIDRs, empty = anywhere
	ViewMode     string // list | grid | gallery
	Access       string // link (anyone with it) | email (anyone who verifies an email) | invited (listed recipients only)
	CanComment   bool   // recipients may comment, approve or ask for changes
	Latest       bool   // a file link follows the newest version (name_v12 → name_v13)
	Watermark    bool   // stamp previews with the viewer's email
	CollectionID string // set for links to a collection; DriveID and Path are then empty
	CreatedAt    int64
	LastAccess   int64
}

// Active reports whether the share can still be used at unix time t.
func (sh *Share) Active(t int64) bool {
	if sh.Disabled || (sh.ExpiresAt != 0 && t >= sh.ExpiresAt) {
		return false
	}
	return sh.MaxDownloads == 0 || sh.Downloads < sh.MaxDownloads
}

func (sh *Share) Exts() []string {
	var out []string
	for _, e := range strings.Split(sh.UploadExts, ",") {
		if e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), ".")); e != "" {
			out = append(out, e)
		}
	}
	return out
}

const shareCols = `s.id, s.token_hash, s.token_enc, s.owner_id, u.username, s.drive_id, s.path, s.is_dir, s.password_hash,
	s.expires_at, s.max_downloads, s.downloads, s.can_view, s.allow_download, s.allow_upload, s.disabled, s.title,
	s.message, s.note, s.upload_max_mb, s.upload_exts, s.upload_named, s.notify, s.allowed_nets, s.view_mode,
	s.created_at, s.last_access, s.access, s.can_comment, s.latest, s.watermark, s.collection_id`

func scanShare(row interface{ Scan(...any) error }) (*Share, error) {
	sh := &Share{}
	err := row.Scan(&sh.ID, &sh.TokenHash, &sh.TokenEnc, &sh.OwnerID, &sh.OwnerName, &sh.DriveID, &sh.Path, &sh.IsDir,
		&sh.PasswordHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.Downloads, &sh.CanView, &sh.CanDownload, &sh.CanUpload,
		&sh.Disabled, &sh.Title, &sh.Message, &sh.Note, &sh.UploadMaxMB, &sh.UploadExts, &sh.UploadNamed, &sh.Notify,
		&sh.AllowedNets, &sh.ViewMode, &sh.CreatedAt, &sh.LastAccess, &sh.Access, &sh.CanComment, &sh.Latest,
		&sh.Watermark, &sh.CollectionID)
	if err != nil {
		return nil, notFound(err)
	}
	return sh, nil
}

func (s *Store) CreateShare(ctx context.Context, sh *Share) error {
	sh.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO shares (id, token_hash, token_enc, owner_id, drive_id, path, is_dir,
		password_hash, expires_at, max_downloads, can_view, allow_download, allow_upload, disabled, title, message, note,
		upload_max_mb, upload_exts, upload_named, notify, allowed_nets, view_mode, created_at, access, can_comment, latest,
		watermark, collection_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sh.ID, sh.TokenHash, sh.TokenEnc, sh.OwnerID, sh.DriveID, sh.Path, b2i(sh.IsDir), sh.PasswordHash, sh.ExpiresAt,
		sh.MaxDownloads, b2i(sh.CanView), b2i(sh.CanDownload), b2i(sh.CanUpload), b2i(sh.Disabled), sh.Title, sh.Message,
		sh.Note, sh.UploadMaxMB, sh.UploadExts, b2i(sh.UploadNamed), sh.Notify, sh.AllowedNets, sh.ViewMode, sh.CreatedAt,
		sh.Access, b2i(sh.CanComment), b2i(sh.Latest), b2i(sh.Watermark), sh.CollectionID)
	return err
}

func (s *Store) UpdateShare(ctx context.Context, sh *Share) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET password_hash = ?, expires_at = ?, max_downloads = ?, can_view = ?,
		allow_download = ?, allow_upload = ?, disabled = ?, title = ?, message = ?, note = ?, upload_max_mb = ?,
		upload_exts = ?, upload_named = ?, notify = ?, allowed_nets = ?, view_mode = ?, access = ?, can_comment = ?,
		latest = ?, watermark = ? WHERE id = ?`,
		sh.PasswordHash, sh.ExpiresAt, sh.MaxDownloads, b2i(sh.CanView), b2i(sh.CanDownload), b2i(sh.CanUpload),
		b2i(sh.Disabled), sh.Title, sh.Message, sh.Note, sh.UploadMaxMB, sh.UploadExts, b2i(sh.UploadNamed), sh.Notify,
		sh.AllowedNets, sh.ViewMode, sh.Access, b2i(sh.CanComment), b2i(sh.Latest), b2i(sh.Watermark), sh.ID)
	return err
}

const shareFrom = " FROM shares s JOIN users u ON u.id = s.owner_id"

func (s *Store) ShareByID(ctx context.Context, id string) (*Share, error) {
	return scanShare(s.db.QueryRowContext(ctx, "SELECT "+shareCols+shareFrom+" WHERE s.id = ?", id))
}

func (s *Store) ShareByToken(ctx context.Context, tokenHash string) (*Share, error) {
	return scanShare(s.db.QueryRowContext(ctx, "SELECT "+shareCols+shareFrom+" WHERE s.token_hash = ?", tokenHash))
}

// Shares lists shares, all of them when ownerID is 0.
func (s *Store) Shares(ctx context.Context, ownerID int64) ([]*Share, error) {
	q := "SELECT " + shareCols + shareFrom
	args := []any{}
	if ownerID != 0 {
		q += " WHERE s.owner_id = ?"
		args = append(args, ownerID)
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY s.created_at DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *Store) CountActiveShares(ctx context.Context) (n int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM shares WHERE disabled = 0 AND (expires_at = 0 OR expires_at > ?)
		AND (max_downloads = 0 OR downloads < max_downloads)`, now()).Scan(&n)
	return
}

func (s *Store) DeleteShare(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM shares WHERE id = ?", id)
	return err
}

// CountDownload atomically consumes one download. It returns false when the
// share has reached its limit.
func (s *Store) CountDownload(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE shares SET downloads = downloads + 1, last_access = ?
		WHERE id = ? AND (max_downloads = 0 OR downloads < max_downloads)`, now(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) TouchShare(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE shares SET last_access = ? WHERE id = ?", now(), id)
	return err
}

// Repath rewrites share, place and backup paths after a rename or move so
// links, grants and backups keep working.
func (s *Store) Repath(ctx context.Context, drive, from, to string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range repathTables {
		rows, err := tx.QueryContext(ctx, "SELECT rowid, path FROM "+table+
			" WHERE drive_id = ? AND (path = ? OR path LIKE ? ESCAPE '\\')", drive, from, likeEscape(from)+"/%")
		if err != nil {
			return err
		}
		type move struct {
			id   int64
			path string
		}
		var moves []move
		for rows.Next() {
			var m move
			if err := rows.Scan(&m.id, &m.path); err != nil {
				rows.Close()
				return err
			}
			if m.path == from || strings.HasPrefix(m.path, from+"/") {
				moves = append(moves, move{m.id, to + m.path[len(from):]})
			}
		}
		rows.Close()
		for _, m := range moves {
			// OR REPLACE: a favourite or tag already at the destination wins.
			if _, err := tx.ExecContext(ctx, "UPDATE OR REPLACE "+table+" SET path = ? WHERE rowid = ?", m.path, m.id); err != nil {
				return err
			}
		}
	}
	// Backups follow their folders; snapshots hold paths relative to the
	// backed-up folder, so nothing is uploaded again.
	rows, err := tx.QueryContext(ctx, "SELECT id, source_drive, source_path, target_drive, target_path FROM backups")
	if err != nil {
		return err
	}
	var backups [][5]string
	for rows.Next() {
		var b [5]string
		if err := rows.Scan(&b[0], &b[1], &b[2], &b[3], &b[4]); err != nil {
			rows.Close()
			return err
		}
		backups = append(backups, b)
	}
	rows.Close()
	moved := func(d, p string) string {
		if d == drive && (p == from || strings.HasPrefix(p, from+"/")) {
			return to + p[len(from):]
		}
		return p
	}
	for _, b := range backups {
		sp, tp := moved(b[1], b[2]), moved(b[3], b[4])
		if sp == b[2] && tp == b[4] {
			continue
		}
		if _, err := tx.ExecContext(ctx, "UPDATE backups SET source_path = ?, target_path = ? WHERE id = ?", sp, tp, b[0]); err != nil {
			return err
		}
	}
	// Generated previews are keyed by path; the worker remakes them on demand.
	if _, err := tx.ExecContext(ctx, "DELETE FROM media_jobs WHERE drive_id = ? AND (path = ? OR path LIKE ? ESCAPE '\\')",
		drive, from, likeEscape(from)+"/%"); err != nil {
		return err
	}
	return tx.Commit()
}

// repathTables hold drive_id + path columns that follow renames and moves.
var repathTables = []string{"shares", "places", "collection_items", "comments", "favorites", "tags", "locks", "backup_items"}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}
