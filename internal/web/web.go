// Package web serves the Kloxus HTTP surface: the embedded React SPA,
// the /api/v1 JSON contract, reader-facing pages, the workspace outbox,
// and the Firecrawl webhook receiver.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// Deps wires the server's collaborators (demo or real implementations).
type Deps struct {
	WS      *workspace.Manager
	Cfg     *config.Config
	Fetcher sources.Fetcher
	LLM     llm.Provider
	Monitor sources.MonitorProvider
	// Mailer transports real email when configured (Resend in
	// production, a fake in tests). Nil means outbox-only mode: every
	// send is recorded in the workspace outbox and nothing leaves.
	Mailer delivery.Sender
	// RunTimeout bounds one pipeline run (collect or draft). Zero uses
	// defaultRunTimeout; tests set it short to exercise the deadline.
	RunTimeout time.Duration
	// JobTimeout bounds one scheduled job's execution. Zero uses
	// defaultJobTimeout.
	JobTimeout time.Duration
	// Seed re-creates the example publication inside a database; used for
	// workspace resets and the hosted template.
	Seed func(ctx context.Context, db *store.DB) error
}

// Server is the UI handler.
type Server struct {
	d          Deps
	mux        *http.ServeMux
	sampleOnce sync.Once
	sampleV    *config.Publication
	sampleErr  error
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
	m.HandleFunc("GET /healthz", s.healthz)
	m.HandleFunc("GET /sample/a", s.sampleArticle)
	m.HandleFunc("GET /sample/a/", s.sampleArticle) // tolerate a stray trailing slash
	m.HandleFunc("GET /a/{id}", s.articlePage)
	m.HandleFunc("GET /outbox/{id}/eml", s.outboxEML)
	m.HandleFunc("POST /webhooks/firecrawl", s.firecrawlWebhook)
	m.HandleFunc("POST /webhooks/resend", s.resendWebhook)
	// Claim and opt-in links open in any browser — they carry their own
	// proof (token/MAC) and must not mint a visitor workspace first.
	// The trailing-slash twins keep a stray slash on an emailed link
	// from falling through to the SPA 404 (KLOX-27), same as /sample/a.
	m.HandleFunc("GET /claim", s.claimPage)
	m.HandleFunc("GET /claim/", s.claimPage)
	m.HandleFunc("POST /claim", s.claimConfirm)
	m.HandleFunc("POST /claim/", s.claimConfirm)
	m.HandleFunc("GET /confirm-sub", s.confirmSub)
	m.HandleFunc("GET /confirm-sub/", s.confirmSub)
	m.HandleFunc("POST /unsub", s.unsubOneClick) // RFC 8058 one-click
	m.HandleFunc("POST /unsub/", s.unsubOneClick)

	// Old /app/* URLs forward to the same paths at the root; anything
	// else GETable falls through to the SPA.
	m.HandleFunc("GET /app", s.appLegacyRedirect)
	m.HandleFunc("GET /app/", s.appLegacyRedirect)
	m.HandleFunc("GET /", s.appPage)

	// JSON API (v2 contract — see docs/api.md). Same workspace cookie,
	// rate limits and isolation as the public routes.
	m.HandleFunc("GET /api/v1/workspace", s.apiWorkspace)
	m.HandleFunc("POST /api/v1/workspace/claim", s.apiClaimStart)
	m.HandleFunc("POST /api/v1/p/{slug}/subscribe", s.apiSubscribe)
	m.HandleFunc("GET /api/v1/sample", s.apiSample)
	m.HandleFunc("GET /api/v1/proposal", s.apiProposal)
	m.HandleFunc("POST /api/v1/publications", s.apiPublicationCreate)
	m.HandleFunc("POST /api/v1/publications/propose", s.apiPropose)
	m.HandleFunc("GET /api/v1/publications", s.apiPublications)
	m.HandleFunc("GET /api/v1/publications/{id}", s.apiPublication)
	m.HandleFunc("GET /api/v1/publications/{id}/sources", s.apiSources)
	m.HandleFunc("POST /api/v1/publications/{id}/sources", s.apiSourceAdd)
	m.HandleFunc("DELETE /api/v1/publications/{id}/sources/{sid}", s.apiSourceDelete)
	m.HandleFunc("GET /api/v1/publications/{id}/sources/catalog", s.apiSourceCatalog)
	m.HandleFunc("GET /api/v1/publications/{id}/sources/preview", s.apiSourcePreview)
	m.HandleFunc("POST /api/v1/publications/{id}/sources/confirm", s.apiSourceConfirm)
	m.HandleFunc("POST /api/v1/publications/{id}/sources/{sid}/watch", s.apiSourceWatch)
	m.HandleFunc("POST /api/v1/publications/{id}/sources/{sid}/check", s.apiSourceCheck)
	m.HandleFunc("POST /api/v1/publications/{id}/runs", s.apiRunStart)
	m.HandleFunc("GET /api/v1/publications/{id}/runs/latest", s.apiRunLatest)
	m.HandleFunc("GET /api/v1/publications/{id}/issues", s.apiIssues)
	m.HandleFunc("GET /api/v1/publications/{id}/outbox", s.apiOutbox)
	m.HandleFunc("GET /api/v1/publications/{id}/reader", s.apiReader)
	m.HandleFunc("POST /api/v1/publications/{id}/paywall/simulate", s.apiPaywallSimulate)
	m.HandleFunc("POST /api/v1/publications/{id}/paywall/cancel", s.apiPaywallCancel)
	m.HandleFunc("GET /api/v1/publications/{id}/guide", s.apiGuide)
	m.HandleFunc("PUT /api/v1/publications/{id}/guide", s.apiGuideSave)
	m.HandleFunc("GET /api/v1/publications/{id}/guide/versions", s.apiGuideVersions)
	m.HandleFunc("POST /api/v1/publications/{id}/guide/rebuild", s.apiGuideRebuild)
	m.HandleFunc("GET /api/v1/publications/{id}/costs", s.apiCosts)
	m.HandleFunc("GET /api/v1/runs/{id}", s.apiRun)
	m.HandleFunc("GET /api/v1/runs/{id}/events", s.apiRunEvents)
	m.HandleFunc("GET /api/v1/issues/{id}", s.apiIssue)
	m.HandleFunc("GET /api/v1/issues/{id}/readiness", s.apiReadiness)
	m.HandleFunc("GET /api/v1/issues/{id}/items", s.apiIssueItems)
	m.HandleFunc("GET /api/v1/issues/{id}/candidates", s.apiCandidates)
	m.HandleFunc("PUT /api/v1/issues/{id}/lineup", s.apiLineup)
	m.HandleFunc("GET /api/v1/stories/{sid}", s.apiStory)
	m.HandleFunc("POST /api/v1/issues/{id}/items/{iid}/decisions", s.apiDecision)
	m.HandleFunc("GET /api/v1/items/{iid}/dossier", s.apiDossier)
	m.HandleFunc("POST /api/v1/issues/{id}/draft", s.apiDraft)
	m.HandleFunc("POST /api/v1/issues/{id}/compose", s.apiCompose)
	m.HandleFunc("POST /api/v1/issues/{id}/items/{ref}/compose", s.apiComposeSentence)
	m.HandleFunc("GET /api/v1/issues/{id}/document", s.apiDocument)
	m.HandleFunc("PATCH /api/v1/issues/{id}/sentences/{sid}", s.apiSentenceEdit)
	m.HandleFunc("DELETE /api/v1/issues/{id}/sentences/{sid}", s.apiSentenceRemove)
	m.HandleFunc("POST /api/v1/issues/{id}/items/{ref}/sentences", s.apiSentenceRestore)
	m.HandleFunc("POST /api/v1/issues/{id}/sentences/{sid}/interpretation", s.apiSentenceInterpretation)
	m.HandleFunc("PUT /api/v1/issues/{id}/commentary", s.apiCommentary)
	m.HandleFunc("POST /api/v1/issues/{id}/approve", s.apiApprove)
	m.HandleFunc("POST /api/v1/issues/{id}/send", s.apiSend)
	m.HandleFunc("POST /api/v1/issues/{id}/hold", s.apiHold)
	m.HandleFunc("GET /api/v1/issues/{id}/after", s.apiAfter)
	m.HandleFunc("GET /api/v1/p/{slug}/{num}", s.apiPublicIssue)
	m.HandleFunc("GET /api/v1/unsub", s.apiUnsub)
	m.HandleFunc("GET /api/v1/issues/{id}/export", s.apiExport)
	m.HandleFunc("GET /api/v1/issues/{id}/preview", s.apiPreview)
	m.HandleFunc("GET /api/v1/outbox/{id}", s.apiOutboxEmail)
	m.HandleFunc("POST /api/v1/demo/reset", s.apiDemoReset)
	m.HandleFunc("POST /api/v1/demo/new", s.apiDemoNew)
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		m.HandleFunc(method+" /api/v1/", s.apiNotFound)
	}
	return s
}

// reqTimeout bounds each request's work — chiefly its Postgres round
// trips. A pooled connection the Supabase pooler silently dropped can
// leave a handler waiting on a dead socket indefinitely; the deadline
// turns that into an error instead. The run-events SSE stream is exempt:
// its lifetime is a run's, not a request's.
const reqTimeout = 3 * time.Minute

// dispatchTimeout bounds one scheduler dispatch cycle — the claim plus
// every job it runs serially — so a dead conn can't wedge the single
// dispatcher goroutine for good.
const dispatchTimeout = 10 * time.Minute

// ServeHTTP resolves the request workspace (cookie in hosted mode), caps
// request bodies, assigns a request id, then dispatches.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rid := newReqID()
	w.Header().Set("X-Request-Id", rid)
	r = r.WithContext(context.WithValue(r.Context(), ctxReqID, rid))

	if !isEventStream(r) {
		ctx, cancel := context.WithTimeout(r.Context(), reqTimeout)
		defer cancel()
		r = r.WithContext(ctx)
	}

	if r.URL.Path == "/healthz" ||
		r.URL.Path == "/api/v1/sample" || strings.HasPrefix(r.URL.Path, "/sample/a") {
		s.mux.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/webhooks/") ||
		r.URL.Path == "/claim" || strings.HasPrefix(r.URL.Path, "/claim/") ||
		r.URL.Path == "/confirm-sub" || strings.HasPrefix(r.URL.Path, "/confirm-sub/") ||
		(r.Method == http.MethodPost && (r.URL.Path == "/unsub" || strings.HasPrefix(r.URL.Path, "/unsub/"))) {
		// Webhooks carry provider auth and emailed links their own token
		// — resolving would mint a junk workspace per call and trip the
		// create limit.
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		s.mux.ServeHTTP(w, r)
		return
	}
	ws, err := s.d.WS.Resolve(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), ctxWS, ws))
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			// The API is JSON-only: mutating calls must say so.
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				apiErr(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
					"mutating /api/v1 routes need Content-Type: application/json")
				return
			}
		}
	}
	s.mux.ServeHTTP(w, r)
}

// isEventStream reports the SSE endpoint: GET /api/v1/runs/{id}/events
// stays open for the life of a run, so the request deadline must not
// apply.
func isEventStream(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		strings.HasPrefix(r.URL.Path, "/api/v1/runs/") &&
		strings.HasSuffix(r.URL.Path, "/events")
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

// publicBase is the absolute origin for links that leave the app —
// emails, exports, published-issue URLs. A configured public URL wins
// over the request's own host: the app may answer on throwaway
// verification hosts, but mail has to point at the canonical
// deployment (KLOX-27). Without one the request origin is the base.
func (s *Server) publicBase(r *http.Request) string {
	if u := strings.TrimRight(s.d.Cfg.PublicURL, "/"); u != "" {
		return u
	}
	return requestBase(r)
}

// requestBase is the absolute origin the request itself arrived on —
// the fallback for publicBase when no public URL is configured.
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

func (s *Server) pub(r *http.Request) (*store.Publication, error) {
	return s.canonicalPub(db(r).GetPublication(r.Context(), r.PathValue("id")))
}

func (s *Server) issue(r *http.Request) (*store.Issue, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, err
	}
	return db(r).GetIssue(r.Context(), id)
}

func reqIDOf(r *http.Request) string {
	if v, ok := r.Context().Value(ctxReqID).(string); ok {
		return v
	}
	return ""
}
