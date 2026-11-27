package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

func TestClassifySummaryKind(t *testing.T) {
	cases := map[string]string{
		"ANPD multa empresa por vazamento":        "regulacao",
		"Empresa lança novo app de pagamentos":    "lancamento",
		"Entenda o que muda com o novo Pix":       "analise",
		"Opinião: o futuro dos bancos":            "opiniao",
		"Banco central divulga dados de setembro": "noticia",
	}
	for title, want := range cases {
		if got := classifySummaryKind(title, nil); got != want {
			t.Errorf("%q = %s, want %s", title, got, want)
		}
	}
	if got := classifySummaryKind("x", &store.ItemEnrichment{Kind: "analise"}); got != "analise" {
		t.Errorf("enrichment kind ignored: %s", got)
	}
}

func TestTemplateSummaryNeverUsesSourceText(t *testing.T) {
	it := &store.Item{Title: "SEGREDO-TITULO", ContentText: "SEGREDO-CORPO",
		SourceName: "Folha", PublishedAt: "2026-11-20 10:00:00"}
	s := templateSummary("noticia", it)
	if strings.Contains(s, "SEGREDO") {
		t.Fatalf("summary leaks source text: %q", s)
	}
	if s != "Notícia de Folha, publicada em 20/11." {
		t.Fatalf("summary = %q", s)
	}
	it.SourceName = strings.Repeat("a", 400)
	if n := utf8.RuneCountInString(templateSummary("noticia", it)); n > summaryMaxRunes {
		t.Fatalf("summary %d runes > %d", n, summaryMaxRunes)
	}
}

func TestSummarizeTopCachesGloballyAndSkipsHidden(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()
	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Slug: "sum-pub", Name: "P", Topic: "fintech",
		Sections: []config.Section{{Title: "Lead", Items: 1}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/a.xml"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srcs, _ := db.Sources(ctx, pubID)
	nonce := fmt.Sprintf("-%d", time.Now().UnixNano())
	var stories []store.Story
	n := summaryTopN + 5
	for i := 0; i < n; i++ {
		ext := fmt.Sprintf("sm-%d%s", i, nonce)
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind)
			 VALUES ($1,$2,$3,$4,$5,'body','article') RETURNING id`,
			db.Workspace(), srcs[0].ID, ext, "https://x.example/"+ext, "Titulo "+ext).Scan(&id); err != nil {
			t.Fatal(err)
		}
		stories = append(stories, store.Story{ID: int64(i + 1),
			Items: []store.Item{{ID: id, Title: "Titulo " + ext, SourceName: "Fonte", PublishedAt: "2026-11-20 10:00:00"}}})
	}
	scores := make([]float64, n)
	for i := range scores {
		scores[i] = float64(n - i)
	}
	hidden := make([]bool, n)
	hidden[0] = true // best score but hidden: skipped, 31st takes its place
	if got := SummarizeTop(ctx, pool, stories, scores, hidden, nil); got != summaryTopN {
		t.Fatalf("new rows = %d, want %d", got, summaryTopN)
	}
	if again := SummarizeTop(ctx, pool, stories, scores, hidden, nil); again != 0 {
		t.Fatalf("second pass wrote %d rows, want 0 (cache)", again)
	}
	ids := []int64{stories[0].Items[0].ID, stories[1].Items[0].ID, stories[30].Items[0].ID, stories[31].Items[0].ID}
	have, err := pool.ItemSummaryIDs(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if have[ids[0]] || !have[ids[1]] || !have[ids[2]] || have[ids[3]] {
		t.Fatalf("cache membership wrong: %v", have)
	}
}
