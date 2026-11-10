package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const testFeed = `<?xml version="1.0"?>
<rss version="2.0"><channel><title>T</title>
<item><title>Regulador abre consulta</title><link>https://a.example/1</link><guid>g1</guid>
<description>O regulador abriu consulta pública sobre IA em contratações.</description></item>
<item><title>Rumor de gadget</title><link>https://a.example/2</link><guid>g2</guid>
<description>Um rumor sobre um gadget que não existe.</description></item>
</channel></rss>`

var canned = map[string]string{
	"triage":    `[{"ref":1,"score":9.0,"reason":"afeta leitores"},{"ref":2,"score":1.5,"reason":"rumor sem fonte"}]`,
	"storylist": `{"items":[{"refs":[1],"title":"Consulta do regulador","theme":"regulação","why":"afeta quem contrata","section":"Lead"}]}`,
	"dossier":   `[{"ref":1,"summary":"Resumo original [1].","facts":[{"text":"Consulta aberta","ref":1}],"quote":{"text":"","attribution":""},"unknowns":["prazo"],"interpretation":["impacto"],"sources":[{"ref":1,"url":"https://a.example/1"}]}]`,
	"deepdive":  `[{"ref":1,"deep_facts":[{"text":"fundo extra","ref":1}],"extra_unknowns":[]}]`,
	"draft":     `{"entries":[{"ref":1,"headline":"Regulador quer ouvir o mercado","body_md":"O regulador abriu consulta sobre IA em contratações [1]."}]}`,
	"citecheck": `[]`,
}

func TestRunHeadless(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	def := &config.Publication{
		Slug: "p", Name: "Pub", Language: "pt-BR",
		Sections: []config.Section{{Title: "Lead", Items: 1, Commentary: "required"}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "feeds/a.xml"}},
	}
	if _, err := db.SeedPublication(ctx, def); err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}

	fsys := fstest.MapFS{"feeds/a.xml": {Data: []byte(testFeed)}}
	fetcher := sources.FixtureFetcher{FS: fsys}
	provider := llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
		text, ok := canned[req.Template]
		if !ok {
			return llm.Response{}, nil
		}
		return llm.Response{Text: text, InputTokens: 1000, OutputTokens: 200}, nil
	})

	outDir := t.TempDir()
	res, err := RunHeadless(ctx, db, pub, outDir, fetcher, provider, "haiku", "sonnet", 3)
	if err != nil {
		t.Fatalf("RunHeadless: %v", err)
	}
	if res.Items != 2 || res.Stories != 2 || res.Selected != 1 {
		t.Fatalf("counts: %+v", res)
	}
	if res.CostUSD <= 0 {
		t.Errorf("expected nonzero cost, got %f", res.CostUSD)
	}
	body, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	for _, want := range []string{"## Lead", "### Regulador quer ouvir o mercado", "[1]", "commentary", "Sources:"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("output missing %q", want)
		}
	}
	var runs int
	db.QueryRow(`SELECT count(*) FROM runs WHERE publication_id = ?`, pub.ID).Scan(&runs)
	if runs != len(Steps) {
		t.Errorf("runs = %d, want %d", runs, len(Steps))
	}
	var status string
	db.QueryRow(`SELECT status FROM issues WHERE id = ?`, res.IssueID).Scan(&status)
	if status != "drafted" {
		t.Errorf("issue status = %q", status)
	}
}
