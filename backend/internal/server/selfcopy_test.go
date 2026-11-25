package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/oddomens/coffer/internal/backup"
	"github.com/oddomens/coffer/internal/store"
)

func TestCopySelfIntoBackup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	s := e.srv
	m, _ := s.drive("files")
	b := &store.Backup{ID: "b1", Name: "Nightly", TargetDrive: "files", TargetPath: ".backups/x"}
	snap := "20260101T020000Z"
	s.copySelf(ctx, b, m, snap)
	dir := filepath.Join(e.storage, ".backups/x", backup.SelfDir, snap)
	db := filepath.Join(dir, "coffer.db")
	cp, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if u, err := cp.UserByName(ctx, "admin"); err != nil || u.Username != "admin" {
		t.Fatalf("copy unreadable: %v", err)
	}
	if k, err := os.ReadFile(filepath.Join(dir, "secret.key")); err != nil || len(k) < 64 {
		t.Fatalf("key: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(e.srv.cfg.DataDir, "selfcopy-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}

	// Turned off: nothing is written.
	st := s.cur()
	st.SelfBackup = "off"
	s.settings.Store(&st)
	s.copySelf(ctx, b, m, "20260102T020000Z")
	if _, err := os.Stat(filepath.Join(e.storage, ".backups/x", backup.SelfDir, "20260102T020000Z")); !os.IsNotExist(err) {
		t.Fatal("copied while off")
	}
}
