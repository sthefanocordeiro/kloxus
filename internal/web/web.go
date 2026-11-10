// Package web serves the Kloxus UI: server-rendered pages, review
// actions, run progress, demo reset, and the Firecrawl webhook receiver.
package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/editorial"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

//go:embed templates static
var webFS embed.FS

var tmpl = template.Must(template.ParseFS(webFS, "templates/*.html"))

// Deps wires the server's collaborators (demo or real implementations).
type Deps struct {
	DB      *store.DB
	Cfg     *config.Config
	Fetcher sources.Fetcher
	LLM     llm.Provider
	Monitor sources.MonitorProvider
	Mailer  delivery.Mailer
	Reseed  func(ctx context.Context) error // demo reset
}

type runState struct {
	Step    string `json:"step"`
	Done    bool   `json:"done"`
	IssueID int64  `json:"issue_id"`
	Err     string `json:"error"`
}

// Server is the UI handler.
type Server struct {
	d    Deps
	mux  *http.ServeMux
	runs sync.Map // pubID(int64) -> *runState
}

// New builds a Server.
func New(d Deps) *Server {
	s := &Server{d: d, mux: http.NewServeMux()}
	m := s.mux
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(mustSub(webFS, "static"))))
	m.HandleFunc("GET /", s.publications)
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
	m.HandleFunc("POST /issue/{id}/flag/{idx}/dismiss", s.flagDismiss)
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
	m.HandleFunc("GET /pub/{id}/reader", s.readerPage)
	m.HandleFunc("POST /demo/reset", s.demoReset)
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

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, page, data); err != nil {
		slog.Error("render", "page", page, "err", err)
		http.Error(w, "render error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", map[string]any{
		"Data": data, "Body": template.HTML(buf.String()),
	}); err != nil {
		slog.Error("layout", "err", err)
	}
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (s *Server) pub(r *http.Request) (*store.Publication, error) {
	return s.d.DB.GetPublication(r.Context(), r.PathValue("id"))
}

func (s *Server) issue(r *http.Request) (*store.Issue, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, err
	}
	return s.d.DB.GetIssue(r.Context(), id)
}

func (s *Server) publications(w http.ResponseWriter, r *http.Request) {
	pubs, err := s.d.DB.ListPublications(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "page-publications", map[string]any{"Pubs": pubs, "Demo": s.d.Cfg.Mode == "demo"})
}

func (s *Server) pubOverview(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "publication not found", 404)
		return
	}
	issue, _ := s.d.DB.LatestIssue(r.Context(), pub.ID)
	s.render(w, "page-overview", map[string]any{"Pub": pub, "Issue": issue})
}

func (s *Server) pubSettings(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	s.render(w, "page-settings", map[string]any{"Pub": pub})
}

type sourceRow struct {
	store.Source
	MonitorProvider string
	MonitorID       string
	MonitorStatus   string
}

func (s *Server) sources(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	srcs, _ := s.d.DB.Sources(r.Context(), pub.ID)
	var rows []sourceRow
	for _, src := range srcs {
		row := sourceRow{Source: src}
		p, mid, st, _ := s.d.DB.SourceMonitor(r.Context(), src.ID)
		row.MonitorProvider, row.MonitorID, row.MonitorStatus = p, mid, st
		rows = append(rows, row)
	}
	s.render(w, "page-sources", map[string]any{"Pub": pub, "Sources": rows})
}

func (s *Server) sourceAdd(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	typ, url := r.FormValue("type"), strings.TrimSpace(r.FormValue("url"))
	if url == "" || (typ != "rss" && typ != "page") {
		http.Error(w, "type rss|page and url required", 400)
		return
	}
	if _, err := s.d.DB.AddSource(r.Context(), pub.ID, typ, url); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/sources")
}

func (s *Server) sourceWatch(w http.ResponseWriter, r *http.Request) {
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	mid, err := s.d.Monitor.Watch(r.Context(), sourceURL(r, s.d.DB, sid))
	if err != nil {
		http.Error(w, "monitor: "+err.Error(), 502)
		return
	}
	if err := s.d.DB.SetSourceMonitor(r.Context(), sid, s.d.Monitor.Name(), mid, "monitoring"); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/sources")
}

func sourceURL(r *http.Request, db *store.DB, sid int64) string {
	var url string
	db.QueryRowContext(r.Context(), `SELECT url FROM sources WHERE id = ?`, sid).Scan(&url)
	return url
}

func (s *Server) sourceCheck(w http.ResponseWriter, r *http.Request) {
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	_, mid, _, _ := s.d.DB.SourceMonitor(r.Context(), sid)
	res, err := s.d.Monitor.Check(r.Context(), mid)
	if err != nil {
		http.Error(w, "check: "+err.Error(), 502)
		return
	}
	status := "monitoring"
	var details []string
	for _, rr := range res {
		status = rr.Status
		details = append(details, rr.Detail)
	}
	s.d.DB.SetSourceMonitor(r.Context(), sid, s.d.Monitor.Name(), mid, status+": "+strings.Join(details, "; "))
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/sources")
}

// --- run ---

func (s *Server) runPage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	s.render(w, "page-run", map[string]any{"Pub": pub, "Steps": pipeline.Steps})
}

func (s *Server) runStart(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if _, running := s.runs.LoadOrStore(pub.ID, &runState{Step: "starting"}); running {
		s.redirect(w, r, "/pub/"+r.PathValue("id")+"/run")
		return
	}
	st := &runState{Step: "starting"}
	s.runs.Store(pub.ID, st)
	go func() {
		runner := &pipeline.Runner{
			DB: s.d.DB, LLM: s.d.LLM, Fetcher: s.d.Fetcher,
			TriageModel: s.d.Cfg.ModelTriage, DraftModel: s.d.Cfg.ModelDraft,
			MaxDeepDives: s.d.Cfg.MaxDeepDives,
			OnStep:       func(step string) { st.Step = step },
		}
		res, err := runner.RunPhaseA(context.Background(), pub)
		if err != nil {
			st.Err = err.Error()
			st.Done = true
			return
		}
		st.IssueID = res.IssueID
		st.Done = true
	}()
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/run")
}

func (s *Server) runStatus(w http.ResponseWriter, r *http.Request) {
	pid, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	w.Header().Set("Content-Type", "application/json")
	v, ok := s.runs.Load(pid)
	if !ok {
		json.NewEncoder(w).Encode(runState{Done: true})
		return
	}
	json.NewEncoder(w).Encode(v.(*runState))
}

// --- issue review ---

func (s *Server) issueReview(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "issue not found", 404)
		return
	}
	items, _ := s.d.DB.IssueItems(r.Context(), iss.ID)
	type row struct {
		store.IssueItem
		Title   string
		Outlets int
	}
	var rows []row
	for _, ii := range items {
		title, _ := s.d.DB.StoryTitle(r.Context(), ii.StoryID)
		var n int
		s.d.DB.QueryRowContext(r.Context(),
			`SELECT count(*) FROM story_items WHERE story_id = ?`, ii.StoryID).Scan(&n)
		rows = append(rows, row{ii, title, n})
	}
	pub, _ := s.d.DB.GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	s.render(w, "page-review", map[string]any{
		"Pub": pub, "Issue": iss, "Items": rows,
	})
}

func (s *Server) decision(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	iid, _ := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	kind, reason := r.FormValue("kind"), r.FormValue("reason")
	status := map[string]string{"approve": "approved", "reject": "rejected", "alternative": "alternative"}[kind]
	if status == "" {
		http.Error(w, "kind must be approve|reject|alternative", 400)
		return
	}
	if err := s.d.DB.SetIssueItemStatus(r.Context(), iid, status); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.d.DB.RecordDecision(r.Context(), iss.PublicationID, iid, kind, reason)
	if kind == "reject" && reason != "" {
		pub, _ := s.d.DB.GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
		if pub != nil {
			editorial.Rebuild(r.Context(), s.d.DB, pub, s.d.LLM, s.d.Cfg.ModelTriage)
		}
	}
	s.redirect(w, r, "/issue/"+r.PathValue("id"))
}

func (s *Server) itemDossier(w http.ResponseWriter, r *http.Request) {
	iid, _ := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	ii, err := s.d.DB.GetIssueItem(r.Context(), iid)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	djson, err := s.d.DB.Dossier(r.Context(), iid)
	if err != nil {
		http.Error(w, "no dossier", 404)
		return
	}
	var d pipeline.Dossier
	json.Unmarshal([]byte(djson), &d)
	title, _ := s.d.DB.StoryTitle(r.Context(), ii.StoryID)
	s.render(w, "page-dossier", map[string]any{
		"Item": ii, "Title": title, "Dossier": d,
	})
}

func (s *Server) draftRun(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	pub, err := s.d.DB.GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	runner := &pipeline.Runner{
		DB: s.d.DB, LLM: s.d.LLM, Fetcher: s.d.Fetcher,
		TriageModel: s.d.Cfg.ModelTriage, DraftModel: s.d.Cfg.ModelDraft,
		MaxDeepDives: s.d.Cfg.MaxDeepDives,
	}
	if _, err := runner.RunPhaseB(r.Context(), pub, iss.ID); err != nil {
		http.Error(w, "draft: "+err.Error(), 500)
		return
	}
	s.redirect(w, r, "/issue/"+r.PathValue("id")+"/draft")
}

func (s *Server) draftPage(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	pub, _ := s.d.DB.GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	var blockers []string
	for _, sec := range pub.Sections {
		if sec.Commentary == "required" && strings.TrimSpace(iss.Commentary[sec.Title]) == "" {
			blockers = append(blockers, fmt.Sprintf("commentary required in %q", sec.Title))
		}
	}
	if len(iss.Flags) > 0 {
		blockers = append(blockers, fmt.Sprintf("%d flagged sentence(s) unresolved", len(iss.Flags)))
	}
	s.render(w, "page-draft", map[string]any{
		"Pub": pub, "Issue": iss, "BodyHTML": template.HTML(delivery.RenderHTML(iss.Subject, iss.BodyMD)),
		"Sections": pub.Sections, "Blockers": blockers,
	})
}

func (s *Server) commentary(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	c := iss.Commentary
	if c == nil {
		c = map[string]string{}
	}
	c[r.FormValue("section")] = r.FormValue("text")
	if err := s.d.DB.SaveCommentary(r.Context(), iss.ID, c); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.redirect(w, r, "/issue/"+r.PathValue("id")+"/draft")
}

func (s *Server) flagDismiss(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	idx, _ := strconv.Atoi(r.PathValue("idx"))
	flags := append([]store.Flag{}, iss.Flags...)
	if idx >= 0 && idx < len(flags) {
		flags = append(flags[:idx], flags[idx+1:]...)
	}
	if err := s.d.DB.ResolveFlags(r.Context(), iss.ID, flags); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.redirect(w, r, "/issue/"+r.PathValue("id")+"/draft")
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	pub, _ := s.d.DB.GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	for _, sec := range pub.Sections {
		if sec.Commentary == "required" && strings.TrimSpace(iss.Commentary[sec.Title]) == "" {
			http.Error(w, fmt.Sprintf("blocked: commentary required in %q", sec.Title), 409)
			return
		}
	}
	if len(iss.Flags) > 0 {
		http.Error(w, fmt.Sprintf("blocked: %d flagged sentence(s) unresolved", len(iss.Flags)), 409)
		return
	}
	if err := s.d.DB.SetIssueStatus(r.Context(), iss.ID, "approved"); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.d.DB.RecordDecision(r.Context(), iss.PublicationID, 0, "approve", "issue approved")
	s.redirect(w, r, "/issue/"+r.PathValue("id")+"/send")
}

func (s *Server) sendPage(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	pub, _ := s.d.DB.GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	s.render(w, "page-send", map[string]any{"Pub": pub, "Issue": iss})
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if iss.Status != "approved" {
		http.Error(w, "issue must be approved before sending", 409)
		return
	}
	to := []string{"readers@kloxus.demo"}
	if t := r.FormValue("to"); t != "" {
		to = []string{t}
	}
	path, err := s.d.Mailer.Send(iss.Subject, iss.BodyMD, delivery.RenderHTML(iss.Subject, iss.BodyMD), to)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.d.DB.SetIssueStatus(r.Context(), iss.ID, "sent")
	s.render(w, "page-sent", map[string]any{"Path": path})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	switch r.URL.Query().Get("fmt") {
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=issue-%d.html", iss.Number))
		w.Write([]byte(delivery.RenderHTML(iss.Subject, iss.BodyMD)))
	default:
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=issue-%d.md", iss.Number))
		w.Write([]byte(iss.BodyMD))
	}
}

// --- guide / costs ---

func (s *Server) guidePage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	version, content, _ := s.d.DB.LatestGuide(r.Context(), pub.ID)
	s.render(w, "page-guide", map[string]any{"Pub": pub, "Version": version, "Content": content})
}

func (s *Server) guideSave(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if _, err := s.d.DB.SaveGuide(r.Context(), pub.ID, r.FormValue("content")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/guide")
}

func (s *Server) guideRebuild(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if _, _, err := editorial.Rebuild(r.Context(), s.d.DB, pub, s.d.LLM, s.d.Cfg.ModelTriage); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/guide")
}

func (s *Server) costsPage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	rows, _ := s.d.DB.CostReport(r.Context(), pub.ID, 4)
	s.render(w, "page-costs", map[string]any{"Pub": pub, "Rows": rows})
}

// --- paywall / reader ---

func (s *Server) paywallPage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	rid, _ := s.d.DB.UpsertReader(r.Context(), pub.ID, "demo@reader.test")
	active, _ := s.d.DB.HasActiveSubscription(r.Context(), rid)
	s.render(w, "page-paywall", map[string]any{
		"Pub": pub, "Active": active, "Reader": "demo@reader.test",
	})
}

func (s *Server) paywallSimulate(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	rid, _ := s.d.DB.UpsertReader(r.Context(), pub.ID, "demo@reader.test")
	if err := s.d.DB.ActivateSubscription(r.Context(), rid, "fake", "sim-"+strconv.FormatInt(rid, 10)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.redirect(w, r, "/pub/"+r.PathValue("id")+"/paywall")
}

// readerPage shows the issue as a reader sees it: paid sections are
// replaced by a gate unless the test reader has an active subscription.
func (s *Server) readerPage(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	iss, err := s.d.DB.LatestIssue(r.Context(), pub.ID)
	if err != nil {
		http.Error(w, "no issue yet", 404)
		return
	}
	rid, _ := s.d.DB.UpsertReader(r.Context(), pub.ID, "demo@reader.test")
	active, _ := s.d.DB.HasActiveSubscription(r.Context(), rid)
	body := iss.BodyMD
	if !active {
		body = gateSections(body, pub.Paywall.PaidSections)
	}
	s.render(w, "page-reader", map[string]any{
		"Pub": pub, "Issue": iss, "Active": active,
		"BodyHTML": template.HTML(delivery.RenderHTML(iss.Subject, body)),
	})
}

// gateSections replaces paid sections of the issue body with a gate notice.
func gateSections(body string, paid []string) string {
	paidSet := map[string]bool{}
	for _, p := range paid {
		paidSet[p] = true
	}
	var out []string
	gated := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			title := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			gated = paidSet[title]
			out = append(out, line)
			if gated {
				out = append(out, "", "> **This section is for paid readers.** Subscribe to unlock.", "")
				gated = false
				out = append(out, "<!-- gated -->")
				continue
			}
			continue
		}
		if len(out) > 0 && out[len(out)-1] == "<!-- gated -->" {
			continue // skip content inside a gated section until next ##
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// --- demo reset / webhook ---

func (s *Server) demoReset(w http.ResponseWriter, r *http.Request) {
	if err := s.d.DB.Reset(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.d.Reseed != nil {
		if err := s.d.Reseed(r.Context()); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
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
	if s.d.Cfg.WebhookSecret != "" {
		if r.Header.Get("Authorization") != "Bearer "+s.d.Cfg.WebhookSecret {
			http.Error(w, "unauthorized", 401)
			return
		}
	}
	var ev fcEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "bad payload", 400)
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
	src, err := s.d.DB.FindSourceByMonitor(r.Context(), mid)
	if err != nil {
		slog.Warn("webhook: unknown monitor", "id", mid)
		w.WriteHeader(202)
		return
	}
	title := ev.Data.Title
	if title == "" {
		title = ev.Data.URL
	}
	if _, err := s.d.DB.SaveItems(r.Context(), src.ID, []sources.RawItem{{
		ExternalID:  ev.Data.URL + "#" + ev.Data.Status,
		URL:         ev.Data.URL,
		Title:       title,
		ContentText: ev.Data.Markdown,
	}}, func(u string) string { return u }); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	slog.Info("firecrawl monitor.page ingested", "url", ev.Data.URL, "status", ev.Data.Status)
	w.WriteHeader(204)
}
