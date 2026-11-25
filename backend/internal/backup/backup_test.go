package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/files"
)

func local(t *testing.T) (files.Drive, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := files.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, dir
}

func write(t *testing.T, dir, rel, content string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRestorePrune(t *testing.T) {
	ctx := context.Background()
	src, srcDir := local(t)
	dst, _ := local(t)
	t0 := time.Unix(1_700_000_000, 0)
	write(t, srcDir, "Projects/a.txt", "one", t0)
	write(t, srcDir, "Projects/b.txt", "two", t0)
	if err := os.MkdirAll(filepath.Join(srcDir, "Empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	snap1, pr, err := Run(ctx, src, ".", nil, dst, ".backups/main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Files != 2 || pr.Uploaded != 2 {
		t.Fatalf("first run: %+v", pr)
	}
	time.Sleep(1100 * time.Millisecond) // snapshot names have one-second resolution

	// Change one file: only it is uploaded again.
	write(t, srcDir, "Projects/a.txt", "one, edited", t0.Add(time.Hour))
	snap2, pr, err := Run(ctx, src, ".", nil, dst, ".backups/main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Files != 2 || pr.Uploaded != 1 {
		t.Fatalf("second run: %+v", pr)
	}

	// The first snapshot still has the old text.
	m1, err := Load(ctx, dst, ".backups/main", snap1)
	if err != nil {
		t.Fatal(err)
	}
	top := Browse(m1, ".")
	if len(top) != 2 || top[0].Name != "Empty" || top[1].Name != "Projects" || top[1].Items != 2 {
		t.Fatalf("browse: %+v", top)
	}
	if n, err := Restore(ctx, dst, ".backups/main", m1, []string{"Projects/a.txt", "Empty"}, src, "Restored"); err != nil || n != 1 {
		t.Fatalf("restore: %d %v", n, err)
	}
	if b := read(t, src, "Restored/Projects/a.txt"); b != "one" {
		t.Fatalf("restored %q", b)
	}
	if !src.Exists(ctx, "Restored/Empty") {
		t.Fatal("empty folder not restored")
	}

	// Keep one snapshot: the old version of a.txt goes.
	rs, ro, err := Prune(ctx, dst, ".backups/main", 1)
	if err != nil || rs != 1 || ro != 1 {
		t.Fatalf("prune: %d %d %v", rs, ro, err)
	}
	m2, err := Load(ctx, dst, ".backups/main", snap2)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := Restore(ctx, dst, ".backups/main", m2, []string{"."}, src, "Restored2"); err != nil || n != 2 {
		t.Fatalf("restore all: %d %v", n, err)
	}
	if b := read(t, src, "Restored2/Projects/a.txt"); b != "one, edited" {
		t.Fatalf("restored %q", b)
	}
	if _, err := Load(ctx, dst, ".backups/main", "../../etc/passwd"); err == nil {
		t.Fatal("bad snapshot name accepted")
	}
}

func read(t *testing.T, d files.Drive, p string) string {
	t.Helper()
	rc, _, err := d.Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return strings.TrimSpace(string(b))
}

func TestItems(t *testing.T) {
	got := Items("Biz", []string{"Biz/03/Docs", "Biz/02", "Biz/02 old", "Biz/02/x", "Other", "Biz/03/Docs", "Biz/02 old/y"})
	if strings.Join(got, "|") != "Biz/02|Biz/02 old|Biz/03/Docs" {
		t.Fatalf("items: %q", got)
	}
	if got := Items(".", []string{"a/b", ".", "c"}); len(got) != 1 || got[0] != "." {
		t.Fatalf("root picked: %q", got)
	}
}

// Picked items only, kept by their path inside the backed-up folder.
func TestRunPickedItems(t *testing.T) {
	ctx := context.Background()
	src, srcDir := local(t)
	dst, _ := local(t)
	t0 := time.Unix(1_700_000_000, 0)
	write(t, srcDir, "Biz/02/plan.txt", "plan", t0)
	write(t, srcDir, "Biz/03/Docs/brief.txt", "brief", t0)
	write(t, srcDir, "Biz/03/Branding/logo.svg", "logo", t0)
	write(t, srcDir, "Biz/notes.txt", "notes", t0)
	if err := os.MkdirAll(filepath.Join(srcDir, "Biz/05"), 0o755); err != nil {
		t.Fatal(err)
	}
	snap, pr, err := Run(ctx, src, ".", []string{"Biz/03/Docs", "Biz/02", "Biz/notes.txt", "Biz/05", "Biz/gone"}, dst, "b", nil)
	if err != nil || pr.Files != 3 || len(pr.Missing) != 1 || pr.Missing[0] != "Biz/gone" {
		t.Fatalf("run: %+v %v", pr, err)
	}
	m, err := Load(ctx, dst, "b", snap)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range m.Files {
		paths = append(paths, f.P)
	}
	if strings.Join(paths, "|") != "Biz/02/plan.txt|Biz/03/Docs/brief.txt|Biz/notes.txt" || strings.Join(m.Dirs, "|") != "Biz/05" {
		t.Fatalf("manifest: %q %q", paths, m.Dirs)
	}
}
