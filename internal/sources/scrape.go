package sources

// One-shot page scraping for the "paste a link" onboarding flow
// (KLOX-99): the creator's own newsletter/blog URL becomes a proposed
// publication definition. Demo/tests use FakeScraper (deterministic,
// offline — the same rule as every demo fetch: a visitor-typed URL is
// never fetched here); real mode uses FirecrawlScraper, one credit per
// page on the sponsor grant.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/httpx"
	"github.com/sthefanocordeiro/kloxus/internal/strutil"
)

// ScrapedPage is one page's readable content plus the metadata the
// proposal extractor reads.
type ScrapedPage struct {
	URL         string // final URL after redirects
	Title       string
	Description string
	Language    string // page-declared language, e.g. "pt-BR"
	Keywords    []string
	Markdown    string
	StatusCode  int
}

// Scraper pulls one page's readable content. Callers see the provider
// name so simulated results stay labeled (the FakeMonitor convention).
type Scraper interface {
	Scrape(ctx context.Context, url string) (*ScrapedPage, error)
	Name() string
}

// FakeScraper simulates a scrape in demo mode: deterministic, offline.
// The page it returns is honest about what it is — a sample structure
// the creator reviews and edits, never fetched content.
type FakeScraper struct{}

// Name implements Scraper.
func (FakeScraper) Name() string { return "firecrawl (simulated)" }

// Scrape implements Scraper. The URL's host becomes the sample site's
// name so the proposal reads plausibly; nothing is fetched.
func (FakeScraper) Scrape(_ context.Context, rawURL string) (*ScrapedPage, error) {
	host := strings.TrimPrefix(hostOf(rawURL), "www.")
	if host == "" {
		return nil, fmt.Errorf("invalid URL %q", rawURL)
	}
	site := siteName(host)
	return &ScrapedPage{
		URL:         rawURL,
		Title:       site,
		Description: "Cobertura e análise dos temas que " + site + " acompanha toda semana.",
		Language:    "pt-BR",
		StatusCode:  200,
		Markdown: "# " + site + "\n\n" +
			"Edição desta semana de " + site + ", com os destaques que importam.\n\n" +
			"## O destaque\n\nA história principal da semana, explicada.\n\n" +
			"## Vale saber\n\nAs notícias que moldam os próximos dias.\n\n" +
			"## Links rápidos\n\nO resto em uma linha e um link.\n",
	}, nil
}

// FirecrawlScraper scrapes one URL through the Firecrawl API (v2
// /scrape — markdown + page metadata, onlyMainContent). One call costs
// one credit; the handler's per-workspace daily cap bounds spend.
type FirecrawlScraper struct {
	APIKey  string
	BaseURL string // defaults to https://api.firecrawl.dev
	HTTP    *http.Client
	// Timeout bounds one scrape request server-side (default 45 s —
	// Firecrawl renders JS, which can legitimately take tens of
	// seconds; the caller's own request deadline still applies).
	Timeout time.Duration
}

// Name implements Scraper.
func (FirecrawlScraper) Name() string { return "firecrawl" }

func (f FirecrawlScraper) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	return httpx.Client(60 * time.Second)
}

func (f FirecrawlScraper) base() string {
	if f.BaseURL == "" {
		return "https://api.firecrawl.dev"
	}
	return f.BaseURL
}

// Scrape implements Scraper.
func (f FirecrawlScraper) Scrape(ctx context.Context, rawURL string) (*ScrapedPage, error) {
	if f.APIKey == "" {
		return nil, fmt.Errorf("FIRECRAWL_API_KEY is not set")
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	payload := map[string]any{
		"url":             rawURL,
		"formats":         []string{"markdown"},
		"onlyMainContent": true,
		"timeout":         int(timeout.Milliseconds()),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.base()+"/v2/scrape",
		strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+f.APIKey)
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Data    struct {
			Markdown string `json:"markdown"`
			Metadata struct {
				Title       any    `json:"title"`
				Description any    `json:"description"`
				Language    any    `json:"language"`
				Keywords    any    `json:"keywords"`
				URL         string `json:"url"`
				StatusCode  int    `json:"statusCode"`
				Err         string `json:"error"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("firecrawl scrape: decoding response: %w", err)
	}
	if resp.StatusCode >= 300 || !out.Success {
		msg := out.Error
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("firecrawl scrape: %d: %s", resp.StatusCode, strutil.Truncate(msg, 300))
	}
	page := &ScrapedPage{
		URL:         out.Data.Metadata.URL,
		Title:       metaString(out.Data.Metadata.Title),
		Description: metaString(out.Data.Metadata.Description),
		Language:    metaString(out.Data.Metadata.Language),
		Keywords:    metaStrings(out.Data.Metadata.Keywords),
		Markdown:    out.Data.Markdown,
		StatusCode:  out.Data.Metadata.StatusCode,
	}
	if page.URL == "" {
		page.URL = rawURL
	}
	if out.Data.Metadata.Err != "" {
		return page, fmt.Errorf("firecrawl scrape: page error: %s", strutil.Truncate(out.Data.Metadata.Err, 200))
	}
	return page, nil
}

// metaString normalizes a metadata field Firecrawl may return as a
// string or an array of strings.
func metaString(v any) string {
	s := strings.TrimSpace(firstMeta(v))
	const max = 500
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func firstMeta(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		if len(t) > 0 {
			if s, ok := t[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// metaStrings reads a keywords field (comma-separated string or array).
func metaStrings(v any) []string {
	var out []string
	switch t := v.(type) {
	case string:
		for _, k := range strings.Split(t, ",") {
			if k = strings.TrimSpace(k); k != "" {
				out = append(out, k)
			}
		}
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// hostOf extracts the host part of an absolute URL ("" when unparseable).
func hostOf(rawURL string) string {
	if i := strings.Index(rawURL, "://"); i >= 0 {
		rawURL = rawURL[i+3:]
	}
	if i := strings.IndexAny(rawURL, "/?#"); i >= 0 {
		rawURL = rawURL[:i]
	}
	return rawURL
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// siteName turns a host into a readable site name —
// "blog.radar-fintech.com.br" → "Radar Fintech".
func siteName(host string) string {
	parts := strings.Split(host, ".")
	// drop www and the registrable suffix (com, com.br, dev, io, …)
	if len(parts) > 0 && parts[0] == "www" {
		parts = parts[1:]
	}
	if len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 1 && (parts[len(parts)-1] == "com" || parts[len(parts)-1] == "co" ||
		parts[len(parts)-1] == "org" || parts[len(parts)-1] == "net") {
		parts = parts[:len(parts)-1]
	}
	name := strings.Join(parts, " ")
	words := strings.Fields(nonWord.ReplaceAllString(name, " "))
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
