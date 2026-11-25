package pipeline

// KLOX-95 tests: the enrichment sweep's cap-exhaustion path — a
// guard-blocked batch stops the sweep early instead of burning calls
// on every remaining batch, and the pending items stay un-enriched for
// the next pass. Deterministic: the provider is a stub, never a real
// call. Skips without DATABASE_URL.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

func TestEnricherSweepBlockedStops(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Slug: "enrich-blocked-pub", Name: "P",
		Sections: []config.Section{{Title: "Lead", Items: 1}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/a.xml"}},
	})
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, _ := db.Sources(ctx, pubID)
	// One more than a full batch → two batches pending; the guard
	// refusal must stop after the first call.
	nonce := fmt.Sprintf("eb-%d", time.Now().UnixNano())
	for i := 0; i < enrichBatchSize+1; i++ {
		ext := fmt.Sprintf("%s-%02d", nonce, i)
		if _, err := db.ExecContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind)
			 VALUES ($1,$2,$3,$4,$5,'body','article')`,
			db.Workspace(), srcs[0].ID, ext, "https://x.example/"+ext, "Article "+ext); err != nil {
			t.Fatalf("insert %s: %v", ext, err)
		}
	}

	var calls int
	enr := &Enricher{
		Pool: pool, Model: "claude-haiku-5-5",
		LLM: llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
			calls++
			return llm.Response{}, &llm.BlockedError{
				Reason:  llm.ReasonDailyCap,
				Message: "daily USD cap reached",
			}
		}),
	}
	n, err := enr.Sweep(ctx, enrichBatchSize+1)
	if err != nil {
		t.Fatalf("blocked sweep must degrade, not error: %v", err)
	}
	if n != 0 {
		t.Fatalf("blocked sweep enriched %d items", n)
	}
	if calls != 1 {
		t.Fatalf("blocked provider got %d calls, want 1 — later batches fail identically", calls)
	}
}
