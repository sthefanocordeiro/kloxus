package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/testdb"
	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// KLOX-127: the public waitlist endpoint stores one row per address,
// answers identically for new and known emails, validates input and
// rate-limits per IP.
func TestWaitlist(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Pool(t)
	srv, _, _, _ := demoServerOpt(t, func(d *web.Deps) { d.Pool = pool })
	email := fmt.Sprintf("wl-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() { pool.ExecWaitlistCleanup(ctx, email) })
	before, err := pool.WaitlistCount(ctx)
	if err != nil {
		t.Fatalf("WaitlistCount: %v", err)
	}

	r1 := apiCall(t, srv, "POST", "/api/v1/waitlist", map[string]string{"email": email, "source": "landing"})
	if r1.Code != 200 {
		t.Fatalf("first signup = %d %s", r1.Code, r1.Body)
	}
	r2 := apiCall(t, srv, "POST", "/api/v1/waitlist", map[string]string{"email": " " + email + " ", "source": "landing"})
	if r2.Code != 200 || r2.Body.String() != r1.Body.String() {
		t.Fatalf("repeat signup = %d %q, want identical to %q", r2.Code, r2.Body, r1.Body)
	}
	if n, _ := pool.WaitlistCount(ctx); n != before+1 {
		t.Fatalf("count = %d, want %d", n, before+1)
	}
	if rr := apiCall(t, srv, "POST", "/api/v1/waitlist", map[string]string{"email": "not-an-email"}); rr.Code != 400 {
		t.Fatalf("invalid email = %d, want 400", rr.Code)
	}
}

// KLOX-127: the limiter keys on Fly-Client-IP, answers 429 on the 11th
// request in the hour, and does not throttle a different client; the
// Content-Type guard answers 415.
func TestWaitlistRateLimitAndGuards(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Pool(t)
	srv, _, _, _ := demoServerOpt(t, func(d *web.Deps) { d.Pool = pool })
	email := fmt.Sprintf("wl-rl-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() { pool.ExecWaitlistCleanup(ctx, email) })
	hdr := func(ip string) http.Header { return http.Header{"Fly-Client-Ip": {ip}} }
	body := map[string]string{"email": email, "source": "landing"}

	for i := 1; i <= 10; i++ {
		if rr := apiCallHdr(t, srv, "POST", "/api/v1/waitlist", body, hdr("203.0.113.7")); rr.Code != 200 {
			t.Fatalf("request %d = %d %s, want 200", i, rr.Code, rr.Body)
		}
	}
	if rr := apiCallHdr(t, srv, "POST", "/api/v1/waitlist", body, hdr("203.0.113.7")); rr.Code != 429 {
		t.Fatalf("11th request = %d, want 429", rr.Code)
	}
	if rr := apiCallHdr(t, srv, "POST", "/api/v1/waitlist", body, hdr("203.0.113.8")); rr.Code != 200 {
		t.Fatalf("other client = %d, want 200", rr.Code)
	}

	req := httptest.NewRequest("POST", "/api/v1/waitlist", strings.NewReader(`{"email":"a@example.com"}`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 415 {
		t.Fatalf("non-JSON content type = %d, want 415", rr.Code)
	}
}
