package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/files"
)

// A disk that goes away (its marker gone) takes the drive offline with a
// clear 503, and it comes back on its own when the marker returns.
func TestDriveGoesOfflineAndBack(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	marker := filepath.Join(e.storage, files.InternalDir, "drive-id")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("existing drive with files wasn't marked: %v", err)
	}
	saved, _ := os.ReadFile(marker)

	os.Remove(marker)
	if _, err := e.srv.checkDrive(t.Context(), "files"); err == nil {
		t.Fatal("drive without its marker still online")
	}
	resp, body := c.do("GET", "/api/files/list?"+q(adminPlace, "/"), nil)
	expect(t, resp, body, 503)
	if m, _ := e.srv.drive("files"); m == nil || m.d != nil {
		t.Fatal("drive not offline")
	}

	os.WriteFile(marker, saved, 0o644)
	if _, err := e.srv.checkDrive(t.Context(), "files"); err != nil {
		t.Fatalf("drive not back: %v", err)
	}
	resp, body = c.do("GET", "/api/files/list?"+q(adminPlace, "/"), nil)
	expect(t, resp, body, 200)
}

// Replacing a file by upload lists the old one in the trash with its size.
func TestUploadReplaceTrashesOld(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	data := []byte("a new render")
	resp, body := c.do("POST", "/api/uploads", map[string]any{"place": adminPlace, "dir": "/projects/acme/renders", "name": "final.mp4", "size": len(data), "overwrite": true})
	expect(t, resp, body, 201)
	var up struct{ ID string }
	json.Unmarshal(body, &up)
	resp, body = c.do("PATCH", "/api/uploads/"+up.ID, data, "Upload-Offset", "0")
	expect(t, resp, body, 200)
	resp, body = c.do("POST", "/api/uploads/"+up.ID+"/complete", nil)
	expect(t, resp, body, 200)
	if b, _ := os.ReadFile(filepath.Join(e.storage, "projects/acme/renders/final.mp4")); string(b) != string(data) {
		t.Fatalf("not replaced: %q", b)
	}
	resp, body = c.do("GET", "/api/trash", nil)
	expect(t, resp, body, 200)
	var items []struct {
		Name string
		Size int64
		Mine bool
	}
	json.Unmarshal(body, &items)
	if len(items) != 1 || items[0].Name != "final.mp4" || items[0].Size != int64(len("video-bytes")) || !items[0].Mine {
		t.Fatalf("trash: %s", body)
	}

	// Skipping files that are already there.
	resp, body = c.do("POST", "/api/uploads", map[string]any{"place": adminPlace, "dir": "/projects/acme/renders", "name": "final.mp4", "size": 3, "skipExisting": true})
	expect(t, resp, body, 200)
	if string(body) == "" || !json.Valid(body) {
		t.Fatalf("skip: %s", body)
	}

	// Emptying says what it did.
	resp, body = c.do("DELETE", "/api/trash", nil)
	expect(t, resp, body, 200)
	var out struct{ Deleted, Offline, Failed int }
	json.Unmarshal(body, &out)
	if out.Deleted != 1 || out.Offline+out.Failed != 0 {
		t.Fatalf("empty: %s", body)
	}
}

// A backup whose source suddenly lost most of its files is held back, and
// an admin can run it anyway.
func TestBackupHeldBackRunAnyway(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	dir := filepath.Join(e.storage, "work")
	os.MkdirAll(dir, 0o755)
	for i := range 30 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d.txt", i)), []byte("x"), 0o644)
	}
	resp, body := c.do("POST", "/api/admin/backups", map[string]any{"name": "Work", "sourceDrive": "files", "sourcePath": "work", "targetDrive": "files"})
	expect(t, resp, body, 201)
	var b struct{ ID string }
	json.Unmarshal(body, &b)
	type job struct {
		CanForce bool `json:"canForce"`
		LastRun  *struct {
			State, Error string
		} `json:"lastRun"`
	}
	run := func(query string) job {
		t.Helper()
		resp, body := c.do("POST", "/api/admin/backups/"+b.ID+"/run"+query, nil)
		expect(t, resp, body, 202)
		for range 200 {
			time.Sleep(25 * time.Millisecond)
			resp, body := c.do("GET", "/api/admin/backups", nil)
			expect(t, resp, body, 200)
			var list []job
			json.Unmarshal(body, &list)
			if len(list) == 1 && list[0].LastRun != nil && list[0].LastRun.State != "running" && !strings.Contains(string(body), `"running":{`) {
				return list[0]
			}
		}
		t.Fatal("backup didn't finish")
		return job{}
	}
	if j := run(""); j.LastRun.State != "done" {
		t.Fatalf("first run: %+v", j.LastRun)
	}
	for i := range 25 {
		os.Remove(filepath.Join(dir, fmt.Sprintf("%02d.txt", i)))
	}
	time.Sleep(1100 * time.Millisecond)
	j := run("")
	if j.LastRun.State != "failed" || !strings.HasPrefix(j.LastRun.Error, heldBackPrefix) || !j.CanForce {
		t.Fatalf("not held back: %+v %v", j.LastRun, j.CanForce)
	}
	time.Sleep(1100 * time.Millisecond)
	if j := run("?force=1"); j.LastRun.State != "done" || j.CanForce {
		t.Fatalf("run anyway: %+v", j.LastRun)
	}
}
