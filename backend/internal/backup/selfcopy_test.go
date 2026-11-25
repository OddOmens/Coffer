package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSelfCopyKeepsNewest(t *testing.T) {
	ctx := context.Background()
	dst, dstDir := local(t)
	src := t.TempDir()
	db, key := filepath.Join(src, "db"), filepath.Join(src, "key")
	os.WriteFile(db, []byte("database"), 0o600)
	os.WriteFile(key, []byte("key"), 0o600)

	t0 := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	for i := range SelfKeep + 2 {
		snap := t0.AddDate(0, 0, i).Format(nameFormat)
		if err := WriteSelfCopy(ctx, dst, ".backups/main", snap, map[string]string{"coffer.db": db, "secret.key": key}); err != nil {
			t.Fatal(err)
		}
	}
	names, err := SelfCopies(ctx, dst, ".backups/main")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != SelfKeep || names[0] != t0.AddDate(0, 0, 2).Format(nameFormat) {
		t.Fatalf("kept %v", names)
	}
	last := filepath.Join(dstDir, ".backups/main", SelfDir, names[len(names)-1])
	for name, want := range map[string]string{"coffer.db": "database", "secret.key": "key"} {
		if b, err := os.ReadFile(filepath.Join(last, name)); err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", name, b, err)
		}
	}
	// Snapshots and their manifests are untouched by the copies.
	if snaps, err := Snapshots(ctx, dst, ".backups/main"); err != nil || len(snaps) != 0 {
		t.Fatalf("snapshots: %v %v", snaps, err)
	}
	if err := WriteSelfCopy(ctx, dst, ".backups/main", "../x", nil); err == nil {
		t.Fatal("accepted a bad snapshot name")
	}
}
