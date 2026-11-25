package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/oddomens/coffer/internal/config"
	"github.com/oddomens/coffer/internal/index"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

type env struct {
	t       *testing.T
	srv     *Server
	app     *httptest.Server
	pub     *httptest.Server
	storage string
	store   *store.Store
}

const adminPlace = "d-files"

func newEnv(t *testing.T) *env {
	t.Helper()
	data, root := t.TempDir(), t.TempDir()
	storage := filepath.Join(root, "files")
	os.MkdirAll(storage, 0o755)
	cfg := &config.Config{DataDir: data, StorageDir: storage, SessionIdle: time.Hour, SessionMax: 2 * time.Hour,
		DriveRoots: []string{root}, UpdateRepo: "OddOmens/Coffer"}
	st, err := store.Open(filepath.Join(data, "coffer.db"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := security.LoadKeys(data, "")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(filepath.Join(data, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(storage, "projects/acme/renders"), 0o755)
	os.MkdirAll(filepath.Join(storage, "projects/other"), 0o755)
	os.WriteFile(filepath.Join(storage, "projects/acme/renders/final.mp4"), []byte("video-bytes"), 0o644)
	os.WriteFile(filepath.Join(storage, "projects/acme/renders/final_v2.mp4"), []byte("video-bytes-v2"), 0o644)
	os.WriteFile(filepath.Join(storage, "projects/other/private.txt"), []byte("private"), 0o644)
	os.WriteFile(filepath.Join(storage, "evil.html"), []byte("<script>alert(1)</script>"), 0o644)

	srv, err := New(Options{
		Config: cfg, Store: st, Index: idx, Keys: keys, Version: "test",
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		WebUI: fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, srv: srv, app: httptest.NewServer(srv.AppHandler()), pub: httptest.NewServer(srv.PublicHandler()),
		storage: storage, store: st}
	t.Cleanup(func() { e.app.Close(); e.pub.Close(); srv.Close(); idx.Close(); st.Close() })
	m, _ := srv.drive("files")
	if err := idx.Scan(t.Context(), "files", m.d); err != nil {
		t.Fatal(err)
	}
	e.addUser("admin", "admin", "")
	return e
}

// addUser creates a user; non-admins get a home place at scope.
func (e *env) addUser(name, role, scope string) (*store.User, string) {
	h, _ := security.HashPassword("correct horse battery")
	u := &store.User{Username: name, PasswordHash: h, Role: role, Scope: ".", CanWrite: true, CanDelete: true, CanShare: true}
	if err := e.store.CreateUser(e.t.Context(), u); err != nil {
		e.t.Fatal(err)
	}
	if role == "admin" {
		return u, adminPlace
	}
	p := &store.Place{ID: security.RandomToken(9), UserID: u.ID, DriveID: "files", Path: scope, Name: "Home",
		CanWrite: true, CanDelete: true, CanShare: true}
	if err := e.store.CreatePlace(e.t.Context(), p); err != nil {
		e.t.Fatal(err)
	}
	return u, p.ID
}

type client struct {
	t    *testing.T
	base string
	c    *http.Client
}

func (e *env) client(base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: e.t, base: base, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

func (c *client) do(method, path string, body any, hdr ...string) (*http.Response, []byte) {
	c.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func (c *client) login(user string) {
	c.t.Helper()
	resp, body := c.do("POST", "/api/auth/login", map[string]string{"username": user, "password": "correct horse battery"})
	if resp.StatusCode != 200 {
		c.t.Fatalf("login %s: %d %s", user, resp.StatusCode, body)
	}
}

func expect(t *testing.T, resp *http.Response, body []byte, status int) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("%s %s: got %d want %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, status, body)
	}
}

func q(place, p string) string {
	return "place=" + url.QueryEscape(place) + "&path=" + url.QueryEscape(p)
}

func TestAuthRequired(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	resp, body := c.do("GET", "/api/files/list?"+q(adminPlace, "/"), nil)
	expect(t, resp, body, 401)
	resp, body = c.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "wrong password"})
	expect(t, resp, body, 401)
	resp, body = c.do("POST", "/api/auth/login", map[string]string{"username": "nobody", "password": "wrong password"})
	expect(t, resp, body, 401)
}

func TestLoginLockout(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	var last int
	for range 7 {
		resp, _ := c.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "wrong password"})
		last = resp.StatusCode
	}
	if last != 429 {
		t.Fatalf("expected lockout after repeated failures, got %d", last)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.client(e.app.URL).do("GET", "/", nil)
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
}

func TestCrossSiteRequestRejected(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/files/mkdir", map[string]string{"place": adminPlace, "parent": "/", "name": "x"},
		"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example")
	expect(t, resp, body, 403)
}

func TestTraversalAndScope(t *testing.T) {
	e := newEnv(t)
	_, home := e.addUser("client", "user", "projects/acme")
	c := e.client(e.app.URL)
	c.login("client")

	resp, body := c.do("GET", "/api/files/list?"+q(home, "/"), nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "renders") || strings.Contains(string(body), "other") {
		t.Fatalf("scoped listing wrong: %s", body)
	}
	for _, p := range []string{"../other/private.txt", "/../../projects/other/private.txt"} {
		resp, body := c.do("GET", "/api/files/download?"+q(home, p), nil)
		if resp.StatusCode == 200 || strings.Contains(string(body), "private") {
			t.Fatalf("escaped scope with %q: %d %s", p, resp.StatusCode, body)
		}
	}
	// The admin's drive place is not this user's.
	resp, body = c.do("GET", "/api/files/list?"+q(adminPlace, "/"), nil)
	expect(t, resp, body, 404)
	resp, body = c.do("GET", "/api/files/list?"+q(home, "/.coffer"), nil)
	expect(t, resp, body, 400)
	resp, body = c.do("GET", "/api/admin/users", nil)
	expect(t, resp, body, 403)
	// Search only sees the user's own place.
	resp, body = c.do("GET", "/api/search?q=private", nil)
	expect(t, resp, body, 200)
	if strings.Contains(string(body), "private.txt") {
		t.Fatalf("search leaked outside scope: %s", body)
	}
}

func TestSearchAndFolderSizes(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("GET", "/api/search?q=inal_v", nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "final_v2.mp4") {
		t.Fatalf("substring search missed: %s", body)
	}
	resp, body = c.do("GET", "/api/search?q=&kinds=video", nil)
	expect(t, resp, body, 200)
	var res struct{ Entries []struct{ Name string } }
	json.Unmarshal(body, &res)
	if len(res.Entries) != 2 {
		t.Fatalf("kind filter: %s", body)
	}
	resp, body = c.do("GET", "/api/files/list?"+q(adminPlace, "/projects"), nil)
	expect(t, resp, body, 200)
	var list struct {
		Entries []struct {
			Name  string
			Size  int64
			Items int64
		}
	}
	json.Unmarshal(body, &list)
	for _, en := range list.Entries {
		if en.Name == "acme" && (en.Size != int64(len("video-bytes")+len("video-bytes-v2")) || en.Items != 2) {
			t.Fatalf("folder size wrong: %+v", en)
		}
	}
	// Incremental index after a rename and a delete.
	resp, body = c.do("POST", "/api/files/rename", map[string]string{"place": adminPlace, "path": "/projects/acme/renders/final.mp4", "name": "master.mp4"})
	expect(t, resp, body, 200)
	resp, body = c.do("GET", "/api/search?q=master", nil)
	if !strings.Contains(string(body), "master.mp4") {
		t.Fatalf("index not updated after rename: %s", body)
	}
	resp, body = c.do("POST", "/api/files/delete", map[string]any{"place": adminPlace, "paths": []string{"/projects/acme/renders/master.mp4"}})
	expect(t, resp, body, 200)
	resp, body = c.do("GET", "/api/files/stat?"+q(adminPlace, "/projects/acme"), nil)
	var st struct{ Size, Items int64 }
	json.Unmarshal(body, &st)
	if st.Items != 1 || st.Size != int64(len("video-bytes-v2")) {
		t.Fatalf("folder size not updated after delete: %s", body)
	}
}

func TestHTMLNeverInline(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("GET", "/api/files/download?"+q(adminPlace, "/evil.html")+"&inline=1", nil)
	expect(t, resp, body, 200)
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "html") {
		t.Fatalf("html served as %s", ct)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("file response not sandboxed")
	}
}

func TestUploadResumable(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	data := []byte("hello resumable world")
	resp, body := c.do("POST", "/api/uploads", map[string]any{"place": adminPlace, "dir": "/projects", "name": "sub/dir/note.txt", "size": len(data)})
	expect(t, resp, body, 201)
	var up struct{ ID string }
	json.Unmarshal(body, &up)
	resp, body = c.do("PATCH", "/api/uploads/"+up.ID, data[:5], "Upload-Offset", "0")
	expect(t, resp, body, 200)
	resp, body = c.do("PATCH", "/api/uploads/"+up.ID, data[5:], "Upload-Offset", "0")
	expect(t, resp, body, 409)
	resp, body = c.do("PATCH", "/api/uploads/"+up.ID, data[5:], "Upload-Offset", "5")
	expect(t, resp, body, 200)
	resp, body = c.do("POST", "/api/uploads/"+up.ID+"/complete", nil)
	expect(t, resp, body, 200)
	got, err := os.ReadFile(filepath.Join(e.storage, "projects/sub/dir/note.txt"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("upload content mismatch: %q %v", got, err)
	}
	resp, body = c.do("GET", "/api/search?q=note.txt", nil)
	if !strings.Contains(string(body), "note.txt") {
		t.Fatalf("upload not indexed: %s", body)
	}
	resp, body = c.do("POST", "/api/uploads", map[string]any{"place": adminPlace, "dir": "/", "name": "../escape.txt", "size": 1})
	expect(t, resp, body, 400)
}

func TestTextEditor(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("PUT", "/api/files/text", map[string]any{"place": adminPlace, "path": "/notes.md", "content": "# Hi", "create": true})
	expect(t, resp, body, 200)
	resp, body = c.do("GET", "/api/files/text?"+q(adminPlace, "/notes.md"), nil)
	expect(t, resp, body, 200)
	var doc struct{ Content, Etag string }
	json.Unmarshal(body, &doc)
	if doc.Content != "# Hi" {
		t.Fatalf("content %q", doc.Content)
	}
	resp, body = c.do("PUT", "/api/files/text", map[string]any{"place": adminPlace, "path": "/notes.md", "content": "# Edited", "etag": "stale"})
	expect(t, resp, body, 409)
	resp, body = c.do("PUT", "/api/files/text", map[string]any{"place": adminPlace, "path": "/notes.md", "content": "# Edited", "etag": doc.Etag})
	expect(t, resp, body, 200)
	b, _ := os.ReadFile(filepath.Join(e.storage, "notes.md"))
	if string(b) != "# Edited" {
		t.Fatalf("file not saved: %q", b)
	}
}

func createShare(t *testing.T, c *client, req map[string]any) (id, token string) {
	t.Helper()
	resp, body := c.do("POST", "/api/shares", req)
	expect(t, resp, body, 201)
	var sh struct{ ID, URL string }
	json.Unmarshal(body, &sh)
	u, _ := url.Parse(sh.URL)
	return sh.ID, strings.TrimPrefix(u.Path, "/s/")
}

func TestShareFolderConfined(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme"})

	pub := e.client(e.pub.URL)
	resp, body := pub.do("GET", "/api/s/"+token+"/list?path=/renders", nil)
	expect(t, resp, body, 200)
	resp, body = pub.do("GET", "/api/s/"+token+"/download?path=/renders/final.mp4", nil)
	expect(t, resp, body, 200)
	if string(body) != "video-bytes" {
		t.Fatalf("got %q", body)
	}
	for _, p := range []string{"../other/private.txt", "/../../projects/other/private.txt"} {
		resp, body := pub.do("GET", "/api/s/"+token+"/download?path="+url.QueryEscape(p), nil)
		if resp.StatusCode == 200 {
			t.Fatalf("share escaped with %q: %s", p, body)
		}
	}
	resp, body = pub.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "correct horse battery"})
	expect(t, resp, body, 404)
	if resp, _ = pub.do("PROPFIND", "/dav/", nil); resp.StatusCode == 207 {
		t.Fatal("WebDAV reachable on the public listener")
	}
	resp, body = pub.do("GET", "/api/s/"+strings.Repeat("A", 43), nil)
	expect(t, resp, body, 404)
}

func TestShareOptions(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	id, token := createShare(t, c, map[string]any{
		"place": adminPlace, "path": "/projects/acme/renders/final.mp4", "password": "letmein-now!", "maxDownloads": 1,
	})
	pub := e.client(e.pub.URL)
	resp, body := pub.do("GET", "/api/s/"+token+"/download", nil)
	expect(t, resp, body, 401)
	resp, body = pub.do("POST", "/api/s/"+token+"/auth", map[string]string{"password": "nope"})
	expect(t, resp, body, 401)
	resp, body = pub.do("POST", "/api/s/"+token+"/auth", map[string]string{"password": "letmein-now!"})
	expect(t, resp, body, 204)
	resp, body = pub.do("GET", "/api/s/"+token+"/download", nil)
	expect(t, resp, body, 200)
	resp, body = pub.do("GET", "/api/s/"+token+"/download", nil)
	expect(t, resp, body, 410)

	// Pausing, network allowlists and view-only links.
	id2, token2 := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme", "canDownload": false})
	resp, body = pub.do("GET", "/api/s/"+token2+"/download?path=/renders/final.mp4", nil)
	expect(t, resp, body, 403)
	resp, body = pub.do("GET", "/api/s/"+token2+"/download?path=/renders/final.mp4&inline=1", nil)
	expect(t, resp, body, 200)
	resp, body = c.do("PATCH", "/api/shares/"+id2, map[string]any{"allowedNets": "203.0.113.0/24"})
	expect(t, resp, body, 200)
	resp, body = pub.do("GET", "/api/s/"+token2, nil)
	expect(t, resp, body, 403)
	resp, body = c.do("PATCH", "/api/shares/"+id2, map[string]any{"allowedNets": "", "disabled": true})
	expect(t, resp, body, 200)
	resp, body = pub.do("GET", "/api/s/"+token2, nil)
	expect(t, resp, body, 410)

	resp, body = c.do("DELETE", "/api/shares/"+id, nil)
	expect(t, resp, body, 204)
	resp, body = pub.do("GET", "/api/s/"+token, nil)
	expect(t, resp, body, 404)
}

func TestDropBoxShare(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme",
		"canView": false, "canDownload": false, "canUpload": true, "uploadExts": "txt, pdf", "uploadNamed": true, "notify": 4})
	pub := e.client(e.pub.URL)
	resp, body := pub.do("GET", "/api/s/"+token+"/list", nil)
	expect(t, resp, body, 403)
	resp, body = pub.do("POST", "/api/s/"+token+"/uploads", map[string]any{"dir": "/", "name": "virus.exe", "size": 3, "uploader": "Sam"})
	expect(t, resp, body, 415)
	resp, body = pub.do("POST", "/api/s/"+token+"/uploads", map[string]any{"dir": "/", "name": "delivery.txt", "size": 3})
	expect(t, resp, body, 400) // uploader name required

	data := []byte("abc")
	resp, body = pub.do("POST", "/api/s/"+token+"/uploads", map[string]any{"dir": "/", "name": "delivery.txt", "size": len(data), "uploader": "Sam Client"})
	expect(t, resp, body, 201)
	var up struct{ ID string }
	json.Unmarshal(body, &up)
	resp, body = pub.do("PATCH", "/api/s/"+token+"/uploads/"+up.ID, data, "Upload-Offset", "0")
	expect(t, resp, body, 200)
	resp, body = pub.do("POST", "/api/s/"+token+"/uploads/"+up.ID+"/complete", nil)
	expect(t, resp, body, 200)
	matches, _ := filepath.Glob(filepath.Join(e.storage, "projects/acme/Sam Client – *", "delivery.txt"))
	if len(matches) != 1 {
		t.Fatal("named upload not filed under the uploader's folder")
	}
	resp, body = c.do("GET", "/api/notifications", nil)
	if !strings.Contains(string(body), "delivery.txt") {
		t.Fatalf("owner not notified: %s", body)
	}
}

func TestShareFollowsRename(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders"})
	resp, body := c.do("POST", "/api/files/rename", map[string]string{"place": adminPlace, "path": "/projects/acme", "name": "acme-2026"})
	expect(t, resp, body, 200)
	resp, body = e.client(e.pub.URL).do("GET", "/api/s/"+token+"/list", nil)
	expect(t, resp, body, 200)
}

func TestSharedWithMe(t *testing.T) {
	e := newEnv(t)
	alice, aliceHome := e.addUser("alice", "user", "projects")
	bob, _ := e.addUser("bob", "user", "projects/other")
	a := e.client(e.app.URL)
	a.login("alice")
	resp, body := a.do("POST", "/api/places/share", map[string]any{"place": aliceHome, "path": "/acme", "users": []int64{bob.ID}, "canWrite": false})
	expect(t, resp, body, 201)

	b := e.client(e.app.URL)
	b.login("bob")
	resp, body = b.do("GET", "/api/places", nil)
	var pl struct {
		Places []struct{ ID, Kind, GrantedBy string }
	}
	json.Unmarshal(body, &pl)
	var shared string
	for _, p := range pl.Places {
		if p.Kind == "shared" && p.GrantedBy == "alice" {
			shared = p.ID
		}
	}
	if shared == "" {
		t.Fatalf("grant missing: %s", body)
	}
	resp, body = b.do("GET", "/api/files/download?"+q(shared, "/renders/final.mp4"), nil)
	expect(t, resp, body, 200)
	resp, body = b.do("POST", "/api/files/mkdir", map[string]string{"place": shared, "parent": "/", "name": "x"})
	expect(t, resp, body, 403)

	// When Alice loses access, Bob does too.
	ps, _ := e.store.PlacesForUser(t.Context(), alice.ID)
	ps[0].Path = "projects/other"
	e.store.UpdatePlace(t.Context(), ps[0])
	resp, body = b.do("GET", "/api/files/list?"+q(shared, "/"), nil)
	expect(t, resp, body, 404)
}

func TestShareDiesWithOwnerScope(t *testing.T) {
	e := newEnv(t)
	u, home := e.addUser("client", "user", "projects/acme")
	c := e.client(e.app.URL)
	c.login("client")
	_, token := createShare(t, c, map[string]any{"place": home, "path": "/renders"})
	ps, _ := e.store.PlacesForUser(t.Context(), u.ID)
	ps[0].Path = "projects/other"
	e.store.UpdatePlace(t.Context(), ps[0])
	resp, body := e.client(e.pub.URL).do("GET", "/api/s/"+token, nil)
	expect(t, resp, body, 404)
}

func TestTrashRoundTrip(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/files/delete", map[string]any{"place": adminPlace, "paths": []string{"/projects/other/private.txt"}})
	expect(t, resp, body, 200)
	resp, body = c.do("GET", "/api/trash", nil)
	expect(t, resp, body, 200)
	var items []struct{ ID string }
	json.Unmarshal(body, &items)
	if len(items) != 1 {
		t.Fatalf("trash: %s", body)
	}
	resp, body = c.do("POST", "/api/trash/"+items[0].ID+"/restore", nil)
	expect(t, resp, body, 200)
	if _, err := os.Stat(filepath.Join(e.storage, "projects/other/private.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestWebDAV(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/me/tokens", map[string]string{"name": "Finder", "password": "correct horse battery"})
	expect(t, resp, body, 201)
	var tok struct{ Secret string }
	json.Unmarshal(body, &tok)

	dav := func(method, p string, body []byte, hdr ...string) (*http.Response, []byte) {
		req, _ := http.NewRequest(method, e.app.URL+p, bytes.NewReader(body))
		req.SetBasicAuth("admin", tok.Secret)
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
	resp, body = dav("PROPFIND", "/dav/", nil, "Depth", "1")
	expect(t, resp, body, 207)
	if !strings.Contains(string(body), "Files") {
		t.Fatalf("drive missing from root: %s", body)
	}
	resp, body = dav("PUT", "/dav/Files/projects/from-finder.txt", []byte("hello finder"))
	if resp.StatusCode != 201 && resp.StatusCode != 204 {
		t.Fatalf("PUT: %d %s", resp.StatusCode, body)
	}
	b, _ := os.ReadFile(filepath.Join(e.storage, "projects/from-finder.txt"))
	if string(b) != "hello finder" {
		t.Fatalf("PUT content %q", b)
	}
	resp, body = dav("GET", "/dav/Files/projects/other/private.txt", nil)
	expect(t, resp, body, 200)
	resp, body = dav("DELETE", "/dav/Files/projects/from-finder.txt", nil)
	expect(t, resp, body, 204)
	resp, body = c.do("GET", "/api/trash", nil)
	if !strings.Contains(string(body), "from-finder.txt") {
		t.Fatalf("WebDAV delete did not go to trash: %s", body)
	}
	// Wrong secret, and the account password, are both refused.
	req, _ := http.NewRequest("PROPFIND", e.app.URL+"/dav/", nil)
	req.SetBasicAuth("admin", "correct horse battery")
	r, _ := http.DefaultClient.Do(req)
	if r.StatusCode != 401 {
		t.Fatalf("account password accepted for WebDAV: %d", r.StatusCode)
	}
}

func TestRecoveryCodeLogin(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/me/totp/setup", map[string]string{"password": "correct horse battery"})
	expect(t, resp, body, 200)
	var setup struct{ Secret string }
	json.Unmarshal(body, &setup)
	code := totpNow(setup.Secret)
	resp, body = c.do("POST", "/api/me/totp/enable", map[string]string{"code": code, "password": "correct horse battery"})
	expect(t, resp, body, 200)
	var en struct{ RecoveryCodes []string }
	json.Unmarshal(body, &en)
	if len(en.RecoveryCodes) != 10 {
		t.Fatalf("recovery codes: %s", body)
	}
	fresh := e.client(e.app.URL)
	resp, body = fresh.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "correct horse battery"})
	expect(t, resp, body, 401)
	resp, body = fresh.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "correct horse battery", "code": en.RecoveryCodes[0]})
	expect(t, resp, body, 200)
	again := e.client(e.app.URL)
	resp, body = again.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "correct horse battery", "code": en.RecoveryCodes[0]})
	expect(t, resp, body, 401)
}

func TestRequire2FAPolicy(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("GET", "/api/admin/settings", nil)
	var st map[string]any
	json.Unmarshal(body, &st)
	st["require2fa"] = "all"
	resp, body = c.do("PUT", "/api/admin/settings", st)
	expect(t, resp, body, 200)
	resp, body = c.do("GET", "/api/files/list?"+q(adminPlace, "/"), nil)
	expect(t, resp, body, 403)
	resp, body = c.do("GET", "/api/me", nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), `"mustEnroll2fa":true`) {
		t.Fatalf("me does not flag enrollment: %s", body)
	}
}

func TestLocalDriveRoots(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/admin/drives", map[string]any{"name": "Etc", "kind": "local", "path": "/etc"})
	expect(t, resp, body, 400)
	second := filepath.Join(filepath.Dir(e.storage), "archive")
	os.MkdirAll(second, 0o755)
	resp, body = c.do("POST", "/api/admin/drives", map[string]any{"name": "Archive", "kind": "local", "path": second})
	expect(t, resp, body, 201)
	resp, body = c.do("GET", "/api/places", nil)
	if !strings.Contains(string(body), "Archive") {
		t.Fatalf("second drive not listed: %s", body)
	}
	_ = strconv.Itoa
}

// totpNow computes the current code for tests.
func totpNow(secret string) string {
	for c := range 1000000 {
		code := strconv.Itoa(c)
		for len(code) < 6 {
			code = "0" + code
		}
		if _, ok := security.VerifyTOTP(secret, code, time.Now(), 0); ok {
			return code
		}
	}
	return ""
}
