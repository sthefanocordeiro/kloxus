package web

// Pipeline run lifecycle: prepare runs launched by the API, polled
// through /api/v1/runs and /api/v1/runs/{id}/events.
import (
	"context"
	"log/slog"
	"sync"

	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// runState is the JSON payload served by /api/v1/runs/{id}.
type runState struct {
	RunID      string `json:"run_id"`
	Kind       string `json:"kind"` // prepare | draft
	Step       string `json:"step"`
	Done       bool   `json:"done"`
	IssueID    int64  `json:"issue_id"`
	NoNewItems bool   `json:"no_new_items"`
	Err        string `json:"error"`
}

// runProgress wraps runState with a mutex: the pipeline goroutine writes
// it while /api/v1/runs/{id} reads from other goroutines. events is the
// per-step event log streamed by /api/v1/runs/{id}/events.
type runProgress struct {
	mu     sync.Mutex
	st     runState
	events []pipeline.StepEvent
}

// startRun launches a Phase A run for the publication, reusing an
// in-flight run when one exists.
func (s *Server) startRun(wsp *workspace.Workspace, pub *store.Publication) *runProgress {
	st := &runProgress{st: runState{RunID: newReqID(), Kind: "prepare", Step: "starting"}}
	if prev, running := wsp.Runs.LoadOrStore(pub.ID, st); running {
		// One pipeline run at a time per workspace: an in-flight run wins;
		// a finished run is replaced by this new one.
		pst := prev.(*runProgress)
		pst.mu.Lock()
		done := pst.st.Done
		pst.mu.Unlock()
		if !done {
			return pst
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
			OnEvent: func(ev pipeline.StepEvent) {
				st.mu.Lock()
				st.events = append(st.events, ev)
				st.mu.Unlock()
			},
			OnIssue: func(issueID int64) {
				st.mu.Lock()
				st.st.IssueID = issueID
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
		st.st.Step = pipeline.Steps[len(pipeline.Steps)-1] // report the final step, not the last one ticked
	}()
	return st
}

// findRun locates a run (prepare or draft) by its run id.
func findRun(wsp *workspace.Workspace, id string) (*runProgress, bool) {
	var out *runProgress
	wsp.Runs.Range(func(_, v any) bool {
		st, ok := v.(*runProgress)
		if !ok {
			return true
		}
		st.mu.Lock()
		match := st.st.RunID == id
		st.mu.Unlock()
		if match {
			out = st
			return false
		}
		return true
	})
	return out, out != nil
}
