package server

import (
	"context"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oddomens/coffer/internal/backup"
	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/store"
)

// Backup marks on folder listings: which files and folders a backup covers,
// and whether their current version is in its latest snapshot. Files are
// checked against the snapshot itself (path, size and modification time),
// not against the run's date: a file copied in today often keeps an old
// modification time.

type backupMark struct {
	// done: in the latest snapshot as it is now. pending: covered, but new or
	// changed since. partial: a folder with backed-up folders inside.
	// unknown: covered, but the snapshot couldn't be read just now.
	State     string `json:"state"`
	Backup    string `json:"backup"`
	Drive     string `json:"drive"` // where the copies are kept
	Cloud     bool   `json:"cloud"`
	Encrypted bool   `json:"encrypted,omitempty"`
	Last      int64  `json:"last,omitempty"` // when the latest good run started
}

type backupCover struct {
	b      *store.Backup
	target *mounted
	at     int64
	snap   *snapIndex // nil before the first good run, or when unreadable
	failed bool
}

// snapIndex is what one snapshot holds, by path relative to the backed-up
// folder.
type snapIndex struct {
	name  string
	files map[string][2]int64 // size, modification time
	dirs  map[string]bool
}

var snapCache = struct {
	sync.Mutex
	m map[string]*snapIndex // by backup ID
}{m: map[string]*snapIndex{}}

// backupCovers lists the enabled backups of a drive, each with its latest
// good snapshot.
// backupStoreAt is the backup whose copies live at p on driveID (its
// .backups/<name> folder or anything inside), for admins. That folder only
// holds coded object names, so the app opens Browse snapshots there instead.
func (s *Server) backupStoreAt(ctx context.Context, u *store.User, driveID, p string) map[string]string {
	if !u.IsAdmin() {
		return nil
	}
	list, err := s.store.Backups(ctx)
	if err != nil {
		return nil
	}
	p = path.Clean(p)
	for _, b := range list {
		t := path.Clean(b.TargetPath)
		if b.TargetDrive == driveID && (p == t || strings.HasPrefix(p, t+"/")) {
			return map[string]string{"id": b.ID, "name": b.Name}
		}
	}
	return nil
}

func (s *Server) backupCovers(ctx context.Context, driveID string) []backupCover {
	list, err := s.store.Backups(ctx)
	if err != nil {
		return nil
	}
	var out []backupCover
	for _, b := range list {
		if !b.Enabled || b.SourceDrive != driveID {
			continue
		}
		target, _ := s.drive(b.TargetDrive)
		if target == nil {
			continue
		}
		c := backupCover{b: b, target: target}
		name, at, err := s.store.LastGoodSnapshot(ctx, b.ID)
		if err == nil && name != "" {
			c.at = at
			c.snap, err = s.snapshotIndex(ctx, b, target, name)
			c.failed = err != nil
		}
		out = append(out, c)
	}
	return out
}

// snapshotIndex loads a snapshot's file list once and keeps it until the
// backup has a newer one.
func (s *Server) snapshotIndex(ctx context.Context, b *store.Backup, target *mounted, name string) (*snapIndex, error) {
	snapCache.Lock()
	ix := snapCache.m[b.ID]
	snapCache.Unlock()
	if ix != nil && ix.name == name {
		return ix, nil
	}
	if target.d == nil {
		return nil, errDriveOffline
	}
	// A bucket may be slow; a listing shouldn't wait long for it.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	m, err := backup.Load(ctx, target.d, b.TargetPath, name)
	if err != nil {
		return nil, err
	}
	ix = &snapIndex{name: name, files: make(map[string][2]int64, len(m.Files)), dirs: map[string]bool{".": true}}
	addDirs := func(p string) {
		for d := files.Parent(p); d != "." && !ix.dirs[d]; d = files.Parent(d) {
			ix.dirs[d] = true
		}
	}
	for _, f := range m.Files {
		ix.files[f.P] = [2]int64{f.S, f.M}
		addDirs(f.P)
	}
	for _, d := range m.Dirs {
		ix.dirs[d] = true
		addDirs(d)
	}
	snapCache.Lock()
	snapCache.m[b.ID] = ix
	snapCache.Unlock()
	return ix, nil
}

// markFor is the most reassuring true mark for drive path p: a finished
// backup over a pending one, and the cloud over a local copy.
func markFor(covers []backupCover, p string, isDir bool, size, mod int64) *backupMark {
	var best *backupMark
	rank := map[string]int{"partial": 0, "unknown": 1, "pending": 2, "done": 3}
	score := func(m *backupMark) int {
		n := rank[m.State] * 2
		if m.Cloud {
			n++
		}
		return n
	}
	for _, c := range covers {
		src := c.b.SourcePath
		roots := backupRoots(c.b)
		var state string
		switch {
		case slices.ContainsFunc(roots, func(root string) bool { return files.Within(root, p) }):
			rel := "."
			if p != src {
				rel = strings.TrimPrefix(p, src+"/")
				if src == "." {
					rel = p
				}
			}
			switch {
			case c.failed:
				state = "unknown"
			case c.snap == nil:
				state = "pending"
			case isDir && c.snap.dirs[rel]:
				state = "done"
			case !isDir && c.snap.files[rel] == [2]int64{size, mod}:
				state = "done"
			default:
				state = "pending"
			}
		case isDir && slices.ContainsFunc(roots, func(root string) bool { return files.Within(p, root) }):
			state = "partial"
		default:
			continue
		}
		m := &backupMark{State: state, Backup: c.b.Name, Drive: c.target.info.Name, Cloud: c.target.info.Kind == "s3",
			Encrypted: encrypted(c.target), Last: c.at}
		if best == nil || score(m) > score(best) {
			best = m
		}
	}
	return best
}
