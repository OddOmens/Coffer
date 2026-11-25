package store

import (
	"context"
	"encoding/json"
	"strings"
)

// Housekeeping for a box that runs for years: health signals and pruning of
// records that would otherwise grow forever.

// MediaQueue is the media worker's backlog: how many jobs wait and since
// when the oldest has waited (0 when none).
func (s *Store) MediaQueue(ctx context.Context) (queued int, oldest int64, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(MIN(updated_at), 0) FROM media_jobs WHERE state = 'queued'").Scan(&queued, &oldest)
	return
}

// PruneMediaJobs deletes finished and failed jobs last touched before
// before. A missing preview is simply queued again when someone opens it.
func (s *Store) PruneMediaJobs(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM media_jobs WHERE state IN ('done', 'failed') AND updated_at < ?", before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneBackupRuns deletes finished runs that started before before, always
// keeping each backup's newest keep runs and its newest successful one.
func (s *Store) PruneBackupRuns(ctx context.Context, before int64, keep int) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM backup_runs WHERE started_at < ? AND state != 'running'
		AND id IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY backup_id ORDER BY id DESC) AS n FROM backup_runs) WHERE n > ?)
		AND id NOT IN (SELECT MAX(id) FROM backup_runs WHERE state = 'done' GROUP BY backup_id)`, before, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneExpiredShares deletes links that expired before before, with their
// recipients and sign-in codes.
func (s *Store) PruneExpiredShares(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM shares WHERE expires_at > 0 AND expires_at < ?", before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DisabledShares lists the IDs of links that are turned off.
func (s *Store) DisabledShares(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM shares WHERE disabled = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// KVPrefix returns the key/value records whose key starts with prefix, by
// key without the prefix.
func (s *Store) KVPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM settings WHERE substr(key, 1, ?) = ?", len("kv:"+prefix), "kv:"+prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(k, "kv:"+prefix)] = v
	}
	return out, rows.Err()
}

func (s *Store) DeleteKV(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", "kv:"+key)
	return err
}

// GetJSON and PutJSON keep a small JSON value as a key/value record.
func (s *Store) GetJSON(ctx context.Context, key string, v any) error {
	raw, err := s.KV(ctx, key)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), v)
}

func (s *Store) PutJSON(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.PutKV(ctx, key, string(b))
}

// VacuumInto writes a consistent copy of the database to path, which must
// not exist yet.
func (s *Store) VacuumInto(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}
