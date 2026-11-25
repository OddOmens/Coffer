package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/store"
)

func ownerNotifications(t *testing.T, c *client) []string {
	t.Helper()
	resp, body := c.do("GET", "/api/notifications", nil)
	expect(t, resp, body, 200)
	var out struct{ Items []struct{ Title string } }
	json.Unmarshal(body, &out)
	var titles []string
	for _, n := range out.Items {
		titles = append(titles, n.Title)
	}
	return titles
}

func TestShareDownloadNotificationsCollapse(t *testing.T) {
	old := shareEventWindow
	shareEventWindow = 300 * time.Millisecond
	t.Cleanup(func() { shareEventWindow = old })
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme", "notify": 2})
	pub := e.client(e.pub.URL)
	for range 4 {
		resp, body := pub.do("GET", "/api/s/"+token+"/download?path=/renders/final.mp4", nil)
		expect(t, resp, body, 200)
	}
	if n := ownerNotifications(t, c); len(n) != 1 {
		t.Fatalf("want one notification right away, got %q", n)
	}
	time.Sleep(600 * time.Millisecond)
	n := ownerNotifications(t, c)
	if len(n) != 2 || !strings.Contains(n[0]+n[1], "3 more downloads") {
		t.Fatalf("want a summary of the other three, got %q", n)
	}
}

func TestShareCodeLimits(t *testing.T) {
	id := "share-codes-test"
	for i := range codesPerAddrHour {
		if !codeAllowed(id, "a@b.test") {
			t.Fatalf("code %d refused", i+1)
		}
		codeSent(id)
	}
	if codeAllowed(id, "a@b.test") {
		t.Fatal("fourth code for one address allowed")
	}
	for i := codesPerAddrHour; i < shareCodesPerHour; i++ {
		codeSent(id)
	}
	if codeAllowed(id, "other@b.test") {
		t.Fatal("link over its hourly code cap")
	}
}

func TestShareHeadAndPreviewsDontCount(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme", "maxDownloads": 1})
	pub := e.client(e.pub.URL)
	dl := "/api/s/" + token + "/download?path=/renders/final.mp4"
	resp, body := pub.do("HEAD", dl, nil)
	expect(t, resp, body, 200)
	for range 3 {
		resp, body = pub.do("GET", dl+"&inline=1", nil)
		expect(t, resp, body, 200)
		resp, body = pub.do("GET", dl+"&inline=1", nil, "Range", "bytes=0-3")
		expect(t, resp, body, 206)
	}
	resp, body = pub.do("GET", dl, nil)
	expect(t, resp, body, 200)
	resp, body = pub.do("GET", dl, nil)
	expect(t, resp, body, 410)
}

func TestShareHidesDotfiles(t *testing.T) {
	e := newEnv(t)
	acme := filepath.Join(e.storage, "projects/acme")
	os.WriteFile(filepath.Join(acme, ".env"), []byte("SECRET=1"), 0o644)
	os.MkdirAll(filepath.Join(acme, ".git"), 0o755)
	os.WriteFile(filepath.Join(acme, ".git/config"), []byte("x"), 0o644)
	c := e.client(e.app.URL)
	c.login("admin")
	st := e.srv.cur()
	st.HideDotfiles = false
	e.srv.settings.Store(&st)
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme"})
	pub := e.client(e.pub.URL)
	for _, p := range []string{"/download?path=/.env", "/download?path=/.git/config", "/list?path=/.git", "/zip?name=.env", "/zip?path=/.git"} {
		resp, body := pub.do("GET", "/api/s/"+token+p, nil)
		expect(t, resp, body, 404)
	}
	resp, body := pub.do("GET", "/api/s/"+token+"/zip", nil)
	expect(t, resp, body, 200)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if strings.Contains(f.Name, ".env") || strings.Contains(f.Name, ".git") {
			t.Fatalf("hidden file in the link's zip: %s", f.Name)
		}
	}
}

func TestShareUploadSessionsCapped(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme", "canUpload": true})
	pub := e.client(e.pub.URL)
	start := func() *http.Response {
		resp, _ := pub.do("POST", "/api/s/"+token+"/uploads", map[string]any{"dir": "/", "name": "a.bin", "size": 10})
		return resp
	}
	var first string
	for i := range maxShareUploads {
		resp, body := pub.do("POST", "/api/s/"+token+"/uploads", map[string]any{"dir": "/", "name": "a.bin", "size": 10})
		expect(t, resp, body, 201)
		if i == 0 {
			var up struct{ ID string }
			json.Unmarshal(body, &up)
			first = up.ID
		}
	}
	if r := start(); r.StatusCode != 429 {
		t.Fatalf("upload past the cap: %d", r.StatusCode)
	}
	resp, body := pub.do("DELETE", "/api/s/"+token+"/uploads/"+first, nil)
	expect(t, resp, body, 204)
	if r := start(); r.StatusCode != 201 {
		t.Fatalf("upload after one was aborted: %d", r.StatusCode)
	}
}

func TestBrokenZipIsCutOff(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	e := newEnv(t)
	bad := filepath.Join(e.storage, "projects/acme/renders/locked.mov")
	os.WriteFile(bad, []byte("x"), 0o000)
	c := e.client(e.app.URL)
	c.login("admin")
	req, _ := http.NewRequest("GET", e.app.URL+"/api/files/zip?"+q(adminPlace, "/projects/acme/renders"), nil)
	resp, err := c.c.Do(req)
	if err != nil {
		return // cut off before the headers: fine too
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("a zip that failed half-way ended like a complete one")
	}
}

func TestAppListenerIgnoresShareProxies(t *testing.T) {
	e := newEnv(t)
	// A tunnel's subnet is trusted on the share listener only.
	e.srv.cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	e.srv.cfg.AppNets = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	c := e.client(e.app.URL)
	resp, body := c.do("GET", "/api/config", nil, "X-Forwarded-For", "10.0.0.5")
	expect(t, resp, body, 200)
	var cfg struct{ PublicOnly bool }
	json.Unmarshal(body, &cfg)
	if !cfg.PublicOnly {
		t.Fatal("forwarding header honoured on the app listener")
	}
}

func TestOverwriteNeedsDeleteAndKeepsOldVersion(t *testing.T) {
	e := newEnv(t)
	_, home := e.addUser("bob", "user", "projects/other")
	dav := davClient(t, e, "bob")
	c := e.client(e.app.URL)
	c.login("bob")
	resp, body := c.do("GET", "/api/files/text?"+q(home, "/private.txt"), nil)
	expect(t, resp, body, 200)
	var doc struct{ Etag string }
	json.Unmarshal(body, &doc)

	setPlace(t, e, home, func(p *store.Place) { p.CanDelete = false })
	resp, body = dav("PUT", "/dav/Home/private.txt", []byte("overwritten"))
	if resp.StatusCode < 400 {
		t.Fatalf("WebDAV replaced a file without delete rights: %d", resp.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(e.storage, "projects/other/private.txt")); string(b) != "private" {
		t.Fatalf("content: %q", b)
	}
	resp, body = c.do("PUT", "/api/files/text", map[string]any{"place": home, "path": "/private.txt", "content": "edited", "etag": doc.Etag})
	expect(t, resp, body, 403)
	// New files, and Finder's empty placeholder, need only write access.
	resp, body = dav("PUT", "/dav/Home/new.txt", nil)
	expect(t, resp, body, 201)
	resp, body = dav("PUT", "/dav/Home/new.txt", []byte("content"))
	expect(t, resp, body, 201)

	setPlace(t, e, home, func(p *store.Place) { p.CanDelete = true })
	resp, body = dav("PUT", "/dav/Home/private.txt", []byte("overwritten"))
	expect(t, resp, body, 201)
	resp, body = c.do("GET", "/api/files/text?"+q(home, "/private.txt"), nil)
	json.Unmarshal(body, &doc)
	resp, body = c.do("PUT", "/api/files/text", map[string]any{"place": home, "path": "/private.txt", "content": "edited", "etag": doc.Etag})
	expect(t, resp, body, 200)
	if b, _ := os.ReadFile(filepath.Join(e.storage, "projects/other/private.txt")); string(b) != "edited" {
		t.Fatalf("content: %q", b)
	}
	resp, body = c.do("GET", "/api/trash", nil)
	expect(t, resp, body, 200)
	if n := strings.Count(string(body), `"name":"private.txt"`); n != 2 {
		t.Fatalf("want both old versions in the trash, got %d: %s", n, body)
	}
}

func TestTOTPSetupNeedsPassword(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/me/totp/setup", map[string]string{"password": "wrong password!"})
	expect(t, resp, body, 403)
	resp, body = c.do("POST", "/api/me/totp/setup", map[string]string{"password": "correct horse battery"})
	expect(t, resp, body, 200)
	var setup struct{ Secret string }
	json.Unmarshal(body, &setup)
	resp, body = c.do("POST", "/api/me/totp/enable", map[string]string{"code": totpNow(setup.Secret)})
	expect(t, resp, body, 403)
}

func TestPasswordChangeRevokesAppPasswords(t *testing.T) {
	e := newEnv(t)
	dav := davClient(t, e, "admin")
	resp, body := dav("PROPFIND", "/dav/", nil, "Depth", "0")
	expect(t, resp, body, 207)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body = c.do("POST", "/api/me/password", map[string]string{"current": "correct horse battery", "new": "a brand new passphrase"})
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"appPasswordsRevoked":1`) {
		t.Fatalf("response: %s", body)
	}
	resp, body = dav("PROPFIND", "/dav/", nil, "Depth", "0")
	expect(t, resp, body, 401)
}
