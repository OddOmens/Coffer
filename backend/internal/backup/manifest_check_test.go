package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A manifest is read back from the target drive, which others may write to:
// entries that climb out of the restore folder or point at odd objects must
// be refused, not restored (or crash the server on a short object name).
func TestLoadRefusesTamperedManifest(t *testing.T) {
	ctx := context.Background()
	dst, _ := local(t)
	good := strings.Repeat("ab", 32) + "-1700000000-3"
	cases := map[string]Manifest{
		"parent path":   {Files: []File{{P: "../../escaped.txt", K: good}}},
		"absolute path": {Files: []File{{P: "/etc/x", K: good}}},
		"internal dir":  {Files: []File{{P: "a/.coffer/uploads/x", K: good}}},
		"short object":  {Files: []File{{P: "a.txt", K: "a"}}},
		"object path":   {Files: []File{{P: "a.txt", K: "../../snapshots/x"}}},
		"parent dir":    {Dirs: []string{"ok", "../up"}},
	}
	i := 0
	for name, m := range cases {
		i++
		snap := time.Unix(1_700_000_000+int64(i), 0).UTC().Format(nameFormat)
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		json.NewEncoder(zw).Encode(m)
		zw.Close()
		if err := dst.MkdirAll(ctx, "b/snapshots"); err != nil {
			t.Fatal(err)
		}
		if err := dst.WriteFile(ctx, "b/snapshots/"+snap+".json.gz", &buf, int64(buf.Len())); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(ctx, dst, "b", snap); err == nil {
			t.Errorf("%s: tampered manifest was accepted", name)
		}
	}
}

func TestLoadAcceptsRunOutput(t *testing.T) {
	ctx := context.Background()
	src, srcDir := local(t)
	dst, _ := local(t)
	write(t, srcDir, "Old/photo.jpg", "x", time.Unix(-86400, 0)) // before 1970: negative mtime
	write(t, srcDir, "a b/ü.txt", "y", time.Unix(1_700_000_000, 0))
	snap, _, err := Run(ctx, src, ".", nil, dst, "b", nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(ctx, dst, "b", snap)
	if err != nil || len(m.Files) != 2 {
		t.Fatalf("load: %v %+v", err, m)
	}
}
