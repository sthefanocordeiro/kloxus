package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
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
	db := testdb.WS(t)

	def := &config.Publication{
		Slug: "p", Name: "Pub", Language: "pt-BR",
		Sections: []config.Section{{Title: "Lead", Items: 1, Commentary: "required"}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/a.xml"}},
	}
	if _, err := db.SeedPublication(ctx, def); err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}

	fsys := fstest.MapFS{"fixtures/feeds/a.xml": {Data: []byte(testFeed)}}
	fetcher := sources.FixtureFetcher{FS: fsys}
	var deepPrompt string
	provider := llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
		if req.Template == "deepdive" {
			deepPrompt = req.User
		}
		text, ok := canned[req.Template]
		if !ok {
			return llm.Response{}, nil
		}
		return llm.Response{Text: text, InputTokens: 1000, OutputTokens: 200}, nil
	})

	outDir := t.TempDir()
	res, err := RunHeadless(ctx, db, pub, outDir, fetcher, provider, "haiku", "sonnet", 3, false, false)
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
	for _, want := range []string{"## Lead", "### Regulador quer ouvir o mercado", "[1]", "Sources:"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("output missing %q", want)
		}
	}
	var runs int
	db.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE publication_id = $1`, pub.ID).Scan(&runs)
	if runs != len(Steps) {
		t.Errorf("runs = %d, want %d", runs, len(Steps))
	}
	var status string
	db.QueryRowContext(ctx, `SELECT status FROM issues WHERE id = $1`, res.IssueID).Scan(&status)
	if status != "drafted" {
		t.Errorf("issue status = %q", status)
	}
	if strings.Contains(deepPrompt, `"title":`) {
		t.Errorf("deepdive prompt contains stored dossier title")
	}
	var dossierJSON string
	db.QueryRowContext(ctx, `SELECT d.json FROM dossiers d JOIN issue_items ii ON ii.id = d.issue_item_id WHERE ii.issue_id = $1`, res.IssueID).Scan(&dossierJSON)
	if !strings.Contains(dossierJSON, `"title":"Consulta do regulador"`) {
		t.Errorf("stored dossier lost picked title: %s", dossierJSON)
	}
}

// testCatalogSource inserts a throwaway source_catalog row for the
// test to attach, then removes it and its items at cleanup. Package
// binaries run in parallel against the same test database and catalog
// items are global (workspace_id NULL) — seeding the shared
// hacker-news/tecnoblog entries lets a concurrent test's candidate set
// pull these rows, and lets that test's cleanup delete a row this run
// already collected (the story_items FK flake of CI run 43935756030,
// KLOX-60 rework). A dedicated catalog isolates the fixture both ways:
// nothing else collects it, and its cleanup only touches its own rows.
func testCatalogSource(t *testing.T, db *store.DB, pool *store.Pool, slug string) *store.CatalogSource {
	t.Helper()
	ctx := context.Background()
	slug = fmt.Sprintf("%s-%d", slug, time.Now().UnixNano())
	if _, err := db.ExecContext(ctx, `INSERT INTO source_catalog
		(slug, name, url, feed_url, policy, domain) VALUES
		($1,$2,$3,$3,'launch',$4)`,
		slug, "Test "+slug, "https://"+slug+".example/feed", slug+".example"); err != nil {
		t.Fatalf("insert catalog row: %v", err)
	}
	cs, err := pool.CatalogEntry(ctx, slug)
	if err != nil {
		t.Fatalf("CatalogEntry: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		// story_items.item_id is a hard FK — unlink before deleting
		// the shared rows (same ordering as the store tests).
		db.ExecContext(ctx, `DELETE FROM story_items WHERE item_id IN
			(SELECT id FROM items WHERE catalog_id = $1)`, cs.ID)
		db.ExecContext(ctx, `DELETE FROM sources WHERE catalog_id = $1`, cs.ID)
		db.ExecContext(ctx, `DELETE FROM items WHERE catalog_id = $1`, cs.ID)
		db.ExecContext(ctx, `DELETE FROM source_catalog WHERE id = $1`, cs.ID)
	})
	return cs
}

// TestPhaseACatalogScale guards the KLOX-45 bound: no matter how deep
// the shared pool is, a run's candidate set is capped at 60 items per
// catalog source within the 7-day window. Seeding ~5k shared items
// (the pool size that timed out on window #20 before batching) must
// leave a run that sees exactly 120 — and finishes far inside the
// timeout because the oversized pool never reaches dedupe.
func TestPhaseACatalogScale(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()

	def := &config.Publication{
		Slug: "p", Name: "Pub", Language: "pt-BR",
		Sections: []config.Section{{Title: "Lead", Items: 2, Commentary: "none"}},
	}
	pubID, err := db.SeedPublication(ctx, def)
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	hn := testCatalogSource(t, db, pool, "scale-signals")
	tb := testCatalogSource(t, db, pool, "scale-articles")
	if _, err := db.AddCatalogSource(ctx, pubID, hn); err != nil {
		t.Fatalf("AddCatalogSource hn: %v", err)
	}
	if _, err := db.AddCatalogSource(ctx, pubID, tb); err != nil {
		t.Fatalf("AddCatalogSource tb: %v", err)
	}
	// ~5k shared items: signal rows (some citing the articles, so
	// mergeCitedGroups runs) plus a launch feed's articles. Only the
	// newest 60 per source may enter the run (KLOX-45).
	if _, err := db.ExecContext(ctx, `
		INSERT INTO items (catalog_id, external_id, url, canonical_url, title, published_at, content_text, kind, meta_json)
		SELECT $1, 'sig-' || g, 'https://sig.example/' || g, 'https://sig.example/' || g,
		       'signal story ' || g, now_utc(), 'signal body ' || g, 'signal',
		       json_build_object('cited_urls', json_build_array('https://tb.example/' || (g % 1500)))::text
		FROM generate_series(1, 3500) g`, hn.ID); err != nil {
		t.Fatalf("seed signals: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO items (catalog_id, external_id, url, canonical_url, title, published_at, content_text, kind)
		SELECT $1, 'tb-' || g, 'https://tb.example/' || g, 'https://tb.example/' || g,
		       'article ' || g, now_utc(), 'article body ' || g, 'article'
		FROM generate_series(1, 1500) g`, tb.ID); err != nil {
		t.Fatalf("seed articles: %v", err)
	}

	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	runner := &Runner{DB: db, Deterministic: true, MaxDeepDives: 3}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	res, err := runner.RunPhaseA(runCtx, pub)
	if err != nil {
		t.Fatalf("RunPhaseA at ~5k-item pool: %v", err)
	}
	// The candidate bounds hold: 60 signal + 60 article = 120 collected,
	// never the seeded 5000.
	if res.Items != 120 || res.Stories == 0 || res.IssueID == 0 {
		t.Fatalf("result: %+v", res)
	}
	// Every story that survives lineup merges carries a triage verdict.
	var total, scored int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*), count(triage_score) FROM stories WHERE publication_id = $1`,
		pubID).Scan(&total, &scored); err != nil {
		t.Fatalf("count stories: %v", err)
	}
	if total == 0 || scored != total {
		t.Fatalf("triaged stories = %d of %d", scored, total)
	}
}

// TestPhaseASelectedCountsCreatedItems is the KLOX-55 header contract:
// the "selected" number — res.Selected and the storylist event the run
// screen renders as "N selecionadas" — must equal the issue items
// actually created, not the raw picks. The newest story here is a
// signal-only link (no evidence body): the lineup picks it, but no
// issue item can be created from it, so it must not count.
func TestPhaseASelectedCountsCreatedItems(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()

	def := &config.Publication{
		Slug: "p", Name: "Pub",
		Sections: []config.Section{{Title: "Lead", Items: 2, Commentary: "none"}},
	}
	pubID, err := db.SeedPublication(ctx, def)
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	hn := testCatalogSource(t, db, pool, "sel-signals")
	tb := testCatalogSource(t, db, pool, "sel-articles")
	if _, err := db.AddCatalogSource(ctx, pubID, hn); err != nil {
		t.Fatalf("AddCatalogSource hn: %v", err)
	}
	if _, err := db.AddCatalogSource(ctx, pubID, tb); err != nil {
		t.Fatalf("AddCatalogSource tb: %v", err)
	}
	if _, err := pool.SaveCatalogItems(ctx, hn.ID, []store.CatalogItem{
		{ExternalID: "sig1", URL: "https://sig.example/1", CanonicalURL: "https://sig.example/1",
			Title: "Signal-only link", PublishedAt: "2030-01-02T00:00:00Z", Kind: "signal"},
	}); err != nil {
		t.Fatalf("save signal: %v", err)
	}
	if _, err := pool.SaveCatalogItems(ctx, tb.ID, []store.CatalogItem{
		{ExternalID: "art1", URL: "https://art.example/1", CanonicalURL: "https://art.example/1",
			Title: "Regulador abre consulta", PublishedAt: "2030-01-01T00:00:00Z",
			ContentText: "O regulador abriu consulta pública.", Kind: "article"},
	}); err != nil {
		t.Fatalf("save article: %v", err)
	}

	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	var emitted int
	runner := &Runner{DB: db, Deterministic: true, MaxDeepDives: 3}
	runner.OnEvent = func(ev StepEvent) {
		if ev.Step == "storylist" {
			emitted = ev.Counts["selected"]
		}
	}
	res, err := runner.RunPhaseA(ctx, pub)
	if err != nil {
		t.Fatalf("RunPhaseA: %v", err)
	}
	iis, err := db.IssueItems(ctx, res.IssueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	if len(iis) != 1 {
		t.Fatalf("issue items = %d, want 1 — the signal-only story has no evidence body", len(iis))
	}
	if res.Selected != len(iis) {
		t.Fatalf("res.Selected = %d, want %d — count must match the rendered lineup", res.Selected, len(iis))
	}
	if emitted != len(iis) {
		t.Fatalf("storylist event selected = %d, want %d", emitted, len(iis))
	}
}

// TestPhaseASignalOnlyLeadLeavesNoEmptySection is the KLOX-60 hosted
// reproducer: the freshest catalog entry is a signal-only link — it
// outscores every article, so before the fix it took the lead pick and
// produced no issue item, leaving "Lead" empty while "Radar" filled;
// the draft gate then refused the issue (409 section_empty). Now the
// slot goes to the best draftable story and every section fills.
func TestPhaseASignalOnlyLeadLeavesNoEmptySection(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()

	def := &config.Publication{
		Slug: "p", Name: "Pub",
		Sections: []config.Section{
			{Title: "Lead", Items: 1, Commentary: "none"},
			{Title: "Radar", Items: 1, Commentary: "none"},
		},
	}
	pubID, err := db.SeedPublication(ctx, def)
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	hn := testCatalogSource(t, db, pool, "k60-signals")
	tb := testCatalogSource(t, db, pool, "k60-articles")
	if _, err := db.AddCatalogSource(ctx, pubID, hn); err != nil {
		t.Fatalf("AddCatalogSource hn: %v", err)
	}
	if _, err := db.AddCatalogSource(ctx, pubID, tb); err != nil {
		t.Fatalf("AddCatalogSource tb: %v", err)
	}
	if _, err := pool.SaveCatalogItems(ctx, hn.ID, []store.CatalogItem{
		{ExternalID: "sig1", URL: "https://sig.example/1", CanonicalURL: "https://sig.example/1",
			Title: "Signal-only link", PublishedAt: "2030-01-03T00:00:00Z", Kind: "signal"},
	}); err != nil {
		t.Fatalf("save signal: %v", err)
	}
	if _, err := pool.SaveCatalogItems(ctx, tb.ID, []store.CatalogItem{
		{ExternalID: "art1", URL: "https://art.example/1", CanonicalURL: "https://art.example/1",
			Title: "Regulador abre consulta", PublishedAt: "2030-01-02T00:00:00Z",
			ContentText: "O regulador abriu consulta pública.", Kind: "article"},
		{ExternalID: "art2", URL: "https://art.example/2", CanonicalURL: "https://art.example/2",
			Title: "Nova linha de crédito", PublishedAt: "2030-01-01T00:00:00Z",
			ContentText: "Banco anunciou linha de crédito.", Kind: "article"},
	}); err != nil {
		t.Fatalf("save articles: %v", err)
	}

	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	runner := &Runner{DB: db, Deterministic: true, MaxDeepDives: 3}
	res, err := runner.RunPhaseA(ctx, pub)
	if err != nil {
		t.Fatalf("RunPhaseA: %v", err)
	}
	iis, err := db.IssueItems(ctx, res.IssueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	filled := map[string]int{}
	for _, ii := range iis {
		filled[ii.Section]++
	}
	for _, sec := range def.Sections {
		if filled[sec.Title] == 0 {
			t.Fatalf("section %q has no issue items — the draft gate will refuse this issue", sec.Title)
		}
	}
	// The signal-only story ranked first but produced no item; both
	// slots went to the two draftable articles.
	if len(iis) != 2 || res.Selected != 2 {
		t.Fatalf("items = %d selected = %d, want 2/2", len(iis), res.Selected)
	}
	for _, ii := range iis {
		story, err := db.StoryWithItems(ctx, ii.StoryID)
		if err != nil {
			t.Fatalf("StoryWithItems: %v", err)
		}
		if story.Title == "Signal-only link" {
			t.Fatalf("issue item %d points at the signal-only story", ii.ID)
		}
	}
}

// TestPhaseAReslotsSectionWhenPickYieldsNoItem covers the model
// storylist path behind the same defect: the storylist picks a
// signal-only story for the lead section, the pick yields no item,
// and the section must be re-slotted with the best remaining
// draftable story rather than left empty (KLOX-60).
func TestPhaseAReslotsSectionWhenPickYieldsNoItem(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()

	def := &config.Publication{
		Slug: "p", Name: "Pub",
		Sections: []config.Section{
			{Title: "Lead", Items: 1, Commentary: "none"},
			{Title: "Radar", Items: 1, Commentary: "none"},
		},
	}
	pubID, err := db.SeedPublication(ctx, def)
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	hn := testCatalogSource(t, db, pool, "k60r-signals")
	tb := testCatalogSource(t, db, pool, "k60r-articles")
	if _, err := db.AddCatalogSource(ctx, pubID, hn); err != nil {
		t.Fatalf("AddCatalogSource hn: %v", err)
	}
	if _, err := db.AddCatalogSource(ctx, pubID, tb); err != nil {
		t.Fatalf("AddCatalogSource tb: %v", err)
	}
	// Seeded first → lowest item id → story 1 → storylist ref 1.
	if _, err := pool.SaveCatalogItems(ctx, hn.ID, []store.CatalogItem{
		{ExternalID: "sig1", URL: "https://sig.example/1", CanonicalURL: "https://sig.example/1",
			Title: "Signal-only link", PublishedAt: "2030-01-03T00:00:00Z", Kind: "signal"},
	}); err != nil {
		t.Fatalf("save signal: %v", err)
	}
	if _, err := pool.SaveCatalogItems(ctx, tb.ID, []store.CatalogItem{
		{ExternalID: "art1", URL: "https://art.example/1", CanonicalURL: "https://art.example/1",
			Title: "Regulador abre consulta", PublishedAt: "2030-01-02T00:00:00Z",
			ContentText: "O regulador abriu consulta pública.", Kind: "article"},
		{ExternalID: "art2", URL: "https://art.example/2", CanonicalURL: "https://art.example/2",
			Title: "Nova linha de crédito", PublishedAt: "2030-01-01T00:00:00Z",
			ContentText: "Banco anunciou linha de crédito.", Kind: "article"},
	}); err != nil {
		t.Fatalf("save articles: %v", err)
	}

	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	canned := map[string]string{
		"triage": `[{"ref":1,"score":9.0,"reason":"freshest"},{"ref":2,"score":8.0,"reason":"a"},{"ref":3,"score":7.0,"reason":"b"}]`,
		// the model slots the signal-only story into Lead and story 2
		// into Radar; story 3 is left on the bench
		"storylist": `{"items":[{"refs":[1],"title":"sig","theme":"t","why":"w","section":"Lead"},{"refs":[2],"title":"a","theme":"t","why":"w","section":"Radar"}]}`,
		// entries are the Radar pick (ref 2) and the re-slotted Lead
		// story (ref 3 = len(picks)+1)
		"dossier": `[
			{"ref":2,"summary":"Resumo a [1].","facts":[{"text":"Fato a","ref":1}],"interpretation":["i"],"sources":[{"ref":1,"url":"https://art.example/1"}]},
			{"ref":3,"summary":"Resumo b [1].","facts":[{"text":"Fato b","ref":1}],"interpretation":["i"],"sources":[{"ref":1,"url":"https://art.example/2"}]}
		]`,
		"deepdive": `[]`,
	}
	runner := &Runner{
		DB: db, TriageModel: "haiku", DraftModel: "sonnet", MaxDeepDives: 3,
		LLM: llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
			return llm.Response{Text: canned[req.Template], InputTokens: 1, OutputTokens: 1}, nil
		}),
	}
	res, err := runner.RunPhaseA(ctx, pub)
	if err != nil {
		t.Fatalf("RunPhaseA: %v", err)
	}
	iis, err := db.IssueItems(ctx, res.IssueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	filled := map[string]int{}
	for _, ii := range iis {
		filled[ii.Section]++
	}
	for _, sec := range def.Sections {
		if filled[sec.Title] == 0 {
			t.Fatalf("section %q has no issue items — the draft gate will refuse this issue", sec.Title)
		}
	}
	var lead *store.IssueItem
	for i := range iis {
		if iis[i].Section == "Lead" {
			lead = &iis[i]
		}
	}
	if lead == nil {
		t.Fatal("no item in Lead")
	}
	story, err := db.StoryWithItems(ctx, lead.StoryID)
	if err != nil {
		t.Fatalf("StoryWithItems: %v", err)
	}
	if story.Title != "Nova linha de crédito" {
		t.Fatalf("Lead story = %q, want the re-slotted article", story.Title)
	}
	if !strings.Contains(lead.Reason, "re-slotted") {
		t.Fatalf("Lead item reason = %q, want the re-slot marker", lead.Reason)
	}
}

func TestPhaseBReplayUsesCanonicalItems(t *testing.T) {
	ctx := context.Background()
	db := testdb.WS(t)

	def := &config.Publication{
		Slug: "p", Name: "Pub", Language: "pt-BR",
		Sections: []config.Section{{Title: "Lead", Items: 2, Commentary: "none"}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/a.xml"}},
	}
	if _, err := db.SeedPublication(ctx, def); err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}

	responses := map[string]string{
		"triage":    canned["triage"],
		"storylist": `{"items":[{"refs":[1],"title":"Consulta do regulador","theme":"regulação","why":"afeta quem contrata","section":"Lead"},{"refs":[2],"title":"Rumor verificado","theme":"rumor","why":"cobertura suficiente","section":"Lead"}]}`,
		"dossier": `[
			{"ref":1,"summary":"Resumo um [1].","facts":[{"text":"Fato um","ref":1}],"quote":{"text":"","attribution":""},"unknowns":[],"interpretation":[],"sources":[{"ref":1,"url":"https://a.example/1"}]},
			{"ref":2,"summary":"Resumo dois [1].","facts":[{"text":"Fato dois","ref":1}],"quote":{"text":"","attribution":""},"unknowns":[],"interpretation":[],"sources":[{"ref":1,"url":"https://a.example/2"}]}
		]`,
		"deepdive": `[]`,
	}
	fsys := fstest.MapFS{"fixtures/feeds/a.xml": {Data: []byte(testFeed)}}
	runner := &Runner{
		DB: db, Fetcher: sources.FixtureFetcher{FS: fsys}, TriageModel: "haiku", DraftModel: "sonnet", MaxDeepDives: 3,
		DemoMode: true,
		LLM: llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
			return llm.Response{Text: responses[req.Template], InputTokens: 1, OutputTokens: 1}, nil
		}),
	}
	resA, err := runner.RunPhaseA(ctx, pub)
	if err != nil {
		t.Fatalf("RunPhaseA: %v", err)
	}
	iis, err := db.IssueItems(ctx, resA.IssueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	if len(iis) != 2 {
		t.Fatalf("issue items = %d, want 2", len(iis))
	}
	if err := db.SetIssueItemStatus(ctx, iis[0].ID, "rejected"); err != nil {
		t.Fatalf("SetIssueItemStatus: %v", err)
	}
	if err := db.SetIssueItemStatus(ctx, iis[1].ID, "approved"); err != nil {
		t.Fatalf("SetIssueItemStatus: %v", err)
	}

	allEntries, allDossiers, err := runner.loadEntries(ctx, resA.IssueID, true)
	if err != nil {
		t.Fatalf("loadEntries: %v", err)
	}
	type drItem struct {
		Ref            int
		Section        string
		Score          float64
		Title          string
		Summary        string
		Facts          []DossierFact
		DeepFacts      []DossierFact
		Interpretation []string
	}
	drItems := make([]drItem, len(allEntries))
	for i, e := range allEntries {
		d := allDossiers[e.ref]
		drItems[i] = drItem{Ref: e.ref, Section: e.section, Score: e.score, Title: e.title,
			Summary: d.Summary, Facts: d.Facts, DeepFacts: d.DeepFacts, Interpretation: d.Interpretation}
	}
	draftUser, err := RenderPrompt("draft", map[string]any{
		"Publication": pub.Name, "Audience": pub.Audience, "Language": pub.Language,
		"Tone": pub.Tone, "Items": drItems,
	})
	if err != nil {
		t.Fatalf("RenderPrompt draft: %v", err)
	}

	// Item 2's stored text is "Um rumor sobre um gadget que não
	// existe." — the draft sentence must stay inside its lexicon or
	// the deterministic support floor adds a flag the test does not
	// seed.
	draftText := `{"entries":[{"ref":1,"headline":"Um","body_md":"Fato um [1]."},{"ref":2,"headline":"Dois","body_md":"Rumor de gadget confirmado [1]."}]}`
	draftByRef := map[int]struct {
		headline, body string
	}{
		1: {headline: "Um", body: "Fato um [1]."},
		2: {headline: "Dois", body: "Rumor de gadget confirmado [1]."},
	}
	sectionOrder := []string{pub.Sections[0].Title}
	checkBody := assembleIssue(pub, resA.Number, allEntries, draftByRef, allDossiers, sectionOrder)
	type cSent struct {
		ID   int
		Item int
		Refs string
		Text string
	}
	type cSource struct {
		Ref int
		URL string
	}
	type cItem struct {
		Ref     int
		Sources []cSource
	}
	var cs []cSent
	for _, s := range extractSentences(checkBody) {
		cs = append(cs, cSent{ID: s.id, Item: s.itemRef, Refs: s.refs, Text: s.text})
	}
	var ci []cItem
	for _, e := range allEntries {
		c := cItem{Ref: e.ref}
		for _, s := range allDossiers[e.ref].Sources {
			c.Sources = append(c.Sources, cSource{Ref: s.Ref, URL: s.URL})
		}
		ci = append(ci, c)
	}
	checkUser, err := RenderPrompt("citecheck", map[string]any{
		"Publication": pub.Name, "Sentences": cs, "Items": ci,
	})
	if err != nil {
		t.Fatalf("RenderPrompt citecheck: %v", err)
	}

	fixtures := fstest.MapFS{}
	addFixture := func(req llm.Request, text string) {
		data, err := json.Marshal(map[string]any{
			"model": req.Model, "text": text, "input_tokens": 1, "output_tokens": 1,
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		fixtures["fixtures/llm/"+llm.Key(req)+".json"] = &fstest.MapFile{Data: data}
	}
	addFixture(llm.Request{Model: "sonnet", Template: "draft", User: draftUser}, draftText)
	addFixture(llm.Request{Model: "haiku", Template: "citecheck", User: checkUser},
		`[{"id":1,"verdict":"unsupported","note":"rejected item"},{"id":2,"verdict":"unsupported","note":"active item"}]`)

	runner.LLM = llm.Replay{FS: fixtures, Dir: "fixtures/llm"}
	resB, err := runner.RunPhaseB(ctx, pub, resA.IssueID)
	if err != nil {
		t.Fatalf("RunPhaseB: %v", err)
	}
	if resB.Selected != 1 {
		t.Fatalf("selected = %d, want 1", resB.Selected)
	}
	iss, err := db.GetIssue(ctx, resA.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if strings.Contains(iss.BodyMD, "<!-- item 1 -->") {
		t.Errorf("rejected item present in issue body")
	}
	if !strings.Contains(iss.BodyMD, "<!-- item 2 -->") {
		t.Errorf("active item missing from issue body")
	}
	if len(iss.Flags) != 1 || iss.Flags[0].Item != 2 || iss.Flags[0].Note != "active item" {
		t.Fatalf("flags = %+v, want only active item flag", iss.Flags)
	}
}

// TestReplaySecondPublicationOnReusedDB reproduces KLOX-15: a second
// publication on the same database re-collects the same feeds into new
// item rows, so row ids shift. Dossier JSON embedded in prompts must not
// carry those database-local ids, or the deepdive replay key (and the
// downstream draft/citecheck keys) miss on every reused database.
func TestReplaySecondPublicationOnReusedDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.WS(t)

	data, err := examples.FS.ReadFile("publications/futuro-proximo.yaml")
	if err != nil {
		t.Fatalf("reading example publication: %v", err)
	}
	def, err := config.ParsePublication(data)
	if err != nil {
		t.Fatalf("ParsePublication: %v", err)
	}

	fetcher := sources.FixtureFetcher{FS: examples.FS}
	replay := llm.Replay{FS: examples.FS, Dir: "fixtures/llm", DumpDir: os.Getenv("KLOX_LLM_DUMP")}
	var deepPrompts []string
	provider := llm.Func(func(ctx context.Context, req llm.Request) (llm.Response, error) {
		if req.Template == "deepdive" {
			deepPrompts = append(deepPrompts, req.User)
		}
		return replay.Complete(ctx, req)
	})
	run := func(pub *store.Publication) *Result {
		t.Helper()
		res, err := RunHeadless(ctx, db, pub, t.TempDir(), fetcher, provider,
			"claude-haiku-5-5", "claude-sonnet-5-5", 3, true, false)
		if err != nil {
			t.Fatalf("RunHeadless(%s): %v", pub.Slug, err)
		}
		return res
	}

	if _, err := db.SeedPublication(ctx, def); err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub1, err := db.GetPublication(ctx, "futuro-proximo")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	res1 := run(pub1)
	if res1.Selected == 0 {
		t.Fatal("first run selected no items")
	}

	// Second publication: same prompt-relevant config, new slug. Its
	// sources are new rows, so collect re-inserts every item under new
	// ids — the reused-database condition that used to shift the
	// deepdive replay key.
	def2 := *def
	def2.Slug = "futuro-proximo-2"
	if _, err := db.SeedPublication(ctx, &def2); err != nil {
		t.Fatalf("SeedPublication 2: %v", err)
	}
	pub2, err := db.GetPublication(ctx, "futuro-proximo-2")
	if err != nil {
		t.Fatalf("GetPublication 2: %v", err)
	}
	var maxID1, minID2 int64
	db.QueryRowContext(ctx, `SELECT COALESCE(MAX(i.id),0) FROM items i
		JOIN sources s ON s.id = i.source_id WHERE s.publication_id = $1`, pub1.ID).Scan(&maxID1)
	res2 := run(pub2)
	db.QueryRowContext(ctx, `SELECT COALESCE(MIN(i.id),0) FROM items i
		JOIN sources s ON s.id = i.source_id WHERE s.publication_id = $1`, pub2.ID).Scan(&minID2)
	if maxID1 == 0 || minID2 <= maxID1 {
		t.Fatalf("expected shifted item ids for the second publication (max pub1=%d, min pub2=%d)", maxID1, minID2)
	}
	if len(deepPrompts) != 2 {
		t.Fatalf("deepdive calls = %d, want 2", len(deepPrompts))
	}
	for i, p := range deepPrompts {
		if strings.Contains(p, "item_id") {
			t.Errorf("deepdive prompt %d contains database-local item_id", i+1)
		}
	}
	if deepPrompts[0] != deepPrompts[1] {
		t.Errorf("deepdive prompt differs between runs on the same feeds")
	}

	// The issue drafted: non-empty body with /a/{id} source links that
	// resolve to the second publication's own item rows.
	iss, err := db.GetIssue(ctx, res2.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if strings.TrimSpace(iss.BodyMD) == "" {
		t.Fatal("second run produced an empty draft")
	}
	linkRe := regexp.MustCompile(`/a/(\d+)`)
	links := linkRe.FindAllStringSubmatch(iss.BodyMD, -1)
	if len(links) == 0 {
		t.Fatal("draft has no /a/{id} source links")
	}
	for _, m := range links {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		var owner int64
		err := db.QueryRowContext(ctx, `SELECT s.publication_id FROM items i
			JOIN sources s ON s.id = i.source_id WHERE i.id = $1`, id).Scan(&owner)
		if err != nil {
			t.Fatalf("source link /a/%d resolves to no item: %v", id, err)
		}
		if owner != pub2.ID {
			t.Errorf("source link /a/%d points at item of publication %d, want %d", id, owner, pub2.ID)
		}
	}

	// The stored dossier keeps the row ids the links depend on.
	var dossierJSON string
	if err := db.QueryRowContext(ctx, `SELECT d.json FROM dossiers d
		JOIN issue_items ii ON ii.id = d.issue_item_id WHERE ii.issue_id = $1 LIMIT 1`,
		res2.IssueID).Scan(&dossierJSON); err != nil {
		t.Fatalf("reading dossier: %v", err)
	}
	if !strings.Contains(dossierJSON, `"item_id"`) {
		t.Errorf("stored dossier lost item_id: %s", dossierJSON[:200])
	}
}

// TestCitationAfterPeriodStaysAttached: a "[n]" dangling after a
// sentence-final period belongs to that sentence — it must not split off
// into an uncited fragment (which would flag a false "no citation").
func TestCitationAfterPeriodStaysAttached(t *testing.T) {
	recs := extractSentences("### H <!-- item 3 -->\n\nO custo segue três vezes o da banda larga urbana. [1] Outra afirmação sem fonte.")
	if len(recs) != 2 {
		t.Fatalf("extractSentences = %d sentences, want 2: %#v", len(recs), recs)
	}
	if recs[0].refs != "[1]" {
		t.Fatalf("sentence 0 refs = %q, want [1] (sentence: %q)", recs[0].refs, recs[0].text)
	}
	if recs[1].refs != "" {
		t.Fatalf("sentence 1 refs = %q, want uncited", recs[1].refs)
	}
	if recs[1].text != "Outra afirmação sem fonte." {
		t.Fatalf("sentence 1 = %q", recs[1].text)
	}
}

// consecutive markers after terminal punctuation all belong to the
// sentence that precedes them — dropping a trailing "[n]" would orphan
// it from the stored line (and from any flag keyed on that line).
func TestCitationsAfterPeriodAllStayAttached(t *testing.T) {
	recs := extractSentences("### H <!-- item 3 -->\n\nO custo segue três vezes o da banda larga urbana. [1] [2] Outra afirmação sem fonte.")
	if len(recs) != 2 {
		t.Fatalf("extractSentences = %d sentences, want 2: %#v", len(recs), recs)
	}
	if recs[0].refs != "[1][2]" {
		t.Fatalf("sentence 0 refs = %q, want [1][2] (sentence: %q)", recs[0].refs, recs[0].text)
	}
	if recs[0].text != "O custo segue três vezes o da banda larga urbana. [1] [2]" {
		t.Fatalf("sentence 0 = %q, want both markers attached", recs[0].text)
	}
	if recs[1].refs != "" {
		t.Fatalf("sentence 1 refs = %q, want uncited", recs[1].refs)
	}
	if recs[1].text != "Outra afirmação sem fonte." {
		t.Fatalf("sentence 1 = %q", recs[1].text)
	}
}
