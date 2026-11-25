package store

// Migration-runner tests (KLOX-222): the contract test is pure and runs
// without Postgres; the end-to-end test opens a throwaway database on
// DATABASE_URL's server so tamper scenarios never touch the shared
// test database. Skips without DATABASE_URL like every store test.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/migrations"
)

// migrationFiles returns the embedded .sql file names in apply order.
func migrationFiles(t *testing.T) []string {
	t.Helper()
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	return names
}

// TestMigrationContract pins the NNNN_descricao numbering contract and
// the consistency of migrationRenames — the two pieces a future
// migration can break without ever touching a database.
func TestMigrationContract(t *testing.T) {
	files := migrationFiles(t)
	if len(files) == 0 {
		t.Fatal("no migration files embedded")
	}
	present := map[string]bool{}
	seen := map[string]string{}
	for _, name := range files {
		present[name] = true
		num, ok := migrationNumber(name)
		if !ok {
			t.Errorf("migration file %q does not match NNNN_descricao.sql", name)
			continue
		}
		if prev, dup := seen[num]; dup {
			t.Errorf("migration number %s used by both %s and %s", num, prev, name)
		}
		seen[num] = name
	}
	for old, cur := range migrationRenames {
		if present[old] {
			t.Errorf("migrationRenames key %q still exists as a file", old)
		}
		if !present[cur] {
			t.Errorf("migrationRenames target %q is not a file", cur)
		}
	}
}

// TestItemSummariesConsolidated guards the KLOX-222 dedupe: the second
// item_summaries file is comments-only, so applying it is a no-op.
func TestItemSummariesConsolidated(t *testing.T) {
	body, err := migrations.FS.ReadFile("0020_item_summaries.sql")
	if err != nil {
		t.Fatal(err)
	}
	if stmts := splitStatements(string(body)); len(stmts) != 0 {
		t.Fatalf("0020_item_summaries.sql should apply zero statements, got %d", len(stmts))
	}
}

// tempDatabase creates a throwaway database on DATABASE_URL's server
// and returns its URL. Dropped at test cleanup.
func tempDatabase(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		t.Skip("DATABASE_URL not set — Postgres tests skipped")
	}
	admin, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	defer admin.Close()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	name := "kloxus_migtest_" + hex.EncodeToString(b)
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("create temp database: %v", err)
	}
	t.Cleanup(func() {
		adm, err := sql.Open("pgx", raw)
		if err != nil {
			t.Errorf("cleanup connect: %v", err)
			return
		}
		defer adm.Close()
		if _, err := adm.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`); err != nil {
			t.Errorf("drop temp database %s: %v", name, err)
		}
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// TestMigrate exercises the runner end-to-end on a fresh database:
// apply-all with checksums, idempotent re-run, convergence of rows
// recorded under pre-renumber names, and checksum tamper detection.
func TestMigrate(t *testing.T) {
	pool, err := Open(tempDatabase(t))
	if err != nil {
		t.Fatalf("Open fresh db: %v", err)
	}
	defer pool.Close()
	ctx := context.Background()

	files := migrationFiles(t)

	countVersions := func() int {
		var n int
		if err := pool.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
			t.Fatalf("count schema_migrations: %v", err)
		}
		return n
	}

	if n := countVersions(); n != len(files) {
		t.Fatalf("fresh migrate recorded %d versions, want %d", n, len(files))
	}
	var missing int
	if err := pool.db.QueryRowContext(ctx,
		`SELECT count(*) FROM schema_migrations WHERE length(checksum) <> 64`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Fatalf("%d applied migrations lack a sha256 checksum", missing)
	}

	// Re-running migrate on a migrated database applies nothing.
	if err := pool.migrate(); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	if n := countVersions(); n != len(files) {
		t.Fatalf("idempotent re-migrate changed version count to %d", n)
	}

	// A row recorded under a pre-renumber name converges to the new
	// name with an adopted checksum — without re-applying the file
	// (its ALTER is not idempotent, so a re-apply would fail).
	if _, err := pool.db.ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE version = '0005_story_dedupe_key.sql'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.db.ExecContext(ctx,
		`INSERT INTO schema_migrations (version) VALUES ('0002_story_dedupe_key.sql')`); err != nil {
		t.Fatal(err)
	}
	// Versions matching no file are inert and must not block the boot.
	if _, err := pool.db.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, checksum) VALUES ('9999_gone.sql', 'x')`); err != nil {
		t.Fatal(err)
	}
	if err := pool.migrate(); err != nil {
		t.Fatalf("rename convergence migrate: %v", err)
	}
	var sum string
	err = pool.db.QueryRowContext(ctx,
		`SELECT checksum FROM schema_migrations WHERE version = '0005_story_dedupe_key.sql'`).Scan(&sum)
	if err != nil {
		t.Fatalf("renamed row missing: %v", err)
	}
	if len(sum) != 64 {
		t.Fatalf("renamed row has checksum %q, want sha256 hex", sum)
	}
	var stale int
	if err := pool.db.QueryRowContext(ctx,
		`SELECT count(*) FROM schema_migrations WHERE version = '0002_story_dedupe_key.sql'`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatal("pre-renumber version row survived convergence")
	}

	// Editing an applied file must fail the next boot.
	if _, err := pool.db.ExecContext(ctx,
		`UPDATE schema_migrations SET checksum = 'deadbeef' WHERE version = '0001_init.sql'`); err != nil {
		t.Fatal(err)
	}
	err = pool.migrate()
	if err == nil {
		t.Fatal("tampered checksum did not fail migrate")
	}
	if !strings.Contains(err.Error(), "0001_init.sql") {
		t.Fatalf("checksum error does not name the file: %v", err)
	}
}

func TestMigrationNumber(t *testing.T) {
	for name, want := range map[string]string{
		"0001_init.sql":  "0001",
		"0030_x_y_z.sql": "0030",
		"9999.sql":       "", // missing '_'
		"001_init.sql":   "", // three digits
		"00001_init.sql": "", // five digits
		"abcd_init.sql":  "",
		"0001init.sql":   "",
		"migrations.go":  "",
		"0001_.sql":      "0001",
	} {
		got, _ := migrationNumber(name)
		if got != want {
			t.Errorf("migrationNumber(%q) = %q, want %q", name, got, want)
		}
	}
}
