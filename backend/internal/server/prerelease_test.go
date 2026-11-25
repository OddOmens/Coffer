package server

import (
	"testing"

	"github.com/oddomens/coffer/internal/store"
)

func TestResolveRefusesSelfCopyAnySpelling(t *testing.T) {
	a := &access{Kind: "home", Root: "home/bob"}
	for _, p := range []string{"x/_coffer/s/secret.key", `x\_coffer\s\secret.key`, "x/_COFFER/s", `_coffer`, "a/../_coffer/k"} {
		if got, err := a.resolve(p); err == nil {
			t.Errorf("resolve(%q) = %q, want refused", p, got)
		}
	}
	if _, err := a.resolve("x/coffer_notes/a.txt"); err != nil {
		t.Errorf("ordinary path refused: %v", err)
	}
	// Admins browsing the drive itself may open it.
	d := &access{Kind: "drive", Root: "."}
	if _, err := d.resolve(`x\_coffer\s`); err != nil {
		t.Errorf("drive access refused: %v", err)
	}
}

func TestLinkSeesOnlyItsOwnThreads(t *testing.T) {
	list := []*store.Comment{
		{ID: 1, ShareID: "A"},              // client A
		{ID: 2, ShareID: "B"},              // client B
		{ID: 3, UserID: 7},                 // team, outside any link
		{ID: 4, UserID: 7, Internal: true}, // team note
		{ID: 5, UserID: 7, ParentID: 1},    // team reply to A
		{ID: 6, UserID: 7, ParentID: 4},    // reply under the note
		{ID: 7, UserID: 7, ParentID: 2},    // team reply to B
	}
	want := map[int64]bool{1: true, 3: true, 5: true}
	for _, c := range list {
		if got := linkSees(c, list, "A"); got != want[c.ID] {
			t.Errorf("comment %d: sees %v, want %v", c.ID, got, want[c.ID])
		}
	}
}

func TestViewOnlyImagesAreWatermarkable(t *testing.T) {
	for p, want := range map[string]bool{"a.jpg": true, "a.png": true, "a.svg": false, "a.exr": false, "a.mp4": true, "a.pdf": true} {
		if got := viewOnlyOK(p); got != want {
			t.Errorf("viewOnlyOK(%q) = %v, want %v", p, got, want)
		}
	}
}
