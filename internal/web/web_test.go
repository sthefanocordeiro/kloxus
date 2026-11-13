package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
	"github.com/sthefanocordeiro/kloxus/internal/web"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// demoServer seeds the example publication and returns the server plus its
// collaborators. The whole test runs offline on replayed model calls.
func demoServer(t *testing.T) (*web.Server, *store.DB, *store.Publication, *config.Config) {
	t.Helper()
	ctx := context.Background()
	base := t.TempDir()
	cfg := &config.Config{
		Mode: "demo", Port: 8080,
		OutDir:      filepath.Join(base, "out"),
		ModelTriage: "claude-haiku-5-5", ModelDraft: "claude-sonnet-5-5", MaxDeepDives: 3,
	}
	db := testdb.WS(t)

	seed := func(ctx context.Context, sdb *store.DB) error {
		data, err := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		if err != nil {
			return err
		}
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		_, err = sdb.SeedPublication(ctx, def)
		return err
	}
	if err := seed(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pubs, err := db.ListPublications(ctx)
	if err != nil || len(pubs) == 0 {
		t.Fatalf("ListPublications: %v", err)
	}
	pub := pubs[0]

	srv := web.New(web.Deps{
		WS: workspace.OpenLocal(db, seed), Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm", DumpDir: filepath.Join(cfg.OutDir, "llm-dump")},
		Monitor: sources.FakeMonitor{},
		Seed:    seed,
	})
	return srv, db, pub, cfg
}

// TestDemoIssueLifecycle exercises the whole demo flow through the JSON
// API: Phase A run, review decisions, draft, blockers, approval,
// exports, reader gating and outbox send.
func TestDemoIssueLifecycle(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)

	if rr := apiCall(t, srv, "GET", "/healthz", nil); rr.Code != 200 {
		t.Fatalf("healthz = %d", rr.Code)
	}

	// --- Phase A ---
	issueID := runPhaseA(t, srv, db, pub.ID)
	iis, err := db.IssueItems(ctx, issueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	sort.Slice(iis, func(a, b int) bool { return iis[a].ID < iis[b].ID })
	if len(iis) != 8 {
		t.Fatalf("issue items = %d, want 8", len(iis))
	}
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	// --- review decisions ---
	decide := func(iid int64, kind, reason string) {
		body := map[string]string{"kind": kind}
		if reason != "" {
			body["reason"] = reason
		}
		rr := apiCall(t, srv, "POST", issueURL+"/items/"+fmt.Sprint(iid)+"/decisions", body)
		if rr.Code != 200 {
			t.Fatalf("decision %s on item %d = %d %s", kind, iid, rr.Code, rr.Body.String()[:300])
		}
	}
	decide(iis[1].ID, "reject", "off the publication's angle")
	decide(iis[2].ID, "reject", "too thin for this issue")
	decide(iis[5].ID, "reject", "duplicates a stronger item")
	decide(iis[7].ID, "reject", "prioritize policy with direct Brazil impact")
	decide(iis[6].ID, "alternative", "")

	// approve the kept items, including the alternative's replacement
	iis2, err := db.IssueItems(ctx, issueID)
	if err != nil {
		t.Fatalf("IssueItems after alternative: %v", err)
	}
	pending := 0
	for _, ii := range iis2 {
		if ii.Status == "pending" {
			pending++
			decide(ii.ID, "approve", "")
		}
	}
	if pending != 4 {
		t.Fatalf("pending items after decisions = %d, want 4", pending)
	}

	// --- draft (twice: replay is idempotent) ---
	for i := 0; i < 2; i++ {
		rr := apiCall(t, srv, "POST", issueURL+"/draft", nil)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("POST draft #%d = %d %s", i+1, rr.Code, rr.Body.String()[:300])
		}
		waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))
	}
	if rd := apiGet(t, srv, issueURL+"/readiness"); rd["ready"].(bool) {
		t.Fatal("issue should not be ready — required commentary + flag(s) unresolved")
	}

	// --- clear blockers ---
	ver := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["version"].(string)
	rr := apiCall(t, srv, "PUT", issueURL+"/commentary", map[string]string{
		"section": "The big one", "text": "My editorial take on the regulator consultation.", "version": ver,
	})
	if rr.Code != 200 {
		t.Fatalf("PUT commentary = %d", rr.Code)
	}
	dbg, _ := db.GetIssue(ctx, issueID)
	if len(dbg.Flags) == 0 {
		t.Fatal("expected at least one citation flag from the fixture citecheck")
	}
	sents := issueURL + "/sentences/"
	for len(dbg.Flags) > 0 {
		doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
		next := ""
	outer:
		for _, s := range doc["sections"].([]any) {
			for _, i := range s.(map[string]any)["items"].([]any) {
				for _, sn := range i.(map[string]any)["sentences"].([]any) {
					if sn.(map[string]any)["flag"] != nil {
						next = sn.(map[string]any)["id"].(string)
						break outer
					}
				}
			}
		}
		if next == "" {
			t.Fatalf("flags remain (%d) but no flagged sentence found", len(dbg.Flags))
		}
		if rr := apiCall(t, srv, "DELETE", sents+next,
			map[string]any{"version": doc["version"].(string)}); rr.Code != 200 {
			t.Fatalf("remove flagged sentence %s = %d: %s", next, rr.Code, rr.Body.String()[:200])
		}
		dbg, _ = db.GetIssue(ctx, issueID)
	}
	if rd := apiGet(t, srv, issueURL+"/readiness"); !rd["ready"].(bool) {
		t.Fatalf("blockers should be cleared: %v", rd["checks"])
	}

	// --- approve, export, send ---
	if rr := apiCall(t, srv, "POST", issueURL+"/approve", nil); rr.Code != 200 {
		t.Fatalf("POST approve = %d %s", rr.Code, rr.Body.String()[:300])
	}
	if rr := apiCall(t, srv, "GET", issueURL+"/export?fmt=markdown", nil); rr.Code != 200 || !strings.Contains(rr.Body.String(), "# Futuro Próximo") {
		t.Fatal("markdown export failed")
	}
	if rr := apiCall(t, srv, "GET", issueURL+"/export?fmt=html", nil); rr.Code != 200 || !strings.Contains(rr.Body.String(), "<") {
		t.Fatal("html export failed")
	}
	g := apiGet(t, srv, "/api/v1/publications/"+fmt.Sprint(pub.ID)+"/guide")
	if !strings.Contains(g["guide"].(map[string]any)["content"].(string), "rioritize policy with direct Brazil impact") {
		t.Fatal("guide missing recorded rejection reason")
	}

	// --- reader gating ---
	pv := apiGet(t, srv, issueURL+"/preview?reader=non-subscriber")
	if !pv["gated"].(bool) || !strings.Contains(pv["body_md"].(string), "Exclusivo para assinantes") {
		t.Fatal("non-subscriber preview should gate paid sections")
	}
	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+fmt.Sprint(pub.ID)+"/paywall/simulate", nil)
	if v := apiJSON(t, rr); !v["subscribed"].(bool) {
		t.Fatalf("paywall simulate = %v", v)
	}
	pv = apiGet(t, srv, issueURL+"/preview?reader=subscriber")
	if strings.Contains(pv["body_md"].(string), "Exclusivo para assinantes") {
		t.Fatal("subscriber preview should be unlocked after simulated payment")
	}

	// a test send records the outbox email but leaves the issue approved
	if rr := apiCall(t, srv, "POST", issueURL+"/send", map[string]string{"audience": "test"}); rr.Code != 200 {
		t.Fatalf("POST test send = %d %s", rr.Code, rr.Body.String()[:300])
	}
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "approved" {
		t.Fatalf("test send marked issue %q — it must stay approved", iss.Status)
	}
	// the readers send then marks it sent
	if rr := apiCall(t, srv, "POST", issueURL+"/send", map[string]string{"audience": "readers"}); rr.Code != 200 {
		t.Fatalf("POST send = %d %s", rr.Code, rr.Body.String()[:300])
	}
	iss, err = db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "sent" {
		t.Fatalf("issue status = %q, want sent", iss.Status)
	}
}

// TestSourceAddValidation covers the demo-mode simulation (visitor URLs
// are stored but never fetched) and the real-mode URL validation.
func TestSourceAddValidation(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	base := "/api/v1/publications/" + fmt.Sprint(pub.ID) + "/sources"
	add := func(typ, u string) *httptest.ResponseRecorder {
		return apiCall(t, srv, "POST", base, map[string]string{"type": typ, "url": u})
	}
	if rr := add("rss", "https://example.com/feed.xml"); rr.Code != 201 ||
		!strings.Contains(apiJSON(t, rr)["note"].(string), "not fetched") {
		t.Fatalf("demo mode should accept but simulate non-fixture sources (code %d)", rr.Code)
	}
	if rr := add("rss", "notaurl"); apiErrCode(t, rr) != "invalid_source" {
		t.Fatal("demo mode should reject malformed URLs")
	}
	if rr := add("rss", "fixtures/feeds/radar-tech.xml"); rr.Code != 201 {
		t.Fatalf("fixture source add = %d", rr.Code)
	}

	// real mode validates URL syntax without fetching
	srv2, _, pub2, cfg := demoServer(t)
	cfg.Mode = "real"
	base2 := "/api/v1/publications/" + fmt.Sprint(pub2.ID) + "/sources"
	add2 := func(typ, u string) *httptest.ResponseRecorder {
		return apiCall(t, srv2, "POST", base2, map[string]string{"type": typ, "url": u})
	}
	if rr := add2("rss", "notaurl"); apiErrCode(t, rr) != "invalid_source" {
		t.Fatal("real mode should reject malformed source URLs")
	}
	if rr := add2("rss", "ftp://example.com/feed"); apiErrCode(t, rr) != "invalid_source" {
		t.Fatal("real mode should reject non-http(s) schemes")
	}
	if rr := add2("page", "https://example.com/news"); rr.Code != 201 {
		t.Fatalf("valid real-mode source add = %d", rr.Code)
	}
}

// TestWebhookAuth covers the constant-time secret check and real-mode
// fail-closed behavior.
func TestWebhookAuth(t *testing.T) {
	srv, _, _, cfg := demoServer(t)
	cfg.WebhookSecret = "s3cret"
	webhook := func(auth, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/webhooks/firecrawl", strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr
	}
	if rr := webhook("", `{"type":"other"}`); rr.Code != 401 {
		t.Fatalf("no auth = %d, want 401", rr.Code)
	}
	if rr := webhook("Bearer wrong", `{"type":"other"}`); rr.Code != 401 {
		t.Fatalf("wrong secret = %d, want 401", rr.Code)
	}
	if rr := webhook("Bearer s3cret", `{"type":"other"}`); rr.Code != 204 {
		t.Fatalf("valid secret = %d, want 204", rr.Code)
	}

	// real mode with no secret configured fails closed
	srv2, _, _, cfg2 := demoServer(t)
	cfg2.Mode = "real"
	cfg2.WebhookSecret = ""
	req := httptest.NewRequest("POST", "/webhooks/firecrawl", strings.NewReader(`{"type":"other"}`))
	rr := httptest.NewRecorder()
	srv2.ServeHTTP(rr, req)
	if rr.Code != 503 {
		t.Fatalf("real mode without secret = %d, want 503", rr.Code)
	}
}
