package web

// The JSON API under /api/v1 (milestone 1 of the v2 contract — see
// docs/api.md). It exposes the same behaviour as the HTML routes; the
// rules do not change, only the format of the answers. Errors carry a
// stable code, a human message and, for blocked actions, the list of
// reasons with a pointer to what must be fixed.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
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
