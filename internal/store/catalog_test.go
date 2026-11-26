package store

// Postgres-backed tests for the global ingestion schema (skipped when
// DATABASE_URL is unset, like every store test): the seeded catalog,
// shared items visible to multiple workspaces, the fetch log, and the
// 30-day body retention.
import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCatalogSeeded: migration 0002 seeds the launch/signal/hold/
// blocked catalog — and the policy gate classifies it correctly.
func TestCatalogSeeded(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	cat, err := pool.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	byPolicy := map[string]int{}
	var slugSeen map[string]bool = map[string]bool{}
	for _, c := range cat {
		byPolicy[c.Policy]++
		slugSeen[c.Slug] = true
	}
	// Floors, not exact counts: the catalog table is global and
	// parallel test binaries insert their own throwaway rows.
	if byPolicy["launch"] < 6 {
		t.Fatalf("launch sources=%d want at least 6", byPolicy["launch"])
	}
	if byPolicy["signal"] < 1 { // hacker-news
		t.Fatalf("signal sources=%d want at least 1", byPolicy["signal"])
	}
	for _, slug := range []string{"tecnoblog", "olhar-digital", "tabnews", "macmagazine", "meiobit", "startups", "hacker-news", "canaltech", "techcrunch"} {
		if !slugSeen[slug] {
			t.Fatalf("catalog missing %s", slug)
		}
	}
	// KLOX-152: forums/aggregators are off, BR portals are signal-only
	for _, slug := range []string{"tabnews", "hacker-news"} {
		c, err := pool.CatalogEntry(ctx, slug)
		if err != nil || c.Fetchable() {
			t.Fatalf("%s must exist but not be fetchable: %+v err=%v", slug, c, err)
		}
	}
	for _, slug := range []string{"g1", "folha", "valor", "cnn-brasil", "metropoles"} {
		c, err := pool.CatalogEntry(ctx, slug)
		if err != nil || c.Policy != "signal" || c.BodyMode != "links" || !c.Fetchable() {
			t.Fatalf("%s should be a fetchable links-only signal: %+v err=%v", slug, c, err)
		}
	}
	// the fetch gate: only launch/signal may be requested
	c, err := pool.CatalogEntry(ctx, "canaltech")
	if err != nil {
		t.Fatalf("CatalogEntry: %v", err)
	}
	if c.Policy != "blocked" || c.Fetchable() {
		t.Fatalf("blocked source reports fetchable: %+v", c)
	}
}

// TestSharedItemsAcrossWorkspaces: one SaveCatalogItems call serves
// every workspace that picked the source — global ingestion, no
// per-tenant fetch.
func TestSharedItemsAcrossWorkspaces(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()

	cs := testCatalogEntry(t, pool, "shared-items")
	added, err := pool.SaveCatalogItems(ctx, cs.ID, []CatalogItem{{
		ExternalID: "test-shared-1", URL: "https://tecnoblog.net/x",
		CanonicalURL: "https://tecnoblog.net/x", Title: "Shared",
		ContentText: "body", Kind: "article",
	}})
	if err != nil {
		t.Fatalf("SaveCatalogItems: %v", err)
	}
	// Upsert idempotence: a second save of the same external_id is a no-op.
	again, err := pool.SaveCatalogItems(ctx, cs.ID, []CatalogItem{{
		ExternalID: "test-shared-1", URL: "https://tecnoblog.net/x", Title: "Shared",
	}})
	if err != nil || again != 0 {
		t.Fatalf("re-save: added=%d err=%v", again, err)
	}
	if added != 1 {
		t.Fatalf("added=%d want 1", added)
	}

	// Two workspaces picking the same catalog source both see the item.
	for _, ws := range []*DB{openOn(t, pool), openOn(t, pool)} {
		pubID, err := ws.SeedPublication(ctx, testPub())
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := ws.AddCatalogSource(ctx, pubID, cs); err != nil {
			t.Fatalf("AddCatalogSource: %v", err)
		}
		items, err := ws.ItemsForPublication(ctx, pubID)
		if err != nil {
			t.Fatalf("ItemsForPublication: %v", err)
		}
		var found *Item
		for i := range items {
			if items[i].ExternalID == "test-shared-1" {
				found = &items[i]
			}
		}
		if found == nil || found.SourceName != cs.Name || found.CatalogID != cs.ID {
			t.Fatalf("shared item missing or misattributed: %+v", found)
		}
		total, fresh, err := ws.CatalogItemsStats(ctx, pubID)
		if err != nil || total < 1 || fresh < 1 {
			t.Fatalf("CatalogItemsStats: total=%d fresh=%d err=%v", total, fresh, err)
		}
	}
}

// TestFetchLogRecords: outcomes land in fetch_log for the admin view.
func TestFetchLogRecords(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	cs := testCatalogEntry(t, pool, "fetch-log")
	if err := pool.LogFetch(ctx, cs.ID, "ok", 200, 12, 3, "test"); err != nil {
		t.Fatalf("LogFetch: %v", err)
	}
	logs, err := pool.FetchLog(ctx, cs.ID, 1)
	if err != nil || len(logs) != 1 {
		t.Fatalf("FetchLog: %v n=%d", err, len(logs))
	}
	if logs[0].Status != "ok" || logs[0].Items != 12 || logs[0].NewItems != 3 {
		t.Fatalf("log row: %+v", logs[0])
	}
}

// TestRetentionPurgesBodies: the 30-day window clears content_text but
// keeps title/link/metadata.
func TestRetentionPurgesBodies(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	cs := testCatalogEntry(t, pool, "retention")
	old := time.Now().Add(-31 * 24 * time.Hour)
	if _, err := pool.db.ExecContext(ctx, `INSERT INTO items
		(catalog_id, external_id, url, title, content_text, fetched_at)
		VALUES ($1,'ret-old','https://x/old','old','body',$2),
		       ($1,'ret-new','https://x/new','new','body',$3)`,
		cs.ID, TSUTC(old), NowUTC()); err != nil {
		t.Fatalf("seed items: %v", err)
	}
	n, err := pool.PurgeOldBodies(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("PurgeOldBodies: n=%d err=%v", n, err)
	}
	var body, title string
	if err := pool.db.QueryRowContext(ctx,
		`SELECT content_text, title FROM items WHERE external_id = 'ret-old'`).Scan(&body, &title); err != nil {
		t.Fatal(err)
	}
	if body != "" || title != "old" {
		t.Fatalf("old row: body=%q title=%q", body, title)
	}
	if err := pool.db.QueryRowContext(ctx,
		`SELECT content_text FROM items WHERE external_id = 'ret-new'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "body" {
		t.Fatal("recent body was purged")
	}
}

// TestSaveDJItemsTransactional proves the content update and semantic-row
// cleanup are one atomic write: when the enrichment cleanup fails, the item
// keeps its prior text and both deterministic semantic rows. After the
// failure is removed the same update commits and cleans both rows.
func TestSaveDJItemsTransactional(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	cs := testCatalogEntry(t, pool, "dj-transactional")

	raw := []CatalogItem{{
		ExternalID: "djtx-1", URL: "https://dj.example/story", CanonicalURL: "https://dj.example/story",
		Title: "Original DJ story", ContentText: "original body", PublishedAt: "2026-11-21T10:00:00Z",
		Kind: "article", Meta: map[string]any{"slug": "djtx-1"},
	}}
	if a, c, err := pool.SaveDJItems(ctx, cs.ID, raw); err != nil || a != 1 || c != 0 {
		t.Fatalf("initial SaveDJItems a=%d c=%d err=%v", a, c, err)
	}
	var itemID int64
	if err := pool.db.QueryRowContext(ctx,
		`SELECT id FROM items WHERE catalog_id = $1 AND external_id = $2`, cs.ID, "djtx-1").Scan(&itemID); err != nil {
		t.Fatalf("item id: %v", err)
	}
	vector := "[0" + strings.Repeat(",0", 255) + "]"
	if err := pool.SaveEmbeddings(ctx, []ItemEmbedding{{ItemID: itemID, Vector: vector, Model: "local-test"}}); err != nil {
		t.Fatalf("SaveEmbeddings: %v", err)
	}
	if _, err := pool.db.ExecContext(ctx, `INSERT INTO item_enrichment
		(item_id, ai_kind, promotional, topics_json, summary_pt, language, model)
		VALUES ($1,'noticia',false,'[]','','pt','local-test')`, itemID); err != nil {
		t.Fatalf("insert enrichment: %v", err)
	}

	fn := fmt.Sprintf("klox263_fail_dj_cleanup_%d", cs.ID)
	trg := fmt.Sprintf("klox263_fail_dj_cleanup_%d", cs.ID)
	if _, err := pool.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF OLD.item_id = %d THEN
				RAISE EXCEPTION 'forced dj semantic cleanup failure';
			END IF;
			RETURN OLD;
		END;
		$$`, fn, itemID)); err != nil {
		t.Fatalf("create failure function: %v", err)
	}
	if _, err := pool.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE DELETE ON item_enrichment
		FOR EACH ROW EXECUTE FUNCTION %s()`, trg, fn)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	var once sync.Once
	drop := func() {
		once.Do(func() {
			if _, err := pool.db.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON item_enrichment`, trg)); err != nil {
				t.Errorf("drop failure trigger: %v", err)
			}
			if _, err := pool.db.ExecContext(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn)); err != nil {
				t.Errorf("drop failure function: %v", err)
			}
		})
	}
	t.Cleanup(drop)

	raw[0].Title = "Revised DJ story"
	raw[0].ContentText = "revised body"
	_, _, err := pool.SaveDJItems(ctx, cs.ID, raw)
	if err == nil || !strings.Contains(err.Error(), "forced dj semantic cleanup failure") {
		t.Fatalf("SaveDJItems error = %v, want forced cleanup failure", err)
	}
	var title, body string
	if err := pool.db.QueryRowContext(ctx,
		`SELECT title, content_text FROM items WHERE id = $1`, itemID).Scan(&title, &body); err != nil {
		t.Fatal(err)
	}
	if title != "Original DJ story" || body != "original body" {
		t.Fatalf("failed cleanup left updated item: title=%q body=%q", title, body)
	}
	var embeddings, enrichments int
	if err := pool.db.QueryRowContext(ctx, `SELECT count(*) FROM item_embedding WHERE item_id = $1`, itemID).Scan(&embeddings); err != nil {
		t.Fatal(err)
	}
	if err := pool.db.QueryRowContext(ctx, `SELECT count(*) FROM item_enrichment WHERE item_id = $1`, itemID).Scan(&enrichments); err != nil {
		t.Fatal(err)
	}
	if embeddings != 1 || enrichments != 1 {
		t.Fatalf("failed cleanup left partial semantic state: embeddings=%d enrichments=%d", embeddings, enrichments)
	}

	drop()
	if a, c, err := pool.SaveDJItems(ctx, cs.ID, raw); err != nil || a != 0 || c != 1 {
		t.Fatalf("success SaveDJItems a=%d c=%d err=%v", a, c, err)
	}
	if err := pool.db.QueryRowContext(ctx,
		`SELECT title, content_text FROM items WHERE id = $1`, itemID).Scan(&title, &body); err != nil {
		t.Fatal(err)
	}
	if title != "Revised DJ story" || body != "revised body" {
		t.Fatalf("committed item: title=%q body=%q", title, body)
	}
	if err := pool.db.QueryRowContext(ctx, `SELECT count(*) FROM item_embedding WHERE item_id = $1`, itemID).Scan(&embeddings); err != nil {
		t.Fatal(err)
	}
	if err := pool.db.QueryRowContext(ctx, `SELECT count(*) FROM item_enrichment WHERE item_id = $1 AND model LIKE 'local-%'`, itemID).Scan(&enrichments); err != nil {
		t.Fatal(err)
	}
	if embeddings != 0 || enrichments != 0 {
		t.Fatalf("successful update left stale semantic rows: embeddings=%d enrichments=%d", embeddings, enrichments)
	}
}
