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
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
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

	data, err := examples.FS.ReadFile("publications/futuro-proximo.yaml")
	if err != nil {
		t.Fatalf("reading example: %v", err)
	}
	def, err := config.ParsePublication(data)
	if err != nil {
		t.Fatalf("ParsePublication: %v", err)
	}
	pubID, err := db.SeedPublication(ctx, def)
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, strconv.FormatInt(pubID, 10))
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}

	srv := web.New(web.Deps{
		DB: db, Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm", DumpDir: filepath.Join(cfg.OutDir, "llm-dump")},
		Monitor: sources.FakeMonitor{},
		Mailer:  delivery.Outbox{Dir: filepath.Join(base, "outbox")},
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
	decide(iis[1].ID, "reject", "")
	decide(iis[2].ID, "reject", "")
	decide(iis[5].ID, "reject", "")
	decide(iis[7].ID, "reject", "prioritize policy with direct Brazil impact")
	decide(iis[6].ID, "alternative", "")

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
	if rr := call(t, srv, "POST", issueURL+"/flag/0/dismiss", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST dismiss flag = %d", rr.Code)
	}
	if rr := call(t, srv, "GET", issueURL+"/draft", nil); strings.Contains(rr.Body.String(), "Sending blocked") {
		t.Fatal("blockers should be cleared after commentary + flag dismissal")
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
	if rr := call(t, srv, "GET", "/pub/"+strconv.FormatInt(pub.ID, 10)+"/guide", nil); !strings.Contains(rr.Body.String(), "prioritize policy with direct Brazil impact") {
		t.Fatal("guide missing recorded rejection reason")
	}

	// --- reader gating ---
	pubURL := "/pub/" + strconv.FormatInt(pub.ID, 10)
	if rr := call(t, srv, "GET", pubURL+"/reader", nil); !strings.Contains(rr.Body.String(), "This section is for paid readers") {
		t.Fatal("reader view should gate paid sections for non-subscribers")
	}
	if rr := call(t, srv, "POST", pubURL+"/paywall/simulate", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST paywall simulate = %d", rr.Code)
	}
	if rr := call(t, srv, "GET", pubURL+"/reader", nil); strings.Contains(rr.Body.String(), "This section is for paid readers") {
		t.Fatal("reader view should be unlocked after simulated payment")
	}

	if rr := call(t, srv, "POST", issueURL+"/send", url.Values{"to": {"test@example.com"}}); !strings.Contains(rr.Body.String(), "Issue sent to the demo outbox") {
		t.Fatalf("POST send = %d %s", rr.Code, rr.Body.String())
	}
	iss, err := db.GetIssue(ctx, st.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "sent" {
		t.Fatalf("issue status = %q, want sent", iss.Status)
	}
}

// TestSourceAddValidation covers the demo-mode fetch check and the
// real-mode URL validation added for source registration.
func TestSourceAddValidation(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	add := func(typ, u string) *httptest.ResponseRecorder {
		return call(t, srv, "POST", "/pub/"+strconv.FormatInt(pub.ID, 10)+"/sources/add",
			url.Values{"type": {typ}, "url": {u}})
	}
	if rr := add("rss", "https://example.com/feed.xml"); !strings.Contains(rr.Body.String(), "real URLs need real mode") {
		t.Fatalf("demo mode should reject non-fixture source URLs (code %d)", rr.Code)
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
