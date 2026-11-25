package store

import (
	"context"
	"encoding/json"
	"strings"
)

// Uploads in progress.

type Upload struct {
	ID        string
	UserID    int64
	ShareID   string
	DriveID   string
	Dir       string // drive-relative target directory
	Name      string // may contain sub-directories for folder uploads
	Size      int64
	Overwrite bool
	State     string // backend upload state (JSON)
	CreatedAt int64
}

func (s *Store) CreateUpload(ctx context.Context, up *Upload) error {
	up.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO uploads (id, user_id, share_id, drive_id, dir, name, size, overwrite, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, up.ID, up.UserID, up.ShareID, up.DriveID, up.Dir, up.Name, up.Size,
		b2i(up.Overwrite), up.State, up.CreatedAt)
	return err
}

func (s *Store) SaveUploadState(ctx context.Context, id, state string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE uploads SET state = ? WHERE id = ?", state, id)
	return err
}

func (s *Store) UploadByID(ctx context.Context, id string) (*Upload, error) {
	up := &Upload{}
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, share_id, drive_id, dir, name, size, overwrite, state, created_at
		FROM uploads WHERE id = ?`, id).
		Scan(&up.ID, &up.UserID, &up.ShareID, &up.DriveID, &up.Dir, &up.Name, &up.Size, &up.Overwrite, &up.State, &up.CreatedAt)
	return up, notFound(err)
}

func (s *Store) CountUploads(ctx context.Context) (n int, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM uploads").Scan(&n)
	return
}

func (s *Store) DeleteUpload(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM uploads WHERE id = ?", id)
	return err
}

// CountShareUploads counts the open uploads through a link.
func (s *Store) CountShareUploads(ctx context.Context, shareID string) (n int, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM uploads WHERE share_id = ?", shareID).Scan(&n)
	return
}

// UploadsOn returns the open uploads to a drive.
func (s *Store) UploadsOn(ctx context.Context, driveID string) ([]*Upload, error) {
	return s.uploadsWhere(ctx, "drive_id = ?", driveID)
}

// UploadsBefore returns uploads created before t.
func (s *Store) UploadsBefore(ctx context.Context, before int64) ([]*Upload, error) {
	return s.uploadsWhere(ctx, "created_at < ?", before)
}

func (s *Store) uploadsWhere(ctx context.Context, where string, arg any) ([]*Upload, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, share_id, drive_id, dir, name, size, overwrite, state, created_at
		FROM uploads WHERE `+where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Upload
	for rows.Next() {
		up := &Upload{}
		if err := rows.Scan(&up.ID, &up.UserID, &up.ShareID, &up.DriveID, &up.Dir, &up.Name, &up.Size, &up.Overwrite,
			&up.State, &up.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, up)
	}
	return out, rows.Err()
}

// Trash.

type TrashItem struct {
	ID        string `json:"id"`
	DriveID   string `json:"-"`
	UserID    int64  `json:"-"`
	Username  string `json:"deletedBy"`
	OrigPath  string `json:"-"`
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
	DeletedAt int64  `json:"deletedAt"`
}

func (s *Store) AddTrash(ctx context.Context, t *TrashItem) error {
	t.DeletedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO trash (id, drive_id, user_id, orig_path, is_dir, size, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.ID, t.DriveID, t.UserID, t.OrigPath, b2i(t.IsDir), t.Size, t.DeletedAt)
	return err
}

const trashCols = `t.id, t.drive_id, t.user_id, COALESCE(u.username, ''), t.orig_path, t.is_dir, t.size, t.deleted_at`

func scanTrash(row interface{ Scan(...any) error }) (*TrashItem, error) {
	t := &TrashItem{}
	err := row.Scan(&t.ID, &t.DriveID, &t.UserID, &t.Username, &t.OrigPath, &t.IsDir, &t.Size, &t.DeletedAt)
	return t, notFound(err)
}

func (s *Store) TrashItems(ctx context.Context) ([]*TrashItem, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+trashCols+
		" FROM trash t LEFT JOIN users u ON u.id = t.user_id ORDER BY t.deleted_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TrashItem
	for rows.Next() {
		t, err := scanTrash(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TrashByID(ctx context.Context, id string) (*TrashItem, error) {
	return scanTrash(s.db.QueryRowContext(ctx, "SELECT "+trashCols+
		" FROM trash t LEFT JOIN users u ON u.id = t.user_id WHERE t.id = ?", id))
}

func (s *Store) DeleteTrash(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM trash WHERE id = ?", id)
	return err
}

// Settings.

type Settings struct {
	AppName      string `json:"appName"`
	AccentColor  string `json:"accentColor"`
	LoginMessage string `json:"loginMessage"`
	LogoType     string `json:"logoType"` // empty when no logo is set
	HideDotfiles bool   `json:"hideDotfiles"`

	ShareRequirePassword   bool  `json:"shareRequirePassword"`
	ShareDefaultExpiryDays int   `json:"shareDefaultExpiryDays"`
	ShareMaxExpiryDays     int   `json:"shareMaxExpiryDays"`
	ShareUploadMaxMB       int64 `json:"shareUploadMaxMB"`
	TrashDays              int   `json:"trashDays"`

	Require2FA      string `json:"require2fa"` // off | admins | all
	WebDAV          bool   `json:"webdav"`
	UpdateCheck     bool   `json:"updateCheck"`
	ScanMinutes     int    `json:"scanMinutes"`   // full rescan interval for local drives
	ScanMinutesS3   int    `json:"scanMinutesS3"` // and for bucket drives
	DefaultTheme    string `json:"defaultTheme"`  // light | dark | system
	DefaultDensity  string `json:"defaultDensity"`
	ShowQuickCreate bool   `json:"showQuickCreate"`

	// Outgoing email for invitations, codes and notifications.
	SMTPHost     string `json:"smtpHost"`
	SMTPPort     int    `json:"smtpPort"`
	SMTPSecurity string `json:"smtpSecurity"` // starttls | tls | none
	SMTPUser     string `json:"smtpUser"`
	SMTPSecret   string `json:"smtpSecret,omitempty"` // sealed password, never sent to browsers
	SMTPFrom     string `json:"smtpFrom"`
	SMTPFromName string `json:"smtpFromName"`

	// Share pages.
	ShareFooter      string `json:"shareFooter"`
	ShareWebsite     string `json:"shareWebsite"`
	ShareHidePowered bool   `json:"shareHidePowered"`

	// Previews made by the media worker.
	MediaAuto   bool `json:"mediaAuto"`   // prepare previews as new files are indexed, not just when opened
	ProxyHeight int  `json:"proxyHeight"` // 720 | 1080

	// Upkeep for a box nobody looks at.
	HealthEmail  bool   `json:"healthEmail"`  // email admins about health problems, not just notify them
	SelfBackup   string `json:"selfBackup"`   // copy Coffer's database and key into backups: auto | on | off
	MediaCacheGB int    `json:"mediaCacheGB"` // size cap of the preview cache; 0 = no cap
}

func DefaultSettings() Settings {
	return Settings{
		AppName:                "Coffer",
		AccentColor:            "#3b5bdb",
		HideDotfiles:           true,
		ShareDefaultExpiryDays: 14,
		ShareUploadMaxMB:       20 * 1024,
		TrashDays:              30,
		Require2FA:             "off",
		WebDAV:                 true,
		UpdateCheck:            true,
		ScanMinutes:            15,
		ScanMinutesS3:          360,
		DefaultTheme:           "system",
		DefaultDensity:         "comfortable",
		ShowQuickCreate:        true,
		SMTPPort:               587,
		SMTPSecurity:           "starttls",
		MediaAuto:              true,
		ProxyHeight:            1080,
		HealthEmail:            true,
		SelfBackup:             "auto",
		MediaCacheGB:           20,
	}
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	st := DefaultSettings()
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'app'").Scan(&raw)
	if err := notFound(err); err == ErrNotFound {
		return st, nil
	} else if err != nil {
		return st, err
	}
	err = json.Unmarshal([]byte(raw), &st)
	// The violet accent was the default before 0.4; follow the new default.
	if strings.EqualFold(st.AccentColor, "#6d5efc") {
		st.AccentColor = DefaultSettings().AccentColor
	}
	return st, err
}

func (s *Store) SaveSettings(ctx context.Context, st Settings) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('app', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return err
}

// Audit log.

type AuditEntry struct {
	ID       int64  `json:"id"`
	TS       int64  `json:"ts"`
	UserID   int64  `json:"userId"`
	Username string `json:"username"`
	ShareID  string `json:"shareId"`
	IP       string `json:"ip"`
	Action   string `json:"action"`
	Path     string `json:"path"`
	Detail   string `json:"detail"`
}

func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit (ts, user_id, username, share_id, ip, action, path, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, now(), e.UserID, e.Username, e.ShareID, e.IP, e.Action, e.Path, e.Detail)
	return err
}

// AuditLog returns entries older than beforeID (0 = newest), newest first.
func (s *Store) AuditLog(ctx context.Context, beforeID int64, limit int, action string) ([]AuditEntry, error) {
	q := "SELECT id, ts, user_id, username, share_id, ip, action, path, detail FROM audit WHERE 1=1"
	var args []any
	if beforeID > 0 {
		q += " AND id < ?"
		args = append(args, beforeID)
	}
	if action != "" {
		q += " AND action = ?"
		args = append(args, action)
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

func (s *Store) PurgeAudit(ctx context.Context, before int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM audit WHERE ts < ?", before)
	return err
}

// Small key/value records kept next to the settings (worker heartbeats).

func (s *Store) PutKV(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, "kv:"+key, value)
	return err
}

func (s *Store) KV(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", "kv:"+key).Scan(&v)
	return v, notFound(err)
}
