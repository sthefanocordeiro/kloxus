package web_test

// Spend-guard acceptance coverage (D32/D36): budget refusal, kill
// switch without restart, provider outage/malformed JSON never a 500.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// guardServer is demoServer plus a Postgres-backed spend guard. The
// persisted kill switch is cleared first — app_settings is shared state
// and a leftover kill from another test would block every run here.
func guardServer(t *testing.T, opt func(*web.Deps)) (*web.Server, *store.DB, *store.Publication, *llm.Guard, *store.Pool) {
	t.Helper()
	pool := testdb.Pool(t)
	g := &llm.Guard{Store: pool}
	if err := g.SetKill(context.Background(), false); err != nil {
		t.Fatalf("clear kill: %v", err)
	}
	srv, db, pub, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Guard = g
		if opt != nil {
			opt(d)
		}
	})
	return srv, db, pub, g, pool
}

// postRun triggers a prepare run and returns its final view.
func postRun(t *testing.T, srv *web.Server, pubID int64) map[string]any {
	t.Helper()
	rr := apiCall(t, srv, "POST", "/api/v1/publications/"+strconv.FormatInt(pubID, 10)+"/runs", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST run = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	runID := apiJSON(t, rr)["run"].(map[string]any)["id"].(string)
	return waitRun(t, srv, runID)
}

// TestRunBudgetRefusal: the run past the workspace's daily budget is
// refused cleanly — blocked status, sample flag, no 500 (D36). The
// ledger is shared across test invocations, so the budget is set
// relative to the runs already recorded today.
func TestRunBudgetRefusal(t *testing.T) {
	pool := testdb.Pool(t)
	ctx := context.Background()
	today := time.Now().UTC().Format("2006-01-02") + " 00:00:00"
	used, err := pool.LLMRunsSince(ctx, "local", today)
	if err != nil {
		t.Fatalf("LLMRunsSince: %v", err)
	}
	srv, _, pub, g, _ := guardServer(t, func(d *web.Deps) {
		d.Cfg.RunsPerDay = used + 1
	})
	g.RunsPerDay = used + 1

	if run := postRun(t, srv, pub.ID); run["status"] != "done" {
		t.Fatalf("first run = %v (%v)", run["status"], run["error"])
	}
	run := postRun(t, srv, pub.ID)
	if run["status"] != "blocked" || run["blocked"] != true {
		t.Fatalf("run=%v, want blocked", run)
	}
	if run["blocked_reason"] != string(llm.ReasonWorkspaceRuns) {
		t.Fatalf("blocked_reason=%v", run["blocked_reason"])
	}
	if run["sample"] != true {
		t.Fatal("blocked run should offer the recorded sample")
	}
	if msg, _ := run["error"].(string); msg == "" {
		t.Fatal("blocked run has no creator-facing message")
	}
}

// TestDailyCapRefusal: a ledger already at the cap blocks admission
// (D36). The seeded call is deliberately microscopic — llm_ledger is a
// shared production-shaped table and every call row counts toward the
// real global cap, so tests must leave almost nothing behind.
func TestDailyCapRefusal(t *testing.T) {
	srv, _, pub, g, pool := guardServer(t, func(d *web.Deps) {
		d.Cfg.LLMDailyCap = 0.0001
	})
	g.DailyCapUSD = 0.0001
	if err := pool.LLMCall(context.Background(), "other-ws", "claude-haiku-5-5", 1, 1, 0.0002); err != nil {
		t.Fatalf("seed spend: %v", err)
	}
	run := postRun(t, srv, pub.ID)
	if run["status"] != "blocked" || run["blocked_reason"] != string(llm.ReasonDailyCap) {
		t.Fatalf("run=%v, want daily-cap block", run)
	}
}

// TestKillSwitchNoRestart: flipping the admin kill switch blocks the
// very next run without a process restart (D36).
func TestKillSwitchNoRestart(t *testing.T) {
	token := "test-admin-token"
	srv, _, pub, g, _ := guardServer(t, func(d *web.Deps) {
		d.Cfg.AdminToken = token
	})
	g.RunsPerDay = 0 // unlimited
	call := func(method, path string, body any) map[string]any {
		req := httptest.NewRequest(method, path, nil)
		if body != nil {
			b, _ := json.Marshal(body)
			req = httptest.NewRequest(method, path, strings.NewReader(string(b)))
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("%s %s = %d: %s", method, path, rr.Code, rr.Body.String()[:300])
		}
		return apiJSON(t, rr)
	}

	state := call("GET", "/api/v1/admin/llm", nil)
	if state["kill"] == true {
		t.Fatal("kill switch already on")
	}
	call("POST", "/api/v1/admin/llm", map[string]any{"kill": true})

	run := postRun(t, srv, pub.ID)
	if run["status"] != "blocked" || run["blocked_reason"] != string(llm.ReasonKillSwitch) {
		t.Fatalf("run=%v, want kill-switch block", run)
	}
	// Mid-run safety: provider calls check live kill state too — covered
	// by guard unit tests; here also verify resume works live.
	call("POST", "/api/v1/admin/llm", map[string]any{"kill": false})
	if run := postRun(t, srv, pub.ID); run["status"] != "done" {
		t.Fatalf("run after resume = %v (%v)", run["status"], run["error"])
	}
}

// TestKillSwitchNoAuth: admin routes need the bearer token.
func TestKillSwitchNoAuth(t *testing.T) {
	srv, _, _, _, _ := guardServer(t, func(d *web.Deps) {
		d.Cfg.AdminToken = "right-token"
	})
	for _, hdr := range []string{"", "Bearer wrong-token"} {
		req := httptest.NewRequest("GET", "/api/v1/admin/llm", nil)
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code == 200 {
			t.Fatalf("admin access with %q succeeded", hdr)
		}
	}
	// Admin routes must not mint a workspace cookie.
	req := httptest.NewRequest("GET", "/api/v1/admin/llm", nil)
	req.Header.Set("Authorization", "Bearer right-token")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Header().Get("Set-Cookie") != "" {
		t.Fatal("admin route set a workspace cookie")
	}
}

// TestProviderOutageNoServerError: a permanently failing provider fails
// the run cleanly — status failed, issue blocked, never a 500.
type failingProvider struct{ err error }

func (f failingProvider) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, f.err
}

func TestProviderOutageNoServerError(t *testing.T) {
	srv, db, pub, _, _ := guardServer(t, func(d *web.Deps) {
		d.LLM = failingProvider{err: errors.New("anthropic: 503 overloaded")}
	})
	run := postRun(t, srv, pub.ID)
	if run["status"] != "failed" {
		t.Fatalf("run=%v, want failed", run)
	}
	iss, err := db.LatestIssue(context.Background(), pub.ID)
	if err != nil {
		t.Fatalf("LatestIssue: %v", err)
	}
	if iss.Status != "blocked" {
		t.Fatalf("issue status=%q, want blocked", iss.Status)
	}
}

// textProvider always answers with fixed text.
type textProvider struct{ text string }

func (p textProvider) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Text: p.text, InputTokens: 1, OutputTokens: 1}, nil
}

// TestMalformedJSONNoServerError: a provider returning garbage fails the
// run cleanly after its repair retry.
func TestMalformedJSONNoServerError(t *testing.T) {
	srv, _, pub, _, _ := guardServer(t, func(d *web.Deps) {
		d.LLM = textProvider{text: "not json at all"}
	})
	run := postRun(t, srv, pub.ID)
	if run["status"] != "failed" {
		t.Fatalf("run=%v, want failed, got %v", run["status"], run)
	}
	errMsg, _ := run["error"].(string)
	if errMsg == "" {
		t.Fatal("failed run has no error message")
	}
}

// TestBlockedRunSample: the run resource a blocked run returns carries
// the recorded sample path (frontend renders it instead of an error).
func TestBlockedRunSample(t *testing.T) {
	srv, _, pub, g, _ := guardServer(t, nil)
	if err := g.SetKill(context.Background(), true); err != nil {
		t.Fatalf("SetKill: %v", err)
	}
	run := postRun(t, srv, pub.ID)
	if run["sample"] != true {
		t.Fatalf("blocked run sample=%v", run["sample"])
	}
	// The sample endpoint serves the recorded issue without a workspace.
	rr := apiCall(t, srv, "GET", "/api/v1/sample", nil)
	if rr.Code != 200 {
		t.Fatalf("sample = %d", rr.Code)
	}
}

// TestRunScopeTokenBudget: a provider wrapped in a tiny token budget is
// cut mid-run with the token-budget reason — spend stays bounded. The
// wrapping guard uses in-memory accounting so replay fixture costs never
// reach the shared ledger (they are not real spend).
func TestRunScopeTokenBudget(t *testing.T) {
	srv, _, pub, g, _ := guardServer(t, func(d *web.Deps) {
		// Wrap whatever provider the demo uses so calls run the guard.
		d.LLM = &llm.Guard{TokensPerRun: 10, Inner: d.LLM}
	})
	g.RunsPerDay = 0
	run := postRun(t, srv, pub.ID)
	if run["status"] != "blocked" || run["blocked_reason"] != string(llm.ReasonWorkspaceToken) {
		t.Fatalf("run=%v, want token-budget block", run)
	}
}

// TestGuardNowInjected: the guard's clock is injectable for day-boundary
// tests (unit coverage lives in llm/guard_test.go; here we just check
// plumbing).
func TestGuardNowInjected(t *testing.T) {
	fixed := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	g := &llm.Guard{Store: testdb.Pool(t), RunsPerDay: 0, Now: func() time.Time { return fixed }}
	ctx := context.Background()
	if err := g.SetKill(ctx, false); err != nil {
		t.Fatalf("clear kill: %v", err)
	}
	if err := g.AdmitRun(ctx, "clock-ws"); err != nil {
		t.Fatalf("admit: %v", err)
	}
	_ = fmt.Sprint(g) // keep fmt import used if assertions above change
}
