// Package store owns SQLite access: connection, embedded migrations,
// and repositories.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/migrations"
)

// DB wraps *sql.DB with Kloxus repositories.
type DB struct{ *sql.DB }

// Open opens (creating if needed) the database at path and applies migrations.
// WAL mode plus a busy timeout keep reads responsive while the pipeline
// writes in the background (the run page is polled during a run).
func Open(path string) (*DB, error) {
	sdb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(1)
	db := &DB{sdb}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 10000; PRAGMA foreign_keys = ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting pragmas: %w", err)
	}
	if err := db.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) migrate() error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
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
		if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version = ?`, name).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
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
	Tone          string
	Frequency     string
	Sections      []config.Section
	Paywall       config.Paywall
	AIDisclosure  string
}

func (db *DB) scanPublication(row *sql.Row) (*Publication, error) {
	var p Publication
	var sections, paywall string
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Language, &p.Topic, &p.Audience,
		&p.EditorialLine, &p.Tone, &p.Frequency, &sections, &paywall, &p.AIDisclosure)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(sections), &p.Sections); err != nil {
		return nil, fmt.Errorf("decoding sections for %q: %w", p.Slug, err)
	}
	if err := json.Unmarshal([]byte(paywall), &p.Paywall); err != nil {
		return nil, fmt.Errorf("decoding paywall for %q: %w", p.Slug, err)
	}
	return &p, nil
}

const publicationCols = `id, slug, name, language, topic, audience, editorial_line, tone, frequency, sections_json, paywall_json, ai_disclosure`

// GetPublication resolves a publication by numeric id or slug.
func (db *DB) GetPublication(ctx context.Context, ref string) (*Publication, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return db.scanPublication(db.QueryRowContext(ctx,
			`SELECT `+publicationCols+` FROM publications WHERE id = ?`, id))
	}
	return db.scanPublication(db.QueryRowContext(ctx,
		`SELECT `+publicationCols+` FROM publications WHERE slug = ?`, ref))
}

// ListPublications returns all publications ordered by name.
func (db *DB) ListPublications(ctx context.Context) ([]*Publication, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+publicationCols+` FROM publications ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Publication
	for rows.Next() {
		var p Publication
		var sections, paywall string
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Language, &p.Topic, &p.Audience,
			&p.EditorialLine, &p.Tone, &p.Frequency, &sections, &paywall, &p.AIDisclosure); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(sections), &p.Sections)
		json.Unmarshal([]byte(paywall), &p.Paywall)
		out = append(out, &p)
	}
	return out, rows.Err()
}

// SeedPublication inserts a publication definition and its sources.
// Idempotent on slug; returns the publication id.
func (db *DB) SeedPublication(ctx context.Context, p *config.Publication) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM publications WHERE slug = ?`, p.Slug).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	sections, _ := json.Marshal(p.Sections)
	paywall, _ := json.Marshal(p.Paywall)
	res, err := db.ExecContext(ctx, `INSERT INTO publications
		(slug, name, language, topic, audience, editorial_line, tone, frequency, sections_json, paywall_json, ai_disclosure)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.Slug, p.Name, p.Language, p.Topic, p.Audience, p.EditorialLine,
		p.Tone, p.Frequency, string(sections), string(paywall), p.AIDisclosure)
	if err != nil {
		return 0, err
	}
	if id, err = res.LastInsertId(); err != nil {
		return 0, err
	}
	for _, s := range p.Sources {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO sources (publication_id, type, url) VALUES (?,?,?)`,
			id, s.Type, s.URL); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// Source is a stored source row.
type Source struct {
	ID          int64
	Name        string
	Type        string
	URL         string
	LastFetchAt sql.NullString
	LastStatus  sql.NullString
}

const sourceCols = `id, name, type, url, last_fetch_at, last_status`

// Sources lists a publication's sources.
func (db *DB) Sources(ctx context.Context, pubID int64) ([]Source, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+sourceCols+` FROM sources WHERE publication_id = ? ORDER BY id`, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var s Source
		if err := rows.Scan(&s.ID, &s.Name, &s.Type, &s.URL, &s.LastFetchAt, &s.LastStatus); err != nil {
			return nil, err
		}
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
		`UPDATE runs SET issue_id = ? WHERE publication_id = ? AND issue_id IS NULL`,
		issueID, pubID)
	return err
}

// NextIssueNumber returns the next sequential issue number for a publication.
func (db *DB) NextIssueNumber(ctx context.Context, pubID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(number), 0) + 1 FROM issues WHERE publication_id = ?`, pubID).Scan(&n)
	return n, err
}

// CreateIssue inserts an issue and returns its id.
func (db *DB) CreateIssue(ctx context.Context, pubID int64, number int, subject, bodyMD string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO issues (publication_id, number, subject, body_md) VALUES (?,?,?,?)`,
		pubID, number, subject, bodyMD)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// IssueBody returns the stored markdown body of an issue.
func (db *DB) IssueBody(ctx context.Context, pubID int64, number int) (string, error) {
	var body string
	err := db.QueryRowContext(ctx,
		`SELECT body_md FROM issues WHERE publication_id = ? AND number = ?`, pubID, number).Scan(&body)
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
		(publication_id, issue_id, step, model, input_tokens, output_tokens, cost_usd, duration_ms)
		VALUES (?,?,?,?,?,?,?,?)`,
		r.PublicationID, issueID, r.Step, r.Model, r.InputTokens, r.OutputTokens, r.CostUSD, r.DurationMS)
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
		WHERE r.publication_id = ?
		  AND (i.number IN (SELECT number FROM issues WHERE publication_id = ? ORDER BY number DESC LIMIT ?)
		       OR r.issue_id IS NULL)
		ORDER BY i.number DESC, r.id`, pubID, pubID, lastN)
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

// LatestGuide returns the newest editorial guide for a publication.
// Returns sql.ErrNoRows when none exists yet.
func (db *DB) LatestGuide(ctx context.Context, pubID int64) (version int, content string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT version, content FROM editorial_guides WHERE publication_id = ? ORDER BY version DESC LIMIT 1`,
		pubID).Scan(&version, &content)
	return version, content, err
}

// LatestGuides returns up to n newest guide versions, newest first.
func (db *DB) LatestGuides(ctx context.Context, pubID int64, n int) ([]struct {
	Version int
	Content string
}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT version, content FROM editorial_guides WHERE publication_id = ? ORDER BY version DESC LIMIT ?`,
		pubID, n)
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
