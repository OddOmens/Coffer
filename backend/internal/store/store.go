// Package store persists users, sessions, shares and app state in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	// The database holds password hashes and sealed secrets: keep it (and the
	// WAL and shared-memory files SQLite creates with the same mode) private
	// whatever the umask.
	if f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600); err == nil {
		f.Close()
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if info, err := os.Stat(p); err == nil && info.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(p, info.Mode().Perm()&^0o077)
		}
	}
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

var migrations = []string{`
CREATE TABLE users (
	id INTEGER PRIMARY KEY,
	username TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT 'user',
	scope TEXT NOT NULL DEFAULT '.',
	can_write INTEGER NOT NULL DEFAULT 1,
	can_delete INTEGER NOT NULL DEFAULT 1,
	can_share INTEGER NOT NULL DEFAULT 1,
	totp_secret TEXT NOT NULL DEFAULT '',
	totp_enabled INTEGER NOT NULL DEFAULT 0,
	totp_last INTEGER NOT NULL DEFAULT 0,
	disabled INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE sessions (
	id TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	last_seen INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	ip TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE shares (
	id TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	token_enc TEXT NOT NULL,
	owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	path TEXT NOT NULL,
	is_dir INTEGER NOT NULL,
	password_hash TEXT NOT NULL DEFAULT '',
	expires_at INTEGER NOT NULL DEFAULT 0,
	max_downloads INTEGER NOT NULL DEFAULT 0,
	downloads INTEGER NOT NULL DEFAULT 0,
	allow_download INTEGER NOT NULL DEFAULT 1,
	allow_upload INTEGER NOT NULL DEFAULT 0,
	note TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	last_access INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX shares_owner ON shares(owner_id);
CREATE TABLE uploads (
	id TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL DEFAULT 0,
	share_id TEXT NOT NULL DEFAULT '',
	dir TEXT NOT NULL,
	name TEXT NOT NULL,
	size INTEGER NOT NULL,
	overwrite INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE TABLE trash (
	id TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL,
	orig_path TEXT NOT NULL,
	is_dir INTEGER NOT NULL,
	size INTEGER NOT NULL,
	deleted_at INTEGER NOT NULL
);
CREATE TABLE settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE audit (
	id INTEGER PRIMARY KEY,
	ts INTEGER NOT NULL,
	user_id INTEGER NOT NULL DEFAULT 0,
	username TEXT NOT NULL DEFAULT '',
	share_id TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL,
	path TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_ts ON audit(ts);
`, `
CREATE TABLE drives (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	config TEXT NOT NULL DEFAULT '{}',
	sort INTEGER NOT NULL DEFAULT 0,
	disabled INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
INSERT INTO drives (id, name, kind, config, created_at) VALUES ('files', 'Files', 'local', '{"path":""}', strftime('%s','now'));
CREATE TABLE places (
	id TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	drive_id TEXT NOT NULL REFERENCES drives(id) ON DELETE CASCADE,
	path TEXT NOT NULL,
	name TEXT NOT NULL,
	can_write INTEGER NOT NULL DEFAULT 1,
	can_delete INTEGER NOT NULL DEFAULT 1,
	can_share INTEGER NOT NULL DEFAULT 1,
	granted_by INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX places_user ON places(user_id);
INSERT INTO places (id, user_id, drive_id, path, name, can_write, can_delete, can_share, created_at)
	SELECT lower(hex(randomblob(9))), id, 'files', scope, CASE WHEN scope = '.' THEN 'Files' ELSE 'My files' END,
	can_write, can_delete, can_share, created_at FROM users WHERE role != 'admin';
ALTER TABLE shares ADD COLUMN drive_id TEXT NOT NULL DEFAULT 'files';
ALTER TABLE shares ADD COLUMN can_view INTEGER NOT NULL DEFAULT 1;
UPDATE shares SET can_view = allow_download;
ALTER TABLE shares ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE shares ADD COLUMN message TEXT NOT NULL DEFAULT '';
ALTER TABLE shares ADD COLUMN upload_max_mb INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN upload_exts TEXT NOT NULL DEFAULT '';
ALTER TABLE shares ADD COLUMN upload_named INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN notify INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN allowed_nets TEXT NOT NULL DEFAULT '';
ALTER TABLE shares ADD COLUMN view_mode TEXT NOT NULL DEFAULT 'list';
ALTER TABLE trash ADD COLUMN drive_id TEXT NOT NULL DEFAULT 'files';
ALTER TABLE uploads ADD COLUMN drive_id TEXT NOT NULL DEFAULT 'files';
ALTER TABLE uploads ADD COLUMN state TEXT NOT NULL DEFAULT '{}';
ALTER TABLE users ADD COLUMN prefs TEXT NOT NULL DEFAULT '{}';
CREATE TABLE app_tokens (
	id TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	created_at INTEGER NOT NULL,
	last_used INTEGER NOT NULL DEFAULT 0,
	last_ip TEXT NOT NULL DEFAULT ''
);
CREATE TABLE recovery_codes (
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	code_hash TEXT NOT NULL,
	used_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (user_id, code_hash)
);
CREATE TABLE notifications (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	link TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	read_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX notifications_user ON notifications(user_id, id);
`, `
ALTER TABLE shares ADD COLUMN access TEXT NOT NULL DEFAULT 'link';
ALTER TABLE shares ADD COLUMN can_comment INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN latest INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN watermark INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN collection_id TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN quota_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '';
CREATE INDEX audit_path ON audit(path);
CREATE TABLE share_recipients (
	id TEXT PRIMARY KEY,
	share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
	email TEXT NOT NULL COLLATE NOCASE,
	name TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	invited_at INTEGER NOT NULL DEFAULT 0,
	last_seen INTEGER NOT NULL DEFAULT 0,
	views INTEGER NOT NULL DEFAULT 0,
	downloads INTEGER NOT NULL DEFAULT 0,
	revoked INTEGER NOT NULL DEFAULT 0,
	UNIQUE (share_id, email)
);
CREATE TABLE share_codes (
	share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
	email TEXT NOT NULL COLLATE NOCASE,
	code_hash TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	sent_at INTEGER NOT NULL,
	PRIMARY KEY (share_id, email)
);
CREATE TABLE collections (
	id TEXT PRIMARY KEY,
	owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE collection_items (
	collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
	drive_id TEXT NOT NULL,
	path TEXT NOT NULL,
	is_dir INTEGER NOT NULL,
	added_at INTEGER NOT NULL,
	PRIMARY KEY (collection_id, drive_id, path)
);
CREATE TABLE comments (
	id INTEGER PRIMARY KEY,
	drive_id TEXT NOT NULL,
	path TEXT NOT NULL,
	share_id TEXT NOT NULL DEFAULT '',
	parent_id INTEGER NOT NULL DEFAULT 0,
	user_id INTEGER NOT NULL DEFAULT 0,
	author TEXT NOT NULL,
	email TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT 'comment',
	body TEXT NOT NULL DEFAULT '',
	timecode REAL,
	x REAL,
	y REAL,
	resolved INTEGER NOT NULL DEFAULT 0,
	internal INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX comments_file ON comments(drive_id, path);
CREATE TABLE favorites (
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	drive_id TEXT NOT NULL,
	path TEXT NOT NULL,
	is_dir INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (user_id, drive_id, path)
);
CREATE TABLE tags (
	drive_id TEXT NOT NULL,
	path TEXT NOT NULL,
	tag TEXT NOT NULL COLLATE NOCASE,
	is_dir INTEGER NOT NULL,
	created_by INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (drive_id, path, tag)
);
CREATE INDEX tags_tag ON tags(tag);
CREATE TABLE tag_colors (
	tag TEXT PRIMARY KEY COLLATE NOCASE,
	color TEXT NOT NULL
);
CREATE TABLE locks (
	drive_id TEXT NOT NULL,
	path TEXT NOT NULL,
	user_id INTEGER NOT NULL,
	username TEXT NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	PRIMARY KEY (drive_id, path)
);
CREATE TABLE media_jobs (
	drive_id TEXT NOT NULL,
	path TEXT NOT NULL,
	kind TEXT NOT NULL,
	size INTEGER NOT NULL,
	mtime INTEGER NOT NULL,
	state TEXT NOT NULL DEFAULT 'queued',
	attempts INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (drive_id, path, kind)
);
CREATE INDEX media_jobs_state ON media_jobs(state, updated_at);
CREATE TABLE backups (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	source_drive TEXT NOT NULL,
	source_path TEXT NOT NULL DEFAULT '.',
	target_drive TEXT NOT NULL,
	target_path TEXT NOT NULL,
	hour INTEGER NOT NULL DEFAULT 2,
	keep INTEGER NOT NULL DEFAULT 30,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL
);
CREATE TABLE backup_runs (
	id INTEGER PRIMARY KEY,
	backup_id TEXT NOT NULL REFERENCES backups(id) ON DELETE CASCADE,
	started_at INTEGER NOT NULL,
	finished_at INTEGER NOT NULL DEFAULT 0,
	state TEXT NOT NULL DEFAULT 'running',
	files INTEGER NOT NULL DEFAULT 0,
	bytes INTEGER NOT NULL DEFAULT 0,
	uploaded INTEGER NOT NULL DEFAULT 0,
	uploaded_bytes INTEGER NOT NULL DEFAULT 0,
	snapshot TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX backup_runs_backup ON backup_runs(backup_id, id);
`, `
CREATE TABLE backup_items (
	backup_id TEXT NOT NULL REFERENCES backups(id) ON DELETE CASCADE,
	drive_id TEXT NOT NULL,
	path TEXT NOT NULL,
	PRIMARY KEY (backup_id, path)
);
CREATE INDEX backup_items_path ON backup_items(drive_id, path);
`, `
CREATE TABLE mirrors (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	source_drive TEXT NOT NULL,
	source_path TEXT NOT NULL DEFAULT '.',
	target_drive TEXT NOT NULL,
	target_path TEXT NOT NULL DEFAULT '.',
	every_minutes INTEGER NOT NULL DEFAULT 15,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	last_pass INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE mirror_files (
	mirror_id TEXT NOT NULL REFERENCES mirrors(id) ON DELETE CASCADE,
	path TEXT NOT NULL,
	parent TEXT NOT NULL,
	is_dir INTEGER NOT NULL,
	size INTEGER NOT NULL,
	mtime INTEGER NOT NULL,
	removed INTEGER NOT NULL DEFAULT 0,
	at INTEGER NOT NULL,
	PRIMARY KEY (mirror_id, path)
);
CREATE INDEX mirror_files_parent ON mirror_files(mirror_id, parent);
`, `
ALTER TABLE mirrors ADD COLUMN fix_names INTEGER NOT NULL DEFAULT 0;
`}

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() int64 { return time.Now().Unix() }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
