// Package web serves the Kloxus UI: server-rendered pages, review
// actions, run progress, demo workspaces, and the Firecrawl webhook
// receiver.
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/editorial"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

//go:embed templates static
var webFS embed.FS

var tmpl = template.Must(template.ParseFS(webFS, "templates/*.html"))

// Deps wires the server's collaborators (demo or real implementations).
type Deps struct {
	WS      *workspace.Manager
	Cfg     *config.Config
	Fetcher sources.Fetcher
	LLM     llm.Provider
	Monitor sources.MonitorProvider
	// Seed re-creates the example publication inside a database; used for
	// workspace resets and the hosted template.
	Seed func(ctx context.Context, db *store.DB) error
}

// runState is the JSON payload polled by /run/status.
type runState struct {
	RunID      string `json:"run_id"`
	Step       string `json:"step"`
	Done       bool   `json:"done"`
	IssueID    int64  `json:"issue_id"`
	NoNewItems bool   `json:"no_new_items"`
	Err        string `json:"error"`
}

// runProgress wraps runState with a mutex: the pipeline goroutine writes
// it while /run/status reads from other goroutines.
type runProgress struct {
	mu sync.Mutex
	st runState
}

// Server is the UI handler.
type Server struct {
	d   Deps
	mux *http.ServeMux
}

type ctxKey int

const (
	ctxWS ctxKey = iota
	ctxReqID
)

// New builds a Server.
func New(d Deps) *Server {
	s := &Server{d: d, mux: http.NewServeMux()}
	m := s.mux
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(mustSub(webFS, "static"))))
	m.HandleFunc("GET /healthz", s.healthz)
	m.HandleFunc("GET /", s.publications)
	m.HandleFunc("GET /demo/reset", s.demoResetConfirm)
	m.HandleFunc("POST /demo/reset", s.demoReset)
	m.HandleFunc("POST /demo/new", s.demoNew)
	m.HandleFunc("GET /pub/{id}", s.pubOverview)
	m.HandleFunc("GET /pub/{id}/settings", s.pubSettings)
	m.HandleFunc("GET /pub/{id}/sources", s.sources)
	m.HandleFunc("POST /pub/{id}/sources/add", s.sourceAdd)
	m.HandleFunc("POST /pub/{id}/sources/{sid}/watch", s.sourceWatch)
	m.HandleFunc("POST /pub/{id}/sources/{sid}/check", s.sourceCheck)
	m.HandleFunc("GET /pub/{id}/run", s.runPage)
	m.HandleFunc("POST /pub/{id}/run", s.runStart)
	m.HandleFunc("GET /pub/{id}/run/status", s.runStatus)
	m.HandleFunc("GET /issue/{id}", s.issueReview)
	m.HandleFunc("POST /issue/{id}/item/{iid}/decision", s.decision)
	m.HandleFunc("GET /item/{iid}", s.itemDossier)
	m.HandleFunc("POST /issue/{id}/draft", s.draftRun)
	m.HandleFunc("GET /issue/{id}/draft", s.draftPage)
	m.HandleFunc("POST /issue/{id}/commentary", s.commentary)
	m.HandleFunc("POST /issue/{id}/flag/{idx}/remove", s.flagRemove)
	m.HandleFunc("POST /issue/{id}/flag/{idx}/edit", s.flagEdit)
	m.HandleFunc("POST /issue/{id}/approve", s.approve)
	m.HandleFunc("GET /issue/{id}/send", s.sendPage)
	m.HandleFunc("POST /issue/{id}/send", s.send)
	m.HandleFunc("GET /issue/{id}/export", s.export)
	m.HandleFunc("GET /pub/{id}/guide", s.guidePage)
	m.HandleFunc("POST /pub/{id}/guide", s.guideSave)
	m.HandleFunc("POST /pub/{id}/guide/rebuild", s.guideRebuild)
	m.HandleFunc("GET /pub/{id}/costs", s.costsPage)
	m.HandleFunc("GET /pub/{id}/paywall", s.paywallPage)
	m.HandleFunc("POST /pub/{id}/paywall/simulate", s.paywallSimulate)
	m.HandleFunc("POST /pub/{id}/paywall/cancel", s.paywallCancel)
	m.HandleFunc("GET /pub/{id}/reader", s.readerPage)
	m.HandleFunc("GET /pub/{id}/outbox", s.outboxPage)
	m.HandleFunc("GET /outbox/{id}", s.outboxView)
	m.HandleFunc("GET /outbox/{id}/raw", s.outboxRaw)
	m.HandleFunc("GET /outbox/{id}/eml", s.outboxEML)
	m.HandleFunc("GET /a/{id}", s.articlePage)
	m.HandleFunc("POST /webhooks/firecrawl", s.firecrawlWebhook)
	return s
}

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// ServeHTTP resolves the request workspace (cookie in hosted mode), caps
// request bodies, assigns a request id, then dispatches.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rid := newReqID()
	w.Header().Set("X-Request-Id", rid)
	r = r.WithContext(context.WithValue(r.Context(), ctxReqID, rid))

	if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/healthz" {
		s.mux.ServeHTTP(w, r)
		return
	}
	ws, err := s.d.WS.Resolve(w, r)
	if err != nil {
		s.errPage(w, r, http.StatusTooManyRequests, err.Error())
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), ctxWS, ws))
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	}
	s.mux.ServeHTTP(w, r)
}

func newReqID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// reqWS returns the request's workspace (local mode: the fixed one).
func reqWS(r *http.Request) *workspace.Workspace {
	return r.Context().Value(ctxWS).(*workspace.Workspace)
}

// db returns the request's workspace database.
func db(r *http.Request) *store.DB { return reqWS(r).DB }

// requestBase is the absolute origin used for links in exports/emails.
func requestBase(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return proto + "://" + r.Host
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !s.d.WS.Healthy() {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("unhealthy\n"))
		return
	}
	w.Write([]byte("ok\n"))
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, page, data); err != nil {
		slog.Error("render", "page", page, "err", err, "rid", r.Context().Value(ctxReqID))
		http.Error(w, "render error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	base := map[string]any{
		"Data": data, "Body": template.HTML(buf.String()),
		"Workspace": wsLabel(r), "Demo": s.d.Cfg.Mode == "demo",
	}
	if err := tmpl.ExecuteTemplate(w, "layout", base); err != nil {
		slog.Error("layout", "err", err, "rid", r.Context().Value(ctxReqID))
	}
}

func wsLabel(r *http.Request) string {
	if v := r.Context().Value(ctxWS); v != nil {
		if w, ok := v.(*workspace.Workspace); ok && w != nil {
			return w.Label
		}
	}
	return ""
}

// errPage renders a styled error page and logs the cause with the
// request id. Database errors never surface as bare "not found" text.
func (s *Server) errPage(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if code >= 500 {
		slog.Error("request failed", "code", code, "msg", msg,
			"path", r.URL.Path, "rid", r.Context().Value(ctxReqID))
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-error", map[string]any{
		"Code": code, "Message": msg, "ReqID": r.Context().Value(ctxReqID),
	}); err != nil {
		http.Error(w, fmt.Sprintf("%d — %s", code, msg), code)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	tmpl.ExecuteTemplate(w, "layout", map[string]any{
		"Data": map[string]any{}, "Body": template.HTML(buf.String()),
		"Workspace": wsLabel(r), "Demo": s.d.Cfg.Mode == "demo",
	})
}

// notFound renders a styled 404.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request, msg string) {
	s.errPage(w, r, http.StatusNotFound, msg)
}

// dbErr maps store errors: no rows → 404, anything else → logged 500.
func (s *Server) dbErr(w http.ResponseWriter, r *http.Request, err error, what string) {
	if err == sql.ErrNoRows {
		s.notFound(w, r, what+" not found")
		return
	}
	s.errPage(w, r, http.StatusInternalServerError, "internal error")
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (s *Server) pub(r *http.Request) (*store.Publication, error) {
	return db(r).GetPublication(r.Context(), r.PathValue("id"))
}

func (s *Server) issue(r *http.Request) (*store.Issue, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, err
	}
	return db(r).GetIssue(r.Context(), id)
}

func (s *Server) publications(w http.ResponseWriter, r *http.Request) {
	pubs, err := db(r).ListPublications(r.Context())
	if err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	s.render(w, r, "page-publications", map[string]any{"Pubs": pubs, "Demo": s.d.Cfg.Mode == "demo"})
}

func (s *Server) pubOverview(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	issue, _ := db(r).LatestIssue(r.Context(), pub.ID)
	s.render(w, r, "page-overview", map[string]any{
		"Pub": pub, "Issue": issue, "Checklist": s.checklist(r, pub, issue),
	})
}

// checklist builds the first-run guide shown to judges. Each step ticks
// itself from workspace state as the visitor progresses.
func (s *Server) checklist(r *http.Request, pub *store.Publication, iss *store.Issue) []map[string]any {
	ctx := r.Context()
	d := db(r)
	hasIssue := iss != nil
	decisions, _ := d.Decisions(ctx, pub.ID)
	hasDecision := len(decisions) > 0
	dossierSeen := d.Progress(ctx, "dossier_seen") != ""
	commentaryDone := false
	flagsDone := false
	if iss != nil {
		for _, sec := range pub.Sections {
			if sec.Commentary == "required" && strings.TrimSpace(iss.Commentary[sec.Title]) != "" {
				commentaryDone = true
			}
		}
		flagsDone = len(iss.Flags) == 0
	}
	sent := iss != nil && iss.Status == "sent"
	outboxSeen := d.Progress(ctx, "outbox_seen") != ""
	readerFree := d.Progress(ctx, "reader_free") != ""
	readerSub := d.Progress(ctx, "reader_sub") != ""
	guideSeen := d.Progress(ctx, "guide_seen") != "" && func() bool {
		v, _, err := d.LatestGuide(ctx, pub.ID)
		return err == nil && v > 0
	}()
	type step struct {
		Text string
		Done bool
	}
	steps := []step{
		{"Prepare the next issue", hasIssue},
		{"Review the story list: approve, reject with a reason, ask for an alternative", hasDecision},
		{"Open a dossier", dossierSeen},
		{"Fix the flagged sentence and write your commentary", commentaryDone && flagsDone && hasIssue},
		{"Approve, send and open the outbox", sent && outboxSeen},
		{"See the paywall as a non-subscriber, then as a paying reader", readerFree && readerSub},
		{"Read the updated editorial guide", guideSeen},
	}
	out := make([]map[string]any, len(steps))
	for i, st := range steps {
		out[i] = map[string]any{"Text": st.Text, "Done": st.Done}
	}
	return out
}

func (s *Server) pubSettings(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	s.render(w, r, "page-settings", map[string]any{"Pub": pub})
}

type sourceRow struct {
	store.Source
	Canonical       string
	MonitorProvider string
	MonitorID       string
	MonitorStatus   string
}

func (s *Server) sources(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	s.renderSources(w, r, pub, "", "")
}

func (s *Server) renderSources(w http.ResponseWriter, r *http.Request, pub *store.Publication, errMsg, note string) {
	srcs, _ := db(r).Sources(r.Context(), pub.ID)
	var rows []sourceRow
	for _, src := range srcs {
		row := sourceRow{Source: src, Canonical: dedupe.Canonical(src.URL)}
		p, mid, st, _ := db(r).SourceMonitor(r.Context(), src.ID)
		row.MonitorProvider, row.MonitorID, row.MonitorStatus = p, mid, st
		rows = append(rows, row)
	}
	s.render(w, r, "page-sources", map[string]any{
		"Pub": pub, "Sources": rows, "Error": errMsg, "Note": note, "Demo": s.d.Cfg.Mode == "demo",
	})
}

func (s *Server) sourceAdd(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	typ, rawURL := r.FormValue("type"), strings.TrimSpace(r.FormValue("url"))
	if rawURL == "" || (typ != "rss" && typ != "page") {
		s.errPage(w, r, 400, "type rss|page and url required")
		return
	}
	note := ""
	if s.d.Cfg.Mode == "demo" {
		// Demo mode never fetches a visitor-typed URL (SSRF surface on the
		// hosted demo): validate the format, store, simulate.
		if strings.HasPrefix(rawURL, "fixtures/") {
			if _, err := fs.Stat(examples.FS, rawURL); err != nil {
				s.renderSources(w, r, pub, fmt.Sprintf("no fixture at %q — demo sources live under fixtures/...", rawURL), "")
				return
			}
		} else {
			u, err := url.Parse(rawURL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				s.renderSources(w, r, pub, fmt.Sprintf("invalid source URL %q", rawURL), "")
				return
			}
			note = "Simulated in demo mode: sources are not fetched on the hosted demo."
		}
	} else {
		u, err := url.Parse(rawURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			s.renderSources(w, r, pub, fmt.Sprintf("invalid source URL %q — real-mode sources must be absolute http(s) URLs", rawURL), "")
			return
		}
	}
	if _, err := db(r).AddSource(r.Context(), pub.ID, typ, rawURL); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	if note != "" {
		s.renderSources(w, r, pub, "", note)
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/sources")
}

func (s *Server) sourceWatch(w http.ResponseWriter, r *http.Request) {
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	url, err := s.sourceURL(r, sid)
	if err != nil {
		s.dbErr(w, r, err, "source")
		return
	}
	mid, err := s.d.Monitor.Watch(r.Context(), url)
	if err != nil {
		s.errPage(w, r, 502, "monitor: "+err.Error())
		return
	}
	if err := db(r).SetSourceMonitor(r.Context(), sid, s.d.Monitor.Name(), mid, "monitoring"); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/sources")
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
		`SELECT url FROM sources WHERE id = ? AND publication_id = ?`, sid, pub.ID).Scan(&url)
	return url, err
}

func (s *Server) sourceCheck(w http.ResponseWriter, r *http.Request) {
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	if _, err := s.sourceURL(r, sid); err != nil {
		s.dbErr(w, r, err, "source")
		return
	}
	_, mid, _, _ := db(r).SourceMonitor(r.Context(), sid)
	res, err := s.d.Monitor.Check(r.Context(), mid)
	if err != nil {
		s.errPage(w, r, 502, "check: "+err.Error())
		return
	}
	status := "monitoring"
	var details []string
	for _, rr := range res {
		status = rr.Status
		details = append(details, rr.Detail)
	}
	db(r).SetSourceMonitor(r.Context(), sid, s.d.Monitor.Name(), mid, status+": "+strings.Join(details, "; "))
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/sources")
}

// --- run ---

func (s *Server) runPage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	s.render(w, r, "page-run", map[string]any{"Pub": pub, "Steps": pipeline.StepsA})
}

func (s *Server) runStart(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	wsp := reqWS(r)
	st := &runProgress{st: runState{RunID: newReqID(), Step: "starting"}}
	if prev, running := wsp.Runs.LoadOrStore(pub.ID, st); running {
		// One pipeline run at a time per workspace: an in-flight run wins;
		// a finished run is replaced by this new one.
		pst := prev.(*runProgress)
		pst.mu.Lock()
		done := pst.st.Done
		pst.mu.Unlock()
		if !done {
			s.redirect(w, r, "/pub/"+r.PathValue("id")+"/run")
			return
		}
		wsp.Runs.Store(pub.ID, st)
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
		}
		res, err := runner.RunPhaseA(context.Background(), pub)
		st.mu.Lock()
		defer st.mu.Unlock()
		st.st.Done = true
		if err != nil {
			st.st.Err = err.Error()
			slog.Error("pipeline run failed", "pub", pub.ID, "err", err, "rid", st.st.RunID)
			return
		}
		st.st.NoNewItems = res.NoNewItems
		st.st.IssueID = res.IssueID
	}()
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/run")
}

func (s *Server) runStatus(w http.ResponseWriter, r *http.Request) {
	pid, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	w.Header().Set("Content-Type", "application/json")
	v, ok := reqWS(r).Runs.Load(pid)
	if !ok {
		json.NewEncoder(w).Encode(runState{Done: true})
		return
	}
	st := v.(*runProgress)
	st.mu.Lock()
	snap := st.st
	st.mu.Unlock()
	json.NewEncoder(w).Encode(snap)
}

// --- issue review ---

// reviewCard is the per-item view model for the story list and its
// in-place update fragment.
type reviewCard struct {
	store.IssueItem
	Title      string
	Outlets    string
	RejectWhy  string
	AltMissing bool
}

func (s *Server) cardFor(r *http.Request, ii store.IssueItem) reviewCard {
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
	card := reviewCard{IssueItem: ii, Title: title, Outlets: strings.Join(names, ", ")}
	if ii.Status == "rejected" {
		_, reason, err := db(r).LatestDecision(ctx, ii.ID)
		if err == nil {
			card.RejectWhy = reason
		}
	}
	return card
}

func (s *Server) issueReview(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	items, _ := db(r).IssueItems(r.Context(), iss.ID)
	var rows []reviewCard
	pending := 0
	for _, ii := range items {
		if ii.Status == "pending" {
			pending++
		}
		rows = append(rows, s.cardFor(r, ii))
	}
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	s.render(w, r, "page-review", map[string]any{
		"Pub": pub, "Issue": iss, "Items": rows, "Pending": pending,
	})
}

func (s *Server) decision(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	iid, _ := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	kind, reason := r.FormValue("kind"), strings.TrimSpace(r.FormValue("reason"))
	status := map[string]string{"approve": "approved", "reject": "rejected"}[kind]
	inPlace := r.Header.Get("X-Requested-With") == "fetch"

	finish := func(card reviewCard) {
		if inPlace {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			tmpl.ExecuteTemplate(w, "partial-card", card)
			return
		}
		s.redirect(w, r, "/issue/"+r.PathValue("id"))
	}

	ii, err := db(r).GetIssueItem(r.Context(), iid)
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}

	switch kind {
	case "approve":
		if err := db(r).SetIssueItemStatus(r.Context(), iid, status); err != nil {
			s.errPage(w, r, 500, "internal error")
			return
		}
		db(r).RecordDecision(r.Context(), iss.PublicationID, iid, kind, reason)
	case "reject":
		if reason == "" {
			card := s.cardFor(r, *ii)
			card.RejectWhy = "required"
			if inPlace {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				tmpl.ExecuteTemplate(w, "partial-card", card)
				return
			}
			s.redirect(w, r, "/issue/"+r.PathValue("id"))
			return
		}
		if err := db(r).SetIssueItemStatus(r.Context(), iid, status); err != nil {
			s.errPage(w, r, 500, "internal error")
			return
		}
		db(r).RecordDecision(r.Context(), iss.PublicationID, iid, kind, reason)
		pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
		if pub != nil {
			editorial.Rebuild(r.Context(), db(r), pub, s.d.LLM, s.d.Cfg.ModelTriage)
		}
	case "alternative":
		pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
		runner := &pipeline.Runner{
			DB: db(r), LLM: s.d.LLM, Fetcher: s.d.Fetcher,
			TriageModel: s.d.Cfg.ModelTriage, DraftModel: s.d.Cfg.ModelDraft,
			DemoMode: s.d.Cfg.Mode == "demo",
		}
		newII, err := runner.SubstituteAlternative(r.Context(), pub, iss, ii)
		if err == sql.ErrNoRows {
			card := s.cardFor(r, *ii)
			card.AltMissing = true
			finish(card)
			return
		}
		if err != nil {
			s.errPage(w, r, 500, "internal error")
			return
		}
		db(r).RecordDecision(r.Context(), iss.PublicationID, iid, "alternative",
			fmt.Sprintf("replaced by item %d (score %.1f)", newII.ID, newII.Score))
		ii = newII
	default:
		s.errPage(w, r, 400, "kind must be approve|reject|alternative")
		return
	}
	if iss.Status == "approved" {
		db(r).SetIssueStatus(r.Context(), iss.ID, "drafted")
	}
	card := s.cardFor(r, *ii)
	finish(card)
}

func (s *Server) itemDossier(w http.ResponseWriter, r *http.Request) {
	iid, _ := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	ii, err := db(r).GetIssueItem(r.Context(), iid)
	if err != nil {
		s.dbErr(w, r, err, "item")
		return
	}
	djson, err := db(r).Dossier(r.Context(), iid)
	if err != nil {
		s.notFound(w, r, "no dossier for this item")
		return
	}
	var d pipeline.Dossier
	json.Unmarshal([]byte(djson), &d)
	title, _ := db(r).StoryTitle(r.Context(), ii.StoryID)
	iss, _ := db(r).GetIssue(r.Context(), ii.IssueID)
	var pub *store.Publication
	if iss != nil {
		pub, _ = db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	}
	db(r).SetProgress(r.Context(), "dossier_seen")
	s.render(w, r, "page-dossier", map[string]any{
		"Pub": pub, "Issue": iss, "Item": ii, "Title": title, "Dossier": d,
	})
}

// --- draft / commentary / flags ---

func (s *Server) draftRun(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	items, _ := db(r).IssueItems(r.Context(), iss.ID)
	pending := 0
	for _, ii := range items {
		if ii.Status == "pending" {
			pending++
		}
	}
	if pending > 0 {
		s.redirect(w, r, "/issue/"+r.PathValue("id")+"?pending="+strconv.Itoa(pending))
		return
	}
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	runner := &pipeline.Runner{
		DB: db(r), LLM: s.d.LLM, Fetcher: s.d.Fetcher,
		TriageModel: s.d.Cfg.ModelTriage, DraftModel: s.d.Cfg.ModelDraft,
		MaxDeepDives: s.d.Cfg.MaxDeepDives,
		DemoMode:     s.d.Cfg.Mode == "demo",
	}
	wsp := reqWS(r)
	wsp.Activity.RLock()
	_, err = runner.RunPhaseB(r.Context(), pub, iss.ID)
	wsp.Activity.RUnlock()
	if err != nil {
		s.errPage(w, r, 500, "draft failed — the run page has details; request "+reqIDOf(r))
		return
	}
	s.redirect(w, r, "/issue/"+r.PathValue("id")+"/draft")
}

func reqIDOf(r *http.Request) string {
	if v, ok := r.Context().Value(ctxReqID).(string); ok {
		return v
	}
	return ""
}

// blockers returns the reasons an issue cannot be approved/sent.
func (s *Server) blockers(r *http.Request, iss *store.Issue, pub *store.Publication) []string {
	var out []string
	for _, sec := range pub.Sections {
		if sec.Commentary == "required" && strings.TrimSpace(iss.Commentary[sec.Title]) == "" {
			out = append(out, fmt.Sprintf("commentary required in %q", sec.Title))
		}
	}
	if len(iss.Flags) > 0 {
		out = append(out, fmt.Sprintf("%d flagged sentence(s) unresolved — remove or edit each", len(iss.Flags)))
	}
	return out
}

func (s *Server) draftPage(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	s.renderDraft(w, r, iss, pub, http.StatusOK, "")
}

func (s *Server) renderDraft(w http.ResponseWriter, r *http.Request, iss *store.Issue, pub *store.Publication, status int, note string) {
	if pub == nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	s.render(w, r, "page-draft", map[string]any{
		"Pub": pub, "Issue": iss, "BodyHTML": template.HTML(delivery.RenderHTML(iss.Subject, delivery.ReaderBody(iss, pub, ""), "")),
		"Sections": pub.Sections, "Blockers": s.blockers(r, iss, pub), "Note": note,
	})
}

func (s *Server) commentary(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	c := iss.Commentary
	if c == nil {
		c = map[string]string{}
	}
	c[r.FormValue("section")] = r.FormValue("text")
	if err := db(r).SaveCommentary(r.Context(), iss.ID, c); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	if iss.Status == "approved" {
		// Edits after approval revert to drafted and require re-approval.
		db(r).SetIssueStatus(r.Context(), iss.ID, "drafted")
	}
	s.redirect(w, r, "/issue/"+r.PathValue("id")+"/draft")
}

// resolveFlag drops flag idx from the issue and records the resolution.
func (s *Server) resolveFlag(r *http.Request, iss *store.Issue, idx int, kind, detail string) error {
	flags := append([]store.Flag{}, iss.Flags...)
	if idx < 0 || idx >= len(flags) {
		return fmt.Errorf("flag %d out of range", idx)
	}
	flags = append(flags[:idx], flags[idx+1:]...)
	if err := db(r).ResolveFlags(r.Context(), iss.ID, flags); err != nil {
		return err
	}
	db(r).RecordDecision(r.Context(), iss.PublicationID, 0, kind, detail)
	if iss.Status == "approved" {
		db(r).SetIssueStatus(r.Context(), iss.ID, "drafted")
	}
	return nil
}

// flagRemove deletes the flagged sentence from the stored body.
func (s *Server) flagRemove(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	idx, _ := strconv.Atoi(r.PathValue("idx"))
	if idx < 0 || idx >= len(iss.Flags) {
		s.notFound(w, r, "flag")
		return
	}
	sent := iss.Flags[idx].Sentence
	body := removeSentence(iss.BodyMD, sent)
	if err := db(r).SetIssueBody(r.Context(), iss.ID, body); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	if err := s.resolveFlag(r, iss, idx, "flag_remove", sent); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	s.redirect(w, r, "/issue/"+r.PathValue("id")+"/draft")
}

// flagEdit replaces the flagged sentence after a deterministic recheck:
// the new text must cite an existing source [n] of that item, or be
// marked as interpretation (stored as a blockquote so the citation check
// and readers both treat it as commentary).
func (s *Server) flagEdit(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	idx, _ := strconv.Atoi(r.PathValue("idx"))
	if idx < 0 || idx >= len(iss.Flags) {
		s.notFound(w, r, "flag")
		return
	}
	flag := iss.Flags[idx]
	text := strings.TrimSpace(r.FormValue("text"))
	mode := r.FormValue("mode")
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	if text == "" {
		s.renderDraft(w, r, iss, pub, http.StatusBadRequest, "edited sentence cannot be empty")
		return
	}
	var replacement string
	if mode == "interpretation" {
		replacement = "\n> _Interpretação: " + text + "_\n"
	} else {
		maxRef := s.maxSourceRef(r, iss, flag.Item)
		var bad string
		for _, m := range refRe.FindAllString(text, -1) {
			n, _ := strconv.Atoi(strings.Trim(m, "[]"))
			if n < 1 || (maxRef > 0 && n > maxRef) {
				bad = m
				break
			}
		}
		if bad != "" {
			s.renderDraft(w, r, iss, pub, http.StatusBadRequest,
				fmt.Sprintf("source %s does not exist for this item — cite [1]..[%d] or mark as interpretation", bad, maxRef))
			return
		}
		if !refRe.MatchString(text) {
			s.renderDraft(w, r, iss, pub, http.StatusBadRequest,
				"edited sentence needs a citation [n] — or mark it as interpretation")
			return
		}
		replacement = text
	}
	body := strings.Replace(iss.BodyMD, flag.Sentence, replacement, 1)
	if body == iss.BodyMD {
		s.notFound(w, r, "flagged sentence no longer in the body")
		return
	}
	if err := db(r).SetIssueBody(r.Context(), iss.ID, body); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	if err := s.resolveFlag(r, iss, idx, "flag_edit", flag.Sentence+" → "+text); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	s.redirect(w, r, "/issue/"+r.PathValue("id")+"/draft")
}

var refRe = regexp.MustCompile(`\[\d+\]`)

// maxSourceRef returns the number of sources on the dossier of the item
// that produced the flagged sentence (0 when unknown → any ref allowed).
func (s *Server) maxSourceRef(r *http.Request, iss *store.Issue, itemRef int64) int {
	items, err := db(r).IssueItems(r.Context(), iss.ID)
	if err != nil {
		return 0
	}
	for _, ii := range items {
		dj, err := db(r).Dossier(r.Context(), ii.ID)
		if err != nil {
			continue
		}
		var d pipeline.Dossier
		if json.Unmarshal([]byte(dj), &d) != nil || int64(d.Ref) != itemRef {
			continue
		}
		return len(d.Sources)
	}
	return 0
}

// removeSentence deletes a sentence from the body and tidies up the
// whitespace it leaves behind.
func removeSentence(body, sent string) string {
	out := strings.Replace(body, sent, "", 1)
	out = strings.ReplaceAll(out, "  ", " ")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return out
}

// --- approve / send / export ---

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	if b := s.blockers(r, iss, pub); len(b) > 0 {
		s.renderDraft(w, r, iss, pub, http.StatusConflict, "")
		return
	}
	if err := db(r).SetIssueStatus(r.Context(), iss.ID, "approved"); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	db(r).RecordDecision(r.Context(), iss.PublicationID, 0, "approve", "issue approved")
	s.redirect(w, r, "/issue/"+r.PathValue("id")+"/send")
}

func (s *Server) sendPage(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	s.render(w, r, "page-send", map[string]any{"Pub": pub, "Issue": iss})
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	if iss.Status != "approved" {
		s.renderDraft(w, r, iss, pub, http.StatusConflict, "")
		return
	}
	to := []string{"readers@kloxus.demo"}
	kind := "issue"
	if t := strings.TrimSpace(r.FormValue("to")); t != "" {
		to = []string{t}
		kind = "test"
	}
	md := delivery.ReaderBody(iss, pub, requestBase(r))
	mailer := delivery.Outbox{DB: db(r), Kind: kind, Ctx: r.Context()}
	id, err := mailer.Send(iss.Subject, md, delivery.RenderHTML(iss.Subject, md, requestBase(r)), to)
	if err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	if err := db(r).SetIssueStatus(r.Context(), iss.ID, "sent"); err != nil {
		s.errPage(w, r, 500, "issue sent but status update failed")
		return
	}
	s.render(w, r, "page-sent", map[string]any{"Pub": pub, "Issue": iss, "EmailID": id})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		s.dbErr(w, r, err, "issue")
		return
	}
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	if iss.Status != "approved" && iss.Status != "sent" {
		// Exports ship only approved output: show the draft with blockers.
		s.renderDraft(w, r, iss, pub, http.StatusConflict, "")
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

// --- guide / costs ---

func (s *Server) guidePage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	guides, _ := db(r).LatestGuides(r.Context(), pub.ID, 2)
	data := map[string]any{"Pub": pub}
	if len(guides) > 0 {
		data["Version"] = guides[0].Version
		data["Content"] = guides[0].Content
		if len(guides) > 1 {
			data["Added"] = addedLines(guides[1].Content, guides[0].Content)
		}
		db(r).SetProgress(r.Context(), "guide_seen")
	}
	s.render(w, r, "page-guide", data)
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

func (s *Server) guideSave(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	if _, err := db(r).SaveGuide(r.Context(), pub.ID, r.FormValue("content")); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/guide")
}

func (s *Server) guideRebuild(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	if _, _, err := editorial.Rebuild(r.Context(), db(r), pub, s.d.LLM, s.d.Cfg.ModelTriage); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/guide")
}

func (s *Server) costsPage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	rows, _ := db(r).CostReport(r.Context(), pub.ID, 4)
	type group struct {
		Issue int
		Rows  []store.CostRow
		Total float64
	}
	var groups []*group
	for _, row := range rows {
		if len(groups) == 0 || groups[len(groups)-1].Issue != row.IssueNumber {
			groups = append(groups, &group{Issue: row.IssueNumber})
		}
		g := groups[len(groups)-1]
		g.Rows = append(g.Rows, row)
		g.Total += row.CostUSD
	}
	s.render(w, r, "page-costs", map[string]any{"Pub": pub, "Groups": groups})
}

// --- paywall / reader ---

const demoReaderEmail = "demo@reader.test"
const demoReaderName = "Demo Reader"

func (s *Server) paywallPage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	rid, _ := db(r).UpsertReader(r.Context(), pub.ID, demoReaderEmail, demoReaderName)
	active, _ := db(r).HasActiveSubscription(r.Context(), rid)
	s.render(w, r, "page-paywall", map[string]any{
		"Pub": pub, "Active": active, "Reader": demoReaderName,
	})
}

func (s *Server) paywallSimulate(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	ctx := r.Context()
	rid, _ := db(r).UpsertReader(ctx, pub.ID, demoReaderEmail, demoReaderName)
	tokenBytes := make([]byte, 12)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)
	if err := db(r).ActivateSubscription(ctx, rid, "fake", "sim-"+strconv.FormatInt(rid, 10), token); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	// Demo mode delivers the magic link through the workspace outbox.
	link := requestBase(r) + "/pub/" + r.PathValue("id") + "/reader?key=" + token
	htmlBody := fmt.Sprintf(
		`<!DOCTYPE html><html><body style="font-family:system-ui,sans-serif;max-width:38em;margin:2em auto">`+
			`<h2>%s — acesso de assinante</h2>`+
			`<p>Olá, %s! Clique para abrir sua edição como assinante:</p>`+
			`<p><a href="%s">Abrir %s (magic link)</a></p>`+
			`<p style="color:#777;font-size:.85em">Simulado no modo demo — nenhum e-mail real foi enviado.</p></body></html>`,
		pub.Name, demoReaderName, link, pub.Name)
	db(r).SaveOutboxEmail(ctx, "magic_link", demoReaderEmail,
		pub.Name+" — seu link de acesso", "", htmlBody)
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/paywall")
}

func (s *Server) paywallCancel(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	rid, _ := db(r).UpsertReader(r.Context(), pub.ID, demoReaderEmail, demoReaderName)
	if err := db(r).CancelSubscription(r.Context(), rid); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/paywall")
}

// readerPage shows the issue as a reader sees it: paid sections become a
// teaser unless the demo reader is subscribed (or opens a magic link).
func (s *Server) readerPage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	iss, err := db(r).LatestFinishedIssue(r.Context(), pub.ID)
	if err != nil {
		s.notFound(w, r, "no published issue yet")
		return
	}
	rid, _ := db(r).UpsertReader(r.Context(), pub.ID, demoReaderEmail, demoReaderName)
	active, _ := db(r).HasActiveSubscription(r.Context(), rid)
	if key := r.URL.Query().Get("key"); key != "" {
		if mrid, err := db(r).ReaderByMagicToken(r.Context(), pub.ID, key); err == nil && mrid == rid {
			active = true
		}
	}
	forceFree := r.URL.Query().Get("view") == "free"
	unlocked := active && !forceFree
	if forceFree {
		db(r).SetProgress(r.Context(), "reader_free")
	} else if unlocked {
		db(r).SetProgress(r.Context(), "reader_sub")
	}
	md := delivery.ReaderBody(iss, pub, requestBase(r))
	if !unlocked {
		md = gateSections(md, pub.Paywall.PaidSections)
	}
	s.render(w, r, "page-reader", map[string]any{
		"Pub": pub, "Issue": iss, "Active": unlocked, "Subscribed": active, "Reader": demoReaderName,
		"BodyHTML": template.HTML(delivery.RenderHTML(iss.Subject, md, requestBase(r))),
	})
}

// gateSections reduces paid sections of the issue to a teaser: the
// headline and first sentence of each item plus a subscribers-only note.
func gateSections(body string, paid []string) string {
	paidSet := map[string]bool{}
	for _, p := range paid {
		paidSet[p] = true
	}
	var out []string
	gated := false
	skipRest := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			gated = paidSet[strings.TrimSpace(strings.TrimPrefix(t, "## "))]
			skipRest = false
			out = append(out, line)
			continue
		}
		if !gated {
			out = append(out, line)
			continue
		}
		if strings.HasPrefix(t, "### ") {
			out = append(out, line)
			skipRest = false
			continue
		}
		if skipRest || t == "" {
			continue
		}
		// first content line of an item inside a paid section: teaser
		first := firstSentence(t)
		out = append(out, first)
		out = append(out, "", "> **Subscribers only — subscribe to keep reading.**", "")
		skipRest = true
	}
	return strings.Join(out, "\n")
}

// firstSentence returns text up to the first sentence end (or the whole
// line when shorter than ~30 words).
func firstSentence(t string) string {
	if i := strings.Index(t, ". "); i >= 0 {
		return t[:i+1]
	}
	return t
}

// --- outbox ---

func (s *Server) outboxPage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		s.dbErr(w, r, err, "publication")
		return
	}
	emails, _ := db(r).OutboxEmails(r.Context())
	db(r).SetProgress(r.Context(), "outbox_seen")
	s.render(w, r, "page-outbox", map[string]any{"Pub": pub, "Emails": emails})
}

func (s *Server) outboxEmail(r *http.Request) (*store.OutboxEmail, error) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return db(r).GetOutboxEmail(r.Context(), id)
}

func (s *Server) outboxView(w http.ResponseWriter, r *http.Request) {
	e, err := s.outboxEmail(r)
	if err != nil {
		s.dbErr(w, r, err, "email")
		return
	}
	var pubID int64
	db(r).QueryRowContext(r.Context(), `SELECT id FROM publications ORDER BY id LIMIT 1`).Scan(&pubID)
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(pubID))
	s.render(w, r, "page-outbox-view", map[string]any{"Pub": pub, "Email": e})
}

func (s *Server) outboxRaw(w http.ResponseWriter, r *http.Request) {
	e, err := s.outboxEmail(r)
	if err != nil {
		s.dbErr(w, r, err, "email")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(e.BodyHTML))
}

func (s *Server) outboxEML(w http.ResponseWriter, r *http.Request) {
	e, err := s.outboxEmail(r)
	if err != nil {
		s.dbErr(w, r, err, "email")
		return
	}
	w.Header().Set("Content-Type", "message/rfc822")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=email-%d.eml", e.ID))
	w.Write([]byte(delivery.EML(e)))
}

// --- fixture article ---

// articlePage serves the fictional fixture article behind every source
// link, so "every fact has a working source reference" is visibly true.
func (s *Server) articlePage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	it, err := db(r).GetItem(r.Context(), id)
	if err != nil {
		s.dbErr(w, r, err, "article")
		return
	}
	s.render(w, r, "page-article", map[string]any{
		"Item": it, "Outlet": it.SourceName, "Canonical": dedupe.Canonical(it.URL),
	})
}

// --- demo reset / webhook ---

func (s *Server) demoResetConfirm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "page-reset", map[string]any{})
}

// demoReset wipes and reseeds only this visitor's workspace.
func (s *Server) demoReset(w http.ResponseWriter, r *http.Request) {
	if err := s.d.WS.Reset(r.Context(), reqWS(r)); err != nil {
		s.errPage(w, r, 500, "reset failed")
		return
	}
	s.redirect(w, r, "/")
}

// demoNew abandons the current workspace and starts a fresh one.
func (s *Server) demoNew(w http.ResponseWriter, r *http.Request) {
	if _, err := s.d.WS.New(w, r); err != nil {
		s.errPage(w, r, 429, err.Error())
		return
	}
	s.redirect(w, r, "/")
}

// firecrawlWebhook receives monitor.page events (real mode).
type fcEvent struct {
	Type string `json:"type"`
	Data struct {
		MonitorID    string `json:"monitor_id"`
		MonitorIDAlt string `json:"monitorId"`
		URL          string `json:"url"`
		Status       string `json:"status"`
		IsMeaningful bool   `json:"isMeaningful"`
		Markdown     string `json:"markdown"`
		Title        string `json:"title"`
	} `json:"data"`
}

func (s *Server) firecrawlWebhook(w http.ResponseWriter, r *http.Request) {
	if s.d.Cfg.Mode == "real" && s.d.Cfg.WebhookSecret == "" {
		s.errPage(w, r, 503, "webhook secret not configured")
		return
	}
	if s.d.Cfg.WebhookSecret != "" {
		got := r.Header.Get("Authorization")
		want := "Bearer " + s.d.Cfg.WebhookSecret
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			s.errPage(w, r, 401, "unauthorized")
			return
		}
	}
	var ev fcEvent
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&ev); err != nil {
		s.errPage(w, r, 400, "bad payload")
		return
	}
	if ev.Type != "monitor.page" || (ev.Data.Status != "changed" && ev.Data.Status != "new") {
		w.WriteHeader(204)
		return
	}
	mid := ev.Data.MonitorID
	if mid == "" {
		mid = ev.Data.MonitorIDAlt
	}
	src, err := db(r).FindSourceByMonitor(r.Context(), mid)
	if err != nil {
		slog.Warn("webhook: unknown monitor", "id", mid)
		w.WriteHeader(202)
		return
	}
	title := ev.Data.Title
	if title == "" {
		title = ev.Data.URL
	}
	if _, err := db(r).SaveItems(r.Context(), src.ID, []sources.RawItem{{
		ExternalID:  ev.Data.URL + "#" + ev.Data.Status,
		URL:         ev.Data.URL,
		Title:       title,
		ContentText: ev.Data.Markdown,
	}}, dedupe.Canonical); err != nil {
		s.errPage(w, r, 500, "internal error")
		return
	}
	slog.Info("firecrawl monitor.page ingested", "status", ev.Data.Status)
	w.WriteHeader(204)
}
