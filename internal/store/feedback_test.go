package store

import (
	"context"
	"fmt"
	"github.com/sthefanocordeiro/kloxus/internal/embed"
	"testing"
	"time"
)

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
	if _, err := db.ExecContext(ctx, `INSERT INTO story_items (workspace_id, story_id, item_id) VALUES ($1,$2,$3)`, db.ws, storyID, itemID); err != nil {
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
