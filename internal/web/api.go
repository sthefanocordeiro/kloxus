package web

// The JSON API under /api/v1 (milestone 1 of the v2 contract — see
// docs/api.md). It exposes the same behaviour as the HTML routes; the
// rules do not change, only the format of the answers. Errors carry a
// stable code, a human message and, for blocked actions, the list of
// reasons with a pointer to what must be fixed.
import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/editorial"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// --- error envelope ---

// apiTarget points to the thing a reason is about: a section title, a
// sentence id ("item.seq") or an issue item id.
type apiTarget struct {
	Kind string `json:"kind"` // section | sentence | item | issue
	Ref  string `json:"ref"`
}

// apiReason is one reason an action is blocked.
type apiReason struct {
	Code    string     `json:"code"`
	Message string     `json:"message"`
	Target  *apiTarget `json:"target,omitempty"`
}

type apiErrorBody struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Reasons []apiReason `json:"reasons,omitempty"`
}

// apiErr writes the JSON error envelope.
func apiErr(w http.ResponseWriter, status int, code, msg string, reasons ...apiReason) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiErrorBody{Error: apiError{Code: code, Message: msg, Reasons: reasons}})
}

// apiJSON writes a JSON response.
func apiJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// apiDecode reads a JSON request body (empty body is allowed → false).
func apiDecode(r *http.Request, v any) (bool, error) {
	if r.Body == nil {
		return false, nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if err.Error() == "EOF" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Server) apiNotFound(w http.ResponseWriter, r *http.Request) {
	apiErr(w, http.StatusNotFound, "not_found", "no such resource: "+r.URL.Path)
}

// apiDBErr maps store errors: no rows → not_found, else logged internal.
func apiDBErr(w http.ResponseWriter, r *http.Request, err error, what string) bool {
	if err == nil {
		return false
	}
	if err == sql.ErrNoRows {
		apiErr(w, http.StatusNotFound, "not_found", what+" not found")
		return true
	}
	slog.Error("api request failed", "err", err, "path", r.URL.Path, "rid", reqIDOf(r))
	apiErr(w, http.StatusInternalServerError, "internal", "internal error")
	return true
}

// apiPubID parses a publication ref (id or slug) in the request path.
func (s *Server) apiPub(r *http.Request) (*store.Publication, error) {
	return s.pub(r)
}

// canonicalPub swaps a creator-made (custom) publication's prompt-bearing
// fields for the canonical demo sample. Demo fixtures replay by prompt
// hash, so the stored blocks are display-only — runs, section capacities
// and editorial prompts must use the sample's text. The signature matches
// GetPublication's so it wraps a load directly.
func (s *Server) canonicalPub(pub *store.Publication, err error) (*store.Publication, error) {
	if err != nil || s.d.Cfg.Mode != "demo" || pub == nil || !pub.Custom {
		return pub, err
	}
	def, derr := s.sampleDef()
	if derr != nil {
		return pub, err
	}
	cp := *pub
	cp.Language, cp.Topic, cp.Audience = def.Language, def.Topic, def.Audience
	cp.EditorialLine, cp.Tone, cp.Sections = def.EditorialLine, def.Tone, def.Sections
	return &cp, err
}

// sampleDef loads the embedded sample publication definition once —
// the proposal source for the create-newsletter screen and the
// canonical prompt input for custom publications in demo mode.
func (s *Server) sampleDef() (*config.Publication, error) {
	s.sampleOnce.Do(func() {
		data, err := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		if err == nil {
			s.sampleV, s.sampleErr = config.ParsePublication(data)
		} else {
			s.sampleErr = err
		}
	})
	return s.sampleV, s.sampleErr
}

// localizeReaderMD makes a rendered reader body speak the
// publication's own words: canonical section headings (which a custom
// publication replays internally for fixture compatibility) swap for
// the creator's titles, and boilerplate chrome translates for pt
// publications. Gating happens on canonical titles, so callers must
// gate first, then localize.
func (s *Server) localizeReaderMD(md string, pub *store.Publication) string {
	if pub == nil {
		return md
	}
	if pub.Custom && s.d.Cfg.Mode == "demo" {
		if def, err := s.sampleDef(); err == nil && len(def.Sections) == len(pub.Sections) {
			for i, cs := range def.Sections {
				if cs.Title == pub.Sections[i].Title {
					continue
				}
				md = regexp.MustCompile(`(?m)^##\s+`+regexp.QuoteMeta(cs.Title)+`$`).
					ReplaceAllString(md, "## "+pub.Sections[i].Title)
			}
		}
	}
	if strings.HasPrefix(strings.ToLower(pub.Language), "pt") {
		md = strings.ReplaceAll(md, "**Sources:**", "**Fontes:**")
		md = strings.ReplaceAll(md,
			"_Prepared with AI assistance and reviewed by the editor._",
			"_Preparado com auxílio de IA e revisado pelo editor._")
	}
	return md
}

func (s *Server) apiIssueID(r *http.Request) (*store.Issue, error) {
	return s.issue(r)
}

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

// --- helpers shared by handlers ---

// apiPublication is the publication + settings resource.
type apiPublication struct {
	ID            int64        `json:"id"`
	Slug          string       `json:"slug"`
	Name          string       `json:"name"`
	Custom        bool         `json:"custom"`
	Language      string       `json:"language"`
	Topic         string       `json:"topic"`
	Audience      string       `json:"audience"`
	EditorialLine string       `json:"editorial_line"`
	Tone          string       `json:"tone"`
	Frequency     string       `json:"frequency"`
	AIDisclosure  string       `json:"ai_disclosure"`
	Sections      []apiSection `json:"sections"`
	Paywall       apiPaywall   `json:"paywall"`
}

type apiSection struct {
	Title      string `json:"title"`
	Items      int    `json:"items"`
	Commentary string `json:"commentary"`
}

type apiPaywall struct {
	Enabled      bool     `json:"enabled"`
	PaidSections []string `json:"paid_sections"`
	Price        string   `json:"price"`
}

func publicationView(p *store.Publication) apiPublication {
	v := apiPublication{
		ID: p.ID, Slug: p.Slug, Name: p.Name, Custom: p.Custom, Language: p.Language,
		Topic: p.Topic, Audience: p.Audience, EditorialLine: p.EditorialLine,
		Tone: p.Tone, Frequency: p.Frequency, AIDisclosure: p.AIDisclosure,
		Paywall: apiPaywall{
			Enabled: p.Paywall.Enabled, PaidSections: p.Paywall.PaidSections,
			Price: p.Paywall.Price,
		},
	}
	for _, sc := range p.Sections {
		v.Sections = append(v.Sections, apiSection{
			Title: sc.Title, Items: sc.Items, Commentary: sc.Commentary,
		})
	}
	if v.Sections == nil {
		v.Sections = []apiSection{}
	}
	if v.Paywall.PaidSections == nil {
		v.Paywall.PaidSections = []string{}
	}
	return v
}

// apiIssueSummary is the issue resource in lists and detail.
func (s *Server) issueSummary(r *http.Request, iss *store.Issue) map[string]any {
	ctx := r.Context()
	counts := map[string]int{"pending": 0, "approved": 0, "rejected": 0, "alternative": 0}
	if items, err := db(r).IssueItems(ctx, iss.ID); err == nil {
		for _, ii := range items {
			counts[ii.Status]++
		}
	}
	missing := 0
	if pub, err := s.canonicalPub(db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID))); err == nil {
		for _, sc := range pub.Sections {
			if sc.Commentary == "required" && strings.TrimSpace(iss.Commentary[sc.Title]) == "" {
				missing++
			}
		}
	}
	m := map[string]any{
		"id": iss.ID, "publication_id": iss.PublicationID, "number": iss.Number,
		"status": iss.Status, "subject": iss.Subject, "created_at": iss.CreatedAt,
		"items":                       counts,
		"flags":                       len(iss.Flags),
		"missing_required_commentary": missing,
		"document_version":            docVersion(iss),
	}
	if iss.ScheduledAt != "" {
		m["scheduled_at"] = iss.ScheduledAt
	}
	if rec, err := db(r).ApprovalReceipt(ctx, iss.ID); err == nil {
		m["receipt"] = receiptView(rec)
	}
	return m
}

// orEmpty keeps the contract's arrays as [] — never null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// receiptView renders a stored approval receipt for the API.
func receiptView(rec *store.ApprovalReceipt) map[string]any {
	checks := []apiCheck{}
	json.Unmarshal([]byte(rec.ChecksJSON), &checks)
	return map[string]any{
		"issue_id": rec.IssueID, "number": rec.Number,
		"document_version": rec.DocumentVersion,
		"approver":         rec.Approver,
		"approved_at":      rec.ApprovedAt,
		"checks":           checks,
	}
}

// apiItem is one triage card.
type apiItem struct {
	ID        int64    `json:"id"`
	IssueID   int64    `json:"issue_id"`
	StoryID   int64    `json:"story_id"`
	Section   string   `json:"section"`
	Score     float64  `json:"score"`
	Reason    string   `json:"reason"`
	Status    string   `json:"status"`
	Origin    string   `json:"origin"`
	Title     string   `json:"title"`
	Outlets   []string `json:"outlets"`
	RejectWhy string   `json:"reject_reason,omitempty"`
}

func (s *Server) apiCardFor(r *http.Request, ii store.IssueItem) apiItem {
	ctx := r.Context()
	title, _ := db(r).StoryTitle(ctx, ii.StoryID)
	var names []string
	if story, err := db(r).StoryWithItems(ctx, ii.StoryID); err == nil {
		seen := map[string]bool{}
		for _, it := range story.Items {
			n := it.SourceName
			if n == "" {
				n = it.SourceURL
			}
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	card := apiItem{
		ID: ii.ID, IssueID: ii.IssueID, StoryID: ii.StoryID, Section: ii.Section,
		Score: ii.Score, Reason: ii.Reason, Status: ii.Status, Origin: ii.Origin,
		Title: title, Outlets: names,
	}
	if card.Outlets == nil {
		card.Outlets = []string{}
	}
	if ii.Status == "rejected" {
		if _, reason, err := db(r).LatestDecision(ctx, ii.ID); err == nil {
			card.RejectWhy = reason
		}
	}
	return card
}

func pendingDecisions(r *http.Request, issueID int64) int {
	items, err := db(r).IssueItems(r.Context(), issueID)
	if err != nil {
		return 0
	}
	n := 0
	for _, ii := range items {
		if ii.Status == "pending" {
			n++
		}
	}
	return n
}

// --- workspace / publications / sources ---

func (s *Server) apiWorkspace(w http.ResponseWriter, r *http.Request) {
	ws := reqWS(r)
	owner := s.workspaceOwner(r.Context(), db(r))
	mail := "outbox"
	if s.liveMail() {
		mail = "live"
	}
	apiJSON(w, 200, map[string]any{
		"id": ws.ID, "label": ws.Label, "mode": s.d.Cfg.Mode, "hosted": s.d.WS.Hosted(),
		"llm_mode": s.d.Cfg.LLMMode,
		"claimed":  owner != "", "owner_email": owner, "mail": mail,
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

// --- runs ---

// apiRun is the run resource snapshot.
type apiRun struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Status     string         `json:"status"` // running | done | failed
	Step       string         `json:"step"`
	IssueID    int64          `json:"issue_id,omitempty"`
	NoNewItems bool           `json:"no_new_items"`
	Error      string         `json:"error,omitempty"`
	Events     []runEventView `json:"events"`
}

type runEventView struct {
	Seq    int            `json:"seq"`
	Step   string         `json:"step"`
	Counts map[string]int `json:"counts,omitempty"`
}

func runView(st *runProgress) apiRun {
	st.mu.Lock()
	defer st.mu.Unlock()
	v := apiRun{
		ID: st.st.RunID, Kind: st.st.Kind, Step: st.st.Step,
		IssueID: st.st.IssueID, NoNewItems: st.st.NoNewItems, Error: st.st.Err,
		Status: "running",
	}
	if st.st.Done {
		v.Status = "done"
		if st.st.Err != "" {
			v.Status = "failed"
		}
	}
	for i, ev := range st.events {
		v.Events = append(v.Events, runEventView{Seq: i + 1, Step: ev.Step, Counts: ev.Counts})
	}
	if v.Events == nil {
		v.Events = []runEventView{}
	}
	return v
}

func (s *Server) apiRunStart(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	st := s.startRun(reqWS(r), pub)
	apiJSON(w, 202, map[string]any{"run": runView(st)})
}

func (s *Server) apiRunLatest(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	v, ok := reqWS(r).Runs.Load(pub.ID)
	if !ok {
		apiErr(w, 404, "no_runs", "no runs yet for this publication")
		return
	}
	apiJSON(w, 200, map[string]any{"run": runView(v.(*runProgress))})
}

func (s *Server) apiRun(w http.ResponseWriter, r *http.Request) {
	st, ok := findRun(reqWS(r), r.PathValue("id"))
	if !ok {
		apiErr(w, 404, "not_found", "run not found")
		return
	}
	apiJSON(w, 200, map[string]any{"run": runView(st)})
}

// apiRunEvents streams one SSE event per finished step with its real
// counts, then a final "done" event with the run snapshot.
func (s *Server) apiRunEvents(w http.ResponseWriter, r *http.Request) {
	st, ok := findRun(reqWS(r), r.PathValue("id"))
	if !ok {
		apiErr(w, 404, "not_found", "run not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiErr(w, 500, "internal", "streaming not supported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	send := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}
	sent := 0
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		st.mu.Lock()
		evs := append([]pipeline.StepEvent{}, st.events...)
		done := st.st.Done
		st.mu.Unlock()
		snap := runView(st)
		for ; sent < len(evs); sent++ {
			send("step", runEventView{Seq: sent + 1, Step: evs[sent].Step, Counts: evs[sent].Counts})
		}
		if done {
			send("done", snap)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

// --- issues / items / dossier ---

func (s *Server) apiIssues(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	issues, err := db(r).ListIssues(r.Context(), pub.ID)
	if apiDBErr(w, r, err, "issues") {
		return
	}
	out := []map[string]any{}
	for _, iss := range issues {
		out = append(out, s.issueSummary(r, iss))
	}
	apiJSON(w, 200, map[string]any{"issues": out})
}

func (s *Server) apiIssue(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	apiJSON(w, 200, map[string]any{"issue": s.issueSummary(r, iss)})
}

func (s *Server) apiIssueItems(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	items, _ := db(r).IssueItems(r.Context(), iss.ID)
	out := []apiItem{}
	for _, ii := range items {
		out = append(out, s.apiCardFor(r, ii))
	}
	apiJSON(w, 200, map[string]any{
		"items": out, "pending": pendingDecisions(r, iss.ID),
	})
}

// apiCandidate is one ranked story on the build board: in the issue or
// still on the bench.
type apiCandidate struct {
	StoryID          int64    `json:"story_id"`
	ItemID           int64    `json:"item_id,omitempty"`
	Title            string   `json:"title"`
	Outlets          []string `json:"outlets"`
	Sources          int      `json:"sources"`
	Duplicates       int      `json:"duplicates"`
	Score            float64  `json:"score"`
	Reason           string   `json:"reason"`
	Rank             int      `json:"rank"`
	InIssue          bool     `json:"in_issue"`
	Section          string   `json:"section,omitempty"`
	SuggestedSection string   `json:"suggested_section,omitempty"`
	Status           string   `json:"status,omitempty"`
	Origin           string   `json:"origin,omitempty"`
	Position         int      `json:"position"`
	Dossier          bool     `json:"dossier"`
	Available        bool     `json:"available"`
}

// demoCandidateCap is how many ranked stories a demo workspace can
// actually draft — replay fixtures only exist for the top of the list.
const demoCandidateCap = 14

func (s *Server) apiCandidates(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	cands, err := db(r).RankedCandidates(r.Context(), iss.PublicationID, iss.ID)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	out := s.candidateViews(r, cands)
	apiJSON(w, 200, map[string]any{
		"candidates": out,
		"sections":   sectionViews(pub),
		"demo":       s.d.Cfg.Mode == "demo",
		"llm_mode":   s.d.Cfg.LLMMode,
	})
}

func (s *Server) candidateViews(r *http.Request, cands []store.RankedCandidate) []apiCandidate {
	demo := s.d.Cfg.Mode == "demo"
	out := []apiCandidate{}
	for _, c := range cands {
		story, err := db(r).StoryWithItems(r.Context(), c.Story.ID)
		outlets := []string{}
		sources := 0
		if err == nil {
			seen := map[string]bool{}
			for _, it := range story.Items {
				n := it.SourceName
				if n == "" {
					n = it.SourceURL
				}
				if !seen[n] {
					seen[n] = true
					outlets = append(outlets, n)
				}
			}
			sources = len(story.Items)
		}
		cv := apiCandidate{
			StoryID: c.Story.ID, Title: c.Story.Title,
			Outlets: outlets, Sources: sources, Duplicates: c.Duplicates,
			Score: c.Story.TriageScore, Reason: c.Story.TriageReason, Rank: c.Rank,
			Dossier: c.Dossier, Available: true,
		}
		if c.Item != nil {
			cv.ItemID = c.Item.ID
			cv.Section = c.Item.Section
			cv.Status = c.Item.Status
			cv.Origin = c.Item.Origin
			cv.Position = c.Item.Position
			cv.InIssue = c.Item.Status == "pending" || c.Item.Status == "approved"
			// any prior placement is a valid suggestion: a rejected or
			// alternative story re-adds where it once sat
			cv.SuggestedSection = c.Item.Section
		}
		if demo && cv.Origin != "phaseA" && c.Rank > demoCandidateCap {
			cv.Available = false
		}
		out = append(out, cv)
	}
	return out
}

func sectionViews(pub *store.Publication) []map[string]any {
	out := []map[string]any{}
	for _, sec := range pub.Sections {
		out = append(out, map[string]any{
			"title": sec.Title, "capacity": sec.Items, "commentary": sec.Commentary,
		})
	}
	return out
}

// apiLineup replaces the issue lineup: which stories sit in which
// section, in which slot order. Items dropped from the lineup are
// 'removed' — not rejected — unless the caller uses the reject
// decision with a reason.
func (s *Server) apiLineup(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	var in struct {
		Entries []struct {
			StoryID int64  `json:"story_id"`
			Section string `json:"section"`
		} `json:"entries"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	ctx := r.Context()
	pub, err := s.canonicalPub(db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	caps := map[string]int{}
	for _, sec := range pub.Sections {
		caps[sec.Title] = sec.Items
	}
	cands, err := db(r).RankedCandidates(ctx, iss.PublicationID, iss.ID)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	valid := map[int64]*store.RankedCandidate{}
	for i := range cands {
		valid[cands[i].Story.ID] = &cands[i]
	}
	counts := map[string]int{}
	demo := s.d.Cfg.Mode == "demo"
	seen := map[int64]bool{}
	for _, e := range in.Entries {
		c, ok := valid[e.StoryID]
		if !ok {
			apiErr(w, 400, "unknown_story", fmt.Sprintf("story %d is not a ranked candidate of this run", e.StoryID))
			return
		}
		if seen[e.StoryID] {
			apiErr(w, 400, "duplicate_story", fmt.Sprintf("story %d appears twice in the lineup", e.StoryID))
			return
		}
		seen[e.StoryID] = true
		cap_, ok := caps[e.Section]
		if !ok {
			apiErr(w, 400, "unknown_section", fmt.Sprintf("section %q is not configured for this publication", e.Section))
			return
		}
		counts[e.Section]++
		if counts[e.Section] > cap_ {
			apiErr(w, 409, "section_full",
				fmt.Sprintf("section %q holds %d item(s) — the lineup asks for %d", e.Section, cap_, counts[e.Section]),
				apiReason{Code: "section_full",
					Message: fmt.Sprintf("%s is full (%d/%d)", e.Section, cap_, cap_),
					Target:  &apiTarget{Kind: "section", Ref: e.Section}})
			return
		}
		if demo && c.Rank > demoCandidateCap && (c.Item == nil || c.Item.Origin != "phaseA") {
			apiErr(w, 409, "demo_unavailable",
				"this story is not available in the demo — only the top-ranked stories can be added",
				apiReason{Code: "demo_unavailable",
					Message: "not available in the demo",
					Target:  &apiTarget{Kind: "story", Ref: fmt.Sprint(e.StoryID)}})
			return
		}
	}

	// apply: place every entry, then remove what fell out of the lineup
	reIncluded := false
	positions := map[string]int{}
	for _, e := range in.Entries {
		positions[e.Section]++
		pos := positions[e.Section] - 1
		if ii, err := db(r).IssueItemByStory(ctx, iss.ID, e.StoryID); err == nil {
			if ii.Status == "rejected" {
				reIncluded = true
			}
			if apiDBErr(w, r, db(r).SetIssueItemPlacement(ctx, ii.ID, e.Section, pos, "approved"), "item") {
				return
			}
		} else {
			c := valid[e.StoryID]
			if _, err := db(r).CreateIssueItem(ctx, store.IssueItem{
				IssueID: iss.ID, StoryID: e.StoryID, Section: e.Section,
				Score: c.Story.TriageScore, Reason: c.Story.TriageReason,
				Status: "approved", Origin: "manual", Position: pos,
			}); apiDBErr(w, r, err, "item") {
				return
			}
		}
	}
	items, _ := db(r).IssueItems(ctx, iss.ID)
	for _, ii := range items {
		if (ii.Status == "pending" || ii.Status == "approved") && !seen[ii.StoryID] {
			if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, ii.ID, "removed"), "item") {
				return
			}
		}
	}

	// re-including a rejected story undoes the rejection: the guide
	// must forget the rule it learned from it, same as a decision undo
	var guide any
	if reIncluded {
		guide = s.guideAfterRebuild(w, r, pub)
	}
	cands, _ = db(r).RankedCandidates(ctx, iss.PublicationID, iss.ID)
	resp := map[string]any{"candidates": s.candidateViews(r, cands)}
	if guide != nil {
		resp["guide"] = guide
	}
	apiJSON(w, 200, resp)
}

// apiStory serves a ranked story's member articles for the board's
// side sheet when no dossier exists yet.
func (s *Server) apiStory(w http.ResponseWriter, r *http.Request) {
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	story, err := db(r).StoryWithItems(r.Context(), sid)
	if apiDBErr(w, r, err, "story") {
		return
	}
	outlets := []string{}
	seen := map[string]bool{}
	items := []map[string]any{}
	for _, it := range story.Items {
		n := it.SourceName
		if n == "" {
			n = it.SourceURL
		}
		if !seen[n] {
			seen[n] = true
			outlets = append(outlets, n)
		}
		items = append(items, map[string]any{
			"id": it.ID, "title": it.Title, "url": "/a/" + fmt.Sprint(it.ID),
			"outlet": n, "published_at": it.PublishedAt,
			"snippet": truncateStr(it.ContentText, 400),
		})
	}
	apiJSON(w, 200, map[string]any{
		"story": map[string]any{
			"id": story.ID, "title": story.Title,
			"score": story.TriageScore, "reason": story.TriageReason,
			"outlets": outlets, "items": items,
		},
	})
}

func (s *Server) apiDossier(w http.ResponseWriter, r *http.Request) {
	iid, _ := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	ii, err := db(r).GetIssueItem(r.Context(), iid)
	if apiDBErr(w, r, err, "item") {
		return
	}
	dj, err := db(r).Dossier(r.Context(), iid)
	if apiDBErr(w, r, err, "dossier") {
		return
	}
	var dd pipeline.Dossier
	if err := json.Unmarshal([]byte(dj), &dd); err != nil {
		apiErr(w, 500, "internal", "dossier is unreadable")
		return
	}
	dd.Facts = orEmpty(dd.Facts)
	dd.DeepFacts = orEmpty(dd.DeepFacts)
	dd.Unknowns = orEmpty(dd.Unknowns)
	dd.Interpretation = orEmpty(dd.Interpretation)
	dd.Sources = orEmpty(dd.Sources)
	db(r).SetProgress(r.Context(), "dossier_seen")
	apiJSON(w, 200, map[string]any{
		"item":    s.apiCardFor(r, *ii),
		"dossier": dd,
	})
}

// apiDecision applies one triage decision: approve | reject |
// alternative | undo. Same rules as the HTML route.
func (s *Server) apiDecision(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iid, _ := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	ii, err := db(r).GetIssueItem(r.Context(), iid)
	if apiDBErr(w, r, err, "item") {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	var in struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	ctx := r.Context()
	pub, _ := s.canonicalPub(db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID)))
	var guide any

	finish := func(iid int64) {
		ii2, err := db(r).GetIssueItem(ctx, iid)
		if err == nil {
			ii = ii2
		}
		resp := map[string]any{
			"item":    s.apiCardFor(r, *ii),
			"pending": pendingDecisions(r, iss.ID),
		}
		if guide != nil {
			resp["guide"] = guide
		}
		apiJSON(w, 200, resp)
	}

	switch in.Kind {
	case "undo":
		if ii.Status != "rejected" {
			apiErr(w, 409, "not_rejected", "undo applies only to a rejected item")
			return
		}
		if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, iid, "pending"), "item") {
			return
		}
		db(r).RecordDecision(ctx, iss.PublicationID, iid, "undo", "")
		// an undone rejection must leave the editorial guide too
		if pub != nil {
			guide = s.guideAfterRebuild(w, r, pub)
		}
		finish(iid)
		return
	case "approve":
		if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, iid, "approved"), "item") {
			return
		}
		db(r).RecordDecision(ctx, iss.PublicationID, iid, "approve", in.Reason)
	case "reject":
		if strings.TrimSpace(in.Reason) == "" {
			apiErr(w, 400, "reason_required",
				"a rejection needs a reason — it feeds the editorial guide",
				apiReason{Code: "reason_required", Message: "provide a rejection reason",
					Target: &apiTarget{Kind: "item", Ref: fmt.Sprint(iid)}})
			return
		}
		if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, iid, "rejected"), "item") {
			return
		}
		db(r).RecordDecision(ctx, iss.PublicationID, iid, "reject", in.Reason)
		if pub != nil {
			guide = s.guideAfterRebuild(w, r, pub)
		}
	case "alternative":
		if pub == nil {
			apiErr(w, 500, "internal", "internal error")
			return
		}
		runner := &pipeline.Runner{
			DB: db(r), LLM: s.d.LLM, Fetcher: s.d.Fetcher,
			TriageModel: s.d.Cfg.ModelTriage, DraftModel: s.d.Cfg.ModelDraft,
			DemoMode: s.d.Cfg.Mode == "demo", Deterministic: s.d.Cfg.LLMOff(),
		}
		newII, err := runner.SubstituteAlternative(ctx, pub, iss, ii)
		if err == sql.ErrNoRows {
			apiErr(w, 409, "no_alternative", "there is no alternative candidate for this item")
			return
		}
		if apiDBErr(w, r, err, "item") {
			return
		}
		db(r).RecordDecision(ctx, iss.PublicationID, iid, "alternative",
			fmt.Sprintf("replaced by item %d (score %.1f)", newII.ID, newII.Score))
		ii = newII
	default:
		apiErr(w, 400, "validation", "kind must be approve|reject|alternative|undo")
		return
	}
	if iss.Status == "approved" {
		// a decision after approval returns the issue to draft
		db(r).RevertIssueToDraft(ctx, iss.ID)
	}
	finish(ii.ID)
}

// guideAfterRebuild rebuilds the editorial guide and reports what it
// learned ("Rule learned: …" toasts in the new interface).
func (s *Server) guideAfterRebuild(w http.ResponseWriter, r *http.Request, pub *store.Publication) any {
	prevV, prevC, _ := db(r).LatestGuide(r.Context(), pub.ID)
	content, version, err := editorial.Rebuild(r.Context(), db(r), pub, s.d.LLM, s.d.Cfg.ModelTriage)
	if errors.Is(err, llm.ErrDisabled) {
		return nil // deterministic mode: guide rebuild is an AI action — skip
	}
	if err != nil {
		return nil
	}
	out := map[string]any{"version": version}
	if version != prevV {
		out["added"] = addedLines(prevC, content)
	}
	return out
}

// --- readiness / approve / draft ---

// apiCheck is one readiness-check entry.
type apiCheck struct {
	Code    string     `json:"code"`
	OK      bool       `json:"ok"`
	Count   int        `json:"count,omitempty"` // failing items, so clients can localize
	Message string     `json:"message,omitempty"`
	Target  *apiTarget `json:"target,omitempty"`
}

// readinessChecks builds the checklist: every pauta decided, every
// factual sentence sourced, no flags, required commentary written.
func (s *Server) readinessChecks(r *http.Request, iss *store.Issue, pub *store.Publication, doc *issueDocument) []apiCheck {
	checks := []apiCheck{}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		checks = append(checks, apiCheck{Code: "read_only", OK: false,
			Message: "this issue is sent or scheduled — locked issues are read-only"})
	}
	if doc == nil {
		doc, _ = buildIssueDocument(r.Context(), db(r), iss, pub)
	}

	pending := pendingDecisions(r, iss.ID)
	c := apiCheck{Code: "pending_decisions", OK: pending == 0, Count: pending,
		Message: fmt.Sprintf("%d story decision(s) still open", pending)}
	if !c.OK {
		c.Target = &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}
	}
	checks = append(checks, c)

	uncited, firstUncited := 0, ""
	for _, sec := range doc.Sections {
		for _, it := range sec.Items {
			for _, sn := range it.Sentences {
				if sn.Kind == "fact" && len(sn.Citations) == 0 {
					uncited++
					if firstUncited == "" {
						firstUncited = sn.ID
					}
				}
			}
		}
	}
	c = apiCheck{Code: "uncited_sentences", OK: uncited == 0, Count: uncited,
		Message: fmt.Sprintf("%d sentence(s) without a citation", uncited)}
	if !c.OK {
		c.Target = &apiTarget{Kind: "sentence", Ref: firstUncited}
	}
	checks = append(checks, c)

	c = apiCheck{Code: "flags_unresolved", OK: len(iss.Flags) == 0, Count: len(iss.Flags),
		Message: fmt.Sprintf("%d flagged sentence(s) unresolved — edit, mark as interpretation or remove each", len(iss.Flags))}
	if !c.OK {
		// point at the first flagged sentence we can locate
		ref := ""
	search:
		for _, sec := range doc.Sections {
			for _, it := range sec.Items {
				for _, sn := range it.Sentences {
					if sn.Flag != nil {
						ref = sn.ID
						break search
					}
				}
			}
		}
		c.Target = &apiTarget{Kind: "sentence", Ref: ref}
	}
	checks = append(checks, c)

	missing := []string{}
	if pub != nil {
		for _, sc := range pub.Sections {
			if sc.Commentary == "required" && strings.TrimSpace(iss.Commentary[sc.Title]) == "" {
				missing = append(missing, sc.Title)
				checks = append(checks, apiCheck{Code: "commentary_required", OK: false,
					Message: fmt.Sprintf("commentary required in %q", sc.Title),
					Target:  &apiTarget{Kind: "section", Ref: sc.Title}})
			}
		}
	}
	if len(missing) == 0 {
		checks = append(checks, apiCheck{Code: "commentary_required", OK: true})
	}
	return checks
}

func (s *Server) apiReadiness(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return
	}
	checks := s.readinessChecks(r, iss, pub, doc)
	ready := true
	for _, c := range checks {
		if !c.OK {
			ready = false
		}
	}
	apiJSON(w, 200, map[string]any{
		"ready": ready, "checks": checks, "coverage": doc.Coverage,
	})
}

// apiDraft starts Phase B (draft + citecheck) as a run: GET
// /api/v1/runs/{id}/events streams draft/citecheck steps, then the
// issue document is ready.
func (s *Server) apiDraft(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	// the build board owns the lineup now: every configured section
	// needs at least one item in it before the issue can be drafted
	lineup, _ := db(r).IssueItems(r.Context(), iss.ID)
	filled := map[string]int{}
	for _, ii := range lineup {
		if ii.Status == "pending" || ii.Status == "approved" {
			filled[ii.Section]++
		}
	}
	var reasons []apiReason
	for _, sec := range pub.Sections {
		if filled[sec.Title] == 0 {
			reasons = append(reasons, apiReason{Code: "section_empty",
				Message: fmt.Sprintf("section %q has no stories", sec.Title),
				Target:  &apiTarget{Kind: "section", Ref: sec.Title}})
		}
	}
	if len(reasons) > 0 {
		apiErr(w, 409, "blocked", fmt.Sprintf("%d section(s) still empty — fill each one before drafting", len(reasons)), reasons...)
		return
	}
	for _, ii := range lineup {
		if ii.Status == "pending" {
			if apiDBErr(w, r, db(r).SetIssueItemStatus(r.Context(), ii.ID, "approved"), "item") {
				return
			}
		}
	}
	wsp := reqWS(r)
	st := &runProgress{st: runState{RunID: newReqID(), Kind: "draft", Step: "starting", IssueID: iss.ID}}
	key := "draft-" + fmt.Sprint(iss.ID)
	if prev, running := wsp.Runs.LoadOrStore(key, st); running {
		pst := prev.(*runProgress)
		pst.mu.Lock()
		done := pst.st.Done
		pst.mu.Unlock()
		if !done {
			apiJSON(w, 202, map[string]any{"run": runView(pst)})
			return
		}
		wsp.Runs.Store(key, st)
	}
	go func() {
		wsp.Activity.RLock()
		defer wsp.Activity.RUnlock()
		// Same panic guard as prepare runs (review F5).
		defer func() {
			if p := recover(); p != nil {
				st.mu.Lock()
				defer st.mu.Unlock()
				st.st.Done = true
				st.st.Err = fmt.Sprintf("pipeline panic: %v", p)
				slog.Error("draft run panicked", "issue", iss.ID, "panic", p, "rid", st.st.RunID)
			}
		}()
		runner := &pipeline.Runner{
			DB: wsp.DB, LLM: s.d.LLM, Fetcher: s.d.Fetcher,
			TriageModel: s.d.Cfg.ModelTriage, DraftModel: s.d.Cfg.ModelDraft,
			MaxDeepDives: s.d.Cfg.MaxDeepDives,
			DemoMode:     s.d.Cfg.Mode == "demo",
			Deterministic: s.d.Cfg.LLMOff(),
			OnStep: func(step string) {
				st.mu.Lock()
				st.st.Step = step
				st.mu.Unlock()
			},
			OnEvent: func(ev pipeline.StepEvent) {
				st.mu.Lock()
				st.events = append(st.events, ev)
				st.mu.Unlock()
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.runTimeout())
		defer cancel()
		res, err := runner.RunPhaseB(ctx, pub, iss.ID)
		st.mu.Lock()
		defer st.mu.Unlock()
		st.st.Done = true
		if err != nil {
			st.st.Err = err.Error()
			return
		}
		st.st.IssueID = res.IssueID
	}()
	apiJSON(w, 202, map[string]any{"run": runView(st)})
}

// --- document / sentence mutations / commentary ---

func (s *Server) apiDocument(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return
	}
	apiJSON(w, 200, map[string]any{"document": doc})
}

// apiSentenceCtx loads issue, document and locates a sentence.
func (s *Server) apiSentenceCtx(w http.ResponseWriter, r *http.Request) (*store.Issue, *issueDocument, *locatedSentence, bool) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return nil, nil, nil, false
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return nil, nil, nil, false
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return nil, nil, nil, false
	}
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return nil, nil, nil, false
	}
	loc := doc.findSentence(r.PathValue("sid"))
	if loc == nil {
		apiErr(w, 404, "not_found", "sentence not found")
		return nil, nil, nil, false
	}
	return iss, doc, loc, true
}

// dropFlag removes the flag matching (item ref, raw sentence); reports
// whether one was dropped.
func dropFlag(r *http.Request, iss *store.Issue, itemRef int64, raw string) (bool, error) {
	flags := iss.Flags
	out := flags[:0]
	dropped := false
	for _, f := range flags {
		if f.Item == itemRef && f.Sentence == raw {
			dropped = true
			continue
		}
		out = append(out, f)
	}
	if !dropped {
		return false, nil
	}
	return true, db(r).ResolveFlags(r.Context(), iss.ID, out)
}

// revertApproved returns an approved issue to drafted after an edit —
// which also invalidates the stored approval receipt.
func revertApproved(r *http.Request, iss *store.Issue) {
	if iss.Status == "approved" {
		db(r).RevertIssueToDraft(r.Context(), iss.ID)
		iss.Status = "drafted"
	}
}

// checkDocVersion enforces the document's optimistic-concurrency token:
// every document mutation must send back the version it read. A stale
// or missing version answers 409 conflict with the fresh document.
func checkDocVersion(w http.ResponseWriter, doc *issueDocument, v string) bool {
	if v != "" && v == doc.Version {
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(map[string]any{
		"error": apiError{Code: "conflict",
			Message: "the document changed since this version — reload and retry the edit"},
		"document": doc,
	})
	return false
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// apiSentenceEdit replaces a sentence's text. The client sends the
// display form — {"text": "…", "citations": [1]} — and the server
// composes the stored form (markers appended after the words). Fact
// sentences re-run the citation check: the refs must be sources of that
// item and the claim must stay supported — the flag (or the write) is
// refused otherwise. Interpretation sentences carry no citation
// requirement.
func (s *Server) apiSentenceEdit(w http.ResponseWriter, r *http.Request) {
	iss, doc, loc, ok := s.apiSentenceCtx(w, r)
	if !ok {
		return
	}
	var in struct {
		Text      string `json:"text"`
		Citations []int  `json:"citations"`
		Version   string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	text := displayText(in.Text) // the contract takes marker-free text
	if text == "" {
		apiErr(w, 400, "validation", "edited sentence cannot be empty",
			apiReason{Code: "empty_sentence", Message: "edited sentence cannot be empty",
				Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
		return
	}
	ctx := r.Context()
	var replacement string
	if loc.sentence.Kind == "interpretation" {
		replacement = "> _Interpretação: " + text + "_"
	} else {
		// fact: citations must point at this item's sources and the new
		// claim must stay supported by them
		d, _ := s.itemDossierByID(r, loc.item)
		maxRef := 0
		if d != nil {
			maxRef = len(d.Sources)
		}
		var bad string
		for _, n := range in.Citations {
			if n < 1 || (maxRef > 0 && n > maxRef) {
				bad = fmt.Sprintf("[%d]", n)
				break
			}
		}
		if bad != "" {
			apiErr(w, 409, "invalid_citation",
				fmt.Sprintf("source %s does not exist for this item — cite [1]..[%d] or mark as interpretation", bad, maxRef),
				apiReason{Code: "invalid_citation", Message: "citation ref out of range",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		if len(in.Citations) == 0 {
			apiErr(w, 409, "citation_required",
				"edited sentence needs a citation — or mark it as interpretation",
				apiReason{Code: "citation_required", Message: "factual sentences need a citation",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		if text == loc.sentence.Text && equalInts(in.Citations, loc.sentence.Citations) {
			apiErr(w, 409, "unchanged",
				"the sentence is unchanged — the flag stays until the claim is fixed, marked as interpretation, or removed",
				apiReason{Code: "unchanged", Message: "sentence text is unchanged",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		composed := text
		seen := map[int]bool{}
		for _, n := range in.Citations {
			if seen[n] {
				continue
			}
			seen[n] = true
			composed += fmt.Sprintf(" [%d]", n)
		}
		if !s.editSupportedFor(r, loc.item, int64(loc.item.Ref), composed) {
			apiErr(w, 409, "unsupported_claim",
				"the citation check still cannot verify this sentence against the cited source — the flag stays",
				apiReason{Code: "unsupported_claim", Message: "edit could not be verified against the cited source",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		replacement = composed
	}
	body, ok := replaceInItem(iss.BodyMD, loc.item, loc.sentence.raw, replacement)
	if !ok {
		apiErr(w, 409, "sentence_gone", "the sentence is no longer in the body")
		return
	}
	if apiDBErr(w, r, db(r).SetIssueBody(ctx, iss.ID, body), "issue") {
		return
	}
	dropped, err := dropFlag(r, iss, int64(loc.item.Ref), loc.sentence.raw)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	kind := "sentence_edit"
	if dropped {
		kind = "flag_edit"
	}
	db(r).RecordDecision(ctx, iss.PublicationID, loc.item.IssueItemID, kind,
		loc.sentence.raw+" → "+text)
	revertApproved(r, iss)
	s.respondSentence(w, r, iss, nil)
}

// apiSentenceRemove deletes a sentence (and its flag when present). The
// response carries the removed payload so the client can offer Undo —
// POST /issues/{id}/items/{ref}/sentences restores it verbatim.
func (s *Server) apiSentenceRemove(w http.ResponseWriter, r *http.Request) {
	iss, doc, loc, ok := s.apiSentenceCtx(w, r)
	if !ok {
		return
	}
	var in struct {
		Version string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	// capture the undo payload before the write: position inside the
	// item, display text, citations, kind and the flag it carried
	index := 0
	for i, sn := range loc.item.Sentences {
		if sn.ID == loc.sentence.ID {
			index = i
			break
		}
	}
	removed := map[string]any{
		"ref": loc.item.Ref, "index": index, "text": loc.sentence.Text,
		"kind": loc.sentence.Kind, "citations": orEmpty(loc.sentence.Citations),
	}
	if loc.sentence.Flag != nil {
		removed["flag"] = loc.sentence.Flag.Note
	}
	ctx := r.Context()
	body, ok := removeInItem(iss.BodyMD, loc.item, loc.sentence.raw)
	if !ok {
		apiErr(w, 409, "sentence_gone", "the sentence is no longer in the body")
		return
	}
	if apiDBErr(w, r, db(r).SetIssueBody(ctx, iss.ID, body), "issue") {
		return
	}
	dropped, err := dropFlag(r, iss, int64(loc.item.Ref), loc.sentence.raw)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	kind := "sentence_remove"
	if dropped {
		kind = "flag_remove"
	}
	db(r).RecordDecision(ctx, iss.PublicationID, loc.item.IssueItemID, kind, loc.sentence.raw)
	revertApproved(r, iss)
	s.respondSentence(w, r, iss, map[string]any{"removed": removed})
}

// apiSentenceRestore re-inserts a removed sentence into its item —
// the Undo for apiSentenceRemove. The client sends back the payload
// DELETE returned; the server composes the stored form at the same
// index inside the item's span and re-attaches the flag it carried.
func (s *Server) apiSentenceRestore(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return
	}
	ref, _ := strconv.Atoi(r.PathValue("ref"))
	var item *docItem
	for si := range doc.Sections {
		for ii := range doc.Sections[si].Items {
			if doc.Sections[si].Items[ii].Ref == ref {
				item = &doc.Sections[si].Items[ii]
			}
		}
	}
	if item == nil {
		apiErr(w, 404, "not_found", "item not found in the document")
		return
	}
	var in struct {
		Index     int    `json:"index"`
		Text      string `json:"text"`
		Kind      string `json:"kind"`
		Citations []int  `json:"citations"`
		Flag      string `json:"flag,omitempty"`
		Version   string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	text := displayText(in.Text)
	if text == "" {
		apiErr(w, 400, "validation", "restored sentence cannot be empty")
		return
	}
	var raw string
	if in.Kind == "interpretation" {
		raw = "> _Interpretação: " + text + "_"
	} else {
		d, _ := s.itemDossierByID(r, item)
		maxRef := 0
		if d != nil {
			maxRef = len(d.Sources)
		}
		raw = text
		seen := map[int]bool{}
		for _, n := range in.Citations {
			if n < 1 || (maxRef > 0 && n > maxRef) {
				apiErr(w, 409, "invalid_citation",
					fmt.Sprintf("source [%d] does not exist for this item", n))
				return
			}
			if seen[n] {
				continue
			}
			seen[n] = true
			raw += fmt.Sprintf(" [%d]", n)
		}
	}
	if in.Index < 0 {
		in.Index = 0
	}
	body, ok := insertInItem(iss.BodyMD, item, in.Index, raw)
	if !ok {
		apiErr(w, 409, "conflict", "the item no longer accepts this sentence")
		return
	}
	ctx := r.Context()
	if apiDBErr(w, r, db(r).SetIssueBody(ctx, iss.ID, body), "issue") {
		return
	}
	if in.Flag != "" {
		flags := append(append([]store.Flag{}, iss.Flags...),
			store.Flag{Item: int64(ref), Sentence: raw, Note: in.Flag})
		if apiDBErr(w, r, db(r).ResolveFlags(ctx, iss.ID, flags), "issue") {
			return
		}
	}
	db(r).RecordDecision(ctx, iss.PublicationID, item.IssueItemID, "sentence_restore", raw)
	revertApproved(r, iss)
	s.respondSentence(w, r, iss, nil)
}

// apiSentenceInterpretation marks a sentence as interpretation: it is
// stored as an editor-attributed block, so the citation check no longer
// applies — the flag, when present, is resolved.
func (s *Server) apiSentenceInterpretation(w http.ResponseWriter, r *http.Request) {
	iss, doc, loc, ok := s.apiSentenceCtx(w, r)
	if !ok {
		return
	}
	var in struct {
		Text    string `json:"text"`
		Version string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		text = displayText(loc.sentence.raw)
	}
	if text == "" {
		apiErr(w, 400, "validation", "interpretation text cannot be empty")
		return
	}
	ctx := r.Context()
	replacement := "\n> _Interpretação: " + text + "_\n"
	body, ok := replaceInItem(iss.BodyMD, loc.item, loc.sentence.raw, replacement)
	if !ok {
		apiErr(w, 409, "sentence_gone", "the sentence is no longer in the body")
		return
	}
	if apiDBErr(w, r, db(r).SetIssueBody(ctx, iss.ID, body), "issue") {
		return
	}
	dropped, err := dropFlag(r, iss, int64(loc.item.Ref), loc.sentence.raw)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	kind := "mark_interpretation"
	if dropped {
		kind = "flag_interpretation"
	}
	db(r).RecordDecision(ctx, iss.PublicationID, loc.item.IssueItemID, kind, loc.sentence.raw)
	revertApproved(r, iss)
	s.respondSentence(w, r, iss, nil)
}

// respondSentence re-parses the document after a mutation and answers
// with fresh coverage (and the mutated sentence when it still exists).
// extra keys (e.g. "removed" for undo) are merged into the response.
func (s *Server) respondSentence(w http.ResponseWriter, r *http.Request, iss *store.Issue, extra map[string]any) {
	fresh, err := db(r).GetIssue(r.Context(), iss.ID)
	if err == nil {
		iss = fresh
	}
	pub, _ := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return
	}
	resp := map[string]any{"coverage": doc.Coverage, "flags": len(iss.Flags), "version": doc.Version}
	if loc := doc.findSentence(r.PathValue("sid")); loc != nil {
		resp["sentence"] = loc.sentence
	}
	for k, v := range extra {
		resp[k] = v
	}
	apiJSON(w, 200, resp)
}

// apiCommentary saves the editor's commentary for a section. The AI
// never writes here — this is the only write path.
func (s *Server) apiCommentary(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	var in struct {
		Section string `json:"section"`
		Text    string `json:"text"`
		Version string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if in.Version != docVersion(iss) {
		pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
		if apiDBErr(w, r, err, "publication") {
			return
		}
		doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
		if apiDBErr(w, r, err, "document") {
			return
		}
		checkDocVersion(w, doc, in.Version)
		return
	}
	if strings.TrimSpace(in.Section) == "" {
		apiErr(w, 400, "validation", "section is required")
		return
	}
	c := iss.Commentary
	if c == nil {
		c = map[string]string{}
	}
	c[in.Section] = in.Text
	if apiDBErr(w, r, db(r).SaveCommentary(r.Context(), iss.ID, c), "issue") {
		return
	}
	revertApproved(r, iss)
	// docVersion below must see the just-written commentary — when
	// iss.Commentary was nil, c is a fresh map that iss does not alias
	iss.Commentary = c
	apiJSON(w, 200, map[string]any{
		"commentary": map[string]string{"section": in.Section, "text": in.Text},
		"version":    docVersion(iss),
	})
}

// --- approve / send / export / preview ---

func (s *Server) apiApprove(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	checks := s.readinessChecks(r, iss, pub, nil)
	var reasons []apiReason
	for _, c := range checks {
		if !c.OK {
			reasons = append(reasons, apiReason{Code: c.Code, Message: c.Message, Target: c.Target})
		}
	}
	if len(reasons) > 0 {
		apiErr(w, 409, "blocked", "the issue is not ready to approve — fix each reason first", reasons...)
		return
	}
	rec, err := s.recordApproval(r, iss, pub, checks)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	fresh, _ := db(r).GetIssue(r.Context(), iss.ID)
	apiJSON(w, 200, map[string]any{"issue": s.issueSummary(r, fresh), "receipt": receiptView(rec)})
}

// recordApproval marks the issue approved (when it is not already) and
// returns the stored receipt. The first receipt wins — a repeated
// approve returns the same receipt, never a new timestamp.
func (s *Server) recordApproval(r *http.Request, iss *store.Issue, pub *store.Publication, checks []apiCheck) (*store.ApprovalReceipt, error) {
	ctx := r.Context()
	if iss.Status != "approved" {
		if err := db(r).SetIssueStatus(ctx, iss.ID, "approved"); err != nil {
			return nil, err
		}
		iss.Status = "approved" // the receipt attests to the approved document
		db(r).RecordDecision(ctx, iss.PublicationID, 0, "approve", "issue approved")
	}
	rec, err := db(r).ApprovalReceipt(ctx, iss.ID)
	if err != sql.ErrNoRows {
		return rec, err
	}
	if checks == nil {
		checks = s.readinessChecks(r, iss, pub, nil)
	}
	cj, _ := json.Marshal(checks)
	rec = &store.ApprovalReceipt{
		IssueID: iss.ID, Number: iss.Number,
		DocumentVersion: docVersion(iss),
		Approver:        reqWS(r).Label,
		ChecksJSON:      string(cj),
	}
	if err := db(r).SaveApprovalReceipt(ctx, rec); err != nil {
		return nil, err
	}
	return db(r).ApprovalReceipt(ctx, iss.ID)
}

func (s *Server) apiSend(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Audience string `json:"audience"` // test | readers (safe default: explicit choice required)
		To       string `json:"to"`       // optional test address
		When     string `json:"when"`     // now | schedule — scheduling locks the issue until Hold
		At       string `json:"at"`       // RFC3339 send time, required when when=schedule
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if in.Audience != "test" && in.Audience != "readers" {
		apiErr(w, 400, "validation", "audience is required: test | readers — sending needs an explicit choice")
		return
	}
	if in.When == "" {
		in.When = "now"
	}
	if in.When != "now" && in.When != "schedule" {
		apiErr(w, 400, "validation", "when must be now | schedule")
		return
	}
	if in.When == "schedule" && in.Audience != "readers" {
		apiErr(w, 400, "validation", "scheduling applies to a reader send — a test send goes out now")
		return
	}
	if iss.Status != "approved" {
		apiErr(w, 409, "blocked", "the issue must be approved before sending",
			apiReason{Code: "not_approved", Message: "issue is not approved",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	if in.When == "schedule" {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(in.At))
		if err != nil {
			apiErr(w, 400, "bad_time", "scheduling needs a send time: at must be RFC3339 (e.g. 2026-01-04T08:00:00Z)")
			return
		}
		if !at.After(time.Now()) {
			apiErr(w, 400, "bad_time", "the scheduled time must be in the future")
			return
		}
		atStr := at.UTC().Format(time.RFC3339)
		// demo mode has no dispatch clock: a scheduled issue is locked
		// and stays queued until Hold returns it to draft.
		if apiDBErr(w, r, db(r).ScheduleIssue(r.Context(), iss.ID, atStr), "issue") {
			return
		}
		db(r).RecordDecision(r.Context(), iss.PublicationID, 0, "schedule", "issue scheduled for "+atStr)
		fresh, _ := db(r).GetIssue(r.Context(), iss.ID)
		apiJSON(w, 200, map[string]any{
			"issue": s.issueSummary(r, fresh), "scheduled": true, "scheduled_at": atStr,
		})
		return
	}
	if !s.liveMail() {
		// Outbox/demo mode: one recorded message, no claim gate.
		to := []string{"readers@kloxus.demo"}
		kind := "issue"
		if in.Audience == "test" {
			addr := strings.TrimSpace(in.To)
			if addr == "" {
				addr = demoReaderEmail
			}
			to = []string{addr}
			kind = "test"
		}
		emailID, err := s.sendIssueMail(r.Context(), db(r), reqWS(r).ID, iss, pub, to, kind, requestBase(r))
		if apiDBErr(w, r, err, "send") {
			return
		}
		if kind == "issue" {
			if apiDBErr(w, r, db(r).SetIssueStatus(r.Context(), iss.ID, "sent"), "issue") {
				return
			}
			if fresh, _ := db(r).GetIssue(r.Context(), iss.ID); fresh != nil {
				iss = fresh
			}
		}
		apiJSON(w, 200, map[string]any{
			"issue": s.issueSummary(r, iss), "outbox_email_id": emailID, "audience": in.Audience,
			"to": to, "subject": iss.Subject, "delivery": "outbox",
		})
		return
	}

	// Live delivery (D33): the workspace must be claimed; test sends go
	// to the claimed address only; reader sends go to confirmed readers.
	ctx := r.Context()
	wdb := db(r)
	owner := s.workspaceOwner(ctx, wdb)
	if owner == "" {
		apiErr(w, 409, "unclaimed_workspace",
			"claim the workspace before sending — verify the creator email in workspace settings")
		return
	}
	kind := "issue"
	var readers []store.Reader
	if in.Audience == "test" {
		kind = "test"
		if addr := strings.TrimSpace(in.To); addr != "" && !strings.EqualFold(addr, owner) {
			apiErr(w, 400, "validation", "test sends go to the claimed address only")
			return
		}
	} else {
		var err error
		readers, err = wdb.ConfirmedReaders(ctx, iss.PublicationID)
		if apiDBErr(w, r, err, "readers") {
			return
		}
	}
	// An explicit Idempotency-Key namespaces the per-recipient keys, so a
	// retried request can't mail the same reader twice.
	idemNS := ""
	if k := strings.TrimSpace(r.Header.Get("Idempotency-Key")); k != "" {
		idemNS = "h/" + k
	}
	sum, err := s.sendIssueLive(ctx, wdb, reqWS(r).ID, iss, pub, kind, owner, requestBase(r), idemNS, readers)
	if apiDBErr(w, r, err, "send") {
		return
	}
	if kind == "test" && len(sum.Refused) > 0 {
		apiErr(w, 429, "send_refused", "test send refused: "+strings.Join(sum.Refused, "; "))
		return
	}
	// A test send only records the mail — the issue stays approved so
	// it can still go to readers. A readers send marks it sent only
	// when something left the app: an all-refused or empty send keeps
	// it approved so it can go out once deliverable.
	if kind == "issue" && sum.Sent+sum.Fallback+sum.Deduped > 0 {
		if apiDBErr(w, r, db(r).SetIssueStatus(r.Context(), iss.ID, "sent"), "issue") {
			return
		}
		if fresh, _ := db(r).GetIssue(r.Context(), iss.ID); fresh != nil {
			iss = fresh
		}
	}
	deliv := "none"
	switch {
	case sum.Sent > 0 && sum.Fallback > 0:
		deliv = "mixed"
	case sum.Sent > 0:
		deliv = "resend"
	case sum.Fallback > 0:
		deliv = "outbox_fallback"
	}
	resp := map[string]any{
		"issue": s.issueSummary(r, iss), "outbox_email_id": sum.FirstID, "audience": in.Audience,
		"to": sum.To, "subject": iss.Subject, "delivery": deliv,
		"sent": sum.Sent, "refused": sum.Refused, "deduped": sum.Deduped,
	}
	if sum.Fallback > 0 {
		resp["fallback"] = true
	}
	if kind == "issue" && sum.Sent+sum.Fallback+sum.Deduped == 0 {
		resp["warnings"] = []string{"nothing was delivered — the issue stayed approved and can be sent again"}
	}
	apiJSON(w, 200, resp)
}

// apiHold unschedules a queued issue: the spec's Hold returns a
// scheduled (locked) issue to draft so it can be edited and approved
// again. Hold is a no-op for anything not scheduled.
func (s *Server) apiHold(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status != "scheduled" {
		apiErr(w, 409, "validation", "only a scheduled issue can be held")
		return
	}
	if apiDBErr(w, r, db(r).RevertIssueToDraft(r.Context(), iss.ID), "issue") {
		return
	}
	db(r).RecordDecision(r.Context(), iss.PublicationID, 0, "hold", "scheduled issue held")
	fresh, _ := db(r).GetIssue(r.Context(), iss.ID)
	apiJSON(w, 200, map[string]any{"issue": s.issueSummary(r, fresh)})
}

func (s *Server) apiExport(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	if iss.Status != "approved" && iss.Status != "sent" {
		apiErr(w, 409, "blocked", "exports ship only approved output — approve the issue first",
			apiReason{Code: "not_approved", Message: "issue is not approved",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	md := delivery.ReaderBody(iss, pub, requestBase(r))
	switch r.URL.Query().Get("fmt") {
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=issue-%d.html", iss.Number))
		w.Write([]byte(delivery.RenderHTML(iss.Subject, md, requestBase(r))))
	default:
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=issue-%d.md", iss.Number))
		w.Write([]byte(md))
	}
}

// apiPreview renders the reader-facing body with the two preview
// switches: reader (subscriber | non-subscriber) gates paid sections;
// surface (email | web | phone) is presentation-only and echoed back.
func (s *Server) apiPreview(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	rawPub, loadErr := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	pub, err := s.canonicalPub(rawPub, loadErr)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	reader := r.URL.Query().Get("reader")
	if reader == "" {
		reader = "subscriber"
	}
	surface := r.URL.Query().Get("surface")
	if surface == "" {
		surface = "email"
	}
	if reader != "subscriber" && reader != "non-subscriber" {
		apiErr(w, 400, "validation", "reader must be subscriber | non-subscriber")
		return
	}
	if surface != "email" && surface != "web" && surface != "phone" {
		apiErr(w, 400, "validation", "surface must be email | web | phone")
		return
	}
	md := delivery.ReaderBody(iss, pub, requestBase(r))
	gated := false
	if reader == "non-subscriber" {
		md = gateSections(md, pub.Paywall.PaidSections, pub.Language)
		gated = true
	}
	md = s.localizeReaderMD(md, rawPub)
	apiJSON(w, 200, map[string]any{
		"body_md": md, "gated": gated, "reader": reader, "surface": surface,
	})
}

// --- outbox ---

func (s *Server) apiOutbox(w http.ResponseWriter, r *http.Request) {
	if _, err := s.apiPub(r); apiDBErr(w, r, err, "publication") {
		return
	}
	emails, _ := db(r).OutboxEmails(r.Context())
	db(r).SetProgress(r.Context(), "outbox_seen")
	out := []map[string]any{}
	for _, e := range emails {
		out = append(out, map[string]any{
			"id": e.ID, "kind": e.Kind, "to": e.To, "subject": e.Subject,
			"created_at": e.CreatedAt,
		})
	}
	apiJSON(w, 200, map[string]any{"emails": out})
}

func (s *Server) apiOutboxEmail(w http.ResponseWriter, r *http.Request) {
	e, err := s.outboxEmail(r)
	if apiDBErr(w, r, err, "email") {
		return
	}
	apiJSON(w, 200, map[string]any{"email": map[string]any{
		"id": e.ID, "kind": e.Kind, "to": e.To, "subject": e.Subject,
		"body_md": e.BodyMD, "body_html": e.BodyHTML, "created_at": e.CreatedAt,
	}})
}

// --- paywall / reader state ---

func (s *Server) apiReader(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	rid, _ := db(r).UpsertReader(r.Context(), pub.ID, demoReaderEmail, demoReaderName)
	active, _ := db(r).HasActiveSubscription(r.Context(), rid)
	subs, _ := db(r).SubscriberCount(r.Context(), pub.ID)
	readers, _ := db(r).ReaderCount(r.Context(), pub.ID)
	confirmed, _ := db(r).ConfirmedReaderCount(r.Context(), pub.ID)
	resp := map[string]any{
		"reader":      map[string]string{"email": demoReaderEmail, "name": demoReaderName},
		"subscribed":  active,
		"subscribers": subs,
		"readers":     readers,
		"confirmed":   confirmed,
	}
	if iss, err := db(r).LatestFinishedIssue(r.Context(), pub.ID); err == nil {
		resp["issue"] = s.issueSummary(r, iss)
	}
	apiJSON(w, 200, resp)
}

func (s *Server) apiPaywallSimulate(w http.ResponseWriter, r *http.Request) {
	r = s.withWS(r)
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	ctx := r.Context()
	rid, _ := db(r).UpsertReader(ctx, pub.ID, demoReaderEmail, demoReaderName)
	tokenBytes := make([]byte, 12)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)
	if apiDBErr(w, r, db(r).ActivateSubscription(ctx, rid, "fake", "sim-"+strconv.FormatInt(rid, 10), token), "subscription") {
		return
	}
	// Demo mode delivers the magic link through the workspace outbox; the
	// sealed token lets it open in any browser. It opens the latest
	// finished issue on the reader-facing page.
	link := requestBase(r) + "/p/" + pub.Slug + "/latest?key=" + token + s.tokQ(r)
	if iss, err := db(r).LatestPublicIssue(ctx, pub.ID); err == nil {
		link = fmt.Sprintf("%s/p/%s/%d?key=%s%s",
			requestBase(r), pub.Slug, iss.Number, token, s.tokQ(r))
	}
	htmlBody := fmt.Sprintf(
		`<!DOCTYPE html><html><body style="font-family:system-ui,sans-serif;max-width:38em;margin:2em auto">`+
			`<h2>%s — acesso de assinante</h2>`+
			`<p>Olá, %s! Clique para abrir sua edição como assinante:</p>`+
			`<p><a href="%s">Abrir %s (magic link)</a></p>`+
			`<p style="color:#777;font-size:.85em">Simulado no modo demo — nenhum e-mail real foi enviado.</p></body></html>`,
		pub.Name, demoReaderName, link, pub.Name)
	emailID, err := db(r).SaveOutboxEmail(ctx, "magic_link", demoReaderEmail,
		pub.Name+" — seu link de acesso", "", htmlBody)
	if apiDBErr(w, r, err, "email") {
		return
	}
	apiJSON(w, 200, map[string]any{
		"subscribed": true, "outbox_email_id": emailID, "magic_link": link,
	})
}

func (s *Server) apiPaywallCancel(w http.ResponseWriter, r *http.Request) {
	r = s.withWS(r)
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	rid, _ := db(r).UpsertReader(r.Context(), pub.ID, demoReaderEmail, demoReaderName)
	if apiDBErr(w, r, db(r).CancelSubscription(r.Context(), rid), "subscription") {
		return
	}
	apiJSON(w, 200, map[string]any{"subscribed": false})
}

// withWS resolves a cross-workspace override for reader-facing requests.
// New links carry ?tok= — an AES-GCM sealed workspace id that reveals
// nothing in the URL. ?ws= is still honored for links already delivered.
// The visitor's own session workspace is never touched.
func (s *Server) withWS(r *http.Request) *http.Request {
	// A workspace override is honored on read-only requests only — the
	// raw ?ws= id must never mutate another visitor's state (review F2).
	if !s.d.WS.Hosted() || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return r
	}
	wsid := r.URL.Query().Get("ws")
	if wsid == "" {
		if tok := r.URL.Query().Get("tok"); tok != "" {
			wsid = s.d.WS.ResolvePublicToken(tok)
		}
	}
	if wsid == "" || wsid == reqWS(r).ID {
		return r
	}
	if target, err := s.d.WS.Get(r.Context(), wsid); err == nil {
		return r.WithContext(context.WithValue(r.Context(), ctxWS, target))
	}
	return r
}

// readerToken is the opaque workspace reference for public links.
func (s *Server) readerToken(r *http.Request) string {
	if !s.d.WS.Hosted() {
		return ""
	}
	return s.d.WS.PublicToken(reqWS(r).ID)
}

// tokQ is the "&tok=…" query suffix for public links — empty in local
// mode, where the workspace is already the reader's own.
func (s *Server) tokQ(r *http.Request) string {
	if tok := s.readerToken(r); tok != "" {
		return "&tok=" + url.QueryEscape(tok)
	}
	return ""
}

// apiPublicIssue serves the reader-facing issue page data: the gated or
// full body depending on the reader's subscription (magic-link key or a
// forced free preview), with no editor-only fields.
func (s *Server) apiPublicIssue(w http.ResponseWriter, r *http.Request) {
	r = s.withWS(r)
	rawPub, loadErr := db(r).GetPublication(r.Context(), r.PathValue("slug"))
	pub, err := s.canonicalPub(rawPub, loadErr)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var iss *store.Issue
	if r.PathValue("num") == "latest" {
		iss, err = db(r).LatestPublicIssue(r.Context(), pub.ID)
	} else {
		num, nerr := strconv.Atoi(r.PathValue("num"))
		if nerr != nil {
			apiErr(w, 404, "not_found", "issue not found")
			return
		}
		iss, err = db(r).IssueByNumber(r.Context(), pub.ID, num)
	}
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status != "sent" && iss.Status != "scheduled" {
		apiErr(w, 404, "not_found", "issue not found")
		return
	}
	ctx := r.Context()
	rid, _ := db(r).UpsertReader(ctx, pub.ID, demoReaderEmail, demoReaderName)
	active, _ := db(r).HasActiveSubscription(ctx, rid)
	key := r.URL.Query().Get("key")
	if key != "" {
		if mrid, err := db(r).ReaderByMagicToken(ctx, pub.ID, key); err == nil && mrid == rid {
			active = true
		}
	}
	forceFree := r.URL.Query().Get("view") == "free"
	unlocked := active && !forceFree
	md := delivery.ReaderBody(iss, pub, requestBase(r))
	if !unlocked {
		md = gateSections(md, pub.Paywall.PaidSections, pub.Language)
	}
	md = s.localizeReaderMD(md, rawPub)
	apiJSON(w, 200, map[string]any{
		"publication": map[string]any{"name": rawPub.Name, "slug": rawPub.Slug, "language": rawPub.Language},
		"issue":       map[string]any{"number": iss.Number, "subject": iss.Subject, "created_at": iss.CreatedAt},
		"body_md":     md, "gated": !unlocked, "subscribed": active,
		"reader":        map[string]string{"name": demoReaderName},
		"paid_sections": pub.Paywall.PaidSections,
	})
}

// apiAfter is the post-send summary: public URL, downloads, the approval
// receipt and the rules the editorial guide learned from this issue.
func (s *Server) apiAfter(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	if iss.Status != "sent" && iss.Status != "scheduled" {
		apiErr(w, 409, "blocked", "the public page exists once the issue is sent or scheduled",
			apiReason{Code: "not_sent", Message: "issue is not sent",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	rawReasons, _ := db(r).IssueRejectionReasons(r.Context(), iss.ID)
	learned := editorial.RulesFrom(rawReasons)
	version, _, _ := db(r).LatestGuide(r.Context(), pub.ID)
	publicURL := fmt.Sprintf("%s/p/%s/%d", requestBase(r), pub.Slug, iss.Number)
	if tok := s.readerToken(r); tok != "" {
		publicURL += "?tok=" + url.QueryEscape(tok)
	}
	apiJSON(w, 200, map[string]any{
		"issue":       s.issueSummary(r, iss),
		"public_url":  publicURL,
		"learned":     orEmpty(learned),
		"guide":       map[string]any{"version": version},
		"export_html": fmt.Sprintf("/api/v1/issues/%d/export?fmt=html", iss.ID),
		"export_md":   fmt.Sprintf("/api/v1/issues/%d/export", iss.ID),
	})
}

// apiUnsub performs the reader-facing one-click unsubscribe behind the
// signed link every issue email carries. The React unsubscribe page calls
// it once with the link's parameters.
func (s *Server) apiUnsub(w http.ResponseWriter, r *http.Request) {
	r = s.withWS(r)
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), r.URL.Query().Get("pub")))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	rid, _ := strconv.ParseInt(r.URL.Query().Get("r"), 10, 64)
	tok := r.URL.Query().Get("t")
	if rid == 0 || !s.d.WS.ValidMAC("unsub", fmt.Sprintf("%d:%d", pub.ID, rid), tok) {
		apiErr(w, 403, "validation", "invalid unsubscribe link")
		return
	}
	if apiDBErr(w, r, db(r).UnsubscribeReader(r.Context(), rid), "reader") {
		return
	}
	apiJSON(w, 200, map[string]any{"unsubscribed": true})
}

// --- guide / costs ---

func (s *Server) apiGuide(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	version, content, err := db(r).LatestGuide(r.Context(), pub.ID)
	if err == sql.ErrNoRows {
		apiJSON(w, 200, map[string]any{"guide": map[string]any{"version": 0, "content": ""}})
		return
	}
	if apiDBErr(w, r, err, "guide") {
		return
	}
	db(r).SetProgress(r.Context(), "guide_seen")
	apiJSON(w, 200, map[string]any{"guide": map[string]any{"version": version, "content": content}})
}

func (s *Server) apiGuideVersions(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	guides, _ := db(r).LatestGuides(r.Context(), pub.ID, 50)
	out := []map[string]any{}
	for _, g := range guides {
		out = append(out, map[string]any{"version": g.Version, "content": g.Content})
	}
	apiJSON(w, 200, map[string]any{"versions": out})
}

func (s *Server) apiGuideSave(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	version, err := db(r).SaveGuide(r.Context(), pub.ID, in.Content)
	if apiDBErr(w, r, err, "guide") {
		return
	}
	apiJSON(w, 200, map[string]any{"guide": map[string]any{"version": version, "content": in.Content}})
}

func (s *Server) apiGuideRebuild(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	if s.d.Cfg.LLMOff() {
		// Guide rebuild is an AI action — blocked outright in
		// deterministic mode, never reaching the provider.
		apiErr(w, 409, "ai_disabled", "guide rebuild needs a model — model calls are disabled (KLOXUS_LLM_MODE=off)")
		return
	}
	content, version, err := editorial.Rebuild(r.Context(), db(r), pub, s.d.LLM, s.d.Cfg.ModelTriage)
	if errors.Is(err, llm.ErrDisabled) {
		apiErr(w, 409, "ai_disabled", "guide rebuild needs a model — model calls are disabled (KLOXUS_LLM_MODE=off)")
		return
	}
	if err != nil {
		apiErr(w, 409, "conflict", err.Error())
		return
	}
	apiJSON(w, 200, map[string]any{"guide": map[string]any{"version": version, "content": content}})
}

func (s *Server) apiCosts(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	last := 4
	if v := r.URL.Query().Get("last"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			last = n
		}
	}
	rows, _ := db(r).CostReport(r.Context(), pub.ID, last)
	type issueCosts struct {
		Number   int             `json:"number"`
		Rows     []store.CostRow `json:"rows"`
		TotalUSD float64         `json:"total_usd"`
	}
	groups := []*issueCosts{}
	for _, row := range rows {
		if len(groups) == 0 || groups[len(groups)-1].Number != row.IssueNumber {
			groups = append(groups, &issueCosts{Number: row.IssueNumber})
		}
		g := groups[len(groups)-1]
		g.Rows = append(g.Rows, row)
		g.TotalUSD += row.CostUSD
	}
	apiJSON(w, 200, map[string]any{"issues": groups})
}

// --- demo workspace ---

func (s *Server) apiDemoReset(w http.ResponseWriter, r *http.Request) {
	if err := s.d.WS.Reset(r.Context(), reqWS(r)); err != nil {
		apiErr(w, 500, "internal", "reset failed")
		return
	}
	apiJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) apiDemoNew(w http.ResponseWriter, r *http.Request) {
	ws, err := s.d.WS.New(w, r)
	if err != nil {
		apiErr(w, 429, "rate_limited", err.Error())
		return
	}
	apiJSON(w, 200, map[string]any{"workspace": map[string]any{"id": ws.ID, "label": ws.Label}})
}

// --- small helpers ---

// itemDossierByID loads the dossier for a document item.
func (s *Server) itemDossierByID(r *http.Request, item *docItem) (*pipeline.Dossier, error) {
	if item == nil || item.IssueItemID == 0 {
		return nil, sql.ErrNoRows
	}

	dj, err := db(r).Dossier(r.Context(), item.IssueItemID)
	if err != nil {
		return nil, err
	}
	var d pipeline.Dossier
	if err := json.Unmarshal([]byte(dj), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// editSupportedFor re-runs the citation check for an edited sentence
// against a document item's dossier.
func (s *Server) editSupportedFor(r *http.Request, item *docItem, itemRef int64, text string) bool {
	d, err := s.itemDossierByID(r, item)
	if err != nil || d == nil {
		return false
	}
	return s.dossierSupports(r, d, itemRef, text)
}

var refRe = regexp.MustCompile(`\[\d+\]`)

// dossierSupports is the citation check for one edited sentence: the
// model verdict when available (real mode), otherwise the deterministic
// overlap check against the dossier facts that carry the cited refs.
func (s *Server) dossierSupports(r *http.Request, d *pipeline.Dossier, itemRef int64, text string) bool {
	refs := map[int]bool{}
	for _, m := range refRe.FindAllString(text, -1) {
		n, _ := strconv.Atoi(strings.Trim(m, "[]"))
		refs[n] = true
	}
	// model check first (real mode); demo misses fall to the local check
	var checks []struct {
		ID      int    `json:"id"`
		Verdict string `json:"verdict"`
	}
	srcs := []map[string]any{}
	for _, src := range d.Sources {
		srcs = append(srcs, map[string]any{"ref": src.Ref, "url": src.URL})
	}
	user, err := pipeline.RenderPrompt("citecheck", map[string]any{
		"Publication": "",
		"Sentences":   []map[string]any{{"id": 1, "item": itemRef, "refs": "", "text": text}},
		"Items":       []map[string]any{{"ref": itemRef, "sources": srcs}},
	})
	if err == nil && !s.d.Cfg.LLMOff() {
		if resp, err := s.d.LLM.Complete(r.Context(), llm.Request{
			Model: s.d.Cfg.ModelTriage, Template: "citecheck", User: user, MaxTokens: 512,
		}); err == nil {
			if json.Unmarshal([]byte(resp.Text), &checks) == nil && len(checks) > 0 {
				return checks[0].Verdict == "ok"
			}
		}
	}
	// deterministic fallback: ≥60% of content words must appear in a
	// fact (or the summary) attributed to one of the cited refs.
	var grounded []string
	for _, f := range append(append([]pipeline.DossierFact{}, d.Facts...), d.DeepFacts...) {
		if refs[f.Ref] {
			grounded = append(grounded, f.Text)
		}
	}
	grounded = append(grounded, d.Summary)
	corpus := strings.ToLower(strings.Join(grounded, " "))
	plain := refRe.ReplaceAllString(text, "")
	words := strings.FieldsFunc(strings.ToLower(plain), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var content, hit int
	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		content++
		if strings.Contains(corpus, w) {
			hit++
		}
	}
	return content > 0 && float64(hit)/float64(content) >= 0.6
}

// sourceURL returns the URL of a source owned by the publication in the
// request path, guarding against cross-publication source ids.
func (s *Server) sourceURL(r *http.Request, sid int64) (string, error) {
	pub, err := s.pub(r)
	if err != nil {
		return "", err
	}
	var url string
	err = db(r).QueryRowContext(r.Context(),
		`SELECT url FROM sources WHERE workspace_id = $1 AND id = $2 AND publication_id = $3`,
		db(r).Workspace(), sid, pub.ID).Scan(&url)
	return url, err
}

// addedLines lists lines present in newer but absent from older.
func addedLines(older, newer string) []string {
	old := map[string]bool{}
	for _, l := range strings.Split(older, "\n") {
		old[strings.TrimSpace(l)] = true
	}
	var out []string
	for _, l := range strings.Split(newer, "\n") {
		if t := strings.TrimSpace(l); t != "" && !old[t] {
			out = append(out, t)
		}
	}
	return out
}
