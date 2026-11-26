package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/embed"
	"github.com/sthefanocordeiro/kloxus/internal/rank"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

// TestCategoryTopicBonus: publisher categories are case/accent-folded at
// matching time and lift an otherwise identical story. No category is
// simply the absence of that signal — never negative.
func TestCategoryTopicBonus(t *testing.T) {
	stories := []store.Story{
		detStory(1, "Safra tem nova projeção", "2025-01-05", 1),
		detStory(2, "Safra tem nova projeção", "2025-01-05", 1),
	}
	stories[0].Items[0].Categories = []string{"AGRONEGOCIO"}
	pub := &store.Publication{Language: "pt-BR", Topic: "agronegócio"}
	scores, reasons := scoreStoriesEnriched(stories, "pt-BR", pub, nil)
	if scores[0] != scores[1]+2 {
		t.Fatalf("category boost missing or asymmetric: %v", scores)
	}
	if !strings.Contains(reasons[0], "no tema") || strings.Contains(reasons[1], "no tema") {
		t.Fatalf("category topic reasons wrong: %q / %q", reasons[0], reasons[1])
	}
	if !categoryOnTopic(pub, &stories[0]) || categoryOnTopic(pub, &stories[1]) {
		t.Fatal("categoryOnTopic mismatch")
	}
}

// TestCategoryRejectStillWins: categories are evidence for the reject
// guard too. A category matching a declared reject term sinks the story
// even when the same category also matches the positive theme vocabulary.
func TestCategoryRejectStillWins(t *testing.T) {
	s := detStory(1, "Safra tem nova projeção", "2025-01-05", 1)
	s.Items[0].Categories = []string{"Agronegócio"}
	pub := &store.Publication{Language: "pt-BR", Topic: "agronegócio", RejectTerms: "agronegócio"}
	scores, reasons := scoreStoriesEnriched([]store.Story{s}, "pt-BR", pub, nil)
	if scores[0] > -90 {
		t.Fatalf("reject guard did not dominate the category boost: %.1f", scores[0])
	}
	if !strings.Contains(reasons[0], "rejeitada pela linha editorial") {
		t.Fatalf("reject marker missing: %q", reasons[0])
	}
}

// TestCategoryNonEditorialStillWins: a matching category adds evidence,
// but the non-editorial feed-noise demotion is larger and continues to
// sink release/list-guide content below ordinary coverage.
func TestCategoryNonEditorialStillWins(t *testing.T) {
	noise := detStory(1, "Onde assistir: Corinthians x Palmeiras ao vivo neste sábado", "2025-01-05", 1)
	noise.Items[0].Categories = []string{"Agronegócio"}
	clean := detStory(2, "Safra tem nova projeção", "2025-01-05", 1)
	pub := &store.Publication{Language: "pt-BR", Topic: "agronegócio"}
	scores, _ := scoreStoriesEnriched([]store.Story{noise, clean}, "pt-BR", pub, nil)
	if scores[0] >= scores[1] {
		t.Fatalf("non-editorial guard did not dominate category boost: %v", scores)
	}
	if got := editorialPenalty(&noise, pub, nil); got != -nonEditorialPenalty {
		t.Fatalf("non-editorial penalty=%v want %v", got, -nonEditorialPenalty)
	}
}

// TestCategoryOnTopicUsesRadarTopicOnly: only the Radar's declared
// editoria/topic is the positive category vocabulary. Editorial-line prose
// must not concatenate into the topic phrase, and distinct category labels
// must not combine into a phrase neither one states.
func TestCategoryOnTopicUsesRadarTopicOnly(t *testing.T) {
	pub := &store.Publication{Topic: "agronegócio", EditorialLine: "priorize economia"}
	story := detStory(1, "x", "2026-01-01T00:00:00Z", 1)
	story.Items[0].Categories = []string{"agronegócio"}
	if !categoryOnTopic(pub, &story) {
		t.Fatal("editorial line concatenation must not stop an exact topic category match")
	}

	pub.Topic = "banco central"
	story.Items[0].Categories = []string{"banco", "central"}
	if categoryOnTopic(pub, &story) {
		t.Fatal("separate categories must not combine into a phrase neither one states")
	}
	story.Items[0].Categories = []string{"Banco Central"}
	if !categoryOnTopic(pub, &story) {
		t.Fatal("one multi-word publisher category should match a multi-word topic")
	}
}

// TestStage1CategoryBoost: with embeddings off, two otherwise identical
// stories differ only by the publisher category signal. The matching one
// receives the explicit relevance boost; the uncategorized one is neither
// excluded nor penalized. Postgres-backed because stage 1 still consults
// the FTS fallback even when every id is absent.
func TestStage1CategoryBoost(t *testing.T) {
	db := testdb.WS(t)
	stories := []store.Story{
		detStory(1, "Safra tem nova projeção", "2025-01-05", 1),
		detStory(2, "Safra tem nova projeção", "2025-01-05", 1),
	}
	stories[0].Items[0].Categories = []string{"AGRONEGOCIO"}
	pub := &store.Publication{Language: "pt-BR", Topic: "agronegócio"}
	r := &Runner{DB: db}
	base := make([]string, len(stories))
	res := r.rankStage1(context.Background(), pub, stories, base, nil)
	if got, want := res.Scores[0]-res.Scores[1], categoryMatchBoost; got != want {
		t.Fatalf("stage1 category delta=%v want %v (scores %v)", got, want, res.Scores)
	}
	if !strings.Contains(res.Reasons[0], "categoria da editoria") || strings.Contains(res.Reasons[1], "categoria da editoria") {
		t.Fatalf("stage1 category reasons wrong: %q / %q", res.Reasons[0], res.Reasons[1])
	}
}

// TestStage1CategoryRejectStillWins: the boost never punches through a
// reject-term hit carried by the same category; the -100 editorial
// penalty dominates it.
func TestStage1CategoryRejectStillWins(t *testing.T) {
	db := testdb.WS(t)
	stories := []store.Story{
		detStory(1, "Safra tem nova projeção", "2025-01-05", 1),
		detStory(2, "Safra tem nova projeção", "2025-01-05", 1),
	}
	stories[0].Items[0].Categories = []string{"Agronegócio"}
	pub := &store.Publication{Language: "pt-BR", Topic: "agronegócio", RejectTerms: "agronegócio"}
	r := &Runner{DB: db}
	res := r.rankStage1(context.Background(), pub, stories, make([]string, len(stories)), nil)
	if res.Scores[0] >= res.Scores[1] {
		t.Fatalf("reject guard did not dominate category boost: %v", res.Scores)
	}
}

type categoryTestEmbedder struct{}

func (categoryTestEmbedder) Embed(context.Context, []string, embed.InputKind) ([][]float32, error) {
	return nil, errors.New("test profile should already be cached")
}
func (categoryTestEmbedder) Model() string { return "category-test-embed" }
func (categoryTestEmbedder) Dims() int     { return 512 }

func categoryUnitVector(dim, idx int) []float32 {
	v := make([]float32, dim)
	v[idx] = 1
	return v
}

// TestStage1CategoryClearsOffTopicFloor: a publisher category matching
// the Radar editoria is independent item-level evidence, so it rescues an
// embedding-floor false negative. Without the category the same story is
// hidden off_topic; this is the "enters the possible candidates" half of
// the contract, while reject/non-editorial guards are applied elsewhere.
func TestStage1CategoryClearsOffTopicFloor(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()
	emb := categoryTestEmbedder{}

	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Slug: "cat-pub", Name: "Agro Radar", Language: "pt-BR", Topic: "agronegócio",
		Sections: []config.Section{{Title: "Lead", Items: 1}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixture.xml"}},
	})
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "cat-pub")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	theme := categoryUnitVector(emb.Dims(), 0)
	if err := db.SavePublicationProfile(ctx, pub.ID, embed.Literal(theme), "", profileHash(pub, emb.Model())); err != nil {
		t.Fatalf("SavePublicationProfile: %v", err)
	}
	srcs, err := db.Sources(ctx, pubID)
	if err != nil || len(srcs) != 1 {
		t.Fatalf("Sources: %v %v", srcs, err)
	}
	if _, err := db.SaveItems(ctx, srcs[0].ID, []sources.RawItem{{
		ExternalID: "cat-hidden", URL: "https://example.com/agro", Title: "Safra tem nova projeção",
		PublishedAt: "2026-11-25T10:00:00Z", ContentText: "texto da safra",
	}}, func(u string) string { return u }); err != nil {
		t.Fatalf("SaveItems: %v", err)
	}
	var itemID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM items WHERE external_id = 'cat-hidden'`).Scan(&itemID); err != nil {
		t.Fatalf("item id: %v", err)
	}
	orthogonal := categoryUnitVector(emb.Dims(), 1)
	if err := pool.SaveItemEmbeddings(ctx, emb.Model(), emb.Dims(), []embed.Vector{{ItemID: itemID, Literal: embed.Literal(orthogonal)}}); err != nil {
		t.Fatalf("SaveItemEmbeddings: %v", err)
	}

	story := detStory(1, "Safra tem nova projeção", "2026-11-25T10:00:00Z", 1)
	story.Items[0].ID = itemID
	r := &Runner{DB: db, Embedder: emb}
	without := r.rankStage1(ctx, pub, []store.Story{story}, []string{""}, nil)
	if without.Hidden[0] != rank.HiddenOffTopic {
		t.Fatalf("uncategorized orthogonal story hidden=%q want off_topic", without.Hidden[0])
	}
	story.Items[0].Categories = []string{"AGRONEGOCIO"}
	with := r.rankStage1(ctx, pub, []store.Story{story}, []string{""}, nil)
	if with.Hidden[0] != rank.HiddenNone {
		t.Fatalf("matching category did not clear off_topic: hidden=%q", with.Hidden[0])
	}
	if got := with.Scores[0] - without.Scores[0]; got < categoryMatchBoost-0.001 || got > categoryMatchBoost+0.001 {
		t.Fatalf("score delta=%v want about %v", got, categoryMatchBoost)
	}
}
