package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

func detStory(id int64, title, published string, n int) store.Story {
	s := store.Story{ID: id, Title: title}
	for i := 0; i < n; i++ {
		s.Items = append(s.Items, store.Item{
			PublishedAt: published,
			SourceURL:   fmt.Sprintf("https://src%d.example/feed", i),
			SourceName:  fmt.Sprintf("src%d", i),
		})
	}
	return s
}

func TestScoreStoriesStableOrder(t *testing.T) {
	stories := []store.Story{
		detStory(3, "old", "2025-01-01", 1),
		detStory(1, "new multi", "2025-01-03", 3),
		detStory(2, "new single", "2025-01-03", 1),
	}
	s1, _ := scoreStories(stories, "")
	s2, _ := scoreStories(stories, "")
	o1 := storyOrder(stories, s1)
	o2 := storyOrder(stories, s2)
	for i := range o1 {
		if o1[i] != o2[i] {
			t.Fatalf("unstable order: %v vs %v", o1, o2)
		}
	}
	// newest + most covered first; on equal recency the multi-source wins.
	if o1[0] != 1 {
		t.Fatalf("order = %v, want index 1 (new multi) first", o1)
	}
	if o1[len(o1)-1] != 0 {
		t.Fatalf("order = %v, want index 0 (old) last", o1)
	}
}

// TestNonEditorialDemotion is the KLOX-55 noise contract: release
// lists, where-to-watch guides and the outlet's own supporter campaign
// sink below real coverage even when newer — llm_mode=off has no
// triage model to weigh them, so the demotion is deterministic.
func TestNonEditorialDemotion(t *testing.T) {
	stories := []store.Story{
		detStory(1, "Onde assistir ao clássico deste sábado", "2025-01-05", 1),
		detStory(2, "Estreias da semana: o que chega ao streaming", "2025-01-05", 3),
		detStory(3, "Apoie o src0: jornalismo independente precisa de você", "2025-01-05", 1),
		detStory(4, "Where to watch the final this Sunday", "2025-01-05", 1),
		detStory(5, "Regulador abre consulta pública sobre IA", "2025-01-01", 1),
	}
	// member-headline match: a clean story title can't hide a noisy
	// member item (the feed headline carries the flag too)
	stories[0].Title = "Cobertura completa da rodada"
	stories[0].Items[0].Title = "Onde assistir aos jogos da rodada"
	scores, reasons := scoreStories(stories, "")
	for i := 0; i < 4; i++ {
		if scores[i] >= scores[4] {
			t.Fatalf("non-editorial story %d scored %.1f ≥ real story %.1f", i, scores[i], scores[4])
		}
		if !strings.Contains(reasons[i], "off the editorial line") {
			t.Errorf("demoted story %d reason lacks the marker: %q", i, reasons[i])
		}
	}
	if strings.Contains(reasons[4], "off the editorial line") {
		t.Errorf("real story wrongly demoted: %q", reasons[4])
	}
	picks := pickLineup(stories, scores, reasons,
		&store.Publication{Sections: []config.Section{{Title: "Lead", Items: 1, Commentary: "none"}}}, nil)
	if len(picks) != 1 || picks[0].Refs[0] != 5 {
		t.Fatalf("picks = %+v, want only the real story", picks)
	}
}

// TestPickLineupSkipsSignalOnlyStory is the KLOX-60 regression: the
// highest-scored candidate for the lead section is a signal-only
// cluster (a bare HN link) — it ranks, but it can never produce an
// issue item. Before the fix it took the lead pick, the section ended
// with zero items while the others filled, and apiDraft refused the
// issue with 409 section_empty.
func TestPickLineupSkipsSignalOnlyStory(t *testing.T) {
	stories := []store.Story{
		{ID: 1, Title: "signal-only link", Items: []store.Item{{
			Kind: "signal", PublishedAt: "2025-01-05",
			SourceURL: "https://hn.example/item/1", SourceName: "hn",
		}}},
		detStory(2, "best article", "2025-01-04", 1),
		detStory(3, "second article", "2025-01-03", 1),
		detStory(4, "third article", "2025-01-02", 1),
	}
	scores, reasons := scoreStories(stories, "")
	picks := pickLineup(stories, scores, reasons, &store.Publication{Sections: []config.Section{
		{Title: "The big one", Items: 1},
		{Title: "Worth knowing", Items: 1},
		{Title: "Quick links", Items: 1},
	}}, nil)
	if len(picks) != 3 {
		t.Fatalf("picks = %d, want 3 — every section gets a draftable story", len(picks))
	}
	for _, p := range picks {
		if p.Refs[0] == 1 {
			t.Fatalf("signal-only story slotted into %q — it yields no issue item", p.Section)
		}
	}
	if picks[0].Section != "The big one" || picks[0].Refs[0] != 2 {
		t.Fatalf("lead pick = %+v, want story 2 (best draftable) in The big one", picks[0])
	}
}

// TestScoreStoriesPTReasons: the deterministic rank reason is
// reader-facing — a pt-BR publication reads pt strings, English stays
// the default (KLOX-56).
func TestScoreStoriesPTReasons(t *testing.T) {
	stories := []store.Story{
		detStory(1, "Pauta sem data", "", 2),
	}
	_, en := scoreStories(stories, "")
	if en[0] != "undated · 2 outlets" {
		t.Fatalf("en reason = %q", en[0])
	}
	_, pt := scoreStories(stories, "pt-BR")
	if pt[0] != "sem data · 2 veículos" {
		t.Fatalf("pt reason = %q", pt[0])
	}
	if strings.Contains(pt[0], "source") {
		t.Fatalf("pt reason leaked English: %q", pt[0])
	}
}

// TestScoreStoriesReasonFormat: the deterministic rank reason reads
// "há 3 h · 2 veículos" — relative freshness, proper plural, never
// "fonte(s)" or a raw "ranked:" prefix (KLOX-64).
func TestScoreStoriesReasonFormat(t *testing.T) {
	fresh := detStory(1, "Pauta recente", time.Now().Add(-3*time.Hour).UTC().Format(time.RFC3339), 1)
	_, pt := scoreStories([]store.Story{fresh}, "pt-BR")
	if pt[0] != "há 3 h · 1 veículo" {
		t.Fatalf("pt reason = %q, want %q", pt[0], "há 3 h · 1 veículo")
	}
	_, en := scoreStories([]store.Story{fresh}, "")
	if en[0] != "3 h ago · 1 outlet" {
		t.Fatalf("en reason = %q, want %q", en[0], "3 h ago · 1 outlet")
	}
	plural := detStory(2, "Pauta recente", time.Now().Add(-3*time.Hour).UTC().Format(time.RFC3339), 2)
	_, pt2 := scoreStories([]store.Story{plural}, "pt-BR")
	if pt2[0] != "há 3 h · 2 veículos" {
		t.Fatalf("pt plural reason = %q, want %q", pt2[0], "há 3 h · 2 veículos")
	}
}

func TestDedupeKey(t *testing.T) {
	same := []store.Item{
		{URL: "https://a.example/x?utm_source=rss", CanonicalURL: "https://a.example/x"},
		{URL: "https://a.example/x", CanonicalURL: "https://a.example/x"},
	}
	if got := dedupeKey(same); got != "https://a.example/x" {
		t.Fatalf("dedupeKey = %q", got)
	}
	diff := []store.Item{
		{URL: "https://a.example/x", CanonicalURL: "https://a.example/x"},
		{URL: "https://b.example/y", CanonicalURL: "https://b.example/y"},
	}
	if got := dedupeKey(diff); got != "" {
		t.Fatalf("dedupeKey for text cluster = %q, want empty", got)
	}
}

func TestPickLineupSectionCapsAndOutletCap(t *testing.T) {
	sections := []config.Section{
		{Title: "Lead", Items: 1, Commentary: "none"},
		{Title: "Radar", Items: 2, Commentary: "none"},
	}
	mk := func(id int64, pub string, outlet string) store.Story {
		return store.Story{ID: id, Title: pub, Items: []store.Item{{
			PublishedAt: pub, SourceURL: "https://" + outlet + "/feed", SourceName: outlet,
		}}}
	}
	stories := []store.Story{
		mk(1, "2025-01-05", "a.example"),
		mk(2, "2025-01-04", "a.example"),
		mk(3, "2025-01-03", "a.example"),
		mk(4, "2025-01-02", "b.example"),
	}
	scores, reasons := scoreStories(stories, "")
	picks := pickLineup(stories, scores, reasons, &store.Publication{Sections: sections}, nil)
	if len(picks) != 3 {
		t.Fatalf("picks = %d, want 3 (capacity 1+2)", len(picks))
	}
	// All three a.example stories outrank b.example on recency, but the
	// outlet cap lets at most two through on the first pass — the last
	// slot is filled in the relaxed pass.
	outlets := map[string]int{}
	for _, p := range picks {
		outlets[primaryOutlet(&stories[p.Refs[0]-1])]++
	}
	if picks[0].Section != "Lead" || picks[1].Section != "Radar" {
		t.Fatalf("sections = %s/%s, want Lead then Radar", picks[0].Section, picks[1].Section)
	}
	if picks[0].Refs[0] != 1 {
		t.Fatalf("lead pick = ref %d, want 1 (highest score)", picks[0].Refs[0])
	}
}

// TestDeterministicRunMakesNoModelCalls is the cost-safety proof: a full
// collect→dedupe→rank→lineup→draft run with a provider that fails the
// test if Complete is ever invoked.
func TestDeterministicRunMakesNoModelCalls(t *testing.T) {
	ctx := context.Background()
	db := testdb.WS(t)

	def := &config.Publication{
		Slug: "det", Name: "Det Pub", Language: "pt-BR",
		Sections: []config.Section{{Title: "Lead", Items: 2, Commentary: "none"}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/det.xml"}},
	}
	if _, err := db.SeedPublication(ctx, def); err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "det")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	feed := `<?xml version="1.0"?>
<rss version="2.0"><channel><title>D</title>
<item><title>Regulador abre consulta</title><link>https://a.example/1?utm_source=feed</link><guid>g1</guid>
<pubDate>Thu, 13 Nov 2025 10:00:00 +0000</pubDate>
<description>O regulador abriu consulta pública sobre IA. Prazo termina em dezembro.</description></item>
<item><title>Regulador abre consulta (republished)</title><link>https://a.example/1</link><guid>g2</guid>
<pubDate>Thu, 13 Nov 2025 11:00:00 +0000</pubDate>
<description>A consulta pública sobre IA foi replicada por outro feed.</description></item>
<item><title>Nova linha de crédito</title><link>https://b.example/2</link><guid>g3</guid>
<pubDate>Wed, 12 Nov 2025 09:00:00 +0000</pubDate>
<description>Banco anunciou linha de crédito para pequenas empresas.</description></item>
</channel></rss>`
	fsys := fstest.MapFS{"fixtures/feeds/det.xml": {Data: []byte(feed)}}
	fetcher := sources.FixtureFetcher{FS: fsys}
	provider := llm.Func(func(context.Context, llm.Request) (llm.Response, error) {
		t.Fatal("model call attempted in deterministic mode")
		return llm.Response{}, nil
	})
	res, err := RunHeadless(ctx, db, pub, t.TempDir(), fetcher, provider, "haiku", "sonnet", 3, false, true)
	if err != nil {
		t.Fatalf("RunHeadless: %v", err)
	}
	if res.CostUSD != 0 {
		t.Fatalf("CostUSD = %v, want 0", res.CostUSD)
	}
	// items 1+2 share a canonical URL (tracking param stripped) → one story.
	if res.Stories != 2 || res.Selected != 2 {
		t.Fatalf("stories=%d selected=%d, want 2/2", res.Stories, res.Selected)
	}
	stories, err := db.StoriesWithItems(ctx, pub.ID)
	if err != nil {
		t.Fatalf("StoriesWithItems: %v", err)
	}
	var keyed int
	for _, s := range stories {
		if s.DedupeKey == "https://a.example/1" {
			keyed++
			if len(s.Items) != 2 {
				t.Fatalf("canonical story has %d items, want 2", len(s.Items))
			}
		}
		if s.TriageScore <= 0 {
			t.Fatalf("story %d score = %v, want > 0", s.ID, s.TriageScore)
		}
	}
	if keyed != 1 {
		t.Fatalf("stories with dedupe_key = %d, want 1", keyed)
	}
	iss, err := db.GetIssue(ctx, res.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "drafted" {
		t.Fatalf("issue status = %q, want drafted", iss.Status)
	}
	if !strings.Contains(iss.BodyMD, "[1]") {
		t.Fatalf("draft body lacks citations:\n%s", iss.BodyMD)
	}
}

// TestRejectTermsDemoteAndSkip is the KLOX-94 editorial-line contract:
// a story hitting the newsletter's reject terms sinks below every real
// candidate with a marked reason, and the auto-lineup never slots it —
// it stays a visible bench candidate instead of silently dropping.
func TestRejectTermsDemoteAndSkip(t *testing.T) {
	stories := []store.Story{
		detStory(1, "Novo celular dobrável chega às lojas", "2025-01-05", 1),
		detStory(2, "Banco Central abre consulta sobre Pix", "2025-01-05", 1),
		detStory(3, "Games: GTA VI ganha nova data de lançamento", "2025-01-04", 1),
		detStory(4, "Fintechs respondem a nova regra do BC", "2025-01-03", 1),
	}
	pub := &store.Publication{
		Language:    "pt-BR",
		RejectTerms: "celular, games",
		Sections:    []config.Section{{Title: "Fintech", Items: 2, Topic: "fintech, banco central"}},
	}
	scores, reasons := scoreStoriesEnriched(stories, "pt-BR", pub, nil)
	if scores[0] >= scores[1] || scores[2] >= scores[3] {
		t.Fatalf("reject stories not demoted: %v", scores)
	}
	if !strings.Contains(reasons[0], "rejeitada pela linha editorial") ||
		!strings.Contains(reasons[2], "rejeitada pela linha editorial") {
		t.Fatalf("reject marker missing: %q / %q", reasons[0], reasons[2])
	}
	picks := pickLineup(stories, scores, reasons, pub, nil)
	for _, p := range picks {
		if p.Refs[0] == 1 || p.Refs[0] == 3 {
			t.Fatalf("rejected story %d auto-slotted: %+v", p.Refs[0], picks)
		}
	}
	// the topic-matched fintech stories fill the section — and the
	// strongest match leads
	if len(picks) != 2 || picks[0].Refs[0] != 2 || picks[1].Refs[0] != 4 {
		t.Fatalf("picks = %+v, want stories 2 then 4 in Fintech", picks)
	}
	if !strings.Contains(picks[0].Why, "no tema da seção") {
		t.Fatalf("section-topic reason marker missing: %q", picks[0].Why)
	}
}

// TestRejectMemberHeadline: a clean story title can't hide a rejected
// member headline — same rule as the non-editorial demotion.
func TestRejectMemberHeadline(t *testing.T) {
	s := detStory(1, "Resumo da semana", "2025-01-05", 1)
	s.Items[0].Title = "Review: o novo celular dobrável"
	pub := &store.Publication{RejectTerms: "celular"}
	scores, reasons := scoreStoriesEnriched([]store.Story{s}, "", pub, nil)
	if !strings.Contains(reasons[0], "rejected by the editorial line") {
		t.Fatalf("member-headline reject missed: %q (score %.1f)", reasons[0], scores[0])
	}
}

// TestSectionTopicPrefersMatch: an older on-topic story beats a newer
// off-topic one for its section, while a section without a topic keeps
// global order.
func TestSectionTopicPrefersMatch(t *testing.T) {
	stories := []store.Story{
		detStory(1, "iPhone dobrável vazou", "2025-01-06", 1),
		detStory(2, "Banco Central corta juros", "2025-01-02", 1),
	}
	pub := &store.Publication{
		Language: "en",
		Sections: []config.Section{
			{Title: "Economy", Items: 1, Topic: "banco central, juros, fintech"},
			{Title: "Gadgets", Items: 1, Topic: "iphone, celular, gadget"},
		},
	}
	scores, reasons := scoreStoriesEnriched(stories, "en", pub, nil)
	picks := pickLineup(stories, scores, reasons, pub, nil)
	if len(picks) != 2 || picks[0].Refs[0] != 2 || picks[1].Refs[0] != 1 {
		t.Fatalf("picks = %+v, want story 2 in Economy, story 1 in Gadgets", picks)
	}
	// no topic declared anywhere → global order stands
	pub.Sections[0].Topic = ""
	pub.Sections[1].Topic = ""
	picks = pickLineup(stories, scores, reasons, pub, nil)
	if len(picks) != 2 || picks[0].Refs[0] != 1 {
		t.Fatalf("topic-less picks = %+v, want story 1 first", picks)
	}
}

// TestPublicationTopicBonus: the newsletter theme lifts direct title
// hits even without enrichment rows (KLOX-94 free stand-in).
func TestPublicationTopicBonus(t *testing.T) {
	stories := []store.Story{
		detStory(1, "Nova regra muda o mercado", "2025-01-05", 1),
		detStory(2, "Fintechs comemoram decisão do BC", "2025-01-05", 1),
	}
	pub := &store.Publication{Topic: "fintech e regulação"}
	scores, _ := scoreStoriesEnriched(stories, "", pub, nil)
	if scores[1] <= scores[0] {
		t.Fatalf("theme hit not rewarded: %v", scores)
	}
}

func TestParseTermsAndSuggestSection(t *testing.T) {
	terms := parseTerms("celular e games, banco central")
	if !termsAny(terms, "novo celular dobravel chega", foldedTokens("novo celular dobravel chega")) {
		t.Fatal("celular should hit")
	}
	if !termsAny(terms, "banco central corta juros", foldedTokens("banco central corta juros")) {
		t.Fatal("banco central should hit")
	}
	if termsAny(terms, "gamescom abre inscricoes", foldedTokens("gamescom abre inscricoes")) {
		t.Fatal("phrase 'games' should not hit inside 'gamescom'")
	}
	secs := []config.Section{
		{Title: "Gadgets", Topic: "iphone, celular"},
		{Title: "Economy", Topic: "banco central, fintech"},
	}
	if got := SuggestSection("Banco Central abre consulta", secs); got != "Economy" {
		t.Fatalf("SuggestSection = %q, want Economy", got)
	}
	if got := SuggestSection("Receita de bolo de fubá", secs); got != "" {
		t.Fatalf("SuggestSection = %q, want empty", got)
	}
}
