package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// Daily pruning of things that would otherwise grow for as long as the box
// runs.

const (
	mediaCacheIdle = 60 * 24 * time.Hour  // previews nobody opened
	mediaJobsKeep  = 30 * 24 * time.Hour  // finished and failed jobs
	backupRunsKeep = 365 * 24 * time.Hour // run history...
	backupRunsMin  = 50                   // ...but never fewer than this per backup
	sharesKeep     = 90 * 24 * time.Hour  // links after they expire or are turned off
	sentKeep       = 30 * 24 * time.Hour  // Send files folders after their link expires
	sentSlack      = 10 * 60              // seconds after the link was made that still count as the send
)

func (s *Server) prune(ctx context.Context, now time.Time) {
	capBytes := int64(s.cur().MediaCacheGB) << 30
	if n, b := pruneCache(s.mediaDir(), mediaCacheIdle, capBytes, now); n > 0 {
		s.log.Info("pruned preview cache", "files", n, "bytes", b)
	}
	if n, err := s.store.PruneMediaJobs(ctx, now.Add(-mediaJobsKeep).Unix()); err != nil {
		s.log.Warn("prune media jobs", "err", err)
	} else if n > 0 {
		s.log.Info("pruned media jobs", "rows", n)
	}
	if n, err := s.store.PruneBackupRuns(ctx, now.Add(-backupRunsKeep).Unix(), backupRunsMin); err != nil {
		s.log.Warn("prune backup runs", "err", err)
	} else if n > 0 {
		s.log.Info("pruned backup runs", "rows", n)
	}
	// Before the links go: the sent folders are found through them.
	s.pruneSent(ctx, now)
	if n, err := s.store.PruneExpiredShares(ctx, now.Add(-sharesKeep).Unix()); err != nil {
		s.log.Warn("prune expired links", "err", err)
	} else if n > 0 {
		s.log.Info("pruned expired links", "links", n)
	}
	s.pruneDisabledShares(ctx, now)
}

// pruneCache deletes files under dir (but not its tmp folder) unused for
// idle, then the least recently used until the rest fit in capBytes (0 for
// no cap). Use is the later of the access and modification times; serving
// a file bumps its modification time once a day (touchMedia), so this works
// on noatime mounts too.
func pruneCache(dir string, idle time.Duration, capBytes int64, now time.Time) (removed int, bytes int64) {
	type entry struct {
		p    string
		size int64
		used time.Time
	}
	var all []entry
	var total int64
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && d.Name() == "tmp" && filepath.Dir(p) == dir {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		used := info.ModTime()
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			if at := time.Unix(st.Atim.Sec, st.Atim.Nsec); at.After(used) {
				used = at
			}
		}
		all = append(all, entry{p, info.Size(), used})
		total += info.Size()
		return nil
	})
	sort.Slice(all, func(i, j int) bool { return all[i].used.Before(all[j].used) })
	for _, e := range all {
		if now.Sub(e.used) < idle && (capBytes <= 0 || total <= capBytes) {
			break
		}
		if os.Remove(e.p) == nil {
			removed++
			bytes += e.size
			total -= e.size
		}
	}
	return removed, bytes
}

// touchMedia marks a cached preview as used, at most once a day.
func touchMedia(p string, info os.FileInfo) {
	if time.Since(info.ModTime()) > 24*time.Hour {
		now := time.Now()
		_ = os.Chtimes(p, now, now)
	}
}

// pruneDisabledShares deletes links that have been turned off for
// sharesKeep. Links don't record when they were turned off, so the first
// day each is seen off is kept here.
func (s *Server) pruneDisabledShares(ctx context.Context, now time.Time) {
	ids, err := s.store.DisabledShares(ctx)
	if err != nil {
		return
	}
	seen := map[string]int64{}
	if err := s.store.GetJSON(ctx, "shares_off", &seen); err != nil && !errors.Is(err, store.ErrNotFound) {
		seen = map[string]int64{}
	}
	next := map[string]int64{}
	for _, id := range ids {
		since, ok := seen[id]
		if !ok {
			since = now.Unix()
		}
		if now.Sub(time.Unix(since, 0)) >= sharesKeep {
			if err := s.store.DeleteShare(ctx, id); err == nil {
				s.log.Info("pruned a link turned off long ago", "share", id)
				continue
			}
		}
		next[id] = since
	}
	if len(next) == 0 && len(seen) == 0 {
		return
	}
	_ = s.store.PutJSON(ctx, "shares_off", next)
}

// Send files puts what it sends in a new folder under the sender's
// Sent folder and links to it. The server marks such links when they are
// made, so pruning only ever touches folders Send files itself made.

type sentMark struct {
	Drive   string `json:"drive"`
	Path    string `json:"path"`
	Owner   int64  `json:"owner"`
	Expires int64  `json:"expires"`
	Created int64  `json:"created"`
}

// markSent remembers that sh is a Send files link to a folder under Sent.
func (s *Server) markSent(ctx context.Context, sh *store.Share) {
	if sh.CollectionID != "" || !sh.IsDir || path.Base(path.Dir(sh.Path)) != "Sent" {
		return
	}
	mk := sentMark{Drive: sh.DriveID, Path: sh.Path, Owner: sh.OwnerID, Expires: sh.ExpiresAt, Created: time.Now().Unix()}
	if err := s.store.PutJSON(ctx, "sent:"+sh.ID, mk); err != nil {
		s.log.Warn("marking a sent folder", "err", err)
	}
}

// pruneSent moves a sent folder to the trash sentKeep after its link
// expired, unless anything in it changed after it was sent.
func (s *Server) pruneSent(ctx context.Context, now time.Time) {
	marks, err := s.store.KVPrefix(ctx, "sent:")
	if err != nil {
		return
	}
	for id, raw := range marks {
		forget := func() { _ = s.store.DeleteKV(ctx, "sent:"+id) }
		var mk sentMark
		if json.Unmarshal([]byte(raw), &mk) != nil {
			forget()
			continue
		}
		exp := mk.Expires
		sh, err := s.store.ShareByID(ctx, id)
		switch {
		case err == nil && sh.Path != mk.Path:
			forget() // moved since: it's someone's folder now
			continue
		case err == nil:
			exp = sh.ExpiresAt
		case !errors.Is(err, store.ErrNotFound):
			continue
		}
		if exp == 0 {
			if err != nil {
				forget() // the link is gone without ever expiring
			}
			continue
		}
		if now.Sub(time.Unix(exp, 0)) < sentKeep {
			continue
		}
		m, err := s.drive(mk.Drive)
		if errors.Is(err, store.ErrNotFound) {
			forget()
			continue
		} else if err != nil {
			continue // offline: try again tomorrow
		}
		if s.sentUntouched(ctx, m, mk) {
			if err := s.trashSent(ctx, m, mk); err != nil {
				s.log.Warn("pruning a sent folder", "path", mk.Path, "err", err)
				continue
			}
			s.log.Info("moved an expired sent folder to the trash", "drive", mk.Drive, "path", mk.Path)
		}
		forget()
	}
}

var errTouched = errors.New("changed after it was sent")

// sentUntouched reports whether mk's folder is still as Send files left it:
// there, under Sent, and nothing in it newer than the link.
func (s *Server) sentUntouched(ctx context.Context, m *mounted, mk sentMark) bool {
	if path.Base(path.Dir(mk.Path)) != "Sent" {
		return false
	}
	e, err := m.d.Stat(ctx, mk.Path)
	if err != nil || !e.IsDir {
		return false
	}
	limit := mk.Created + sentSlack
	err = m.d.Walk(ctx, mk.Path, func(e files.Entry) error {
		if e.ModTime > limit {
			return errTouched
		}
		return nil
	})
	return err == nil
}

// trashSent moves a sent folder to the trash, where its owner can still
// get it back for the trash period.
func (s *Server) trashSent(ctx context.Context, m *mounted, mk sentMark) error {
	id := security.RandomToken(12)
	isDir, size, err := m.d.Trash(ctx, mk.Path, id)
	if err != nil {
		return err
	}
	if fsz, ok := s.index.Size(ctx, m.info.ID, mk.Path); ok && isDir {
		size = fsz.Size
	}
	if err := s.store.AddTrash(ctx, &store.TrashItem{ID: id, DriveID: m.info.ID, UserID: mk.Owner, OrigPath: mk.Path, IsDir: isDir, Size: size}); err != nil {
		_, _ = m.d.Restore(ctx, id, mk.Path)
		return err
	}
	_ = s.index.Remove(ctx, m.info.ID, mk.Path)
	_ = s.store.Audit(ctx, store.AuditEntry{Action: "delete", Path: m.info.ID + ":" + mk.Path, Detail: "sent folder, 30 days after its link expired"})
	return nil
}
