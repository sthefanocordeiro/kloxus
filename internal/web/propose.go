package web

// The "paste a link" proposal flow (KLOX-99): the creator pastes their
// own newsletter/blog URL, the configured Scraper (Firecrawl in real
// mode, the offline fake in demo) pulls the page, and a deterministic
// extractor proposes the newsletter's definition — name, sections,
// editorial-line starter — for review before anything is created.
// Honest states throughout: a URL the provider cannot read answers
// scrape_failed/scrape_blocked, a page with too little readable text
// answers scrape_blocked, and the demo's simulated provider is labeled
// in the response so the UI can badge it.
import (
	"net/http"
	"net/url"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// apiPropose turns a pasted URL into a proposed publication definition.
// One scrape = one Firecrawl credit — the per-workspace daily cap lives
// in the same paid_actions ledger as the §7.3 AI counters.
func (s *Server) apiPropose(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Material string `json:"material"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	material := strings.TrimSpace(in.Material)
	u, err := url.Parse(material)
	if material == "" || err != nil || u.Host == "" ||
		(u.Scheme != "http" && u.Scheme != "https") {
		apiErr(w, 400, "invalid_url", "paste a full http(s) URL — a newsletter, a blog or one issue")
		return
	}
	// The provider comes from wiring: real mode without a key is an
	// honest "not configured" — the product never silently fakes a
	// scrape outside demo.
	if s.d.Scraper == nil {
		apiErr(w, 503, "scrape_unavailable", "link import is not configured on this deployment")
		return
	}
	if cap := s.d.Cfg.ScrapesPerDay; cap > 0 {
		if n, err := db(r).PaidActionCount(r.Context(), store.PaidActionScrape, 0); err == nil && n >= cap {
			apiErr(w, 429, "scrape_cap", "this workspace reached today's link-import limit — try again tomorrow")
			return
		}
	}
	page, err := s.d.Scraper.Scrape(r.Context(), u.String())
	// Attempts count whether the provider read the page or not — the
	// credit may be spent either way, and the cap exists to bound that.
	_ = db(r).RecordPaidAction(r.Context(), store.PaidActionScrape, 0)
	if err != nil {
		apiErr(w, 502, "scrape_failed", "the page could not be read — check the link or try again later")
		return
	}
	// the page's own status speaks first — a 4xx is a wall the creator
	// should understand, a 5xx is a read failure; a readable body below
	// the floor (paywall stub, sign-in page, JS shell) is blocked too
	switch {
	case page.StatusCode >= 500:
		apiErr(w, 502, "scrape_failed", "the page could not be read — check the link or try again later")
		return
	case page.StatusCode >= 400:
		apiErr(w, 422, "scrape_blocked",
			"the page gave almost no readable text — it may be paywalled, sign-in only, or too script-heavy")
		return
	}
	prop := sources.ProposeFromPage(page, u.String())
	if prop.Words < sources.MinWords {
		apiErr(w, 422, "scrape_blocked",
			"the page gave almost no readable text — it may be paywalled, sign-in only, or too script-heavy")
		return
	}
	secs := []apiSection{}
	for _, sc := range config.NormalizeSections(prop.Sections, nil) {
		secs = append(secs, apiSection{
			ID: sc.ID, Title: sc.Title, Format: sc.Format,
			Items: sc.Items, Commentary: sc.Commentary, Access: sc.Access,
		})
	}
	apiJSON(w, 200, map[string]any{
		"proposal": map[string]any{
			"name": prop.Name, "topic": prop.Topic, "audience": prop.Audience,
			"editorial_line": prop.EditorialLine, "tone": prop.Tone,
			"language": prop.Language, "frequency": prop.Frequency,
			"ai_disclosure": prop.AIDisclosure,
			"sections":      secs,
			// the pasted URL feeds the template only — it is never
			// offered as a source (owner call: template cloning, not
			// catalog submission; real mode is catalog-only anyway)
			"sources": []map[string]string{},
			"paywall": apiPaywall{Enabled: false, PaidSections: []string{}, Price: ""},
		},
		"scrape": map[string]any{
			"url":       page.URL,
			"title":     page.Title,
			"provider":  s.d.Scraper.Name(),
			"simulated": s.d.Cfg.Mode == "demo",
			"words":     prop.Words,
			"warnings":  orEmpty(prop.Warnings),
		},
	})
}
