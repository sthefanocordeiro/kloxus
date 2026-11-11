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
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/editorial"
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

func (s *Server) apiIssueID(r *http.Request) (*store.Issue, error) {
	return s.issue(r)
}

// --- helpers shared by handlers ---

// apiPublication is the publication + settings resource.
type apiPublication struct {
	ID            int64        `json:"id"`
	Slug          string       `json:"slug"`
	Name          string       `json:"name"`
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
		ID: p.ID, Slug: p.Slug, Name: p.Name, Language: p.Language,
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
	if pub, err := db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID)); err == nil {
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
	pub, err := s.apiPub(r)
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
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	typ, rawURL := strings.TrimSpace(in.Type), strings.TrimSpace(in.URL)
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
	id, err := db(r).AddSource(r.Context(), pub.ID, typ, rawURL)
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
	if iss.Status == "sent" {
		apiErr(w, 409, "read_only", "this issue was already sent — sent issues are read-only")
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
	pub, _ := db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID))
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
			DemoMode: s.d.Cfg.Mode == "demo",
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
	if iss.Status == "sent" {
		checks = append(checks, apiCheck{Code: "read_only", OK: false,
			Message: "this issue was already sent — sent issues are read-only"})
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
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
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
	if iss.Status == "sent" {
		apiErr(w, 409, "read_only", "this issue was already sent — sent issues are read-only")
		return
	}
	if pending := pendingDecisions(r, iss.ID); pending > 0 {
		apiErr(w, 409, "blocked", fmt.Sprintf("%d story decision(s) still open — decide each pauta before drafting", pending),
			apiReason{Code: "pending_decisions",
				Message: fmt.Sprintf("%d story decision(s) still open", pending),
				Target:  &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	if apiDBErr(w, r, err, "publication") {
		return
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
		runner := &pipeline.Runner{
			DB: wsp.DB, LLM: s.d.LLM, Fetcher: s.d.Fetcher,
			TriageModel: s.d.Cfg.ModelTriage, DraftModel: s.d.Cfg.ModelDraft,
			MaxDeepDives: s.d.Cfg.MaxDeepDives,
			DemoMode:     s.d.Cfg.Mode == "demo",
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
		res, err := runner.RunPhaseB(context.Background(), pub, iss.ID)
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
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
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
	if iss.Status == "sent" {
		apiErr(w, 409, "read_only", "this issue was already sent — sent issues are read-only")
		return nil, nil, nil, false
	}
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
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
	if iss.Status == "sent" {
		apiErr(w, 409, "read_only", "this issue was already sent — sent issues are read-only")
		return
	}
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
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
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
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
	if iss.Status == "sent" {
		apiErr(w, 409, "read_only", "this issue was already sent — sent issues are read-only")
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
		pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
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
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
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
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Audience string `json:"audience"` // test | readers (safe default: explicit choice required)
		To       string `json:"to"`       // optional test address
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if in.Audience != "test" && in.Audience != "readers" {
		apiErr(w, 400, "validation", "audience is required: test | readers — sending needs an explicit choice")
		return
	}
	if iss.Status != "approved" {
		apiErr(w, 409, "blocked", "the issue must be approved before sending",
			apiReason{Code: "not_approved", Message: "issue is not approved",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
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
	emailID, err := s.sendIssueMail(r, iss, pub, to, kind)
	if apiDBErr(w, r, err, "send") {
		return
	}
	if kind == "issue" {
		// a test send only records the outbox email — the issue stays
		// approved so it can still go to readers
		if apiDBErr(w, r, db(r).SetIssueStatus(r.Context(), iss.ID, "sent"), "issue") {
			return
		}
		if fresh, _ := db(r).GetIssue(r.Context(), iss.ID); fresh != nil {
			iss = fresh
		}
	}
	apiJSON(w, 200, map[string]any{
		"issue": s.issueSummary(r, iss), "outbox_email_id": emailID, "audience": in.Audience,
	})
}

func (s *Server) apiExport(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
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
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
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
	resp := map[string]any{
		"reader":     map[string]string{"email": demoReaderEmail, "name": demoReaderName},
		"subscribed": active,
	}
	if iss, err := db(r).LatestFinishedIssue(r.Context(), pub.ID); err == nil {
		resp["issue"] = s.issueSummary(r, iss)
	}
	apiJSON(w, 200, resp)
}

func (s *Server) apiPaywallSimulate(w http.ResponseWriter, r *http.Request) {
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
	// Demo mode delivers the magic link through the workspace outbox; it
	// carries the workspace id so it opens in any browser.
	link := requestBase(r) + "/pub/" + r.PathValue("id") + "/reader?key=" + token + "&ws=" + reqWS(r).ID
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
	content, version, err := editorial.Rebuild(r.Context(), db(r), pub, s.d.LLM, s.d.Cfg.ModelTriage)
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
