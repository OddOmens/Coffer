package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/files"
)

// flaky fails to open the files in fail, the way a bucket's 500 fails an
// upload, and counts the tries.
type flaky struct {
	files.Drive
	fail  map[string]bool
	tries map[string]int
}

func (f *flaky) Open(ctx context.Context, p string) (io.ReadSeekCloser, files.Entry, error) {
	f.tries[p]++
	if f.fail[p] {
		return nil, files.Entry{}, errors.New("internal incident")
	}
	return f.Drive.Open(ctx, p)
}

func TestRunSkipsFailedFiles(t *testing.T) {
	waits := retryWaits
	retryWaits = []time.Duration{0, 0}
	t.Cleanup(func() { retryWaits = waits })

	ctx := context.Background()
	d, srcDir := local(t)
	src := &flaky{Drive: d, fail: map[string]bool{}, tries: map[string]int{}}
	dst, _ := local(t)
	t0, t1 := time.Unix(1_700_000_000, 0), time.Unix(1_700_100_000, 0)
	write(t, srcDir, "a.txt", "one", t0)
	write(t, srcDir, "b.txt", "two", t0)
	if _, _, err := Run(ctx, src, ".", nil, dst, "bk", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // snapshot names have one-second resolution

	// a.txt changed and c.txt is new, but neither can be read: the run goes
	// on, keeps a.txt's last version, and leaves c.txt out.
	write(t, srcDir, "a.txt", "one, edited", t1)
	write(t, srcDir, "b.txt", "two, edited", t1)
	write(t, srcDir, "c.txt", "three", t1)
	src.fail = map[string]bool{"a.txt": true, "c.txt": true}
	src.tries = map[string]int{}
	snap, pr, err := Run(ctx, src, ".", nil, dst, "bk", nil)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Failed != 2 || pr.Uploaded != 1 || pr.Files != 2 || len(pr.Errors) != 2 ||
		!strings.HasPrefix(pr.Errors[0], "a.txt: internal incident") || !strings.HasPrefix(pr.Errors[1], "c.txt: ") {
		t.Fatalf("run with failures: %+v", pr)
	}
	if src.tries["a.txt"] != 1+len(retryWaits) {
		t.Fatalf("a.txt tried %d times", src.tries["a.txt"])
	}
	m, err := Load(ctx, dst, "bk", snap)
	if err != nil {
		t.Fatal(err)
	}
	if a := m.Find("a.txt"); a == nil || a.M != t0.Unix() {
		t.Fatalf("a.txt in the snapshot: %+v", a)
	}
	if b := m.Find("b.txt"); b == nil || b.M != t1.Unix() {
		t.Fatalf("b.txt in the snapshot: %+v", b)
	}
	if m.Find("c.txt") != nil {
		t.Fatal("c.txt is in the snapshot without a copy")
	}
	time.Sleep(1100 * time.Millisecond)

	// The next run picks them up.
	src.fail = nil
	snap, pr, err = Run(ctx, src, ".", nil, dst, "bk", nil)
	if err != nil || pr.Failed != 0 || pr.Uploaded != 2 || pr.Files != 3 {
		t.Fatalf("next run: %+v %v", pr, err)
	}
	if m, _ = Load(ctx, dst, "bk", snap); m.Find("a.txt").M != t1.Unix() || m.Find("c.txt") == nil {
		t.Fatalf("next snapshot: %+v", m.Files)
	}
}

func TestRunStopsWhenEverythingFails(t *testing.T) {
	waits := retryWaits
	retryWaits = nil
	t.Cleanup(func() { retryWaits = waits })

	ctx := context.Background()
	d, srcDir := local(t)
	src := &flaky{Drive: d, fail: map[string]bool{}, tries: map[string]int{}}
	dst, _ := local(t)
	for i := range maxFailedInARow + 10 {
		p := fmt.Sprintf("f%02d.txt", i)
		write(t, srcDir, p, p, time.Unix(1_700_000_000, 0))
		src.fail[p] = true
	}
	_, pr, err := Run(ctx, src, ".", nil, dst, "bk", nil)
	if err == nil || !strings.Contains(err.Error(), "in a row") || pr.Failed != maxFailedInARow {
		t.Fatalf("got %+v %v", pr, err)
	}
	if snaps, _ := Snapshots(ctx, dst, "bk"); len(snaps) != 0 {
		t.Fatal("a snapshot was written")
	}
}
