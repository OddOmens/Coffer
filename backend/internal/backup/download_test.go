package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestSnapshotDownloads(t *testing.T) {
	ctx := context.Background()
	src, srcDir := local(t)
	dst, _ := local(t)
	t0 := time.Unix(1_700_000_000, 0)
	write(t, srcDir, "Projects/Acme/cut.txt", "cut", t0)
	write(t, srcDir, "Projects/Acme/notes.txt", "notes", t0)
	write(t, srcDir, "Projects/Beta/b.txt", "beta", t0)
	write(t, srcDir, "top.txt", "top", t0)
	if err := os.MkdirAll(filepath.Join(srcDir, "Projects/Empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	snap, _, err := Run(ctx, src, ".", nil, dst, ".backups/main", nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(ctx, dst, ".backups/main", snap)
	if err != nil {
		t.Fatal(err)
	}

	// One file, read straight from the backup.
	f := m.Find("Projects/Acme/notes.txt")
	if f == nil || m.Find("Projects/Acme") != nil || m.Find("nope.txt") != nil {
		t.Fatal("find")
	}
	rc, err := Open(ctx, dst, ".backups/main", f)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "notes" {
		t.Fatalf("open: %q", got)
	}

	unzip := func(dir string, names ...string) map[string]string {
		t.Helper()
		var buf bytes.Buffer
		if err := WriteZip(ctx, dst, ".backups/main", m, dir, names, &buf); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, zf := range zr.File {
			r, _ := zf.Open()
			b, _ := io.ReadAll(r)
			r.Close()
			out[zf.Name] = string(b)
		}
		return out
	}
	keys := func(m map[string]string) []string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		slices.Sort(ks)
		return ks
	}

	// A whole folder, with paths inside it.
	z := unzip("Projects")
	if want := []string{"Acme/cut.txt", "Acme/notes.txt", "Beta/b.txt", "Empty/"}; !slices.Equal(keys(z), want) {
		t.Fatalf("folder zip: %v", keys(z))
	}
	if z["Acme/cut.txt"] != "cut" {
		t.Fatalf("folder zip content: %q", z["Acme/cut.txt"])
	}
	// Picked entries of a folder only.
	if got, want := keys(unzip("Projects", "Beta", "Empty")), []string{"Beta/b.txt", "Empty/"}; !slices.Equal(got, want) {
		t.Fatalf("picked zip: %v", got)
	}
	// The whole snapshot.
	if got := keys(unzip(".")); len(got) != 5 || got[len(got)-1] != "top.txt" {
		t.Fatalf("snapshot zip: %v", got)
	}
	// A name that merely starts like a picked one stays out.
	if got := keys(unzip("Projects/Acme", "cut")); len(got) != 0 {
		t.Fatalf("prefix leak: %v", got)
	}
}
