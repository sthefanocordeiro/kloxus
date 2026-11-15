package store

// Candidate-set bounds (KLOX-45, D41): the shared catalog pool a run
// sees is limited to the last 7 days (published date, fetched_at when
// missing or unparseable), the 60 newest per source, and never promo
// rows. Postgres-backed like every store test.
import (
	"context"
	"fmt"
	"testing"
	"time"
)

// seedCatalogItem inserts a catalog item with explicit dates.
func seedCatalogItem(t *testing.T, pool *Pool, catalogID int64, ext, published, fetched string) {
	t.Helper()
	var pub, fet any
	if published != "" {
		pub = published
	}
	if fetched != "" {
		fet = fetched
	} else {
		fet = NowUTC()
	}
	if _, err := pool.db.ExecContext(context.Background(), `INSERT INTO items
		(catalog_id, external_id, url, canonical_url, title, published_at, fetched_at, content_text, kind)
		VALUES ($1,$2,$3,$3,$4,$5,$6,'body','article')`,
		catalogID, ext, "https://c.example/"+ext, "cand-"+ext, pub, fet); err != nil {
		t.Fatalf("seed %s: %v", ext, err)
	}
	t.Cleanup(func() {
		pool.db.ExecContext(context.Background(),
			`DELETE FROM items WHERE external_id = $1`, ext)
	})
}

func ts(d time.Duration) string { return TSUTC(time.Now().Add(d)) }

// TestCandidateWindow: the 7-day window applies to catalog items with a
// fetched_at fallback for missing or unparseable published dates —
// workspace items are unaffected (D41).
func TestCandidateWindow(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()
	cs, err := pool.CatalogEntry(ctx, "tecnoblog")
	if err != nil {
		t.Fatalf("CatalogEntry: %v", err)
	}
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.AddCatalogSource(ctx, pubID, cs); err != nil {
		t.Fatalf("AddCatalogSource: %v", err)
	}

	seedCatalogItem(t, pool, cs.ID, "cw-fresh", ts(-24*time.Hour), "")
	seedCatalogItem(t, pool, cs.ID, "cw-edge", ts(-6*24*time.Hour), "")
	seedCatalogItem(t, pool, cs.ID, "cw-old", ts(-10*24*time.Hour), "")
	seedCatalogItem(t, pool, cs.ID, "cw-undated", "", "")
	seedCatalogItem(t, pool, cs.ID, "cw-undated-old", "", ts(-10*24*time.Hour))
	seedCatalogItem(t, pool, cs.ID, "cw-bad-date", "ontem à noite", "")
	seedCatalogItem(t, pool, cs.ID, "cw-bad-old", "garbage!!", ts(-10*24*time.Hour))

	items, err := db.ItemsForPublication(ctx, pubID)
	if err != nil {
		t.Fatalf("ItemsForPublication: %v", err)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.ExternalID] = true
	}
	for ext, want := range map[string]bool{
		"cw-fresh": true, "cw-edge": true, "cw-undated": true,
		"cw-bad-date": true,
		"cw-old":      false, "cw-undated-old": false, "cw-bad-old": false,
	} {
		if got[ext] != want {
			t.Errorf("%s included=%v want %v", ext, got[ext], want)
		}
	}
	total, _, err := db.CatalogItemsStats(ctx, pubID)
	if err != nil {
		t.Fatalf("CatalogItemsStats: %v", err)
	}
	var want int
	for ext, w := range map[string]bool{
		"cw-fresh": true, "cw-edge": true, "cw-undated": true,
		"cw-bad-date": true, "cw-old": false, "cw-undated-old": false, "cw-bad-old": false,
	} {
		if w && got[ext] {
			want++
		}
	}
	if total != want {
		t.Fatalf("CatalogItemsStats total=%d want %d", total, want)
	}
}

// TestCandidateCapPerSource: only the 60 newest items of a catalog
// source enter a run — the older ones are cut by recency.
func TestCandidateCapPerSource(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()
	cs, err := pool.CatalogEntry(ctx, "olhar-digital")
	if err != nil {
		t.Fatalf("CatalogEntry: %v", err)
	}
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.AddCatalogSource(ctx, pubID, cs); err != nil {
		t.Fatalf("AddCatalogSource: %v", err)
	}
	// 70 items, one hour apart — the 10 oldest must be cut.
	for i := 0; i < 70; i++ {
		seedCatalogItem(t, pool, cs.ID, fmt.Sprintf("cc-%02d", i),
			ts(-time.Duration(70-i)*time.Hour), "")
	}
	items, err := db.ItemsForPublication(ctx, pubID)
	if err != nil {
		t.Fatalf("ItemsForPublication: %v", err)
	}
	n := 0
	for _, it := range items {
		if it.CatalogID == cs.ID {
			n++
		}
	}
	if n != 60 {
		t.Fatalf("catalog items=%d want 60", n)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.ExternalID] = true
	}
	for i := 0; i < 10; i++ { // oldest cut
		if got[fmt.Sprintf("cc-%02d", i)] {
			t.Errorf("old item cc-%02d should be cut by the cap", i)
		}
	}
	for i := 60; i < 70; i++ { // newest kept
		if !got[fmt.Sprintf("cc-%02d", i)] {
			t.Errorf("new item cc-%02d missing", i)
		}
	}
}

// TestPromoExcluded: a deal-classified catalog item is stored as
// kind='promo' and never reaches the candidate set — while a normal
// item from the same save does.
func TestPromoExcluded(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()
	cs, err := pool.CatalogEntry(ctx, "meiobit")
	if err != nil {
		t.Fatalf("CatalogEntry: %v", err)
	}
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.AddCatalogSource(ctx, pubID, cs); err != nil {
		t.Fatalf("AddCatalogSource: %v", err)
	}
	if _, err := pool.SaveCatalogItems(ctx, cs.ID, []CatalogItem{
		{ExternalID: "px-deal", URL: "https://m.example/deal", Title: "OFERTA: monitor com 40% OFF e cupom", Kind: "article"},
		{ExternalID: "px-news", URL: "https://m.example/news", Title: "Regulador abre consulta sobre IA", Kind: "article"},
	}); err != nil {
		t.Fatalf("SaveCatalogItems: %v", err)
	}
	t.Cleanup(func() {
		pool.db.ExecContext(context.Background(),
			`DELETE FROM items WHERE external_id IN ('px-deal','px-news')`)
	})
	var kind string
	if err := pool.db.QueryRowContext(ctx,
		`SELECT kind FROM items WHERE external_id = 'px-deal'`).Scan(&kind); err != nil {
		t.Fatalf("stored kind: %v", err)
	}
	if kind != "promo" {
		t.Fatalf("px-deal stored kind=%q want promo", kind)
	}
	items, err := db.ItemsForPublication(ctx, pubID)
	if err != nil {
		t.Fatalf("ItemsForPublication: %v", err)
	}
	for _, it := range items {
		if it.ExternalID == "px-deal" {
			t.Fatal("promo item reached the candidate set")
		}
	}
	var news bool
	for _, it := range items {
		if it.ExternalID == "px-news" {
			news = true
		}
	}
	if !news {
		t.Fatal("news item missing from candidates")
	}
}
