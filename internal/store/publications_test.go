package store

import (
	"context"
	"testing"
)

// TestDeletePublicationWithPautaAndRenditions: pauta_days, renditions and
// issue_exports must not block or survive a publication delete (KLOX-195/A1).
func TestDeletePublicationWithPautaAndRenditions(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.CreatePublication(ctx, testPub())
	if err != nil {
		t.Fatalf("CreatePublication: %v", err)
	}
	issueID, err := db.CreateIssue(ctx, pubID, 1, "s", "b", testPub().Sections)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := db.SavePautaDay(ctx, pubID, "2026-11-24", "[]"); err != nil {
		t.Fatalf("SavePautaDay: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO renditions (workspace_id, issue_id, format) VALUES ($1, $2, 'x')`, db.ws, issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO issue_exports (workspace_id, issue_id, format) VALUES ($1, $2, 'x')`, db.ws, issueID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeletePublication(ctx, pubID); err != nil {
		t.Fatalf("DeletePublication: %v", err)
	}
	for _, tbl := range []string{"pauta_days", "renditions", "issue_exports"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+tbl+` WHERE workspace_id = $1`, db.ws).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s: %d orphan rows after delete", tbl, n)
		}
	}
}

// TestPublicationNichesPersistAndClear pins the store-level contract
// behind KLOX-274: editoria state is explicit rows, including the
// meaningful empty state after a clear.
func TestPublicationNichesPersistAndClear(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	def := testPub()
	def.Niches = []string{"agronegocio", "economia", "agronegocio"}
	pubID, err := db.CreatePublication(ctx, def)
	if err != nil {
		t.Fatalf("CreatePublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "test-pub")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	if len(pub.Niches) != 2 {
		t.Fatalf("stored niches = %v, want two deduped keys", pub.Niches)
	}

	preserve := testPub()
	if err := db.UpdatePublication(ctx, pubID, preserve); err != nil {
		t.Fatalf("UpdatePublication nil niches: %v", err)
	}
	pub, err = db.GetPublication(ctx, "test-pub")
	if err != nil {
		t.Fatalf("GetPublication after nil update: %v", err)
	}
	if len(pub.Niches) != 2 {
		t.Fatalf("nil niches update changed state to %v, want preserved", pub.Niches)
	}

	def.Niches = []string{}
	if err := db.UpdatePublication(ctx, pubID, def); err != nil {
		t.Fatalf("UpdatePublication clear: %v", err)
	}
	pub, err = db.GetPublication(ctx, "test-pub")
	if err != nil {
		t.Fatalf("GetPublication after clear: %v", err)
	}
	if pub.Niches == nil || len(pub.Niches) != 0 {
		t.Fatalf("niches after clear = %#v, want explicit empty slice", pub.Niches)
	}
}
