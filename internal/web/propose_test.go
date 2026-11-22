package web_test

// Contract tests for POST /api/v1/publications/propose (KLOX-99): the
// pasted-URL proposal flow. Every test injects a stub Scraper — no
// Firecrawl call ever leaves the test process, the same rule as the
// FakeMonitor tests.
import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// stubScraper returns a fixed page or error.
type stubScraper struct {
	page *sources.ScrapedPage
	err  error
}

func (s stubScraper) Name() string { return "stub" }
func (s stubScraper) Scrape(context.Context, string) (*sources.ScrapedPage, error) {
	return s.page, s.err
}

func richPage() *sources.ScrapedPage {
	return &sources.ScrapedPage{
		URL:      "https://radar.example.com/edicao-42",
		Title:    "Radar Fintech | Edição 42",
		Language: "pt-BR",
		Markdown: strings.Join([]string{
			"# Radar Fintech",
			strings.Repeat("texto editorial de verdade para contar palavras. ", 30),
			"## O pix fora do ar",
			"## Bancos sob pressão",
			"## Links da semana",
		}, "\n"),
	}
}

func TestProposeSuccess(t *testing.T) {
	srv, _, _, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Scraper = stubScraper{page: richPage()}
	})
	rr := apiCall(t, srv, "POST", "/api/v1/publications/propose",
		map[string]string{"material": "https://radar.example.com/edicao-42"})
	if rr.Code != 200 {
		t.Fatalf("status = %d: %s", rr.Code, shortBody(rr))
	}
	body := apiJSON(t, rr)
	prop, ok := body["proposal"].(map[string]any)
	if !ok {
		t.Fatalf("no proposal object: %s", shortBody(rr))
	}
	if prop["name"] != "Radar Fintech" {
		t.Fatalf("proposal.name = %v", prop["name"])
	}
	secs, ok := prop["sections"].([]any)
	if !ok || len(secs) != 3 {
		t.Fatalf("sections = %v", prop["sections"])
	}
	first := secs[0].(map[string]any)
	if first["title"] != "O pix fora do ar" || first["format"] != "featured" {
		t.Fatalf("first section = %v", first)
	}
	// the pasted URL never becomes an offered source — template only
	if srcs := prop["sources"].([]any); len(srcs) != 0 {
		t.Fatalf("sources = %v, want empty", srcs)
	}
	sc, ok := body["scrape"].(map[string]any)
	if !ok {
		t.Fatalf("no scrape object: %s", shortBody(rr))
	}
	if sc["provider"] != "stub" || sc["simulated"] != true {
		t.Fatalf("scrape = %v", sc)
	}
}

func TestProposeInvalidURL(t *testing.T) {
	srv, _, _, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Scraper = stubScraper{page: richPage()}
	})
	for _, material := range []string{"", "not a url", "ftp://x/y", "https://"} {
		rr := apiCall(t, srv, "POST", "/api/v1/publications/propose",
			map[string]string{"material": material})
		if got := apiErrCode(t, rr); got != "invalid_url" {
			t.Fatalf("material %q: code = %q, want invalid_url", material, got)
		}
	}
}

func TestProposeProviderFailure(t *testing.T) {
	srv, _, _, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Scraper = stubScraper{err: fmt.Errorf("upstream boom")}
	})
	rr := apiCall(t, srv, "POST", "/api/v1/publications/propose",
		map[string]string{"material": "https://x.example.com"})
	if rr.Code != 502 {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := apiErrCode(t, rr); got != "scrape_failed" {
		t.Fatalf("code = %q, want scrape_failed", got)
	}
}

func TestProposeBlockedOnThinPage(t *testing.T) {
	srv, _, _, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Scraper = stubScraper{page: &sources.ScrapedPage{
			URL:      "https://pay.example.com/post",
			Title:    "Paywalled",
			Markdown: "Subscribe to keep reading.",
		}}
	})
	rr := apiCall(t, srv, "POST", "/api/v1/publications/propose",
		map[string]string{"material": "https://pay.example.com/post"})
	if rr.Code != 422 {
		t.Fatalf("status = %d: %s", rr.Code, shortBody(rr))
	}
	if got := apiErrCode(t, rr); got != "scrape_blocked" {
		t.Fatalf("code = %q, want scrape_blocked", got)
	}
}

func TestProposeUnavailableWithoutProvider(t *testing.T) {
	srv, _, _, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Scraper = nil
	})
	rr := apiCall(t, srv, "POST", "/api/v1/publications/propose",
		map[string]string{"material": "https://x.example.com"})
	if rr.Code != 503 {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := apiErrCode(t, rr); got != "scrape_unavailable" {
		t.Fatalf("code = %q, want scrape_unavailable", got)
	}
}

func TestProposeDailyCap(t *testing.T) {
	srv, db, _, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Scraper = stubScraper{page: richPage()}
		d.Cfg.ScrapesPerDay = 1
	})
	body := map[string]string{"material": "https://radar.example.com"}
	if rr := apiCall(t, srv, "POST", "/api/v1/publications/propose", body); rr.Code != 200 {
		t.Fatalf("first scrape = %d: %s", rr.Code, shortBody(rr))
	}
	rr := apiCall(t, srv, "POST", "/api/v1/publications/propose", body)
	if rr.Code != 429 {
		t.Fatalf("second scrape = %d, want 429", rr.Code)
	}
	if got := apiErrCode(t, rr); got != "scrape_cap" {
		t.Fatalf("code = %q, want scrape_cap", got)
	}
	// attempts land in the same paid_actions ledger as the AI counters
	if n, err := db.PaidActionCount(context.Background(), store.PaidActionScrape, 0); err != nil || n < 1 {
		t.Fatalf("PaidActionCount scrape = %d, err = %v", n, err)
	}
}
