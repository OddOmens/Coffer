package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/files"
)

// A source that suddenly has far fewer files (an unmounted disk) makes no
// snapshot unless forced.
func TestRunHeldBackWhenSourceShrinks(t *testing.T) {
	ctx := context.Background()
	src, srcDir := local(t)
	dst, _ := local(t)
	t0 := time.Unix(1_700_000_000, 0)
	for i := range 30 {
		write(t, srcDir, fmt.Sprintf("p/%02d.txt", i), "x", t0)
	}
	if _, _, err := Run(ctx, src, ".", nil, dst, "b", nil); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		os.Remove(filepath.Join(srcDir, fmt.Sprintf("p/%02d.txt", i)))
	}
	time.Sleep(1100 * time.Millisecond)
	_, _, err := Run(ctx, src, ".", nil, dst, "b", nil)
	var se *ShrunkError
	if !errors.As(err, &se) || se.Files != 10 || se.PrevFiles != 30 {
		t.Fatalf("not held back: %v", err)
	}
	if snaps, _ := Snapshots(ctx, dst, "b"); len(snaps) != 1 {
		t.Fatalf("snapshot made anyway: %d", len(snaps))
	}
	if _, _, err := RunWith(ctx, src, ".", nil, dst, "b", nil, Options{Force: true}); err != nil {
		t.Fatalf("forced: %v", err)
	}

	// An empty source after a snapshot with files is always held back.
	empty, _ := local(t)
	time.Sleep(1100 * time.Millisecond)
	if _, _, err := Run(ctx, empty, ".", nil, dst, "b", nil); !errors.As(err, &se) {
		t.Fatalf("empty source: %v", err)
	}
	if shrunk(5, 0, 10, 0) || !shrunk(0, 0, 1, 1) || !shrunk(9, 1<<20, 20, 1<<20) || !shrunk(5, 1, 5, 200<<20) {
		t.Fatal("shrunk thresholds")
	}
}

func TestRunMarksUnreadablePartial(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	ctx := context.Background()
	src, srcDir := local(t)
	dst, _ := local(t)
	t0 := time.Unix(1_700_000_000, 0)
	write(t, srcDir, "ok/a.txt", "a", t0)
	write(t, srcDir, "locked/b.txt", "b", t0)
	os.Chmod(filepath.Join(srcDir, "locked"), 0o000)
	t.Cleanup(func() { os.Chmod(filepath.Join(srcDir, "locked"), 0o755) })
	snap, pr, err := Run(ctx, src, ".", nil, dst, "b", nil)
	if err != nil || pr.Unreadable != 1 || len(pr.UnreadablePaths) != 1 || pr.UnreadablePaths[0] != "locked" {
		t.Fatalf("run: %+v %v", pr, err)
	}
	m, err := Load(ctx, dst, "b", snap)
	if err != nil || !m.Partial {
		t.Fatalf("manifest not partial: %v", err)
	}
}

// writeSnap stores a manifest as a snapshot made at c.
func writeSnap(t *testing.T, d files.Drive, root string, c time.Time, m Manifest) string {
	t.Helper()
	name := c.UTC().Format(nameFormat)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	json.NewEncoder(zw).Encode(m)
	zw.Close()
	ctx := context.Background()
	d.MkdirAll(ctx, root+"/snapshots")
	if err := d.WriteFile(ctx, root+"/snapshots/"+name+".json.gz", &buf, int64(buf.Len())); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestPruneRetention(t *testing.T) {
	ctx := context.Background()
	dst, _ := local(t)
	t1 := time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC)
	now = func() time.Time { return t1 }
	t.Cleanup(func() { now = time.Now })
	full := Manifest{Version: 1, Files: []File{{P: "a", S: 1, K: objectName("a", 1, 1, nil)}}}
	// A nightly snapshot for 400 days.
	names := map[int]string{}
	for i := range 400 {
		names[i] = writeSnap(t, dst, "b", t1.AddDate(0, 0, -i), full)
	}
	if _, _, err := Prune(ctx, dst, "b", 7); err != nil {
		t.Fatal(err)
	}
	snaps, _ := Snapshots(ctx, dst, "b")
	have := map[string]bool{}
	for _, sn := range snaps {
		have[sn.Name] = true
	}
	for i := range 7 {
		if !have[names[i]] {
			t.Fatalf("newest %d pruned", i)
		}
	}
	// About 8 weekly and 12 monthly on top of the newest 7, never all.
	if len(snaps) < 7+6 || len(snaps) > 7+8+13 {
		t.Fatalf("kept %d snapshots", len(snaps))
	}
	months := map[time.Month]bool{}
	for _, sn := range snaps {
		months[time.Unix(sn.Created, 0).UTC().Month()] = true
	}
	if len(months) != 12 {
		t.Fatalf("snapshots kept from %d months", len(months))
	}
	if have[names[399]] {
		t.Fatal("kept a snapshot older than a year")
	}
}

// When the latest runs were partial or empty, the last good snapshot stays.
func TestPruneKeepsNewestGood(t *testing.T) {
	ctx := context.Background()
	dst, _ := local(t)
	t1 := time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC)
	now = func() time.Time { return t1 }
	t.Cleanup(func() { now = time.Now })
	good := writeSnap(t, dst, "b", t1.AddDate(-2, 0, 0), Manifest{Version: 1, Files: []File{{P: "a", S: 1, K: objectName("a", 1, 1, nil)}}})
	for i := range 5 {
		writeSnap(t, dst, "b", t1.Add(time.Duration(-i)*time.Hour), Manifest{Version: 1, Partial: true, Files: []File{{P: "b", S: 1, K: objectName("b", 1, 1, nil)}}})
	}
	if _, _, err := Prune(ctx, dst, "b", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ctx, dst, "b", good); err != nil {
		t.Fatalf("good snapshot pruned: %v", err)
	}
}
