package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// Issue is a stored issue row.
type Issue struct {
	ID            int64
	PublicationID int64
	Number        int
	Status        string
	Subject       string
	BodyMD        string
	Flags         []Flag
	Commentary    map[string]string
	CreatedAt     string
}

// Flag is a citation-check flag stored in issues.flags_json.
type Flag struct {
	Item     int64  `json:"item"`
	Sentence string `json:"sentence"`
	Note     string `json:"note"`
}

func (db *DB) scanIssue(row *sql.Row) (*Issue, error) {
	var i Issue
	var flags, commentary string
	err := row.Scan(&i.ID, &i.PublicationID, &i.Number, &i.Status, &i.Subject,
		&i.BodyMD, &flags, &commentary, &i.CreatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(flags), &i.Flags)
	json.Unmarshal([]byte(commentary), &i.Commentary)
	return &i, nil
}

// GetIssue loads an issue by id.
func (db *DB) GetIssue(ctx context.Context, id int64) (*Issue, error) {
	return db.scanIssue(db.QueryRowContext(ctx,
		`SELECT id, publication_id, number, status, subject, body_md, flags_json, commentary_json, created_at
		 FROM issues WHERE id = ?`, id))
}

// LatestIssue returns a publication's newest issue (sql.ErrNoRows if none).
func (db *DB) LatestIssue(ctx context.Context, pubID int64) (*Issue, error) {
	return db.scanIssue(db.QueryRowContext(ctx,
		`SELECT id, publication_id, number, status, subject, body_md, flags_json, commentary_json, created_at
		 FROM issues WHERE publication_id = ? ORDER BY number DESC LIMIT 1`, pubID))
}

// SetIssueItemStatus updates an issue item's review status.
func (db *DB) SetIssueItemStatus(ctx context.Context, id int64, status string) error {
	_, err := db.ExecContext(ctx, `UPDATE issue_items SET status = ? WHERE id = ?`, status, id)
	return err
}

// SetIssueStatus updates an issue's status.
func (db *DB) SetIssueStatus(ctx context.Context, id int64, status string) error {
	_, err := db.ExecContext(ctx, `UPDATE issues SET status = ? WHERE id = ?`, status, id)
	return err
}

// StoryTitle returns a story's title (empty if the story is gone).
func (db *DB) StoryTitle(ctx context.Context, storyID int64) (string, error) {
	var t string
	err := db.QueryRowContext(ctx, `SELECT title FROM stories WHERE id = ?`, storyID).Scan(&t)
	return t, err
}

// ResolveFlags replaces an issue's flag list (e.g. after dismissing one).
func (db *DB) ResolveFlags(ctx context.Context, issueID int64, flags []Flag) error {
	data, err := json.Marshal(flags)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE issues SET flags_json = ? WHERE id = ?`, string(data), issueID)
	return err
}

// GetIssueItem loads one issue item.
func (db *DB) GetIssueItem(ctx context.Context, id int64) (*IssueItem, error) {
	var ii IssueItem
	err := db.QueryRowContext(ctx,
		`SELECT id, issue_id, story_id, section, score, reason, status FROM issue_items WHERE id = ?`, id).
		Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status)
	if err != nil {
		return nil, err
	}
	return &ii, nil
}

// SaveCommentary replaces an issue's commentary map.
func (db *DB) SaveCommentary(ctx context.Context, issueID int64, commentary map[string]string) error {
	data, err := json.Marshal(commentary)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE issues SET commentary_json = ? WHERE id = ?`, string(data), issueID)
	return err
}

// AddSource registers a new source on a publication.
func (db *DB) AddSource(ctx context.Context, pubID int64, typ, url string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO sources (publication_id, type, url) VALUES (?,?,?)`, pubID, typ, url)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetSourceMonitor records monitor provider state on a source.
func (db *DB) SetSourceMonitor(ctx context.Context, sourceID int64, provider, monitorID, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sources SET monitor_provider = ?, monitor_id = ?, monitor_status = ? WHERE id = ?`,
		provider, monitorID, status, sourceID)
	return err
}

// SourceMonitor returns monitor fields for a source.
func (db *DB) SourceMonitor(ctx context.Context, sourceID int64) (provider, monitorID, status string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT monitor_provider, monitor_id, monitor_status FROM sources WHERE id = ?`, sourceID).
		Scan(&provider, &monitorID, &status)
	return
}

// FindSourceByMonitor resolves a source from a provider monitor id.
func (db *DB) FindSourceByMonitor(ctx context.Context, monitorID string) (*Source, error) {
	var s Source
	err := db.QueryRowContext(ctx,
		`SELECT id, type, url, last_fetch_at, last_status FROM sources WHERE monitor_id = ?`, monitorID).
		Scan(&s.ID, &s.Type, &s.URL, &s.LastFetchAt, &s.LastStatus)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// UpsertReader returns the id of a reader for (publication, email).
func (db *DB) UpsertReader(ctx context.Context, pubID int64, email string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM readers WHERE publication_id = ? AND email = ?`, pubID, email).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO readers (publication_id, email) VALUES (?,?)`, pubID, email)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ActivateSubscription marks the reader's subscription active.
func (db *DB) ActivateSubscription(ctx context.Context, readerID int64, provider, ref string) error {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM subscriptions WHERE reader_id = ?`, readerID).Scan(&id)
	if err == sql.ErrNoRows {
		_, err = db.ExecContext(ctx,
			`INSERT INTO subscriptions (reader_id, status, provider, provider_ref) VALUES (?,?,?,?)`,
			readerID, "active", provider, ref)
		return err
	}
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE subscriptions SET status = 'active', provider = ?, provider_ref = ? WHERE reader_id = ?`,
		provider, ref, readerID)
	return err
}

// HasActiveSubscription reports whether the reader has an active sub.
func (db *DB) HasActiveSubscription(ctx context.Context, readerID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM subscriptions WHERE reader_id = ? AND status = 'active'`, readerID).Scan(&n)
	return n > 0, err
}

// Reset wipes all demo data (used by "Reset demo").
func (db *DB) Reset(ctx context.Context) error {
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer db.Exec(`PRAGMA foreign_keys = ON`)
	tables := []string{
		"story_items", "issue_items", "dossiers", "decisions", "runs", "jobs",
		"subscriptions", "readers", "payment_accounts", "editorial_guides",
		"items", "stories", "issues", "sources", "publications",
	}
	for _, t := range tables {
		if _, err := db.ExecContext(ctx, `DELETE FROM `+t); err != nil {
			return err
		}
	}
	// Reset AUTOINCREMENT counters so a reseeded publication keeps id 1 and
	// deep links (e.g. /pub/1/...) stay valid across resets.
	if _, err := db.ExecContext(ctx, `DELETE FROM sqlite_sequence`); err != nil &&
		!strings.Contains(err.Error(), "no such table") {
		return err
	}
	return nil
}
