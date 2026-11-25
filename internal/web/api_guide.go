package web

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/editorial"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

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
