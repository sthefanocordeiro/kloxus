package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/embed"
	"github.com/sthefanocordeiro/kloxus/internal/rank"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

func TestSplitTopicRolesRecoversEditoriaAndSubjects(t *testing.T) {
	editorias, subjects := splitTopicRoles("pecuária, clima no campo, agronegócio")
	if editorias != "agronegócio" || subjects != "pecuária, clima no campo" {
		t.Fatalf("roles = (%q, %q)", editorias, subjects)
	}
	editorias, subjects = splitTopicRoles("pecuária, clima no campo")
	if editorias != "" || subjects != "pecuária, clima no campo" {
		t.Fatalf("subject-only roles = (%q, %q)", editorias, subjects)
	}
	editorias, subjects = splitTopicRoles("AGRONEGOCIO")
	if editorias != "agronegócio" || subjects != "" {
		t.Fatalf("editoria-only roles = (%q, %q)", editorias, subjects)
	}
}

func TestEditorialCategoryBoostBranches(t *testing.T) {
	story := detStory(1, "Safra tem nova projeção", "2026-01-01T00:00:00Z", 1)
	story.Items[0].Categories = []string{"Agronegócio"}

	both := &store.Publication{Topic: "pecuária, agronegócio"}
	if editorialCategoryBoost(both, &story, nil) {
		t.Fatal("category alone satisfied editoria+subjects conjunction")
	}
	subjectStory := detStory(2, "Pecuária tem nova projeção", "2026-01-01T00:00:00Z", 1)
	subjectStory.Items[0].Categories = []string{"Agronegócio"}
	if !editorialCategoryBoost(both, &subjectStory, nil) {
		t.Fatal("category plus typed-subject evidence should satisfy conjunction")
	}

	editoriaOnly := &store.Publication{Topic: "agronegócio"}
	if !editorialCategoryBoost(editoriaOnly, &story, nil) {
		t.Fatal("editoria-only radar should allow a compatible category as sufficient evidence")
	}

	noEditoria := &store.Publication{Topic: "pecuária"}
	subjectCategory := detStory(3, "Safra tem nova projeção", "2026-01-01T00:00:00Z", 1)
	subjectCategory.Items[0].Categories = []string{"Pecuária"}
	if editorialCategoryBoost(noEditoria, &subjectCategory, nil) {
		t.Fatal("category must not become editorial evidence when no editoria is marked")
	}
}

func TestCategoryAloneDoesNotBoostHeuristicWhenSubjectsExist(t *testing.T) {
	withCategory := detStory(1, "Safra tem nova projeção", "2025-01-05", 1)
	withCategory.Items[0].Categories = []string{"Agronegócio"}
	withoutCategory := detStory(2, "Safra tem nova projeção", "2025-01-05", 1)
	pub := &store.Publication{Language: "pt-BR", Topic: "pecuária, agronegócio"}
	scores, reasons := scoreStoriesEnriched([]store.Story{withCategory, withoutCategory}, "pt-BR", pub, nil)
	if scores[0] != scores[1] {
		t.Fatalf("category alone changed heuristic score with editoria+subjects: %v", scores)
	}
	if reasons[0] != reasons[1] {
		t.Fatalf("category alone changed heuristic reason: %q / %q", reasons[0], reasons[1])
	}
}

func TestCategoryBoostHeuristicWhenSubjectEvidenceExists(t *testing.T) {
	withCategory := detStory(1, "Pecuária tem nova projeção", "2025-01-05", 1)
	withCategory.Items[0].Categories = []string{"Agronegócio"}
	withoutCategory := detStory(2, "Pecuária tem nova projeção", "2025-01-05", 1)
	pub := &store.Publication{Language: "pt-BR", Topic: "pecuária, agronegócio"}
	scores, reasons := scoreStoriesEnriched([]store.Story{withCategory, withoutCategory}, "pt-BR", pub, nil)
	if got, want := scores[0]-scores[1], categoryMatchBoost; got != want {
		t.Fatalf("category+subject delta=%v want %v (scores %v)", got, want, scores)
	}
	if !strings.Contains(reasons[0], "categoria da editoria") || strings.Contains(reasons[1], "categoria da editoria") {
		t.Fatalf("category reasons wrong: %q / %q", reasons[0], reasons[1])
	}
}

func TestCategoryDoesNotBoostHeuristicWithoutEditoria(t *testing.T) {
	withCategory := detStory(1, "Safra tem nova projeção", "2025-01-05", 1)
	withCategory.Items[0].Categories = []string{"Pecuária"}
	withoutCategory := detStory(2, "Safra tem nova projeção", "2025-01-05", 1)
	pub := &store.Publication{Language: "pt-BR", Topic: "pecuária"}
	scores, _ := scoreStoriesEnriched([]store.Story{withCategory, withoutCategory}, "pt-BR", pub, nil)
	if scores[0] != scores[1] {
		t.Fatalf("category changed heuristic score without editoria: %v", scores)
	}
}

func TestCategoryConjunctionRejectStillWins(t *testing.T) {
	story := detStory(1, "Pecuária tem nova projeção", "2025-01-05", 1)
	story.Items[0].Categories = []string{"Agronegócio"}
	pub := &store.Publication{Language: "pt-BR", Topic: "pecuária, agronegócio", RejectTerms: "agronegócio"}
	scores, reasons := scoreStoriesEnriched([]store.Story{story}, "pt-BR", pub, nil)
	if scores[0] > -90 {
		t.Fatalf("reject guard did not dominate conjunction boost: %.1f", scores[0])
	}
	if !strings.Contains(reasons[0], "rejeitada pela linha editorial") {
		t.Fatalf("reject marker missing: %q", reasons[0])
	}
}

func TestStage1CategoryConjunctionDoesNotClearOffTopicWithoutSubjectEvidence(t *testing.T) {
	pool := testdb.Pool(t)
	db := testdb.WSOn(t, pool)
	ctx := context.Background()
	emb := categoryTestEmbedder{}

	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Slug: "cat-conjunction-pub", Name: "Agro Radar", Language: "pt-BR", Topic: "pecuária, agronegócio",
		Sections: []config.Section{{Title: "Lead", Items: 1}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixture.xml"}},
	})
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "cat-conjunction-pub")
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
		ExternalID: "cat-conjunction-hidden", URL: "https://example.com/agro", Title: "Safra tem nova projeção",
		PublishedAt: "2026-11-25T10:00:00Z", ContentText: "texto da safra",
	}}, func(u string) string { return u }); err != nil {
		t.Fatalf("SaveItems: %v", err)
	}
	var itemID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM items WHERE external_id = 'cat-conjunction-hidden'`).Scan(&itemID); err != nil {
		t.Fatalf("item id: %v", err)
	}
	orthogonal := categoryUnitVector(emb.Dims(), 1)
	if err := pool.SaveItemEmbeddings(ctx, emb.Model(), emb.Dims(), []embed.Vector{{ItemID: itemID, Literal: embed.Literal(orthogonal)}}); err != nil {
		t.Fatalf("SaveItemEmbeddings: %v", err)
	}

	story := detStory(1, "Safra tem nova projeção", "2026-11-25T10:00:00Z", 1)
	story.Items[0].ID = itemID

	categoryOnlyStory := story
	categoryOnlyStory.ID = 2
	categoryOnlyStory.Items = append([]store.Item(nil), story.Items...)
	categoryOnlyStory.Items[0].Categories = []string{"AGRONEGOCIO"}

	bothStory := categoryOnlyStory
	bothStory.ID = 3
	bothStory.Title = "Pecuária tem nova projeção"
	bothStory.Items = append([]store.Item(nil), categoryOnlyStory.Items...)
	bothStory.Items[0].Title = "Pecuária tem nova projeção"

	r := &Runner{DB: db, Embedder: emb}
	ranked := r.rankStage1(ctx, pub, []store.Story{story, categoryOnlyStory, bothStory}, []string{"", "", ""}, nil)
	if ranked.Hidden[0] != rank.HiddenOffTopic {
		t.Fatalf("uncategorized orthogonal story hidden=%q want off_topic", ranked.Hidden[0])
	}
	if ranked.Hidden[1] != rank.HiddenOffTopic {
		t.Fatalf("category alone cleared off_topic despite editoria+subjects: hidden=%q", ranked.Hidden[1])
	}
	if got := ranked.Scores[1] - ranked.Scores[0]; got != 0 {
		t.Fatalf("category alone changed stage1 score by %v", got)
	}
	if ranked.Hidden[2] != rank.HiddenNone {
		t.Fatalf("category plus subject evidence did not clear off_topic: hidden=%q", ranked.Hidden[2])
	}
	if got := ranked.Scores[2] - ranked.Scores[0]; got < categoryMatchBoost-0.001 || got > categoryMatchBoost+0.001 {
		t.Fatalf("category+subject score delta=%v want about %v", got, categoryMatchBoost)
	}
}
