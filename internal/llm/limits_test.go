package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Fatalf("empty: %d", got)
	}
	if got := EstimateTokens(strings.Repeat("a", 90)); got != 30 {
		t.Fatalf("90 chars: got %d, want 30", got)
	}
	// Runes, not bytes: accented pt-BR text counts once per character.
	if got := EstimateTokens(strings.Repeat("ç", 30)); got != 10 {
		t.Fatalf("30 runes: got %d, want 10", got)
	}
}

func TestCheckSizeRefuses(t *testing.T) {
	req := Request{Template: "dossier", User: strings.Repeat("x", 30*3*1000+3)} // ~30.001k
	err := CheckSize(req)
	var p *PromptTooLargeError
	if !errors.As(err, &p) {
		t.Fatalf("err=%v, want *PromptTooLargeError", err)
	}
	if p.Cap != 30000 || p.Template != "dossier" {
		t.Fatalf("PromptTooLargeError=%+v", p)
	}
}

func TestCheckSizeAllows(t *testing.T) {
	for _, tmpl := range []string{"triage", "storylist", "dossier", "deepdive", "draft", "citecheck"} {
		if err := CheckSize(Request{Template: tmpl, User: "small"}); err != nil {
			t.Fatalf("%s: %v", tmpl, err)
		}
	}
	// Unknown templates fall under the default cap, not off.
	req := Request{Template: "future-step", User: strings.Repeat("x", 40*3*1000)}
	if err := CheckSize(req); err == nil {
		t.Fatal("unknown template with huge input should be refused")
	}
}

// TestGuardSizeRefusalBeforeProvider: the guard refuses an over-cap
// request without calling Inner (KLOX-44).
func TestGuardSizeRefusalBeforeProvider(t *testing.T) {
	var called bool
	g := &Guard{
		Store: &memSpend{},
		Inner: Func(func(context.Context, Request) (Response, error) {
			called = true
			return okResp(), nil
		}),
	}
	big := Request{Template: "dossier", User: strings.Repeat("x", 30*3*1000+3)}
	_, err := g.Complete(WithRunScope(context.Background(), "ws1"), big)
	var p *PromptTooLargeError
	if !errors.As(err, &p) {
		t.Fatalf("err=%v, want *PromptTooLargeError", err)
	}
	if called {
		t.Fatal("provider was called for an over-cap prompt")
	}
}

// TestGuardRunCapUSD: the per-run dollar cap stops calls once the
// run's recorded cost reaches it — a deterministic stop (KLOX-44).
func TestGuardRunCapUSD(t *testing.T) {
	ms := &memSpend{}
	g := &Guard{
		Store: ms, RunCapUSD: 0.10, Now: fakeClock(time.Now()),
		Inner: stubProvider{resp: Response{Text: "{}", InputTokens: 200000, OutputTokens: 20000}},
	}
	// haiku pricing ($0.10/$0.50 per MTok): 200k in + 20k out = $0.03
	// per call. Calls pass while recorded cost < $0.10; once the
	// ledger reaches $0.12 the next call is refused.
	ctx := WithRunScope(context.Background(), "ws1")
	for i := 0; i < 4; i++ {
		if _, err := g.Complete(ctx, Request{Model: "claude-haiku-5-5"}); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	mustBlocked(t, func() error {
		_, err := g.Complete(ctx, Request{Model: "claude-haiku-5-5"})
		return err
	}(), ReasonRunSpend)
	// WithWorkspace has no per-run scope — only kill/cap apply.
	if _, err := g.Complete(WithWorkspace(context.Background(), "ws1"), Request{Model: "claude-haiku-5-5"}); err != nil {
		t.Fatalf("workspace-scoped call: %v", err)
	}
}
