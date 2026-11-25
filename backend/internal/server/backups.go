package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oddomens/coffer/internal/backup"
	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// Scheduled backups of one drive folder into another drive (typically an
// R2 bucket), with point-in-time restore.

var (
	backupMu      sync.Mutex
	backupRunning = map[string]*store.BackupRun{}
)

type backupJSON struct {
	*store.Backup
	SourceName string           `json:"sourceName"`
	TargetName string           `json:"targetName"`
	TargetKind string           `json:"targetKind"`
	Running    *store.BackupRun `json:"running,omitempty"`
	NextRun    int64            `json:"nextRun"`
	// The last run was held back (the source shrank a lot): an admin may
	// run it anyway.
	CanForce bool `json:"canForce"`
}

func (s *Server) backupOut(b *store.Backup) backupJSON {
	if b.Items == nil {
		b.Items = []string{}
	}
	out := backupJSON{Backup: b}
	if m, err := s.drive(b.SourceDrive); err == nil || m != nil {
		out.SourceName = m.info.Name
	}
	if m, err := s.drive(b.TargetDrive); err == nil || m != nil {
		out.TargetName, out.TargetKind = m.info.Name, m.info.Kind
	}
	backupMu.Lock()
	if r := backupRunning[b.ID]; r != nil {
		cp := *r
		out.Running = &cp
	}
	backupMu.Unlock()
	if b.Enabled {
		out.NextRun = nextRun(b, time.Now()).Unix()
	}
	out.CanForce = b.LastRun != nil && b.LastRun.State == "failed" && strings.HasPrefix(b.LastRun.Error, heldBackPrefix)
	return out
}

func nextRun(b *store.Backup, now time.Time) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), b.Hour, 0, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func (s *Server) handleBackupsList(w http.ResponseWriter, r *http.Request, _ *store.User) {
	list, err := s.store.Backups(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]backupJSON, 0, len(list))
	for _, b := range list {
		out = append(out, s.backupOut(b))
	}
	writeJSON(w, http.StatusOK, out)
}

type backupReq struct {
	Name        *string   `json:"name"`
	SourceDrive string    `json:"sourceDrive"`
	SourcePath  string    `json:"sourcePath"`
	TargetDrive string    `json:"targetDrive"`
	TargetPath  string    `json:"targetPath"`
	Items       *[]string `json:"items"` // drive paths; empty for the whole folder
	Hour        *int      `json:"hour"`
	Keep        *int      `json:"keep"`
	Enabled     *bool     `json:"enabled"`
}

func (s *Server) applyBackup(w http.ResponseWriter, b *store.Backup, req backupReq) bool {
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" || len(n) > 80 {
			badRequest(w, "give the backup a name (up to 80 characters)")
			return false
		}
		b.Name = n
	}
	if req.Hour != nil {
		if *req.Hour < 0 || *req.Hour > 23 {
			badRequest(w, "the hour must be between 0 and 23")
			return false
		}
		b.Hour = *req.Hour
	}
	if req.Keep != nil {
		if *req.Keep < 1 || *req.Keep > 3650 {
			badRequest(w, "keep between 1 and 3650 snapshots")
			return false
		}
		b.Keep = *req.Keep
	}
	if req.Enabled != nil {
		b.Enabled = *req.Enabled
	}
	return true
}

func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req backupReq
	if !decodeJSON(w, r, &req) {
		return
	}
	b := &store.Backup{ID: security.RandomToken(9), Hour: 2, Keep: 30, Enabled: true}
	if req.Name == nil {
		badRequest(w, "give the backup a name")
		return
	}
	if !s.applyBackup(w, b, req) {
		return
	}
	src, err1 := s.drive(req.SourceDrive)
	dst, err2 := s.drive(req.TargetDrive)
	if err1 != nil || err2 != nil {
		badRequest(w, "pick a source and a target drive that are online")
		return
	}
	sp, err := files.Clean(req.SourcePath)
	if err != nil {
		badRequest(w, "invalid source folder")
		return
	}
	if req.TargetPath == "" {
		req.TargetPath = ".backups/" + strings.ReplaceAll(b.Name, "/", "-")
	}
	tp, err := files.Clean(req.TargetPath)
	if err != nil || tp == "." {
		badRequest(w, "pick a folder on the target drive, such as .backups/studio")
		return
	}
	if e, err := src.d.Stat(r.Context(), sp); err != nil || !e.IsDir {
		badRequest(w, "the source folder doesn't exist")
		return
	}
	b.SourceDrive, b.SourcePath, b.TargetDrive, b.TargetPath = src.info.ID, sp, dst.info.ID, tp
	if req.Items != nil {
		var paths []string
		for _, p := range *req.Items {
			c, err := files.Clean(p)
			if err != nil {
				badRequest(w, "invalid path: "+p)
				return
			}
			paths = append(paths, c)
		}
		if b.Items, err = checkItems(r.Context(), src, sp, paths, nil); err != nil {
			badRequest(w, err.Error())
			return
		}
	}
	if !s.backupPlaceOK(w, r, b) {
		return
	}
	if err := s.store.CreateBackup(r.Context(), b); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "backup.create", src.info.ID+":"+sp, "to "+dst.info.ID+":"+tp)
	writeJSON(w, http.StatusCreated, s.backupOut(b))
}

func (s *Server) backupByID(w http.ResponseWriter, r *http.Request) (*store.Backup, bool) {
	b, err := s.store.BackupByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "backup not found")
		return nil, false
	}
	return b, true
}

func (s *Server) handleBackupUpdate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req backupReq
	if !decodeJSON(w, r, &req) {
		return
	}
	b, ok := s.backupByID(w, r)
	if !ok || !s.applyBackup(w, b, req) {
		return
	}
	if req.Items != nil {
		src, err := s.drive(b.SourceDrive)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "drive_offline", "the source drive is offline")
			return
		}
		var paths []string
		for _, p := range *req.Items {
			c, err := files.Clean(p)
			if err != nil {
				badRequest(w, "invalid path: "+p)
				return
			}
			paths = append(paths, c)
		}
		if len(paths) == 0 && len(b.Items) > 0 {
			badRequest(w, "a backup needs at least one file or folder; pause or delete it instead")
			return
		}
		// Only new picks need to exist: a picked folder that's gone can
		// still be taken out.
		items, err := checkItems(r.Context(), src, b.SourcePath, paths, b.Items)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		b.Items = items
		if !s.backupPlaceOK(w, r, b) {
			return
		}
		if err := s.store.SetBackupItems(r.Context(), b); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.store.UpdateBackup(r.Context(), b); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "backup.update", b.SourceDrive+":"+b.SourcePath, b.Name)
	writeJSON(w, http.StatusOK, s.backupOut(b))
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	b, ok := s.backupByID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteBackup(r.Context(), b.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "backup.delete", b.SourceDrive+":"+b.SourcePath, b.Name+" (data left in place)")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBackupRun(w http.ResponseWriter, r *http.Request, u *store.User) {
	b, ok := s.backupByID(w, r)
	if !ok {
		return
	}
	// "Run anyway": make a snapshot even though the source shrank a lot.
	force := r.URL.Query().Get("force") == "1"
	if !s.startBackup(b, force) {
		writeErr(w, http.StatusConflict, "running", "this backup is already running")
		return
	}
	detail := b.Name
	if force {
		detail += " (run anyway)"
	}
	s.audit(r, u, "", "backup.run", b.SourceDrive+":"+b.SourcePath, detail)
	writeJSON(w, http.StatusAccepted, s.backupOut(b))
}

func (s *Server) handleBackupRuns(w http.ResponseWriter, r *http.Request, _ *store.User) {
	b, ok := s.backupByID(w, r)
	if !ok {
		return
	}
	runs, err := s.store.BackupRuns(r.Context(), b.ID, 30)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleBackupSnapshots(w http.ResponseWriter, r *http.Request, _ *store.User) {
	b, ok := s.backupByID(w, r)
	if !ok {
		return
	}
	dst, err := s.drive(b.TargetDrive)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "drive_offline", "the backup drive is offline")
		return
	}
	snaps, err := backup.Snapshots(r.Context(), dst.d, b.TargetPath)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Newest first for the UI.
	for i, j := 0, len(snaps)-1; i < j; i, j = i+1, j-1 {
		snaps[i], snaps[j] = snaps[j], snaps[i]
	}
	writeJSON(w, http.StatusOK, snaps)
}

// snapshotCache keeps the last few snapshots opened for browsing. A
// snapshot never changes once written, and a video being scrubbed asks for
// a new range every few seconds, each needing the whole file list.
type snapshotCache struct {
	mu      sync.Mutex
	entries []cachedSnapshot // newest last
}

type cachedSnapshot struct {
	key string
	m   *backup.Manifest
}

const snapshotCacheSize = 2

func (c *snapshotCache) load(ctx context.Context, d files.Drive, driveID, root, name string) (*backup.Manifest, error) {
	key := driveID + "\x00" + root + "\x00" + name
	c.mu.Lock()
	for i, e := range c.entries {
		if e.key == key {
			c.entries = append(append(c.entries[:i:i], c.entries[i+1:]...), e)
			c.mu.Unlock()
			return e.m, nil
		}
	}
	c.mu.Unlock()
	m, err := backup.Load(ctx, d, root, name)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries = append(c.entries, cachedSnapshot{key, m})
	if len(c.entries) > snapshotCacheSize {
		c.entries = c.entries[len(c.entries)-snapshotCacheSize:]
	}
	c.mu.Unlock()
	return m, nil
}

// snapshot loads the snapshot named in the URL, and the folder or file in
// its "path" query (relative to the backed-up folder).
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) (*store.Backup, *mounted, *backup.Manifest, string, bool) {
	b, ok := s.backupByID(w, r)
	if !ok {
		return nil, nil, nil, "", false
	}
	dst, err := s.drive(b.TargetDrive)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "drive_offline", "the backup drive is offline")
		return nil, nil, nil, "", false
	}
	p, err := files.Clean(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, nil, "", false
	}
	m, err := s.snaps.load(r.Context(), dst.d, b.TargetDrive, b.TargetPath, r.PathValue("snap"))
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, nil, "", false
	}
	return b, dst, m, p, true
}

func (s *Server) handleBackupBrowse(w http.ResponseWriter, r *http.Request, _ *store.User) {
	_, _, m, dir, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": files.Rel(".", dir), "entries": backup.Browse(m, dir), "files": len(m.Files)})
}

// handleBackupFile serves one file of a snapshot, for previews and
// downloads, without restoring it.
func (s *Server) handleBackupFile(w http.ResponseWriter, r *http.Request, u *store.User) {
	b, dst, m, p, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	f := m.Find(p)
	if f == nil {
		s.fail(w, r, fs.ErrNotExist)
		return
	}
	rc, err := backup.Open(r.Context(), dst.d, b.TargetPath, f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rc.Close()
	inline := r.URL.Query().Get("inline") == "1"
	if !inline && r.Header.Get("Range") == "" {
		s.audit(r, u, "", "backup.download", b.SourceDrive+":"+joinRel(b.SourcePath, p), r.PathValue("snap"))
	}
	name := path.Base(p)
	serveFile(w, r, rc, files.Entry{Name: name, Size: f.S, ModTime: f.M}, name, inline)
}

// handleBackupZip downloads folders and files of a snapshot as one zip:
// the "name" entries of the folder in "path", or all of it.
func (s *Server) handleBackupZip(w http.ResponseWriter, r *http.Request, u *store.User) {
	b, dst, m, dir, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	names := r.URL.Query()["name"]
	if len(names) > 5000 {
		badRequest(w, "too many items")
		return
	}
	for _, n := range names {
		if !files.ValidEntryName(n) {
			s.fail(w, r, files.ErrInvalidName)
			return
		}
	}
	zipName := path.Base(dir)
	switch {
	case len(names) == 1:
		zipName = names[0]
	case dir == ".":
		zipName = b.Name
	}
	t, _ := time.Parse("20060102T150405Z", r.PathValue("snap"))
	zipName += " " + t.Local().Format("2006-01-02 1504")
	s.audit(r, u, "", "backup.download", b.SourceDrive+":"+joinRel(b.SourcePath, dir), r.PathValue("snap")+" "+strings.Join(names, ", "))
	zipHeaders(w, zipName)
	if err := backup.WriteZip(r.Context(), dst.d, b.TargetPath, m, dir, names, w); err != nil && r.Context().Err() == nil {
		s.log.Warn("snapshot zip failed", "backup", b.Name, "snapshot", r.PathValue("snap"), "err", err)
	}
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Snapshot string   `json:"snapshot"`
		Paths    []string `json:"paths"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	b, ok := s.backupByID(w, r)
	if !ok {
		return
	}
	dst, err1 := s.drive(b.TargetDrive)
	src, err2 := s.drive(b.SourceDrive)
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusServiceUnavailable, "drive_offline", "a drive is offline")
		return
	}
	m, err := backup.Load(r.Context(), dst.d, b.TargetPath, req.Snapshot)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var sel []string
	for _, p := range req.Paths {
		c, err := files.Clean(p)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		sel = append(sel, c)
	}
	if len(sel) == 0 {
		sel = []string{"."}
	}
	t, _ := time.Parse("20060102T150405Z", req.Snapshot)
	folder := "Restored " + t.Local().Format("2006-01-02 1504")
	into := joinRel(b.SourcePath, files.UniqueName(r.Context(), src.d, b.SourcePath, folder))
	// Restores can take a while: run in the background and notify.
	go func() {
		ctx := context.Background()
		n, err := backup.Restore(ctx, dst.d, b.TargetPath, m, sel, src.d, into)
		s.reindex(src, files.Parent(into))
		if err != nil {
			s.notify(u.ID, "backup", "Restore from “"+b.Name+"” failed", fmt.Sprintf("%d files restored before: %v", n, err), "/admin/backups")
			return
		}
		s.notify(u.ID, "backup", "Restored "+fmt.Sprint(n)+" files from “"+b.Name+"”", "Into "+files.Rel(".", into), "")
	}()
	s.audit(r, u, "", "backup.restore", b.SourceDrive+":"+into, req.Snapshot+" "+strings.Join(sel, ", "))
	writeJSON(w, http.StatusAccepted, map[string]string{"into": files.Rel(".", into), "drive": src.info.ID})
}

func joinRel(dir, name string) string {
	if dir == "." {
		return name
	}
	return dir + "/" + name
}

// startBackup runs b in the background unless it is already running.
// force makes a snapshot even if the source shrank a lot.
func (s *Server) startBackup(b *store.Backup, force bool) bool {
	backupMu.Lock()
	if backupRunning[b.ID] != nil {
		backupMu.Unlock()
		return false
	}
	run, err := s.store.StartBackupRun(context.Background(), b.ID)
	if err != nil {
		backupMu.Unlock()
		return false
	}
	backupRunning[b.ID] = run
	backupMu.Unlock()
	go s.runBackup(b, run, force)
	return true
}

// heldBackPrefix starts the error of a run held back because the source
// shrank; the admin page offers "Run anyway" for it.
const heldBackPrefix = "Held back: "

func (s *Server) runBackup(b *store.Backup, run *store.BackupRun, force bool) {
	ctx := context.Background()
	defer func() {
		backupMu.Lock()
		delete(backupRunning, b.ID)
		backupMu.Unlock()
	}()
	// The backups list reads run under backupMu while this goes on, so every
	// change to it here holds the lock too.
	finish := func(err error) {
		backupMu.Lock()
		run.FinishedAt = time.Now().Unix()
		run.State = "done"
		if err != nil {
			run.State, run.Error = "failed", truncate(err.Error(), 500)
		}
		backupMu.Unlock()
		if err != nil {
			s.log.Warn("backup failed", "backup", b.Name, "err", err)
			s.notifyAdmins("backup", "Backup “"+b.Name+"” failed", run.Error, "/admin/backups")
		}
		_ = s.store.SaveBackupRun(ctx, run)
	}
	// Look at the disks now: an unplugged one isn't backed up as empty.
	src, err := s.checkDrive(ctx, b.SourceDrive)
	if err != nil {
		finish(fmt.Errorf("source drive: %w", err))
		return
	}
	dst, err := s.checkDrive(ctx, b.TargetDrive)
	if err != nil {
		finish(fmt.Errorf("target drive: %w", err))
		return
	}
	var only []string
	if len(b.Items) > 0 {
		only = b.Items
	}
	snap, pr, err := backup.RunWith(ctx, src.d, b.SourcePath, only, dst.d, b.TargetPath, func(p backup.Progress) {
		backupMu.Lock()
		run.Files, run.Bytes, run.Uploaded, run.UploadedBytes = p.Files, p.Bytes, p.Uploaded, p.UploadedBytes
		backupMu.Unlock()
	}, backup.Options{Force: force})
	if shrunk := new(backup.ShrunkError); errors.As(err, &shrunk) {
		err = errors.New(heldBackPrefix + shrunk.Error() + ", so no snapshot was made. If that's expected, use Run anyway.")
	}
	backupMu.Lock()
	run.Files, run.Bytes, run.Uploaded, run.UploadedBytes, run.Snapshot = pr.Files, pr.Bytes, pr.Uploaded, pr.UploadedBytes, snap
	backupMu.Unlock()
	if err == nil {
		s.copySelf(ctx, b, dst, snap)
	}
	if err == nil {
		if _, _, perr := backup.Prune(ctx, dst.d, b.TargetPath, b.Keep); perr != nil {
			s.log.Warn("backup prune failed", "backup", b.Name, "err", perr)
		}
		s.log.Info("backup done", "backup", b.Name, "files", pr.Files, "uploaded", pr.Uploaded, "failed", pr.Failed)
		// Done, but say what was left out.
		var notes []string
		if len(pr.Missing) > 0 {
			// A picked folder was deleted, or moved somewhere Coffer didn't see.
			shown := pr.Missing[:min(len(pr.Missing), 5)]
			for i, p := range shown {
				shown[i] = files.Rel(".", p)
			}
			notes = append(notes, "Not found, so not backed up: "+strings.Join(shown, ", "))
		}
		if pr.Unreadable > 0 {
			what := fmt.Sprintf("%d folders", pr.Unreadable)
			if pr.Unreadable == 1 {
				what = "1 folder"
			}
			shown := pr.UnreadablePaths
			for i, p := range shown {
				shown[i] = files.Rel(".", p)
			}
			notes = append(notes, what+" couldn't be read, so nothing in them was backed up: "+strings.Join(shown, ", "))
		}
		if pr.Failed > 0 {
			what := fmt.Sprintf("%d files", pr.Failed)
			if pr.Failed == 1 {
				what = "1 file"
			}
			notes = append(notes, what+" couldn't be copied and will be tried again next run: "+strings.Join(pr.Errors[:min(len(pr.Errors), 3)], "; "))
		}
		if len(notes) > 0 {
			backupMu.Lock()
			run.Error = truncate(strings.Join(notes, ". "), 500)
			backupMu.Unlock()
			s.notifyAdmins("backup", "Backup “"+b.Name+"” missed something", run.Error, "/admin/backups")
		}
	}
	for _, e := range pr.Errors {
		s.log.Warn("backup skipped a file", "backup", b.Name, "err", e)
	}
	finish(err)
}

// backupLoop starts nightly runs.
func (s *Server) backupLoop(ctx context.Context) {
	_ = s.store.AbandonBackupRuns(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		list, err := s.store.Backups(ctx)
		if err != nil {
			continue
		}
		now := time.Now()
		for _, b := range list {
			if !b.Enabled || now.Hour() != b.Hour {
				continue
			}
			if b.LastRun != nil && now.Sub(time.Unix(b.LastRun.StartedAt, 0)) < 20*time.Hour {
				continue
			}
			s.startBackup(b, false)
		}
	}
}

// checkItems checks picked drive paths for a backup of root on m: each must
// be inside root, and exist unless it's in known (already picked). It
// returns them without any inside another, or nil when root itself is
// picked.
func checkItems(ctx context.Context, m *mounted, root string, paths, known []string) ([]string, error) {
	for _, p := range paths {
		if !files.Within(root, p) {
			return nil, fmt.Errorf("%s isn't in the backed-up folder %s", files.Rel(".", p), files.Rel(".", root))
		}
		if p == files.InternalDir || strings.HasPrefix(p, files.InternalDir+"/") || strings.Contains(p, "/"+files.InternalDir+"/") || strings.HasSuffix(p, "/"+files.InternalDir) {
			return nil, fmt.Errorf("Coffer's own %s folder isn't backed up", files.InternalDir)
		}
		if slices.Contains(known, p) {
			continue
		}
		if m.d == nil {
			return nil, errDriveOffline
		}
		if _, err := m.d.Stat(ctx, p); err != nil {
			return nil, fmt.Errorf("%s wasn't found", files.Rel(".", p))
		}
	}
	items := backup.Items(root, paths)
	if len(items) == 1 && items[0] == root {
		return nil, nil
	}
	return items, nil
}

// backupRoots are the drive paths a backup takes.
func backupRoots(b *store.Backup) []string {
	if len(b.Items) > 0 {
		return b.Items
	}
	return []string{b.SourcePath}
}

// backupPlaceOK refuses a backup that would take in its own copies, or share
// its target folder with another backup (each prunes its own snapshots).
func (s *Server) backupPlaceOK(w http.ResponseWriter, r *http.Request, b *store.Backup) bool {
	if b.SourceDrive == b.TargetDrive {
		for _, p := range backupRoots(b) {
			if files.Within(p, b.TargetPath) || files.Within(b.TargetPath, p) {
				badRequest(w, "the backup can't be inside what it backs up, or the other way round")
				return false
			}
		}
	}
	list, err := s.store.Backups(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	for _, o := range list {
		if o.ID != b.ID && o.TargetDrive == b.TargetDrive && (files.Within(o.TargetPath, b.TargetPath) || files.Within(b.TargetPath, o.TargetPath)) {
			badRequest(w, "the backup “"+o.Name+"” already keeps its copies in "+files.Rel(".", o.TargetPath)+"; pick another folder")
			return false
		}
	}
	return true
}

// handleBackupAdd adds files and folders picked in the file browser to a
// backup (a new one when no ID is given), or takes them out again.
func (s *Server) handleBackupAdd(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place       string   `json:"place"`
		Paths       []string `json:"paths"`
		Backup      string   `json:"backup"` // add to this one, or make one:
		Name        string   `json:"name"`
		TargetDrive string   `json:"targetDrive"`
		Remove      bool     `json:"remove"`
		Run         bool     `json:"run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok {
		return
	}
	if len(req.Paths) == 0 || len(req.Paths) > 1000 {
		badRequest(w, "pick between 1 and 1000 files or folders")
		return
	}
	var picked []string
	for _, p := range req.Paths {
		dp, err := a.resolve(p)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		picked = append(picked, dp)
	}
	ctx := r.Context()
	var b *store.Backup
	added, already, removed := 0, 0, 0
	if req.Backup == "" {
		if req.Remove {
			badRequest(w, "pick the backup to take them out of")
			return
		}
		name := strings.TrimSpace(req.Name)
		dst, err := s.drive(req.TargetDrive)
		if err != nil {
			badRequest(w, "pick a drive to back up to that is online")
			return
		}
		b = &store.Backup{ID: security.RandomToken(9), Hour: 2, Keep: 30, Enabled: true,
			SourceDrive: a.DriveID, SourcePath: ".", TargetDrive: dst.info.ID}
		if !s.applyBackup(w, b, backupReq{Name: &name}) {
			return
		}
		if b.Items, err = checkItems(ctx, a.m, ".", picked, nil); err != nil {
			badRequest(w, err.Error())
			return
		}
		// A free folder for the copies: .backups/<name>, or <name> 2, ...
		list, err := s.store.Backups(ctx)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		base := ".backups/" + strings.ReplaceAll(b.Name, "/", "-")
		for i := 1; ; i++ {
			b.TargetPath = base
			if i > 1 {
				b.TargetPath = fmt.Sprintf("%s %d", base, i)
			}
			if !slices.ContainsFunc(list, func(o *store.Backup) bool {
				return o.TargetDrive == b.TargetDrive && (files.Within(o.TargetPath, b.TargetPath) || files.Within(b.TargetPath, o.TargetPath))
			}) {
				break
			}
		}
		if !s.backupPlaceOK(w, r, b) {
			return
		}
		if err := s.store.CreateBackup(ctx, b); err != nil {
			s.fail(w, r, err)
			return
		}
		added = len(picked)
		s.audit(r, u, "", "backup.create", b.SourceDrive+":"+strings.Join(backupRoots(b), ", "), "to "+b.TargetDrive+":"+b.TargetPath)
	} else {
		var err error
		if b, err = s.store.BackupByID(ctx, req.Backup); err != nil {
			writeErr(w, http.StatusNotFound, "not_found", "backup not found")
			return
		}
		if b.SourceDrive != a.DriveID {
			badRequest(w, "“"+b.Name+"” backs up another drive")
			return
		}
		for _, p := range picked {
			if !files.Within(b.SourcePath, p) {
				badRequest(w, "“"+b.Name+"” only backs up "+files.Rel(".", b.SourcePath))
				return
			}
		}
		if req.Remove {
			if len(b.Items) == 0 {
				badRequest(w, "“"+b.Name+"” backs up all of "+files.Rel(".", b.SourcePath)+", so nothing in it can be left out")
				return
			}
			keep := slices.DeleteFunc(slices.Clone(b.Items), func(p string) bool { return slices.Contains(picked, p) })
			switch {
			case len(keep) == len(b.Items):
				badRequest(w, "these were not added to “"+b.Name+"” on their own; take out the folder they're in instead")
				return
			case len(keep) == 0:
				badRequest(w, "that's everything “"+b.Name+"” backs up; pause or delete the backup instead")
				return
			}
			removed = len(b.Items) - len(keep)
			b.Items = keep
			req.Run = false
		} else {
			for _, p := range picked {
				if slices.ContainsFunc(backupRoots(b), func(q string) bool { return files.Within(q, p) }) {
					already++
				}
			}
			if already < len(picked) {
				items, err := checkItems(ctx, a.m, b.SourcePath, append(slices.Clone(b.Items), picked...), b.Items)
				if err != nil {
					badRequest(w, err.Error())
					return
				}
				b.Items = items
				added = len(picked) - already
			}
		}
		if added+removed > 0 {
			if !s.backupPlaceOK(w, r, b) {
				return
			}
			if err := s.store.SetBackupItems(ctx, b); err != nil {
				s.fail(w, r, err)
				return
			}
			action := "backup.add"
			if removed > 0 {
				action = "backup.remove"
			}
			s.audit(r, u, "", action, b.SourceDrive+":"+strings.Join(picked, ", "), b.Name)
		}
	}
	started := req.Run && s.startBackup(b, false)
	writeJSON(w, http.StatusOK, map[string]any{"backup": s.backupOut(b), "added": added, "already": already, "removed": removed, "started": started})
}
