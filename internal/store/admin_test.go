package store

import (
	"context"
	"testing"
)

func TestClassifyWorkspace(t *testing.T) {
	cases := []struct {
		name string
		w    WorkspaceInventory
		want string
	}{
		{"empty anonymous", WorkspaceInventory{ID: "a"}, WSOldCandidate},
		{"seeded publication only", WorkspaceInventory{ID: "a", Publications: 1, Issues: 2, Runs: 3}, WSOldCandidate},
		{"local demo", WorkspaceInventory{ID: "local"}, WSPreserve},
		{"claimed", WorkspaceInventory{ID: "a", ClaimedAt: "2026-11-01"}, WSPreserve},
		{"owner email", WorkspaceInventory{ID: "a", OwnerEmail: "x@y.z"}, WSPreserve},
		{"payment account", WorkspaceInventory{ID: "a", PaymentAccounts: 1}, WSPreserve},
		{"active subscription", WorkspaceInventory{ID: "a", ActiveSubscriptions: 1, Subscriptions: 1}, WSPreserve},
		{"readers", WorkspaceInventory{ID: "a", Readers: 1}, WSPreserve},
		{"sent issue", WorkspaceInventory{ID: "a", SentIssues: 1}, WSPreserve},
		{"new model pauta", WorkspaceInventory{ID: "a", PautaDays: 1}, WSNew},
		{"new model rendition", WorkspaceInventory{ID: "a", Renditions: 1}, WSNew},
		{"claimed wins over new", WorkspaceInventory{ID: "a", ClaimedAt: "x", PautaDays: 1}, WSPreserve},
		{"custom pub no pauta", WorkspaceInventory{ID: "a", Publications: 1, CustomPublications: 1}, WSAmbiguous},
		{"pending subscription", WorkspaceInventory{ID: "a", Subscriptions: 1}, WSAmbiguous},
		{"outbox rows", WorkspaceInventory{ID: "a", OutboxEmails: 2}, WSAmbiguous},
	}
	for _, c := range cases {
		if got, _ := ClassifyWorkspace(c.w); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

// TestWorkspaceInventories: a seeded workspace shows up with its counts and
// the read does not change the workspace table.
func TestWorkspaceInventories(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()
	if _, err := db.SeedPublication(ctx, testPub()); err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	var before int
	_ = pool.db.QueryRowContext(ctx, `SELECT count(*) FROM workspaces`).Scan(&before)
	list, err := pool.WorkspaceInventories(ctx)
	if err != nil {
		t.Fatalf("WorkspaceInventories: %v", err)
	}
	var found *WorkspaceInventory
	for i := range list {
		if list[i].ID == db.ws {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatalf("workspace %s missing from inventory", db.ws)
	}
	if found.Publications != 1 {
		t.Errorf("publications = %d, want 1", found.Publications)
	}
	var after int
	_ = pool.db.QueryRowContext(ctx, `SELECT count(*) FROM workspaces`).Scan(&after)
	if before != after || len(list) != after {
		t.Errorf("inventory not read-only/complete: before=%d after=%d listed=%d", before, after, len(list))
	}
}
