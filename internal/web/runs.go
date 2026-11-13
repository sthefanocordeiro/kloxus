package web

// Pipeline run lifecycle: prepare runs launched by the API, polled
// through /api/v1/runs and /api/v1/runs/{id}/events.
import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// defaultRunTimeout bounds a collect or draft run's work the way
// reqTimeout bounds a request. statement_timeout cannot deliver its
// error over a dead socket, so a dropped pooled conn would otherwise
// wedge the worker forever — the run never reports Done, the
// publication's run slot stays locked, and the checked-out conn leaks.
// On expiry the run is marked failed and the slot frees. Sized well
// above real run durations (the hosted e2e journeys run ~2–3 min);
// Deps.RunTimeout overrides it in tests.
const defaultRunTimeout = 15 * time.Minute

func (s *Server) runTimeout() time.Duration {
	if s.d.RunTimeout > 0 {
		return s.d.RunTimeout
	}
	return defaultRunTimeout
}

// runState is the JSON payload served by /api/v1/runs/{id}.
type runState struct {
	RunID      string `json:"run_id"`
	Kind       string `json:"kind"` // prepare | draft
	Step       string `json:"step"`
	Done       bool   `json:"done"`
	IssueID    int64  `json:"issue_id"`
	NoNewItems bool   `json:"no_new_items"`
	Err        string `json:"error"`
	// Blocked marks a run refused or aborted by a spend guard; the
	// creator gets the message in Err plus the recorded sample.
	Blocked       bool   `json:"blocked,omitempty"`
	BlockedReason string `json:"blocked_reason,omitempty"`
	Sample        bool   `json:"sample,omitempty"`
}

// blockRun marks a refused/failed-guard run: done, carrying the
// creator-facing message and the recorded-sample pointer.
func (st *runProgress) blockRun(b *llm.BlockedError) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.st.Done = true
	st.st.Blocked = true
	st.st.BlockedReason = string(b.Reason)
	st.st.Sample = true
	st.st.Err = b.Message
}

// admitRun applies the spend guards before a pipeline run launches
// (D32/D36). A bookkeeping failure logs and admits — the guards degrade
// rather than wedge the product on a ledger hiccup.
//
// Deterministic mode (KLOXUS_LLM_MODE=off) is exempt: the run makes no
// model calls, so AI admission — kill switch, budgets, the run-start
// ledger charge — does not apply to it. The guard still exists for the
// admin surface and keeps wrapping any real provider.
func (s *Server) admitRun(ctx context.Context, wsID string) *llm.BlockedError {
	if s.d.Guard == nil || s.d.Cfg.LLMOff() {
		return nil
	}
	err := s.d.Guard.AdmitRun(ctx, wsID)
	if err == nil {
		return nil
	}
	if b, ok := llm.AsBlocked(err); ok {
		return b
	}
	slog.Warn("run admission check failed", "ws", wsID, "err", err)
	return nil
}

// markIssueBlocked leaves a failed Phase A issue in a resumable state:
// 'blocked' keeps whatever triage the run completed on the board while
// telling the creator a fresh run is the way forward.
func (s *Server) markIssueBlocked(wsp *workspace.Workspace, issueID int64) {
	if issueID == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := wsp.DB.SetIssueStatus(ctx, issueID, "blocked"); err != nil {
		slog.Warn("failed issue could not be marked blocked", "issue", issueID, "err", err)
	}
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
	// Spend guards: a refused run still gets a run record — done,
	// blocked, carrying the message and the recorded sample — so the
	// creator sees an answer, never an error page.
	if b := s.admitRun(context.Background(), wsp.ID); b != nil {
		st.blockRun(b)
		return st
	}
	go func() {
		wsp.Activity.RLock()
		defer wsp.Activity.RUnlock()
		// A pipeline panic must fail this run, not take the process —
		// and every other workspace — down with it (review F5).
		defer func() {
			if p := recover(); p != nil {
				st.mu.Lock()
				st.st.Done = true
				st.st.Err = fmt.Sprintf("pipeline panic: %v", p)
				issueID := st.st.IssueID
				st.mu.Unlock()
				s.markIssueBlocked(wsp, issueID)
				slog.Error("pipeline run panicked", "pub", pub.ID, "panic", p, "rid", st.st.RunID)
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
			OnIssue: func(issueID int64) {
				st.mu.Lock()
				st.st.IssueID = issueID
				st.mu.Unlock()
			},
		}
		ctx, cancel := context.WithTimeout(llm.WithRunScope(context.Background(), wsp.ID), s.runTimeout())
		defer cancel()
		res, err := runner.RunPhaseA(ctx, pub)
		st.mu.Lock()
		st.st.Done = true
		issueID := st.st.IssueID
		if err != nil {
			st.st.Err = err.Error()
			if b, ok := llm.AsBlocked(err); ok {
				st.st.Blocked = true
				st.st.BlockedReason = string(b.Reason)
				st.st.Sample = true
			}
			st.mu.Unlock()
			// A failed step leaves the run resumable: the partial issue
			// is marked blocked and a fresh run starts clean.
			s.markIssueBlocked(wsp, issueID)
			slog.Error("pipeline run failed", "pub", pub.ID, "err", err, "rid", st.st.RunID)
			return
		}
		st.st.NoNewItems = res.NoNewItems
		st.st.IssueID = res.IssueID
		st.st.Step = pipeline.Steps[len(pipeline.Steps)-1] // report the final step, not the last one ticked
		st.mu.Unlock()
		// A nothing-new run made no model calls — the daily budget
		// counts real runs only, so refund its start (D36).
		// Deterministic runs were never charged, so there is nothing
		// to refund — undoing would drop a real run's ledger row.
		if res.NoNewItems && s.d.Guard != nil && !s.d.Cfg.LLMOff() {
			s.d.Guard.UndoRunStart(ctx, wsp.ID)
		}
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
