package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oddomens/coffer/internal/store"
)

// davClient returns a WebDAV request helper signed in with a new app password.
func davClient(t *testing.T, e *env, user string) func(method, p string, body []byte, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	c := e.client(e.app.URL)
	c.login(user)
	resp, body := c.do("POST", "/api/me/tokens", map[string]string{"name": "dav", "password": "correct horse battery"})
	expect(t, resp, body, 201)
	var tok struct{ Secret string }
	json.Unmarshal(body, &tok)
	return func(method, p string, body []byte, hdr ...string) (*http.Response, []byte) {
		req, _ := http.NewRequest(method, e.app.URL+p, bytes.NewReader(body))
		req.SetBasicAuth(user, tok.Secret)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r, b
	}
}

func setPlace(t *testing.T, e *env, id string, edit func(*store.Place)) {
	t.Helper()
	p, err := e.store.PlaceByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	edit(p)
	if err := e.store.UpdatePlace(t.Context(), p); err != nil {
		t.Fatal(err)
	}
}

func TestSymlinkCannotLeavePlace(t *testing.T) {
	e := newEnv(t)
	if err := os.Symlink("../other", filepath.Join(e.storage, "projects/acme/peek")); err != nil {
		t.Skip("symlinks unsupported")
	}
	_, home := e.addUser("client", "user", "projects/acme")
	c := e.client(e.app.URL)
	c.login("client")
	resp, body := c.do("GET", "/api/files/download?"+q(home, "/peek/private.txt"), nil)
	if resp.StatusCode == 200 {
		t.Fatalf("read another folder through a symlink: %s", body)
	}
	resp, body = c.do("GET", "/api/files/list?"+q(home, "/"), nil)
	expect(t, resp, body, 200)
	if strings.Contains(string(body), "peek") {
		t.Fatalf("escaping symlink listed: %s", body)
	}
	resp, body = c.do("GET", "/api/files/zip?"+q(home, "/")+"&name=peek", nil)
	if resp.StatusCode == 200 {
		t.Fatalf("zipped through a symlink: %d", resp.StatusCode)
	}
	resp, body = c.do("GET", "/api/files/zip?"+q(home, "/"), nil)
	expect(t, resp, body, 200)
	if bytes.Contains(body, []byte("private")) {
		t.Fatal("folder zip followed an escaping symlink")
	}
	dav := davClient(t, e, "client")
	resp, body = dav("GET", "/dav/Home/peek/private.txt", nil)
	if resp.StatusCode == 200 {
		t.Fatalf("WebDAV read through a symlink: %s", body)
	}
	// The admin, whose place is the whole drive, still follows it.
	a := e.client(e.app.URL)
	a.login("admin")
	resp, body = a.do("GET", "/api/files/download?"+q(adminPlace, "/projects/acme/peek/private.txt"), nil)
	expect(t, resp, body, 200)
}

func TestActivityHidesOutsideDestinations(t *testing.T) {
	e := newEnv(t)
	_, home := e.addUser("client", "user", "projects/acme")
	a := e.client(e.app.URL)
	a.login("admin")
	resp, body := a.do("POST", "/api/files/move", map[string]any{"place": adminPlace, "paths": []string{"/projects/acme/renders/final.mp4"}, "dest": "/projects/other"})
	expect(t, resp, body, 200)
	c := e.client(e.app.URL)
	c.login("client")
	resp, body = c.do("GET", "/api/files/activity?"+q(home, "/"), nil)
	expect(t, resp, body, 200)
	if strings.Contains(string(body), "projects/other") || !strings.Contains(string(body), `"detail":"to another folder"`) {
		t.Fatalf("activity names a folder outside the place: %s", body)
	}
}

func TestGrantNeverExceedsGranter(t *testing.T) {
	e := newEnv(t)
	_, aliceHome := e.addUser("alice", "user", "projects")
	bob, _ := e.addUser("bob", "user", "projects/other")
	a := e.client(e.app.URL)
	a.login("alice")
	resp, body := a.do("POST", "/api/places/share", map[string]any{"place": aliceHome, "path": "/acme", "users": []int64{bob.ID}, "canWrite": true, "canDelete": true})
	expect(t, resp, body, 201)
	resp, body = a.do("GET", "/api/places/granted", nil)
	var grants []struct{ ID string }
	json.Unmarshal(body, &grants)
	if len(grants) != 1 {
		t.Fatalf("grants: %s", body)
	}
	b := e.client(e.app.URL)
	b.login("bob")
	resp, body = b.do("POST", "/api/files/mkdir", map[string]string{"place": grants[0].ID, "parent": "/", "name": "ok"})
	expect(t, resp, body, 201)

	// Alice is made read-only: Bob's write and delete rights go with hers.
	setPlace(t, e, aliceHome, func(p *store.Place) { p.CanWrite, p.CanDelete = false, false })
	resp, body = b.do("POST", "/api/files/mkdir", map[string]string{"place": grants[0].ID, "parent": "/", "name": "nope"})
	expect(t, resp, body, 403)
	resp, body = b.do("POST", "/api/files/delete", map[string]any{"place": grants[0].ID, "paths": []string{"/ok"}})
	expect(t, resp, body, 403)
	// And she can't hand them back out by editing the grant.
	resp, body = a.do("PATCH", "/api/places/"+grants[0].ID, map[string]bool{"canWrite": true})
	expect(t, resp, body, 200)
	if strings.Contains(string(body), `"canWrite":true`) {
		t.Fatalf("grant raised above the granter: %s", body)
	}
}

func TestMoveRespectsQuotaAndDeleteRights(t *testing.T) {
	e := newEnv(t)
	fam, home := e.addUser("fam", "user", "projects/acme")
	fam.QuotaBytes = 30 // 25 bytes used
	e.store.UpdateUser(t.Context(), fam)
	a := e.client(e.app.URL)
	a.login("admin")
	resp, body := a.do("POST", "/api/files/move", map[string]any{"place": adminPlace, "paths": []string{"/projects/other/private.txt"}, "dest": "/projects/acme"})
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "storage limit") {
		t.Fatalf("move went over the limit: %s", body)
	}
	if _, err := os.Stat(filepath.Join(e.storage, "projects/other/private.txt")); err != nil {
		t.Fatal("file moved anyway")
	}
	// Moving inside the home doesn't grow it.
	c := e.client(e.app.URL)
	c.login("fam")
	resp, body = c.do("POST", "/api/files/move", map[string]any{"place": home, "paths": []string{"/renders/final.mp4"}, "dest": "/"})
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"done":1`) {
		t.Fatalf("move within the home: %s", body)
	}
}

func TestMoveOutOfPlaceNeedsDelete(t *testing.T) {
	e := newEnv(t)
	u, acme := e.addUser("bob", "user", "projects/acme")
	setPlace(t, e, acme, func(p *store.Place) { p.CanDelete = false })
	other := &store.Place{ID: "otherplace", UserID: u.ID, DriveID: "files", Path: "projects/other", Name: "Other", CanWrite: true, CanDelete: true}
	if err := e.store.CreatePlace(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	b := e.client(e.app.URL)
	b.login("bob")
	resp, body := b.do("POST", "/api/files/move", map[string]any{"place": acme, "paths": []string{"/renders/final.mp4"}, "destPlace": "otherplace", "dest": "/"})
	expect(t, resp, body, 403)
	resp, body = b.do("POST", "/api/files/move", map[string]any{"place": acme, "paths": []string{"/renders/final.mp4"}, "dest": "/"})
	expect(t, resp, body, 200)
}

func TestUploadCompleteRechecksQuota(t *testing.T) {
	e := newEnv(t)
	fam, home := e.addUser("fam", "user", "projects/other")
	fam.QuotaBytes = 1000 // 7 bytes used
	e.store.UpdateUser(t.Context(), fam)
	c := e.client(e.app.URL)
	c.login("fam")
	start := func(name string, size int) string {
		resp, body := c.do("POST", "/api/uploads", map[string]any{"place": home, "dir": "/", "name": name, "size": size})
		expect(t, resp, body, 201)
		var up struct{ ID string }
		json.Unmarshal(body, &up)
		resp, body = c.do("PATCH", "/api/uploads/"+up.ID, bytes.Repeat([]byte("x"), size), "Upload-Offset", "0")
		expect(t, resp, body, 200)
		return up.ID
	}
	one := start("one.bin", 600)
	// The open upload holds its 600 bytes: a second one can't claim them.
	resp, body := c.do("POST", "/api/uploads", map[string]any{"place": home, "dir": "/", "name": "two.bin", "size": 600})
	expect(t, resp, body, 507)
	two := start("two.bin", 300)
	// Something else fills the space before the uploads finish.
	os.WriteFile(filepath.Join(e.storage, "projects/other/big.bin"), bytes.Repeat([]byte("x"), 200), 0o644)
	m, _ := e.srv.drive("files")
	e.srv.index.Scan(t.Context(), "files", m.d)
	resp, body = c.do("POST", "/api/uploads/"+one+"/complete", nil)
	expect(t, resp, body, 200)
	resp, body = c.do("POST", "/api/uploads/"+two+"/complete", nil)
	expect(t, resp, body, 507)
}

func TestTrashRestoreNeedsWrite(t *testing.T) {
	e := newEnv(t)
	_, home := e.addUser("bob", "user", "projects/other")
	setPlace(t, e, home, func(p *store.Place) { p.CanWrite = false })
	a := e.client(e.app.URL)
	a.login("admin")
	resp, body := a.do("POST", "/api/files/delete", map[string]any{"place": adminPlace, "paths": []string{"/projects/other/private.txt"}})
	expect(t, resp, body, 200)
	b := e.client(e.app.URL)
	b.login("bob")
	resp, body = b.do("GET", "/api/trash", nil)
	var items []struct{ ID string }
	json.Unmarshal(body, &items)
	if len(items) != 1 {
		t.Fatalf("trash: %s", body)
	}
	resp, body = b.do("POST", "/api/trash/"+items[0].ID+"/restore", nil)
	expect(t, resp, body, 403)
}

func TestFindPlaceSkipsOfflineDrives(t *testing.T) {
	ps := []*access{{ID: "x", DriveID: "files", Root: ".", m: &mounted{}, Online: false}}
	if findPlace(ps, "files", "a/b") != nil {
		t.Fatal("offline place returned; its nil drive would panic")
	}
}

func TestWebDAVProppatchKeepsContent(t *testing.T) {
	e := newEnv(t)
	dav := davClient(t, e, "admin")
	resp, body := dav("PUT", "/dav/Files/projects/keep.txt", []byte("precious"))
	if resp.StatusCode != 201 && resp.StatusCode != 204 {
		t.Fatalf("PUT: %d %s", resp.StatusCode, body)
	}
	// Windows Explorer sets file times like this after every upload.
	patch := `<?xml version="1.0" encoding="utf-8"?><D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:">` +
		`<D:set><D:prop><Z:Win32LastModifiedTime>Wed, 01 Jan 2025 00:00:00 GMT</Z:Win32LastModifiedTime></D:prop></D:set></D:propertyupdate>`
	resp, body = dav("PROPPATCH", "/dav/Files/projects/keep.txt", []byte(patch))
	expect(t, resp, body, 207)
	b, _ := os.ReadFile(filepath.Join(e.storage, "projects/keep.txt"))
	if string(b) != "precious" {
		t.Fatalf("PROPPATCH changed the file to %q", b)
	}
}

func TestWebDAVLimits(t *testing.T) {
	e := newEnv(t)
	dav := davClient(t, e, "admin")
	resp, body := dav("PROPFIND", "/dav/Files/", nil, "Depth", "infinity")
	expect(t, resp, body, 403)
	resp, body = dav("PROPFIND", "/dav/Files/", nil)
	expect(t, resp, body, 207)
	if strings.Contains(string(body), "final.mp4") {
		t.Fatal("PROPFIND without Depth walked the whole drive")
	}
	huge := `<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:prop>` + strings.Repeat("<D:getetag/>", 200000) + `</D:prop></D:propfind>`
	resp, body = dav("PROPFIND", "/dav/Files/", []byte(huge), "Depth", "0")
	if resp.StatusCode < 400 {
		t.Fatalf("huge PROPFIND body accepted: %d", resp.StatusCode)
	}
	resp, body = dav("MKCOL", "/dav/Files/projects/bad.", nil)
	if resp.StatusCode == 201 {
		t.Fatal("MKCOL created an invalid name")
	}
	resp, body = dav("MOVE", "/dav/Files/projects/other/private.txt", nil, "Destination", e.app.URL+"/dav/Files/projects/other/bad%01.txt")
	if resp.StatusCode < 400 {
		t.Fatalf("MOVE to an invalid name: %d", resp.StatusCode)
	}
}

func TestWebDAVLocksArePerUser(t *testing.T) {
	e := newEnv(t)
	e.addUser("bob", "user", "projects")
	admin, bob := davClient(t, e, "admin"), davClient(t, e, "bob")
	lock := `<?xml version="1.0" encoding="utf-8"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope>` +
		`<D:locktype><D:write/></D:locktype><D:owner>x</D:owner></D:lockinfo>`
	resp, body := admin("LOCK", "/dav/", []byte(lock), "Timeout", "Infinite", "Depth", "infinity")
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "Second-3600") {
		t.Fatalf("lock timeout not capped: %s", body)
	}
	resp, body = bob("PUT", "/dav/Home/bob.txt", []byte("still works"))
	if resp.StatusCode != 201 && resp.StatusCode != 204 {
		t.Fatalf("another user's WebDAV lock blocked a write: %d %s", resp.StatusCode, body)
	}
}

func TestWebDAVCutOffPutKeepsOldFile(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/me/tokens", map[string]string{"name": "dav", "password": "correct horse battery"})
	expect(t, resp, body, 201)
	var tok struct{ Secret string }
	json.Unmarshal(body, &tok)
	u, _ := url.Parse(e.app.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("PUT", e.app.URL+"/dav/Files/projects/other/private.txt", nil)
	req.SetBasicAuth("admin", tok.Secret)
	conn.Write([]byte("PUT /dav/Files/projects/other/private.txt HTTP/1.1\r\nHost: " + u.Host +
		"\r\nAuthorization: " + req.Header.Get("Authorization") + "\r\nContent-Length: 1000\r\n\r\npartial"))
	conn.(*net.TCPConn).CloseWrite()
	io.ReadAll(conn)
	conn.Close()
	b, _ := os.ReadFile(filepath.Join(e.storage, "projects/other/private.txt"))
	if string(b) != "private" {
		t.Fatalf("a cut-off PUT replaced the file with %q", b)
	}
}

func TestDavServesScriptsAsBytes(t *testing.T) {
	e := newEnv(t)
	os.WriteFile(filepath.Join(e.storage, "projects/acme/app.js"), []byte("alert(1)"), 0o644)
	e.addUser("client", "user", "projects/acme")
	dav := davClient(t, e, "client")
	resp, body := dav("GET", "/dav/Home/app.js", nil)
	expect(t, resp, body, 200)
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("WebDAV served a script as %q", ct)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("WebDAV file response is not sandboxed")
	}
}
