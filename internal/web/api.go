package web

// The JSON API under /api/v1 (milestone 1 of the v2 contract — see
// docs/api.md). It exposes the same behaviour as the HTML routes; the
// rules do not change, only the format of the answers. Errors carry a
// stable code, a human message and, for blocked actions, the list of
// reasons with a pointer to what must be fixed.
import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	RejectTerms   string       `json:"reject_terms"`
	Tone          string       `json:"tone"`
	Frequency     string       `json:"frequency"`
	AIDisclosure  string       `json:"ai_disclosure"`
	Sections      []apiSection `json:"sections"`
	Paywall       apiPaywall   `json:"paywall"`
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
		Topic: p.Topic, Audience: p.Audience,
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
	// Edited-after-approval (KLOX-66): the issue sat in "approved" and a
	// post-approval edit reverted it — the stored approve decision for
	// this issue is the marker (the receipt itself is voided on revert).
	// The header shows "re-approve to send" until a fresh approve lands.
	if iss.Status == "drafted" || iss.Status == "reviewing" {
		if ok, _ := db(r).DecisionExists(ctx, iss.PublicationID, "approve",
			approveDecisionReason(iss.ID)); ok {
			m["edited_after_approval"] = true
		}
	}
	return m
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
	json.Unmarshal([]byte(rec.ChecksJSON), &checks)
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
	// mail_transport is the sender's own wire identity — positive
	// server-side evidence for verification preflights: "outbox" (no
	// sender), "resend" (the real provider), "synthetic" (the loopback
	// fake), "custom" (any other injected sender).
	transport := "outbox"
	// mail_webhook reports whether the app can verify Resend delivery
	// events (RESEND_WEBHOOK_SECRET set); mail_stale_sent counts
	// provider-accepted sends whose delivery event never arrived —
	// together they are the posture a dead endpoint cannot fake
	// (KLOX-98). Outbox mode has no webhook, so both stay inert.
	mailWebhook := "n/a"
	staleSent := 0
	if s.liveMail() {
		mail = "live"
		transport = "custom"
		if t, ok := s.d.Mailer.(interface{ Transport() string }); ok {
			transport = t.Transport()
		}
		mailWebhook = "unconfigured"
		if s.d.Cfg.ResendWebhookSecret != "" {
			mailWebhook = "configured"
		}
		staleSent = s.staleSent(r.Context(), db(r))
	}
	apiJSON(w, 200, map[string]any{
		"id": ws.ID, "label": ws.Label, "mode": s.d.Cfg.Mode, "hosted": s.d.WS.Hosted(),
		"llm_mode": s.d.Cfg.LLMMode,
		"claimed":  owner != "", "owner_email": owner, "mail": mail,
		"mail_transport": transport, "mail_webhook": mailWebhook,
		"mail_stale_sent": staleSent,
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
	CatalogID    int64          `json:"catalog_id,omitempty"`
	Policy       string         `json:"policy,omitempty"`
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
		CatalogID:    src.CatalogID, Policy: src.Policy,
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
	if s.d.Cfg.Mode != "demo" && s.d.Pool != nil {
		s.realSourceAdd(w, r, pub, rawURL, in.Name)
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

// apiSourceCatalog lists the sources a publication can pick. Demo mode
// offers the sample fixture sources; real mode serves the global
// catalog — launch/signal entries only; hold/blocked entries are never
// listed to users (D29/D31/KLOX-49).
func (s *Server) apiSourceCatalog(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	if s.d.Cfg.Mode != "demo" && s.d.Pool != nil {
		s.apiRealCatalog(w, r, pub)
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
	if s.d.Cfg.Mode != "demo" && s.d.Pool != nil {
		if s.realCatalogPreview(w, r, rawURL) {
			return
		}
		apiErr(w, 400, "catalog_only",
			"that source is not in the catalog — pick from the listed sources")
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
	if s.d.Cfg.Mode != "demo" && s.d.Pool != nil {
		s.realSourceConfirm(w, r, pub, in.Include, decoded)
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
// the first screen doesn't create a demo workspace before Start. It is
// always the fictional Valdória sample (KLOX-49): the landing shows only
// content we are allowed to display — the recorded real issue carried
// dossiers from outlets outside the catalog (g1), so it no longer ships.
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
	for _, sec := range config.NormalizeSections(def.Sections, def.Paywall.PaidSections) {
		secs = append(secs, apiSection{
			ID: sec.ID, Title: sec.Title, Format: sec.Format,
			Items: sec.Items, Commentary: sec.Commentary, Access: sec.Access,
			Topic: sec.Topic,
		})
	}
	srcs := []map[string]string{}
	for _, sd := range def.Sources {
		srcs = append(srcs, map[string]string{"type": sd.Type, "url": sd.URL, "name": sd.Name})
	}
	apiJSON(w, 200, map[string]any{"proposal": map[string]any{
		"name": def.Name, "topic": def.Topic, "audience": def.Audience,
		"editorial_line": localizeEditorialLine(def.EditorialLine, def.Language),
		"reject_terms":   def.RejectTerms, "tone": def.Tone,
		"language": def.Language, "frequency": def.Frequency,
		"ai_disclosure": def.AIDisclosure,
		"sections":      secs, "sources": srcs,
		"paywall": apiPaywall{Enabled: def.Paywall.Enabled, PaidSections: def.Paywall.PaidSections, Price: localizePrice(def.Paywall.Price, def.Language)},
	}})
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
		RejectTerms   string           `json:"reject_terms"`
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
	secs, reasons := validateSections(in.Sections)
	if len(reasons) > 0 {
		apiErr(w, 400, "validation", "the section list needs a fix", reasons...)
		return
	}
	// the builder's per-section access flag is the source of truth for
	// the paywall's title list — the two must never drift apart
	in.Paywall.PaidSections = config.PaidSectionTitles(secs)
	def := &config.Publication{
		Name: in.Name, Topic: in.Topic, Audience: in.Audience,
		EditorialLine: in.EditorialLine, RejectTerms: in.RejectTerms, Tone: in.Tone,
		Language: in.Language, Frequency: in.Frequency,
		AIDisclosure: in.AIDisclosure, Sections: secs, Paywall: in.Paywall,
	}
	if err := def.Validate(); err != nil {
		apiErr(w, 400, "validation", err.Error())
		return
	}
	// One newsletter per account (owner amendment, KLOX-66): the rule
	// lives in the API, not just the UI — any existing publication makes
	// the account "has a newsletter", including a template-seeded one.
	// Workspaces that already hold several publications (pre-amendment
	// data) keep them all; the guard only stops new growth, and deleting
	// back to zero reopens onboarding.
	pubs, err := db(r).ListPublications(r.Context())
	if apiDBErr(w, r, err, "publications") {
		return
	}
	if len(pubs) > 0 {
		apiErr(w, 409, "one_newsletter",
			"this account already has a newsletter — one newsletter per account")
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

// validateSections applies the builder's rules and returns the
// normalized list (ids, formats and access defaults filled, paywall
// titles resolved) — the value to persist. Bound checks come back as
// structured reasons so the client can localize each one.
func validateSections(in []config.Section) ([]config.Section, []apiReason) {
	var reasons []apiReason
	secs := config.NormalizeSections(in, nil)
	if len(secs) == 0 {
		reasons = append(reasons, apiReason{Code: "sections_empty",
			Message: "add at least one section"})
		return nil, reasons
	}
	ids, titles := map[string]bool{}, map[string]bool{}
	total := 0
	for i, sc := range secs {
		target := &apiTarget{Kind: "section", Ref: sc.Title}
		if strings.TrimSpace(sc.Title) == "" {
			reasons = append(reasons, apiReason{Code: "section_title_empty",
				Message: fmt.Sprintf("section %d needs a name", i+1), Target: target})
		} else if len([]rune(sc.Title)) > 40 {
			reasons = append(reasons, apiReason{Code: "section_title_long",
				Message: fmt.Sprintf("section %q runs past 40 characters", sc.Title), Target: target})
		} else if titles[strings.ToLower(sc.Title)] {
			reasons = append(reasons, apiReason{Code: "section_title_dup",
				Message: fmt.Sprintf("section %q is repeated", sc.Title), Target: target})
		}
		titles[strings.ToLower(sc.Title)] = true
		if ids[sc.ID] {
			reasons = append(reasons, apiReason{Code: "section_id_dup",
				Message: fmt.Sprintf("section id %q is repeated", sc.ID), Target: target})
		}
		ids[sc.ID] = true
		if sc.Items < 1 || sc.Items > 10 {
			reasons = append(reasons, apiReason{Code: "section_items",
				Message: fmt.Sprintf("section %q asks for %d stories — pick 1 to 10", sc.Title, sc.Items), Target: target})
		} else {
			total += sc.Items
		}
		if !config.ValidSectionFormat(sc.Format) {
			reasons = append(reasons, apiReason{Code: "section_format",
				Message: fmt.Sprintf("section %q has an unknown format %q", sc.Title, sc.Format), Target: target})
		}
		switch sc.Commentary {
		case "required", "optional", "none":
		default:
			reasons = append(reasons, apiReason{Code: "section_commentary",
				Message: fmt.Sprintf("section %q has an unknown commentary mode %q", sc.Title, sc.Commentary), Target: target})
		}
		if !config.ValidSectionAccess(sc.Access) {
			reasons = append(reasons, apiReason{Code: "section_access",
				Message: fmt.Sprintf("section %q has an unknown access level %q", sc.Title, sc.Access), Target: target})
		}
	}
	if total > 20 {
		reasons = append(reasons, apiReason{Code: "sections_total",
			Message: fmt.Sprintf("the edition asks for %d stories — the limit is 20", total)})
	}
	if len(reasons) > 0 {
		return nil, reasons
	}
	return secs, nil
}

// apiPublicationUpdate rewrites the creator-editable blocks of a
// publication — identity copy, cadence, sections and the paywall
// block. The change lands on the next edition: issues already in
// progress keep the sections they were built with (KLOX-65).
func (s *Server) apiPublicationUpdate(w http.ResponseWriter, r *http.Request) {
	// the write path reads the stored row — canonicalPub swaps a custom
	// publication's prompt fields for the demo sample, and persisting
	// those fallbacks would silently overwrite the creator's edits
	pub, err := db(r).GetPublication(r.Context(), r.PathValue("id"))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Name          string           `json:"name"`
		Audience      string           `json:"audience"`
		EditorialLine string           `json:"editorial_line"`
		Tone          string           `json:"tone"`
		Frequency     string           `json:"frequency"`
		AIDisclosure  string           `json:"ai_disclosure"`
		Sections      []config.Section `json:"sections"`
		Paywall       *apiPaywall      `json:"paywall"`
		// Topic and RejectTerms ride pointers so "clear the field" ("")
		// is a real write while an absent key keeps the stored value —
		// the keep-if-empty convention can't tell the two apart (KLOX-94).
		Topic       *string `json:"topic"`
		RejectTerms *string `json:"reject_terms"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	def := &config.Publication{
		Name: in.Name, Topic: pub.Topic, Audience: in.Audience,
		EditorialLine: in.EditorialLine, RejectTerms: pub.RejectTerms, Tone: in.Tone,
		Language: pub.Language, Slug: pub.Slug,
		Frequency:    in.Frequency,
		AIDisclosure: in.AIDisclosure,
		Sections:     config.NormalizeSections(pub.Sections, pub.Paywall.PaidSections),
		Paywall:      pub.Paywall,
	}
	// every block the request leaves out keeps the stored value — the
	// builder's sections-only save must not wipe the identity fields
	// (KLOX-65)
	if def.Name == "" {
		def.Name = pub.Name
	}
	if in.Topic != nil {
		def.Topic = strings.TrimSpace(*in.Topic)
	}
	if def.Audience == "" {
		def.Audience = pub.Audience
	}
	if def.EditorialLine == "" {
		def.EditorialLine = pub.EditorialLine
	}
	if def.Tone == "" {
		def.Tone = pub.Tone
	}
	if def.Frequency == "" {
		def.Frequency = pub.Frequency
	}
	if def.AIDisclosure == "" {
		def.AIDisclosure = pub.AIDisclosure
	}
	if in.RejectTerms != nil {
		def.RejectTerms = strings.TrimSpace(*in.RejectTerms)
	}
	if strings.TrimSpace(def.Name) == "" {
		apiErr(w, 400, "validation", "the newsletter needs a name",
			apiReason{Code: "name_empty", Message: "give the newsletter a name"})
		return
	}
	if in.Sections != nil {
		secs, reasons := validateSections(in.Sections)
		if len(reasons) > 0 {
			apiErr(w, 400, "validation", "the section list needs a fix", reasons...)
			return
		}
		def.Sections = secs
	}
	if in.Paywall != nil {
		def.Paywall.Enabled = in.Paywall.Enabled
		def.Paywall.Price = in.Paywall.Price
	}
	def.Paywall.PaidSections = config.PaidSectionTitles(def.Sections)
	if err := def.Validate(); err != nil {
		apiErr(w, 400, "validation", err.Error())
		return
	}
	if apiDBErr(w, r, db(r).UpdatePublication(r.Context(), pub.ID, def), "publication") {
		return
	}
	fresh, err := db(r).GetPublication(r.Context(), fmt.Sprint(pub.ID))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	apiJSON(w, 200, map[string]any{"publication": publicationView(fresh)})
}

// apiPublicationDelete removes a publication and everything it owns —
// issues, stories, sources, readers, guides. The call must carry an
// explicit {"confirm": true} so a stray request can never delete a
// newsletter (KLOX-65).
func (s *Server) apiPublicationDelete(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !in.Confirm {
		apiErr(w, 400, "validation", "deleting a newsletter requires confirm: true",
			apiReason{Code: "confirm_required", Message: "confirm the deletion"})
		return
	}
	if err := db(r).DeletePublication(r.Context(), pub.ID); apiDBErr(w, r, err, "publication") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"` // running | done | failed | blocked
	Step          string         `json:"step"`
	IssueID       int64          `json:"issue_id,omitempty"`
	NoNewItems    bool           `json:"no_new_items"`
	Error         string         `json:"error,omitempty"`
	Blocked       bool           `json:"blocked,omitempty"`
	BlockedReason string         `json:"blocked_reason,omitempty"`
	Sample        bool           `json:"sample,omitempty"`
	Events        []runEventView `json:"events"`
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
		Blocked: st.st.Blocked, BlockedReason: st.st.BlockedReason, Sample: st.st.Sample,
		Status: "running",
	}
	if st.st.Done {
		v.Status = "done"
		if st.st.Err != "" {
			v.Status = "failed"
		}
		if st.st.Blocked {
			v.Status = "blocked"
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
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
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
	// The server's 60s WriteTimeout would kill a stream held open by a
	// long run — renew the deadline every tick so a live handler keeps
	// its connection while a wedged one still dies.
	rc := http.NewResponseController(w)
	for {
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
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
	pending := 0
	for _, ii := range items {
		if ii.Status == "pending" {
			pending++
		}
		out = append(out, s.apiCardFor(r, ii))
	}
	apiJSON(w, 200, map[string]any{
		"items": out, "pending": pending,
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
	// Summary is the one-line AI summary the story card shows (KLOX-67
	// §7.2); empty when the item pool has no enrichment yet.
	Summary string `json:"summary,omitempty"`
	// AIDraft marks a lineup card whose pauta body arrived AI-written
	// and has not been edited by the author yet.
	AIDraft bool `json:"ai_draft,omitempty"`
	// RejectedEditorial marks a bench story hitting the newsletter's
	// reject terms: demoted to the bench tail, never auto-slotted and
	// never silently dropped — the card shows the policy (KLOX-94).
	RejectedEditorial bool `json:"rejected_editorial,omitempty"`
}

// demoCandidateCap is how many ranked stories a demo workspace can
// actually draft — replay fixtures only exist for the top of the list.
const demoCandidateCap = 14

// candidateCap bounds the ranked pool the board can see (KLOX-45):
// the bench is paginated through the top candidates only.
const candidateCap = 150

// candidatePageSize is the bench page the board renders — it is also
// the API's default and maximum page size.
const candidatePageSize = 50

func (s *Server) apiCandidates(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if size <= 0 || size > candidatePageSize {
		size = candidatePageSize
	}
	cands, err := db(r).RankedCandidates(r.Context(), iss.PublicationID, iss.ID)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	secs := issueSections(iss, pub)
	views, meta, err := s.candidatePage(r, cands, page, size, pub, secs)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	resp := map[string]any{
		"candidates":       views,
		"sections":         sectionViews(secs),
		"sections_changed": sectionsChanged(iss, pub),
		"demo":             s.d.Cfg.Mode == "demo",
		"llm_mode":         s.d.Cfg.LLMMode,
	}
	maps.Copy(resp, meta)
	apiJSON(w, 200, resp)
}

// candidatePage cuts the ranked pool to the board's window: every
// in-issue story plus the top candidateCap ranked stories, paged by the
// bench only — in-issue rows ride along on every page so the lineup
// column never loses a card. Bench candidates keep their global rank.
func (s *Server) candidatePage(r *http.Request, cands []store.RankedCandidate, page, size int, pub *store.Publication, secs []config.Section) ([]apiCandidate, map[string]any, error) {
	pool := cands
	if len(pool) > candidateCap {
		pool = pool[:candidateCap]
	}
	inIssue, bench := []store.RankedCandidate{}, []store.RankedCandidate{}
	for _, c := range pool {
		switch {
		case c.Item != nil && (c.Item.Status == "pending" || c.Item.Status == "approved"):
			inIssue = append(inIssue, c)
		case c.Item != nil && c.Item.Status == "rejected":
			// A reasoned rejection is an editorial decision, not a
			// bench slot — the story must not resurface as a
			// suggestion (KLOX-55). Undo restores it to the lineup.
		default:
			bench = append(bench, c)
		}
	}
	// An in-issue story ranked beyond the cap must still render on the
	// board — the bench is bounded, the lineup is not.
	if len(cands) > candidateCap {
		for _, c := range cands[candidateCap:] {
			if c.Item != nil && (c.Item.Status == "pending" || c.Item.Status == "approved") {
				inIssue = append(inIssue, c)
			}
		}
	}
	// KLOX-94: reject-term hits sink to the bench tail — marked, never
	// silently dropped (rejectedEditorial flags them in the views). The
	// partition is stable, so rank order survives inside each half.
	if pub != nil && strings.TrimSpace(pub.RejectTerms) != "" {
		sort.SliceStable(bench, func(a, b int) bool {
			ra := pipeline.TermsMatch(pub.RejectTerms, bench[a].Story.Title)
			rb := pipeline.TermsMatch(pub.RejectTerms, bench[b].Story.Title)
			return !ra && rb
		})
	}
	benchTotal := len(bench)
	if from := page * size; from < benchTotal {
		to := from + size
		if to > benchTotal {
			to = benchTotal
		}
		bench = bench[from:to]
	} else {
		bench = []store.RankedCandidate{}
	}
	visible := make([]store.RankedCandidate, 0, len(inIssue)+len(bench))
	visible = append(visible, inIssue...)
	visible = append(visible, bench...)
	sort.Slice(visible, func(a, b int) bool { return visible[a].Rank < visible[b].Rank })
	views, err := s.candidateViews(r, visible, pub, secs)
	if err != nil {
		return nil, nil, err
	}
	return views, map[string]any{
		"page": page, "page_size": size,
		"bench_total": benchTotal, "has_more": (page+1)*size < benchTotal,
	}, nil
}

func (s *Server) candidateViews(r *http.Request, cands []store.RankedCandidate, pub *store.Publication, secs []config.Section) ([]apiCandidate, error) {
	demo := s.d.Cfg.Mode == "demo"
	ids := make([]int64, len(cands))
	for i, c := range cands {
		ids[i] = c.Story.ID
	}
	byStory, err := db(r).StoryOutlets(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	summaries, err := db(r).StorySummaries(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	out := []apiCandidate{}
	for _, c := range cands {
		outlets := []string{}
		seen := map[string]bool{}
		for _, o := range byStory[c.Story.ID] {
			n := o.Name
			if n == "" {
				n = o.URL
			}
			if !seen[n] {
				seen[n] = true
				outlets = append(outlets, n)
			}
		}
		cv := apiCandidate{
			StoryID: c.Story.ID, Title: c.Story.Title,
			Outlets: outlets, Sources: len(byStory[c.Story.ID]), Duplicates: c.Duplicates,
			Score: c.Story.TriageScore, Reason: c.Story.TriageReason, Rank: c.Rank,
			Dossier: c.Dossier, Available: true, Summary: summaries[c.Story.ID],
		}
		if c.Item != nil {
			cv.ItemID = c.Item.ID
			cv.Section = c.Item.Section
			cv.Status = c.Item.Status
			cv.Origin = c.Item.Origin
			cv.Position = c.Item.Position
			cv.AIDraft = c.Item.AIDraft
			cv.InIssue = c.Item.Status == "pending" || c.Item.Status == "approved"
			// any prior placement is a valid suggestion: a rejected or
			// alternative story re-adds where it once sat
			cv.SuggestedSection = c.Item.Section
		} else {
			// A bench story hitting the reject terms carries the
			// marker and gets no section suggestion — the policy is
			// visible, never silent (KLOX-94).
			if pub != nil && strings.TrimSpace(pub.RejectTerms) != "" &&
				pipeline.TermsMatch(pub.RejectTerms, c.Story.Title) {
				cv.RejectedEditorial = true
				if strings.HasPrefix(strings.ToLower(pub.Language), "pt") {
					cv.Reason += " · rejeitada pela linha editorial"
				} else {
					cv.Reason += " · rejected by the editorial line"
				}
			} else if sug := pipeline.SuggestSection(c.Story.Title, secs); sug != "" {
				// a bench story with no prior placement suggests the
				// section whose declared topic it best fits (KLOX-94)
				cv.SuggestedSection = sug
			}
		}
		if demo && cv.Origin != "phaseA" && c.Rank > demoCandidateCap {
			cv.Available = false
		}
		out = append(out, cv)
	}
	return out, nil
}

// rerankCap is the §7.2 bound on one editorial-rerank call — title +
// one-line summary only, at most 120 suggestions.
const rerankCap = 120

// apiRerank is the user-triggered "Ordenar pela minha linha editorial"
// (KLOX-67 §7.2): one Haiku call re-orders the ranked pool by the
// publication's editorial line, persisted as new triage scores. Capped
// at ReranksPerDay per newsletter and counted against the workspace's
// paid actions; on cap, kill switch or failure the answer is
// {reranked:false} — suggestions stay on the free ranking, never an
// error page.
func (s *Server) apiRerank(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	ctx := r.Context()
	pub, err := s.canonicalPub(db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	reply := func(ok bool) {
		apiJSON(w, 200, map[string]any{"reranked": ok})
	}
	if s.d.Cfg.Mode == "demo" || s.d.Cfg.LLMOff() || s.d.LLM == nil {
		reply(false)
		return
	}
	if s.d.Cfg.ReranksPerDay > 0 {
		if n, err := db(r).PaidActionCount(ctx, store.PaidActionRerank, pub.ID); err == nil && n >= s.d.Cfg.ReranksPerDay {
			reply(false)
			return
		}
	}
	if s.d.Cfg.PaidActionsPerDay > 0 {
		if n, err := db(r).PaidActionsToday(ctx); err == nil && n >= s.d.Cfg.PaidActionsPerDay {
			reply(false)
			return
		}
	}
	cands, err := db(r).RankedCandidates(ctx, pub.ID, iss.ID)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	if len(cands) > rerankCap {
		cands = cands[:rerankCap]
	}
	if len(cands) == 0 {
		reply(true)
		return
	}
	var ids []int64
	for _, c := range cands {
		ids = append(ids, c.Story.ID)
	}
	summaries, _ := db(r).StorySummaries(ctx, ids)
	type rrItem struct {
		Ref     int
		StoryID int64
		Title   string
		Summary string
		Reason  string
	}
	var list []rrItem
	for i, c := range cands {
		list = append(list, rrItem{Ref: i + 1, StoryID: c.Story.ID,
			Title: c.Story.Title, Summary: summaries[c.Story.ID],
			Reason: c.Story.TriageReason})
	}
	user, err := pipeline.RenderPrompt("rerank", map[string]any{
		"Publication": pub.Name, "Audience": pub.Audience,
		"EditorialLine": pub.EditorialLine, "Items": list,
	})
	if err != nil {
		reply(false)
		return
	}
	resp, err := s.d.LLM.Complete(ctx, llm.Request{
		Model: s.d.Cfg.ModelTriage, Template: "rerank", User: user, MaxTokens: 2048,
	})
	if err != nil {
		reply(false)
		return
	}
	var order []int
	if err := decodeJSONArr(resp.Text, &order); err != nil {
		reply(false)
		return
	}
	if err := db(r).RecordPaidAction(ctx, store.PaidActionRerank, pub.ID); err != nil {
		slog.Warn("paid-action record failed", "err", err)
	}
	// Persist the model's order as descending triage scores: reranked
	// stories float to the top in the returned sequence; anything the
	// answer dropped keeps its score below them.
	var updates []store.StoryTriage
	top := 12.0
	seen := map[int]bool{}
	for pos, ref := range order {
		if ref < 1 || ref > len(list) || seen[ref] {
			continue
		}
		seen[ref] = true
		updates = append(updates, store.StoryTriage{
			StoryID: list[ref-1].StoryID, Score: top - float64(pos)*0.01,
			Reason: list[ref-1].Reason,
		})
	}
	if len(updates) > 0 {
		if err := db(r).SetStoriesTriage(ctx, updates); err != nil {
			apiDBErr(w, r, err, "rerank")
			return
		}
	}
	reply(len(updates) > 0)
}

// decodeJSONArr tolerates a fenced/explained JSON array answer.
func decodeJSONArr(text string, out *[]int) error {
	s := strings.TrimSpace(text)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), out); err == nil {
		return nil
	}
	start, end := strings.Index(s, "["), strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return fmt.Errorf("no JSON array in answer")
	}
	return json.Unmarshal([]byte(s[start:end+1]), out)
}

func sectionViews(secs []config.Section) []map[string]any {
	out := []map[string]any{}
	for _, sec := range secs {
		out = append(out, map[string]any{
			"id": sec.ID, "title": sec.Title, "capacity": sec.Items,
			"commentary": sec.Commentary, "format": sec.Format, "access": sec.Access,
			"topic": sec.Topic,
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
	for _, sec := range issueSections(iss, pub) {
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
				fmt.Sprintf("section %q holds %d %s — the lineup asks for %d", e.Section, cap_, plural(cap_, "item", "items"), counts[e.Section]),
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
	views, meta, err := s.candidatePage(r, cands, 0, candidatePageSize, pub, issueSections(iss, pub))
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	resp := map[string]any{"candidates": views}
	maps.Copy(resp, meta)
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
		row := map[string]any{
			"id": it.ID, "title": it.Title, "url": "/a/" + fmt.Sprint(it.ID),
			"outlet": n, "published_at": it.PublishedAt,
			"snippet": truncateStr(it.ContentText, 400),
		}
		if it.Signal() {
			// Link-level signal: shown as "via <source>" with its link —
			// never as citation evidence (D30).
			row["signal"] = true
			row["via"] = n
		}
		items = append(items, row)
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

// guideProvider is the provider the guide rebuild may call: nil under
// llm_mode=off so editorial.Rebuild takes its deterministic fallback
// without ever invoking the model (KLOX-55).
func (s *Server) guideProvider() llm.Provider {
	if s.d.Cfg.LLMOff() {
		return nil
	}
	return s.d.LLM
}

// guideAfterRebuild rebuilds the editorial guide and reports what it
// learned ("Rule learned: …" toasts in the new interface). Under
// llm_mode=off a nil provider runs Rebuild's deterministic fallback, so
// a reasoned rejection persists a guide version with no model call.
func (s *Server) guideAfterRebuild(w http.ResponseWriter, r *http.Request, pub *store.Publication) any {
	prevV, prevC, _ := db(r).LatestGuide(r.Context(), pub.ID)
	content, version, err := editorial.Rebuild(r.Context(), db(r), pub, s.guideProvider(), s.d.Cfg.ModelTriage)
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

	pending := doc.dctx.pending
	c := apiCheck{Code: "pending_decisions", OK: pending == 0, Count: pending,
		Message: fmt.Sprintf("%d %s still open", pending, plural(pending, "story decision", "story decisions"))}
	if !c.OK {
		c.Target = &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}
	}
	checks = append(checks, c)

	uncited, firstUncited := 0, ""
	for _, sec := range doc.Sections {
		if sec.Format == config.SectionLinks {
			// a links item's source is the link itself — no written
			// sentence, citation or flag gate applies (KLOX-65)
			continue
		}
		for _, it := range sec.Items {
			if it.LinkOnly {
				// same exemption per-item: a link-only pauta ships the
				// story link with zero written text (KLOX-66 D2)
				continue
			}
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
		Message: fmt.Sprintf("%d %s without a citation", uncited, plural(uncited, "sentence", "sentences"))}
	if !c.OK {
		c.Target = &apiTarget{Kind: "sentence", Ref: firstUncited}
	}
	checks = append(checks, c)

	// coverage.flagged counts live flags only — a stored flag matching
	// no sentence gives the editor nothing to act on, so it must not
	// keep blocking approval (KLOX-52)
	live := doc.Coverage.Flagged
	c = apiCheck{Code: "flags_unresolved", OK: live == 0, Count: live,
		Message: fmt.Sprintf("%d flagged %s unresolved — edit, mark as interpretation or remove each", live, plural(live, "sentence", "sentences"))}
	if !c.OK {
		// point at the first flagged sentence we can locate
		ref := ""
	search:
		for _, sec := range doc.Sections {
			if sec.Format == config.SectionLinks {
				continue
			}
			for _, it := range sec.Items {
				if it.LinkOnly {
					continue
				}
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

	// manual compose leaves item slots empty until the editor writes
	// them — an empty item blocks approval exactly like an uncited fact
	empty, firstEmpty := 0, ""
	for _, sec := range doc.Sections {
		if sec.Format == config.SectionLinks {
			continue
		}
		for _, it := range sec.Items {
			if it.LinkOnly {
				continue
			}
			if len(it.Sentences) == 0 {
				empty++
				if firstEmpty == "" {
					firstEmpty = fmt.Sprint(it.Ref)
				}
			}
		}
	}
	c = apiCheck{Code: "empty_items", OK: empty == 0, Count: empty,
		Message: fmt.Sprintf("%d %s still empty — write each item's text", empty, plural(empty, "item", "items"))}
	if !c.OK {
		c.Target = &apiTarget{Kind: "item", Ref: firstEmpty}
	}
	checks = append(checks, c)

	missing := []string{}
	// doc.pub carries the edition's own section snapshot — required
	// commentary is checked against what the run configured, not what
	// the builder says today (KLOX-65)
	if doc != nil && doc.pub != nil {
		for _, sc := range doc.pub.Sections {
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
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	checks := s.readinessChecks(r, iss, doc.pub, doc)
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
	// the build board owns the lineup now: every section the edition
	// was configured with needs at least one item before drafting —
	// the snapshot, not the builder's current state (KLOX-65)
	lineup, _ := db(r).IssueItems(r.Context(), iss.ID)
	filled := map[string]int{}
	for _, ii := range lineup {
		if ii.Status == "pending" || ii.Status == "approved" {
			filled[ii.Section]++
		}
	}
	var reasons []apiReason
	for _, sec := range issueSections(iss, pub) {
		if filled[sec.Title] == 0 {
			reasons = append(reasons, apiReason{Code: "section_empty",
				Message: fmt.Sprintf("section %q has no stories", sec.Title),
				Target:  &apiTarget{Kind: "section", Ref: sec.Title}})
		}
	}
	if len(reasons) > 0 {
		apiErr(w, 409, "blocked", fmt.Sprintf("%d %s still empty — fill each one before drafting", len(reasons), plural(len(reasons), "section", "sections")), reasons...)
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
	// Same spend-guard admission as prepare runs: a refusal comes back
	// as a finished, blocked run with the message and recorded sample.
	if b := s.admitRun(r.Context(), wsp.ID); b != nil {
		st.blockRun(b)
		apiJSON(w, 202, map[string]any{"run": runView(st)})
		return
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
			MaxDeepDives:  s.d.Cfg.MaxDeepDives,
			DemoMode:      s.d.Cfg.Mode == "demo",
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
		ctx, cancel := context.WithTimeout(llm.WithRunScope(context.Background(), wsp.ID), s.runTimeout())
		defer cancel()
		res, err := runner.RunPhaseB(ctx, pubWithIssueSections(iss, pub), iss.ID)
		st.mu.Lock()
		defer st.mu.Unlock()
		st.st.Done = true
		if err != nil {
			st.st.Err = err.Error()
			if b, ok := llm.AsBlocked(err); ok {
				st.st.Blocked = true
				st.st.BlockedReason = string(b.Reason)
				st.st.Sample = true
			}
			return
		}
		st.st.IssueID = res.IssueID
	}()
	apiJSON(w, 202, map[string]any{"run": runView(st)})
}

// apiCompose writes the manual-compose skeleton: the board's lineup
// becomes the issue document with empty item slots — headlines and
// source lists only, no model-written text. Pending lineup items are
// approved as-is. The whole path is deterministic: items missing a
// dossier get one built from their collected items, and the endpoint
// never touches the LLM provider.
func (s *Server) apiCompose(w http.ResponseWriter, r *http.Request) {
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
	// compose is for empty issues: it must never overwrite written work
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return
	}
	written := 0
	for _, sec := range doc.Sections {
		for _, it := range sec.Items {
			written += len(it.Sentences)
		}
	}
	if written > 0 {
		apiErr(w, 409, "not_empty", "this issue already has written content — compose is only for empty issues")
		return
	}
	// same lineup gate as drafting: every section the edition was
	// configured with needs a story (snapshot, not builder — KLOX-65)
	lineup, _ := db(r).IssueItems(r.Context(), iss.ID)
	filled := map[string]int{}
	for _, ii := range lineup {
		if ii.Status == "pending" || ii.Status == "approved" {
			filled[ii.Section]++
		}
	}
	var reasons []apiReason
	for _, sec := range issueSections(iss, pub) {
		if filled[sec.Title] == 0 {
			reasons = append(reasons, apiReason{Code: "section_empty",
				Message: fmt.Sprintf("section %q has no stories", sec.Title),
				Target:  &apiTarget{Kind: "section", Ref: sec.Title}})
		}
	}
	if len(reasons) > 0 {
		apiErr(w, 409, "blocked", fmt.Sprintf("%d %s still empty — fill each one before composing", len(reasons), plural(len(reasons), "section", "sections")), reasons...)
		return
	}
	ctx := r.Context()
	for _, ii := range lineup {
		if ii.Status == "pending" {
			if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, ii.ID, "approved"), "item") {
				return
			}
		}
	}
	runner := &pipeline.Runner{DB: db(r)}
	// AI on (real mode): each pauta arrives prefilled from the shared
	// research cache — one Haiku call per new story, capped and
	// degrading to the manual slot (KLOX-67 §7.3). Demo replay and
	// LLM-off keep the deterministic, model-free skeleton untouched.
	if s.d.Pool != nil && s.d.Cfg.Mode != "demo" && !s.d.Cfg.LLMOff() {
		runner.Research = &pipeline.Researcher{
			Pool: s.d.Pool, LLM: s.d.LLM, Model: s.d.Cfg.ModelTriage,
			PaidActionsPerDay: s.d.Cfg.PaidActionsPerDay,
		}
	}
	res, err := runner.ComposeSkeleton(ctx, pubWithIssueSections(iss, pub), iss.ID)
	if err != nil {
		apiErr(w, 409, "blocked", err.Error())
		return
	}
	db(r).RecordDecision(ctx, iss.PublicationID, 0, "manual_compose",
		fmt.Sprintf("lineup of %d %s became empty slots for manual writing", res.Selected, plural(res.Selected, "item", "items")))
	fresh, err := db(r).GetIssue(ctx, iss.ID)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	apiJSON(w, 200, map[string]any{"issue": s.issueSummary(r, fresh), "selected": res.Selected})
}

// apiComposeSentence adds editor-written text to an item — the manual
// counterpart of a drafted sentence. Multi-sentence input is stored one
// sentence per line; each factual sentence carries every chosen [n]
// marker, so citecheck and readiness treat it exactly like AI-drafted
// content (an uncited addition fails the uncited_sentences check).
// kind "interpretation" stores an editor-attributed block instead.
func (s *Server) apiComposeSentence(w http.ResponseWriter, r *http.Request) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	ref, _ := strconv.Atoi(r.PathValue("ref"))
	item := doc.findItem(ref)
	if item == nil {
		apiErr(w, 404, "not_found", "item not found in the document")
		return
	}
	var in struct {
		Text      string `json:"text"`
		Kind      string `json:"kind"`
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
		apiErr(w, 400, "validation", "item text cannot be empty",
			apiReason{Code: "empty_sentence", Message: "item text cannot be empty",
				Target: &apiTarget{Kind: "item", Ref: fmt.Sprint(ref)}})
		return
	}
	if !authorTextOK(text) {
		apiErr(w, 400, "validation", "item text must be a single line with no document markup")
		return
	}
	kind := in.Kind
	if kind == "" {
		kind = "fact"
	}
	if kind != "fact" && kind != "interpretation" {
		apiErr(w, 400, "validation", "kind must be fact or interpretation")
		return
	}
	var raw string
	var newFlags []store.Flag
	if kind == "interpretation" {
		// (KLOX-48/51) an opinion carrying a number, a date or a
		// quotation is a fact, not a take — it may not bypass the
		// citation check, same guard as mark-as-interpretation.
		if pipeline.HasNumberDateOrQuote(text) {
			apiErr(w, 409, "interpretation_restricted",
				"a claim with a number, a date or a quotation cannot be marked as interpretation — cite a source instead",
				apiReason{Code: "interpretation_restricted", Message: "sentence contains a number, a date or a quotation",
					Target: &apiTarget{Kind: "item", Ref: fmt.Sprint(ref)}})
			return
		}
		raw = "> _Interpretação: " + text + "_"
	} else {
		d := item.dossier
		maxRef := 0
		if d != nil {
			maxRef = len(d.Sources)
		}
		seen := map[int]bool{}
		markers := ""
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
			markers += fmt.Sprintf(" [%d]", n)
		}
		// store one sentence per line, each carrying the citations, so
		// the document parser and citecheck see the same shape as a
		// drafted item
		var lines []string
		for _, piece := range pipeline.SplitSentences(text) {
			piece = strings.TrimSpace(piece)
			if !strings.ContainsFunc(piece, unicode.IsLetter) {
				continue
			}
			lines = append(lines, piece+markers)
		}
		if len(lines) == 0 {
			apiErr(w, 400, "validation", "no sentence found in the item text")
			return
		}
		raw = strings.Join(lines, "\n")
		// (KLOX-51) a composed fact must be verifiable exactly like a
		// drafted sentence: every line the citation floor cannot verify —
		// or that copies the source — lands flagged, and the flag blocks
		// approval until the editor fixes or removes the sentence.
		seenFlags := map[string]bool{}
		for _, f := range iss.Flags {
			if f.Item == int64(ref) {
				seenFlags[f.Sentence] = true
			}
		}
		for _, line := range lines {
			notes := s.composeFlagNotes(item, doc.dctx, line)
			if len(notes) == 0 || seenFlags[line] {
				continue
			}
			seenFlags[line] = true
			newFlags = append(newFlags, store.Flag{
				Item: int64(ref), Sentence: line, Note: strings.Join(notes, "; ")})
		}
	}
	// append at the end of the item's body (before its sources block)
	body, ok := insertInItem(iss.BodyMD, item, len(item.Sentences), raw)
	if !ok {
		apiErr(w, 409, "conflict", "the item no longer accepts this text")
		return
	}
	ctx := r.Context()
	flags := iss.Flags
	if len(newFlags) > 0 {
		flags = append(append([]store.Flag{}, iss.Flags...), newFlags...)
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: item.IssueItemID, Body: body,
		Flags: flags, Decision: "manual_compose", Reason: raw, RevertApproved: iss.Status == "approved",
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == "approved" {
		iss.Status = "drafted"
	}
	s.respondSentence(w, r, iss, doc, map[string]any{
		"added":   len(strings.Split(raw, "\n")),
		"flagged": len(newFlags),
	})
}

// --- document / sentence mutations / commentary ---

func (s *Server) apiDocument(w http.ResponseWriter, r *http.Request) {
	_, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	apiJSON(w, 200, map[string]any{"document": doc})
}

// issueDoc loads the issue, its canonical publication and the parsed
// document in a single Postgres round-trip. Every remote-DB statement
// costs ~150 ms on the hosted app, so the issue→publication→dossiers→
// items chain this replaced could not meet the KLOX-47 latency targets.
func (s *Server) issueDoc(w http.ResponseWriter, r *http.Request) (*store.Issue, *issueDocument, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		apiDBErr(w, r, err, "issue")
		return nil, nil, false
	}
	b, err := db(r).IssueDocBundle(r.Context(), id)
	if apiDBErr(w, r, err, "issue") {
		return nil, nil, false
	}
	pub, err := s.canonicalPub(b.Pub, nil)
	if apiDBErr(w, r, err, "publication") {
		return nil, nil, false
	}
	return b.Issue, parseIssueDocument(b.Issue, pub, dcFromPairs(b.Pairs, b.Items)), true
}

// apiSentenceCtx loads issue, document and locates a sentence.
func (s *Server) apiSentenceCtx(w http.ResponseWriter, r *http.Request) (*store.Issue, *issueDocument, *locatedSentence, bool) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return nil, nil, nil, false
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return nil, nil, nil, false
	}
	loc := doc.findSentence(r.PathValue("sid"))
	if loc == nil {
		apiErr(w, 404, "not_found", "sentence not found")
		return nil, nil, nil, false
	}
	return iss, doc, loc, true
}

// dropFlagRaw removes the flag matching (item ref, raw sentence) and
// returns the list the issue should carry after the write — identical
// to the current flags when nothing matched.
func dropFlagRaw(iss *store.Issue, itemRef int64, raw string) ([]store.Flag, bool) {
	out := iss.Flags[:0]
	dropped := false
	for _, f := range iss.Flags {
		if f.Item == itemRef && f.Sentence == raw {
			dropped = true
			continue
		}
		out = append(out, f)
	}
	return out, dropped
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
	if !authorTextOK(text) {
		apiErr(w, 400, "validation", "edited sentence must be a single line with no document markup")
		return
	}
	ctx := r.Context()
	var replacement string
	if loc.sentence.Kind == "interpretation" {
		// (KLOX-48/51) same guard as compose and mark-as-interpretation:
		// a claim carrying a number, a date or a quotation is a fact —
		// editing its text may not sneak it past the citation check.
		if pipeline.HasNumberDateOrQuote(text) {
			apiErr(w, 409, "interpretation_restricted",
				"a claim with a number, a date or a quotation cannot be marked as interpretation — cite a source instead",
				apiReason{Code: "interpretation_restricted", Message: "sentence contains a number, a date or a quotation",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		replacement = "> _Interpretação: " + text + "_"
	} else {
		// fact: citations must point at this item's sources and the new
		// claim must stay supported by them
		d := loc.item.dossier
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
		if !s.editSupportedFor(r, loc.item, doc.dctx, int64(loc.item.Ref), composed) {
			apiErr(w, 409, "unsupported_claim",
				"the citation check still cannot verify this sentence against the cited source — the flag stays",
				apiReason{Code: "unsupported_claim", Message: "edit could not be verified against the cited source",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		if s.tooCloseToSources(loc.item, doc.dctx, composed) {
			apiErr(w, 409, "too_close_to_source",
				"the sentence copies the source almost word for word — rewrite it in the editor's own words",
				apiReason{Code: "too_close_to_source", Message: "sentence copies a run of source words",
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
	flags, dropped := dropFlagRaw(iss, int64(loc.item.Ref), loc.sentence.raw)
	kind := "sentence_edit"
	if dropped {
		kind = "flag_edit"
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: loc.item.IssueItemID, Body: body,
		Flags: flags, Decision: kind, Reason: loc.sentence.raw + " → " + text,
		RevertApproved: iss.Status == "approved",
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == "approved" {
		iss.Status = "drafted"
	}
	s.respondSentence(w, r, iss, doc, nil)
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
	flags, dropped := dropFlagRaw(iss, int64(loc.item.Ref), loc.sentence.raw)
	kind := "sentence_remove"
	if dropped {
		kind = "flag_remove"
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: loc.item.IssueItemID, Body: body,
		Flags: flags, Decision: kind, Reason: loc.sentence.raw,
		RevertApproved: iss.Status == "approved",
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == "approved" {
		iss.Status = "drafted"
	}
	s.respondSentence(w, r, iss, doc, map[string]any{"removed": removed})
}

// restoreEntry is one removed sentence the client asks to put back:
// the undo payload DELETE (or the bulk flagged-removal) returned.
type restoreEntry struct {
	Index     int    `json:"index"`
	Text      string `json:"text"`
	Kind      string `json:"kind"`
	Citations []int  `json:"citations"`
	Flag      string `json:"flag,omitempty"`
}

// apiSentenceRestore re-inserts removed sentences into their item —
// the Undo for apiSentenceRemove and apiItemRemoveFlagged. The client
// sends back the payload(s) DELETE returned — one flat object, or
// {"sentences": [...]} for a batch; the server composes the stored
// form at each recorded index inside the item's span and re-attaches
// the flags they carried, all in one write.
func (s *Server) apiSentenceRestore(w http.ResponseWriter, r *http.Request) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	ref, _ := strconv.Atoi(r.PathValue("ref"))
	item := doc.findItem(ref)
	if item == nil {
		apiErr(w, 404, "not_found", "item not found in the document")
		return
	}
	var in struct {
		restoreEntry
		Sentences []restoreEntry `json:"sentences"`
		Version   string         `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	entries := in.Sentences
	if len(entries) == 0 {
		entries = []restoreEntry{in.restoreEntry}
	}
	// compose every entry's stored form first — a bad citation or an
	// empty text refuses the whole restore, never half of it
	maxRef := 0
	if item.dossier != nil {
		maxRef = len(item.dossier.Sources)
	}
	raws := make([]string, len(entries))
	for i, e := range entries {
		text := displayText(e.Text)
		if text == "" {
			apiErr(w, 400, "validation", "restored sentence cannot be empty")
			return
		}
		if !authorTextOK(text) {
			apiErr(w, 400, "validation", "restored sentence must be a single line with no document markup")
			return
		}
		if e.Kind == "interpretation" {
			raws[i] = "> _Interpretação: " + text + "_"
			continue
		}
		raw := text
		seen := map[int]bool{}
		for _, n := range e.Citations {
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
		raws[i] = raw
	}
	if item.spanStart < 0 || item.spanEnd > len(iss.BodyMD) || item.spanStart >= item.spanEnd {
		apiErr(w, 409, "conflict", "the item no longer accepts this sentence")
		return
	}
	// splice in ascending recorded index — each entry lands back at the
	// position it was removed from, in one body write
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return entries[order[a]].Index < entries[order[b]].Index })
	seg := iss.BodyMD[item.spanStart:item.spanEnd]
	cur := make([]string, len(item.Sentences))
	for i, sn := range item.Sentences {
		cur[i] = sn.raw
	}
	for _, oi := range order {
		idx := entries[oi].Index
		if idx < 0 {
			idx = 0
		}
		if idx > len(cur) {
			idx = len(cur)
		}
		seg = insertRaw(seg, cur, idx, raws[oi])
		cur = slices.Insert(cur, idx, raws[oi])
	}
	body := iss.BodyMD[:item.spanStart] + seg + iss.BodyMD[item.spanEnd:]
	ctx := r.Context()
	flags := append([]store.Flag{}, iss.Flags...)
	restored := 0
	for _, oi := range order {
		if entries[oi].Flag != "" {
			flags = append(flags, store.Flag{Item: int64(ref), Sentence: raws[oi], Note: entries[oi].Flag})
			restored++
		}
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: item.IssueItemID, Body: body, Flags: flags,
		Decision:       "sentence_restore",
		Reason:         fmt.Sprintf("%d %s restored to item %d", len(entries), plural(len(entries), "sentence", "sentences"), ref),
		RevertApproved: iss.Status == "approved",
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == "approved" {
		iss.Status = "drafted"
	}
	s.respondSentence(w, r, iss, doc, nil)
}

// apiItemRemoveFlagged deletes every flagged sentence of one item in a
// single write — the editor's bulk action for a flagged-heavy item.
// The response carries the removed payload list so the client can offer
// Undo: POST /issues/{id}/items/{ref}/sentences restores them.
func (s *Server) apiItemRemoveFlagged(w http.ResponseWriter, r *http.Request) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	ref, _ := strconv.Atoi(r.PathValue("ref"))
	item := doc.findItem(ref)
	if item == nil {
		apiErr(w, 404, "not_found", "item not found in the document")
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
	if item.spanStart < 0 || item.spanEnd > len(iss.BodyMD) || item.spanStart >= item.spanEnd {
		apiErr(w, 409, "conflict", "the item is no longer in the body")
		return
	}
	// remove every flagged sentence inside the item's span — one write
	// for the whole batch, undo payload in document order
	seg := iss.BodyMD[item.spanStart:item.spanEnd]
	removedRaws := map[string]bool{}
	var removed []map[string]any
	for i := range item.Sentences {
		sn := &item.Sentences[i]
		if sn.Flag == nil {
			continue
		}
		ns, ok := removeRaw(seg, sn.raw)
		if !ok {
			continue
		}
		seg = ns
		removedRaws[sn.raw] = true
		entry := map[string]any{
			"ref": item.Ref, "index": i, "text": sn.Text,
			"kind": sn.Kind, "citations": orEmpty(sn.Citations),
			"flag": sn.Flag.Note,
		}
		removed = append(removed, entry)
	}
	if len(removed) == 0 {
		apiErr(w, 409, "nothing_flagged", "this item has no flagged sentences")
		return
	}
	body := iss.BodyMD[:item.spanStart] + seg + iss.BodyMD[item.spanEnd:]
	ctx := r.Context()
	kept := make([]store.Flag, 0, len(iss.Flags))
	for _, f := range iss.Flags {
		if f.Item == int64(item.Ref) && removedRaws[f.Sentence] {
			continue
		}
		kept = append(kept, f)
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: item.IssueItemID, Body: body, Flags: kept,
		Decision:       "flagged_bulk_remove",
		Reason:         fmt.Sprintf("%d flagged %s removed from item %d", len(removed), plural(len(removed), "sentence", "sentences"), item.Ref),
		RevertApproved: iss.Status == "approved",
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = kept
	if iss.Status == "approved" {
		iss.Status = "drafted"
	}
	s.respondSentence(w, r, iss, doc, map[string]any{"removed": removed})
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
	if !authorTextOK(text) {
		apiErr(w, 400, "validation", "interpretation text must be a single line with no document markup")
		return
	}
	// (KLOX-48) a claim carrying a number, a date or a quotation is a
	// fact, not an opinion — it may not escape the citation check by
	// being relabeled; fix the claim or remove it.
	if pipeline.HasNumberDateOrQuote(loc.sentence.Text) || pipeline.HasNumberDateOrQuote(text) {
		apiErr(w, 409, "interpretation_restricted",
			"a claim with a number, a date or a quotation cannot be marked as interpretation — fix it or remove it",
			apiReason{Code: "interpretation_restricted", Message: "sentence contains a number, a date or a quotation",
				Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
		return
	}
	ctx := r.Context()
	replacement := "\n> _Interpretação: " + text + "_\n"
	body, ok := replaceInItem(iss.BodyMD, loc.item, loc.sentence.raw, replacement)
	if !ok {
		apiErr(w, 409, "sentence_gone", "the sentence is no longer in the body")
		return
	}
	flags, dropped := dropFlagRaw(iss, int64(loc.item.Ref), loc.sentence.raw)
	kind := "mark_interpretation"
	if dropped {
		kind = "flag_interpretation"
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: loc.item.IssueItemID, Body: body,
		Flags: flags, Decision: kind, Reason: loc.sentence.raw,
		RevertApproved: iss.Status == "approved",
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == "approved" {
		iss.Status = "drafted"
	}
	s.respondSentence(w, r, iss, doc, nil)
}

// respondSentence answers a mutation with the post-write document
// state: fresh coverage and version, plus the mutated sentence when it
// still exists. The document re-parses in memory over the request's
// already-loaded context — the dossier/item queries happen once per
// request, not once per build (KLOX-47). extra keys (e.g. "removed"
// for undo) are merged into the response.
func (s *Server) respondSentence(w http.ResponseWriter, r *http.Request, iss *store.Issue, doc *issueDocument, extra map[string]any) {
	// no reload: callers update iss in memory after SaveIssueEdit, so the
	// re-parse sees the post-write state without a second round-trip
	doc = parseIssueDocument(iss, doc.pub, doc.dctx)
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
	// Approval-time AI citecheck (KLOX-67 §7.5): the deterministic
	// floor passed, so exactly one Haiku call checks for inverted
	// meaning across every cited fact — author-edited AI claims are
	// re-checked here like any other. Capped per edition per day and
	// counted against the workspace's paid actions; on cap, kill
	// switch or API failure the deterministic results stand alone.
	if s.approveCiteCheck(w, r, iss, pub) {
		return
	}
	rec, err := s.recordApproval(r, iss, pub, checks)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	fresh, _ := db(r).GetIssue(r.Context(), iss.ID)
	apiJSON(w, 200, map[string]any{"issue": s.issueSummary(r, fresh), "receipt": receiptView(rec)})
}

// approveCiteCheck runs the one Haiku inverted-meaning pass at
// approval. It writes a response and returns true only when a verdict
// blocked approval; every failure mode (caps, kill switch, provider
// error) returns false so approval proceeds on deterministic checks.
func (s *Server) approveCiteCheck(w http.ResponseWriter, r *http.Request, iss *store.Issue, pub *store.Publication) bool {
	ctx := r.Context()
	if s.d.Cfg.Mode == "demo" || s.d.Cfg.LLMOff() || s.d.LLM == nil {
		return false // real-mode AI only; demo keeps the deterministic floor
	}
	if s.d.Cfg.CiteChecksPerEdition > 0 {
		if n, err := db(r).PaidActionCount(ctx, store.PaidActionCiteCheck, iss.ID); err == nil && n >= s.d.Cfg.CiteChecksPerEdition {
			return false
		}
	}
	if s.d.Cfg.PaidActionsPerDay > 0 {
		if n, err := db(r).PaidActionsToday(ctx); err == nil && n >= s.d.Cfg.PaidActionsPerDay {
			return false
		}
	}
	doc, err := buildIssueDocument(ctx, db(r), iss, pub)
	if err != nil {
		return false
	}
	type sent struct {
		ID    int    `json:"id"`
		Item  int    `json:"item"`
		Refs  string `json:"refs"`
		Text  string `json:"text"`
	}
	var sents []sent
	var items []map[string]any
	// byID maps the check's sentence id back to its raw stored text —
	// the flag's key in the document.
	rawByID := map[int]string{}
	itemRefByID := map[int]int64{}
	id := 0
	for _, sec := range doc.Sections {
		for _, it := range sec.Items {
			var srcs []map[string]any
			for _, src := range it.Sources {
				srcs = append(srcs, map[string]any{"ref": src.Ref, "url": src.URL})
			}
			if len(srcs) > 0 {
				items = append(items, map[string]any{"ref": it.Ref, "sources": srcs})
			}
			for _, sn := range it.Sentences {
				if sn.Kind != "fact" || len(sn.Citations) == 0 {
					continue
				}
				id++
				refs := make([]string, len(sn.Citations))
				for i, c := range sn.Citations {
					refs[i] = fmt.Sprint(c)
				}
				sents = append(sents, sent{ID: id, Item: it.Ref,
					Refs: strings.Join(refs, ","), Text: sn.Text})
				rawByID[id] = sn.raw
				itemRefByID[id] = int64(it.Ref)
			}
		}
	}
	if len(sents) == 0 {
		return false
	}
	user, err := pipeline.RenderPrompt("citecheck", map[string]any{
		"Publication": pub.Name, "Sentences": sents, "Items": items,
	})
	if err != nil {
		return false
	}
	resp, err := s.d.LLM.Complete(ctx, llm.Request{
		Model: s.d.Cfg.ModelTriage, Template: "citecheck", User: user, MaxTokens: 4096,
	})
	if err != nil {
		slog.Info("approval citecheck skipped", "err", err)
		return false // blocked/failed → deterministic results stand (§7.5)
	}
	if err := db(r).RecordPaidAction(ctx, store.PaidActionCiteCheck, iss.ID); err != nil {
		slog.Warn("paid-action record failed", "err", err)
	}
	var checks []struct {
		ID      int    `json:"id"`
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Text)), &checks); err != nil {
		// the model answered non-JSON — treat as unavailable
		var anyJSON []struct {
			ID      int    `json:"id"`
			Verdict string `json:"verdict"`
			Note    string `json:"note"`
		}
		start := strings.Index(resp.Text, "[")
		end := strings.LastIndex(resp.Text, "]")
		if start < 0 || end <= start || json.Unmarshal([]byte(resp.Text[start:end+1]), &anyJSON) != nil {
			return false
		}
		checks = anyJSON
	}
	var newFlags []store.Flag
	var reasons []apiReason
	for _, c := range checks {
		if c.Verdict != "unsupported" {
			continue
		}
		raw, ok := rawByID[c.ID]
		if !ok {
			continue
		}
		note := c.Note
		if note == "" {
			note = "citation check: claim not supported by the cited source"
		}
		newFlags = append(newFlags, store.Flag{Item: itemRefByID[c.ID], Sentence: raw, Note: note})
		reasons = append(reasons, apiReason{Code: "unsupported_claim",
			Message: note, Target: &apiTarget{Kind: "sentence", Ref: fmt.Sprintf("%d.%d", itemRefByID[c.ID], c.ID)}})
	}
	if len(newFlags) == 0 {
		return false
	}
	flags := append(append([]store.Flag{}, iss.Flags...), newFlags...)
	if err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, Body: iss.BodyMD, Flags: flags,
		Decision: "citecheck", Reason: fmt.Sprintf("approval-time check flagged %d %s", len(newFlags), plural(len(newFlags), "sentence", "sentences")),
	}); err != nil {
		slog.Warn("citecheck flag save failed", "err", err)
		return false
	}
	apiErr(w, 409, "blocked", "the citation check found claims its sources do not support — edit or remove each", reasons...)
	return true
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
		db(r).RecordDecision(ctx, iss.PublicationID, 0, "approve", approveDecisionReason(iss.ID))
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
		// the approver is the claimed owner — an anonymous session has
		// no name to show, so it stays empty rather than leaking the
		// workspace label (§2 never-show, KLOX-64)
		Approver:   s.workspaceOwner(ctx, db(r)),
		ChecksJSON: string(cj),
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
		emailID, err := s.sendIssueMail(r.Context(), db(r), reqWS(r).ID, iss, pub, to, kind, s.publicBase(r))
		if apiDBErr(w, r, err, "send") {
			return
		}
		if kind == "issue" {
			if apiDBErr(w, r, db(r).SetIssueStatus(r.Context(), iss.ID, "sent"), "issue") {
				return
			}
		} else {
			// a test send stamps the marker but never enters the
			// lifecycle — the issue stays approved and editable (KLOX-53)
			if apiDBErr(w, r, db(r).MarkIssueTestSent(r.Context(), iss.ID), "issue") {
				return
			}
		}
		if fresh, _ := db(r).GetIssue(r.Context(), iss.ID); fresh != nil {
			iss = fresh
		}
		apiJSON(w, 200, map[string]any{
			"issue": s.issueSummary(r, iss), "outbox_email_id": emailID, "audience": in.Audience,
			"to": to, "subject": localizeSubject(iss.Subject, pub.Language), "delivery": "outbox",
		})
		return
	}

	// Live delivery (D33): reader sends go to confirmed readers of a
	// claimed workspace; test sends go to the claimed address only. An
	// unclaimed workspace may still "send a test" — it writes to the
	// outbox and completes the journey: the test_sent_at marker unlocks
	// the after-sending screen and the public page while the issue stays
	// approved and editable (KLOX-49, KLOX-53). Real email still
	// requires a claimed address.
	ctx := r.Context()
	wdb := db(r)
	owner := s.workspaceOwner(ctx, wdb)
	if owner == "" {
		if in.Audience != "test" {
			apiErr(w, 409, "unclaimed_workspace",
				"claim the workspace before sending — verify the creator email in workspace settings")
			return
		}
		to := []string{demoReaderEmail}
		if addr := strings.TrimSpace(in.To); addr != "" {
			to = []string{addr}
		}
		emailID, err := s.sendIssueMail(ctx, wdb, reqWS(r).ID, iss, pub, to, "test", s.publicBase(r))
		if apiDBErr(w, r, err, "send") {
			return
		}
		if apiDBErr(w, r, wdb.MarkIssueTestSent(ctx, iss.ID), "issue") {
			return
		}
		if fresh, _ := wdb.GetIssue(ctx, iss.ID); fresh != nil {
			iss = fresh
		}
		apiJSON(w, 200, map[string]any{
			"issue": s.issueSummary(r, iss), "outbox_email_id": emailID, "audience": in.Audience,
			"to": to, "subject": localizeSubject(iss.Subject, pub.Language), "delivery": "outbox",
		})
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
	sum, err := s.sendIssueLive(ctx, wdb, reqWS(r).ID, iss, pub, kind, owner, s.publicBase(r), idemNS, readers)
	if apiDBErr(w, r, err, "send") {
		return
	}
	if kind == "test" && len(sum.Refused) > 0 {
		apiErr(w, 429, "send_refused", "test send refused: "+strings.Join(sum.Refused, "; "))
		return
	}
	// A test send stamps the test_sent_at marker but never enters the
	// lifecycle — the issue stays approved so it can still go to
	// readers (KLOX-53). A readers send marks it sent only when
	// something left the app: an all-refused, all-fallback or empty
	// send keeps it approved so it can go out once deliverable.
	if kind == "test" {
		if apiDBErr(w, r, wdb.MarkIssueTestSent(ctx, iss.ID), "issue") {
			return
		}
		if fresh, _ := wdb.GetIssue(ctx, iss.ID); fresh != nil {
			iss = fresh
		}
	}
	if kind == "issue" && sum.Sent+sum.Deduped > 0 {
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
		"to": sum.To, "subject": localizeSubject(iss.Subject, pub.Language), "delivery": deliv,
		"sent": sum.Sent, "refused": sum.Refused, "deduped": sum.Deduped,
	}
	if sum.Fallback > 0 {
		resp["fallback"] = true
	}
	if kind == "issue" && sum.Sent+sum.Deduped == 0 {
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
	rawPub, loadErr := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	pub, err := s.canonicalPub(rawPub, loadErr)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	if iss.Status != "approved" && iss.Status != "sent" {
		apiErr(w, 409, "blocked", "exports ship only approved output — approve the issue first",
			apiReason{Code: "not_approved", Message: "issue is not approved",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	aiUsed, _ := db(r).IssueUsedAI(r.Context(), iss.ID)
	md := delivery.ReaderBody(iss, pub, s.publicBase(r), aiUsed)
	md = s.localizeReaderMD(md, rawPub)
	// both spellings ship the same file — ?format= is the spelling the
	// audit reached for; ?fmt= is the documented one (KLOX-56).
	f := r.URL.Query().Get("fmt")
	if f == "" {
		f = r.URL.Query().Get("format")
	}
	switch f {
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=issue-%d.html", iss.Number))
		w.Write([]byte(delivery.RenderHTML(localizeSubject(iss.Subject, rawPub.Language), md, s.publicBase(r))))
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
	aiUsed, _ := db(r).IssueUsedAI(r.Context(), iss.ID)
	md := delivery.ReaderBody(iss, pub, s.publicBase(r), aiUsed)
	gated := false
	if reader == "non-subscriber" {
		md = gateSections(md, paidSectionTitles(iss, pub), pub.Language)
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
		// The publication outbox shows only the newsletter's own mail
		// (KLOX-66 §8): account-level magic-link mail belongs to the
		// workspace claim, never to an edition. Under the
		// one-newsletter rule every remaining kind — issue, test,
		// reader_confirm — is this newsletter's.
		if e.Kind == "magic_link" {
			continue
		}
		out = append(out, map[string]any{
			"id": e.ID, "kind": e.Kind, "to": e.To, "subject": e.Subject,
			"provider": e.Provider, "status": e.Status,
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
		"body_md": e.BodyMD, "body_html": e.BodyHTML,
		"provider": e.Provider, "provider_id": e.ProviderID, "status": e.Status,
		"error":      e.Error,
		"created_at": e.CreatedAt,
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
	link := s.publicBase(r) + "/p/" + pub.Slug + "/latest?key=" + token + s.tokQ(r)
	if iss, err := db(r).LatestPublicIssue(ctx, pub.ID); err == nil {
		link = fmt.Sprintf("%s/p/%s/%d?key=%s%s",
			s.publicBase(r), pub.Slug, iss.Number, token, s.tokQ(r))
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
	if iss.Status != "sent" && iss.Status != "scheduled" && iss.TestSentAt == "" {
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
	aiUsed, _ := db(r).IssueUsedAI(ctx, iss.ID)
	md := delivery.ReaderBody(iss, pub, s.publicBase(r), aiUsed)
	if !unlocked {
		md = gateSections(md, paidSectionTitles(iss, pub), pub.Language)
	}
	md = s.localizeReaderMD(md, rawPub)
	apiJSON(w, 200, map[string]any{
		"publication": map[string]any{"name": rawPub.Name, "slug": rawPub.Slug, "language": rawPub.Language},
		"issue":       map[string]any{"number": iss.Number, "subject": localizeSubject(iss.Subject, rawPub.Language), "created_at": iss.CreatedAt},
		"body_md":     md, "gated": !unlocked, "subscribed": active,
		"reader":        map[string]string{"name": demoReaderName},
		"paid_sections": orEmpty(paidSectionTitles(iss, pub)),
		// price on the reader page (KLOX-66): the gated card sells the
		// subscription with the creator's price and the paid sections
		"paywall": map[string]any{"enabled": rawPub.Paywall.Enabled, "price": localizePrice(rawPub.Paywall.Price, rawPub.Language)},
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
	if iss.Status != "sent" && iss.Status != "scheduled" && iss.TestSentAt == "" {
		apiErr(w, 409, "blocked", "the public page exists once the issue is sent, scheduled or test-sent",
			apiReason{Code: "not_sent", Message: "issue is not sent",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	// "Learned" rules must be rules the guide actually recorded
	// (KLOX-55): rejection reasons only feed this block when a
	// non-empty guide exists — otherwise the after-sending screen
	// claimed learning while the guide sat at version 0.
	version, guideContent, _ := db(r).LatestGuide(r.Context(), pub.ID)
	var learned []string
	if strings.TrimSpace(guideContent) != "" {
		rawReasons, _ := db(r).IssueRejectionReasons(r.Context(), iss.ID)
		learned = editorial.RulesFrom(rawReasons)
	}
	publicURL := fmt.Sprintf("%s/p/%s/%d", s.publicBase(r), pub.Slug, iss.Number)
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
	// Under llm_mode=off Rebuild runs its deterministic fallback on a
	// nil provider — the same condensation a rejection-triggered
	// rebuild uses — so the manual rebuild stays available with zero
	// model calls (KLOX-55).
	content, version, err := editorial.Rebuild(r.Context(), db(r), pub, s.guideProvider(), s.d.Cfg.ModelTriage)
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

// editSupportedFor re-runs the citation check for an edited sentence
// against a document item's dossier — already parsed in the document
// context, no second store lookup.
func (s *Server) editSupportedFor(r *http.Request, item *docItem, dc *docContext, itemRef int64, text string) bool {
	d := item.dossier
	if d == nil {
		return false
	}
	return s.dossierSupports(r, d, dc, itemRef, text)
}

// citedCorpus is the stored text the citation floor and the originality
// rule verify against for one dossier source: the catalog item's stored
// body when it exists, else the dossier facts attributed to that ref —
// the same corpus the pipeline's citecheck loads.
func (s *Server) citedCorpus(d *pipeline.Dossier, dc *docContext, src pipeline.DossierSource) string {
	if dc != nil {
		if it, ok := dc.items[src.ItemID]; ok && it.ContentText != "" {
			return it.ContentText
		}
	}
	var b strings.Builder
	for _, f := range append(append([]pipeline.DossierFact{}, d.Facts...), d.DeepFacts...) {
		if f.Ref == src.Ref {
			b.WriteString(f.Text)
			b.WriteString(" ")
		}
	}
	return b.String()
}

// composeFlagNotes applies the deterministic half of citecheck to one
// composed (editor-written) line (KLOX-51): an uncited fact, a claim
// the cited source's stored text does not support, or a ≥10-word run
// copied from a source each produce a flag note identical to the ones
// the pipeline writes — the flag then blocks approval.
func (s *Server) composeFlagNotes(item *docItem, dc *docContext, line string) []string {
	var notes []string
	d := item.dossier
	byRef := map[int]pipeline.DossierSource{}
	if d != nil {
		for _, src := range d.Sources {
			byRef[src.Ref] = src
		}
	}
	nums := pipeline.CitationNums(line)
	if len(nums) == 0 {
		notes = append(notes, "sentence has no citation")
	}
	for _, n := range nums {
		src, ok := byRef[n]
		if !ok {
			notes = append(notes, fmt.Sprintf("cites source [%d] that does not exist", n))
			continue
		}
		// when the source carries no stored text at all the dossier
		// summary is the last-resort corpus — same fallback as
		// dossierSupports on the edit path.
		corpus := s.citedCorpus(d, dc, src)
		if corpus == "" {
			corpus = d.Summary
		}
		if !pipeline.SupportedBySource(line, corpus) {
			notes = append(notes, fmt.Sprintf("not supported by [%d]", n))
		}
	}
	if d != nil {
		for _, src := range d.Sources {
			if pipeline.TooCloseToSource(line, s.citedCorpus(d, dc, src)) {
				notes = append(notes, "too close to the source")
				break
			}
		}
	}
	return notes
}

// tooCloseToSources applies the originality rule to an edited sentence:
// it must not copy a run of source words — the write-time counterpart
// of citecheck's "too close to the source" flag.
func (s *Server) tooCloseToSources(item *docItem, dc *docContext, text string) bool {
	d := item.dossier
	if d == nil {
		return false
	}
	for _, src := range d.Sources {
		if pipeline.TooCloseToSource(text, s.citedCorpus(d, dc, src)) {
			return true
		}
	}
	return false
}

var refRe = regexp.MustCompile(`\[\d+\]`)

// dossierSupports is the citation check for one edited sentence
// (KLOX-48): every [n] must be a source of the item, and the cited
// source's stored text must pass the shared lexical floor — the model
// verdict (real mode) can only tighten that floor, never waive it, so
// an unrelated [n] can never clear a flag.
func (s *Server) dossierSupports(r *http.Request, d *pipeline.Dossier, dc *docContext, itemRef int64, text string) bool {
	byRef := map[int]pipeline.DossierSource{}
	for _, src := range d.Sources {
		byRef[src.Ref] = src
	}
	// Lexical floor against the cited sources' stored text. When an
	// item carries no stored body (a signal row) the dossier facts
	// attributed to that ref — themselves verbatim excerpts — are the
	// only corpus left.
	var corpus []string
	for _, n := range pipeline.CitationNums(text) {
		src, ok := byRef[n]
		if !ok {
			return false // cites a source that does not exist
		}
		if dc != nil {
			if it, ok := dc.items[src.ItemID]; ok && it.ContentText != "" {
				corpus = append(corpus, it.ContentText)
				continue
			}
		}
		for _, f := range append(append([]pipeline.DossierFact{}, d.Facts...), d.DeepFacts...) {
			if f.Ref == n {
				corpus = append(corpus, f.Text)
			}
		}
	}
	if len(corpus) == 0 {
		corpus = append(corpus, d.Summary)
	}
	if !pipeline.SupportedBySource(text, strings.Join(corpus, " ")) {
		return false
	}
	// model check on top of the floor (real mode); demo misses mean ok.
	// "manual" dossiers (deterministic compose) skip the model entirely —
	// the manual-compose flow must work with zero model calls, and its
	// sentences are editor-written, not model claims to re-verify.
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
	if err == nil && d.Origin != "manual" && !s.d.Cfg.LLMOff() {
		if resp, err := s.d.LLM.Complete(r.Context(), llm.Request{
			Model: s.d.Cfg.ModelTriage, Template: "citecheck", User: user, MaxTokens: 512,
		}); err == nil {
			if json.Unmarshal([]byte(resp.Text), &checks) == nil && len(checks) > 0 {
				return checks[0].Verdict == "ok"
			}
		}
	}
	return true
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
