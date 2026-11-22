package store

// KLOX-109 G2 store tests: vector round-trip, the cached publication
// profile and the FTS fallback rank. Skips without DATABASE_URL.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/embed"
)

func TestParseVector(t *testing.T) {
	if v := ParseVector("[1,0.5,-2e-1]"); len(v) != 3 || v[1] != 0.5 || v[2] != -0.2 {
		t.Fatalf("ParseVector = %v", v)
	}
	if ParseVector("") != nil || ParseVector("[a,b]") != nil || ParseVector("[]") != nil {
		t.Fatal("empty or malformed literals must parse to nil")
	}
}

func TestStage1Persistence(t *testing.T) {
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
	nonce := fmt.Sprintf("s1-%d", time.Now().UnixNano())
	seed := func(ext, title, body string) int64 {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind, published_at)
			 VALUES ($1,$2,$3,$4,$5,$6,'article',$7) RETURNING id`,
			db.ws, srcs[0].ID, nonce+ext, "https://x.example/"+nonce+ext, title, body,
			time.Now().UTC().Format(time.RFC3339)).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", ext, err)
		}
		return id
	}
	fin := seed("a", "Banco Central amplia regras do Pix", "O Pix ganha novas regras de segurança para bancos e fintechs.")
	srv := seed("b", "Servidor de arquivos em um único binário", "Um servidor de arquivos para toda a rede.")

	// FTS: only the on-theme item matches an OR query of theme terms.
	got, err := db.FTSRank(ctx, []int64{fin, srv}, []string{"pix", "fintech", "banco"})
	if err != nil {
		t.Fatalf("FTSRank: %v", err)
	}
	if got[fin] <= 0 || got[srv] != 0 {
		t.Fatalf("FTS rank = %v, want only the fintech item", got)
	}
	if m, _ := db.FTSRank(ctx, []int64{fin}, nil); len(m) != 0 {
		t.Fatalf("no terms must rank nothing: %v", m)
	}

	// Vectors round-trip through the pool's writer and the DB reader.
	vec := make([]float32, 512)
	vec[3] = 1
	if err := pool.SaveItemEmbeddings(ctx, "voyage-4-lite", 512, []embed.Vector{{ItemID: fin, Literal: embed.Literal(vec)}}); err != nil {
		t.Fatalf("SaveItemEmbeddings: %v", err)
	}
	vs, err := db.ItemVectors(ctx, "voyage-4-lite", []int64{fin, srv})
	if err != nil || len(vs) != 1 || len(vs[fin]) != 512 || vs[fin][3] != 1 {
		t.Fatalf("ItemVectors = %d rows, %v", len(vs), err)
	}
	if other, _ := db.ItemVectors(ctx, "another-model", []int64{fin}); len(other) != 0 {
		t.Fatal("vectors of another model must not load")
	}

	// Profile: missing, saved, replaced on a new hash.
	if _, err := db.GetPublicationProfile(ctx, pubID); err == nil {
		t.Fatal("no profile yet: want sql.ErrNoRows")
	}
	if err := db.SavePublicationProfile(ctx, pubID, embed.Literal(vec), "", "h1"); err != nil {
		t.Fatalf("SavePublicationProfile: %v", err)
	}
	p, err := db.GetPublicationProfile(ctx, pubID)
	if err != nil || p.ProfileHash != "h1" || len(p.ThemeVec) != 512 || p.SkipVec != nil {
		t.Fatalf("profile = %+v, %v", p, err)
	}
	if err := db.SavePublicationProfile(ctx, pubID, embed.Literal(vec), embed.Literal(vec), "h2"); err != nil {
		t.Fatalf("SavePublicationProfile 2: %v", err)
	}
	if p, _ = db.GetPublicationProfile(ctx, pubID); p.ProfileHash != "h2" || len(p.SkipVec) != 512 {
		t.Fatalf("profile after update = %+v", p)
	}
}

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

// TestApplyFeedbackIncrementalMeans: accept/reject fold the story
// vector into the running means and counts; undo_reject reverses it.
func TestApplyFeedbackIncrementalMeans(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, _ := db.Sources(ctx, pubID)
	nonce := fmt.Sprintf("fb-%d", time.Now().UnixNano())
	var itemID, storyID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind, published_at)
		 VALUES ($1,$2,$3,$4,'t','b','article',$5) RETURNING id`,
		db.ws, srcs[0].ID, nonce, "https://x.example/"+nonce, time.Now().UTC().Format(time.RFC3339)).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO stories (workspace_id, publication_id, title) VALUES ($1,$2,'t') RETURNING id`,
		db.ws, pubID).Scan(&storyID); err != nil {
		t.Skipf("stories insert shape differs: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO story_items (story_id, item_id) VALUES ($1,$2)`, storyID, itemID); err != nil {
		t.Fatal(err)
	}
	vec := make([]float32, 512)
	vec[0] = 1
	if _, err := db.ExecContext(ctx,
		`INSERT INTO item_embeddings (item_id, model, dims, embedding) VALUES ($1,'m',512,$2)`,
		itemID, embed.Literal(vec)); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyFeedback(ctx, pubID, storyID, "m", FeedbackReject); err != nil {
		t.Fatalf("reject: %v", err)
	}
	p, err := db.GetPublicationProfile(ctx, pubID)
	if err != nil || p.NRejected != 1 || len(p.RejectedMean) != 512 || p.RejectedMean[0] < 0.99 {
		t.Fatalf("after reject: %+v err=%v", p, err)
	}
	if err := db.ApplyFeedback(ctx, pubID, storyID, "m", FeedbackAccept); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := db.ApplyFeedback(ctx, pubID, storyID, "m", FeedbackUndoReject); err != nil {
		t.Fatalf("undo: %v", err)
	}
	p, _ = db.GetPublicationProfile(ctx, pubID)
	if p.NAccepted != 1 || p.NRejected != 0 || len(p.RejectedMean) != 0 {
		t.Fatalf("after accept+undo: acc=%d rej=%d rejMean=%d", p.NAccepted, p.NRejected, len(p.RejectedMean))
	}
	// unknown model / no vectors: silent no-op
	if err := db.ApplyFeedback(ctx, pubID, storyID, "other", FeedbackAccept); err != nil {
		t.Fatalf("no-vector feedback must be a no-op: %v", err)
	}
}
