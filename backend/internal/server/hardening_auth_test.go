package server

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/security"
)

func trustLoopback(e *env) {
	loop := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	e.srv.cfg.TrustedProxies, e.srv.cfg.AppTrustedProxies = loop, loop
}

// A trusted proxy that is not Cloudflare passes a client's CF-Connecting-IP
// through untouched, so X-Forwarded-For must win.
func TestRealIPPrefersForwardedFor(t *testing.T) {
	e := newEnv(t)
	trustLoopback(e)
	var got netip.Addr
	h := e.srv.realIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = clientIP(r) }), e.srv.cfg.IsTrustedProxy)
	try := func(hdr ...string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:5555"
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Add(hdr[i], hdr[i+1])
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		return got.String()
	}
	if ip := try("CF-Connecting-IP", "10.0.0.5", "X-Forwarded-For", "203.0.113.7"); ip != "203.0.113.7" {
		t.Errorf("spoofed CF-Connecting-IP won over X-Forwarded-For: %s", ip)
	}
	if ip := try("X-Forwarded-For", "10.0.0.5", "X-Forwarded-For", "203.0.113.7"); ip != "203.0.113.7" {
		t.Errorf("client-supplied X-Forwarded-For line won over the proxy's: %s", ip)
	}
	if ip := try("X-Forwarded-For", "10.0.0.5, 203.0.113.7, 127.0.0.1"); ip != "203.0.113.7" {
		t.Errorf("hop walk: %s", ip)
	}
	if ip := try("CF-Connecting-IP", "198.51.100.4"); ip != "198.51.100.4" {
		t.Errorf("CF-Connecting-IP fallback: %s", ip)
	}
	// Untrusted peers can set nothing.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:5555"
	r.Header.Set("CF-Connecting-IP", "10.0.0.5")
	r.Header.Set("X-Forwarded-For", "10.0.0.5")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got.String() != "192.0.2.1" {
		t.Errorf("untrusted peer set its address: %s", got)
	}
}

func TestAppNetworksNotBypassedWithCFHeader(t *testing.T) {
	e := newEnv(t)
	trustLoopback(e)
	e.srv.cfg.AppNets = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	c := e.client(e.app.URL)
	publicOnly := func(hdr ...string) bool {
		resp, body := c.do("GET", "/api/config", nil, hdr...)
		expect(t, resp, body, 200)
		var cfg struct{ PublicOnly bool }
		json.Unmarshal(body, &cfg)
		return cfg.PublicOnly
	}
	if !publicOnly("CF-Connecting-IP", "10.0.0.5", "X-Forwarded-For", "203.0.113.7") {
		t.Fatal("internet client reached the full app with a forged CF-Connecting-IP")
	}
	if publicOnly("X-Forwarded-For", "10.0.0.5") {
		t.Fatal("LAN client behind the proxy was refused the app")
	}
}

// Over HTTPS only the __Host- cookie counts, so a plain cookie planted over
// HTTP (or by a sibling host) cannot sign the browser into another account.
func TestPlainCookieIgnoredOverHTTPS(t *testing.T) {
	e := newEnv(t)
	trustLoopback(e)
	c := e.client(e.app.URL)
	c.login("admin") // plain HTTP: gets coffer_sid
	resp, body := c.do("GET", "/api/me", nil)
	expect(t, resp, body, 200)
	resp, body = c.do("GET", "/api/me", nil, "X-Forwarded-Proto", "https")
	expect(t, resp, body, 401)
}

func TestLoginReplacesBrowserSession(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	c.login("admin")
	resp, body := c.do("GET", "/api/me/sessions", nil)
	expect(t, resp, body, 200)
	var list []map[string]any
	json.Unmarshal(body, &list)
	if len(list) != 1 {
		t.Fatalf("previous session left behind: %d sessions", len(list))
	}
}

func hotp(secret string, step int64) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

// Parallel sign-ins with the same code must not all get in.
func TestTOTPCodeSingleUseUnderRace(t *testing.T) {
	e := newEnv(t)
	u, _ := e.store.UserByName(t.Context(), "admin")
	secret := security.NewTOTPSecret()
	u.TOTPSecret, u.TOTPEnabled = e.srv.keys.Encrypt([]byte(secret)), true
	if err := e.store.SetTOTP(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	code := hotp(secret, time.Now().Unix()/30+1) // valid for the next 30 s at least
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 5 {
		wg.Go(func() {
			resp, _ := e.client(e.app.URL).do("POST", "/api/auth/login",
				map[string]string{"username": "admin", "password": "correct horse battery", "code": code})
			if resp.StatusCode == 200 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("one code signed in %d times", ok)
	}
}

// A failed admin edit must not wipe the user's recovery codes.
func TestResetTOTPKeepsCodesOnFailedEdit(t *testing.T) {
	e := newEnv(t)
	bob, _ := e.addUser("bob", "user", "projects")
	if _, err := e.srv.newRecoveryCodes(t.Context(), bob); err != nil {
		t.Fatal(err)
	}
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("PATCH", fmt.Sprintf("/api/admin/users/%d", bob.ID), map[string]any{"resetTotp": true, "username": "admin"})
	expect(t, resp, body, 409)
	if n, _ := e.store.RecoveryCodesLeft(t.Context(), bob.ID); n != 10 {
		t.Fatalf("recovery codes wiped by a rejected edit: %d left", n)
	}
	resp, body = c.do("PATCH", fmt.Sprintf("/api/admin/users/%d", bob.ID), map[string]any{"resetTotp": true})
	expect(t, resp, body, 200)
	if n, _ := e.store.RecoveryCodesLeft(t.Context(), bob.ID); n != 0 {
		t.Fatalf("reset left %d recovery codes", n)
	}
}

// Self-service writes must not put back columns an admin changed meanwhile.
func TestSelfServiceDoesNotRevertAdminChanges(t *testing.T) {
	e := newEnv(t)
	e.addUser("bob", "user", "projects")
	c := e.client(e.app.URL)
	c.login("bob")
	stale, _ := e.store.UserByName(t.Context(), "bob")
	// An admin disables bob while one of his requests holds the old row.
	fresh, _ := e.store.UserByName(t.Context(), "bob")
	fresh.Disabled = true
	e.store.UpdateUser(t.Context(), fresh)
	if err := e.store.SetPrefs(t.Context(), stale.ID, `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetTOTP(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.store.UserByID(t.Context(), stale.ID); !got.Disabled {
		t.Fatal("disable reverted")
	}
	resp, body := c.do("GET", "/api/me", nil)
	expect(t, resp, body, 401)
}

func TestPrefsMustBeObject(t *testing.T) {
	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("PUT", "/api/me/prefs", []byte("null"))
	expect(t, resp, body, 400)
	resp, body = c.do("PUT", "/api/me/prefs", map[string]any{"theme": "dark"})
	expect(t, resp, body, 200)
	resp, body = c.do("GET", "/api/me", nil)
	if !strings.Contains(string(body), `"prefs":{"theme":"dark"}`) {
		t.Fatalf("prefs not saved: %s", body)
	}
}
