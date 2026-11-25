package web

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// sampleArticle serves a stub article page for the landing sample's
// sources: the dossiers cite fictional .example outlets, so the links
// resolve inside the app instead of hitting a dead external domain.
// Workspace-free like /api/v1/sample — no session before Start.
func (s *Server) sampleArticle(w http.ResponseWriter, r *http.Request) {
	u, err := url.Parse(strings.TrimSpace(r.URL.Query().Get("u")))
	if err != nil || u.Host == "" || !strings.HasSuffix(u.Host, ".example") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	title := strings.Trim(strings.ReplaceAll(u.Path, "/", " "), " ")
	if title == "" {
		title = u.Host
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>%s — %s</title>`+
		`<style>body{font-family:Georgia,serif;max-width:38em;margin:3em auto;padding:0 1.2em;color:#1c1c1c;line-height:1.6}`+
		`.site{color:#0e7a6d;font-size:.85em;text-transform:uppercase;letter-spacing:.08em}`+
		`.note{margin-top:3em;padding:.9em 1.1em;border:1px dashed #bbb;border-radius:.5em;color:#666;font-size:.85em;font-family:system-ui,sans-serif}`+
		`</style></head><body>`+
		`<p class="site">%s</p><h1>%s</h1>`+
		`<p>Este é um artigo de demonstração do universo ficcional de Valdória — `+
		`o Kloxus serve fontes simuladas para que cada citação abra um original de verdade, mesmo em modo demo.</p>`+
		`<div class="note">Conteúdo simulado · fictional source · <a href="/">← Kloxus</a></div>`+
		`</body></html>`,
		title, u.Host, u.Host, title)
}

// --- workspace / publications / sources ---

func (s *Server) apiWorkspace(w http.ResponseWriter, r *http.Request) {
	ws := reqWS(r)
	apiJSON(w, 200, map[string]any{
		"id": ws.ID, "label": ws.Label, "mode": s.d.Cfg.Mode, "hosted": s.d.WS.Hosted(),
	})
}

func (s *Server) apiPublications(w http.ResponseWriter, r *http.Request) {
	pubs, err := db(r).ListPublications(r.Context())
	if apiDBErr(w, r, err, "publications") {
		return
	}
	out := []apiPublication{}
	for _, p := range pubs {
		out = append(out, publicationView(p))
	}
	apiJSON(w, 200, map[string]any{"publications": out})
}

func (s *Server) apiPublication(w http.ResponseWriter, r *http.Request) {
	// detail view shows the stored (creator-edited) blocks — canonicalPub
	// applies only to prompt/section semantics
	pub, err := db(r).GetPublication(r.Context(), r.PathValue("id"))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	resp := map[string]any{"publication": publicationView(pub)}
	if iss, err := db(r).LatestIssue(r.Context(), pub.ID); err == nil {
		resp["latest_issue"] = s.issueSummary(r, iss)
	}
	apiJSON(w, 200, resp)
}

// apiSourceRow is one source with monitor state.
type apiSourceRow struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	URL          string         `json:"url"`
	CanonicalURL string         `json:"canonical_url"`
	LastFetchAt  *string        `json:"last_fetch_at"`
	LastStatus   *string        `json:"last_status"`
	ItemsPerWeek int            `json:"items_per_week"`
	Monitor      apiMonitorView `json:"monitor"`
}

type apiMonitorView struct {
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (s *Server) sourceRow(r *http.Request, src store.Source) apiSourceRow {
	row := apiSourceRow{
		ID: src.ID, Name: src.Name, Type: src.Type, URL: src.URL,
		CanonicalURL: dedupe.Canonical(src.URL),
	}
	if src.LastFetchAt.Valid {
		row.LastFetchAt = &src.LastFetchAt.String
	}
	if src.LastStatus.Valid {
		row.LastStatus = &src.LastStatus.String
	}
	p, mid, st, _ := db(r).SourceMonitor(r.Context(), src.ID)
	row.Monitor = apiMonitorView{Provider: p, ID: mid, Status: st}
	row.ItemsPerWeek, _ = db(r).ItemsPerWeek(r.Context(), src.ID)
	return row
}

func (s *Server) apiSources(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	srcs, _ := db(r).Sources(r.Context(), pub.ID)
	out := []apiSourceRow{}
	for _, src := range srcs {
		out = append(out, s.sourceRow(r, src))
	}
	apiJSON(w, 200, map[string]any{"sources": out})
}

func (s *Server) apiSourceAdd(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Type string `json:"type"`
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	typ, rawURL := strings.TrimSpace(in.Type), strings.TrimSpace(in.URL)
	in.Name = strings.TrimSpace(in.Name)
	if rawURL == "" || (typ != "rss" && typ != "page") {
		apiErr(w, 400, "validation", "type rss|page and url are required")
		return
	}
	note := ""
	if s.d.Cfg.Mode == "demo" {
		// Demo mode never fetches a visitor-typed URL (SSRF surface):
		// validate the format, store, simulate.
		if strings.HasPrefix(rawURL, "fixtures/") {
			if _, err := fs.Stat(examples.FS, rawURL); err != nil {
				apiErr(w, 400, "invalid_source",
					fmt.Sprintf("no fixture at %q — demo sources live under fixtures/...", rawURL))
				return
			}
		} else {
			u, err := url.Parse(rawURL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				apiErr(w, 400, "invalid_source", fmt.Sprintf("invalid source URL %q", rawURL))
				return
			}
			note = "Simulated in demo mode: sources are not fetched on the hosted demo."
		}
	} else {
		u, err := url.Parse(rawURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			apiErr(w, 400, "invalid_source",
				fmt.Sprintf("invalid source URL %q — real-mode sources must be absolute http(s) URLs", rawURL))
			return
		}
	}
	id, err := db(r).AddSource(r.Context(), pub.ID, typ, rawURL, in.Name)
	if apiDBErr(w, r, err, "source") {
		return
	}
	var row apiSourceRow
	for _, src := range mustSources(r, pub.ID) {
		if src.ID == id || (row.ID == 0 && src.URL == rawURL) {
			row = s.sourceRow(r, src)
		}
	}
	if row.ID == 0 {
		row.ID = id
		row.Type, row.URL = typ, rawURL
		row.CanonicalURL = dedupe.Canonical(rawURL)
	}
	resp := map[string]any{"source": row}
	if note != "" {
		resp["note"] = note
	}
	apiJSON(w, 201, resp)
}

func mustSources(r *http.Request, pubID int64) []store.Source {
	srcs, _ := db(r).Sources(r.Context(), pubID)
	return srcs
}

// apiSourceDelete removes a source — the way out for a failing one.
func (s *Server) apiSourceDelete(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	if apiDBErr(w, r, db(r).DeleteSource(r.Context(), pub.ID, sid), "source") {
		return
	}
	apiJSON(w, 200, map[string]any{"ok": true})
}

// apiSourceCatalog lists the sample fixture sources not yet added — the
// "offered" sources the sources screen proposes in demo mode.
func (s *Server) apiSourceCatalog(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	def, err := s.sampleDef()
	if err != nil {
		apiErr(w, 500, "internal", "source catalog unavailable")
		return
	}
	existing := map[string]bool{}
	for _, src := range mustSources(r, pub.ID) {
		existing[src.URL] = true
	}
	var offered []map[string]any
	for _, sd := range def.Sources {
		if existing[sd.URL] {
			continue
		}
		row := map[string]any{"type": sd.Type, "url": sd.URL, "name": sd.Name, "items_per_week": 0}
		// the fixture fetch is local — count what the source would bring
		// in so the row shows a real items-per-week before it's added
		if items, err := s.d.Fetcher.Fetch(r.Context(), sd.Type, sd.URL); err == nil {
			row["items_per_week"] = len(items)
		}
		offered = append(offered, row)
	}
	if offered == nil {
		offered = []map[string]any{}
	}
	apiJSON(w, 200, map[string]any{"offered": offered})
}

// apiSourcePreview shows the three latest items a source would bring in.
// Fixture URLs read from the embedded files; a pasted URL is validated
// for format only — demo mode never fetches it.
func (s *Server) apiSourcePreview(w http.ResponseWriter, r *http.Request) {
	_, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	typ, rawURL := r.URL.Query().Get("type"), strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" || (typ != "rss" && typ != "page") {
		apiErr(w, 400, "validation", "type rss|page and url are required")
		return
	}
	if !strings.HasPrefix(rawURL, "fixtures/") && s.d.Cfg.Mode == "demo" {
		u, perr := url.Parse(rawURL)
		if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			apiErr(w, 400, "invalid_source", fmt.Sprintf("invalid source URL %q", rawURL))
			return
		}
		apiJSON(w, 200, map[string]any{
			"items": []any{},
			"note":  "Simulated in demo mode: this URL is never fetched — in live mode the three latest items appear here.",
		})
		return
	}
	items, err := s.d.Fetcher.Fetch(r.Context(), typ, rawURL)
	if err != nil {
		apiErr(w, 400, "invalid_source", "cannot read that source: "+err.Error())
		return
	}
	var out []map[string]any
	for i, it := range items {
		if i == 3 {
			break
		}
		out = append(out, map[string]any{
			"title": it.Title, "url": it.URL, "outlet": it.Outlet, "published_at": it.PublishedAt,
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	apiJSON(w, 200, map[string]any{"items": out})
}

// apiSourceConfirm accepts the selected offered fixture sources and marks
// onboarding done — the client then starts the first run itself. An
// optional {"include": [...urls]} body limits which offered sources are
// added; no body accepts them all.
func (s *Server) apiSourceConfirm(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Include []string `json:"include"`
	}
	decoded, err := apiDecode(r, &in)
	if err != nil {
		apiErr(w, 400, "validation", "invalid JSON body")
		return
	}
	if s.d.Cfg.Mode == "demo" {
		if def, err := s.sampleDef(); err == nil {
			existing := map[string]bool{}
			for _, src := range mustSources(r, pub.ID) {
				existing[src.URL] = true
			}
			want := map[string]bool{}
			for _, u := range in.Include {
				want[u] = true
			}
			for _, sd := range def.Sources {
				if existing[sd.URL] || (decoded && !want[sd.URL]) {
					continue
				}
				db(r).AddSource(r.Context(), pub.ID, sd.Type, sd.URL, sd.Name)
			}
		}
	}
	db(r).SetProgress(r.Context(), "sources_confirmed")
	out := []apiSourceRow{}
	for _, src := range mustSources(r, pub.ID) {
		out = append(out, s.sourceRow(r, src))
	}
	apiJSON(w, 200, map[string]any{"sources": out})
}

// apiSample serves the landing page's cited dossier — workspace-free, so
// the first screen doesn't create a demo workspace before Start.
func (s *Server) apiSample(w http.ResponseWriter, r *http.Request) {
	data, err := examples.FS.ReadFile("landing/sample.json")
	if err != nil {
		apiErr(w, 500, "internal", "sample dossier unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(data)
}

// apiProposal returns the sample newsletter's proposed definition — the
// five editable blocks plus carried settings and the sources offered next.
func (s *Server) apiProposal(w http.ResponseWriter, r *http.Request) {
	def, err := s.sampleDef()
	if err != nil {
		apiErr(w, 500, "internal", "sample proposal unavailable")
		return
	}
	secs := []apiSection{}
	for _, sec := range def.Sections {
		secs = append(secs, apiSection{Title: sec.Title, Items: sec.Items, Commentary: sec.Commentary})
	}
	srcs := []map[string]string{}
	for _, sd := range def.Sources {
		srcs = append(srcs, map[string]string{"type": sd.Type, "url": sd.URL, "name": sd.Name})
	}
	apiJSON(w, 200, map[string]any{"proposal": map[string]any{
		"name": def.Name, "topic": def.Topic, "audience": def.Audience,
		"editorial_line": def.EditorialLine, "tone": def.Tone,
		"language": def.Language, "frequency": def.Frequency,
		"ai_disclosure": def.AIDisclosure,
		"sections":      secs, "sources": srcs,
		"paywall": apiPaywall{Enabled: def.Paywall.Enabled, PaidSections: def.Paywall.PaidSections, Price: def.Paywall.Price},
	}})
}

// apiPropose turns pasted material into a proposal. In demo mode only the
// sample newsletter is offered — pasted links/issues are refused with
// live_only, matching the "available in live mode" UX.
func (s *Server) apiPropose(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Material string `json:"material"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if s.d.Cfg.Mode == "demo" {
		apiErr(w, 422, "live_only",
			"proposals from pasted material are available in live mode — the hosted demo offers the sample newsletter")
		return
	}
	apiErr(w, 501, "not_implemented", "proposal extraction is not implemented")
}

// apiPublicationCreate stores a creator-made publication from the
// (possibly edited) proposal blocks. Sources are added separately on the
// sources screen.
func (s *Server) apiPublicationCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string           `json:"name"`
		Topic         string           `json:"topic"`
		Audience      string           `json:"audience"`
		EditorialLine string           `json:"editorial_line"`
		Tone          string           `json:"tone"`
		Language      string           `json:"language"`
		Frequency     string           `json:"frequency"`
		AIDisclosure  string           `json:"ai_disclosure"`
		Sections      []config.Section `json:"sections"`
		Paywall       config.Paywall   `json:"paywall"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	def := &config.Publication{
		Name: in.Name, Topic: in.Topic, Audience: in.Audience,
		EditorialLine: in.EditorialLine, Tone: in.Tone,
		Language: in.Language, Frequency: in.Frequency,
		AIDisclosure: in.AIDisclosure, Sections: in.Sections, Paywall: in.Paywall,
	}
	if err := def.Validate(); err != nil {
		apiErr(w, 400, "validation", err.Error())
		return
	}
	id, err := db(r).CreatePublication(r.Context(), def)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	db(r).SetProgress(r.Context(), "publication_created")
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(id))
	apiJSON(w, 201, map[string]any{"publication": publicationView(pub)})
}

func (s *Server) apiSourceWatch(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	srcURL, err := s.sourceURL(r, sid)
	if apiDBErr(w, r, err, "source") {
		return
	}
	mid, err := s.d.Monitor.Watch(r.Context(), srcURL)
	if err != nil {
		apiErr(w, 502, "monitor", "monitor: "+err.Error())
		return
	}
	if apiDBErr(w, r, db(r).SetSourceMonitor(r.Context(), sid, s.d.Monitor.Name(), mid, "monitoring"), "source") {
		return
	}
	var row apiSourceRow
	for _, src := range mustSources(r, pub.ID) {
		if src.ID == sid {
			row = s.sourceRow(r, src)
		}
	}
	apiJSON(w, 200, map[string]any{"source": row})
}

func (s *Server) apiSourceCheck(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	if _, err := s.sourceURL(r, sid); apiDBErr(w, r, err, "source") {
		return
	}
	_, mid, _, _ := db(r).SourceMonitor(r.Context(), sid)
	res, err := s.d.Monitor.Check(r.Context(), mid)
	if err != nil {
		apiErr(w, 502, "monitor", "check: "+err.Error())
		return
	}
	status := "monitoring"
	var details []string
	for _, rr := range res {
		status = rr.Status
		details = append(details, rr.Detail)
	}
	db(r).SetSourceMonitor(r.Context(), sid, s.d.Monitor.Name(), mid, status+": "+strings.Join(details, "; "))
	var row apiSourceRow
	for _, src := range mustSources(r, pub.ID) {
		if src.ID == sid {
			row = s.sourceRow(r, src)
		}
	}
	apiJSON(w, 200, map[string]any{"source": row})
}
