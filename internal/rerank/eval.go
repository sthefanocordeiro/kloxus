package rerank

import (
	"fmt"
	"io"
	"sort"
)

// EvalPub is one frozen evaluation publication: the hand labels plus
// the ranked ids each strategy produced when the fixture was recorded
// (fts = FTS fallback, emb = embeddings only, emb_rerank = embeddings +
// reranker). CI replays it - no paid calls.
type EvalPub struct {
	Name      string              `json:"name"`
	Synthetic bool                `json:"synthetic"` // true until the Voyage recording replaces it
	Relevant  []string            `json:"relevant"`  // ids labelled relevant (of the 60 labelled items)
	Rankings  map[string][]string `json:"rankings"`  // "fts" | "emb" | "emb_rerank"
	// MustNotTop20 lists ids (e.g. the file-server story) that must stay
	// out of the top 20 of the emb and emb_rerank rankings; Promos must
	// not appear in any ranking at all.
	MustNotTop20 []string `json:"must_not_top20"`
	Promos       []string `json:"promos"`
}

// Row is one table line.
type Row struct {
	Strategy string
	P10      float64
	NDCG10   float64
}

var strategyOrder = []string{"fts", "emb", "emb_rerank"}

// Evaluate computes precision@10 and nDCG@10 for each strategy.
func Evaluate(p EvalPub) []Row {
	rel := map[string]bool{}
	for _, id := range p.Relevant {
		rel[id] = true
	}
	var rows []Row
	for _, s := range strategyOrder {
		r, ok := p.Rankings[s]
		if !ok {
			continue
		}
		rows = append(rows, Row{s, PrecisionAtK(r, rel, 10), NDCGAtK(r, rel, 10)})
	}
	return rows
}

// WriteTable prints the markdown table the PR carries.
func WriteTable(w io.Writer, pubs []EvalPub) {
	fmt.Fprintln(w, "| publication | strategy | precision@10 | nDCG@10 |")
	fmt.Fprintln(w, "|---|---|---|---|")
	names := map[string]string{"fts": "(a) FTS", "emb": "(b) embeddings", "emb_rerank": "(c) embeddings + reranker"}
	sort.SliceStable(pubs, func(i, j int) bool { return pubs[i].Name < pubs[j].Name })
	for _, p := range pubs {
		label := p.Name
		if p.Synthetic {
			label += " (synthetic)"
		}
		for _, r := range Evaluate(p) {
			fmt.Fprintf(w, "| %s | %s | %.2f | %.3f |\n", label, names[r.Strategy], r.P10, r.NDCG10)
		}
	}
}
