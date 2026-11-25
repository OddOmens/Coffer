package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/store"
)

func TestPruneCache(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	put := func(rel string, size int, age time.Duration) string {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, make([]byte, size), 0o600)
		at := now.Add(-age)
		os.Chtimes(p, at, at)
		return p
	}
	stale := put("aa/stale.mp4", 10, 61*24*time.Hour)
	oldest := put("bb/old.jpg", 100, 5*24*time.Hour)
	newer := put("cc/new.jpg", 100, time.Hour)
	tmp := put("tmp/render.mp4", 10, 90*24*time.Hour)

	n, b := pruneCache(dir, mediaCacheIdle, 150, now)
	if n != 2 || b != 110 {
		t.Fatalf("removed %d files, %d bytes", n, b)
	}
	for p, want := range map[string]bool{stale: false, oldest: false, newer: true, tmp: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Fatalf("%s: exists=%v", p, err == nil)
		}
	}
	// No cap: only idle files go.
	if n, _ := pruneCache(dir, mediaCacheIdle, 0, now); n != 0 {
		t.Fatalf("no cap removed %d", n)
	}
}

func TestPruneRecords(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.srv
	admin, _ := e.store.UserByName(ctx, "admin")
	now := time.Now()

	// Expired links go 90 days after expiry, with their recipients.
	mk := func(id string, exp int64, disabled bool) {
		sh := &store.Share{ID: id, TokenHash: id, TokenEnc: id, OwnerID: admin.ID, DriveID: "files", Path: "projects", IsDir: true,
			CanView: true, CanDownload: true, ExpiresAt: exp, Disabled: disabled, ViewMode: "list", Access: "link"}
		if err := e.store.CreateShare(ctx, sh); err != nil {
			t.Fatal(err)
		}
	}
	mk("old", now.Add(-100*24*time.Hour).Unix(), false)
	mk("recent", now.Add(-10*24*time.Hour).Unix(), false)
	mk("off", 0, true)
	if _, err := e.store.UpsertRecipient(ctx, "r1", "old", "a@example.com", "", true); err != nil {
		t.Fatal(err)
	}
	s.prune(ctx, now)
	if _, err := e.store.ShareByID(ctx, "old"); err != store.ErrNotFound {
		t.Fatalf("expired link kept: %v", err)
	}
	if rs, _ := e.store.Recipients(ctx, "old"); len(rs) != 0 {
		t.Fatalf("recipients kept: %v", rs)
	}
	for _, id := range []string{"recent", "off"} {
		if _, err := e.store.ShareByID(ctx, id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	// A link turned off goes 90 days after it was first seen off.
	s.prune(ctx, now.Add(91*24*time.Hour))
	if _, err := e.store.ShareByID(ctx, "off"); err != store.ErrNotFound {
		t.Fatalf("disabled link kept: %v", err)
	}

	// Backup runs: older than a year go, but each backup keeps 50 and its
	// last good run.
	b := &store.Backup{ID: "b1", Name: "Nightly", SourceDrive: "files", SourcePath: ".", TargetDrive: "files", TargetPath: ".backups/x", Keep: 3, Enabled: true}
	if err := e.store.CreateBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		r, _ := e.store.StartBackupRun(ctx, "b1")
		r.FinishedAt, r.State = r.StartedAt, "failed"
		if i == 0 {
			r.State = "done"
		}
		e.store.SaveBackupRun(ctx, r)
	}
	n, err := e.store.PruneBackupRuns(ctx, now.Add(time.Hour).Unix(), backupRunsMin)
	if err != nil || n != 9 {
		t.Fatalf("pruned %d runs: %v", n, err)
	}
	runs, _ := e.store.BackupRuns(ctx, "b1", 100)
	if len(runs) != 51 || runs[len(runs)-1].State != "done" {
		t.Fatalf("kept %d runs", len(runs))
	}

	// Media jobs: done and failed ones after 30 days.
	if _, err := e.store.EnqueueMedia(ctx, "files", "a.mov", store.MediaProxy, 1, 1); err != nil {
		t.Fatal(err)
	}
	j, _ := e.store.ClaimMedia(ctx, 0)
	e.store.FinishMedia(ctx, j, "done", "")
	e.store.EnqueueMedia(ctx, "files", "b.mov", store.MediaProxy, 1, 1)
	if n, _ := e.store.PruneMediaJobs(ctx, now.Add(time.Hour).Unix()); n != 1 {
		t.Fatalf("pruned %d jobs", n)
	}
	if q, _, _ := e.store.MediaQueue(ctx); q != 1 {
		t.Fatalf("queued job pruned")
	}
}

func TestPruneSentFolders(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.client(e.app.URL)
	c.login("admin")
	os.MkdirAll(filepath.Join(e.storage, "Sent/2026-01-01 Cut"), 0o755)
	os.WriteFile(filepath.Join(e.storage, "Sent/2026-01-01 Cut/a.mov"), []byte("a"), 0o644)
	os.MkdirAll(filepath.Join(e.storage, "Sent/2026-01-01 Later"), 0o755)
	os.MkdirAll(filepath.Join(e.storage, "Elsewhere"), 0o755)
	exp := time.Now().Add(time.Hour).Unix()
	sent, _ := createShare(t, c, map[string]any{"place": adminPlace, "path": "/Sent/2026-01-01 Cut", "expiresAt": exp, "sent": true})
	later, _ := createShare(t, c, map[string]any{"place": adminPlace, "path": "/Sent/2026-01-01 Later", "expiresAt": exp, "sent": true})
	createShare(t, c, map[string]any{"place": adminPlace, "path": "/Elsewhere", "expiresAt": exp, "sent": true})
	marks, _ := e.store.KVPrefix(ctx, "sent:")
	if len(marks) != 2 || marks[sent] == "" || marks[later] == "" {
		t.Fatalf("marks: %v", marks)
	}
	// Someone added to one of them after it was sent.
	future := time.Now().Add(time.Hour)
	os.WriteFile(filepath.Join(e.storage, "Sent/2026-01-01 Later/added.txt"), []byte("x"), 0o644)
	os.Chtimes(filepath.Join(e.storage, "Sent/2026-01-01 Later/added.txt"), future, future)

	// Not yet 30 days past expiry: nothing moves.
	e.srv.pruneSent(ctx, time.Now().Add(29*24*time.Hour))
	if _, err := os.Stat(filepath.Join(e.storage, "Sent/2026-01-01 Cut")); err != nil {
		t.Fatal("pruned early")
	}
	e.srv.pruneSent(ctx, time.Now().Add(31*24*time.Hour))
	if _, err := os.Stat(filepath.Join(e.storage, "Sent/2026-01-01 Cut")); !os.IsNotExist(err) {
		t.Fatal("expired sent folder kept")
	}
	if _, err := os.Stat(filepath.Join(e.storage, "Sent/2026-01-01 Later/added.txt")); err != nil {
		t.Fatal("changed sent folder removed")
	}
	if _, err := os.Stat(filepath.Join(e.storage, "Elsewhere")); err != nil {
		t.Fatal("folder outside Sent removed")
	}
	items, _ := e.store.TrashItems(ctx)
	if len(items) != 1 || items[0].OrigPath != "Sent/2026-01-01 Cut" {
		t.Fatalf("trash: %+v", items)
	}
	if marks, _ := e.store.KVPrefix(ctx, "sent:"); len(marks) != 0 {
		t.Fatalf("marks left: %v", marks)
	}
}
