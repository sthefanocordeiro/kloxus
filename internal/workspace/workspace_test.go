package workspace

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		t.Fatal("cookie shape does not carry the workspace id prefix")
	}
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("Cookie", strings.Split(cookie, ";")[0])
	if got := m.cookieID(req2); got != id {
		t.Fatal("cookieID did not round-trip the workspace id")
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
			t.Fatal("cookieID accepted a forged or malformed cookie")
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
		if ok, _ := m.allowCreate("10.0.0.1|fp"); !ok {
			t.Fatalf("create %d rejected early", i)
		}
	}
	if ok, retry := m.allowCreate("10.0.0.1|fp"); ok {
		t.Fatal("rate limit not enforced")
	} else if retry <= 0 || retry > time.Hour {
		t.Fatalf("retryAfter = %v, want (0, 1h]", retry)
	}
	// a different (IP, fingerprint) bucket is unaffected
	if ok, _ := m.allowCreate("10.0.0.2|fp"); !ok {
		t.Fatal("unrelated bucket rate-limited")
	}
	// stale entries age out of the window
	m.creates["10.0.0.1|fp"] = []time.Time{time.Now().Add(-2 * time.Hour)}
	if ok, _ := m.allowCreate("10.0.0.1|fp"); !ok {
		t.Fatal("stale window still blocking")
	}
}

// TestAllowCreateCompositeKey: the bucket is IP+fingerprint, not IP
// alone — visitors sharing a CGNAT address keep independent limits
// (KLOX-193), and retryAfter is the window's oldest entry expiring.
func TestAllowCreateCompositeKey(t *testing.T) {
	m := testMgr()
	m.SetCreateLimit(2)
	ok, _ := m.allowCreate("10.0.0.9|ua-a")
	ok, _ = m.allowCreate("10.0.0.9|ua-a")
	_ = ok
	if ok, _ := m.allowCreate("10.0.0.9|ua-a"); ok {
		t.Fatal("third create on the same (IP, fingerprint) allowed")
	}
	// same IP, another browser fingerprint: independent allowance
	if ok, _ := m.allowCreate("10.0.0.9|ua-b"); !ok {
		t.Fatal("same IP with a different fingerprint was rate-limited")
	}
	// same fingerprint, another IP: independent allowance
	if ok, _ := m.allowCreate("10.0.0.10|ua-a"); !ok {
		t.Fatal("same fingerprint on another IP was rate-limited")
	}
	// retryAfter tracks when the oldest create leaves the window
	m.creates["10.0.0.9|ua-a"] = []time.Time{time.Now().Add(-30 * time.Minute), time.Now()}
	if _, retry := m.allowCreate("10.0.0.9|ua-a"); retry < 29*time.Minute || retry > 31*time.Minute {
		t.Fatalf("retryAfter = %v, want ~30m", retry)
	}
}

// TestCreateKeySplitsBrowsers: two browsers behind one address must
// not share a rate-limit bucket (KLOX-193).
func TestCreateKeySplitsBrowsers(t *testing.T) {
	mk := func(ua, lang string) *http.Request {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Fly-Client-IP", "198.51.100.77")
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept-Language", lang)
		return req
	}
	a := createKey(mk("Mozilla/5.0 Chrome/131", "pt-BR"))
	b := createKey(mk("Mozilla/5.0 Firefox/133", "pt-BR"))
	c := createKey(mk("Mozilla/5.0 Chrome/131", "en-US"))
	if a == b || a == c {
		t.Fatal("distinct browser fingerprints collapsed into one bucket")
	}
	if a != createKey(mk("Mozilla/5.0 Chrome/131", "pt-BR")) {
		t.Fatal("same request produced two buckets")
	}
}

func TestSetCreateLimit(t *testing.T) {
	m := testMgr()
	m.SetCreateLimit(3)
	for i := 0; i < 3; i++ {
		if ok, _ := m.allowCreate("10.0.0.5|fp"); !ok {
			t.Fatalf("create %d rejected early", i)
		}
	}
	if ok, _ := m.allowCreate("10.0.0.5|fp"); ok {
		t.Fatal("custom limit not enforced")
	}
	m.SetCreateLimit(0)
	if m.createLimit != 3 {
		t.Fatal("non-positive limit must keep the current value")
	}
}

// A fresh KLOXUS_DATA_DIR (clean checkout, new volume) has no parent
// directory yet — the first boot must create it, not fail.
func TestLoadOrCreateSecretCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "cookie.secret")
	s1, err := LoadOrCreateSecret(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	s2, err := LoadOrCreateSecret(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if string(s1) != string(s2) {
		t.Fatal("secret not persisted across loads")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("secret file missing: %v", err)
	}
}
