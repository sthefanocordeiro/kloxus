package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/rank"
	"github.com/sthefanocordeiro/kloxus/internal/rerank"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

type fakeReranker struct {
	calls int
	docs  int
	err   error
	score func(i int) float64
}

func (f *fakeReranker) Model() string { return "fake-rerank" }
func (f *fakeReranker) Rerank(_ context.Context, _ string, docs []string, _ int) ([]rerank.Scored, error) {
	f.calls++
	f.docs += len(docs)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]rerank.Scored, len(docs))
	for i := range docs {
		out[i] = rerank.Scored{Index: i, Score: f.score(i)}
	}
	return out, nil
}

// TestRerankStageRunPathAndSilentFailure drives rerankStage end to end
// on a disposable database: cache miss -> reranker call -> scores
// applied and cached; a second pass hits the cache (no call); a failing
// reranker leaves the stage-1 order untouched (silent to the creator).
func TestRerankStageRunPathAndSilentFailure(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Slug: "rerank-pub", Name: "P", Topic: "fintech",
		Sections: []config.Section{{Title: "Lead", Items: 1}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/a.xml"}},
	})
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, fmt.Sprint(pubID))
	if err != nil {
		t.Fatal(err)
	}
	srcs, _ := db.Sources(ctx, pubID)
	nonce := fmt.Sprintf("-%d", time.Now().UnixNano())
	var stories []store.Story
	for n := 0; n < 3; n++ {
		ext := fmt.Sprintf("rs-%d%s", n, nonce)
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind)
			 VALUES ($1,$2,$3,$4,$5,'body','signal') RETURNING id`,
			db.Workspace(), srcs[0].ID, ext, "https://x.example/"+ext, "Story "+ext).Scan(&id); err != nil {
			t.Fatalf("seed: %v", err)
		}
		stories = append(stories, store.Story{ID: int64(n + 1), Title: "Story " + ext,
			Items: []store.Item{{ID: id, Title: "Story " + ext, ContentText: "body", PublishedAt: "2026-11-20 10:00:00"}}})
	}
	fresh := func() *stage1Result {
		return &stage1Result{
			Scores:  []float64{5, 4, 3},
			Rel:     []float64{0.5, 0.5, 0.5},
			Reasons: []string{"", "", ""},
			Hidden:  []string{rank.HiddenNone, rank.HiddenNone, rank.HiddenNone},
		}
	}

	// success: story 2 (index 2) scores best and must overtake story 0.
	rr := &fakeReranker{score: func(i int) float64 { return []float64{0.1, 0.2, 0.9}[i] }}
	r := &Runner{DB: db, Reranker: rr}
	s1 := fresh()
	r.rerankStage(ctx, pub, stories, s1)
	if rr.calls != 1 || rr.docs != 3 {
		t.Fatalf("calls=%d docs=%d, want 1 call with 3 docs", rr.calls, rr.docs)
	}
	if !(s1.Scores[2] > s1.Scores[0]) {
		t.Errorf("reranked best story did not overtake: %v", s1.Scores)
	}

	// second run: all scores cached, no paid call.
	s1 = fresh()
	r.rerankStage(ctx, pub, stories, s1)
	if rr.calls != 1 {
		t.Errorf("cache hit must not call the reranker again, calls=%d", rr.calls)
	}
	if !(s1.Scores[2] > s1.Scores[0]) {
		t.Errorf("cached scores not applied: %v", s1.Scores)
	}

	// failure on a cold cache (new model hash via a new publication
	// profile is simulated with a different reranker error path): the
	// stage-1 order must stand.
	pub2 := *pub
	pub2.Topic = "outro tema completamente diferente"
	bad := &fakeReranker{err: errors.New("429 rate limited")}
	r2 := &Runner{DB: db, Reranker: bad}
	s1 = fresh()
	r2.rerankStage(ctx, &pub2, stories, s1)
	if bad.calls != 1 {
		t.Fatalf("failing reranker calls=%d, want 1", bad.calls)
	}
	want := fresh()
	for i := range want.Scores {
		if s1.Scores[i] != want.Scores[i] || s1.Rel[i] != want.Rel[i] {
			t.Errorf("failure changed stage-1 result: %v vs %v", s1.Scores, want.Scores)
			break
		}
	}
}
