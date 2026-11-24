package coverage

import (
	"context"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/ingest"
)

type stubFetcher struct{ items []ingest.DJItem }

func (s stubFetcher) Poll(context.Context, int) ([]ingest.DJItem, error) {
	return s.items, nil
}

// TestRunScoresNiches: a mixed snapshot hits each niche's topical
// predicate, topical hits land in samples, and the union never exceeds
// the item count.
func TestRunScoresNiches(t *testing.T) {
	f := stubFetcher{items: []ingest.DJItem{
		{ID: "1", Slug: "pix", Title: "Pix bate recorde no BC",
			Description: "Banco Central divulga novo marco de transações do Pix.",
			Categories:  []string{"finance"}, PublishedAt: "2026-11-20T10:00:00Z"},
		{ID: "2", Slug: "anatel", Title: "Anatel publica nova regulamentação",
			Description: "A agência reguladora abriu consulta pública do decreto.",
			Categories:  []string{"politics"}, PublishedAt: "2026-11-20T09:00:00Z"},
		{ID: "3", Slug: "festival", Title: "Festival de cinema abre temporada",
			Description: "Mostra reúne filmes nacionais e shows de música.",
			Categories:  []string{"entertainment"}, PublishedAt: "2026-11-20T08:00:00Z"},
		{ID: "4", Slug: "sport", Title: "Brasil vence amistoso",
			Description: "Seleção jogou bem e marcou dois gols.",
			Categories:  []string{"sports"}, PublishedAt: "2026-11-20T07:00:00Z"},
	}}
	rep, err := Run(context.Background(), f, 2)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Items != 4 {
		t.Fatalf("items=%d", rep.Items)
	}
	row := func(key string) Row {
		for _, r := range rep.Rows {
			if r.Niche.Key == key {
				return r
			}
		}
		t.Fatalf("niche %s missing", key)
		return Row{}
	}
	if row("fintech").Topical == 0 {
		t.Error("fintech: no topical hit")
	}
	if row("regulacao").Topical == 0 {
		t.Error("regulacao: no topical hit")
	}
	if row("criativo").Topical == 0 {
		t.Error("criativo: no topical hit")
	}
	for _, r := range rep.Rows {
		if r.Either > rep.Items {
			t.Fatalf("%s union %d > items %d", r.Niche.Key, r.Either, rep.Items)
		}
		if len(r.Samples) > 5 {
			t.Fatalf("%s samples %d > 5", r.Niche.Key, len(r.Samples))
		}
	}
	if !strings.Contains(rep.Markdown(), "Fintech") {
		t.Fatal("markdown missing fintech label")
	}
}

// TestRunSamplesSorted: samples come out strongest-similarity first.
func TestRunSamplesSorted(t *testing.T) {
	var items []ingest.DJItem
	for _, title := range []string{
		"Regulador publica nova regra do setor financeiro",
		"Banco central decide juros na reunião",
		"Regulamentação muda mercado de pagamentos",
	} {
		items = append(items, ingest.DJItem{
			Title: title, Description: title,
			Categories: []string{"finance"}, PublishedAt: "2026-11-20T10:00:00Z"})
	}
	rep, err := Run(context.Background(), stubFetcher{items: items}, 1)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, r := range rep.Rows {
		for i := 1; i < len(r.Samples); i++ {
			if r.Samples[i].Sim > r.Samples[i-1].Sim {
				t.Fatalf("%s samples unsorted", r.Niche.Key)
			}
		}
	}
}
