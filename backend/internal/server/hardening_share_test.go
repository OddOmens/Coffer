package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/store"
)

// Regression tests for the public share surface.

func TestCountsAsDownload(t *testing.T) {
	for _, c := range []struct {
		rng, ifRange string
		want         bool
	}{
		{"", "", true},
		{"bytes=0-", "", true},
		{"bytes=0-99", "", true},
		{"bytes=-500", "", true},       // suffix range: the end, or the whole file
		{"bytes= 0-", "", true},        // spaces are allowed by ServeContent
		{"bytes=100-200,0-", "", true}, // several ranges
		{"bytes=1-", `"bogus"`, true},  // If-Range mismatch serves everything
		{"items=1-", "", true},         // not bytes: served whole
		{"bytes=100-", "", false},      // scrubbing
		{"bytes=4096-8191", "", false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if c.rng != "" {
			r.Header.Set("Range", c.rng)
		}
		if c.ifRange != "" {
			r.Header.Set("If-Range", c.ifRange)
		}
		if got := countsAsDownload(r); got != c.want {
			t.Errorf("Range %q If-Range %q: got %v want %v", c.rng, c.ifRange, got, c.want)
		}
	}
}

func TestShareDownloadLimitWithRanges(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders/final.mp4", "maxDownloads": 2})
	pub := e.client(e.pub.URL)
	for range 2 {
		resp, body := pub.do("GET", "/api/s/"+token+"/download", nil, "Range", "bytes=-100000")
		expect(t, resp, body, 206)
		if string(body) != "video-bytes" {
			t.Fatalf("got %q", body)
		}
	}
	// Both suffix-range fetches counted, so the link is used up.
	resp, body := pub.do("GET", "/api/s/"+token+"/download", nil, "Range", "bytes=-100000")
	expect(t, resp, body, 410)
}

func TestShareBadPathKeepsDownload(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme", "maxDownloads": 1})
	pub := e.client(e.pub.URL)
	resp, body := pub.do("GET", "/api/s/"+token+"/download?path=/nope.mp4", nil)
	expect(t, resp, body, 404)
	resp, body = pub.do("GET", "/api/s/"+token+"/download?path=/renders", nil)
	expect(t, resp, body, 400)
	resp, body = pub.do("GET", "/api/s/"+token+"/download?path=/renders/final.mp4", nil)
	expect(t, resp, body, 200)
}

func TestShareViewOnlyServesPreviewsOnly(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.storage, "projects/acme/renders")
	os.WriteFile(filepath.Join(dir, "project.zip"), []byte("PK"), 0o644)
	os.WriteFile(filepath.Join(dir, "photo.cr2"), []byte("raw"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o644)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders", "canDownload": false})
	pub := e.client(e.pub.URL)
	for _, p := range []string{"/project.zip", "/photo.cr2"} {
		resp, body := pub.do("GET", "/api/s/"+token+"/download?inline=1&path="+p, nil)
		expect(t, resp, body, 403)
	}
	for _, p := range []string{"/final.mp4", "/notes.txt"} {
		resp, body := pub.do("GET", "/api/s/"+token+"/download?inline=1&path="+p, nil)
		expect(t, resp, body, 200)
	}

	// With downloads allowed, the original is fine.
	_, token = createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders"})
	resp, body := pub.do("GET", "/api/s/"+token+"/download?inline=1&path=/project.zip", nil)
	expect(t, resp, body, 200)
}

func TestShareWatermarkedThumb(t *testing.T) {
	e := newEnv(t)
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 64, 48)))
	os.WriteFile(filepath.Join(e.storage, "projects/acme/renders/still.png"), b.Bytes(), 0o644)
	c := e.client(e.app.URL)
	c.login("admin")
	_, plain := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders"})
	_, marked := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders", "watermark": true})
	pub := e.client(e.pub.URL)

	resp, body := pub.do("GET", "/api/s/"+plain+"/thumb?path=/still.png", nil)
	expect(t, resp, body, 200)
	clean := body
	resp, body = pub.do("GET", "/api/s/"+marked+"/thumb?path=/still.png", nil)
	expect(t, resp, body, 200)
	if resp.Header.Get("Content-Type") != "image/jpeg" || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("headers: %v", resp.Header)
	}
	if bytes.Equal(body, clean) {
		t.Fatal("watermarked link served the plain thumbnail")
	}
	resp, body = pub.do("GET", "/api/s/"+marked+"/thumb?path=/notes.txt", nil)
	expect(t, resp, body, 404)
}

func TestShareNetsBehindUntrustedProxy(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme", "allowedNets": "127.0.0.0/8"})
	pub := e.client(e.pub.URL)
	resp, body := pub.do("GET", "/api/s/"+token, nil)
	expect(t, resp, body, 200)
	// A tunnel that isn't in COFFER_TRUSTED_PROXIES: the peer is local, the
	// visitor isn't.
	for _, h := range []string{"CF-Connecting-IP", "X-Forwarded-For"} {
		resp, body = pub.do("GET", "/api/s/"+token, nil, h, "198.51.100.7")
		expect(t, resp, body, 403)
	}
	// Links without an allowlist don't care.
	_, open := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme"})
	resp, body = pub.do("GET", "/api/s/"+open, nil, "CF-Connecting-IP", "198.51.100.7")
	expect(t, resp, body, 200)

	// Through a trusted proxy the forwarded address decides.
	e2 := newEnv(t)
	e2.srv.cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	c2 := e2.client(e2.app.URL)
	c2.login("admin")
	_, token2 := createShare(t, c2, map[string]any{"place": adminPlace, "path": "/projects/acme", "allowedNets": "192.168.0.0/16"})
	pub2 := e2.client(e2.pub.URL)
	resp, body = pub2.do("GET", "/api/s/"+token2, nil, "CF-Connecting-IP", "192.168.1.20")
	expect(t, resp, body, 200)
	resp, body = pub2.do("GET", "/api/s/"+token2, nil, "CF-Connecting-IP", "198.51.100.7")
	expect(t, resp, body, 403)
}

func TestShareCodeTriesAreCapped(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.client(e.app.URL)
	c.login("admin")
	id, _ := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme"})
	if err := e.store.PutShareCode(ctx, id, "a@b.test", "hash", time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		sc, err := e.store.TryShareCode(ctx, id, "A@b.test", 5)
		if err != nil || sc.Hash != "hash" || sc.Attempts != i+1 {
			t.Fatalf("try %d: %+v %v", i+1, sc, err)
		}
	}
	if _, err := e.store.TryShareCode(ctx, id, "a@b.test", 5); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sixth try: %v", err)
	}
	e.store.PutShareCode(ctx, id, "late@b.test", "hash", time.Now().Add(-time.Second).Unix())
	if _, err := e.store.TryShareCode(ctx, id, "late@b.test", 5); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired code: %v", err)
	}
}

func TestShareCodeBackoffPerAddress(t *testing.T) {
	e := newEnv(t)
	e.srv.cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	port, msgs := fakeSMTP(t)
	c := e.client(e.app.URL)
	c.login("admin")
	enableMail(t, c, port)
	id, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme", "access": "invited",
		"invite": map[string]any{"emails": []string{"client@acme.test"}}})
	nextMail(t, msgs)

	const email, code = "client@acme.test", "123456"
	fresh := func() { // as if a new code had been asked for
		e.store.PutShareCode(t.Context(), id, email, e.srv.keys.Sign("share-code", id, email, code), time.Now().Add(time.Minute).Unix())
	}
	guess := func(code string, ip int) (*http.Response, []byte) {
		return e.client(e.pub.URL).do("POST", "/api/s/"+token+"/verify/check", map[string]string{"email": email, "code": code},
			"CF-Connecting-IP", "198.51.100."+strconv.Itoa(ip))
	}
	fresh()
	for i := range 5 {
		resp, body := guess("000000", i+1)
		expect(t, resp, body, 401)
	}
	// A new code and new IPs don't reset the tries against this address.
	fresh()
	resp, body := guess("000000", 6)
	expect(t, resp, body, 401)
	resp, body = guess(code, 7)
	expect(t, resp, body, 429)
}

func TestShareInviteOnlyDropsSelfVerified(t *testing.T) {
	e := newEnv(t)
	port, msgs := fakeSMTP(t)
	c := e.client(e.app.URL)
	c.login("admin")
	enableMail(t, c, port)
	id, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme", "access": "email"})

	v := e.client(e.pub.URL)
	resp, body := v.do("POST", "/api/s/"+token+"/verify", map[string]string{"email": "walkin@else.test"})
	expect(t, resp, body, 204)
	code := codeRe.FindStringSubmatch(nextMail(t, msgs))[1]
	resp, body = v.do("POST", "/api/s/"+token+"/verify/check", map[string]string{"email": "walkin@else.test", "code": code})
	expect(t, resp, body, 204)
	resp, body = v.do("GET", "/api/s/"+token+"/list?path=/", nil)
	expect(t, resp, body, 200)

	resp, body = c.do("PATCH", "/api/shares/"+id, map[string]any{"access": "invited"})
	expect(t, resp, body, 200)
	resp, body = v.do("GET", "/api/s/"+token+"/list?path=/", nil)
	expect(t, resp, body, 403)
	resp, body = v.do("GET", "/api/s/"+token+"/download?path=/renders/final.mp4", nil)
	expect(t, resp, body, 403)
}

func TestShareCommentPrivacy(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.client(e.app.URL)
	c.login("admin")
	id, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders", "canComment": true})
	p := "projects/acme/renders/final.mp4"
	guest := &store.Comment{DriveID: "files", Path: p, ShareID: id, Author: "client@acme.test", Email: "client@acme.test", Kind: store.CommentApproved}
	note := &store.Comment{DriveID: "files", Path: p, UserID: 1, Author: "admin", Kind: store.CommentNote, Body: "budget is 5k", Internal: true}
	for _, cm := range []*store.Comment{guest, note} {
		if err := e.store.AddComment(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	reply := &store.Comment{DriveID: "files", Path: p, ParentID: note.ID, UserID: 1, Author: "admin", Kind: store.CommentNote, Body: "agreed, 5k"}
	e.store.AddComment(ctx, reply)

	v := e.client(e.pub.URL)
	resp, body := v.do("GET", "/api/s/"+token+"/comments?path=/final.mp4", nil)
	expect(t, resp, body, 200)
	if strings.Contains(string(body), "@") || strings.Contains(string(body), "5k") {
		t.Fatalf("comments leak: %s", body)
	}
	resp, body = v.do("GET", "/api/s/"+token+"/list", nil)
	expect(t, resp, body, 200)
	var list struct {
		Entries []struct {
			Name   string
			Review *store.ReviewSummary
		}
	}
	json.Unmarshal(body, &list)
	for _, en := range list.Entries {
		if en.Name == "final.mp4" {
			if en.Review == nil || en.Review.Comments != 0 || en.Review.Open != 0 || strings.Contains(en.Review.By, "@") {
				t.Fatalf("review summary: %s", body)
			}
		}
	}
	resp, body = v.do("POST", "/api/s/"+token+"/comments", map[string]any{"path": "/final.mp4", "body": "what budget?", "name": "Sam", "parentId": note.ID})
	expect(t, resp, body, 400)

	// A link that can't be viewed takes no comments either.
	_, drop := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme/renders",
		"canView": false, "canDownload": false, "canUpload": true, "canComment": true})
	resp, body = v.do("GET", "/api/s/"+drop+"/comments?path=/final.mp4", nil)
	expect(t, resp, body, 403)
}

func TestDropBoxIgnoresFolder(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme",
		"canView": false, "canDownload": false, "canUpload": true})
	pub := e.client(e.pub.URL)
	for _, dir := range []string{"/renders", "/does-not-exist"} {
		resp, body := pub.do("POST", "/api/s/"+token+"/uploads", map[string]any{"dir": dir, "name": "in.txt", "size": 1})
		expect(t, resp, body, 201)
		var up struct{ ID string }
		json.Unmarshal(body, &up)
		resp, body = pub.do("PATCH", "/api/s/"+token+"/uploads/"+up.ID, []byte("x"), "Upload-Offset", "0")
		expect(t, resp, body, 200)
		resp, body = pub.do("POST", "/api/s/"+token+"/uploads/"+up.ID+"/complete", nil)
		expect(t, resp, body, 200)
	}
	if m, _ := filepath.Glob(filepath.Join(e.storage, "projects/acme/in*.txt")); len(m) != 2 {
		t.Fatalf("uploads not at the top of the drop box: %v", m)
	}
}

func TestShareZipBackslashNames(t *testing.T) {
	e := newEnv(t)
	os.WriteFile(filepath.Join(e.storage, "projects/acme/renders", `a\..\..\evil.bat`), []byte("x"), 0o644)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme"})
	resp, body := e.client(e.pub.URL).do("GET", "/api/s/"+token+"/zip", nil)
	expect(t, resp, body, 200)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		if strings.Contains(f.Name, `\`) {
			t.Fatalf("zip entry %q", f.Name)
		}
		found = found || strings.HasSuffix(f.Name, "evil.bat")
	}
	if !found {
		t.Fatal("file missing from zip")
	}
}

func TestShareZipTooManyNames(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme"})
	q := strings.Repeat("&name=x", 5001)
	resp, body := e.client(e.pub.URL).do("GET", "/api/s/"+token+"/zip?path=/"+q, nil)
	expect(t, resp, body, 400)
}

func TestSharesListUnknownPlace(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme"})
	resp, body := c.do("GET", "/api/shares?place=nope&path=/", nil)
	expect(t, resp, body, http.StatusOK)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("got %s", body)
	}
}

func TestShareSymlinkCannotLeaveFolder(t *testing.T) {
	e := newEnv(t)
	if err := os.Symlink("../other", filepath.Join(e.storage, "projects/acme/peek")); err != nil {
		t.Skip("symlinks unsupported")
	}
	c := e.client(e.app.URL)
	c.login("admin")
	_, token := createShare(t, c, map[string]any{"place": adminPlace, "path": "/projects/acme"})
	pub := e.client(e.pub.URL)
	resp, body := pub.do("GET", "/api/s/"+token+"/download?path="+url.QueryEscape("/peek/private.txt"), nil)
	if resp.StatusCode == 200 {
		t.Fatalf("read another folder through a symlink: %s", body)
	}
	resp, body = pub.do("GET", "/api/s/"+token+"/list?path=/", nil)
	expect(t, resp, body, 200)
	if strings.Contains(string(body), "peek") {
		t.Fatalf("escaping symlink listed: %s", body)
	}
	resp, _ = pub.do("GET", "/api/s/"+token+"/zip?name=peek", nil)
	if resp.StatusCode == 200 {
		t.Fatal("zipped through a symlink")
	}
	resp, body = pub.do("GET", "/api/s/"+token+"/zip", nil)
	expect(t, resp, body, 200)
	if bytes.Contains(body, []byte("private")) {
		t.Fatal("folder zip followed an escaping symlink")
	}
}
