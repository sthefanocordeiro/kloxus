package store

// KLOX-223: the *_ts backfill/normalization migration (0032) is
// exercised on a scratch database replaying the pre-migration chain,
// so the test proves upgrade behavior on mixed stored data — both
// timestamp shapes plus unparseable values — not just the fresh-init
// path every other store test covers. Adapted from the KLOX-218
// eff_at variant of the same exercise (PR #163, superseded by #164).
// Skips without DATABASE_URL.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/migrations"
)

func TestNormalizeTS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2026-11-20T10:00:00Z", "2026-11-20T10:00:00Z"},
		{"2026-11-20T10:00:00+03:00", "2026-11-20T07:00:00Z"},
		{"2026-11-20T10:00:00.5Z", "2026-11-20T10:00:00Z"},
		{"2026-11-20 10:00:00", "2026-11-20T10:00:00Z"},
		{"2026-11-20T10:00:00", "2026-11-20T10:00:00Z"},
		{"2026-11-20", "2026-11-20T00:00:00Z"},
		{"  2026-11-20T10:00:00Z  ", "2026-11-20T10:00:00Z"},
		{"", ""},
		{"   ", ""},
		{"trash", "trash"},
		{"20 Nov 2026", "20 Nov 2026"},
	}
	for _, c := range cases {
		if got := NormalizeTS(c.in); got != c.want {
			t.Errorf("NormalizeTS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// scratchDB creates a throwaway database on the test server, applies
// every migration whose filename sorts before `upto` (exclusive), runs
// seed, then applies `upto` itself — a real upgrade path, not a fresh
// Open(). Returns the scratch handle; the database is dropped on
// cleanup.
func scratchDB(t *testing.T, upto string, seed func(t *testing.T, db *sql.DB)) *sql.DB {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set — Postgres tests skipped")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("DATABASE_URL: %v", err)
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	defer admin.Close()

	b := make([]byte, 8)
	rand.Read(b)
	name := "kloxus_mig_" + hex.EncodeToString(b)
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("create scratch db: %v", err)
	}
	t.Cleanup(func() {
		admin2, err := sql.Open("pgx", base)
		if err != nil {
			return
		}
		defer admin2.Close()
		admin2.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
	})

	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatalf("open scratch: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	apply := func(fname string) {
		t.Helper()
		body, err := migrations.FS.ReadFile(fname)
		if err != nil {
			t.Fatalf("read %s: %v", fname, err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin %s: %v", fname, err)
		}
		for _, stmt := range splitStatements(string(body)) {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				t.Fatalf("%s: %v", fname, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit %s: %v", fname, err)
		}
	}

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
	sort.Strings(names)
	for _, n := range names {
		if n >= upto {
			break
		}
		apply(n)
	}
	seed(t, db)
	apply(upto)
	return db
}

func TestMigration0032Upgrade(t *testing.T) {
	var catID, pubID int64
	db := scratchDB(t, "0032_items_timestamptz.sql", func(t *testing.T, db *sql.DB) {
		must := func(q string, args ...any) {
			t.Helper()
			if _, err := db.Exec(q, args...); err != nil {
				t.Fatalf("seed: %v\n%s", err, q)
			}
		}
		must(`INSERT INTO workspaces (id) VALUES ('ws1')`)
		must(`INSERT INTO source_catalog (slug, name, url, policy)
		      VALUES ('cat', 'Cat', 'https://x', 'launch')`)
		must(`INSERT INTO publications (workspace_id, slug, name) VALUES ('ws1', 'p', 'Pub')`)
		if err := db.QueryRow(`SELECT id FROM source_catalog WHERE slug='cat'`).Scan(&catID); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT id FROM publications WHERE slug='p'`).Scan(&pubID); err != nil {
			t.Fatal(err)
		}
		items := []struct{ ext, pub, fet string }{
			{"a", "2026-11-20T10:00:00Z", "2026-11-21 00:00:00"},     // RFC3339 Z — stays
			{"b", "2026-11-20 10:00:00", "2026-11-21 00:00:00"},     // space — normalized
			{"c", "2026-11-20T10:00:00+03:00", "2026-11-21 00:00:00"}, // offset — re-based to Z
			{"d", "trash!!", "2026-11-21 05:00:00"},                 // garbage — kept raw, ts NULL
			{"e", "", "2026-11-22 00:00:00"},                        // empty — stays empty
			{"h", "2026-11-19", "2026-11-22 00:00:00"},              // bare date — UTC midnight
		}
		for _, it := range items {
			must(`INSERT INTO items (catalog_id, external_id, url, title, published_at, fetched_at, content_text, kind)
			      VALUES ($1,$2,'u','t',$3,$4,'body','article')`, catID, it.ext, it.pub, it.fet)
		}
		must(`INSERT INTO items (catalog_id, external_id, url, title, published_at, fetched_at, content_text, kind)
		      VALUES ($1,'f','u','t',NULL,'2026-11-23 00:00:00','body','article')`, catID)
		must(`INSERT INTO issues (workspace_id, publication_id, number, status, scheduled_at) VALUES
		      ('ws1',$1,1,'scheduled','2026-11-30 08:00:00'),
		      ('ws1',$1,2,'scheduled','2026-12-01T09:00:00+03:00'),
		      ('ws1',$1,3,'scheduled','nonsense')`, pubID)
	})
	ctx := context.Background()

	// Stored published_at: parseable values normalized to RFC3339 UTC;
	// unparseable text is preserved verbatim (its published_ts is NULL
	// and reads fall back to fetched_ts), empty stays empty.
	wantPub := map[string]string{
		"a": "2026-11-20T10:00:00Z",
		"b": "2026-11-20T10:00:00Z",
		"c": "2026-11-20T07:00:00Z",
		"d": "trash!!",
		"e": "",
		"h": "2026-11-19T00:00:00Z",
	}
	for ext, w := range wantPub {
		var got string
		if err := db.QueryRowContext(ctx,
			`SELECT published_at FROM items WHERE external_id=$1`, ext).Scan(&got); err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if got != w {
			t.Errorf("item %s published_at = %q, want %q", ext, got, w)
		}
	}
	var nNull int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM items WHERE external_id='f' AND published_at IS NULL`).Scan(&nNull); err != nil {
		t.Fatal(err)
	}
	if nNull != 1 {
		t.Error("NULL published_at should stay NULL after migration")
	}

	// published_ts mirrors parseable published_at in UTC; garbage,
	// empty and NULL inputs all yield NULL.
	wantPubTS := map[string]string{
		"a": "2026-11-20 10:00:00",
		"b": "2026-11-20 10:00:00",
		"c": "2026-11-20 07:00:00",
		"h": "2026-11-19 00:00:00",
	}
	for ext, w := range wantPubTS {
		var got time.Time
		if err := db.QueryRowContext(ctx,
			`SELECT published_ts AT TIME ZONE 'UTC' FROM items WHERE external_id=$1`, ext).Scan(&got); err != nil {
			t.Fatalf("published_ts %s: %v", ext, err)
		}
		if got.UTC().Format("2006-01-02 15:04:05") != w {
			t.Errorf("item %s published_ts = %v, want %s", ext, got, w)
		}
	}
	for _, ext := range []string{"d", "e", "f"} {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM items WHERE external_id=$1 AND published_ts IS NULL`, ext).Scan(&n); err != nil || n != 1 {
			t.Errorf("item %s published_ts should be NULL", ext)
		}
	}

	// fetched_ts mirrors every stored fetched_at.
	wantFetTS := map[string]string{
		"a": "2026-11-21 00:00:00",
		"b": "2026-11-21 00:00:00",
		"c": "2026-11-21 00:00:00",
		"d": "2026-11-21 05:00:00",
		"e": "2026-11-22 00:00:00",
		"f": "2026-11-23 00:00:00",
		"h": "2026-11-22 00:00:00",
	}
	for ext, w := range wantFetTS {
		var got time.Time
		if err := db.QueryRowContext(ctx,
			`SELECT fetched_ts AT TIME ZONE 'UTC' FROM items WHERE external_id=$1`, ext).Scan(&got); err != nil {
			t.Fatalf("fetched_ts %s: %v", ext, err)
		}
		if got.UTC().Format("2006-01-02 15:04:05") != w {
			t.Errorf("item %s fetched_ts = %v, want %s", ext, got, w)
		}
	}

	// The effective-time expression reads use: published when
	// parseable, else fetched.
	wantEff := map[string]string{
		"a": "2026-11-20 10:00:00",
		"b": "2026-11-20 10:00:00",
		"c": "2026-11-20 07:00:00",
		"d": "2026-11-21 05:00:00",
		"e": "2026-11-22 00:00:00",
		"f": "2026-11-23 00:00:00",
		"h": "2026-11-19 00:00:00",
	}
	for ext, w := range wantEff {
		var got time.Time
		if err := db.QueryRowContext(ctx,
			`SELECT COALESCE(published_ts, fetched_ts) AT TIME ZONE 'UTC'
			   FROM items WHERE external_id=$1`, ext).Scan(&got); err != nil {
			t.Fatalf("eff %s: %v", ext, err)
		}
		if got.UTC().Format("2006-01-02 15:04:05") != w {
			t.Errorf("item %s effective ts = %v, want %s", ext, got, w)
		}
	}

	// issues.scheduled_at normalized the same way; unparseable keeps
	// its raw text.
	var s1, s2, s3 string
	db.QueryRowContext(ctx, `SELECT scheduled_at FROM issues WHERE number=1`).Scan(&s1)
	db.QueryRowContext(ctx, `SELECT scheduled_at FROM issues WHERE number=2`).Scan(&s2)
	db.QueryRowContext(ctx, `SELECT scheduled_at FROM issues WHERE number=3`).Scan(&s3)
	if s1 != "2026-11-30T08:00:00Z" || s2 != "2026-12-01T06:00:00Z" || s3 != "nonsense" {
		t.Errorf("scheduled_at = %q %q %q", s1, s2, s3)
	}

	// Indexes exist; safe_ts is retained for code-rollback compat.
	for _, idx := range []string{"idx_items_eff_ts", "idx_items_fetched_ts"} {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_indexes WHERE indexname=$1`, idx).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s missing", idx)
		}
	}
	var nSafe int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_proc WHERE proname='safe_ts'`).Scan(&nSafe); err != nil || nSafe != 1 {
		t.Fatal("safe_ts should still exist — migration stays additive")
	}

	// Trigger: a new insert and a published_at revision both recompute.
	if _, err := db.ExecContext(ctx, `INSERT INTO items
		(catalog_id, external_id, url, title, published_at, fetched_at, content_text, kind)
		VALUES ($1,'g','u','t','2026-11-24 12:00:00','2026-11-25 00:00:00','b','article')`, catID); err != nil {
		t.Fatal(err)
	}
	var g time.Time
	if err := db.QueryRowContext(ctx,
		`SELECT published_ts AT TIME ZONE 'UTC' FROM items WHERE external_id='g'`).Scan(&g); err != nil {
		t.Fatal(err)
	}
	if g.UTC().Format("2006-01-02 15:04:05") != "2026-11-24 12:00:00" {
		t.Errorf("insert trigger published_ts = %v", g)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE items SET published_at='2026-11-24T18:00:00Z' WHERE external_id='g'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT published_ts AT TIME ZONE 'UTC' FROM items WHERE external_id='g'`).Scan(&g); err != nil {
		t.Fatal(err)
	}
	if g.UTC().Format("2006-01-02 15:04:05") != "2026-11-24 18:00:00" {
		t.Errorf("update trigger published_ts = %v", g)
	}

	// to_utc_ts pins zone-less input to UTC, not the session zone: a
	// write from a non-UTC session must still land at UTC midnight —
	// the exact skew safe_ts had before.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET TIME ZONE 'America/Sao_Paulo'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO items
		(catalog_id, external_id, url, title, published_at, fetched_at, content_text, kind)
		VALUES ($1,'h2','u','t','2026-11-18','2026-11-18 10:00:00','b','article')`, catID); err != nil {
		t.Fatal(err)
	}
	var h2 time.Time
	if err := conn.QueryRowContext(ctx,
		`SELECT published_ts AT TIME ZONE 'UTC' FROM items WHERE external_id='h2'`).Scan(&h2); err != nil {
		t.Fatal(err)
	}
	if h2.UTC().Format("2006-01-02 15:04:05") != "2026-11-18 00:00:00" {
		t.Errorf("bare date under non-UTC session: published_ts = %v, want 2026-11-18 00:00:00 UTC", h2)
	}
	if _, err := conn.ExecContext(ctx, `SET TIME ZONE DEFAULT`); err != nil {
		t.Fatal(err)
	}
}

// TestSendsTodayRange pins the sargable day filter at the day boundary
// (KLOX-223): rows at 23:59:59 count, rows at next-day 00:00:00 don't.
func TestSendsTodayRange(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	ws := db.Workspace()
	rows := []struct{ created, provider string }{
		{"2026-11-25 00:00:00", "resend"},
		{"2026-11-25 23:59:59", "resend"},
		{"2026-11-26 00:00:00", "resend"},
		{"2026-11-25 12:00:00", "log"},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, `INSERT INTO outbox_emails
			(workspace_id, kind, to_addr, provider, status, created_at)
			VALUES ($1,'issue','x@y',$2,'sent',$3)`, ws, r.provider, r.created); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.SendsToday(ctx, "2026-11-25")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("SendsToday = %d, want 2", n)
	}
}
