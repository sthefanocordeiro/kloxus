package web

// Regression coverage for the deterministic-mode admission bypass
// (KLOXUS_LLM_MODE=off): a no-AI run makes zero model calls, so the AI
// spend guards — kill switch, run budget, daily cap, the run-start
// ledger charge — must not apply to it. AI-mode admission is unchanged.
// These tests need no database: a counting SpendStore stands in for the
// Postgres ledger and observes that nothing was charged.
import (
	"context"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
)

// countSpend is a SpendStore that only counts run starts; the other
// methods answer configured values so the budget checks are exercisable.
type countSpend struct {
	starts int
	undos  int
	runs   int
	spend  float64
	kill   string
}

func (c *countSpend) LLMRunStart(context.Context, string) error { c.starts++; return nil }
func (c *countSpend) LLMRunUndo(context.Context, string) error  { c.undos++; return nil }
func (c *countSpend) LLMCall(context.Context, string, string, int, int, float64) error {
	return nil
}
func (c *countSpend) LLMRunsSince(context.Context, string, string) (int, error) {
	return c.runs, nil
}
func (c *countSpend) LLMSpendSince(context.Context, string) (float64, error) {
	return c.spend, nil
}
func (c *countSpend) AppSetting(_ context.Context, key string) (string, error) {
	return c.kill, nil
}
func (c *countSpend) SetAppSetting(_ context.Context, key, value string) error {
	if key == "llm_kill" {
		c.kill = value
	}
	return nil
}

func detGuardDeps(spend *countSpend, g *llm.Guard) *Server {
	g.Store = spend
	return &Server{d: Deps{
		Cfg:   &config.Config{Mode: "real", LLMMode: "off"},
		Guard: g,
		LLM:   llm.None{},
	}}
}

// TestAdmitRunDeterministicKillOn: the persisted kill switch pauses
// model calls; a deterministic run makes none, so it must be admitted —
// and must not consume the run budget either.
func TestAdmitRunDeterministicKillOn(t *testing.T) {
	ctx := context.Background()
	spend := &countSpend{}
	g := &llm.Guard{}
	if err := g.SetKill(ctx, true); err != nil {
		t.Fatalf("SetKill: %v", err)
	}
	g.Store = spend
	s := detGuardDeps(spend, g)
	if b := s.admitRun(ctx, "local"); b != nil {
		t.Fatalf("deterministic run refused by kill switch: %s", b.Reason)
	}
	if spend.starts != 0 {
		t.Fatalf("deterministic run charged %d ledger starts", spend.starts)
	}
}

// TestAdmitRunDeterministicBeyondBudget: deterministic admissions past
// the AI run allowance and over the daily cap still pass and are never
// charged — the allowance counts real model runs only.
func TestAdmitRunDeterministicBeyondBudget(t *testing.T) {
	ctx := context.Background()
	spend := &countSpend{runs: 5, spend: 10}
	g := &llm.Guard{RunsPerDay: 2, DailyCapUSD: 1}
	s := detGuardDeps(spend, g)
	for i := 0; i < 3; i++ {
		if b := s.admitRun(ctx, "local"); b != nil {
			t.Fatalf("deterministic admission %d refused: %s", i+1, b.Reason)
		}
	}
	if spend.starts != 0 {
		t.Fatalf("deterministic runs charged %d ledger starts", spend.starts)
	}
}

// TestAdmitRunAIModeStillBlocked: the same server shape with the model
// enabled keeps every refusal — kill switch, run budget, daily cap —
// and keeps charging admitted runs.
func TestAdmitRunAIModeStillBlocked(t *testing.T) {
	ctx := context.Background()
	newServer := func(spend *countSpend, g *llm.Guard) *Server {
		s := detGuardDeps(spend, g)
		s.d.Cfg.LLMMode = "auto"
		return s
	}

	spend := &countSpend{kill: "1"}
	g := &llm.Guard{Store: spend}
	if b := newServer(spend, g).admitRun(ctx, "local"); b == nil || b.Reason != llm.ReasonKillSwitch {
		t.Fatalf("AI run under kill = %v, want kill_switch", b)
	}

	spend = &countSpend{runs: 2}
	g = &llm.Guard{Store: spend, RunsPerDay: 2}
	if b := newServer(spend, g).admitRun(ctx, "local"); b == nil || b.Reason != llm.ReasonWorkspaceRuns {
		t.Fatalf("AI run past budget = %v, want workspace_run_budget", b)
	}

	spend = &countSpend{spend: 2}
	g = &llm.Guard{Store: spend, DailyCapUSD: 1}
	if b := newServer(spend, g).admitRun(ctx, "local"); b == nil || b.Reason != llm.ReasonDailyCap {
		t.Fatalf("AI run over cap = %v, want daily_spend_cap", b)
	}

	spend = &countSpend{}
	g = &llm.Guard{Store: spend, RunsPerDay: 2}
	if b := newServer(spend, g).admitRun(ctx, "local"); b != nil {
		t.Fatalf("AI run within budget refused: %s", b.Reason)
	}
	if spend.starts != 1 {
		t.Fatalf("admitted AI run charged %d starts, want 1", spend.starts)
	}
}
