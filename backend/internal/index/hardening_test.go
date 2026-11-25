package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndexFilesOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	check := func(p string) {
		t.Helper()
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if m := info.Mode().Perm(); m != 0o600 {
			t.Errorf("%s has mode %o, want 600", filepath.Base(p), m)
		}
	}

	// A new index.
	p := filepath.Join(dir, "index.db")
	ix, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.db.Exec("INSERT INTO scans (drive, started) VALUES ('x', 1)"); err != nil {
		t.Fatal(err)
	}
	check(p)
	if _, err := os.Stat(p + "-wal"); err == nil {
		check(p + "-wal")
	}
	ix.Close()

	// An existing index left world-readable by an older version.
	os.Chmod(p, 0o664)
	os.WriteFile(p+"-shm", nil, 0o664)
	os.Chmod(p+"-shm", 0o664)
	ix, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	check(p)
	check(p + "-shm")
}
