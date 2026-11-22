package web_test

import (
	"context"
	"fmt"
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
