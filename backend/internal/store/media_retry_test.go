package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Anyone with a link can ask for a retry: finished renditions must not be
// made again, and failures only retried after a pause.
func TestResetMediaOnlyRetriesOldFailures(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "coffer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state := func(p string) string {
		j, err := s.MediaJob(ctx, "d", p, MediaProxy)
		if err != nil {
			t.Fatal(err)
		}
		return j.State
	}
	set := func(p, st string, age int64) {
		if _, err := s.EnqueueMedia(ctx, "d", p, MediaProxy, 1, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE media_jobs SET state = ?, updated_at = ? WHERE path = ?", st, now()-age, p); err != nil {
			t.Fatal(err)
		}
	}
	set("done.mov", "done", 3600)
	set("fresh.mov", "failed", 5)
	set("old.mov", "failed", 3600)
	for _, p := range []string{"done.mov", "fresh.mov", "old.mov"} {
		if err := s.ResetMedia(ctx, "d", p, MediaProxy); err != nil {
			t.Fatal(err)
		}
	}
	if st := state("done.mov"); st != "done" {
		t.Errorf("finished rendition was queued again: %s", st)
	}
	if st := state("fresh.mov"); st != "failed" {
		t.Errorf("a just-failed rendition was retried at once: %s", st)
	}
	if st := state("old.mov"); st != "queued" {
		t.Errorf("an old failure was not retried: %s", st)
	}
}
