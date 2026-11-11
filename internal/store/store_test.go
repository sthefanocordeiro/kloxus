package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
)

func open(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testPub() *config.Publication {
	return &config.Publication{
		Slug: "test-pub",
		Name: "Test Pub",
		Sections: []config.Section{
			{Title: "Lead", Items: 1, Commentary: "required"},
		},
		Sources: []config.SourceDef{
			{Type: "rss", URL: "fixtures/feeds/a.xml"},
		},
	}
}

func TestMigrateAndSeed(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	var applied int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if applied < 1 {
		t.Fatal("no migrations applied")
	}

	id1, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	id2, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication (again): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("seed not idempotent: %d != %d", id1, id2)
	}

	pub, err := db.GetPublication(ctx, "test-pub")
	if err != nil {
		t.Fatalf("GetPublication by slug: %v", err)
	}
	if pub.Name != "Test Pub" || len(pub.Sections) != 1 {
		t.Fatalf("unexpected publication: %+v", pub)
	}
	byID, err := db.GetPublication(ctx, "1")
	if err != nil || byID.Slug != "test-pub" {
		t.Fatalf("GetPublication by id: %v %+v", err, byID)
	}

	srcs, err := db.Sources(ctx, pub.ID)
	if err != nil || len(srcs) != 1 {
		t.Fatalf("Sources: %v len=%d", err, len(srcs))
	}

	if n, _ := db.NextIssueNumber(ctx, pub.ID); n != 1 {
		t.Fatalf("NextIssueNumber = %d", n)
	}
	if _, err := db.CreateIssue(ctx, pub.ID, 1, "s", "body"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if n, _ := db.NextIssueNumber(ctx, pub.ID); n != 2 {
		t.Fatalf("NextIssueNumber after create = %d", n)
	}
	body, err := db.IssueBody(ctx, pub.ID, 1)
	if err != nil || body != "body" {
		t.Fatalf("IssueBody: %v %q", err, body)
	}
}

func TestLatestGuideEmpty(t *testing.T) {
	db := open(t)
	_, _, err := db.LatestGuide(context.Background(), 1)
	if err != sql.ErrNoRows {
		t.Fatalf("LatestGuide on empty = %v, want ErrNoRows", err)
	}
}

// TestSeedRefreshesConfig: seeding an existing slug updates config
// columns (paywall price, sections) while preserving visitor data — the
// redeploy path relies on this so workspaces created before a deploy
// pick up template changes.
func TestSeedRefreshesConfig(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	p := testPub()
	p.Paywall = config.Paywall{Enabled: true, Price: "R$ 12/month", PaidSections: []string{"Lead"}}
	id, err := db.SeedPublication(ctx, p)
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	// visitor-added source survives the refresh
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sources (publication_id, type, url) VALUES (?,?,?)`, id, "rss", "https://visitor.example/feed"); err != nil {
		t.Fatalf("add source: %v", err)
	}

	p2 := testPub()
	p2.Paywall = config.Paywall{Enabled: true, Price: "V$ 12/month", PaidSections: []string{"Lead"}}
	if _, err := db.SeedPublication(ctx, p2); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	pub, err := db.GetPublication(ctx, "test-pub")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	if pub.Paywall.Price != "V$ 12/month" {
		t.Fatalf("price not refreshed: %q", pub.Paywall.Price)
	}
	srcs, err := db.Sources(ctx, pub.ID)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	var found bool
	for _, s := range srcs {
		if s.URL == "https://visitor.example/feed" {
			found = true
		}
	}
	if !found {
		t.Fatal("visitor-added source lost on refresh")
	}
}
