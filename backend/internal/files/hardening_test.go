package files

import (
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestInternalDirAnyCase(t *testing.T) {
	for _, p := range []string{".COFFER/trash", ".Coffer", "/.cOFFER/uploads/x.part"} {
		if _, err := Clean(p); err == nil {
			t.Errorf("Clean(%q) reached the internal dir", p)
		}
	}
	if ValidName(".Coffer") == nil || ValidEntryName(".COFFER") {
		t.Error("internal dir name accepted in another case")
	}
}

func TestConfineSymlinks(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "clients/acme/sub"), 0o755)
	os.MkdirAll(filepath.Join(dir, "clients/other"), 0o755)
	os.WriteFile(filepath.Join(dir, "clients/other/secret.txt"), []byte("secret"), 0o644)
	if err := os.Symlink("../other", filepath.Join(dir, "clients/acme/peek")); err != nil {
		t.Skip("symlinks unsupported")
	}
	os.Symlink("sub", filepath.Join(dir, "clients/acme/inside"))
	os.Symlink("../../.coffer", filepath.Join(dir, "clients/acme/internal"))
	os.Symlink("loop", filepath.Join(dir, "clients/acme/loop"))
	d, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	for _, p := range []string{"peek", "peek/secret.txt", "internal", "internal/trash", "loop"} {
		if got, err := Confine(d, "clients/acme", p); err == nil {
			t.Errorf("Confine(%q) = %q, want an error", p, got)
		}
	}
	// os.Root alone follows a link that stays on the drive.
	if _, _, err := d.Open(t.Context(), "clients/acme/peek/secret.txt"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	for _, p := range []string{"inside", "inside/new.txt", "sub", "missing/new.txt", ""} {
		if _, err := Confine(d, "clients/acme", p); err != nil {
			t.Errorf("Confine(%q): %v", p, err)
		}
	}
	if _, err := Confine(d, ".", "clients/acme/internal/trash"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("link into the internal dir: %v", err)
	}
	// A place whose root is itself a link still works.
	os.Symlink("clients/acme", filepath.Join(dir, "acme-link"))
	if _, err := Confine(d, "acme-link", "sub"); err != nil {
		t.Errorf("linked place root: %v", err)
	}
}

func TestConcurrentRenamesNeverOverwrite(t *testing.T) {
	dir := t.TempDir()
	const n = 20
	for i := range n {
		os.WriteFile(filepath.Join(dir, "f"+string(rune('a'+i))), []byte{byte(i)}, 0o644)
	}
	d, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d.Rename(t.Context(), "f"+string(rune('a'+i)), "target") == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	es, _ := d.List(t.Context(), ".", false)
	if ok != 1 || len(es) != n {
		t.Fatalf("%d renames succeeded, %d files left (want 1 and %d)", ok, len(es), n)
	}
}

func TestDecodableCaps(t *testing.T) {
	for _, c := range []struct {
		cfg  image.Config
		want bool
	}{
		{image.Config{ColorModel: color.RGBAModel, Width: 6000, Height: 4000}, true},
		{image.Config{ColorModel: color.RGBAModel, Width: 10000, Height: 6000}, false},
		{image.Config{ColorModel: color.NRGBA64Model, Width: 6000, Height: 4000}, true},
		{image.Config{ColorModel: color.NRGBA64Model, Width: 7000, Height: 5000}, false},
		{image.Config{ColorModel: color.RGBAModel, Width: 0, Height: 10}, false},
	} {
		if got := decodable(c.cfg); got != c.want {
			t.Errorf("%dx%d %v: got %v", c.cfg.Width, c.cfg.Height, c.cfg.ColorModel, got)
		}
	}
}
