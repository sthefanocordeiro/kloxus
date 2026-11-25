// Package testdb gives tests a Postgres pool and isolated workspaces.
// Tests skip when DATABASE_URL is unset — CI always provides it.
package testdb

import (
	"context"
	"os"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// Pool opens a shared pool on DATABASE_URL (or skips the test).
func Pool(t *testing.T) *store.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set — Postgres tests skipped")
	}
	pool, err := store.Open(url)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// WS returns a fresh workspace-scoped store on Pool(t). The workspace
// row (and its cascaded data) is deleted at cleanup.
func WS(t *testing.T) *store.DB {
	t.Helper()
	return WSOn(t, Pool(t))
}

// WSOn returns a fresh workspace-scoped store on pool.
func WSOn(t *testing.T, pool *store.Pool) *store.DB {
	t.Helper()
	ctx := context.Background()
	id := workspace.NewID()
	if err := pool.CreateWorkspace(ctx, id); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.DeleteWorkspace(context.Background(), id); err != nil {
			t.Errorf("delete workspace %s: %v", id, err)
		}
	})
	return pool.For(id)
}
