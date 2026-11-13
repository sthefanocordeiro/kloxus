package store

// Postgres-backed tests for the global ingestion schema (skipped when
// DATABASE_URL is unset, like every store test): the seeded catalog,
// shared items visible to multiple workspaces, the fetch log, and the
// 30-day body retention.
import (
	"context"
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
	if byPolicy["launch"] != 6 {
		t.Fatalf("launch sources=%d want 6", byPolicy["launch"])
	}
	if byPolicy["signal"] != 2 { // hacker-news + daily-journal
		t.Fatalf("signal sources=%d want 2", byPolicy["signal"])
	}
	for _, slug := range []string{"tecnoblog", "olhar-digital", "tabnews", "macmagazine", "meiobit", "startups", "hacker-news", "daily-journal", "canaltech", "techcrunch"} {
		if !slugSeen[slug] {
			t.Fatalf("catalog missing %s", slug)
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

	cs, err := pool.CatalogEntry(ctx, "tecnoblog")
	if err != nil {
		t.Fatalf("CatalogEntry: %v", err)
	}
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
	t.Cleanup(func() {
		pool.db.ExecContext(context.Background(),
			`DELETE FROM items WHERE external_id = 'test-shared-1'`)
	})
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
		if found == nil || found.SourceName != "Tecnoblog" || found.CatalogID != cs.ID {
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
	cs, err := pool.CatalogEntry(ctx, "tecnoblog")
	if err != nil {
		t.Fatalf("CatalogEntry: %v", err)
	}
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
	cs, err := pool.CatalogEntry(ctx, "tecnoblog")
	if err != nil {
		t.Fatalf("CatalogEntry: %v", err)
	}
	old := time.Now().Add(-31 * 24 * time.Hour)
	if _, err := pool.db.ExecContext(ctx, `INSERT INTO items
		(catalog_id, external_id, url, title, content_text, fetched_at)
		VALUES ($1,'ret-old','https://x/old','old','body',$2),
		       ($1,'ret-new','https://x/new','new','body',$3)`,
		cs.ID, TSUTC(old), NowUTC()); err != nil {
		t.Fatalf("seed items: %v", err)
	}
	t.Cleanup(func() {
		pool.db.ExecContext(context.Background(),
			`DELETE FROM items WHERE external_id IN ('ret-old','ret-new')`)
	})
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
