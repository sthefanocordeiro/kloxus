package store

import (
	"context"
	"testing"
)

// TestPublicationFKsOnNewTables: media and standard_messages rows must
// reference a real publication, and DeletePublication still clears them
// (KLOX-220, KLOX-189 M3).
func TestPublicationFKsOnNewTables(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.CreatePublication(ctx, testPub())
	if err != nil {
		t.Fatalf("CreatePublication: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO media (workspace_id, publication_id, token, content_type, data) VALUES ($1, 999999, 't', 'image/png', 'x')`, db.ws); err == nil {
		t.Error("media insert with dangling publication_id: want FK violation")
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO standard_messages (workspace_id, publication_id, name, body) VALUES ($1, 999999, 'n', 'b')`, db.ws); err == nil {
		t.Error("standard_messages insert with dangling publication_id: want FK violation")
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO media (workspace_id, publication_id, token, content_type, data) VALUES ($1, $2, 't', 'image/png', 'x')`, db.ws, pubID); err != nil {
		t.Fatalf("media insert: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO standard_messages (workspace_id, publication_id, name, body) VALUES ($1, $2, 'n', 'b')`, db.ws, pubID); err != nil {
		t.Fatalf("standard_messages insert: %v", err)
	}
	if err := db.DeletePublication(ctx, pubID); err != nil {
		t.Fatalf("DeletePublication: %v", err)
	}
	for _, tbl := range []string{"media", "standard_messages"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+tbl+` WHERE workspace_id = $1`, db.ws).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s: %d orphan rows after delete", tbl, n)
		}
	}
}

// TestSubscriptionOneRowPerReader: repeated activations update the same
// row — subscriptions_reader_key makes a second row impossible and the
// upsert keeps a concurrent double-activate from deadlocking on it
// (KLOX-220, KLOX-189 M3).
func TestSubscriptionOneRowPerReader(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.CreatePublication(ctx, testPub())
	if err != nil {
		t.Fatalf("CreatePublication: %v", err)
	}
	rid, err := db.UpsertReader(ctx, pubID, "r@example.com", "R")
	if err != nil {
		t.Fatalf("UpsertReader: %v", err)
	}
	if err := db.ActivateSubscription(ctx, rid, "fake", "ref-1", "tok-1"); err != nil {
		t.Fatalf("ActivateSubscription: %v", err)
	}
	if err := db.ActivateSubscriptionUntil(ctx, rid, "abacatepay", "ref-2", "tok-2", "2030-01-01 00:00:00"); err != nil {
		t.Fatalf("ActivateSubscriptionUntil: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM subscriptions WHERE workspace_id = $1 AND reader_id = $2`, db.ws, rid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("subscriptions rows for reader = %d, want 1", n)
	}
	var token, until string
	if err := db.QueryRowContext(ctx,
		`SELECT magic_token, paid_until FROM subscriptions WHERE workspace_id = $1 AND reader_id = $2`, db.ws, rid).Scan(&token, &until); err != nil {
		t.Fatal(err)
	}
	if token != "tok-2" || until != "2030-01-01 00:00:00" {
		t.Fatalf("upsert did not update row: token=%q paid_until=%q", token, until)
	}
}

// TestIssueItemsUniqueStoryPerIssue: issue_items_story_key enforces the
// one-row-per-story invariant RankedCandidates used to defend with
// DISTINCT ON (KLOX-220, KLOX-189 M3).
func TestIssueItemsUniqueStoryPerIssue(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.CreatePublication(ctx, testPub())
	if err != nil {
		t.Fatalf("CreatePublication: %v", err)
	}
	issue1, err := db.CreateIssue(ctx, pubID, 1, "s", "b", testPub().Sections)
	if err != nil {
		t.Fatalf("CreateIssue 1: %v", err)
	}
	issue2, err := db.CreateIssue(ctx, pubID, 2, "s", "b", testPub().Sections)
	if err != nil {
		t.Fatalf("CreateIssue 2: %v", err)
	}
	if _, err := db.CreateIssueItem(ctx, IssueItem{IssueID: issue1, StoryID: 42, Section: "Lead"}); err != nil {
		t.Fatalf("CreateIssueItem: %v", err)
	}
	if _, err := db.CreateIssueItem(ctx, IssueItem{IssueID: issue1, StoryID: 42, Section: "Lead"}); err == nil {
		t.Error("duplicate (issue_id, story_id): want unique violation")
	}
	if _, err := db.CreateIssueItem(ctx, IssueItem{IssueID: issue2, StoryID: 42, Section: "Lead"}); err != nil {
		t.Errorf("same story in another issue must be allowed: %v", err)
	}
}
