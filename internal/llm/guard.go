package llm

// Spend guards for real model calls (D32, D36): a per-workspace run
// budget, a per-run token budget, a global daily spend cap and a kill
// switch — all persisted in Postgres so restarts do not reset them.
// The Guard wraps the real provider and refuses calls with
// *BlockedError; run admission (AdmitRun) applies the same checks
// before a pipeline run launches.
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// BlockedReason identifies which guard refused a call or run.
type BlockedReason string

const (
	ReasonKillSwitch     BlockedReason = "kill_switch"
	ReasonWorkspaceRuns  BlockedReason = "workspace_run_budget"
	ReasonWorkspaceToken BlockedReason = "workspace_token_budget"
	ReasonDailyCap       BlockedReason = "daily_spend_cap"
)

// BlockedError says a guard refused a model call or a run. It carries a
// creator-facing message — never an internal detail.
type BlockedError struct {
	Reason  BlockedReason
	Message string
}

func (e *BlockedError) Error() string { return e.Message }

// AsBlocked reports whether err is a *BlockedError.
func AsBlocked(err error) (*BlockedError, bool) {
	var b *BlockedError
	if errors.As(err, &b) {
		return b, true
	}
	return nil, false
}

// SpendStore persists run starts, call usage and the kill switch. The
// Postgres implementation lives in internal/store; a nil Store falls
// back to in-memory counters (tests, embedded runs).
type SpendStore interface {
	LLMRunStart(ctx context.Context, ws string) error
	// LLMRunUndo drops one recorded run start — the budget counts real
	// runs, so a run that collected nothing and made no model calls is
	// refunded.
	LLMRunUndo(ctx context.Context, ws string) error
	LLMCall(ctx context.Context, ws, model string, in, out int, cost float64) error
	// Timestamps travel in the schema's TEXT UTC shape
	// ('YYYY-MM-DD HH24:MI:SS') — same convention as store.RequeueStaleJobs.
	LLMRunsSince(ctx context.Context, ws, since string) (int, error)
	LLMSpendSince(ctx context.Context, since string) (float64, error)
	AppSetting(ctx context.Context, key string) (string, error)
	SetAppSetting(ctx context.Context, key, value string) error
}

// killSetting is the app_settings key for the persisted kill switch.
const killSetting = "llm_kill"

// runScope carries the calling workspace and the token counter for one
// pipeline run through the context, so Guard.Complete can account a
// call to the right workspace and bound tokens per run.
type runScope struct {
	ws   string
	used int64
}

type scopeKey struct{}

// WithRunScope returns a context accounting model calls to workspace ws
// under a fresh per-run token counter. Callers that only need the
// workspace (interactive calls) use WithWorkspace instead.
func WithRunScope(ctx context.Context, ws string) context.Context {
	return context.WithValue(ctx, scopeKey{}, &runScope{ws: ws})
}

// WithWorkspace returns a context accounting calls to workspace ws
// without a per-run token budget.
func WithWorkspace(ctx context.Context, ws string) context.Context {
	return context.WithValue(ctx, scopeKey{}, &runScope{ws: ws, used: -1})
}

func scopeOf(ctx context.Context) *runScope {
	if s, ok := ctx.Value(scopeKey{}).(*runScope); ok {
		return s
	}
	return &runScope{used: -1}
}

// Guard is a Provider that enforces the spend limits before forwarding
// to Inner. Limits of 0 are disabled.
type Guard struct {
	Inner Provider
	Store SpendStore
	// Now supplies the clock (tests inject a fake). Nil uses time.Now.
	Now func() time.Time
	// RunsPerDay caps pipeline run starts per workspace per UTC day.
	RunsPerDay int
	// TokensPerRun caps total input+output tokens inside one run scope.
	TokensPerRun int64
	// DailyCapUSD caps the recorded global spend per UTC day.
	DailyCapUSD float64
	// KillEnv names the env kill switch (default KLOXUS_LLM_KILL): any
	// non-empty value stops calls without touching stored state.
	KillEnv string

	mu     sync.Mutex // guards mem counters and killed flag
	killed bool       // in-process copy of the persisted flag
	mem    map[string]int
}

func (g *Guard) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Guard) killEnv() string {
	if g.KillEnv != "" {
		return g.KillEnv
	}
	return "KLOXUS_LLM_KILL"
}

// dayStart returns the stored-timestamp form of the current UTC day's
// start — the window budgets measure against.
func (g *Guard) dayStart() string {
	return g.dayKey() + " 00:00:00"
}

func (g *Guard) dayKey() string { return g.now().UTC().Format("2006-01-02") }

// Killed reports whether the kill switch is on (in-process copy, env
// var, or — when no local flag was loaded — the persisted setting).
func (g *Guard) Killed(ctx context.Context) bool {
	g.mu.Lock()
	k := g.killed
	g.mu.Unlock()
	if k || os.Getenv(g.killEnv()) != "" {
		return true
	}
	if g.Store != nil {
		if v, err := g.Store.AppSetting(ctx, killSetting); err == nil {
			return v == "1" || v == "on" || v == "true"
		}
	}
	return false
}

// SetKill flips the kill switch: persisted via the store (survives
// restarts) and mirrored into the in-process copy so the next call sees
// it without a reload.
func (g *Guard) SetKill(ctx context.Context, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	if g.Store != nil {
		if err := g.Store.SetAppSetting(ctx, killSetting, v); err != nil {
			return err
		}
	}
	g.mu.Lock()
	g.killed = on
	g.mu.Unlock()
	return nil
}

// LoadKill primes the in-process flag from persisted state at boot.
func (g *Guard) LoadKill(ctx context.Context) {
	if g.Store == nil {
		return
	}
	if v, err := g.Store.AppSetting(ctx, killSetting); err == nil {
		g.mu.Lock()
		g.killed = v == "1" || v == "on" || v == "true"
		g.mu.Unlock()
	}
}

func (g *Guard) runsToday(ctx context.Context, ws string) int {
	if g.Store != nil {
		if n, err := g.Store.LLMRunsSince(ctx, ws, g.dayStart()); err == nil {
			return n
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.mem == nil {
		return 0
	}
	return g.mem["runs:"+g.dayKey()+":"+ws]
}

func (g *Guard) spendToday(ctx context.Context) float64 {
	return g.SpendToday(ctx)
}

// SpendToday returns the recorded global spend for the current UTC day.
func (g *Guard) SpendToday(ctx context.Context) float64 {
	if g.Store != nil {
		if v, err := g.Store.LLMSpendSince(ctx, g.dayStart()); err == nil {
			return v
		}
	}
	return 0
}

// AdmitRun applies the run-level checks — kill switch, per-workspace
// daily run budget, global daily cap — and records the run start when
// admitted. A *BlockedError means the run must not launch.
func (g *Guard) AdmitRun(ctx context.Context, ws string) error {
	if g.Killed(ctx) {
		return &BlockedError{Reason: ReasonKillSwitch,
			Message: "Model calls are paused by the operator right now — the recorded sample is shown instead."}
	}
	if g.RunsPerDay > 0 && g.runsToday(ctx, ws) >= g.RunsPerDay {
		return &BlockedError{Reason: ReasonWorkspaceRuns,
			Message: fmt.Sprintf("This workspace reached its limit of %d runs for today — try again tomorrow. The recorded sample is shown below.", g.RunsPerDay)}
	}
	if g.DailyCapUSD > 0 && g.spendToday(ctx) >= g.DailyCapUSD {
		return &BlockedError{Reason: ReasonDailyCap,
			Message: "Today's shared model budget is spent — the recorded sample is shown instead. Try again tomorrow."}
	}
	if g.Store != nil {
		if err := g.Store.LLMRunStart(ctx, ws); err != nil {
			return fmt.Errorf("recording run start: %w", err)
		}
	} else {
		g.mu.Lock()
		if g.mem == nil {
			g.mem = map[string]int{}
		}
		g.mem["runs:"+g.dayKey()+":"+ws]++
		g.mu.Unlock()
	}
	return nil
}

// UndoRunStart refunds one admitted run start: a run that collected no
// new items made no model calls, and the per-workspace budget counts
// real runs only (D36). Which stored row goes does not matter — the
// count is what the budget reads.
func (g *Guard) UndoRunStart(ctx context.Context, ws string) {
	if g.Store != nil {
		if err := g.Store.LLMRunUndo(ctx, ws); err != nil {
			slog.Warn("run-start refund failed", "ws", ws, "err", err)
		}
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := "runs:" + g.dayKey() + ":" + ws
	if g.mem[k] > 0 {
		g.mem[k]--
	}
}

// Complete implements Provider: guards, then forwards to Inner and
// records the call's real usage.
func (g *Guard) Complete(ctx context.Context, req Request) (Response, error) {
	scope := scopeOf(ctx)
	if g.Killed(ctx) {
		return Response{}, &BlockedError{Reason: ReasonKillSwitch,
			Message: "Model calls are paused by the operator right now — the recorded sample is shown instead."}
	}
	if g.TokensPerRun > 0 && scope.used >= 0 && scope.used >= g.TokensPerRun {
		return Response{}, &BlockedError{Reason: ReasonWorkspaceToken,
			Message: fmt.Sprintf("This run passed its token budget of %d — the recorded sample is shown instead.", g.TokensPerRun)}
	}
	if g.DailyCapUSD > 0 && g.spendToday(ctx) >= g.DailyCapUSD {
		return Response{}, &BlockedError{Reason: ReasonDailyCap,
			Message: "Today's shared model budget is spent — the recorded sample is shown instead. Try again tomorrow."}
	}
	resp, err := g.Inner.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if scope.used >= 0 {
		scope.used += int64(resp.InputTokens + resp.OutputTokens)
	}
	if g.Store != nil {
		if err := g.Store.LLMCall(ctx, scope.ws, req.Model,
			resp.InputTokens, resp.OutputTokens,
			CostUSD(req.Model, resp.InputTokens, resp.OutputTokens)); err != nil {
			// Usage accounting must not fail a call that already
			// succeeded; the run's own cost report keeps its record.
			_ = err
		}
	}
	return resp, nil
}
