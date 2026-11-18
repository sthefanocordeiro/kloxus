package store

// KLOX-67 persistence tests: shared item enrichment, story research
// cache, the paid-action ledger and the ai_draft marker lifecycle.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// seedWsItem inserts a workspace-source article and returns its id.
func seedWsItem(t *testing.T, db *DB, sourceID int64, ext, title string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(context.Background(),
		`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind)
		 VALUES ($1,$2,$3,$4,$5,'body','article') RETURNING id`,
		db.ws, sourceID, ext, "https://x.example/"+ext, title).Scan(&id); err != nil {
		t.Fatalf("insert item %s: %v", ext, err)
	}
	return id
}

// TestEnrichmentRoundTrip: one row per item, upsert is idempotent.
func TestEnrichmentRoundTrip(t *testing.T) {
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
	itemID := seedWsItem(t, db, srcs[0].ID, "enr-1", "Enrich me")

	// The item starts un-enriched.
	m, err := db.EnrichmentsForItems(ctx, []int64{itemID})
	if err != nil {
		t.Fatalf("EnrichmentsForItems: %v", err)
	}
	if m[itemID] != nil {
		t.Fatal("enrichment present before save")
	}

	en := ItemEnrichment{
		ItemID: itemID, Kind: "noticia", Promotional: false,
		Topics: []string{"tecnologia", "IA"}, SummaryPT: "Resumo de uma linha.",
		Language: "pt", Model: "claude-haiku-5-5",
	}
	if err := pool.SaveEnrichments(ctx, []ItemEnrichment{en}); err != nil {
		t.Fatalf("SaveEnrichments: %v", err)
	}
	// A second write with the same data stays one row and same values.
	if err := pool.SaveEnrichments(ctx, []ItemEnrichment{en}); err != nil {
		t.Fatalf("idempotent re-save: %v", err)
	}
	m, err = db.EnrichmentsForItems(ctx, []int64{itemID})
	if err != nil {
		t.Fatalf("EnrichmentsForItems: %v", err)
	}
	got := m[itemID]
	if got == nil || got.Kind != "noticia" || len(got.Topics) != 2 || got.SummaryPT != en.SummaryPT {
		t.Fatalf("enrichment = %+v", got)
	}
	if got.Excluded() {
		t.Error("news must not be excluded")
	}

	// Excluded kinds and the promotional flag rule the item out.
	for _, e := range []ItemEnrichment{
		{Kind: "lista"}, {Kind: "publieditorial"}, {Kind: "entretenimento"},
		{Kind: "esporte"}, {Kind: "efemeride"}, {Kind: "noticia", Promotional: true},
	} {
		if !e.Excluded() {
			t.Errorf("kind %q promo=%v should be excluded", e.Kind, e.Promotional)
		}
	}
	if (&ItemEnrichment{Kind: "analise"}).Excluded() {
		t.Error("analise is editorial — never excluded")
	}
}

// TestUnenrichedItemsAndCandidateExclusion: the sweep picks up only
// items with no row yet, and an excluded enrichment drops the item from
// the publication's candidate set.
func TestUnenrichedItemsAndCandidateExclusion(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, _ := db.Sources(ctx, pubID)
	ext := fmt.Sprintf("enr-un-%d", pubID)
	keep := seedWsItem(t, db, srcs[0].ID, ext+"a", "Keeps")
	drop := seedWsItem(t, db, srcs[0].ID, ext+"b", "Drops")

	if err := pool.SaveEnrichments(ctx, []ItemEnrichment{
		{ItemID: keep, Kind: "noticia", SummaryPT: "s"},
		{ItemID: drop, Kind: "lista", SummaryPT: "s"},
	}); err != nil {
		t.Fatalf("SaveEnrichments: %v", err)
	}

	items, err := db.ItemsForPublication(ctx, pubID)
	if err != nil {
		t.Fatalf("ItemsForPublication: %v", err)
	}
	var sawKeep, sawDrop bool
	for _, it := range items {
		sawKeep = sawKeep || it.ID == keep
		sawDrop = sawDrop || it.ID == drop
	}
	if !sawKeep {
		t.Error("non-excluded enriched item missing from candidates")
	}
	if sawDrop {
		t.Error("excluded kind item leaked into candidates")
	}

	// UnenrichedItems only surfaces rows with no enrichment — both of
	// ours are now enriched, so neither appears.
	pending, err := pool.UnenrichedItems(ctx, 500)
	if err != nil {
		t.Fatalf("UnenrichedItems: %v", err)
	}
	for _, u := range pending {
		if u.ID == keep || u.ID == drop {
			t.Errorf("enriched item %d still pending", u.ID)
		}
	}
}

// TestResearchCacheRoundTrip: get is ErrNoRows on a miss, the shared
// save serves any workspace.
func TestResearchCacheRoundTrip(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()

	key := fmt.Sprintf("research-test-%d", time.Now().UnixNano())
	if _, err := pool.ResearchGet(ctx, key); err != sql.ErrNoRows {
		t.Fatalf("miss: %v, want sql.ErrNoRows", err)
	}
	if err := pool.ResearchSave(ctx, key, `{"summary":"s"}`, "claude-haiku-5-5"); err != nil {
		t.Fatalf("ResearchSave: %v", err)
	}
	t.Cleanup(func() {
		pool.db.ExecContext(context.Background(), `DELETE FROM story_research WHERE research_key = $1`, key)
	})
	got, err := pool.ResearchGet(ctx, key)
	if err != nil {
		t.Fatalf("hit: %v", err)
	}
	if got.JSON != `{"summary":"s"}` || got.Model != "claude-haiku-5-5" {
		t.Fatalf("research = %+v", got)
	}
}

// TestPaidActionLedger: counts scope by (kind, ref) for the day and the
// daily total sums across kinds — the §7.3 caps read these.
func TestPaidActionLedger(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	if n, err := db.PaidActionsToday(ctx); err != nil || n != 0 {
		t.Fatalf("initial total = %d, %v", n, err)
	}
	for i := 0; i < 3; i++ {
		if err := db.RecordPaidAction(ctx, PaidActionCiteCheck, 42); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if n, _ := db.PaidActionCount(ctx, PaidActionCiteCheck, 42); n != 3 {
		t.Fatalf("citecheck@42 = %d, want 3", n)
	}
	if n, _ := db.PaidActionCount(ctx, PaidActionCiteCheck, 43); n != 0 {
		t.Fatalf("citecheck@43 = %d, want 0", n)
	}
	if err := db.RecordPaidAction(ctx, PaidActionRerank, 7); err != nil {
		t.Fatalf("record rerank: %v", err)
	}
	if n, _ := db.PaidActionsToday(ctx); n != 4 {
		t.Fatalf("daily total = %d, want 4", n)
	}
}

// TestAIDraftLifecycle: the marker sets at AI draft time and any author
// edit naming the item clears it — publication-level edits (commentary,
// citecheck flags) leave it alone.
func TestAIDraftLifecycle(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	issueID, err := db.CreateIssue(ctx, pubID, 1, "s", "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	iiID, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: 1, Section: "Lead", Status: "approved",
	})
	if err != nil {
		t.Fatalf("CreateIssueItem: %v", err)
	}
	if err := db.SetIssueItemAIDraft(ctx, iiID, true); err != nil {
		t.Fatalf("SetIssueItemAIDraft: %v", err)
	}
	got, err := db.GetIssueItem(ctx, iiID)
	if err != nil || !got.AIDraft {
		t.Fatalf("after set: %+v %v", got, err)
	}
	// An edit that does not name the item (citation flags, commentary)
	// keeps the marker.
	if err := db.SaveIssueEdit(ctx, issueID, IssueEdit{
		PubID: pubID, Body: "body", Decision: "citecheck", Reason: "flags",
	}); err != nil {
		t.Fatalf("flags edit: %v", err)
	}
	got, _ = db.GetIssueItem(ctx, iiID)
	if !got.AIDraft {
		t.Fatal("publication-level edit cleared the marker")
	}
	// An author edit naming the item clears it.
	if err := db.SaveIssueEdit(ctx, issueID, IssueEdit{
		PubID: pubID, IssueItemID: iiID, Body: "edited", Decision: "sentence", Reason: "edit",
	}); err != nil {
		t.Fatalf("author edit: %v", err)
	}
	got, _ = db.GetIssueItem(ctx, iiID)
	if got.AIDraft {
		t.Fatal("author edit did not clear ai_draft")
	}
}

// TestSplitStatementsCommentsAndLiterals is the 0005 regression: a ';'
// inside a '--' comment must never split a statement (CI run
// 44218603585 failed applying 0005 for exactly that), while ';' and
// '--' inside '...' literals and $$ bodies stay content.
func TestSplitStatementsCommentsAndLiterals(t *testing.T) {
	body := `-- header comment; trailing words must not leak
CREATE TABLE t (a TEXT);
-- mid-line semicolon; the tail would corrupt the next statement
CREATE TABLE u (b TEXT DEFAULT 'x;y');
SELECT 1; -- same-line comment ends the line
CREATE FUNCTION f() RETURNS void AS $$
BEGIN
  -- comment inside the body; with a semicolon
  RAISE NOTICE 'done';
END;
$$ LANGUAGE plpgsql;
SELECT 'literal -- still literal';`
	stmts := splitStatements(body)
	if len(stmts) != 5 {
		t.Fatalf("statements = %d, want 5: %q", len(stmts), stmts)
	}
	for i, s := range stmts[:3] {
		if strings.Contains(s, "--") {
			t.Errorf("stmt %d leaked comment text: %q", i+1, s)
		}
	}
	if !strings.Contains(stmts[1], `'x;y'`) {
		t.Errorf("stmt 2 lost its literal: %q", stmts[1])
	}
	if !strings.Contains(stmts[3], "RAISE NOTICE 'done';") ||
		!strings.Contains(stmts[3], "-- comment inside the body") {
		t.Errorf("dollar-quoted body mangled: %q", stmts[3])
	}
	if !strings.Contains(stmts[4], `'literal -- still literal'`) {
		t.Errorf("stmt 5 lost a '--' that lives inside a literal: %q", stmts[4])
	}
}
