package store

import (
	"database/sql"
	"strings"
	"testing"
)

// TestMigration0035LookupIndexesUpgrade replays the pre-0035 migration chain
// on a scratch database, applies 0035 as an upgrade, and verifies the lookup
// indexes exist with the intended key columns. TestMigrate covers the fresh
// database path through the migration runner.
func TestMigration0035LookupIndexesUpgrade(t *testing.T) {
	db := scratchDB(t, "0035_lookup_indexes.sql", func(t *testing.T, db *sql.DB) {})
	for index, column := range map[string]string{
		"idx_story_items_item":     "item_id",
		"idx_outbox_provider_id":   "provider_id",
		"idx_decisions_issue_item": "issue_item_id",
	} {
		var def string
		if err := db.QueryRow(
			`SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`, index).Scan(&def); err != nil {
			t.Fatalf("lookup index %s missing after upgrade: %v", index, err)
		}
		if !strings.Contains(def, "("+column+")") {
			t.Errorf("lookup index %s definition %q does not key (%s)", index, def, column)
		}
	}
}
