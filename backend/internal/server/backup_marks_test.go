package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupMarks(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	type mark struct{ State, Backup, Drive string }
	list := func(dir string) (map[string]*mark, *mark) {
		t.Helper()
		resp, body := c.do("GET", "/api/files/list?"+q(adminPlace, dir), nil)
		expect(t, resp, body, 200)
		var out struct {
			Entries []struct {
				Name   string
				Backup *mark
			}
			Backup *mark
		}
		json.Unmarshal(body, &out)
		m := map[string]*mark{}
		for _, en := range out.Entries {
			m[en.Name] = en.Backup
		}
		return m, out.Backup
	}

	resp, body := c.do("POST", "/api/admin/backups", map[string]any{"name": "Acme", "sourceDrive": "files", "sourcePath": "projects/acme", "targetDrive": "files"})
	expect(t, resp, body, 201)
	var b struct{ ID string }
	json.Unmarshal(body, &b)

	// Before the first run: covered, not yet in a snapshot.
	top, _ := list("/projects")
	if top["acme"] == nil || top["acme"].State != "pending" || top["other"] != nil {
		t.Fatalf("before the run: %+v %+v", top["acme"], top["other"])
	}
	root, _ := list("/")
	if root["projects"] == nil || root["projects"].State != "partial" {
		t.Fatalf("parent folder: %+v", root["projects"])
	}

	resp, body = c.do("POST", "/api/admin/backups/"+b.ID+"/run", nil)
	expect(t, resp, body, 202)
	var files map[string]*mark
	for i := 0; i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		if files, _ = list("/projects/acme/renders"); files["final.mp4"] != nil && files["final.mp4"].State == "done" {
			break
		}
	}
	if m := files["final.mp4"]; m == nil || m.State != "done" || m.Backup != "Acme" || m.Drive != "Files" {
		t.Fatalf("after the run: %+v", m)
	}
	if _, here := list("/projects/acme"); here == nil || here.State != "done" {
		t.Fatalf("folder header: %+v", here)
	}

	// Copied in afterwards with an old date, or edited: not in the backup yet.
	dir := filepath.Join(e.storage, "projects/acme/renders")
	old := time.Unix(1_500_000_000, 0)
	os.WriteFile(filepath.Join(dir, "old-photo.jpg"), []byte("jpeg"), 0o644)
	os.Chtimes(filepath.Join(dir, "old-photo.jpg"), old, old)
	os.WriteFile(filepath.Join(dir, "final_v2.mp4"), []byte("edited, longer bytes"), 0o644)
	os.MkdirAll(filepath.Join(dir, "new-folder"), 0o755)
	files, _ = list("/projects/acme/renders")
	for _, name := range []string{"old-photo.jpg", "final_v2.mp4", "new-folder"} {
		if m := files[name]; m == nil || m.State != "pending" {
			t.Fatalf("%s: %+v", name, m)
		}
	}
	if files["final.mp4"].State != "done" {
		t.Fatal("unchanged file lost its mark")
	}

	// A paused backup makes no promises.
	resp, body = c.do("PATCH", "/api/admin/backups/"+b.ID, map[string]any{"enabled": false})
	expect(t, resp, body, 200)
	if files, _ = list("/projects/acme/renders"); files["final.mp4"] != nil {
		t.Fatalf("paused backup still marks: %+v", files["final.mp4"])
	}
}

// Files and folders picked in the file browser: added to a new backup, then
// to that one, kept by their full path, followed through renames, and taken
// out again.
func TestBackupPickedItems(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	os.WriteFile(filepath.Join(e.storage, "projects/brief.txt"), []byte("brief"), 0o644)
	type result struct {
		Backup struct {
			ID         string
			Items      []string
			TargetPath string
			LastRun    *struct{ State, Error string }
		}
		Added, Already, Removed int
		Started                 bool
	}
	pick := func(status int, req map[string]any) result {
		t.Helper()
		req["place"] = adminPlace
		resp, body := c.do("POST", "/api/admin/backups/items", req)
		expect(t, resp, body, status)
		var r result
		json.Unmarshal(body, &r)
		return r
	}
	wait := func(id string) result {
		t.Helper()
		for i := 0; i < 100; i++ {
			time.Sleep(50 * time.Millisecond)
			resp, body := c.do("GET", "/api/admin/backups", nil)
			expect(t, resp, body, 200)
			var list []struct {
				ID      string
				Running any
				LastRun *struct{ State, Error string }
			}
			json.Unmarshal(body, &list)
			for _, b := range list {
				if b.ID == id && b.Running == nil && b.LastRun != nil && b.LastRun.State != "running" {
					var r result
					r.Backup.LastRun = b.LastRun
					return r
				}
			}
		}
		t.Fatal("backup didn't finish")
		return result{}
	}

	r := pick(200, map[string]any{"paths": []string{"/projects/acme/renders", "/projects/acme"}, "name": "Acme", "targetDrive": "files", "run": true})
	if r.Added != 2 || !r.Started || len(r.Backup.Items) != 1 || r.Backup.Items[0] != "projects/acme" || r.Backup.TargetPath != ".backups/Acme" {
		t.Fatalf("new backup: %+v", r)
	}
	id := r.Backup.ID
	if run := wait(id).Backup.LastRun; run.State != "done" || run.Error != "" {
		t.Fatalf("run: %+v", run)
	}

	// Already in it, outside it, and a second pick added.
	if r = pick(200, map[string]any{"paths": []string{"/projects/acme/renders/final.mp4"}, "backup": id}); r.Already != 1 || r.Added != 0 {
		t.Fatalf("already: %+v", r)
	}
	if r = pick(200, map[string]any{"paths": []string{"/projects/brief.txt"}, "backup": id}); r.Added != 1 || len(r.Backup.Items) != 2 {
		t.Fatalf("add: %+v", r)
	}
	pick(400, map[string]any{"paths": []string{"/nope"}, "backup": id})
	pick(400, map[string]any{"paths": []string{"/.backups"}, "name": "Loop", "targetDrive": "files"})
	pick(404, map[string]any{"paths": []string{"/projects/other"}, "backup": "missing"})

	// A second new backup with the same name gets its own folder.
	if r2 := pick(200, map[string]any{"paths": []string{"/projects/other"}, "name": "Acme", "targetDrive": "files"}); r2.Backup.TargetPath != ".backups/Acme 2" {
		t.Fatalf("second target: %+v", r2)
	} else {
		resp, body := c.do("DELETE", "/api/admin/backups/"+r2.Backup.ID, nil)
		expect(t, resp, body, 204)
	}
	resp, body := c.do("POST", "/api/admin/backups", map[string]any{"name": "Clash", "sourceDrive": "files", "sourcePath": "projects/other", "targetDrive": "files", "targetPath": ".backups/Acme"})
	expect(t, resp, body, 400)

	// Marks: picked things and their parents only.
	list := func(dir string) map[string]string {
		t.Helper()
		resp, body := c.do("GET", "/api/files/list?"+q(adminPlace, dir), nil)
		expect(t, resp, body, 200)
		var out struct {
			Entries []struct {
				Name   string
				Backup *struct{ State, Backup string }
			}
		}
		json.Unmarshal(body, &out)
		m := map[string]string{}
		for _, en := range out.Entries {
			if en.Backup != nil && en.Backup.Backup == "Acme" {
				m[en.Name] = en.Backup.State
			}
		}
		return m
	}
	if m := list("/projects"); m["acme"] != "done" || m["brief.txt"] != "pending" || m["other"] != "" {
		t.Fatalf("marks: %v", m)
	}
	if m := list("/"); m["projects"] != "partial" {
		t.Fatalf("parent: %v", m)
	}

	// Snapshots keep the full path.
	pick(200, map[string]any{"paths": []string{"/projects/brief.txt"}, "backup": id, "run": true})
	wait(id)
	resp, body = c.do("GET", "/api/admin/backups/"+id+"/snapshots", nil)
	var snaps []struct{ Name string }
	json.Unmarshal(body, &snaps)
	resp, body = c.do("GET", "/api/admin/backups/"+id+"/snapshots/"+snaps[0].Name+"?path=projects", nil)
	expect(t, resp, body, 200)
	for _, want := range []string{`"acme"`, `"brief.txt"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("snapshot lacks %s: %s", want, body)
		}
	}
	if strings.Contains(string(body), `"other"`) {
		t.Fatalf("snapshot has an unpicked folder: %s", body)
	}

	// Renamed in Coffer: the backup follows.
	resp, body = c.do("POST", "/api/files/rename", map[string]any{"place": adminPlace, "path": "/projects/acme", "name": "acme-studio"})
	expect(t, resp, body, 200)
	resp, body = c.do("GET", "/api/admin/backups", nil)
	if !strings.Contains(string(body), `"projects/acme-studio"`) {
		t.Fatalf("after rename: %s", body)
	}

	// Taken out: only things picked on their own, never the last one.
	pick(400, map[string]any{"paths": []string{"/projects/acme-studio/renders"}, "backup": id, "remove": true})
	if r = pick(200, map[string]any{"paths": []string{"/projects/brief.txt"}, "backup": id, "remove": true}); r.Removed != 1 || len(r.Backup.Items) != 1 {
		t.Fatalf("remove: %+v", r)
	}
	pick(400, map[string]any{"paths": []string{"/projects/acme-studio"}, "backup": id, "remove": true})
	resp, body = c.do("PATCH", "/api/admin/backups/"+id, map[string]any{"items": []string{}})
	expect(t, resp, body, 400)

	// Gone without Coffer seeing: the run says so.
	os.RemoveAll(filepath.Join(e.storage, "projects/acme-studio"))
	resp, body = c.do("POST", "/api/admin/backups/"+id+"/run", nil)
	expect(t, resp, body, 202)
	if run := wait(id).Backup.LastRun; run.State != "done" || !strings.Contains(run.Error, "/projects/acme-studio") {
		t.Fatalf("missing item: %+v", run)
	}

	// Admins only.
	e.addUser("bob", "user", "projects")
	cb := e.client(e.app.URL)
	cb.login("bob")
	resp, body = cb.do("POST", "/api/admin/backups/items", map[string]any{"place": adminPlace, "paths": []string{"/projects"}, "name": "Mine", "targetDrive": "files"})
	expect(t, resp, body, 403)
}

func TestShowHiddenIsPerUser(t *testing.T) {
	e := newEnv(t)
	os.MkdirAll(filepath.Join(e.storage, ".backups/Business"), 0o755)
	c := e.client(e.app.URL)
	c.login("admin")
	names := func() string {
		t.Helper()
		resp, body := c.do("GET", "/api/files/list?"+q(adminPlace, "/"), nil)
		expect(t, resp, body, 200)
		return string(body)
	}
	if strings.Contains(names(), `".backups"`) {
		t.Fatal("dot-folder listed with the default settings")
	}
	resp, body := c.do("PUT", "/api/me/prefs", map[string]any{"showHidden": true})
	expect(t, resp, body, 200)
	if !strings.Contains(names(), `".backups"`) {
		t.Fatal("showHidden didn't reveal the dot-folder")
	}
}

func TestBackupStoreFolderPointsToSnapshots(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/admin/backups", map[string]any{"name": "Acme", "sourceDrive": "files", "sourcePath": "projects/acme", "targetDrive": "files"})
	expect(t, resp, body, 201)
	var b struct{ ID, TargetPath string }
	json.Unmarshal(body, &b)
	os.MkdirAll(filepath.Join(e.storage, b.TargetPath, "objects"), 0o755)
	store := func(dir string) string {
		t.Helper()
		resp, body := c.do("GET", "/api/files/list?"+q(adminPlace, dir), nil)
		expect(t, resp, body, 200)
		var out struct{ BackupStore *struct{ ID string } }
		json.Unmarshal(body, &out)
		if out.BackupStore == nil {
			return ""
		}
		return out.BackupStore.ID
	}
	if got := store(b.TargetPath); got != b.ID {
		t.Fatalf("backup folder: %q, want %q", got, b.ID)
	}
	if got := store(b.TargetPath + "/objects"); got != b.ID {
		t.Fatalf("inside the backup folder: %q", got)
	}
	if got := store("projects"); got != "" {
		t.Fatalf("ordinary folder marked as a backup store: %q", got)
	}
}
