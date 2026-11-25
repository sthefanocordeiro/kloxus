package pipeline

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/rank"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func mkS1(scores, rel []float64, hidden []string) *stage1Result {
	n := len(scores)
	return &stage1Result{Scores: scores, Rel: rel, Hidden: hidden, Reasons: make([]string, n)}
}

func TestApplyRerankBlendLiftAndFloor(t *testing.T) {
	cfg := rank.DefaultConfig()
	// 0,1 are reranked; 2 is a high-scoring non-reranked story; 3 is hidden.
	s1 := mkS1([]float64{5, 4, 6, 9}, []float64{0.5, 0.4, 0.6, 0.9}, []string{"", "", "", rank.HiddenEnglish})
	applyRerank(cfg, s1, map[int]float64{0: 0.9, 1: 0.1}, 0.2)

	if want := 0.7*0.9 + 0.3*0.5; s1.Rel[0] < want-1e-9 || s1.Rel[0] > want+1e-9 {
		t.Fatalf("rel_final = %v, want %v", s1.Rel[0], want)
	}
	for i := 0; i < 2; i++ {
		if s1.Scores[i] <= s1.Scores[2] {
			t.Fatalf("reranked story %d (%.2f) must sit above the non-reranked one (%.2f)", i, s1.Scores[i], s1.Scores[2])
		}
	}
	if s1.Scores[0] <= s1.Scores[1] {
		t.Fatal("rerank order must survive the lift")
	}
	if s1.Hidden[1] != rank.HiddenOffTopic || s1.Hidden[0] != "" {
		t.Fatalf("floor 0.2 must hide only the 0.1 story: %v", s1.Hidden)
	}
	if !strings.Contains(s1.Reasons[0], "reranker 0.90") {
		t.Fatalf("reason = %q", s1.Reasons[0])
	}
	if s1.Scores[3] != 9 || s1.Scores[2] != 6 {
		t.Fatal("non-reranked stories keep their stage-1 scores")
	}
}

func TestApplyRerankNoScoresIsNoop(t *testing.T) {
	s1 := mkS1([]float64{5}, []float64{0.5}, []string{""})
	applyRerank(rank.DefaultConfig(), s1, nil, 0.2)
	if s1.Scores[0] != 5 || s1.Rel[0] != 0.5 {
		t.Fatal("no rerank scores must leave stage 1 untouched")
	}
}

func TestRerankQueryCarriesEditorialLine(t *testing.T) {
	pub := &store.Publication{Topic: "fintech e Pix", EditorialLine: "Cubra regulação bancária. Pule games e promoções."}
	q := rerankQuery(pub)
	if !strings.Contains(q, "fintech e Pix") || !strings.Contains(strings.ToLower(q), "ignore:") || !strings.Contains(strings.ToLower(q), "games") {
		t.Fatalf("query = %q", q)
	}
}

func TestEarliestItem(t *testing.T) {
	s := &store.Story{Items: []store.Item{
		{ID: 1, PublishedAt: "2026-11-20T10:00:00Z"},
		{ID: 2, PublishedAt: "2026-11-18T10:00:00Z"},
		{ID: 3, PublishedAt: ""},
	}}
	if it := earliestItem(s); it == nil || it.ID != 2 {
		t.Fatalf("earliest = %+v", it)
	}
	if earliestItem(&store.Story{}) != nil {
		t.Fatal("empty story has no earliest item")
	}
}
