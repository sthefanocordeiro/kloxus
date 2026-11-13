package workspace

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testMgr() *Manager {
	return &Manager{
		secret:   []byte("test-secret-key-32-bytes-padded-ok!"),
		entries:  map[string]*Workspace{},
		lastUse:  map[string]time.Time{},
		lastSeen: map[string]time.Time{},
		creates:  map[string][]time.Time{},
	}
}

func TestCookieRoundtrip(t *testing.T) {
	m := testMgr()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	id := NewID()
	m.SetCookie(rr, req, id)
	cookie := rr.Header().Get("Set-Cookie")
	if !strings.HasPrefix(cookie, CookieName+"="+id+".") {
		t.Fatalf("cookie shape: %q", cookie)
	}
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("Cookie", strings.Split(cookie, ";")[0])
	if got := m.cookieID(req2); got != id {
		t.Fatalf("cookieID = %q, want %q", got, id)
	}
}

func TestCookieRejectsForgery(t *testing.T) {
	m := testMgr()
	id := NewID()
	for _, bad := range []string{
		id + "." + strings.Repeat("0", 32),  // bad signature
		"../../etc/passwd.deadbeefdeadbeef", // path-shaped garbage
		id,                                  // no signature
		"",                                  // empty
		strings.ToUpper(id) + ".x",          // wrong case id
	} {
		req := httptest.NewRequest("GET", "/", nil)
		if bad != "" {
			req.Header.Set("Cookie", CookieName+"="+bad)
		}
		if got := m.cookieID(req); got != "" {
			t.Fatalf("cookieID(%q) = %q, want reject", bad, got)
		}
	}
}

func TestPublicTokenRoundtrip(t *testing.T) {
	m := testMgr()
	id := NewID()
	tok := m.PublicToken(id)
	if tok == "" {
		t.Fatal("empty token")
	}
	if strings.Contains(tok, id) {
		t.Fatal("token leaks the workspace id")
	}
	if got := m.ResolvePublicToken(tok); got != id {
		t.Fatalf("ResolvePublicToken = %q, want %q", got, id)
	}
	// forged / truncated tokens never resolve
	for _, bad := range []string{tok[:len(tok)-4] + "AAAA", "bogus", ""} {
		if got := m.ResolvePublicToken(bad); got != "" {
			t.Fatalf("ResolvePublicToken(%q) = %q, want reject", bad, got)
		}
	}
	// a token sealed under another key does not resolve
	other := testMgr()
	other.secret = []byte("different-secret-key-32-bytes-ok!!")
	if got := other.ResolvePublicToken(tok); got != "" {
		t.Fatalf("cross-key token resolved to %q", got)
	}
}

func TestClientIPPrefersFlyHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.Header.Set("Fly-Client-IP", "198.51.100.77")
	if ip := clientIP(req); ip != "198.51.100.77" {
		t.Fatalf("clientIP = %q, want Fly-Client-IP", ip)
	}
	// XFF spoofing cannot change the rate-limit bucket on Fly, and falls
	// back to first-hop off Fly.
	req.Header.Del("Fly-Client-IP")
	if ip := clientIP(req); ip != "198.51.100.1" {
		t.Fatalf("clientIP fallback = %q", ip)
	}
}

func TestAllowCreateBounded(t *testing.T) {
	m := testMgr()
	for i := 0; i < createsPerHour; i++ {
		if !m.allowCreate("10.0.0.1") {
			t.Fatalf("create %d rejected early", i)
		}
	}
	if m.allowCreate("10.0.0.1") {
		t.Fatal("rate limit not enforced")
	}
	// a different IP is unaffected
	if !m.allowCreate("10.0.0.2") {
		t.Fatal("unrelated IP rate-limited")
	}
	// stale entries age out of the window
	m.creates["10.0.0.1"] = []time.Time{time.Now().Add(-2 * time.Hour)}
	if !m.allowCreate("10.0.0.1") {
		t.Fatal("stale window still blocking")
	}
}
