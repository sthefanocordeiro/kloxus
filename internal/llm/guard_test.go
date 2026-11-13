package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// memSpend is an in-memory SpendStore for guard tests. nowStr is the
// stored timestamp new rows get — default "2999" so rows always fall
// inside any real 'since' window unless a test overrides it.
type memSpend struct {
	mu       sync.Mutex
	nowStr   string
	runs     []struct{ ws, created string }
	calls    float64
	settings map[string]string
}

func (m *memSpend) now() string {
	if m.nowStr == "" {
		return "2999-12-31 23:59:59"
	}
	return m.nowStr
}

func (m *memSpend) LLMRunStart(_ context.Context, ws string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs = append(m.runs, struct{ ws, created string }{ws, m.now()})
	return nil
}

func (m *memSpend) LLMRunUndo(_ context.Context, ws string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.runs) - 1; i >= 0; i-- {
		if m.runs[i].ws == ws {
			m.runs = append(m.runs[:i], m.runs[i+1:]...)
			break
		}
	}
	return nil
}

func (m *memSpend) LLMCall(_ context.Context, ws, model string, in, out int, cost float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls += cost
	return nil
}

func (m *memSpend) LLMRunsSince(_ context.Context, ws, since string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.runs {
		if r.ws == ws && r.created >= since {
			n++
		}
	}
	return n, nil
}

func (m *memSpend) LLMSpendSince(_ context.Context, since string) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls, nil
}

func (m *memSpend) AppSetting(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.settings[key]
	if !ok {
		return "", errors.New("no rows")
	}
	return v, nil
}

func (m *memSpend) SetAppSetting(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings == nil {
		m.settings = map[string]string{}
	}
	m.settings[key] = value
	return nil
}

type stubProvider struct {
	resp Response
	err  error
}

func (s stubProvider) Complete(context.Context, Request) (Response, error) {
	return s.resp, s.err
}

func fakeClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func mustBlocked(t *testing.T, err error, want BlockedReason) *BlockedError {
	t.Helper()
	b, ok := AsBlocked(err)
	if !ok || b.Reason != want {
		t.Fatalf("err=%v, want BlockedError reason %q", err, want)
	}
	return b
}

func TestGuardWorkspaceRunBudget(t *testing.T) {
	g := &Guard{Store: &memSpend{}, RunsPerDay: 2, Now: fakeClock(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))}
	ctx := context.Background()
	if err := g.AdmitRun(ctx, "ws1"); err != nil {
		t.Fatalf("admit 1: %v", err)
	}
	if err := g.AdmitRun(ctx, "ws1"); err != nil {
		t.Fatalf("admit 2: %v", err)
	}
	mustBlocked(t, g.AdmitRun(ctx, "ws1"), ReasonWorkspaceRuns)
	// a different workspace keeps its own budget
	if err := g.AdmitRun(ctx, "ws2"); err != nil {
		t.Fatalf("ws2 admit: %v", err)
	}
	// A refunded start frees the budget again (nothing-new runs make no
	// model calls and must not count — D36).
	g.UndoRunStart(ctx, "ws1")
	if err := g.AdmitRun(ctx, "ws1"); err != nil {
		t.Fatalf("admit after refund: %v", err)
	}
}

func TestGuardDayResetsWithClock(t *testing.T) {
	day := time.Date(2026, 11, 13, 23, 0, 0, 0, time.UTC)
	ms := &memSpend{nowStr: "2026-11-13 23:00:00"}
	g := &Guard{Store: ms, RunsPerDay: 1, Now: func() time.Time { return day }}
	ctx := context.Background()
	if err := g.AdmitRun(ctx, "ws1"); err != nil {
		t.Fatalf("admit: %v", err)
	}
	mustBlocked(t, g.AdmitRun(ctx, "ws1"), ReasonWorkspaceRuns)
	// Next UTC day: the stored row is yesterday, the window resets.
	ms.nowStr = "2026-11-14 01:00:00"
	day = day.Add(2 * time.Hour) // 2026-11-14
	if err := g.AdmitRun(ctx, "ws1"); err != nil {
		t.Fatalf("next-day admit: %v", err)
	}
}

func TestGuardDailyCap(t *testing.T) {
	ms := &memSpend{calls: 1.20}
	g := &Guard{Store: ms, DailyCapUSD: 1.00, Now: fakeClock(time.Now())}
	mustBlocked(t, g.AdmitRun(context.Background(), "ws1"), ReasonDailyCap)
}

func TestGuardKillSwitchImmediate(t *testing.T) {
	g := &Guard{Store: &memSpend{}, RunsPerDay: 9, Now: fakeClock(time.Now())}
	ctx := context.Background()
	if err := g.AdmitRun(ctx, "ws1"); err != nil {
		t.Fatalf("admit: %v", err)
	}
	// Flipping the switch takes effect on the very next call — no restart.
	if err := g.SetKill(ctx, true); err != nil {
		t.Fatalf("SetKill: %v", err)
	}
	mustBlocked(t, g.AdmitRun(ctx, "ws1"), ReasonKillSwitch)
	_, err := g.Complete(WithRunScope(ctx, "ws1"), Request{Model: "m"})
	mustBlocked(t, err, ReasonKillSwitch)
	if err := g.SetKill(ctx, false); err != nil {
		t.Fatalf("SetKill off: %v", err)
	}
	if err := g.AdmitRun(ctx, "ws1"); err != nil {
		t.Fatalf("admit after resume: %v", err)
	}
}

func TestGuardKillEnv(t *testing.T) {
	t.Setenv("KLOXUS_LLM_KILL", "1")
	g := &Guard{Store: &memSpend{}, Now: fakeClock(time.Now())}
	mustBlocked(t, g.AdmitRun(context.Background(), "ws1"), ReasonKillSwitch)
}

func TestGuardTokenBudgetAndLedger(t *testing.T) {
	ms := &memSpend{}
	g := &Guard{
		Store: ms, TokensPerRun: 100, Now: fakeClock(time.Now()),
		Inner: stubProvider{resp: Response{Text: "{}", InputTokens: 60, OutputTokens: 10}},
	}
	ctx := WithRunScope(context.Background(), "ws1")
	if _, err := g.Complete(ctx, Request{Model: "claude-haiku-5-5"}); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if ms.calls <= 0 {
		t.Fatal("call was not ledgered")
	}
	if _, err := g.Complete(ctx, Request{Model: "claude-haiku-5-5"}); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	_, err := g.Complete(ctx, Request{Model: "claude-haiku-5-5"})
	mustBlocked(t, err, ReasonWorkspaceToken)
	// WithWorkspace has no per-run budget — only kill/cap apply.
	if _, err := g.Complete(WithWorkspace(context.Background(), "ws1"), Request{Model: "claude-haiku-5-5"}); err != nil {
		t.Fatalf("workspace-scoped call: %v", err)
	}
}

func TestGuardInnerErrorPropagates(t *testing.T) {
	boom := errors.New("provider down")
	g := &Guard{Store: &memSpend{}, Inner: stubProvider{err: boom}}
	_, err := g.Complete(WithRunScope(context.Background(), "ws1"), Request{})
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v, want provider error", err)
	}
}

func TestGuardMessagesAreClean(t *testing.T) {
	for _, r := range []BlockedReason{ReasonKillSwitch, ReasonWorkspaceRuns, ReasonWorkspaceToken, ReasonDailyCap} {
		b := &BlockedError{Reason: r, Message: fmt.Sprintf("msg for %s", r)}
		if b.Error() == "" {
			t.Fatal("empty message")
		}
	}
}
