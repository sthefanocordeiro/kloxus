package pipeline

// KLOX-67 tests: enrichment normalization, the shared research cache
// paths, paid-action caps, the degraded fallback, and the
// enrichment-aware free ranking. All replay/deterministic — no paid
// provider calls (owner freeze).

import (
	"context"
	"strings"
	"testing"

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
	if res.Summary != ResearchUnavailable {
		t.Errorf("summary = %q", res.Summary)
	}
	if len(res.Facts) == 0 || res.Facts[0].Text != "Primeira frase da fonte" {
		t.Errorf("degraded facts = %+v", res.Facts)
	}
	if len(res.Sources) != 1 || res.Sources[0].ItemID != 1 {
		t.Errorf("sources = %+v", res.Sources)
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
// budget) returns the degraded payload, never an error — §7.3.
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
	if outcome != researchDegraded || res.Summary != ResearchUnavailable {
		t.Fatalf("outcome=%v summary=%q", outcome, res.Summary)
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
		1: {ItemID: 1, Kind: "noticia", Topics: []string{"tecnologia"}},  // +1 kind, +2 topic
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

// TestEnricherSweep is the DB-backed batch path: new articles get one
// batch call, the rows persist shared, and a second sweep finds nothing
// left — idempotent per §7.2. Skips without DATABASE_URL.
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
	var ids []int64
	for _, ext := range []string{"es-a", "es-b"} {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind)
			 VALUES ($1,$2,$3,$4,$5,'body','article') RETURNING id`,
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
	n, err := enr.Sweep(ctx, 40)
	if err != nil || n != 2 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	if calls != 1 {
		t.Fatalf("2 items must batch into 1 call, got %d", calls)
	}
	m, err := db.EnrichmentsForItems(ctx, ids)
	if err != nil || len(m) != 2 {
		t.Fatalf("enrichments: %v len=%d", err, len(m))
	}
	var sawExcluded bool
	for _, e := range m {
		sawExcluded = sawExcluded || e.Excluded()
	}
	if !sawExcluded {
		t.Error("the 'lista' row should be excluded")
	}
	// Idempotent: everything is enriched — the next sweep does nothing.
	if n, err := enr.Sweep(ctx, 40); err != nil || n != 0 || calls != 1 {
		t.Fatalf("second sweep: n=%d calls=%d err=%v", n, calls, err)
	}
}

func TestClampWords(t *testing.T) {
	if got := clampWords("a b c", 5); got != "a b c" {
		t.Errorf("short: %q", got)
	}
	if got := clampWords("a b c d e f", 4); got != "a b c d" {
		t.Errorf("clamped: %q", got)
	}
}
