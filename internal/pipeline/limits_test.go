package pipeline

// KLOX-44 spend limits, tested without a database: the prompt-size cap
// refuses before the provider is ever called, and entry sources are
// capped at 3 chosen by distinct outlet then recency.
import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func TestCallRefusesOversizePrompt(t *testing.T) {
	var calls int
	r := &Runner{
		LLM: llm.Func(func(context.Context, llm.Request) (llm.Response, error) {
			calls++
			return llm.Response{Text: "[]"}, nil
		}),
	}
	var out []struct {
		Ref int `json:"ref"`
	}
	// dossier's 30k cap trips on a >90k-char prompt; the provider must
	// never see it, and the refusal is a run error, not a retry.
	_, err := r.call(context.Background(), "sonnet", "dossier", map[string]any{
		"Publication": "p",
		"Language":    "pt-BR",
		"Items": []map[string]any{{
			"Ref": 1, "Title": "t",
			"Sources": []map[string]any{{"Ref": 1, "URL": "u", "Text": strings.Repeat("x", 100000)}},
		}},
	}, &out)
	var p *llm.PromptTooLargeError
	if !errors.As(err, &p) {
		t.Fatalf("err=%v, want *PromptTooLargeError", err)
	}
	if calls != 0 {
		t.Fatalf("provider was called %d time(s) for an over-cap prompt", calls)
	}
}

// TestCallSetsMaxTokens: every model call carries the step's output
// ceiling (KLOX-44).
func TestCallSetsMaxTokens(t *testing.T) {
	got := map[string]int{}
	r := &Runner{
		LLM: llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
			got[req.Template] = req.MaxTokens
			return llm.Response{Text: "[]"}, nil
		}),
	}
	for tmpl, want := range map[string]int{
		"triage": 8192, "storylist": 8192, "citecheck": 8192,
		"dossier": 32768, "deepdive": 32768, "draft": 32768,
	} {
		var out []struct{}
		if _, err := r.call(context.Background(), "m", tmpl, map[string]any{}, &out); err != nil {
			t.Fatalf("%s: %v", tmpl, err)
		}
		if got[tmpl] != want {
			t.Fatalf("%s max_tokens = %d, want %d", tmpl, got[tmpl], want)
		}
	}
}

func TestCapEntrySourcesSmall(t *testing.T) {
	items := []store.Item{
		{ID: 1, SourceName: "A"},
		{ID: 2, SourceName: "B"},
	}
	got := capEntrySources(items)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("small input must pass through unchanged: %+v", got)
	}
}

// TestCapEntrySourcesDistinctOutlet: with more than 3 evidence items,
// the cap prefers the newest item of each unseen outlet, then fills by
// recency; picks keep their original order.
func TestCapEntrySourcesDistinctOutlet(t *testing.T) {
	items := []store.Item{
		{ID: 1, SourceName: "A", PublishedAt: "2026-11-10"},
		{ID: 2, SourceName: "A", PublishedAt: "2026-11-14"},
		{ID: 3, SourceName: "B", PublishedAt: "2026-11-13"},
		{ID: 4, SourceName: "A", PublishedAt: "2026-11-12"},
		{ID: 5, SourceName: "C", PublishedAt: "2026-11-11"},
	}
	got := capEntrySources(items)
	if len(got) != 3 {
		t.Fatalf("got %d sources, want 3", len(got))
	}
	// newest per outlet first (A→id2, B→id3, C→id5), emitted in
	// original member order: [2, 3, 5].
	want := []int64{2, 3, 5}
	for i, w := range want {
		if got[i].ID != w {
			t.Fatalf("pick %d = id %d, want %d", i, got[i].ID, w)
		}
	}
}

// TestCapEntrySourcesFillsByRecency: fewer distinct outlets than slots
// fills the rest with the next-most-recent items regardless of outlet.
func TestCapEntrySourcesFillsByRecency(t *testing.T) {
	items := []store.Item{
		{ID: 1, SourceName: "A", PublishedAt: "2026-11-10"},
		{ID: 2, SourceName: "A", PublishedAt: "2026-11-14"},
		{ID: 3, SourceName: "B", PublishedAt: "2026-11-13"},
		{ID: 4, SourceName: "A", PublishedAt: "2026-11-12"},
		{ID: 5, SourceName: "A", PublishedAt: "2026-11-11"},
	}
	got := capEntrySources(items)
	if len(got) != 3 {
		t.Fatalf("got %d sources, want 3", len(got))
	}
	// distinct first: A→id2 (11-14), B→id3 (11-13); third slot goes to
	// the next most recent, id4 (11-12): [2, 3, 4].
	want := []int64{2, 3, 4}
	for i, w := range want {
		if got[i].ID != w {
			t.Fatalf("pick %d = id %d, want %d", i, got[i].ID, w)
		}
	}
}
