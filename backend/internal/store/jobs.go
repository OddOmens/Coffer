package store

import (
	"context"
	"database/sql"
	"errors"
)

// Media jobs: preview renditions made by the media worker.

const (
	MediaProxy = "proxy" // H.264 MP4 for browsers that can't play the original
	MediaStill = "still" // JPEG of a HEIC, RAW or video frame for previews and thumbnails
)

type MediaJob struct {
	DriveID   string `json:"driveId"`
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	MTime     int64  `json:"mtime"`
	State     string `json:"state"` // queued | running | done | failed
	Attempts  int    `json:"attempts"`
	Error     string `json:"error"`
	UpdatedAt int64  `json:"updatedAt"`
}

const mediaCols = "drive_id, path, kind, size, mtime, state, attempts, error, updated_at"

func scanMedia(row interface{ Scan(...any) error }) (*MediaJob, error) {
	j := &MediaJob{}
	err := row.Scan(&j.DriveID, &j.Path, &j.Kind, &j.Size, &j.MTime, &j.State, &j.Attempts, &j.Error, &j.UpdatedAt)
	return j, notFound(err)
}

// EnqueueMedia queues a rendition unless an up-to-date one exists or is on
// its way. A changed file (size or mtime) is queued again.
func (s *Store) EnqueueMedia(ctx context.Context, drive, p, kind string, size, mtime int64) (*MediaJob, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO media_jobs (drive_id, path, kind, size, mtime, state, updated_at)
		VALUES (?, ?, ?, ?, ?, 'queued', ?)
		ON CONFLICT(drive_id, path, kind) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, state = 'queued',
			attempts = 0, error = '', updated_at = excluded.updated_at
		WHERE media_jobs.size != excluded.size OR media_jobs.mtime != excluded.mtime OR media_jobs.state = 'done'`,
		drive, p, kind, size, mtime, now())
	if err != nil {
		return nil, err
	}
	return s.MediaJob(ctx, drive, p, kind)
}

func (s *Store) MediaJob(ctx context.Context, drive, p, kind string) (*MediaJob, error) {
	return scanMedia(s.db.QueryRowContext(ctx, "SELECT "+mediaCols+" FROM media_jobs WHERE drive_id = ? AND path = ? AND kind = ?", drive, p, kind))
}

// ClaimMedia takes the oldest queued job, also re-taking jobs left running
// by a worker that died (staleBefore).
func (s *Store) ClaimMedia(ctx context.Context, staleBefore int64) (*MediaJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	j, err := scanMedia(tx.QueryRowContext(ctx, "SELECT "+mediaCols+` FROM media_jobs
		WHERE state = 'queued' OR (state = 'running' AND updated_at < ?) ORDER BY updated_at LIMIT 1`, staleBefore))
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE media_jobs SET state = 'running', attempts = attempts + 1, updated_at = ? WHERE drive_id = ? AND path = ? AND kind = ?",
		now(), j.DriveID, j.Path, j.Kind); err != nil {
		return nil, err
	}
	j.State = "running"
	j.Attempts++
	return j, tx.Commit()
}

func (s *Store) FinishMedia(ctx context.Context, j *MediaJob, state, msg string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media_jobs SET state = ?, error = ?, updated_at = ? WHERE drive_id = ? AND path = ? AND kind = ? AND size = ? AND mtime = ?",
		state, msg, now(), j.DriveID, j.Path, j.Kind, j.Size, j.MTime)
	return err
}

func (s *Store) MediaStats(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT state, COUNT(*) FROM media_jobs GROUP BY state")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// Backups.

type Backup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SourceDrive string `json:"sourceDrive"`
	SourcePath  string `json:"sourcePath"`
	// Items, when set, are the only files and folders (drive paths inside
	// SourcePath) the backup takes.
	Items       []string   `json:"items"`
	TargetDrive string     `json:"targetDrive"`
	TargetPath  string     `json:"targetPath"`
	Hour        int        `json:"hour"` // local hour of day the nightly run starts
	Keep        int        `json:"keep"` // snapshots kept
	Enabled     bool       `json:"enabled"`
	CreatedAt   int64      `json:"createdAt"`
	LastRun     *BackupRun `json:"lastRun,omitempty"`
}

type BackupRun struct {
	ID            int64  `json:"id"`
	BackupID      string `json:"-"`
	StartedAt     int64  `json:"startedAt"`
	FinishedAt    int64  `json:"finishedAt"`
	State         string `json:"state"` // running | done | failed
	Files         int64  `json:"files"`
	Bytes         int64  `json:"bytes"`
	Uploaded      int64  `json:"uploaded"`
	UploadedBytes int64  `json:"uploadedBytes"`
	Snapshot      string `json:"snapshot"` // snapshot name in the target
	Error         string `json:"error"`
}

const backupCols = "id, name, source_drive, source_path, target_drive, target_path, hour, keep, enabled, created_at"

func scanBackup(row interface{ Scan(...any) error }) (*Backup, error) {
	b := &Backup{}
	err := row.Scan(&b.ID, &b.Name, &b.SourceDrive, &b.SourcePath, &b.TargetDrive, &b.TargetPath, &b.Hour, &b.Keep, &b.Enabled, &b.CreatedAt)
	return b, notFound(err)
}

func (s *Store) CreateBackup(ctx context.Context, b *Backup) error {
	b.CreatedAt = now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO backups ("+backupCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		b.ID, b.Name, b.SourceDrive, b.SourcePath, b.TargetDrive, b.TargetPath, b.Hour, b.Keep, b2i(b.Enabled), b.CreatedAt); err != nil {
		return err
	}
	if err := insertItems(ctx, tx, b); err != nil {
		return err
	}
	return tx.Commit()
}

// SetBackupItems replaces what a backup takes.
func (s *Store) SetBackupItems(ctx context.Context, b *Backup) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM backup_items WHERE backup_id = ?", b.ID); err != nil {
		return err
	}
	if err := insertItems(ctx, tx, b); err != nil {
		return err
	}
	return tx.Commit()
}

func insertItems(ctx context.Context, tx *sql.Tx, b *Backup) error {
	for _, p := range b.Items {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO backup_items (backup_id, drive_id, path) VALUES (?, ?, ?)", b.ID, b.SourceDrive, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) backupItems(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT path FROM backup_items WHERE backup_id = ? ORDER BY path", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateBackup(ctx context.Context, b *Backup) error {
	_, err := s.db.ExecContext(ctx, "UPDATE backups SET name = ?, hour = ?, keep = ?, enabled = ? WHERE id = ?", b.Name, b.Hour, b.Keep, b2i(b.Enabled), b.ID)
	return err
}

func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM backups WHERE id = ?", id)
	return err
}

func (s *Store) BackupByID(ctx context.Context, id string) (*Backup, error) {
	b, err := scanBackup(s.db.QueryRowContext(ctx, "SELECT "+backupCols+" FROM backups WHERE id = ?", id))
	if err != nil {
		return nil, err
	}
	b.LastRun, _ = s.lastRun(ctx, id)
	if b.Items, err = s.backupItems(ctx, id); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Store) Backups(ctx context.Context) ([]*Backup, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+backupCols+" FROM backups ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	var out []*Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, b)
	}
	rows.Close()
	for _, b := range out {
		b.LastRun, _ = s.lastRun(ctx, b.ID)
		if b.Items, err = s.backupItems(ctx, b.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

const runCols = "id, backup_id, started_at, finished_at, state, files, bytes, uploaded, uploaded_bytes, snapshot, error"

func scanRun(row interface{ Scan(...any) error }) (*BackupRun, error) {
	r := &BackupRun{}
	err := row.Scan(&r.ID, &r.BackupID, &r.StartedAt, &r.FinishedAt, &r.State, &r.Files, &r.Bytes, &r.Uploaded, &r.UploadedBytes, &r.Snapshot, &r.Error)
	return r, notFound(err)
}

// LastGoodSnapshot is the snapshot of a backup's newest successful run, and
// when that run started. It returns "" when no run has succeeded yet.
func (s *Store) LastGoodSnapshot(ctx context.Context, id string) (string, int64, error) {
	var snap string
	var at int64
	err := s.db.QueryRowContext(ctx, "SELECT snapshot, started_at FROM backup_runs WHERE backup_id = ? AND state = 'done' AND snapshot != '' ORDER BY id DESC LIMIT 1", id).Scan(&snap, &at)
	if errors.Is(notFound(err), ErrNotFound) {
		return "", 0, nil
	}
	return snap, at, err
}

func (s *Store) lastRun(ctx context.Context, id string) (*BackupRun, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, "SELECT "+runCols+" FROM backup_runs WHERE backup_id = ? ORDER BY id DESC LIMIT 1", id))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return r, err
}

func (s *Store) StartBackupRun(ctx context.Context, id string) (*BackupRun, error) {
	r := &BackupRun{BackupID: id, StartedAt: now(), State: "running"}
	res, err := s.db.ExecContext(ctx, "INSERT INTO backup_runs (backup_id, started_at) VALUES (?, ?)", id, r.StartedAt)
	if err != nil {
		return nil, err
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

func (s *Store) SaveBackupRun(ctx context.Context, r *BackupRun) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backup_runs SET finished_at = ?, state = ?, files = ?, bytes = ?, uploaded = ?,
		uploaded_bytes = ?, snapshot = ?, error = ? WHERE id = ?`,
		r.FinishedAt, r.State, r.Files, r.Bytes, r.Uploaded, r.UploadedBytes, r.Snapshot, r.Error, r.ID)
	return err
}

func (s *Store) BackupRuns(ctx context.Context, id string, limit int) ([]*BackupRun, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+runCols+" FROM backup_runs WHERE backup_id = ? ORDER BY id DESC LIMIT ?", id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*BackupRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AbandonBackupRuns marks runs interrupted by a restart as failed.
func (s *Store) AbandonBackupRuns(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE backup_runs SET state = 'failed', error = 'interrupted by a restart', finished_at = ? WHERE state = 'running'", now())
	return err
}

// ResetMedia queues a failed rendition again. Anyone with a link can ask, so
// finished ones are left alone and a failure is retried at most every
// mediaRetryAfter seconds; otherwise retries could keep the worker busy forever.
func (s *Store) ResetMedia(ctx context.Context, drive, p, kind string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media_jobs SET state = 'queued', attempts = 0, error = '', updated_at = ? WHERE drive_id = ? AND path = ? AND kind = ? AND state = 'failed' AND updated_at <= ?",
		now(), drive, p, kind, now()-mediaRetryAfter)
	return err
}

const mediaRetryAfter = 10 * 60
