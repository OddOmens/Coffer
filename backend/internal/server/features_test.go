package server

import (
	"encoding/json"
	"io"
	"mime/quotedprintable"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeSMTP accepts mail without authentication and hands each message to
// the returned channel.
func fakeSMTP(t *testing.T) (port int, msgs chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	msgs = make(chan string, 20)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				tp := textproto.NewConn(c)
				tp.PrintfLine("220 fake ESMTP")
				for {
					line, err := tp.ReadLine()
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
					switch cmd {
					case "EHLO", "HELO":
						tp.PrintfLine("250-fake")
						tp.PrintfLine("250 8BITMIME")
					case "DATA":
						tp.PrintfLine("354 go ahead")
						b, _ := tp.ReadDotBytes()
						msgs <- string(b)
						tp.PrintfLine("250 ok")
					case "QUIT":
						tp.PrintfLine("221 bye")
						return
					default:
						tp.PrintfLine("250 ok")
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, msgs
}

func enableMail(t *testing.T, c *client, port int) {
	t.Helper()
	resp, body := c.do("GET", "/api/admin/settings", nil)
	expect(t, resp, body, 200)
	var st map[string]any
	json.Unmarshal(body, &st)
	st["smtpHost"], st["smtpPort"], st["smtpSecurity"], st["smtpFrom"] = "127.0.0.1", port, "none", "files@studio.test"
	resp, body = c.do("PUT", "/api/admin/settings", st)
	expect(t, resp, body, 200)
}

func nextMail(t *testing.T, msgs chan string) string {
	t.Helper()
	select {
	case m := <-msgs:
		b, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(m)))
		return string(b)
	case <-time.After(5 * time.Second):
		t.Fatal("no email arrived")
	}
	return ""
}

func noMail(t *testing.T, msgs chan string) {
	t.Helper()
	select {
	case m := <-msgs:
		t.Fatalf("unexpected email:\n%s", m)
	case <-time.After(400 * time.Millisecond):
	}
}

var (
	inviteRe = regexp.MustCompile(`\?invite=([A-Za-z0-9_.-]+)`)
	codeRe   = regexp.MustCompile(`\b(\d{6})\b`)
)

func TestEmailInvitedShare(t *testing.T) {
	e := newEnv(t)
	port, msgs := fakeSMTP(t)
	c := e.client(e.app.URL)
	c.login("admin")
	enableMail(t, c, port)

	id, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme", "access": "invited",
		"invite": map[string]any{"emails": []string{"Client@Acme.test"}, "message": "Here are the renders"}})
	invite := nextMail(t, msgs)
	if !strings.Contains(invite, "Here are the renders") || !strings.Contains(invite, "To: client@acme.test") {
		t.Fatalf("invitation:\n%s", invite)
	}
	m := inviteRe.FindStringSubmatch(invite)
	if m == nil {
		t.Fatalf("no invite token in:\n%s", invite)
	}

	// Without verifying, nothing is visible.
	v := e.client(e.pub.URL)
	resp, body := v.do("GET", "/api/s/"+token+"/list?path=/", nil)
	expect(t, resp, body, 401)
	if !strings.Contains(string(body), "share_email") {
		t.Fatalf("want share_email: %s", body)
	}
	// The invitation link signs them in.
	resp, body = v.do("POST", "/api/s/"+token+"/verify/invite", map[string]string{"invite": m[1]})
	expect(t, resp, body, 204)
	resp, body = v.do("GET", "/api/s/"+token+"/list?path=/", nil)
	expect(t, resp, body, 200)

	// A stranger asking for a code gets the same answer but no email.
	s := e.client(e.pub.URL)
	resp, body = s.do("POST", "/api/s/"+token+"/verify", map[string]string{"email": "stranger@evil.test"})
	expect(t, resp, body, 204)
	noMail(t, msgs)

	// The invited person on another device verifies with a code.
	d := e.client(e.pub.URL)
	resp, body = d.do("POST", "/api/s/"+token+"/verify", map[string]string{"email": "client@acme.test"})
	expect(t, resp, body, 204)
	codeMail := nextMail(t, msgs)
	cm := codeRe.FindStringSubmatch(codeMail)
	if cm == nil {
		t.Fatalf("no code in:\n%s", codeMail)
	}
	resp, body = d.do("POST", "/api/s/"+token+"/verify/check", map[string]string{"email": "client@acme.test", "code": "000000"})
	if cm[1] != "000000" {
		expect(t, resp, body, 401)
	}
	resp, body = d.do("POST", "/api/s/"+token+"/verify/check", map[string]string{"email": "client@acme.test", "code": cm[1]})
	expect(t, resp, body, 204)
	resp, body = d.do("GET", "/api/s/"+token+"/list?path=/", nil)
	expect(t, resp, body, 200)
	// Downloads are attributed to them.
	resp, body = d.do("GET", "/api/s/"+token+"/download?path=/renders/final.mp4", nil)
	expect(t, resp, body, 200)

	// The owner sees the recipient and can revoke them.
	resp, body = c.do("GET", "/api/shares", nil)
	expect(t, resp, body, 200)
	var shares []struct {
		ID         string
		Recipients []struct {
			ID        string
			Email     string
			Downloads int
		}
	}
	json.Unmarshal(body, &shares)
	if len(shares) != 1 || len(shares[0].Recipients) != 1 || shares[0].Recipients[0].Downloads != 1 {
		t.Fatalf("recipients: %s", body)
	}
	resp, body = c.do("PATCH", "/api/shares/"+id+"/recipients/"+shares[0].Recipients[0].ID, map[string]any{"revoked": true})
	expect(t, resp, body, 200)
	resp, body = d.do("GET", "/api/s/"+token+"/list?path=/", nil)
	expect(t, resp, body, 403)

	// The audit log names them.
	resp, body = c.do("GET", "/api/admin/audit?action=share.download", nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "client@acme.test") {
		t.Fatalf("audit: %s", body)
	}
}

func TestEmailAccessNeedsMail(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/shares", map[string]any{"place": adminPlace, "path": "/projects", "access": "email"})
	expect(t, resp, body, 400)
}

func TestCollectionShare(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/collections", map[string]any{"name": "Delivery", "place": adminPlace,
		"paths": []string{"/projects/acme/renders/final.mp4", "/projects/other"}})
	expect(t, resp, body, 201)
	var col struct {
		ID    string
		Items []struct{ Name string }
	}
	json.Unmarshal(body, &col)
	if len(col.Items) != 2 {
		t.Fatalf("collection: %s", body)
	}
	_, token := createShare(t, c, map[string]any{"collection": col.ID})

	v := e.client(e.pub.URL)
	resp, body = v.do("GET", "/api/s/"+token, nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"name":"Delivery"`) {
		t.Fatalf("meta: %s", body)
	}
	resp, body = v.do("GET", "/api/s/"+token+"/list?path=/", nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"path":"/final.mp4"`) || !strings.Contains(string(body), `"path":"/other"`) {
		t.Fatalf("root: %s", body)
	}
	resp, body = v.do("GET", "/api/s/"+token+"/list?path=/other", nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"path":"/other/private.txt"`) {
		t.Fatalf("inside: %s", body)
	}
	resp, body = v.do("GET", "/api/s/"+token+"/download?path=/other/private.txt", nil)
	expect(t, resp, body, 200)
	if string(body) != "private" {
		t.Fatalf("download: %q", body)
	}
	for _, bad := range []string{"/acme/renders/final.mp4", "/other/../../evil.html", "/final.mp4/x"} {
		resp, _ := v.do("GET", "/api/s/"+token+"/download?path="+bad, nil)
		if resp.StatusCode == 200 {
			t.Fatalf("%s reachable", bad)
		}
	}
	resp, body = v.do("GET", "/api/s/"+token+"/zip", nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "other/private.txt") || !strings.Contains(string(body), "final.mp4") {
		t.Fatal("zip is missing items")
	}
	resp, body = v.do("POST", "/api/s/"+token+"/uploads", map[string]any{"name": "x.txt", "size": 1})
	if resp.StatusCode == 201 {
		t.Fatal("collections must not accept uploads")
	}
}

func TestLatestVersionLink(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders/final_v2.mp4", "latest": true})
	os.WriteFile(filepath.Join(e.storage, "projects/acme/renders/final_v3_approved.mp4"), []byte("v3"), 0o644)
	v := e.client(e.pub.URL)
	resp, body := v.do("GET", "/api/s/"+token, nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"name":"final_v3_approved.mp4"`) || !strings.Contains(string(body), `"version":3`) {
		t.Fatalf("meta: %s", body)
	}
	resp, body = v.do("GET", "/api/s/"+token+"/download", nil)
	expect(t, resp, body, 200)
	if string(body) != "v3" {
		t.Fatalf("got %q", body)
	}
}

func TestReviewThroughLink(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders", "canComment": true})

	v := e.client(e.pub.URL)
	resp, body := v.do("POST", "/api/s/"+token+"/comments", map[string]any{"path": "/final.mp4", "body": "Logo is late"})
	expect(t, resp, body, 400) // needs a name without a verified email
	resp, body = v.do("POST", "/api/s/"+token+"/comments", map[string]any{"path": "/final.mp4", "body": "Logo is late", "timecode": 12.5, "name": "Sarah"})
	expect(t, resp, body, 201)
	resp, body = v.do("POST", "/api/s/"+token+"/comments", map[string]any{"path": "/final.mp4", "kind": "approved", "name": "Sarah"})
	expect(t, resp, body, 201)

	// The team replies, and leaves an internal note.
	resp, body = c.do("POST", "/api/files/comments", map[string]any{"place": adminPlace, "path": "/projects/acme/renders/final.mp4", "body": "Fixing", "parentId": 1})
	expect(t, resp, body, 201)
	resp, body = c.do("POST", "/api/files/comments", map[string]any{"place": adminPlace, "path": "/projects/acme/renders/final.mp4", "body": "Client is picky", "internal": true})
	expect(t, resp, body, 201)

	resp, body = v.do("GET", "/api/s/"+token+"/comments?path=/final.mp4", nil)
	expect(t, resp, body, 200)
	if strings.Contains(string(body), "picky") || !strings.Contains(string(body), "Fixing") {
		t.Fatalf("visitor comments: %s", body)
	}
	resp, body = c.do("GET", "/api/files/list?"+q(adminPlace, "/projects/acme/renders"), nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"status":"approved"`) {
		t.Fatalf("review status missing: %s", body)
	}
	resp, body = c.do("GET", "/api/notifications", nil)
	if !strings.Contains(string(body), "Sarah approved") {
		t.Fatalf("owner not notified: %s", body)
	}
	// Links without comments refuse them.
	_, plain := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/other"})
	resp, body = v.do("POST", "/api/s/"+plain+"/comments", map[string]any{"path": "/private.txt", "body": "hi", "name": "x"})
	expect(t, resp, body, 403)
}

func TestLocks(t *testing.T) {
	e := newEnv(t)
	_, pa := e.addUser("alice", "user", "projects")
	_, pb := e.addUser("bob", "user", "projects")
	a, b, admin := e.client(e.app.URL), e.client(e.app.URL), e.client(e.app.URL)
	a.login("alice")
	b.login("bob")
	admin.login("admin")

	resp, body := a.do("POST", "/api/files/lock", map[string]any{"place": pa, "path": "/other/private.txt", "note": "editing"})
	expect(t, resp, body, 201)
	resp, body = b.do("POST", "/api/files/rename", map[string]any{"place": pb, "path": "/other/private.txt", "name": "x.txt"})
	expect(t, resp, body, 423)
	resp, body = b.do("POST", "/api/files/rename", map[string]any{"place": pb, "path": "/other", "name": "y"})
	expect(t, resp, body, 423) // a folder with a locked file inside
	resp, body = b.do("POST", "/api/files/delete", map[string]any{"place": pb, "paths": []string{"/other/private.txt"}})
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "locked by alice") {
		t.Fatalf("delete: %s", body)
	}
	resp, body = b.do("PUT", "/api/files/text", map[string]any{"place": pb, "path": "/other/private.txt", "content": "x"})
	expect(t, resp, body, 423)
	resp, body = b.do("DELETE", "/api/files/lock?"+q(pb, "/other/private.txt"), nil)
	expect(t, resp, body, 403)
	resp, body = b.do("GET", "/api/files/list?"+q(pb, "/other"), nil)
	if !strings.Contains(string(body), `"username":"alice"`) {
		t.Fatalf("lock not shown: %s", body)
	}
	// Alice can still work on it; an admin can break the lock.
	resp, body = a.do("PUT", "/api/files/text", map[string]any{"place": pa, "path": "/other/private.txt", "content": "mine"})
	if resp.StatusCode == 423 {
		t.Fatalf("owner blocked by own lock: %s", body)
	}
	resp, body = admin.do("DELETE", "/api/files/lock?"+q(adminPlace, "/projects/other/private.txt"), nil)
	expect(t, resp, body, 204)
	resp, body = b.do("POST", "/api/files/rename", map[string]any{"place": pb, "path": "/other/private.txt", "name": "x.txt"})
	expect(t, resp, body, 200)
}

func TestQuota(t *testing.T) {
	e := newEnv(t)
	u, p := e.addUser("fam", "user", "projects")
	u.QuotaBytes = 1 << 20
	e.store.UpdateUser(t.Context(), u)
	c := e.client(e.app.URL)
	c.login("fam")
	resp, body := c.do("POST", "/api/uploads", map[string]any{"place": p, "dir": "/", "name": "big.bin", "size": 2 << 20})
	expect(t, resp, body, 507)
	resp, body = c.do("POST", "/api/uploads", map[string]any{"place": p, "dir": "/", "name": "small.bin", "size": 1000})
	expect(t, resp, body, 201)
	resp, body = c.do("GET", "/api/me", nil)
	if !strings.Contains(string(body), `"quotaBytes":1048576`) {
		t.Fatalf("me: %s", body)
	}
}

func TestTagsFavoritesActivity(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/files/tags", map[string]any{"place": adminPlace, "paths": []string{"/projects/acme"}, "add": []string{"#Acme", "Q4 launch"}})
	expect(t, resp, body, 204)
	resp, body = c.do("POST", "/api/files/tags", map[string]any{"place": adminPlace, "paths": []string{"/projects/acme"}, "add": []string{"<script>"}})
	expect(t, resp, body, 400)
	resp, body = c.do("GET", "/api/files/list?"+q(adminPlace, "/projects"), nil)
	if !strings.Contains(string(body), `"tags":["Acme","Q4 launch"]`) {
		t.Fatalf("tags: %s", body)
	}
	resp, body = c.do("GET", "/api/tags/acme", nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"path":"/projects/acme"`) {
		t.Fatalf("tagged: %s", body)
	}
	resp, body = c.do("PUT", "/api/favorites", map[string]any{"place": adminPlace, "path": "/projects/acme", "on": true})
	expect(t, resp, body, 204)
	resp, body = c.do("POST", "/api/files/rename", map[string]any{"place": adminPlace, "path": "/projects/acme", "name": "acme-co"})
	expect(t, resp, body, 200)
	// Tags and favourites follow the rename.
	resp, body = c.do("GET", "/api/favorites", nil)
	if !strings.Contains(string(body), `"path":"/projects/acme-co"`) {
		t.Fatalf("favorites: %s", body)
	}
	resp, body = c.do("GET", "/api/tags/acme", nil)
	if !strings.Contains(string(body), "acme-co") {
		t.Fatalf("tag after rename: %s", body)
	}
	resp, body = c.do("GET", "/api/files/activity?"+q(adminPlace, "/projects"), nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"action":"rename"`) || !strings.Contains(string(body), `"detail":"to /projects/acme-co"`) {
		t.Fatalf("activity: %s", body)
	}
}

func TestBackupRestoreAPI(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/admin/backups", map[string]any{"name": "Nightly", "sourceDrive": "files", "sourcePath": "projects/acme", "targetDrive": "files", "targetPath": "projects/acme/.bk"})
	expect(t, resp, body, 400) // target inside source
	resp, body = c.do("POST", "/api/admin/backups", map[string]any{"name": "Nightly", "sourceDrive": "files", "sourcePath": "projects/acme", "targetDrive": "files"})
	expect(t, resp, body, 201)
	var b struct{ ID, TargetPath string }
	json.Unmarshal(body, &b)
	if b.TargetPath != ".backups/Nightly" {
		t.Fatalf("target: %s", body)
	}
	resp, body = c.do("POST", "/api/admin/backups/"+b.ID+"/run", nil)
	expect(t, resp, body, 202)
	var snaps []struct{ Name string }
	for i := 0; i < 50 && len(snaps) == 0; i++ {
		time.Sleep(100 * time.Millisecond)
		_, body = c.do("GET", "/api/admin/backups/"+b.ID+"/snapshots", nil)
		json.Unmarshal(body, &snaps)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots: %s", body)
	}
	resp, body = c.do("GET", "/api/admin/backups/"+b.ID+"/snapshots/"+snaps[0].Name+"?path=renders", nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "final_v2.mp4") {
		t.Fatalf("browse: %s", body)
	}
	// Files play and download straight from the snapshot, ranges included.
	snapURL := "/api/admin/backups/" + b.ID + "/snapshots/" + snaps[0].Name
	resp, body = c.do("GET", snapURL+"/file?path=renders/final.mp4&inline=1", nil, "Range", "bytes=0-4")
	expect(t, resp, body, 206)
	if string(body) != "video" {
		t.Fatalf("range: %q", body)
	}
	resp, body = c.do("GET", snapURL+"/file?path=renders/final.mp4", nil)
	expect(t, resp, body, 200)
	if string(body) != "video-bytes" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %q %s", body, resp.Header.Get("Content-Disposition"))
	}
	resp, body = c.do("GET", snapURL+"/file?path=renders", nil)
	expect(t, resp, body, 404)
	resp, body = c.do("GET", snapURL+"/zip?path=renders&name=final.mp4", nil)
	expect(t, resp, body, 200)
	if resp.Header.Get("Content-Type") != "application/zip" || !strings.Contains(string(body), "final.mp4") {
		t.Fatalf("zip: %s %q", resp.Header.Get("Content-Type"), body)
	}
	resp, body = c.do("GET", snapURL+"/zip?name=../x", nil)
	expect(t, resp, body, 400)

	resp, body = c.do("POST", "/api/admin/backups/"+b.ID+"/restore", map[string]any{"snapshot": snaps[0].Name, "paths": []string{"renders/final.mp4"}})
	expect(t, resp, body, 202)
	var res struct{ Into string }
	json.Unmarshal(body, &res)
	restored := filepath.Join(e.storage, res.Into, "renders/final.mp4")
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(restored); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if b, err := os.ReadFile(restored); err != nil || string(b) != "video-bytes" {
		t.Fatalf("restored %s: %q %v", restored, b, err)
	}
	// Backups are not visible to search or browsing (dot folder).
	resp, body = c.do("GET", "/api/files/list?"+q(adminPlace, "/"), nil)
	if strings.Contains(string(body), ".backups") {
		t.Fatalf("backup folder listed: %s", body)
	}
}

func TestWatermarkedPreview(t *testing.T) {
	e := newEnv(t)
	// A tiny PNG.
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 0x49, 0x48, 0x44, 0x52, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0, 0x1f, 0, 0x05, 0, 0x01, 0xff, 0x89, 0x99, 0x3d, 0x1d, 0, 0, 0, 0, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82}
	os.WriteFile(filepath.Join(e.storage, "projects/other/still.png"), png, 0o644)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/other", "watermark": true, "canDownload": false})
	v := e.client(e.pub.URL)
	resp, body := v.do("GET", "/api/s/"+token+"/download?inline=1&path=/still.png", nil)
	expect(t, resp, body, 200)
	if resp.Header.Get("Content-Type") != "image/jpeg" || len(body) < 100 {
		t.Fatalf("not watermarked: %s %d", resp.Header.Get("Content-Type"), len(body))
	}
	resp, body = v.do("GET", "/api/s/"+token+"/download?path=/still.png", nil)
	expect(t, resp, body, 403)
}

func TestSMBOpenFilesShown(t *testing.T) {
	e := newEnv(t)
	os.MkdirAll(filepath.Join(e.srv.cfg.DataDir, "smb"), 0o755)
	status := `{"timestamp": "2026-10-25T11:51:18.933123+0000", "version": "4.21.9",
	 "sessions": {"3741": {"session_id": "3741", "uid": 1001, "gid": 1000, "username": "editor", "groupname": "coffer"}},
	 "tcons": {},
	 "open_files": {"/share/projects/other/private.txt": {"service_path": "/share", "filename": "projects/other/private.txt",
	   "num_pending_deletes": 0, "opens": {"56/1": {"uid": 1001, "share_file_id": 1, "opened_at": "2026-10-25T11:40:00.000000+0000"}}}}}`
	os.WriteFile(filepath.Join(e.srv.cfg.DataDir, "smb", "status.json"), []byte(status), 0o644)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("GET", "/api/files/list?"+q(adminPlace, "/projects/other"), nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"source":"smb"`) || !strings.Contains(string(body), `"username":"editor"`) {
		t.Fatalf("smb open not shown: %s", body)
	}
}
