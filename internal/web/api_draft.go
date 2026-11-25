package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

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
