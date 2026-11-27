package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRerankScoreCache(t *testing.T) {
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
	nonce := fmt.Sprintf("rr-%d", time.Now().UnixNano())
	var id int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind, published_at)
		 VALUES ($1,$2,$3,$4,'t','b','article',$5) RETURNING id`,
		db.ws, srcs[0].ID, nonce, "https://x.example/"+nonce, time.Now().UTC().Format(time.RFC3339)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.SaveRerankScores(ctx, pubID, "h1", "m", map[int64]float64{id: 0.8}, now); err != nil {
		t.Fatalf("SaveRerankScores: %v", err)
	}
	got, err := db.RerankScores(ctx, pubID, "h1", "m", []int64{id}, now.Add(time.Hour))
	if err != nil || got[id] != float64(float32(0.8)) {
		t.Fatalf("fresh cache = %v, %v", got, err)
	}
	if m, _ := db.RerankScores(ctx, pubID, "h2", "m", []int64{id}, now); len(m) != 0 {
		t.Fatal("a changed profile_hash must miss")
	}
	if m, _ := db.RerankScores(ctx, pubID, "h1", "other", []int64{id}, now); len(m) != 0 {
		t.Fatal("a different model must miss")
	}
	if m, _ := db.RerankScores(ctx, pubID, "h1", "m", []int64{id}, now.Add(25*time.Hour)); len(m) != 0 {
		t.Fatal("scores older than 24h must miss")
	}
}
