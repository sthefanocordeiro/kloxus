package pipeline

// KLOX-67 tests: enrichment normalization, the shared research cache
// paths, paid-action caps, the degraded fallback, and the
// enrichment-aware free ranking. All replay/deterministic — no paid
// provider calls (owner freeze).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

func TestNormalizeEnrichment(t *testing.T) {
	got := normalizeEnrichment(7, enrichRow{
		Ref: 1, Kind: "NOTICIA", Promotional: false,
		Topics:    []string{"tecnologia", "IA", "bogus-tag", "startups", "cripto", "fintech", "extra"},
		SummaryPT: strings.Repeat("palavra ", 40),
		Language:  "PT",
	}, "claude-haiku-5-5")
	if got.Kind != "noticia" {
		t.Errorf("kind = %q", got.Kind)
	}
	if len(got.Topics) != 5 {
		t.Errorf("topics = %v (cap is 5, bogus dropped)", got.Topics)
	}
	for _, topic := range got.Topics {
		if topic == "bogus-tag" {
			t.Error("off-list tag kept")
		}
	}
	if n := len(strings.Fields(got.SummaryPT)); n != 25 {
		t.Errorf("summary words = %d, want 25", n)
	}
	if got.Language != "pt" {
		t.Errorf("language = %q", got.Language)
	}

	// unknown kinds become "outro"; publieditorial forces promotional
	if e := normalizeEnrichment(1, enrichRow{Kind: "surpresa"}, "m"); e.Kind != "outro" {
		t.Errorf("unknown kind = %q", e.Kind)
	}
	if e := normalizeEnrichment(1, enrichRow{Kind: "publieditorial"}, "m"); !e.Promotional {
		t.Error("publieditorial must mark promotional")
	}
}

func TestResearchKeyContentIdentity(t *testing.T) {
	a := []store.Item{
		{ID: 1, URL: "https://b.example/x?utm_source=rss", CanonicalURL: "https://b.example/x"},
		{ID: 2, URL: "https://a.example/y", CanonicalURL: "https://a.example/y"},
	}
	b := []store.Item{a[1], a[0]} // order must not matter
	if ResearchKey(a) != ResearchKey(b) {
		t.Error("same items, different order → different key")
	}
	withSignal := append([]store.Item{}, a...)
	withSignal = append(withSignal, store.Item{ID: 9, Kind: "signal", URL: "https://hn.example/1"})
	if ResearchKey(a) != ResearchKey(withSignal) {
		t.Error("signal items must not change the content key")
	}
	if ResearchKey(a) == ResearchKey([]store.Item{a[0]}) {
		t.Error("different item sets share a key")
	}
}

// TestResearcherDegradedNoLLM: with no provider configured ForStory
// degrades to absence — owner D4: the unavailable element is hidden,
// never replaced by a placeholder payload.
func TestResearcherDegradedNoLLM(t *testing.T) {
	rc := &Researcher{Model: "claude-haiku-5-5"} // no Pool, no LLM
	story := &store.Story{Title: "S", Items: []store.Item{
		{ID: 1, URL: "https://x.example/1", SourceName: "X", ContentText: "Primeira frase da fonte. Segunda frase."},
	}}
	res, outcome, err := rc.ForStory(context.Background(), nil, story)
	if err != nil {
		t.Fatalf("ForStory: %v", err)
	}
	if outcome != researchDegraded {
		t.Fatalf("outcome = %v, want degraded", outcome)
	}
	if res != nil {
		t.Fatalf("degraded research = %+v, want nil (no placeholder)", res)
	}
}

func TestResearcherFreshAndPaidCap(t *testing.T) {
	answer := `{"summary":"Resumo.","facts":[{"text":"Fato um.","ref":1}],"open_questions":["?"],"draft_body":"Corpo [1].","sources":[{"ref":1,"url":"https://x.example/1","name":"X"}]}`
	var calls int
	rc := &Researcher{
		Model: "claude-haiku-5-5",
		LLM: llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
			calls++
			if req.Template != "research" {
				t.Errorf("template = %q", req.Template)
			}
			if req.Model != "claude-haiku-5-5" {
				t.Errorf("model = %q", req.Model)
			}
			return llm.Response{Text: answer}, nil
		}),
	}
	story := &store.Story{ID: 5, Title: "S", Items: []store.Item{
		{ID: 1, URL: "https://x.example/1", SourceName: "X", ContentText: "body"},
	}}
	res, outcome, err := rc.ForStory(context.Background(), nil, story)
	if err != nil || outcome != researchFresh {
		t.Fatalf("fresh: outcome=%v err=%v", outcome, err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	if res.DraftBody != "Corpo [1]." || len(res.Facts) != 1 {
		t.Fatalf("research = %+v", res)
	}
	if res.Sources[0].ItemID != 1 {
		t.Fatalf("source item id not attached: %+v", res.Sources)
	}
	d := res.ToDossier(3)
	if d.Ref != 3 || d.Summary != "Resumo." || len(d.Facts) != 1 {
		t.Fatalf("dossier = %+v", d)
	}
}

// TestResearcherBlockedDegrades: a provider refusal (kill switch,
// budget) returns no research, never an error — §7.3 as amended by
// owner D4 (absence, not a placeholder payload).
func TestResearcherBlockedDegrades(t *testing.T) {
	rc := &Researcher{
		Model: "claude-haiku-5-5",
		LLM:   llm.None{},
	}
	story := &store.Story{Title: "S", Items: []store.Item{
		{ID: 1, URL: "https://x.example/1", ContentText: "Texto."},
	}}
	res, outcome, err := rc.ForStory(context.Background(), nil, story)
	if err != nil {
		t.Fatalf("blocked provider must degrade, not error: %v", err)
	}
	if outcome != researchDegraded || res != nil {
		t.Fatalf("outcome=%v res=%+v — D4 degrades to absence, not placeholder text", outcome, res)
	}
}

func TestScoreStoriesEnriched(t *testing.T) {
	pub := &store.Publication{Topic: "tecnologia e IA", EditorialLine: "startups brasileiras"}
	mkStory := func(ids ...int64) store.Story {
		s := store.Story{}
		for _, id := range ids {
			s.Items = append(s.Items, store.Item{ID: id})
		}
		return s
	}
	stories := []store.Story{mkStory(1), mkStory(2), mkStory(3), mkStory(4)}
	base, _ := scoreStories(stories, "pt")
	enrich := map[int64]*store.ItemEnrichment{
		1: {ItemID: 1, Kind: "noticia", Topics: []string{"tecnologia"}}, // +1 kind, +2 topic
		2: {ItemID: 2, Kind: "analise", Topics: []string{"esporte"}},    // +1 kind, no topic
		3: {ItemID: 3, Kind: "outro"},                                   // -0.5 kind
		// 4: no enrichment — unchanged
	}
	got, reasons := scoreStoriesEnriched(stories, "pt", pub, enrich)
	if d := got[0] - base[0]; d != 3 {
		t.Errorf("story1 delta = %v, want 3 (kind + topic)", d)
	}
	if d := got[1] - base[1]; d != 1 {
		t.Errorf("story2 delta = %v, want 1 (kind only)", d)
	}
	if d := got[2] - base[2]; d != -0.5 {
		t.Errorf("story3 delta = %v, want -0.5", d)
	}
	if got[3] != base[3] {
		t.Errorf("unenriched story changed: %v → %v", base[3], got[3])
	}
	if !strings.Contains(reasons[0], "tema") {
		t.Errorf("pt reason should mention the topic boost: %q", reasons[0])
	}
	// nil enrichment map = the pure deterministic score
	again, _ := scoreStoriesEnriched(stories, "pt", pub, nil)
	for i := range again {
		if again[i] != base[i] {
			t.Fatalf("nil enrich changed story %d", i)
		}
	}
}

func TestTopicMatchesIA(t *testing.T) {
	pub := &store.Publication{Topic: "IA e startups"}
	if !topicMatches("IA", foldedTokens(pub.Topic+" "+pub.EditorialLine), pub) {
		t.Error("literal IA should match")
	}
	// lowercase "ia" in prose must NOT match the IA tag — it is a PT verb
	pub2 := &store.Publication{Topic: "quem lê via whatsapp"}
	if topicMatches("IA", foldedTokens(pub2.Topic), pub2) {
		t.Error("lowercase ia must not match the IA tag")
	}
	pub3 := &store.Publication{Topic: "inteligência artificial"}
	if !topicMatches("IA", foldedTokens(pub3.Topic), pub3) {
		t.Error("inteligência artificial should match the IA tag")
	}
	if !topicMatches("tecnologia", foldedTokens("mídia e tecnologia"), pub) {
		t.Error("plain tag should fold and match")
	}
}

func TestEnricherNilSafe(t *testing.T) {
	var e *Enricher
	if n, err := e.Sweep(context.Background(), 40); err != nil || n != 0 {
		t.Fatalf("nil enricher: n=%d err=%v", n, err)
	}
	if n, err := (&Enricher{}).Sweep(context.Background(), 40); err != nil || n != 0 {
		t.Fatalf("empty enricher: n=%d err=%v", n, err)
	}
}

// TestEnricherBatchCall covers the per-batch contract deterministically:
// the items go out as ONE call and each answer ref maps back to its own
// item id. The seeds use kind='signal' so UnenrichedItems — and any
// sweep running against the shared items table — can never pull them
// into a batch mid-test.
func TestEnricherBatchCall(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Slug: "enrich-pub", Name: "P",
		Sections: []config.Section{{Title: "Lead", Items: 1}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/a.xml"}},
	})
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, _ := db.Sources(ctx, pubID)
	nonce := fmt.Sprintf("-%d", time.Now().UnixNano())
	var ids []int64
	for _, ext := range []string{"eb-a" + nonce, "eb-b" + nonce} {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind)
			 VALUES ($1,$2,$3,$4,$5,'body','signal') RETURNING id`,
			db.Workspace(), srcs[0].ID, ext, "https://x.example/"+ext, "Article "+ext).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", ext, err)
		}
		ids = append(ids, id)
	}

	var calls int
	enr := &Enricher{
		Pool: pool, Model: "claude-haiku-5-5",
		LLM: llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
			calls++
			if req.Template != "enrich" || req.Model != "claude-haiku-5-5" {
				t.Errorf("req = template %q model %q", req.Template, req.Model)
			}
			return llm.Response{Text: `[
				{"ref":1,"kind":"noticia","promotional":false,"topics":["tecnologia"],"summary_pt":"Resumo um.","language":"pt"},
				{"ref":2,"kind":"lista","promotional":false,"topics":[],"summary_pt":"Lista.","language":"pt"}
			]`}, nil
		}),
	}
	n, err := enr.enrichBatch(ctx, []store.UnenrichedItem{
		{ID: ids[0], Title: "Article eb-a"},
		{ID: ids[1], Title: "Article eb-b"},
	})
	if err != nil || n != 2 {
		t.Fatalf("enrichBatch: n=%d err=%v", n, err)
	}
	if calls != 1 {
		t.Fatalf("2 items must batch into 1 call, got %d", calls)
	}
	m, err := db.EnrichmentsForItems(ctx, ids)
	if err != nil || len(m) != 2 {
		t.Fatalf("enrichments: %v len=%d", err, len(m))
	}
	if e := m[ids[0]]; e.Kind != "noticia" || e.Excluded() {
		t.Errorf("ref1 → ids[0] should be an included noticia: %+v", e)
	}
	if e := m[ids[1]]; e.Kind != "lista" || !e.Excluded() {
		t.Errorf("ref2 → ids[1] should be an excluded lista: %+v", e)
	}
}

// TestEnricherSweep is the DB-backed sweep path: unenriched articles get
// an enrichment row and never resurface as pending — idempotent per §7.2.
// Assertions are scoped to this test's seeded ids: UnenrichedItems scans
// the shared items table, so package test binaries running on the same
// DATABASE_URL can add their own rows to the same batch — global counts
// (n, calls) are not deterministic here (QA FAIL #2: a second sweep
// observed n=2 calls=2 on foreign rows). Skips without DATABASE_URL.
func TestEnricherSweep(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Slug: "enrich-pub", Name: "P",
		Sections: []config.Section{{Title: "Lead", Items: 1}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/a.xml"}},
	})
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, _ := db.Sources(ctx, pubID)
	nonce := fmt.Sprintf("-%d", time.Now().UnixNano())
	var ids []int64
	for _, ext := range []string{"es-a" + nonce, "es-b" + nonce} {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind)
			 VALUES ($1,$2,$3,$4,$5,'body','article') RETURNING id`,
			db.Workspace(), srcs[0].ID, ext, "https://x.example/"+ext, "Article "+ext).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", ext, err)
		}
		ids = append(ids, id)
	}

	enr := &Enricher{Pool: pool, Model: "claude-haiku-5-5", LLM: sweepLLM(t)}

	// Sweep until the seeded rows are enriched (bounded): the pending
	// scan runs newest-first and a burst of foreign inserts could
	// outrank them — retrying keeps the outcome deterministic without
	// asserting on global table state.
	var m map[int64]*store.ItemEnrichment
	for attempt := 0; ; attempt++ {
		if _, err := enr.Sweep(ctx, 40); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		m, err = db.EnrichmentsForItems(ctx, ids)
		if err != nil {
			t.Fatalf("enrichments: %v", err)
		}
		if len(m) == len(ids) {
			break
		}
		if attempt == 9 {
			t.Fatalf("seeded items still unenriched after %d sweeps", attempt+1)
		}
	}
	if pending := unenrichedAmong(t, ctx, db, ids); pending != 0 {
		t.Fatalf("seeded items still pending: %d", pending)
	}

	// Idempotent: enriched rows never resurface as pending, however
	// many sweeps run or whatever foreign rows join the table.
	if _, err := enr.Sweep(ctx, 40); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if pending := unenrichedAmong(t, ctx, db, ids); pending != 0 {
		t.Fatalf("seeded items pending again after re-sweep: %d", pending)
	}
}

// sweepLLM fakes the enrichment call for Sweep tests: one row for every
// ref a full batch can carry. The shared items table can add foreign
// rows to a batch, so an answer covering only the seeded refs could
// leave them unenriched or map enrichments onto foreign items;
// enrichBatch ignores refs past the actual batch length.
func sweepLLM(t *testing.T) llm.Provider {
	return llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
		if req.Template != "enrich" || req.Model != "claude-haiku-5-5" {
			t.Errorf("req = template %q model %q", req.Template, req.Model)
		}
		var rows []string
		for i := 1; i <= enrichBatchSize; i++ {
			rows = append(rows, fmt.Sprintf(
				`{"ref":%d,"kind":"noticia","promotional":false,"topics":["tecnologia"],"summary_pt":"Resumo.","language":"pt"}`, i))
		}
		return llm.Response{Text: "[" + strings.Join(rows, ",") + "]"}, nil
	})
}

// unenrichedAmong counts how many of the given article ids still have
// no enrichment row — the scoped equivalent of "the sweep left pending
// work". Asserting on the whole items table is unsound while package
// test binaries share one database.
func unenrichedAmong(t *testing.T, ctx context.Context, db *store.DB, ids []int64) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items it
		 WHERE it.id = ANY($1) AND it.kind = 'article'
		   AND NOT EXISTS (SELECT 1 FROM item_enrichment e WHERE e.item_id = it.id)`,
		ids).Scan(&n); err != nil {
		t.Fatalf("pending check: %v", err)
	}
	return n
}

func TestClampWords(t *testing.T) {
	if got := clampWords("a b c", 5); got != "a b c" {
		t.Errorf("short: %q", got)
	}
	if got := clampWords("a b c d e f", 4); got != "a b c d" {
		t.Errorf("clamped: %q", got)
	}
}

// composeFixture seeds a publication with one story-bearing lineup item
// (status approved, no dossier) — the shape ensureResearch fills.
// Returns the ids the test asserts on.
func composeFixture(t *testing.T, db *store.DB, slug, ext string) (pub *store.Publication, issueID, iiID, storyID int64) {
	t.Helper()
	ctx := context.Background()
	// the shared research cache outlives a single test run on a reused
	// database — unique URLs keep this run's key space clean
	nonce := fmt.Sprintf("-%d", time.Now().UnixNano())
	slug, ext = slug+nonce, ext+nonce
	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Slug: slug, Name: "Pub",
		Sections: []config.Section{{Title: "Lead", Items: 1}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/a.xml"}},
	})
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, _ := db.Sources(ctx, pubID)
	var itemID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO items (workspace_id, source_id, external_id, url, canonical_url, title, content_text, kind)
		 VALUES ($1,$2,$3,$4,$4,$5,'O regulador abriu consulta pública sobre IA.','article') RETURNING id`,
		db.Workspace(), srcs[0].ID, ext, "https://x.example/"+ext, "Regulador abre consulta").Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	storyID, err = db.CreateStory(ctx, pubID, "Consulta do regulador", 1, "https://x.example/"+ext)
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	if err := db.AddStoryItem(ctx, storyID, itemID); err != nil {
		t.Fatalf("AddStoryItem: %v", err)
	}
	if err := db.SetStoryTriage(ctx, storyID, 9.0, "top"); err != nil {
		t.Fatalf("SetStoryTriage: %v", err)
	}
	issueID, err = db.CreateIssue(ctx, pubID, 1, "s", "", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	iiID, err = db.CreateIssueItem(ctx, store.IssueItem{
		IssueID: issueID, StoryID: storyID, Section: "Lead",
		Score: 9, Status: "approved",
	})
	if err != nil {
		t.Fatalf("CreateIssueItem: %v", err)
	}
	pub, err = db.GetPublication(ctx, slug)
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	return pub, issueID, iiID, storyID
}

// TestComposeSkeletonResearchPrefill exercises ensureResearch inside
// ComposeSkeleton end-to-end (the piece QA flagged): with a Researcher
// wired, each dossier-less pauta gets one paid research call, the
// dossier lands, ai_draft marks the item and the AI draft body reaches
// the stored issue markdown. A second compose is a free shared-cache
// hit — no new call, no new paid action.
func TestComposeSkeletonResearchPrefill(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()
	pub, issueID, iiID, storyID := composeFixture(t, db, "rs-pub", "rs-1")

	var calls int
	runner := &Runner{DB: db, Research: &Researcher{
		Pool: pool, Model: "claude-haiku-5-5",
		LLM: llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
			calls++
			if req.Template != "research" || req.Model != "claude-haiku-5-5" {
				t.Errorf("req = template %q model %q", req.Template, req.Model)
			}
			return llm.Response{Text: `{
				"summary":"Resumo da pauta.",
				"facts":[{"text":"Consulta aberta.","ref":1}],
				"open_questions":[],
				"draft_body":"O regulador abriu consulta pública sobre IA [1].",
				"sources":[{"ref":1,"url":"https://x.example/rs-1","name":"X"}]
			}`}, nil
		}),
	}}
	res, err := runner.ComposeSkeleton(ctx, pub, issueID)
	if err != nil {
		t.Fatalf("ComposeSkeleton: %v", err)
	}
	if res.Selected != 1 || calls != 1 {
		t.Fatalf("selected=%d calls=%d, want 1/1", res.Selected, calls)
	}
	if n, _ := db.PaidActionCount(ctx, store.PaidActionResearch, storyID); n != 1 {
		t.Fatalf("research paid actions = %d, want 1", n)
	}
	ii, err := db.GetIssueItem(ctx, iiID)
	if err != nil || !ii.AIDraft {
		t.Fatalf("ai_draft after compose: %+v %v", ii, err)
	}
	dj, err := db.Dossier(ctx, iiID)
	if err != nil || !strings.Contains(dj, "Resumo da pauta") {
		t.Fatalf("research dossier: %v %q", err, dj)
	}
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !strings.Contains(iss.BodyMD, "O regulador abriu consulta pública sobre IA [1].") {
		t.Fatalf("issue body lacks the AI draft:\n%s", iss.BodyMD)
	}
	if !strings.Contains(iss.BodyMD, "**Sources:**") {
		t.Fatalf("issue body lacks sources:\n%s", iss.BodyMD)
	}

	// Second compose: every item has a dossier, so ensureResearch does
	// nothing — no call, no paid action. The shared cache is what makes
	// the second newsletter free.
	res, err = runner.ComposeSkeleton(ctx, pub, issueID)
	if err != nil || res.Selected != 1 {
		t.Fatalf("second compose: %v %+v", err, res)
	}
	if calls != 1 {
		t.Fatalf("cached research re-called the model: %d", calls)
	}
	if n, _ := db.PaidActionCount(ctx, store.PaidActionResearch, storyID); n != 1 {
		t.Fatalf("paid actions after cache hit = %d, want 1", n)
	}
}

// TestComposeSkeletonResearchDegraded: a provider refusal inside
// ensureResearch writes no research dossier (owner D4 — no
// "unavailable" placeholders). ensureManualDossiers fills the pauta
// with the deterministic dossier — real sourced material, no
// placeholder text — and the slot stays manual: no ai_draft, no AI
// text in the body, no paid action.
func TestComposeSkeletonResearchDegraded(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()
	pub, issueID, iiID, storyID := composeFixture(t, db, "rs-pub2", "rs-2")

	runner := &Runner{DB: db, Research: &Researcher{
		Pool: pool, Model: "claude-haiku-5-5", LLM: llm.None{},
	}}
	if _, err := runner.ComposeSkeleton(ctx, pub, issueID); err != nil {
		t.Fatalf("ComposeSkeleton: %v", err)
	}
	ii, err := db.GetIssueItem(ctx, iiID)
	if err != nil || ii.AIDraft {
		t.Fatalf("degraded pauta marked ai_draft: %+v %v", ii, err)
	}
	dj, err := db.Dossier(ctx, iiID)
	if err != nil {
		t.Fatalf("degraded pauta must keep a deterministic dossier: %v", err)
	}
	if strings.Contains(strings.ToLower(dj), "indispon") || strings.Contains(strings.ToLower(dj), "unavailable") {
		t.Fatalf("placeholder text in dossier: %q", dj)
	}
	var d Dossier
	if err := json.Unmarshal([]byte(dj), &d); err != nil {
		t.Fatalf("dossier json: %v", err)
	}
	if d.Origin != "manual" {
		t.Fatalf("degraded dossier origin = %q, want manual (deterministic fill)", d.Origin)
	}
	if len(d.Sources) == 0 {
		t.Fatal("deterministic dossier lost its sources")
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if strings.Contains(strings.ToLower(iss.BodyMD), "indispon") ||
		strings.Contains(strings.ToLower(iss.BodyMD), "unavailable") {
		t.Fatalf("placeholder text leaked into the issue body:\n%s", iss.BodyMD)
	}
	if n, _ := db.PaidActionCount(ctx, store.PaidActionResearch, storyID); n != 0 {
		t.Fatalf("degraded path recorded a paid action: %d", n)
	}
}
