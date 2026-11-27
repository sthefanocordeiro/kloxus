package store

// Category persistence contract (KLOX-278). Postgres-backed like the
// other catalog tests; skipped when DATABASE_URL is unset.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/sources"
)

// TestSaveCatalogItemsCategoriesRepoll: a feed repoll refreshes only the
// dedicated category column when the publisher supplies changed labels.
// fetched_at, title/body and meta keep the first-inserted values, and a
// later category-less payload does not erase the signal.
func TestSaveCatalogItemsCategoriesRepoll(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	cs := testCatalogEntry(t, pool, "category-repoll")

	raw := CatalogItem{
		ExternalID: "cat-repoll-1", URL: "https://portal.example/agro",
		CanonicalURL: "https://portal.example/agro", Title: "Original title",
		ContentText: "original body", Kind: "article",
		Categories: []string{"Agro", "Brasil"},
		Meta:       map[string]any{"keep": "first"},
	}
	added, err := pool.SaveCatalogItems(ctx, cs.ID, []CatalogItem{raw})
	if err != nil || added != 1 {
		t.Fatalf("first save added=%d err=%v", added, err)
	}
	var firstFetched string
	if err := pool.db.QueryRowContext(ctx,
		`SELECT fetched_at FROM items WHERE catalog_id = $1 AND external_id = $2`,
		cs.ID, raw.ExternalID).Scan(&firstFetched); err != nil {
		t.Fatalf("first fetched_at: %v", err)
	}

	changed := raw
	changed.Title = "Repoll title must not win"
	changed.ContentText = "repoll body must not win"
	changed.Meta = map[string]any{"keep": "repoll must not win"}
	changed.Categories = []string{"Agronegócio", "Economia"}
	added, err = pool.SaveCatalogItems(ctx, cs.ID, []CatalogItem{changed})
	if err != nil || added != 0 {
		t.Fatalf("category repoll added=%d err=%v", added, err)
	}

	var cats, title, body, meta, fetched string
	if err := pool.db.QueryRowContext(ctx,
		`SELECT categories::text, title, content_text, meta_json, fetched_at
		 FROM items WHERE catalog_id = $1 AND external_id = $2`,
		cs.ID, raw.ExternalID).Scan(&cats, &title, &body, &meta, &fetched); err != nil {
		t.Fatalf("stored row: %v", err)
	}
	var gotCats []string
	if err := json.Unmarshal([]byte(cats), &gotCats); err != nil {
		t.Fatalf("categories json: %v (%s)", err, cats)
	}
	if strings.Join(gotCats, "|") != "Agronegócio|Economia" {
		t.Fatalf("categories=%v want changed non-empty feed labels", gotCats)
	}
	if fetched != firstFetched {
		t.Fatalf("fetched_at changed on repoll: %q -> %q", firstFetched, fetched)
	}
	if title != raw.Title || body != raw.ContentText || !strings.Contains(meta, `"keep":"first"`) {
		t.Fatalf("repoll degraded first-inserted content/meta: title=%q body=%q meta=%s", title, body, meta)
	}

	noSignal := raw
	noSignal.Categories = nil
	added, err = pool.SaveCatalogItems(ctx, cs.ID, []CatalogItem{noSignal})
	if err != nil || added != 0 {
		t.Fatalf("empty-category repoll added=%d err=%v", added, err)
	}
	if err := pool.db.QueryRowContext(ctx,
		`SELECT categories::text FROM items WHERE catalog_id = $1 AND external_id = $2`,
		cs.ID, raw.ExternalID).Scan(&cats); err != nil {
		t.Fatalf("stored categories after empty repoll: %v", err)
	}
	gotCats = nil
	if err := json.Unmarshal([]byte(cats), &gotCats); err != nil {
		t.Fatalf("categories json after empty repoll: %v (%s)", err, cats)
	}
	if strings.Join(gotCats, "|") != "Agronegócio|Economia" {
		t.Fatalf("empty-category repoll erased the signal: %v", gotCats)
	}
}

// TestSaveItemsCategoriesRepoll applies the same category-only repoll
// contract to an editor-added RSS source (the LiveFetcher path). The row
// remains first-inserted for content and fetched_at; only a non-empty
// changed category payload refreshes the dedicated column.
func TestSaveItemsCategoriesRepoll(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, err := db.Sources(ctx, pubID)
	if err != nil || len(srcs) != 1 {
		t.Fatalf("Sources: %v %v", srcs, err)
	}
	raw := sources.RawItem{
		ExternalID: "direct-cat-1", URL: "https://direct.example/agro",
		Title: "Original direct title", ContentText: "original direct body",
		PublishedAt: "2026-11-20T10:00:00Z", Categories: []string{"Agro"},
	}
	added, err := db.SaveItems(ctx, srcs[0].ID, []sources.RawItem{raw}, func(u string) string { return u })
	if err != nil || added != 1 {
		t.Fatalf("first save added=%d err=%v", added, err)
	}
	var firstFetched string
	if err := db.QueryRowContext(ctx,
		`SELECT fetched_at FROM items WHERE source_id = $1 AND external_id = $2`,
		srcs[0].ID, raw.ExternalID).Scan(&firstFetched); err != nil {
		t.Fatalf("first fetched_at: %v", err)
	}

	changed := raw
	changed.Title = "repoll title must not win"
	changed.ContentText = "repoll body must not win"
	changed.Categories = []string{"Agronegócio", "Economia"}
	added, err = db.SaveItems(ctx, srcs[0].ID, []sources.RawItem{changed}, func(u string) string { return u })
	if err != nil || added != 0 {
		t.Fatalf("category repoll added=%d err=%v", added, err)
	}
	var cats, title, body, fetched string
	if err := db.QueryRowContext(ctx,
		`SELECT categories::text, title, content_text, fetched_at
		 FROM items WHERE source_id = $1 AND external_id = $2`,
		srcs[0].ID, raw.ExternalID).Scan(&cats, &title, &body, &fetched); err != nil {
		t.Fatalf("stored row: %v", err)
	}
	var gotCats []string
	if err := json.Unmarshal([]byte(cats), &gotCats); err != nil {
		t.Fatalf("categories json: %v (%s)", err, cats)
	}
	if strings.Join(gotCats, "|") != "Agronegócio|Economia" {
		t.Fatalf("categories=%v want changed non-empty feed labels", gotCats)
	}
	if fetched != firstFetched || title != raw.Title || body != raw.ContentText {
		t.Fatalf("repoll rewrote first-inserted fields: fetched %q=>%q title=%q body=%q", firstFetched, fetched, title, body)
	}

	empty := raw
	empty.Categories = nil
	added, err = db.SaveItems(ctx, srcs[0].ID, []sources.RawItem{empty}, func(u string) string { return u })
	if err != nil || added != 0 {
		t.Fatalf("empty-category repoll added=%d err=%v", added, err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT categories::text FROM items WHERE source_id = $1 AND external_id = $2`,
		srcs[0].ID, raw.ExternalID).Scan(&cats); err != nil {
		t.Fatalf("stored categories after empty repoll: %v", err)
	}
	gotCats = nil
	if err := json.Unmarshal([]byte(cats), &gotCats); err != nil {
		t.Fatalf("categories json after empty repoll: %v (%s)", err, cats)
	}
	if strings.Join(gotCats, "|") != "Agronegócio|Economia" {
		t.Fatalf("empty-category repoll erased the signal: %v", gotCats)
	}
}

// TestSaveDJItemsEmptyCategoriesDoNotErase: DailyJournal already carried
// labels in meta before the dedicated column. A revised payload without
// labels may refresh the story text, but it must not erase the category
// signal; a non-empty replacement still updates it.
func TestSaveDJItemsEmptyCategoriesDoNotErase(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	cs := testCatalogEntry(t, pool, "dj-category-repoll")

	raw := CatalogItem{
		ExternalID: "dj-cat-1", URL: "https://dj.example/story", CanonicalURL: "https://dj.example/story",
		Title: "DJ original", ContentText: "body", PublishedAt: "2026-11-21T10:00:00Z",
		Kind: "article", Categories: []string{"Economia"}, Meta: map[string]any{"slug": "dj-cat-1"},
	}
	if a, c, err := pool.SaveDJItems(ctx, cs.ID, []CatalogItem{raw}); err != nil || a != 1 || c != 0 {
		t.Fatalf("initial SaveDJItems a=%d c=%d err=%v", a, c, err)
	}

	revised := raw
	revised.Title = "DJ revised"
	revised.Categories = nil
	if a, c, err := pool.SaveDJItems(ctx, cs.ID, []CatalogItem{revised}); err != nil || a != 0 || c != 1 {
		t.Fatalf("empty-category revision a=%d c=%d err=%v", a, c, err)
	}
	var cats, title string
	if err := pool.db.QueryRowContext(ctx,
		`SELECT categories::text, title FROM items WHERE catalog_id = $1 AND external_id = $2`,
		cs.ID, raw.ExternalID).Scan(&cats, &title); err != nil {
		t.Fatalf("stored row: %v", err)
	}
	var gotCats []string
	if err := json.Unmarshal([]byte(cats), &gotCats); err != nil {
		t.Fatalf("categories json: %v (%s)", err, cats)
	}
	if title != revised.Title || strings.Join(gotCats, "|") != "Economia" {
		t.Fatalf("empty categories erased signal or revision lost: cats=%v title=%q", gotCats, title)
	}

	replacement := revised
	replacement.Categories = []string{"Agro"}
	if a, c, err := pool.SaveDJItems(ctx, cs.ID, []CatalogItem{replacement}); err != nil || a != 0 || c != 1 {
		t.Fatalf("category replacement a=%d c=%d err=%v", a, c, err)
	}
	if err := pool.db.QueryRowContext(ctx,
		`SELECT categories::text FROM items WHERE catalog_id = $1 AND external_id = $2`,
		cs.ID, raw.ExternalID).Scan(&cats); err != nil {
		t.Fatalf("stored replacement: %v", err)
	}
	gotCats = nil
	if err := json.Unmarshal([]byte(cats), &gotCats); err != nil {
		t.Fatalf("replacement categories json: %v (%s)", err, cats)
	}
	if strings.Join(gotCats, "|") != "Agro" {
		t.Fatalf("categories=%v want replacement", gotCats)
	}
}

// TestStoriesCarryPersistedCategories pins the read path ranking actually
// uses: candidate items and stories reloaded through StoriesWithItems both
// decode the dedicated jsonb column. A regression here silently drops the
// category signal before stage 1 even though ingestion stored it.
func TestStoriesCarryPersistedCategories(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	cs := testCatalogEntry(t, pool, "category-read-path")
	if _, err := pool.SaveCatalogItems(ctx, cs.ID, []CatalogItem{{
		ExternalID: "cat-read-1", URL: "https://portal.example/read",
		CanonicalURL: "https://portal.example/read", Title: "Read path",
		ContentText: "body", Kind: "article", Categories: []string{"Agronegócio", "Brasil"},
	}}); err != nil {
		t.Fatalf("SaveCatalogItems: %v", err)
	}

	db := openOn(t, pool)
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	if _, err := db.AddCatalogSource(ctx, pubID, cs); err != nil {
		t.Fatalf("AddCatalogSource: %v", err)
	}
	items, err := db.ItemsForPublication(ctx, pubID)
	if err != nil {
		t.Fatalf("ItemsForPublication: %v", err)
	}
	var itemID int64
	for _, it := range items {
		if it.ExternalID == "cat-read-1" {
			itemID = it.ID
			if strings.Join(it.Categories, "|") != "Agronegócio|Brasil" {
				t.Fatalf("candidate categories=%v", it.Categories)
			}
		}
	}
	if itemID == 0 {
		t.Fatal("catalog candidate missing")
	}
	storyID, err := db.CreateStory(ctx, pubID, "Read path", 1, "https://portal.example/read")
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	if err := db.AddStoryItem(ctx, storyID, itemID); err != nil {
		t.Fatalf("AddStoryItem: %v", err)
	}
	stories, err := db.StoriesWithItems(ctx, pubID)
	if err != nil {
		t.Fatalf("StoriesWithItems: %v", err)
	}
	if len(stories) != 1 || len(stories[0].Items) != 1 {
		t.Fatalf("stories=%+v", stories)
	}
	if strings.Join(stories[0].Items[0].Categories, "|") != "Agronegócio|Brasil" {
		t.Fatalf("story categories=%v", stories[0].Items[0].Categories)
	}
}
