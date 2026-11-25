// Package index keeps a searchable catalogue of every drive: names for
// instant substring search (SQLite FTS5 trigram), and recursive folder sizes.
//
// Coffer updates it immediately for changes it makes itself, and rescans
// each drive periodically to pick up changes made over SMB or in the bucket.
package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oddomens/coffer/internal/files"

	_ "modernc.org/sqlite"
)

type Index struct {
	db       *sql.DB
	mu       sync.Mutex
	scanning map[string]bool
}

const schema = `
CREATE TABLE IF NOT EXISTS entries (
	id INTEGER PRIMARY KEY,
	drive TEXT NOT NULL,
	path TEXT NOT NULL,
	parent TEXT NOT NULL,
	name TEXT NOT NULL,
	is_dir INTEGER NOT NULL,
	size INTEGER NOT NULL,
	items INTEGER NOT NULL DEFAULT 0,
	mtime INTEGER NOT NULL,
	kind TEXT NOT NULL,
	hidden INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS entries_path ON entries(drive, path);
CREATE INDEX IF NOT EXISTS entries_parent ON entries(drive, parent);
CREATE INDEX IF NOT EXISTS entries_recent ON entries(drive, is_dir, mtime);
CREATE VIRTUAL TABLE IF NOT EXISTS names USING fts5(name, content='entries', content_rowid='id', tokenize='trigram case_sensitive 0');
CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
	INSERT INTO names(rowid, name) VALUES (new.id, new.name);
END;
CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
	INSERT INTO names(names, rowid, name) VALUES ('delete', old.id, old.name);
END;
CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE OF name ON entries BEGIN
	INSERT INTO names(names, rowid, name) VALUES ('delete', old.id, old.name);
	INSERT INTO names(rowid, name) VALUES (new.id, new.name);
END;
CREATE TABLE IF NOT EXISTS scans (
	drive TEXT PRIMARY KEY,
	started INTEGER NOT NULL,
	finished INTEGER NOT NULL DEFAULT 0,
	files INTEGER NOT NULL DEFAULT 0,
	dirs INTEGER NOT NULL DEFAULT 0,
	bytes INTEGER NOT NULL DEFAULT 0,
	ms INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT ''
);
`

func Open(path string) (*Index, error) {
	// The index names every file on every drive: keep it owner-only. SQLite
	// gives the -wal and -shm files the database file's mode, so creating it
	// as 0600 first covers those too; existing files are tightened.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "busy_timeout(15000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(6)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("index schema: %w", err)
	}
	return &Index{db: db, scanning: map[string]bool{}}, nil
}

func (ix *Index) Close() error { return ix.db.Close() }

func hiddenPath(p string) bool {
	if p == "." {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if files.Hidden(seg, true) {
			return true
		}
	}
	return false
}

func parentOf(p string) string {
	if p == "." {
		return ""
	}
	return files.Parent(p)
}

// prefixRange returns bounds matching every path strictly below p.
func prefixRange(p string) (lo, hi string) {
	return p + "/", p + "0" // '0' is the byte after '/'
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func insert(ctx context.Context, x execer, drive string, e files.Entry, size, items int64) (int64, error) {
	var id int64
	err := x.QueryRowContext(ctx, `INSERT INTO entries (drive, path, parent, name, is_dir, size, items, mtime, kind, hidden)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(drive, path) DO UPDATE SET size = excluded.size, items = excluded.items,
		mtime = excluded.mtime, is_dir = excluded.is_dir, kind = excluded.kind RETURNING id`,
		drive, e.Path, parentOf(e.Path), e.Name, e.IsDir, size, items, e.ModTime, e.Kind, hiddenPath(e.Path)).Scan(&id)
	return id, err
}

// Scanning reports whether a full scan of drive is running.
func (ix *Index) Scanning(drive string) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.scanning[drive]
}

var ErrBusy = errors.New("a scan of this drive is already running")

// Scan rebuilds the index for a drive. It builds a new generation alongside
// the old one, so search keeps working while it runs, then swaps them.
func (ix *Index) Scan(ctx context.Context, drive string, d files.Drive) error {
	ix.mu.Lock()
	if ix.scanning[drive] {
		ix.mu.Unlock()
		return ErrBusy
	}
	ix.scanning[drive] = true
	ix.mu.Unlock()
	defer func() {
		ix.mu.Lock()
		delete(ix.scanning, drive)
		ix.mu.Unlock()
	}()

	start := time.Now()
	_, _ = ix.db.ExecContext(ctx, `INSERT INTO scans (drive, started) VALUES (?, ?)
		ON CONFLICT(drive) DO UPDATE SET started = excluded.started`, drive, start.Unix())

	gen := drive + "\x00next"
	_, _ = ix.db.ExecContext(ctx, "DELETE FROM entries WHERE drive = ?", gen)
	stats, err := ix.build(ctx, gen, d, ".")
	if err == nil {
		err = ix.swap(ctx, drive, gen)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
		_, _ = ix.db.ExecContext(context.WithoutCancel(ctx), "DELETE FROM entries WHERE drive = ?", gen)
	}
	_, _ = ix.db.ExecContext(context.WithoutCancel(ctx), `UPDATE scans SET finished = ?, files = ?, dirs = ?, bytes = ?,
		ms = ?, error = ? WHERE drive = ?`, time.Now().Unix(), stats.files, stats.dirs, stats.bytes,
		time.Since(start).Milliseconds(), msg, drive)
	return err
}

func (ix *Index) swap(ctx context.Context, drive, gen string) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM entries WHERE drive = ?", drive); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE entries SET drive = ? WHERE drive = ?", drive, gen); err != nil {
		return err
	}
	return tx.Commit()
}

type buildStats struct{ files, dirs, bytes int64 }

// build walks root on d into the index under key drive, computing folder
// sizes bottom-up. Inserts are committed in batches so other writers interleave.
func (ix *Index) build(ctx context.Context, drive string, d files.Drive, root string) (buildStats, error) {
	type open struct {
		id          int64
		path        string
		size, items int64
	}
	var (
		st    buildStats
		stack []*open
		tx    *sql.Tx
		n     int
	)
	begin := func() error {
		var err error
		tx, err = ix.db.BeginTx(ctx, nil)
		return err
	}
	if err := begin(); err != nil {
		return st, err
	}
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()
	closeDir := func() error {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, err := tx.ExecContext(ctx, "UPDATE entries SET size = ?, items = ? WHERE id = ?", top.size, top.items, top.id); err != nil {
			return err
		}
		if len(stack) > 0 {
			p := stack[len(stack)-1]
			p.size += top.size
			p.items += top.items
		}
		return nil
	}
	err := d.Walk(ctx, root, func(e files.Entry) error {
		for len(stack) > 0 {
			top := stack[len(stack)-1]
			if top.path == "." || strings.HasPrefix(e.Path, top.path+"/") {
				break
			}
			if err := closeDir(); err != nil {
				return err
			}
		}
		if e.Path == "." {
			e.Name = ""
		}
		id, err := insert(ctx, tx, drive, e, e.Size, 0)
		if err != nil {
			return err
		}
		if e.IsDir {
			st.dirs++
			stack = append(stack, &open{id: id, path: e.Path})
		} else {
			st.files++
			st.bytes += e.Size
			if len(stack) > 0 {
				stack[len(stack)-1].size += e.Size
				stack[len(stack)-1].items++
			}
		}
		if n++; n%5000 == 0 {
			if err := tx.Commit(); err != nil {
				return err
			}
			return begin()
		}
		return nil
	})
	if err != nil {
		return st, err
	}
	for len(stack) > 0 {
		if err := closeDir(); err != nil {
			return st, err
		}
	}
	err = tx.Commit()
	tx = nil
	return st, err
}

// adjust adds deltas to every ancestor of p (excluding p itself).
func adjust(ctx context.Context, x execer, drive, p string, dSize, dItems int64) error {
	if dSize == 0 && dItems == 0 {
		return nil
	}
	for a := parentOf(p); a != ""; a = parentOf(a) {
		if _, err := x.ExecContext(ctx, "UPDATE entries SET size = size + ?, items = items + ? WHERE drive = ? AND path = ?",
			dSize, dItems, drive, a); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Index) row(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, drive, p string) (isDir bool, size, items int64, ok bool) {
	err := q.QueryRowContext(ctx, "SELECT is_dir, size, items FROM entries WHERE drive = ? AND path = ?", drive, p).
		Scan(&isDir, &size, &items)
	return isDir, size, items, err == nil
}

func itemsOf(isDir bool, items int64) int64 {
	if isDir {
		return items
	}
	return 1
}

// Remove drops p and everything below it.
func (ix *Index) Remove(ctx context.Context, drive, p string) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	isDir, size, items, ok := ix.row(ctx, tx, drive, p)
	if !ok {
		return nil
	}
	lo, hi := prefixRange(p)
	if _, err := tx.ExecContext(ctx, "DELETE FROM entries WHERE drive = ? AND (path = ? OR (path >= ? AND path < ?))", drive, p, lo, hi); err != nil {
		return err
	}
	if err := adjust(ctx, tx, drive, p, -size, -itemsOf(isDir, items)); err != nil {
		return err
	}
	return tx.Commit()
}

// Move re-paths p and its subtree after a rename or move.
func (ix *Index) Move(ctx context.Context, drive, from, to string) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	isDir, size, items, ok := ix.row(ctx, tx, drive, from)
	if !ok {
		return nil
	}
	lo, hi := prefixRange(from)
	rows, err := tx.QueryContext(ctx, "SELECT id, path FROM entries WHERE drive = ? AND (path = ? OR (path >= ? AND path < ?))", drive, from, lo, hi)
	if err != nil {
		return err
	}
	type mv struct {
		id   int64
		path string
	}
	var mvs []mv
	for rows.Next() {
		var m mv
		if err := rows.Scan(&m.id, &m.path); err != nil {
			rows.Close()
			return err
		}
		mvs = append(mvs, m)
	}
	rows.Close()
	// Anything already indexed at the destination is stale.
	lo2, hi2 := prefixRange(to)
	if _, err := tx.ExecContext(ctx, "DELETE FROM entries WHERE drive = ? AND (path = ? OR (path >= ? AND path < ?))", drive, to, lo2, hi2); err != nil {
		return err
	}
	for _, m := range mvs {
		np := to + m.path[len(from):]
		name := m.path[strings.LastIndex(m.path, "/")+1:]
		if m.path == from {
			name = to[strings.LastIndex(to, "/")+1:]
		}
		if _, err := tx.ExecContext(ctx, "UPDATE entries SET path = ?, parent = ?, name = ?, hidden = ? WHERE id = ?",
			np, parentOf(np), name, hiddenPath(np), m.id); err != nil {
			return err
		}
	}
	n := itemsOf(isDir, items)
	if err := adjust(ctx, tx, drive, from, -size, -n); err != nil {
		return err
	}
	if err := adjust(ctx, tx, drive, to, size, n); err != nil {
		return err
	}
	return tx.Commit()
}

// Sync refreshes the direct children of dir from the drive, indexing any new
// sub-folders fully, and fixes the sizes of dir's ancestors.
func (ix *Index) Sync(ctx context.Context, drive string, d files.Drive, dir string) error {
	if _, _, _, ok := ix.row(ctx, ix.db, drive, dir); !ok && dir != "." {
		// Unknown folder: make sure its parent knows about it first.
		return ix.Sync(ctx, drive, d, parentOf(dir))
	}
	live, err := d.List(ctx, dir, false)
	if errors.Is(err, fs.ErrNotExist) {
		return ix.Remove(ctx, drive, dir)
	}
	if err != nil {
		return err
	}
	type known struct {
		isDir       bool
		size, items int64
	}
	have := map[string]known{}
	rows, err := ix.db.QueryContext(ctx, "SELECT path, is_dir, size, items FROM entries WHERE drive = ? AND parent = ?", drive, dir)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p string
		var k known
		if err := rows.Scan(&p, &k.isDir, &k.size, &k.items); err != nil {
			rows.Close()
			return err
		}
		have[p] = k
	}
	rows.Close()

	seen := map[string]bool{}
	for _, e := range live {
		seen[e.Path] = true
		k, ok := have[e.Path]
		switch {
		case ok && k.isDir == e.IsDir && e.IsDir:
			// Unchanged folder: its subtree is assumed current.
		case ok && k.isDir == e.IsDir:
			if k.size != e.Size {
				if err := ix.setFile(ctx, drive, e, k.size); err != nil {
					return err
				}
			}
		default:
			if ok {
				if err := ix.Remove(ctx, drive, e.Path); err != nil {
					return err
				}
			}
			if err := ix.add(ctx, drive, d, e); err != nil {
				return err
			}
		}
	}
	for p := range have {
		if !seen[p] {
			if err := ix.Remove(ctx, drive, p); err != nil {
				return err
			}
		}
	}
	return nil
}

func (ix *Index) setFile(ctx context.Context, drive string, e files.Entry, oldSize int64) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := insert(ctx, tx, drive, e, e.Size, 0); err != nil {
		return err
	}
	if err := adjust(ctx, tx, drive, e.Path, e.Size-oldSize, 0); err != nil {
		return err
	}
	return tx.Commit()
}

// add indexes a new entry (a whole subtree for folders) and grows ancestors.
func (ix *Index) add(ctx context.Context, drive string, d files.Drive, e files.Entry) error {
	if !e.IsDir {
		tx, err := ix.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := insert(ctx, tx, drive, e, e.Size, 0); err != nil {
			return err
		}
		if err := adjust(ctx, tx, drive, e.Path, e.Size, 1); err != nil {
			return err
		}
		return tx.Commit()
	}
	st, err := ix.build(ctx, drive, d, e.Path)
	if err != nil {
		return err
	}
	return adjust(ctx, ix.db, drive, e.Path, st.bytes, st.files)
}

// DropDrive forgets everything about a drive.
func (ix *Index) DropDrive(ctx context.Context, drive string) error {
	_, err := ix.db.ExecContext(ctx, "DELETE FROM entries WHERE drive = ?", drive)
	if err == nil {
		_, err = ix.db.ExecContext(ctx, "DELETE FROM scans WHERE drive = ?", drive)
	}
	return err
}
