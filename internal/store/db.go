// Package store owns Postgres access: connection pool, embedded
// migrations, and workspace-scoped repositories.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sthefanocordeiro/kloxus/migrations"
)

// tsLayout is the 'YYYY-MM-DD HH24:MI:SS' UTC shape every timestamp
// column stores — the same strings the SQLite schema emitted. Lexical
// comparison is chronological, so range filters stay simple text
// comparisons.
const tsLayout = "2006-01-02 15:04:05"

// NowUTC returns the current time in the stored timestamp shape.
func NowUTC() string { return TSUTC(time.Now()) }

// TSUTC formats t in the stored timestamp shape.
func TSUTC(t time.Time) string { return t.UTC().Format(tsLayout) }

// NormalizeTS returns s as RFC3339 UTC when it parses — the single
// format for API/feed-facing timestamp fields (published_at,
// scheduled_at; KLOX-223). Inputs accepted: RFC3339 in any offset,
// the canonical stored layout and its 'T' variant, and bare ISO
// dates. Unparseable input passes through unchanged so the
// items_*_ts fallback (fetched date) still applies.
func NormalizeTS(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", tsLayout, time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s
}

// Pool is the shared Postgres connection pool. Repository methods live
// on DB, a workspace-scoped handle obtained from For.
type Pool struct {
	db *sql.DB
	// vectorMu/vectorOK/vectorDone cache the pgvector probe (KLOX-93):
	// the extension is either in the image at boot or it is not, but
	// only a completed lookup caches — a probe that errors (cancelled
	// context on the first call) retries next time instead of
	// disabling pgvector for the process lifetime.
	vectorMu   sync.Mutex
	vectorOK   bool
	vectorDone bool
}

// Open connects to the Postgres database at url (DATABASE_URL) and
// applies migrations. The exec query mode keeps the connection
// compatible with both the Supabase direct endpoint and the poolers.
func Open(url string) (*Pool, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set — point it at a Postgres database (local or Supabase)")
	}
	addParam := func(key, val string) {
		if strings.Contains(url, key+"=") {
			return
		}
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		url += sep + key + "=" + val
	}
	// statement_timeout (2 min) bounds every query server-side: a stuck
	// statement errors out instead of pinning a connection — and its
	// request — forever.
	addParam("default_query_exec_mode", "exec")
	addParam("statement_timeout", "120000")
	sdb, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(10)
	// Supabase's pooler drops idle connections; bound lifetimes so a
	// pooled handle never goes silently stale.
	sdb.SetConnMaxLifetime(30 * time.Minute)
	sdb.SetConnMaxIdleTime(5 * time.Minute)
	p := &Pool{db: sdb}
	if err := p.migrate(); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// Close releases the pool.
func (p *Pool) Close() error { return p.db.Close() }

// Ping verifies the database answers.
func (p *Pool) Ping() error { return p.db.Ping() }

// KeepAlive pings the pool on an interval until ctx ends. Supabase's
// pooler reaps idle connections, so after a quiet stretch every pooled
// handle is gone and the first query of the next request pays a fresh
// TCP+TLS+auth handshake on top of the normal RTT — one warm conn
// avoids that tail on the p95 criteria (KLOX-47).
func (p *Pool) KeepAlive(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			p.db.PingContext(pctx)
			cancel()
		}
	}
}

// For returns the repository handle scoped to workspace id. Every query
// a DB method runs carries that workspace's id — no repo method can
// read or write another tenant's rows.
func (p *Pool) For(workspaceID string) *DB {
	return &DB{db: p.db, ws: workspaceID}
}

// sqlQuerier is the small surface both the pool root and a transaction
// implement. Repository methods route through it so a transaction-scoped
// DB runs the exact same SQL inside the open transaction.
type sqlQuerier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// DB is a workspace-scoped repository. The ws field is the tenant
// boundary: injected into every WHERE clause and INSERT it makes
// cross-workspace access impossible at the store layer.
type DB struct {
	db *sql.DB
	tx *sql.Tx
	ws string
}

// executor returns the transaction when one is active, or the pool root.
func (db *DB) executor() sqlQuerier {
	if db.tx != nil {
		return db.tx
	}
	return db.db
}

// txHandle is either a real transaction or a nested view of one. Nested
// commit/rollback are intentionally no-ops: the outer transaction remains
// the atomic boundary, and its failure still rolls every write back.
type txHandle struct {
	sqlQuerier
	commit   func() error
	rollback func() error
}

func (tx *txHandle) Commit() error   { return tx.commit() }
func (tx *txHandle) Rollback() error { return tx.rollback() }

// beginTx starts a transaction, or joins the already-open one. Store
// methods that historically managed their own transaction use this so they
// can also compose inside a larger atomic unit.
func (db *DB) beginTx(ctx context.Context) (*txHandle, error) {
	if db.tx != nil {
		return &txHandle{
			sqlQuerier: db.tx,
			commit:     func() error { return nil },
			rollback:   func() error { return nil },
		}, nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &txHandle{sqlQuerier: tx, commit: tx.Commit, rollback: tx.Rollback}, nil
}

// WithTx runs fn inside one database transaction. When the handle is
// already transaction-scoped it runs fn inline: the outer transaction is
// the atomic boundary, so a later failure still rolls every write back.
func (db *DB) WithTx(ctx context.Context, fn func(*DB) error) error {
	if db.tx != nil {
		return fn(db)
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(&DB{db: db.db, tx: tx, ws: db.ws}); err != nil {
		return err
	}
	return tx.Commit()
}

// Workspace returns the id this handle is scoped to.
func (db *DB) Workspace() string { return db.ws }

// Close is a no-op: a scoped handle borrows the pool and owns nothing.
// Kept so workspace teardown code stays uniform.
func (db *DB) Close() error { return nil }

// Ping verifies the underlying pool answers.
func (db *DB) Ping() error { return db.db.Ping() }

// ExecContext runs a raw statement against the shared pool — or the open
// transaction on a transaction-scoped handle. Callers are responsible for
// scoping tenant tables themselves; repo methods are the safe path.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.executor().ExecContext(ctx, query, args...)
}

// QueryContext runs a raw query against the shared pool or transaction.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.executor().QueryContext(ctx, query, args...)
}

// QueryRowContext runs a raw query against the shared pool or transaction.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.executor().QueryRowContext(ctx, query, args...)
}

// migrateLockKey serializes migrate() across every session — concurrent
// Open calls (parallel test packages, a second Fly machine booting)
// would otherwise both see a version unapplied and run the same DDL
// twice (duplicate pg_proc/pg_type entries).
const migrateLockKey int64 = 0x6b6c6f787573

// Migration contract (KLOX-222, DBA review M1):
//
//   - Files are named NNNN_descricao.sql and apply in lexical order.
//     The NNNN prefix must be unique — a number is never reused. A
//     collision meant apply order depended on filename sorting luck;
//     boot now fails on a duplicate or malformed name instead.
//   - schema_migrations.version stores the full file name, so renaming
//     a file would make the runner re-apply it. migrationRenames maps
//     each pre-renumber name to its current name and the row is
//     rewritten on the next migrate, converging already-migrated
//     databases. Entries are permanent — a database that skipped
//     releases still needs the rename.
//   - checksum is the sha256 hex of the file bytes, recorded at apply
//     time. Rows recorded before checksums existed adopt the current
//     file's hash on the first migrate after upgrade; a stored checksum
//     that differs means an applied file was edited — boot fails
//     instead of letting environments diverge silently.
//   - Rows whose version matches no file are ignored: they cannot
//     re-apply anything, and flagging them would brick a database that
//     once ran a build shipping an extra file.
var migrationRenames = map[string]string{
	"0002_ingest.sql":                 "0003_ingest.sql",
	"0002_llm_guards.sql":             "0004_llm_guards.sql",
	"0002_story_dedupe_key.sql":       "0005_story_dedupe_key.sql",
	"0003_candidate_set.sql":          "0006_candidate_set.sql",
	"0004_test_send.sql":              "0007_test_send.sql",
	"0005_ai_enrichment.sql":          "0008_ai_enrichment.sql",
	"0005_issue_sections.sql":         "0009_issue_sections.sql",
	"0006_composer_blocks.sql":        "0010_composer_blocks.sql",
	"0007_newsletter_template.sql":    "0011_newsletter_template.sql",
	"0008_aderencia.sql":              "0012_aderencia.sql",
	"0008_daily_journal_pgvector.sql": "0013_daily_journal_pgvector.sql",
	"0008_payments.sql":               "0014_payments.sql",
	"0009_daily_journal_off.sql":      "0015_daily_journal_off.sql",
	"0009_ranking_embeddings.sql":     "0016_ranking_embeddings.sql",
	"0010_stage1_ranking.sql":         "0017_stage1_ranking.sql",
	"0011_feedback_sections.sql":      "0018_feedback_sections.sql",
	"0011_waitlist.sql":               "0019_waitlist.sql",
	"0012_item_summaries.sql":         "0020_item_summaries.sql",
	"0013_pauta_days.sql":             "0021_pauta_days.sql",
	"0013_renditions.sql":             "0022_renditions.sql",
	"0014_tabnews_off.sql":            "0023_tabnews_off.sql",
	"0015_br_news_portals.sql":        "0024_br_news_portals.sql",
	"0016_pauta_mail_days.sql":        "0025_pauta_mail_days.sql",
	"0017_multi_editoria_sources.sql": "0026_multi_editoria_sources.sql",
	"0018_issue_exports.sql":          "0027_issue_exports.sql",
	"0018_story_feedback.sql":         "0028_story_feedback.sql",
	"0019_issue_item_title.sql":       "0029_issue_item_title.sql",
	"0020_store_integrity.sql":        "0030_store_integrity.sql",
}

// migrationNumber returns the NNNN prefix of a migration file name.
func migrationNumber(name string) (string, bool) {
	if len(name) < 6 || name[4] != '_' {
		return "", false
	}
	p := name[:4]
	for i := 0; i < 4; i++ {
		if p[i] < '0' || p[i] > '9' {
			return "", false
		}
	}
	return p, true
}

func (p *Pool) migrate() error {
	// The advisory lock is session-scoped, so hold it on a dedicated conn;
	// the migrations themselves may run on other pooled conns.
	lockConn, err := p.db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer lockConn.Close()
	if _, err := lockConn.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, migrateLockKey); err != nil {
		return err
	}
	defer lockConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrateLockKey)
	if _, err := p.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'))
	);`); err != nil {
		return err
	}
	if _, err := p.db.Exec(`ALTER TABLE schema_migrations
		ADD COLUMN IF NOT EXISTS checksum TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	// Converge versions recorded under pre-renumber names. The DELETE
	// covers the pathological case where both names were recorded.
	for old, cur := range migrationRenames {
		if _, err := p.db.Exec(`DELETE FROM schema_migrations WHERE version = $1
			AND EXISTS (SELECT 1 FROM schema_migrations WHERE version = $2)`, old, cur); err != nil {
			return err
		}
		if _, err := p.db.Exec(`UPDATE schema_migrations SET version = $2 WHERE version = $1`, old, cur); err != nil {
			return err
		}
	}
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	// Enforce the numbering contract before touching the database.
	seen := map[string]string{}
	for _, name := range names {
		num, ok := migrationNumber(name)
		if !ok {
			return fmt.Errorf("migration file %q does not match the NNNN_descricao.sql contract", name)
		}
		if prev, dup := seen[num]; dup {
			return fmt.Errorf("migration number %s is used by both %s and %s — numbers never repeat", num, prev, name)
		}
		seen[num] = name
	}
	// Validate every applied file (and adopt checksums on legacy rows)
	// before applying anything pending, so a tampered file fails the
	// boot instead of sitting under newly applied migrations.
	var pending []struct {
		name string
		body []byte
		sum  string
	}
	for _, name := range names {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(body))
		var stored string
		err = p.db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version = $1`, name).Scan(&stored)
		switch {
		case err == sql.ErrNoRows:
			pending = append(pending, struct {
				name string
				body []byte
				sum  string
			}{name, body, sum})
		case err != nil:
			return err
		case stored == "":
			if _, err := p.db.Exec(`UPDATE schema_migrations SET checksum = $2 WHERE version = $1`, name, sum); err != nil {
				return err
			}
		case stored != sum:
			return fmt.Errorf("migration %s: file content changed after it was applied (stored checksum %s, file %s)", name, stored, sum)
		}
	}
	for _, m := range pending {
		tx, err := p.db.Begin()
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(m.body)) {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %s: %w", m.name, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`, m.name, m.sum); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements splits a migration file on top-level semicolons.
// '--' line comments are dropped during the scan itself, so a ';'
// inside a comment can never split a statement (KLOX-67 rework: CI
// failed applying 0005 for exactly that). Single-quoted literals and
// dollar-quoted bodies are respected the same way — their ';' and '--'
// are content, not syntax.
func splitStatements(body string) []string {
	var out []string
	var b strings.Builder
	inDollar, inQuote := false, false
	flush := func() {
		if stmt := strings.TrimSpace(b.String()); stmt != "" {
			out = append(out, stmt)
		}
		b.Reset()
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case inDollar:
			b.WriteByte(c)
			if c == '$' && i+1 < len(body) && body[i+1] == '$' {
				b.WriteByte('$')
				i++
				inDollar = false
			}
		case inQuote:
			b.WriteByte(c)
			if c == '\'' {
				if i+1 < len(body) && body[i+1] == '\'' {
					b.WriteByte('\'')
					i++
				} else {
					inQuote = false
				}
			}
		case c == '$' && i+1 < len(body) && body[i+1] == '$':
			inDollar = true
			b.WriteString("$$")
			i++
		case c == '\'':
			inQuote = true
			b.WriteByte(c)
		case c == '-' && i+1 < len(body) && body[i+1] == '-':
			for i < len(body) && body[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == ';':
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return out
}
