package pipeline

// Evaluation export (KLOX-110 checkpoint, spec section 9): ranks a
// publication's stored stories three ways without touching the rerank
// cache or any story row, so a one-off recording can freeze the
// fixture. Read-only apart from the stage-1 profile embedding the
// pipeline would have saved anyway.

import (
	"context"
	"sort"

	"github.com/sthefanocordeiro/kloxus/internal/embed"
	"github.com/sthefanocordeiro/kloxus/internal/rank"
	"github.com/sthefanocordeiro/kloxus/internal/rerank"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// EvalRankings holds, per strategy ("fts", "emb", "emb_rerank"), the
// story ids best-first (visible stories only), plus per-story stage-1
// relevance and rerank score for inspection.
type EvalRankings struct {
	Orders      map[string][]int64
	EmbRel      map[int64]float64
	RerankScore map[int64]float64
	WithVector  int // stories whose stage-1 used an embedding
}

func orderOf(stories []store.Story, s1 *stage1Result) []int64 {
	idx := make([]int, 0, len(stories))
	for i := range stories {
		if s1.Hidden[i] == rank.HiddenNone {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return s1.Scores[idx[a]] > s1.Scores[idx[b]] })
	out := make([]int64, len(idx))
	for n, i := range idx {
		out[n] = stories[i].ID
	}
	return out
}

// EvalRank runs (a) FTS, (b) embeddings and (c) embeddings + reranker.
// emb and rr may be nil: the matching strategies are then skipped.
func EvalRank(ctx context.Context, db *store.DB, pub *store.Publication, stories []store.Story,
	emb embed.Embedder, rr rerank.Reranker) (*EvalRankings, error) {

	out := &EvalRankings{Orders: map[string][]int64{}, EmbRel: map[int64]float64{}, RerankScore: map[int64]float64{}}
	reasons := make([]string, len(stories))
	enrich := map[int64]*store.ItemEnrichment{}

	fr := &Runner{DB: db, Embedder: embed.Off{}}
	f := fr.rankStage1(ctx, pub, stories, reasons, enrich)
	out.Orders["fts"] = orderOf(stories, &f)

	if emb == nil {
		return out, nil
	}
	er := &Runner{DB: db, Embedder: emb}
	e := er.rankStage1(ctx, pub, stories, reasons, enrich)
	out.Orders["emb"] = orderOf(stories, &e)
	for i := range stories {
		out.EmbRel[stories[i].ID] = e.Rel[i]
	}
	if rr == nil {
		return out, nil
	}

	var order []int
	for i := range stories {
		if e.Hidden[i] == rank.HiddenNone && earliestItem(&stories[i]) != nil {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return e.Scores[order[a]] > e.Scores[order[b]] })
	k := Stage2.K
	if k <= 0 {
		k = rerank.DefaultK
	}
	if len(order) > k {
		order = order[:k]
	}
	docs := make([]string, len(order))
	for n, i := range order {
		docs[n] = rerank.Doc(stories[i].Title, earliestItem(&stories[i]).ContentText)
	}
	res, err := rr.Rerank(ctx, rerankQuery(pub), docs, 0)
	if err != nil {
		return out, err
	}
	scores := map[int]float64{}
	for _, sc := range res {
		if sc.Index >= 0 && sc.Index < len(order) {
			scores[order[sc.Index]] = sc.Score
			out.RerankScore[stories[order[sc.Index]].ID] = sc.Score
		}
	}
	applyRerank(Stage1Config, &e, scores, 0)
	out.Orders["emb_rerank"] = orderOf(stories, &e)
	return out, nil
}
