// Package store owns Postgres access: connection pool, embedded
// migrations, and workspace-scoped repositories.
package store

import (
	"context"
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

// NormTS normalizes an externally-sourced timestamp to RFC3339 UTC —
// the single shape feed/API-facing columns (items.published_at,
// issues.scheduled_at) store so their values are unambiguous and
// lexically ordered (KLOX-218). The internal tsLayout shape and a bare
// ISO date or zone-less 'T' time are read as UTC; input under no known
// layout is returned unchanged — the column keeps the raw value and
// eff_at/safe_ts fall back on the read path.
func NormTS(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	for _, l := range []string{time.RFC3339Nano, tsLayout, "2006-01-02T15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(l, t); err == nil {
			return parsed.UTC().Format(time.RFC3339)
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

// DB is a workspace-scoped repository. The ws field is the tenant
// boundary: injected into every WHERE clause and INSERT it makes
// cross-workspace access impossible at the store layer.
type DB struct {
	db *sql.DB
	ws string
}

// Workspace returns the id this handle is scoped to.
func (db *DB) Workspace() string { return db.ws }

// Close is a no-op: a scoped handle borrows the pool and owns nothing.
// Kept so workspace teardown code stays uniform.
func (db *DB) Close() error { return nil }

// Ping verifies the underlying pool answers.
func (db *DB) Ping() error { return db.db.Ping() }

// ExecContext runs a raw statement against the shared pool. Callers are
// responsible for scoping tenant tables themselves; repo methods are
// the safe path.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.db.ExecContext(ctx, query, args...)
}

// QueryContext runs a raw query against the shared pool.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.db.QueryContext(ctx, query, args...)
}

// QueryRowContext runs a raw query against the shared pool.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.db.QueryRowContext(ctx, query, args...)
}

// migrateLockKey serializes migrate() across every session — concurrent
// Open calls (parallel test packages, a second Fly machine booting)
// would otherwise both see a version unapplied and run the same DDL
// twice (duplicate pg_proc/pg_type entries).
const migrateLockKey int64 = 0x6b6c6f787573

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
	for _, name := range names {
		var applied int
		if err := p.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version = $1`, name).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := p.db.Begin()
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(body)) {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %s: %w", name, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
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
