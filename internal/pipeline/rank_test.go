package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

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
	s1, _ := scoreStories(stories)
	s2, _ := scoreStories(stories)
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
	scores, reasons := scoreStories(stories)
	for i := 0; i < 4; i++ {
		if scores[i] >= scores[4] {
			t.Fatalf("non-editorial story %d scored %.1f ≥ real story %.1f", i, scores[i], scores[4])
		}
		if !strings.Contains(reasons[i], "non-editorial") {
			t.Errorf("demoted story %d reason lacks the marker: %q", i, reasons[i])
		}
	}
	if strings.Contains(reasons[4], "non-editorial") {
		t.Errorf("real story wrongly demoted: %q", reasons[4])
	}
	picks := pickLineup(stories, scores, reasons,
		[]config.Section{{Title: "Lead", Items: 1, Commentary: "none"}})
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
	scores, reasons := scoreStories(stories)
	picks := pickLineup(stories, scores, reasons, []config.Section{
		{Title: "The big one", Items: 1},
		{Title: "Worth knowing", Items: 1},
		{Title: "Quick links", Items: 1},
	})
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
	scores, reasons := scoreStories(stories)
	picks := pickLineup(stories, scores, reasons, sections)
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
