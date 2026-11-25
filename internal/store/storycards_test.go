package store

// KLOX-95 tests: the StoryCards aggregate feeding the suggestion
// cards — summary/kind/language from the first classified member, the
// union of member topics, the newest member's publish time, and the
// workspace scope. Skips without DATABASE_URL like every store test.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestStoryCards: member enrichments reduce to one card per story —
// the first non-empty value per field, the topic union in member
// order, the newest member timestamp as the publish time.
func TestStoryCards(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, err := db.Sources(ctx, pubID)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("Sources: %v", err)
	}
	nonce := fmt.Sprintf("card-%d", time.Now().UnixNano())
	seedItem := func(ext, title, publishedAt string) int64 {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind, published_at)
			 VALUES ($1,$2,$3,$4,$5,'body','article',$6) RETURNING id`,
			db.ws, srcs[0].ID, ext, "https://x.example/"+ext, title, publishedAt).Scan(&id); err != nil {
			t.Fatalf("insert item %s: %v", ext, err)
		}
		return id
	}

	// Story 1: three members — the first carries summary/kind/lang,
	// the second adds topics and the newer publish time, the third is
	// un-enriched and contributes nothing.
	it1 := seedItem(nonce+"-1", "Primeira", "2026-11-20T10:00:00Z")
	it2 := seedItem(nonce+"-2", "Segunda", "2026-11-22T08:30:00Z")
	it3 := seedItem(nonce+"-3", "Terceira", "2026-11-21T15:00:00Z")
	story1, err := db.CreateStory(ctx, pubID, "Pauta um", 1, "https://x.example/"+nonce+"-1")
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	for _, id := range []int64{it1, it2, it3} {
		if err := db.AddStoryItem(ctx, story1, id); err != nil {
			t.Fatalf("AddStoryItem %d: %v", id, err)
		}
	}
	// Story 2: never enriched — the card still exists because publish
	// time is item metadata, but every classified field stays empty.
	it4 := seedItem(nonce+"-4", "Quarta", "2026-11-22T09:00:00Z")
	story2, err := db.CreateStory(ctx, pubID, "Pauta dois", 2, "https://x.example/"+nonce+"-4")
	if err != nil {
		t.Fatalf("CreateStory 2: %v", err)
	}
	if err := db.AddStoryItem(ctx, story2, it4); err != nil {
		t.Fatalf("AddStoryItem story2: %v", err)
	}

	if err := pool.SaveEnrichments(ctx, []ItemEnrichment{
		{ItemID: it1, Kind: "noticia", Topics: []string{"tecnologia"}, SummaryPT: "Resumo da pauta.", Language: "pt"},
		{ItemID: it2, Kind: "analise", Topics: []string{"IA", "regulacao"}, Language: "en"},
	}); err != nil {
		t.Fatalf("SaveEnrichments: %v", err)
	}

	cards, err := db.StoryCards(ctx, []int64{story1, story2})
	if err != nil {
		t.Fatalf("StoryCards: %v", err)
	}
	c := cards[story1]
	if c == nil {
		t.Fatal("story1 has no card")
	}
	if c.Summary != "Resumo da pauta." {
		t.Errorf("summary = %q", c.Summary)
	}
	if c.Kind != "noticia" || c.Language != "pt" {
		t.Errorf("kind/language = %q/%q, want noticia/pt (first classified member)", c.Kind, c.Language)
	}
	wantTopics := []string{"tecnologia", "IA", "regulacao"}
	if len(c.Topics) != len(wantTopics) {
		t.Fatalf("topics = %v, want %v", c.Topics, wantTopics)
	}
	for i, tag := range wantTopics {
		if c.Topics[i] != tag {
			t.Fatalf("topics[%d] = %q, want %q", i, c.Topics[i], tag)
		}
	}
	if c.PublishedAt != "2026-11-22T08:30:00Z" {
		t.Errorf("published_at = %q, want the newest member's 2026-11-22T08:30:00Z", c.PublishedAt)
	}
	c2 := cards[story2]
	if c2 == nil {
		t.Fatal("story2's members must still yield a card (publish time is item data)")
	}
	if c2.Summary != "" || c2.Kind != "" || c2.Language != "" || len(c2.Topics) != 0 {
		t.Errorf("un-enriched story2 got classified fields: %+v", c2)
	}
	if c2.PublishedAt != "2026-11-22T09:00:00Z" {
		t.Errorf("story2 published_at = %q, want member time 2026-11-22T09:00:00Z", c2.PublishedAt)
	}

	// A second workspace's stories never leak into this store's map:
	// story_items carries workspace_id and StoryCards scopes on it.
	db2 := openOn(t, pool)
	pub2, err := db2.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication ws2: %v", err)
	}
	srcs2, _ := db2.Sources(ctx, pub2)
	var it5 int64
	if err := db2.QueryRowContext(ctx,
		`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind)
		 VALUES ($1,$2,$3,$4,$5,'body','article') RETURNING id`,
		db2.ws, srcs2[0].ID, nonce+"-5", "https://x.example/"+nonce+"-5", "Outra").Scan(&it5); err != nil {
		t.Fatalf("insert ws2 item: %v", err)
	}
	story3, err := db2.CreateStory(ctx, pub2, "Pauta três", 3, "https://x.example/"+nonce+"-5")
	if err != nil {
		t.Fatalf("CreateStory ws2: %v", err)
	}
	if err := db2.AddStoryItem(ctx, story3, it5); err != nil {
		t.Fatalf("AddStoryItem ws2: %v", err)
	}
	if err := pool.SaveEnrichments(ctx, []ItemEnrichment{
		{ItemID: it5, Kind: "noticia", SummaryPT: "resumo ws2", Language: "pt"},
	}); err != nil {
		t.Fatalf("SaveEnrichments ws2: %v", err)
	}
	other, err := db.StoryCards(ctx, []int64{story3})
	if err != nil {
		t.Fatalf("StoryCards cross-ws: %v", err)
	}
	if other[story3] != nil {
		t.Fatal("another workspace's story card leaked")
	}
	// ...and the owning workspace sees it.
	own, err := db2.StoryCards(ctx, []int64{story3})
	if err != nil || own[story3] == nil || own[story3].Summary != "resumo ws2" {
		t.Fatalf("ws2 card = %+v %v", own[story3], err)
	}
}
