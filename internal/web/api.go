package web

// The JSON API under /api/v1 (milestone 1 of the v2 contract — see
// docs/api.md). It exposes the same behaviour as the HTML routes; the
// rules do not change, only the format of the answers. Errors carry a
// stable code, a human message and, for blocked actions, the list of
// reasons with a pointer to what must be fixed.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

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
// The body must be valid UTF-8 — encoding/json silently rewrites
// invalid bytes to U+FFFD, which would persist mojibake (a Windows-1252
// "ó" once landed in the database as "Pr�ximo").
func apiDecode(r *http.Request, v any) (bool, error) {
	if r.Body == nil {
		return false, nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return false, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return false, nil
	}
	if !utf8.Valid(data) {
		return false, fmt.Errorf("request body is not valid UTF-8")
	}
	if err := json.Unmarshal(data, v); err != nil {
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
	cp.RejectTerms = def.RejectTerms
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
		// bodies written before KLOX-64 carry canonical English section
		// titles — a pt reader sees pt names (KLOX-56). gating already
		// ran on the stored headings. This runs for custom publications
		// too: API-created pubs are stored custom=TRUE while replaying
		// the canonical pipeline, so a creator who kept the old
		// canonical titles still leaks "The big one" unless the swap
		// applies after the custom-title pass.
		for _, swap := range canonicalSectionPT {
			md = regexp.MustCompile(`(?m)^##\s+`+regexp.QuoteMeta(swap[0])+`$`).
				ReplaceAllString(md, "## "+swap[1])
		}
		md = strings.ReplaceAll(md, " — issue ", " — edição ")
		md = strings.ReplaceAll(md, "**Sources:**", "**Fontes:**")
		md = strings.ReplaceAll(md,
			"_Prepared with AI assistance and reviewed by the editor._",
			"_Preparado com auxílio de IA e revisado pelo editor._")
	}
	return md
}

// canonicalEditorialLineSwap is the pt rendering of the one English
// sentence in the sample publication's editorial line (KLOX-56). Like
// canonicalSectionPT it is presentation-only: the stored block keeps
// the recorded text so replay-fixture prompt hashes stay stable, and
// API responses localize it when the publication speaks pt.
const canonicalEditorialLineSwap = "Skip deals, coupons and buying guides."

const ptEditorialLineSwap = "Pule ofertas, cupons e guias de compra."

func localizeEditorialLine(line, lang string) string {
	if !strings.HasPrefix(strings.ToLower(lang), "pt") {
		return line
	}
	return strings.ReplaceAll(line, canonicalEditorialLineSwap, ptEditorialLineSwap)
}

// localizeSubject renders a stored issue subject for a pt reader —
// subjects written before the body heading localized keep the
// canonical "— issue N" (KLOX-56). Display-only; the stored value is
// untouched.
func localizeSubject(subject, lang string) string {
	if !strings.HasPrefix(strings.ToLower(lang), "pt") {
		return subject
	}
	return strings.Replace(subject, " — issue ", " — edição ", 1)
}

// localizePrice renders the paywall price in the publication's
// language and drops the fictional Valdória "V$" for a real "R$" —
// publications created from the sample proposal (and real-mode
// workspaces, which are never reseeded) stored it before the fix
// (KLOX-81). Only a billing-period suffix swaps: "/month" → "/mês" on
// pt publications, "/mês" → "/month" on en ones. Display-only, like
// localizeSubject: the stored value is untouched.
func localizePrice(price, lang string) string {
	price = strings.Replace(price, "V$", "R$", 1)
	var swaps [][2]string
	if strings.HasPrefix(strings.ToLower(lang), "pt") {
		swaps = [][2]string{{"/month", "/mês"}, {"/mo", "/mês"}, {" per month", " por mês"}}
	} else {
		swaps = [][2]string{{"/mês", "/month"}, {" por mês", " per month"}}
	}
	lower := strings.ToLower(price)
	for _, sw := range swaps {
		if strings.HasSuffix(lower, strings.ToLower(sw[0])) && len(price) >= len(sw[0]) {
			return price[:len(price)-len(sw[0])] + sw[1]
		}
	}
	return price
}

// canonicalSectionPT maps the legacy canonical English section titles
// to the pt names readers see — bodies stored before KLOX-64 keep the
// English headings and swap at render time (KLOX-56).
var canonicalSectionPT = [][2]string{
	{"The big one", "O destaque"},
	{"Worth knowing", "Vale saber"},
	{"Quick links", "Links rápidos"},
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
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="pt-BR"><head><meta charset="utf-8"><title>%s — %s</title>`+
		`<style>body{font-family:Georgia,serif;max-width:38em;margin:3em auto;padding:0 1.2em;color:#1c1c1c;line-height:1.6}`+
		`.site{color:#0e7a6d;font-size:.85em;text-transform:uppercase;letter-spacing:.08em}`+
		`.note{margin-top:3em;padding:.9em 1.1em;border:1px dashed #bbb;border-radius:.5em;color:#666;font-size:.85em;font-family:system-ui,sans-serif}`+
		`</style></head><body>`+
		`<p class="site">%s</p><h1>%s</h1>`+
		`<p>Este artigo pertence ao universo ficcional de Valdória — `+
		`cada citação na sua edição abre um original de verdade como este.</p>`+
		`<div class="note">Fonte fictícia de Valdória · <a href="/">← Kloxus</a></div>`+
		`</body></html>`,
		title, u.Host, u.Host, title)
}

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
	RejectTerms   string       `json:"reject_terms"`
	Tone          string       `json:"tone"`
	Frequency     string       `json:"frequency"`
	AIDisclosure  string       `json:"ai_disclosure"`
	Sections      []apiSection `json:"sections"`
	Paywall       apiPaywall   `json:"paywall"`
	// Niches are the editorias the theme names — shown as chips on the
	// Radar screen (KLOX-207).
	Niches []string `json:"niches"`
}

type apiSection struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Format     string `json:"format"` // featured | news | links
	Items      int    `json:"items"`
	Commentary string `json:"commentary"`
	Access     string `json:"access"` // all | subscribers
	Topic      string `json:"topic"`  // "que tipo de pauta entra aqui?" (KLOX-94)
}

type apiPaywall struct {
	Enabled      bool     `json:"enabled"`
	PaidSections []string `json:"paid_sections"`
	Price        string   `json:"price"`
}

func publicationView(p *store.Publication) apiPublication {
	v := apiPublication{
		ID: p.ID, Slug: p.Slug, Name: p.Name, Custom: p.Custom, Language: p.Language,
		// the stored topic folds the editorias' labels in for ranking —
		// the theme field shows the creator's words alone (KLOX-232)
		Topic: topicWithoutNiches(p.Topic), Audience: p.Audience,
		// the stored line stays canonical for fixture-stable prompts —
		// the response localizes the one English sentence (KLOX-56)
		EditorialLine: localizeEditorialLine(p.EditorialLine, p.Language),
		RejectTerms:   p.RejectTerms,
		Tone:          p.Tone, Frequency: p.Frequency, AIDisclosure: p.AIDisclosure,
		Paywall: apiPaywall{
			Enabled: p.Paywall.Enabled, PaidSections: orEmpty(p.Paywall.PaidSections),
			Price: localizePrice(p.Paywall.Price, p.Language),
		},
	}
	for _, sc := range config.NormalizeSections(p.Sections, p.Paywall.PaidSections) {
		v.Sections = append(v.Sections, apiSection{
			ID: sc.ID, Title: sc.Title, Format: sc.Format,
			Items: sc.Items, Commentary: sc.Commentary, Access: sc.Access,
			Topic: sc.Topic,
		})
	}
	if v.Sections == nil {
		v.Sections = []apiSection{}
	}
	if v.Paywall.PaidSections == nil {
		v.Paywall.PaidSections = []string{}
	}
	v.Niches = nichesFromTopic(p.Topic)
	return v
}

// apiIssueSummary is the issue resource in lists and detail.
func (s *Server) issueSummary(r *http.Request, iss *store.Issue) map[string]any {
	ctx := r.Context()
	counts := map[string]int{store.ItemPending: 0, store.ItemApproved: 0, store.ItemRejected: 0, store.ItemAlternative: 0}
	items, err := db(r).IssueItems(ctx, iss.ID)
	if err != nil {
		slog.Warn("issue summary: list items failed", "issue", iss.ID, "err", err)
	}
	for _, ii := range items {
		counts[ii.Status]++
	}
	missing := 0
	subject := iss.Subject
	rawPub, pubErr := db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID))
	if pubErr == nil {
		subject = localizeSubject(subject, rawPub.Language)
	}
	pub, pubOK := s.canonicalPub(rawPub, pubErr)
	if pubOK == nil {
		for _, sc := range issueSections(iss, pub) {
			if sc.Commentary == "required" && strings.TrimSpace(iss.Commentary[sc.Title]) == "" {
				missing++
			}
		}
	}
	m := map[string]any{
		"id": iss.ID, "publication_id": iss.PublicationID, "number": iss.Number,
		"status": iss.Status, "subject": subject, "created_at": iss.CreatedAt,
		"items":                       counts,
		"flags":                       len(iss.Flags),
		"missing_required_commentary": missing,
		"document_version":            docVersion(iss),
	}
	if pubOK == nil {
		// the edition keeps its own section snapshot — flag drift so the
		// board can say "builder edits apply to the next edition" (KLOX-65)
		m["sections_changed"] = sectionsChanged(iss, pub)
	}
	// The run's recorded model cost travels with the issue so the
	// creator always sees what this issue cost (KLOX-44).
	if cost, err := db(r).IssueCostUSD(ctx, iss.ID); err == nil {
		m["cost_usd"] = cost
	}
	if iss.ScheduledAt != "" {
		m["scheduled_at"] = iss.ScheduledAt
	}
	if iss.TestSentAt != "" {
		m["test_sent_at"] = iss.TestSentAt
	}
	if rec, err := db(r).ApprovalReceipt(ctx, iss.ID); err == nil {
		m["receipt"] = receiptView(rec)
	}
	// KLOX-184: lists name the pauta by its content — first story
	// title, opening line or subject — and remember which formats the
	// author already copied or downloaded ("Exportada").
	m["title"] = s.pautaTitle(ctx, db(r), iss, pub, items)
	if formats, err := db(r).IssueExports(ctx, iss.ID); err == nil && len(formats) > 0 {
		m["exported"] = true
		m["exported_formats"] = formats
	}
	// Edited-after-approval (KLOX-66): the issue sat in "approved" and a
	// post-approval edit reverted it — the stored approve decision for
	// this issue is the marker (the receipt itself is voided on revert).
	// The header shows "re-approve to send" until a fresh approve lands.
	if iss.Status == store.IssueDrafted || iss.Status == store.IssueReviewing {
		if ok, _ := db(r).DecisionExists(ctx, iss.PublicationID, "approve",
			approveDecisionReason(iss.ID)); ok {
			m["edited_after_approval"] = true
		}
	}
	return m
}

// pautaTitle names a pauta in lists (KLOX-184): an author-set name
// first (KLOX-199), then the first story's title, the opening line
// when there is no story, else the stored subject — never the bare
// "pauta N".
func (s *Server) pautaTitle(ctx context.Context, d *store.DB, iss *store.Issue, pub *store.Publication, items []store.IssueItem) string {
	if t := strings.TrimSpace(iss.Subject); t != "" && !autoPautaSubject(iss) {
		return t
	}
	var first *store.IssueItem
	for i := range items {
		ii := &items[i]
		if ii.Status == store.ItemRemoved || ii.Status == store.ItemRejected {
			continue
		}
		if first == nil || ii.Position < first.Position ||
			(ii.Position == first.Position && ii.ID < first.ID) {
			first = ii
		}
	}
	if first != nil {
		// The snapshotted title survives the deck rebuild; the live
		// story is only a fallback for rows that predate the column.
		if title := strings.TrimSpace(first.Title); title != "" {
			return title
		}
		if title, err := d.StoryTitle(ctx, first.StoryID); err == nil && strings.TrimSpace(title) != "" {
			return strings.TrimSpace(title)
		}
	}
	for _, sc := range issueSections(iss, pub) {
		if line := strings.TrimSpace(strings.SplitN(iss.Commentary[sc.Title], "\n", 2)[0]); line != "" {
			return line
		}
	}
	return iss.Subject
}

// autoPautaSubject reports whether the stored subject is still the
// creation-time placeholder ("{radar} — pauta N" or the pipeline's
// "— issue/edição N"): anything else is a name the author typed in the
// editor (KLOX-199) and it beats the derived title.
func autoPautaSubject(iss *store.Issue) bool {
	for _, word := range []string{"pauta", "issue", "edição"} {
		if strings.HasSuffix(iss.Subject, fmt.Sprintf(" — %s %d", word, iss.Number)) {
			return true
		}
	}
	return false
}

// issueSections returns the section definitions an edition works with:
// the snapshot stored on the issue at run time, or the publication's
// current sections when the issue predates the snapshot. Either way the
// list is normalized — ids, formats and access filled — so consumers
// never branch on a field being absent (KLOX-65).
func issueSections(iss *store.Issue, pub *store.Publication) []config.Section {
	var paid []string
	if pub != nil {
		paid = pub.Paywall.PaidSections
	}
	if len(iss.Sections) > 0 {
		return config.NormalizeSections(iss.Sections, paid)
	}
	if pub == nil {
		return nil
	}
	return config.NormalizeSections(pub.Sections, paid)
}

// pubWithIssueSections returns a shallow publication clone whose
// sections are the edition's snapshot — the value the issue document,
// draft gate and compose path must work from so builder edits only
// reach the next edition.
func pubWithIssueSections(iss *store.Issue, pub *store.Publication) *store.Publication {
	if pub == nil {
		return nil
	}
	cp := *pub
	cp.Sections = issueSections(iss, pub)
	return &cp
}

// sectionsChanged reports whether the edition's section snapshot
// differs from the publication's current builder state — the signal
// for the "edits apply to the next edition" note.
func sectionsChanged(iss *store.Issue, pub *store.Publication) bool {
	if len(iss.Sections) == 0 || pub == nil {
		return false
	}
	a, _ := json.Marshal(issueSections(iss, pub))
	b, _ := json.Marshal(config.NormalizeSections(pub.Sections, pub.Paywall.PaidSections))
	return string(a) != string(b)
}

// paidSectionTitles returns the titles gated for non-subscribers on
// this edition — from the snapshot's access levels, or the stored
// paywall list when the issue predates snapshots.
func paidSectionTitles(iss *store.Issue, pub *store.Publication) []string {
	if pub == nil {
		return nil
	}
	if len(iss.Sections) == 0 {
		return pub.Paywall.PaidSections
	}
	return config.PaidSectionTitles(issueSections(iss, pub))
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
	if err := json.Unmarshal([]byte(rec.ChecksJSON), &checks); err != nil {
		slog.Warn("approval receipt: malformed checks_json", "issue", rec.IssueID, "err", err)
	}
	// Receipts stored before the owner e-mail became the approver carry
	// the workspace's session label — never show it (§2, KLOX-64).
	approver := rec.Approver
	if approver == "local" || approver == "local demo" || strings.HasPrefix(approver, "demo session ") {
		approver = ""
	}
	return map[string]any{
		"issue_id": rec.IssueID, "number": rec.Number,
		"document_version": rec.DocumentVersion,
		"approver":         approver,
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
	Signals   []string `json:"signals,omitempty"`
	RejectWhy string   `json:"reject_reason,omitempty"`
	// AIDraft marks a pauta whose body arrived AI-written (KLOX-67).
	AIDraft bool `json:"ai_draft,omitempty"`
}

func (s *Server) apiCardFor(r *http.Request, ii store.IssueItem) apiItem {
	ctx := r.Context()
	title, _ := db(r).StoryTitle(ctx, ii.StoryID)
	var names, signals []string
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
			if it.Signal() {
				signals = append(signals, "via "+n)
			}
		}
	}
	card := apiItem{
		ID: ii.ID, IssueID: ii.IssueID, StoryID: ii.StoryID, Section: ii.Section,
		Score: ii.Score, Reason: ii.Reason, Status: ii.Status, Origin: ii.Origin,
		Title: title, Outlets: names, Signals: signals, AIDraft: ii.AIDraft,
	}
	if card.Outlets == nil {
		card.Outlets = []string{}
	}
	if ii.Status == store.ItemRejected {
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
		if ii.Status == store.ItemPending {
			n++
		}
	}
	return n
}
