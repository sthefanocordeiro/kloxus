// Package store owns Postgres access: connection pool, embedded
// migrations, and workspace-scoped repositories.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sthefanocordeiro/kloxus/internal/config"
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

// Publication is a stored publication row with decoded JSON fields.
type Publication struct {
	ID            int64
	Slug          string
	Name          string
	Language      string
	Topic         string
	Audience      string
	EditorialLine string
	// RejectTerms: creator-declared themes the aderência ranking
	// demotes and the auto-lineup never slots (KLOX-94).
	RejectTerms  string
	Tone         string
	Frequency    string
	Sections     []config.Section
	Paywall      config.Paywall
	AIDisclosure string
	Custom       bool
	Template     []TemplateBlock
	// TemplateRaw is the stored template_json — the compare-and-swap
	// key template saves check so a concurrent write conflicts instead
	// of clobbering (KLOX-72).
	TemplateRaw string
}

// TemplateBlock is one entry in the newsletter's default edition
// template (KLOX-72): a fixed composer block copied into every new
// edition — or a "section" slot the week's pautas fill at
// materialization. Kind/text/url mirror the composer block schema;
// section carries the section title for kind=section.
type TemplateBlock struct {
	Kind    string `json:"kind"` // section | heading | divider | link | image | embed | text
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Section string `json:"section,omitempty"`
}

func (db *DB) scanPublication(row *sql.Row) (*Publication, error) {
	var p Publication
	var sections, paywall, template string
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Language, &p.Topic, &p.Audience,
		&p.EditorialLine, &p.RejectTerms, &p.Tone, &p.Frequency, &sections, &paywall, &p.AIDisclosure, &p.Custom, &template)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(sections), &p.Sections); err != nil {
		return nil, fmt.Errorf("decoding sections for %q: %w", p.Slug, err)
	}
	if err := json.Unmarshal([]byte(paywall), &p.Paywall); err != nil {
		return nil, fmt.Errorf("decoding paywall for %q: %w", p.Slug, err)
	}
	if err := json.Unmarshal([]byte(template), &p.Template); err != nil {
		return nil, fmt.Errorf("decoding template for %q: %w", p.Slug, err)
	}
	p.TemplateRaw = template
	return &p, nil
}

const publicationCols = `id, slug, name, language, topic, audience, editorial_line, reject_terms, tone, frequency, sections_json, paywall_json, ai_disclosure, custom, template_json`

// GetPublication resolves a publication by numeric id or slug.
func (db *DB) GetPublication(ctx context.Context, ref string) (*Publication, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return db.scanPublication(db.QueryRowContext(ctx,
			`SELECT `+publicationCols+` FROM publications WHERE workspace_id = $1 AND id = $2`, db.ws, id))
	}
	return db.scanPublication(db.QueryRowContext(ctx,
		`SELECT `+publicationCols+` FROM publications WHERE workspace_id = $1 AND slug = $2`, db.ws, ref))
}

// ListPublications returns all publications ordered by name.
func (db *DB) ListPublications(ctx context.Context) ([]*Publication, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+publicationCols+` FROM publications WHERE workspace_id = $1 ORDER BY name`, db.ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Publication
	for rows.Next() {
		var p Publication
		var sections, paywall, template string
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Language, &p.Topic, &p.Audience,
			&p.EditorialLine, &p.RejectTerms, &p.Tone, &p.Frequency, &sections, &paywall, &p.AIDisclosure, &p.Custom, &template); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(sections), &p.Sections)
		json.Unmarshal([]byte(paywall), &p.Paywall)
		json.Unmarshal([]byte(template), &p.Template)
		p.TemplateRaw = template
		out = append(out, &p)
	}
	return out, rows.Err()
}

// HasCanonicalPublication reports whether any publication is template-seeded
// (not creator-made). The workspace config refresh only touches canonical
// rows — a publication the creator wrote keeps its edited blocks.
func (db *DB) HasCanonicalPublication(ctx context.Context) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM publications WHERE workspace_id = $1 AND NOT custom`, db.ws).Scan(&n)
	return n > 0, err
}

// CreatePublication stores a creator-made publication (custom=true). Unlike
// SeedPublication it does not attach sources — onboarding offers them on a
// separate screen — and it slugifies the name into a unique slug.
func (db *DB) CreatePublication(ctx context.Context, p *config.Publication) (int64, error) {
	sections, _ := json.Marshal(p.Sections)
	paywall, _ := json.Marshal(p.Paywall)
	slug := p.Slug
	for i := 2; ; i++ {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM publications WHERE workspace_id = $1 AND slug = $2`, db.ws, slug).Scan(&n); err != nil {
			return 0, err
		}
		if n == 0 {
			break
		}
		slug = fmt.Sprintf("%s-%d", p.Slug, i)
	}
	var id int64
	err := db.QueryRowContext(ctx, `INSERT INTO publications
		(workspace_id, slug, name, language, topic, audience, editorial_line, reject_terms, tone, frequency, sections_json, paywall_json, ai_disclosure, custom)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,TRUE)
		RETURNING id`,
		db.ws, slug, p.Name, p.Language, p.Topic, p.Audience, p.EditorialLine, p.RejectTerms,
		p.Tone, p.Frequency, string(sections), string(paywall), p.AIDisclosure).Scan(&id)
	return id, err
}

// DeleteSource removes one source from a publication. Collected items go
// with it — stories and past issues keep their own rows, only the raw
// fetch links are dropped.
func (db *DB) DeleteSource(ctx context.Context, pubID, sourceID int64) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM story_items WHERE workspace_id = $1 AND item_id IN (SELECT id FROM items WHERE workspace_id = $1 AND source_id = $2)`,
		`DELETE FROM items WHERE workspace_id = $1 AND source_id = $2`,
	} {
		if _, err := tx.ExecContext(ctx, q, db.ws, sourceID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM sources WHERE workspace_id = $1 AND id = $2 AND publication_id = $3`, db.ws, sourceID, pubID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// ItemsPerWeek counts items collected from a source in the last 7 days.
// Catalog-bound sources count the shared items their catalog row pulled.
func (db *DB) ItemsPerWeek(ctx context.Context, sourceID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM items i
		WHERE i.fetched_at > $3 AND (
			(i.workspace_id = $1 AND i.source_id = $2)
			OR i.catalog_id = (SELECT catalog_id FROM sources
			                   WHERE workspace_id = $1 AND id = $2 AND catalog_id IS NOT NULL))`,
		db.ws, sourceID, TSUTC(time.Now().Add(-7*24*time.Hour))).Scan(&n)
	return n, err
}

// SeedPublication inserts a publication definition and its sources.
// Idempotent on slug; returns the publication id.
func (db *DB) SeedPublication(ctx context.Context, p *config.Publication) (int64, error) {
	var id int64
	sections, _ := json.Marshal(p.Sections)
	paywall, _ := json.Marshal(p.Paywall)
	err := db.QueryRowContext(ctx,
		`SELECT id FROM publications WHERE workspace_id = $1 AND slug = $2`, db.ws, p.Slug).Scan(&id)
	if err == nil {
		// Existing workspace: refresh publication config from the current
		// template (paywall price, sections, etc.) while keeping the
		// visitor's issues, decisions, sources and readers.
		if _, err := db.ExecContext(ctx, `UPDATE publications SET
			name=$1, language=$2, topic=$3, audience=$4, editorial_line=$5, reject_terms=$6, tone=$7,
			frequency=$8, sections_json=$9, paywall_json=$10, ai_disclosure=$11
			WHERE workspace_id = $12 AND id = $13`,
			p.Name, p.Language, p.Topic, p.Audience, p.EditorialLine, p.RejectTerms, p.Tone,
			p.Frequency, string(sections), string(paywall), p.AIDisclosure, db.ws, id); err != nil {
			return 0, err
		}
		for _, s := range p.Sources {
			if _, err := db.ExecContext(ctx,
				`INSERT INTO sources (workspace_id, publication_id, type, url) VALUES ($1,$2,$3,$4)
				 ON CONFLICT (publication_id, url) DO NOTHING`,
				db.ws, id, s.Type, s.URL); err != nil {
				return 0, err
			}
		}
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	err = db.QueryRowContext(ctx, `INSERT INTO publications
		(workspace_id, slug, name, language, topic, audience, editorial_line, reject_terms, tone, frequency, sections_json, paywall_json, ai_disclosure)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id`,
		db.ws, p.Slug, p.Name, p.Language, p.Topic, p.Audience, p.EditorialLine, p.RejectTerms,
		p.Tone, p.Frequency, string(sections), string(paywall), p.AIDisclosure).Scan(&id)
	if err != nil {
		return 0, err
	}
	for _, s := range p.Sources {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO sources (workspace_id, publication_id, type, url) VALUES ($1,$2,$3,$4)
			 ON CONFLICT (publication_id, url) DO NOTHING`,
			db.ws, id, s.Type, s.URL); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// Source is a stored source row. CatalogID links the row to the global
// catalog pick it mirrors (0 for fixture/custom sources).
type Source struct {
	ID          int64
	Name        string
	Type        string
	URL         string
	CatalogID   int64
	Policy      string // catalog policy: launch | signal | hold | blocked
	LastFetchAt sql.NullString
	LastStatus  sql.NullString
}

const sourceCols = `s.id, s.name, s.type, s.url, s.catalog_id,
	COALESCE(c.policy, ''), s.last_fetch_at, s.last_status`

// Sources lists a publication's sources.
func (db *DB) Sources(ctx context.Context, pubID int64) ([]Source, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+sourceCols+` FROM sources s LEFT JOIN source_catalog c ON c.id = s.catalog_id
		 WHERE s.workspace_id = $1 AND s.publication_id = $2 ORDER BY s.id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var s Source
		var catalogID sql.NullInt64
		if err := rows.Scan(&s.ID, &s.Name, &s.Type, &s.URL, &catalogID, &s.Policy, &s.LastFetchAt, &s.LastStatus); err != nil {
			return nil, err
		}
		s.CatalogID = catalogID.Int64
		out = append(out, s)
	}
	return out, rows.Err()
}

// SourceNames maps source id to outlet name for a publication.
func (db *DB) SourceNames(ctx context.Context, pubID int64) (map[int64]string, error) {
	srcs, err := db.Sources(ctx, pubID)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, s := range srcs {
		if s.Name != "" {
			names[s.ID] = s.Name
		}
	}
	return names, nil
}

// AttachRuns links unattributed pipeline step rows (recorded before the
// issue row existed) to the issue that followed them.
func (db *DB) AttachRuns(ctx context.Context, pubID, issueID int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE runs SET issue_id = $1 WHERE workspace_id = $2 AND publication_id = $3 AND issue_id IS NULL`,
		issueID, db.ws, pubID)
	return err
}

// NextIssueNumber returns the next sequential issue number for a publication.
func (db *DB) NextIssueNumber(ctx context.Context, pubID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(number), 0) + 1 FROM issues WHERE workspace_id = $1 AND publication_id = $2`,
		db.ws, pubID).Scan(&n)
	return n, err
}

// CreateIssue inserts an issue and returns its id. sections snapshots
// the publication's section definitions for this edition — later
// builder edits must not rewrite an edition already in progress
// (KLOX-65); callers pass nil where no snapshot applies.
func (db *DB) CreateIssue(ctx context.Context, pubID int64, number int, subject, bodyMD string, sections []config.Section) (int64, error) {
	var id int64
	secs, _ := json.Marshal(sections)
	err := db.QueryRowContext(ctx,
		`INSERT INTO issues (workspace_id, publication_id, number, subject, body_md, sections_json) VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id`,
		db.ws, pubID, number, subject, bodyMD, string(secs)).Scan(&id)
	return id, err
}

// UpdatePublication rewrites the creator-editable fields of a
// publication: identity copy, cadence, the section definitions the
// next edition uses, and the paywall block. Slug, language and the
// custom flag stay put — they anchor public URLs and template drift.
func (db *DB) UpdatePublication(ctx context.Context, id int64, p *config.Publication) error {
	sections, _ := json.Marshal(p.Sections)
	paywall, _ := json.Marshal(p.Paywall)
	res, err := db.ExecContext(ctx, `UPDATE publications SET
		name=$1, topic=$2, audience=$3, editorial_line=$4, reject_terms=$5, tone=$6, frequency=$7,
		sections_json=$8, paywall_json=$9, ai_disclosure=$10
		WHERE workspace_id = $11 AND id = $12`,
		p.Name, p.Topic, p.Audience, p.EditorialLine, p.RejectTerms, p.Tone, p.Frequency,
		string(sections), string(paywall), p.AIDisclosure, db.ws, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetPublicationTemplate replaces the newsletter's default edition
// template (KLOX-72). want is the template_json the caller read — the
// update is a compare-and-swap on it, so a concurrent save fails with
// sql.ErrNoRows instead of silently clobbering (the API maps that to a
// 409 like the document version contract).
func (db *DB) SetPublicationTemplate(ctx context.Context, pubID int64, want string, blocks []TemplateBlock) error {
	data, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx,
		`UPDATE publications SET template_json = $3
		 WHERE workspace_id = $1 AND id = $2 AND template_json = $4`,
		db.ws, pubID, string(data), want)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeletePublication removes a publication and every row it owns:
// issues and their items/dossiers/decisions, runs, stories and raw
// items, sources, readers and subscriptions, guides and payment
// accounts. The send ledger (outbox_emails) is workspace-scoped by
// design and stays — it is the legal record of what went out. Returns
// sql.ErrNoRows when the publication does not exist in the workspace.
func (db *DB) DeletePublication(ctx context.Context, pubID int64) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	issuesOfPub := `SELECT id FROM issues WHERE workspace_id = $1 AND publication_id = $2`
	itemsOfIssues := `SELECT id FROM issue_items WHERE workspace_id = $1 AND issue_id IN (` + issuesOfPub + `)`
	for _, q := range []string{
		`DELETE FROM decisions WHERE workspace_id = $1 AND (publication_id = $2 OR issue_item_id IN (` + itemsOfIssues + `))`,
		`DELETE FROM dossiers WHERE workspace_id = $1 AND issue_item_id IN (` + itemsOfIssues + `)`,
		`DELETE FROM issue_items WHERE workspace_id = $1 AND issue_id IN (` + issuesOfPub + `)`,
		`DELETE FROM jobs WHERE workspace_id = $1 AND kind = 'send_issue' AND ref_id IN (` + issuesOfPub + `)`,
		`DELETE FROM runs WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM renditions WHERE workspace_id = $1 AND issue_id IN (`+issuesOfPub+`)`,
		`DELETE FROM issue_exports WHERE workspace_id = $1 AND issue_id IN (`+issuesOfPub+`)`,
		`DELETE FROM issues WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM story_items WHERE workspace_id = $1 AND (story_id IN (SELECT id FROM stories WHERE workspace_id = $1 AND publication_id = $2)
			OR item_id IN (SELECT id FROM items WHERE workspace_id = $1 AND source_id IN (SELECT id FROM sources WHERE workspace_id = $1 AND publication_id = $2)))`,
		`DELETE FROM stories WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM items WHERE workspace_id = $1 AND source_id IN (SELECT id FROM sources WHERE workspace_id = $1 AND publication_id = $2)`,
		`DELETE FROM sources WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM subscriptions WHERE workspace_id = $1 AND reader_id IN (SELECT id FROM readers WHERE workspace_id = $1 AND publication_id = $2)`,
		`DELETE FROM readers WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM editorial_guides WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM media WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM standard_messages WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM payment_accounts WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM pauta_days WHERE workspace_id = $1 AND publication_id = $2`,
	} {
		if _, err := tx.ExecContext(ctx, q, db.ws, pubID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM publications WHERE workspace_id = $1 AND id = $2`, db.ws, pubID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// IssueBody returns the stored markdown body of an issue.
func (db *DB) IssueBody(ctx context.Context, pubID int64, number int) (string, error) {
	var body string
	err := db.QueryRowContext(ctx,
		`SELECT body_md FROM issues WHERE workspace_id = $1 AND publication_id = $2 AND number = $3`,
		db.ws, pubID, number).Scan(&body)
	return body, err
}

// Run records cost/duration for one pipeline step.
type Run struct {
	PublicationID int64
	IssueID       int64
	Step          string
	Model         string
	InputTokens   int
	OutputTokens  int
	CostUSD       float64
	DurationMS    int64
}

// RecordRun stores a pipeline step measurement. IssueID 0 means the step
// ran before an issue existed and is stored as NULL.
func (db *DB) RecordRun(ctx context.Context, r Run) error {
	var issueID any
	if r.IssueID != 0 {
		issueID = r.IssueID
	}
	_, err := db.ExecContext(ctx, `INSERT INTO runs
		(workspace_id, publication_id, issue_id, step, model, input_tokens, output_tokens, cost_usd, duration_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		db.ws, r.PublicationID, issueID, r.Step, r.Model, r.InputTokens, r.OutputTokens, r.CostUSD, r.DurationMS)
	return err
}

// CostRow is one row of the per-issue cost report.
type CostRow struct {
	IssueNumber  int
	Step         string
	Model        string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// CostReport returns run rows for the last n issues of a publication,
// plus unattributed (pre-issue) steps shown as issue 0.
func (db *DB) CostReport(ctx context.Context, pubID int64, lastN int) ([]CostRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(i.number, 0), r.step, r.model, r.input_tokens, r.output_tokens, r.cost_usd
		FROM runs r LEFT JOIN issues i ON i.id = r.issue_id
		WHERE r.workspace_id = $1 AND r.publication_id = $2
		  AND (i.number IN (SELECT number FROM issues WHERE workspace_id = $1 AND publication_id = $2 ORDER BY number DESC LIMIT $3)
		       OR r.issue_id IS NULL)
		ORDER BY i.number DESC, r.id`, db.ws, pubID, lastN)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CostRow
	for rows.Next() {
		var c CostRow
		if err := rows.Scan(&c.IssueNumber, &c.Step, &c.Model, &c.InputTokens, &c.OutputTokens, &c.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// IssueUsedAI reports whether any non-deterministic model step ran for
// the issue — the signal the AI disclosure keys on (KLOX-56). A fully
// deterministic or manual-compose issue must not carry the disclosure.
func (db *DB) IssueUsedAI(ctx context.Context, issueID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM runs WHERE workspace_id = $1 AND issue_id = $2
		   AND model <> '' AND model <> 'deterministic'`,
		db.ws, issueID).Scan(&n)
	return n > 0, err
}

// IssueCostUSD returns the recorded model cost of the run steps
// attached to an issue — the number the issue page shows (KLOX-44).
func (db *DB) IssueCostUSD(ctx context.Context, issueID int64) (float64, error) {
	var v float64
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(sum(cost_usd), 0) FROM runs WHERE workspace_id = $1 AND issue_id = $2`,
		db.ws, issueID).Scan(&v)
	return v, err
}

// LatestGuide returns the newest editorial guide for a publication.
// Returns sql.ErrNoRows when none exists yet.
func (db *DB) LatestGuide(ctx context.Context, pubID int64) (version int, content string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT version, content FROM editorial_guides WHERE workspace_id = $1 AND publication_id = $2 ORDER BY version DESC LIMIT 1`,
		db.ws, pubID).Scan(&version, &content)
	return version, content, err
}

// LatestGuides returns up to n newest guide versions, newest first.
func (db *DB) LatestGuides(ctx context.Context, pubID int64, n int) ([]struct {
	Version int
	Content string
}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT version, content FROM editorial_guides WHERE workspace_id = $1 AND publication_id = $2 ORDER BY version DESC LIMIT $3`,
		db.ws, pubID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		Version int
		Content string
	}
	for rows.Next() {
		var g struct {
			Version int
			Content string
		}
		if err := rows.Scan(&g.Version, &g.Content); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
