package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDriveMarker(t *testing.T) {
	// A new, empty drive is marked.
	dir := t.TempDir()
	l, err := OpenLocalDrive(dir, "abc", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Check("abc"); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := l.Check("other"); !errors.Is(err, ErrWrongDrive) {
		t.Fatalf("other id: %v", err)
	}
	l.Close()

	// An empty, unmarked folder for a drive that had files: not mounted.
	// Not even Coffer's own folder is made in it.
	empty := t.TempDir()
	if _, err := OpenLocalDrive(empty, "abc", false, nil); !errors.Is(err, ErrNotMounted) {
		t.Fatalf("empty mount point: %v", err)
	}
	if _, err := os.Stat(filepath.Join(empty, InternalDir)); !os.IsNotExist(err) {
		t.Fatalf("internal dir made in an unmounted folder: %v", err)
	}
	// One that an older Coffer left its folder in counts as empty too.
	os.MkdirAll(filepath.Join(empty, InternalDir, "trash"), 0o755)
	if _, err := OpenLocalDrive(empty, "abc", false, nil); !errors.Is(err, ErrNotMounted) {
		t.Fatalf("mount point with .coffer: %v", err)
	}

	// An existing drive with files (an upgrade) is adopted.
	full := t.TempDir()
	os.WriteFile(filepath.Join(full, "a.txt"), []byte("a"), 0o644)
	l, err = OpenLocalDrive(full, "abc", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	// ...and isn't another drive's.
	if _, err := OpenLocalDrive(full, "xyz", false, nil); !errors.Is(err, ErrWrongDrive) {
		t.Fatalf("other drive: %v", err)
	}
	// Two drives set up on one folder before markers existed.
	l, err = OpenLocalDrive(full, "two", false, []string{"abc"})
	if err != nil {
		t.Fatalf("shared folder: %v", err)
	}
	l.Close()
	// Unless an admin picks the folder for it: both share it.
	if err := MarkLocalDrive(full, "xyz"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"abc", "xyz"} {
		l, err := OpenLocalDrive(full, id, false, nil)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		l.Close()
	}
}

func TestDriveCheckNoticesUnmount(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "disk")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	l, err := OpenLocalDrive(dir, "abc", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// The disk goes away, leaving an empty folder at its path.
	if err := os.Rename(dir, filepath.Join(parent, "gone")); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0o755)
	if err := l.Check("abc"); !errors.Is(err, ErrNotMounted) {
		t.Fatalf("unmounted: %v", err)
	}
	// It's mounted again: the same marker, but a new folder to open.
	os.Remove(dir)
	if err := os.Rename(filepath.Join(parent, "gone"), dir); err != nil {
		t.Fatal(err)
	}
	if err := l.Check("abc"); err != nil && !errors.Is(err, ErrRemounted) {
		t.Fatalf("back: %v", err)
	}
}

func TestWalkReportsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ok"), 0o755)
	os.WriteFile(filepath.Join(dir, "ok/a.txt"), []byte("a"), 0o644)
	os.MkdirAll(filepath.Join(dir, "locked/inner"), 0o755)
	os.Chmod(filepath.Join(dir, "locked"), 0o000)
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "locked"), 0o755) })
	l, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	u := &Unreadable{}
	n := 0
	if err := l.Walk(WithUnreadable(t.Context(), u), ".", func(Entry) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if c, paths := u.Count(); c != 1 || len(paths) != 1 || paths[0] != "locked" {
		t.Fatalf("unreadable: %d %q", c, paths)
	}
}

func TestFinishUploadReplaceKeepsOld(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "doc.txt"), []byte("old"), 0o644)
	l, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	up := &UploadState{ID: "upload0123456789ab", Size: 3, Dir: ".", Name: "doc.txt", Overwrite: true}
	if err := l.StartUpload(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := l.WriteChunk(ctx, up, 0, 3, strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	final, replaced, err := l.FinishUpload(ctx, up, "trash0123456789ab")
	if err != nil || !replaced || final != "doc.txt" {
		t.Fatalf("finish: %q %v %v", final, replaced, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "doc.txt")); string(b) != "new" {
		t.Fatalf("in place: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, trashDir, "trash0123456789ab")); string(b) != "old" {
		t.Fatalf("in trash: %q", b)
	}

}

func TestCleanStaleWrites(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a"), 0o755)
	old := filepath.Join(dir, "a", writeTmpPrefix+"1")
	fresh := filepath.Join(dir, writeTmpPrefix+"2")
	for _, p := range []string{old, fresh} {
		os.WriteFile(p, []byte("x"), 0o644)
	}
	week := time.Now().Add(-7 * 24 * time.Hour)
	os.Chtimes(old, week, week)
	l, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if n, err := l.CleanStaleWrites(t.Context(), 24*time.Hour); err != nil || n != 1 {
		t.Fatalf("cleaned %d: %v", n, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("stale write left")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh write removed")
	}
}
