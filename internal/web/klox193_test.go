package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// hostedServer builds a hosted-mode server (Postgres pool, per-visitor
// workspaces) like main.go runs on Fly — the surface KLOX-193 fixes.
func hostedServer(t *testing.T) (*web.Server, *workspace.Manager) {
	t.Helper()
	ctx := context.Background()
	seed := func(ctx context.Context, sdb *store.DB) error {
		data, _ := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		_, err = sdb.SeedPublication(ctx, def)
		return err
	}
	mgr, err := workspace.OpenHosted(ctx, testdb.Pool(t),
		[]byte("test-secret-key-32-bytes-padded-ok!"), seed)
	if err != nil {
		t.Fatalf("OpenHosted: %v", err)
	}
	t.Cleanup(mgr.Close)
	srv := web.New(web.Deps{
		WS: mgr, Cfg: &config.Config{
			PublishingEnabled: true, Mode: "demo",
			ModelTriage: "claude-haiku-5-5", ModelDraft: "claude-sonnet-5-5",
		},
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm", DumpDir: llmDumpDir("")},
		Monitor: sources.FakeMonitor{},
		Seed:    seed,
	})
	return srv, mgr
}

// TestNoMintOnPageviews (KLOX-193): the landing page, the SPA catch-all,
// the reader API and the API 404 never mint a workspace — no Set-Cookie,
// and the session-create allowance stays untouched.
func TestNoMintOnPageviews(t *testing.T) {
	srv, mgr := hostedServer(t)

	serve := func(method, path string, body any) *httptest.ResponseRecorder {
		var reader *strings.Reader
		if body == nil {
			reader = strings.NewReader("")
		} else {
			b, _ := json.Marshal(body)
			reader = strings.NewReader(string(b))
		}
		req := httptest.NewRequest(method, path, reader)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("User-Agent", "klox193-test")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr
	}

	// mint one workspace so the reader routes have a publication to serve
	rrA := serve("GET", "/api/v1/workspace", nil)
	if rrA.Code != 200 {
		t.Fatalf("mint A = %d", rrA.Code)
	}
	cookieA := strings.SplitN(rrA.Header().Get("Set-Cookie"), ";", 2)[0]
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Cookie", cookieA)
	wsA, err := mgr.Resolve(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("resolve wsA: %v", err)
	}
	ctx := context.Background()
	seed := func(ctx context.Context, sdb *store.DB) error {
		data, _ := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		_, err = sdb.SeedPublication(ctx, def)
		return err
	}
	if err := seed(ctx, wsA.DB); err != nil {
		t.Fatalf("seed wsA: %v", err)
	}
	pubs, _ := wsA.DB.ListPublications(ctx)
	slug := pubs[0].Slug
	wsq := "?ws=" + wsA.ID

	// now clamp the create limit to setup + one: the mint above already
	// spent one create on this (IP, fingerprint) bucket, so any pageview
	// mint below exhausts the allowance before the final real request
	mgr.SetCreateLimit(2)

	anon := []struct{ method, path string }{
		{"GET", "/"},
		{"GET", "/termos"},
		{"GET", "/app/dashboard"},
		{"GET", "/p/" + slug},
		{"GET", "/api/v1/p/" + slug + wsq},
		{"GET", "/api/v1/p/" + slug + "/1" + wsq},
		{"GET", "/api/v1/unsub?r=1&t=bogus&pub=" + slug + wsq},
		{"GET", "/api/v1/definitely-not-a-route"},
		{"POST", "/api/v1/p/" + slug + "/subscribe" + wsq},
	}
	for _, c := range anon {
		var body any
		if c.method == "POST" {
			body = map[string]string{"email": "reader@x.dev"}
		}
		rr := serve(c.method, c.path, body)
		if sc := rr.Header().Get("Set-Cookie"); strings.Contains(sc, workspace.CookieName) {
			t.Fatalf("%s %s set a workspace cookie", c.method, c.path)
		}
		if rr.Code == http.StatusTooManyRequests {
			t.Fatalf("%s %s = 429 — pageviews must never consume the create allowance", c.method, c.path)
		}
	}

	// reader routes actually worked (not just fell through to the SPA)
	if rr := serve("GET", "/api/v1/p/"+slug+wsq, nil); rr.Code != 200 ||
		!strings.Contains(rr.Body.String(), slug) {
		t.Fatalf("GET /api/v1/p/%s = %d %s", slug, rr.Code, shortBody(rr))
	}
	if rr := serve("POST", "/api/v1/p/"+slug+"/subscribe"+wsq,
		map[string]string{"email": "reader@x.dev"}); rr.Code != 200 {
		t.Fatalf("subscribe = %d %s", rr.Code, shortBody(rr))
	}

	// and a real session request still mints — the pageviews above did
	// not spend the remaining allowance
	rr := serve("GET", "/api/v1/workspace", nil)
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Set-Cookie"), workspace.CookieName) {
		t.Fatalf("first real mint = %d (pageviews consumed the allowance?)", rr.Code)
	}
}

// TestRateLimitedSurfaces (KLOX-193): once the (IP, fingerprint) bucket
// is full, /api/ gets the typed JSON envelope + Retry-After, pages get
// the friendly HTML page + Retry-After, and a different browser
// fingerprint on the same address is unaffected.
func TestRateLimitedSurfaces(t *testing.T) {
	srv, mgr := hostedServer(t)
	mgr.SetCreateLimit(1)

	serve := func(method, path, ua, acceptLang string, body any) *httptest.ResponseRecorder {
		var reader *strings.Reader
		if body == nil {
			reader = strings.NewReader("")
		} else {
			b, _ := json.Marshal(body)
			reader = strings.NewReader(string(b))
		}
		req := httptest.NewRequest(method, path, reader)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("User-Agent", ua)
		if acceptLang != "" {
			req.Header.Set("Accept-Language", acceptLang)
		}
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr
	}

	// browser A spends its allowance on a real session request
	if rr := serve("GET", "/api/v1/workspace", "browser-A", "pt-BR", nil); rr.Code != 200 {
		t.Fatalf("first mint = %d", rr.Code)
	}

	// API surface: typed JSON envelope + Retry-After
	rr := serve("GET", "/api/v1/workspace", "browser-A", "pt-BR", nil)
	if rr.Code != 429 {
		t.Fatalf("second mint = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Error.Code != "rate_limited" {
		t.Fatalf("api 429 body = %q, want error.code rate_limited", shortBody(rr))
	}

	// page surface: friendly HTML, not the raw JSON
	rr = serve("GET", "/a/1", "browser-A", "pt-BR", nil)
	if rr.Code != 429 {
		t.Fatalf("page 429 = %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("page 429 without Retry-After")
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("page 429 content-type = %q, want text/html", ct)
	}
	if !strings.Contains(rr.Body.String(), "Tente de novo") {
		t.Fatalf("page 429 body missing the pt copy: %s", shortBody(rr))
	}

	// exempt routes stay exempt even under an exhausted bucket: the
	// request reaches appPage — 200 with a built frontend, 503 with the
	// placeholder dist the tests run with — but never 429, never a
	// Retry-After, never a session cookie
	rr = serve("GET", "/", "browser-A", "pt-BR", nil)
	if rr.Code != 200 && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET / under exhausted bucket = %d, want 200 or 503", rr.Code)
	}
	if rr.Header().Get("Retry-After") != "" {
		t.Fatal("GET / under exhausted bucket set Retry-After — not exempt")
	}
	if sc := rr.Header().Get("Set-Cookie"); strings.Contains(sc, workspace.CookieName) {
		t.Fatal("GET / under exhausted bucket minted a session")
	}

	// same address, another browser: independent bucket (CGNAT case)
	if rr := serve("GET", "/api/v1/workspace", "browser-B", "pt-BR", nil); rr.Code != 200 {
		t.Fatalf("different fingerprint on the same IP = %d, want 200", rr.Code)
	}
	// ...and same browser with a different Accept-Language too
	if rr := serve("GET", "/api/v1/workspace", "browser-A", "en-US", nil); rr.Code != 200 {
		t.Fatalf("different Accept-Language on the same IP = %d, want 200", rr.Code)
	}
}

// TestSubscribeNeedsJSON (KLOX-193): the exempt mutating public route
// still enforces the JSON-only API contract without a session.
func TestSubscribeNeedsJSON(t *testing.T) {
	srv, _ := hostedServer(t)
	req := httptest.NewRequest("POST", "/api/v1/p/slug/subscribe?ws=x",
		strings.NewReader("email=x@y.dev"))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON subscribe = %d, want 415", rr.Code)
	}
}
