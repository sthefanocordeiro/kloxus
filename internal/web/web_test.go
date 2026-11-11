package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
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
		DBPath: filepath.Join(base, "kloxus.db"), OutDir: filepath.Join(base, "out"),
		ModelTriage: "claude-haiku-5-5", ModelDraft: "claude-sonnet-5-5", MaxDeepDives: 3,
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

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

func call(t *testing.T, srv *web.Server, method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// TestDemoIssueLifecycle exercises the whole demo flow through the real
// HTTP handlers: Phase A run, review decisions, draft, blockers, approval,
// exports, reader gating and outbox send.
func TestDemoIssueLifecycle(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)

	if rr := call(t, srv, "GET", "/healthz", nil); rr.Code != 200 {
		t.Fatalf("healthz = %d", rr.Code)
	}

	// --- Phase A ---
	if rr := call(t, srv, "POST", "/pub/"+strconv.FormatInt(pub.ID, 10)+"/run", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST run = %d", rr.Code)
	}
	var st struct {
		Done    bool   `json:"done"`
		IssueID int64  `json:"issue_id"`
		Err     string `json:"error"`
	}
	statusURL := "/pub/" + strconv.FormatInt(pub.ID, 10) + "/run/status"
	deadline := time.Now().Add(30 * time.Second)
	for {
		rr := call(t, srv, "GET", statusURL, nil)
		if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
			t.Fatalf("run status decode: %v", err)
		}
		if st.Done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Err != "" || st.IssueID == 0 {
		t.Fatalf("run failed: %+v", st)
	}

	iis, err := db.IssueItems(ctx, st.IssueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	sort.Slice(iis, func(a, b int) bool { return iis[a].ID < iis[b].ID })
	if len(iis) != 8 {
		t.Fatalf("issue items = %d, want 8", len(iis))
	}
	issueURL := "/issue/" + strconv.FormatInt(st.IssueID, 10)

	// --- review decisions ---
	decide := func(iid int64, kind, reason string) {
		rr := call(t, srv, "POST", issueURL+"/item/"+strconv.FormatInt(iid, 10)+"/decision",
			url.Values{"kind": {kind}, "reason": {reason}})
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("decision %s on item %d = %d %s", kind, iid, rr.Code, rr.Body.String())
		}
	}
	decide(iis[1].ID, "reject", "off the publication's angle")
	decide(iis[2].ID, "reject", "too thin for this issue")
	decide(iis[5].ID, "reject", "duplicates a stronger item")
	decide(iis[7].ID, "reject", "prioritize policy with direct Brazil impact")
	decide(iis[6].ID, "alternative", "")

	// approve the kept items, including the alternative's replacement
	iis2, err := db.IssueItems(ctx, st.IssueID)
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
		if rr := call(t, srv, "POST", issueURL+"/draft", nil); rr.Code != http.StatusSeeOther {
			t.Fatalf("POST draft #%d = %d %s", i+1, rr.Code, rr.Body.String())
		}
	}
	rr := call(t, srv, "GET", issueURL+"/draft", nil)
	if !strings.Contains(rr.Body.String(), "Sending blocked") {
		t.Fatal("draft page should show blockers (required commentary + flag)")
	}

	// --- clear blockers ---
	if rr := call(t, srv, "POST", issueURL+"/commentary", url.Values{
		"section": {"The big one"}, "text": {"My editorial take on the regulator consultation."},
	}); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST commentary = %d", rr.Code)
	}
	dbg, _ := db.GetIssue(ctx, st.IssueID)
	if len(dbg.Flags) == 0 {
		t.Fatal("expected at least one citation flag from the fixture citecheck")
	}
	for len(dbg.Flags) > 0 {
		if rr := call(t, srv, "POST", issueURL+"/flag/0/remove", nil); rr.Code != http.StatusSeeOther {
			t.Fatalf("POST remove flag = %d", rr.Code)
		}
		dbg, _ = db.GetIssue(ctx, st.IssueID)
	}
	rr2 := call(t, srv, "GET", issueURL+"/draft", nil)
	if strings.Contains(rr2.Body.String(), "Sending blocked") {
		i := strings.Index(rr2.Body.String(), "Sending blocked")
		t.Fatalf("blockers should be cleared: %s", rr2.Body.String()[i:i+300])
	}

	// --- approve, export, send ---
	rr = call(t, srv, "POST", issueURL+"/approve", nil)
	if rr.Code != http.StatusSeeOther || !strings.HasSuffix(rr.Header().Get("Location"), "/send") {
		t.Fatalf("POST approve = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	if rr := call(t, srv, "GET", issueURL+"/export?fmt=markdown", nil); rr.Code != 200 || !strings.Contains(rr.Body.String(), "# Futuro Próximo") {
		t.Fatal("markdown export failed")
	}
	if rr := call(t, srv, "GET", issueURL+"/export?fmt=html", nil); rr.Code != 200 || !strings.Contains(rr.Body.String(), "<") {
		t.Fatal("html export failed")
	}
	if rr := call(t, srv, "GET", "/pub/"+strconv.FormatInt(pub.ID, 10)+"/guide", nil); !strings.Contains(rr.Body.String(), "rioritize policy with direct Brazil impact") {
		t.Fatal("guide missing recorded rejection reason")
	}

	// --- reader gating ---
	pubURL := "/pub/" + strconv.FormatInt(pub.ID, 10)
	if rr := call(t, srv, "GET", pubURL+"/reader", nil); !strings.Contains(rr.Body.String(), "Subscribers only") {
		t.Fatal("reader view should gate paid sections for non-subscribers")
	}
	if rr := call(t, srv, "POST", pubURL+"/paywall/simulate", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST paywall simulate = %d", rr.Code)
	}
	if rr := call(t, srv, "GET", pubURL+"/reader", nil); strings.Contains(rr.Body.String(), "Subscribers only") {
		t.Fatal("reader view should be unlocked after simulated payment")
	}

	// a test send records the outbox email but leaves the issue approved
	if rr := call(t, srv, "POST", issueURL+"/send", url.Values{"to": {"test@example.com"}}); !strings.Contains(rr.Body.String(), "sent to the demo outbox") {
		t.Fatalf("POST test send = %d %s", rr.Code, rr.Body.String())
	}
	iss, err := db.GetIssue(ctx, st.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "approved" {
		t.Fatalf("test send marked issue %q — it must stay approved", iss.Status)
	}
	// the readers send then marks it sent
	if rr := call(t, srv, "POST", issueURL+"/send", nil); !strings.Contains(rr.Body.String(), "Issue sent to the demo outbox") {
		t.Fatalf("POST send = %d %s", rr.Code, rr.Body.String())
	}
	iss, err = db.GetIssue(ctx, st.IssueID)
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
	add := func(typ, u string) *httptest.ResponseRecorder {
		return call(t, srv, "POST", "/pub/"+strconv.FormatInt(pub.ID, 10)+"/sources/add",
			url.Values{"type": {typ}, "url": {u}})
	}
	if rr := add("rss", "https://example.com/feed.xml"); !strings.Contains(rr.Body.String(), "not fetched") {
		t.Fatalf("demo mode should accept but simulate non-fixture sources (code %d)", rr.Code)
	}
	if rr := add("rss", "notaurl"); !strings.Contains(rr.Body.String(), "invalid source URL") {
		t.Fatal("demo mode should reject malformed URLs")
	}
	if rr := add("rss", "fixtures/feeds/radar-tech.xml"); rr.Code != http.StatusSeeOther {
		t.Fatalf("fixture source add = %d", rr.Code)
	}

	// real mode validates URL syntax without fetching
	srv2, _, pub2, cfg := demoServer(t)
	cfg.Mode = "real"
	if rr := call(t, srv2, "POST", "/pub/"+strconv.FormatInt(pub2.ID, 10)+"/sources/add",
		url.Values{"type": {"rss"}, "url": {"notaurl"}}); !strings.Contains(rr.Body.String(), "invalid source URL") {
		t.Fatal("real mode should reject malformed source URLs")
	}
	if rr := call(t, srv2, "POST", "/pub/"+strconv.FormatInt(pub2.ID, 10)+"/sources/add",
		url.Values{"type": {"rss"}, "url": {"ftp://example.com/feed"}}); !strings.Contains(rr.Body.String(), "invalid source URL") {
		t.Fatal("real mode should reject non-http(s) schemes")
	}
	if rr := call(t, srv2, "POST", "/pub/"+strconv.FormatInt(pub2.ID, 10)+"/sources/add",
		url.Values{"type": {"page"}, "url": {"https://example.com/news"}}); rr.Code != http.StatusSeeOther {
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
