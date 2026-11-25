package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	cases := map[string]string{
		"":                 ".",
		"/":                ".",
		"a/b":              "a/b",
		"/a/../b":          "b",
		"../../etc/passwd": "etc/passwd",
		"a\\..\\..\\x":     "x",
		"./a/./b/":         "a/b",
	}
	for in, want := range cases {
		got, err := Clean(in)
		if err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{".coffer", "/.coffer/trash", "a\x00b", "../.coffer/uploads"} {
		if _, err := Clean(bad); err == nil {
			t.Errorf("Clean(%q) should fail", bad)
		}
	}
}

func TestJoinStaysInScope(t *testing.T) {
	for _, p := range []string{"../../other", "/..", "x/../../../y", ""} {
		got, err := Join("clients/acme", p)
		if err != nil {
			t.Fatal(err)
		}
		if !Within("clients/acme", got) {
			t.Errorf("Join escaped scope: %q -> %q", p, got)
		}
	}
	if Within("clients/acme", "clients/acme-evil/x") {
		t.Error("prefix sibling treated as inside scope")
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"render_v2.mov", "Final (copy).aep", "日本語.txt", ".hidden"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("ValidName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", "a\\b", " lead", "trail ", "dot.", "x\ny", ".coffer"} {
		if ValidName(bad) == nil {
			t.Errorf("ValidName(%q) should fail", bad)
		}
	}
}

func TestFixName(t *testing.T) {
	long := strings.Repeat("é", 200) + ".mov"
	for in, want := range map[string]string{
		"render_v2.mov": "render_v2.mov",
		"x\ny\x7f.txt":  "xy.txt",
		"a\\b":          "a_b",
		" lead":         "lead",
		"trail. . ":     "trail",
		"...":           "_",
		"\t":            "_",
		"bad\xffutf8":   "bad_utf8",
		".coffer":       "",
		long:            strings.Repeat("é", 125) + ".mov",
	} {
		got := FixName(in)
		if got != want {
			t.Errorf("FixName(%q) = %q, want %q", in, got, want)
		}
		if got != "" && ValidName(got) != nil {
			t.Errorf("FixName(%q) = %q, which ValidName refuses", in, got)
		}
	}
}

func TestSymlinkEscapeBlocked(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600)
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skip("symlinks unsupported")
	}
	os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "file-escape"))
	fsys, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	ctx := t.Context()
	if _, _, err := fsys.Open(ctx, "escape/secret.txt"); err == nil {
		t.Fatal("read through a symlinked directory that leaves the root")
	}
	if _, _, err := fsys.Open(ctx, "file-escape"); err == nil {
		t.Fatal("read through a symlinked file that leaves the root")
	}
	es, err := fsys.List(ctx, ".", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Name == "escape" || e.Name == "file-escape" {
			t.Errorf("escaping symlink %q listed", e.Name)
		}
	}
}
