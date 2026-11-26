package store

// KLOX-108 G1 store tests: the pending-embedding queue (newest first,
// promo excluded, stored vectors leave the queue, a model change puts
// them back) and the embed/rerank ledger lines. Skips without
// DATABASE_URL like every store test; no Voyage call is involved.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/embed"
)

func TestPendingEmbeddingsQueue(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, err := db.Sources(ctx, pubID)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("Sources: %v", err)
	}
	nonce := fmt.Sprintf("emb-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	seed := func(ext, kind string, age time.Duration) int64 {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind, published_at)
			 VALUES ($1,$2,$3,$4,$5,'corpo do item','`+kind+`',$6) RETURNING id`,
			db.ws, srcs[0].ID, nonce+ext, "https://x.example/"+nonce+ext, "Título "+ext,
			now.Add(-age).Format(time.RFC3339)).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", ext, err)
		}
		return id
	}
	newer := seed("a", "article", time.Hour)
	older := seed("b", "article", 5*24*time.Hour)
	promo := seed("c", "promo", time.Hour)
	stale := seed("d", "article", 30*24*time.Hour) // outside the 7-day window

	since := now.Add(-7 * 24 * time.Hour)
	list := func(model string) []int64 {
		ps, err := pool.PendingEmbeddings(ctx, model, since, 100000)
		if err != nil {
			t.Fatalf("PendingEmbeddings: %v", err)
		}
		var ids []int64
		for _, p := range ps {
			switch p.ID {
			case newer, older, promo, stale:
				ids = append(ids, p.ID)
			}
		}
		return ids
	}
	got := list("voyage-4-lite")
	if len(got) != 2 || got[0] != newer || got[1] != older {
		t.Fatalf("pending = %v, want [newer older] (promo and stale excluded)", got)
	}
	n, chars, err := pool.PendingStats(ctx, "voyage-4-lite", since)
	if err != nil || n < 2 || chars <= 0 {
		t.Fatalf("PendingStats = %d, %d, %v", n, chars, err)
	}

	vec := make([]float32, 512)
	vec[0] = 1
	if err := pool.SaveItemEmbeddings(ctx, "voyage-4-lite", 512, []embed.Vector{{ItemID: newer, Literal: embed.Literal(vec)}}); err != nil {
		t.Fatalf("SaveItemEmbeddings: %v", err)
	}
	if got := list("voyage-4-lite"); len(got) != 1 || got[0] != older {
		t.Fatalf("after save pending = %v, want [older]", got)
	}
	if got := list("other-model"); len(got) != 2 {
		t.Fatalf("a model change must re-queue: %v", got)
	}
}

func TestVoyageLedgerLines(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	since := time.Now().UTC().Add(-time.Second).Format("2006-01-02 15:04:05")
	before, err := pool.VoyageSpendSince(ctx, "rerank", since)
	if err != nil {
		t.Fatalf("VoyageSpendSince: %v", err)
	}
	llmBefore, _ := pool.LLMSpendSince(ctx, since)
	if err := pool.VoyageUsage(ctx, "rerank", "rerank-2.5-lite", 35000, 0.0007); err != nil {
		t.Fatalf("VoyageUsage: %v", err)
	}
	after, _ := pool.VoyageSpendSince(ctx, "rerank", since)
	if d := after - before; d < 0.00069 || d > 0.00071 {
		t.Fatalf("rerank spend delta = %v", d)
	}
	if llmAfter, _ := pool.LLMSpendSince(ctx, since); llmAfter != llmBefore {
		t.Fatal("voyage usage leaked into the LLM daily cap")
	}
}
