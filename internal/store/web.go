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

// ListIssues returns a publication's issues, newest first.
func (db *DB) ListIssues(ctx context.Context, pubID int64) ([]*Issue, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, publication_id, number, status, subject, body_md, flags_json, commentary_json, created_at
		 FROM issues WHERE publication_id = ? ORDER BY number DESC`, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Issue
	for rows.Next() {
		var i Issue
		var flags, commentary string
		if err := rows.Scan(&i.ID, &i.PublicationID, &i.Number, &i.Status,
			&i.Subject, &i.BodyMD, &flags, &commentary, &i.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(flags), &i.Flags)
		json.Unmarshal([]byte(commentary), &i.Commentary)
		out = append(out, &i)
	}
	return out, rows.Err()
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

// RevertIssueToDraft returns an approved issue to drafted and drops its
// approval receipt — post-approval edits invalidate the approval.
func (db *DB) RevertIssueToDraft(ctx context.Context, id int64) error {
	if err := db.SetIssueStatus(ctx, id, "drafted"); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `DELETE FROM approval_receipts WHERE issue_id = ?`, id)
	return err
}

// ApprovalReceipt is the stored record of an issue approval.
type ApprovalReceipt struct {
	IssueID         int64
	Number          int
	DocumentVersion string
	Approver        string
	ChecksJSON      string
	ApprovedAt      string
}

// SaveApprovalReceipt records the approval of an issue. The first
// receipt wins: a repeated approve keeps the original (INSERT OR
// IGNORE), so the returned row carries one stable timestamp.
func (db *DB) SaveApprovalReceipt(ctx context.Context, rec *ApprovalReceipt) error {
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO approval_receipts (issue_id, number, document_version, approver, checks_json)
		 VALUES (?,?,?,?,?)`,
		rec.IssueID, rec.Number, rec.DocumentVersion, rec.Approver, rec.ChecksJSON)
	return err
}

// ApprovalReceipt returns the stored receipt (sql.ErrNoRows if none).
func (db *DB) ApprovalReceipt(ctx context.Context, issueID int64) (*ApprovalReceipt, error) {
	var r ApprovalReceipt
	err := db.QueryRowContext(ctx,
		`SELECT issue_id, number, document_version, approver, checks_json, approved_at
		 FROM approval_receipts WHERE issue_id = ?`, issueID).
		Scan(&r.IssueID, &r.Number, &r.DocumentVersion, &r.Approver, &r.ChecksJSON, &r.ApprovedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
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
		`SELECT id, issue_id, story_id, section, score, reason, status, origin FROM issue_items WHERE id = ?`, id).
		Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin)
	if err != nil {
		return nil, err
	}
	return &ii, nil
}

// LatestFinishedIssue returns the newest approved or sent issue — what a
// reader is allowed to see (sql.ErrNoRows when none).
func (db *DB) LatestFinishedIssue(ctx context.Context, pubID int64) (*Issue, error) {
	return db.scanIssue(db.QueryRowContext(ctx,
		`SELECT id, publication_id, number, status, subject, body_md, flags_json, commentary_json, created_at
		 FROM issues WHERE publication_id = ? AND status IN ('approved','sent')
		 ORDER BY number DESC LIMIT 1`, pubID))
}

// SetIssueBody replaces an issue's stored markdown body (sentence edits).
func (db *DB) SetIssueBody(ctx context.Context, id int64, bodyMD string) error {
	_, err := db.ExecContext(ctx, `UPDATE issues SET body_md = ? WHERE id = ?`, bodyMD, id)
	return err
}

// IssueItemByStory returns the issue item for (issue, story) if any.
func (db *DB) IssueItemByStory(ctx context.Context, issueID, storyID int64) (*IssueItem, error) {
	var ii IssueItem
	err := db.QueryRowContext(ctx,
		`SELECT id, issue_id, story_id, section, score, reason, status, origin FROM issue_items
		 WHERE issue_id = ? AND story_id = ?`, issueID, storyID).
		Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin)
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

// UpsertReader returns the id of a reader for (publication, email),
// storing a display name on first insert.
func (db *DB) UpsertReader(ctx context.Context, pubID int64, email, name string) (int64, error) {
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
		`INSERT INTO readers (publication_id, email, name) VALUES (?,?,?)`, pubID, email, name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ActivateSubscription marks the reader's subscription active and stores
// the magic-link token that unlocks the reader view.
func (db *DB) ActivateSubscription(ctx context.Context, readerID int64, provider, ref, magicToken string) error {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM subscriptions WHERE reader_id = ?`, readerID).Scan(&id)
	if err == sql.ErrNoRows {
		_, err = db.ExecContext(ctx,
			`INSERT INTO subscriptions (reader_id, status, provider, provider_ref, magic_token) VALUES (?,?,?,?,?)`,
			readerID, "active", provider, ref, magicToken)
		return err
	}
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE subscriptions SET status = 'active', provider = ?, provider_ref = ?, magic_token = ? WHERE reader_id = ?`,
		provider, ref, magicToken, readerID)
	return err
}

// CancelSubscription marks the reader's subscription canceled.
func (db *DB) CancelSubscription(ctx context.Context, readerID int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE subscriptions SET status = 'canceled' WHERE reader_id = ? AND status = 'active'`, readerID)
	return err
}

// UnsubscribeReader records an email unsubscribe: the subscription is
// canceled and the reader row is stamped so sends can honor the opt-out.
func (db *DB) UnsubscribeReader(ctx context.Context, readerID int64) error {
	if _, err := db.ExecContext(ctx,
		`UPDATE readers SET unsubscribed_at = datetime('now') WHERE id = ?`, readerID); err != nil {
		return err
	}
	return db.CancelSubscription(ctx, readerID)
}

// ReaderUnsubscribed reports whether the reader opted out of emails.
func (db *DB) ReaderUnsubscribed(ctx context.Context, readerID int64) bool {
	var ts string
	err := db.QueryRowContext(ctx,
		`SELECT unsubscribed_at FROM readers WHERE id = ?`, readerID).Scan(&ts)
	return err == nil && ts != ""
}

// HasActiveSubscription reports whether the reader has an active sub.
func (db *DB) HasActiveSubscription(ctx context.Context, readerID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM subscriptions WHERE reader_id = ? AND status = 'active'`, readerID).Scan(&n)
	return n > 0, err
}

// ReaderByMagicToken resolves a magic-link token to a reader id within a
// publication (sql.ErrNoRows when invalid).
func (db *DB) ReaderByMagicToken(ctx context.Context, pubID int64, token string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		SELECT r.id FROM readers r
		JOIN subscriptions s ON s.reader_id = r.id
		WHERE r.publication_id = ? AND s.magic_token = ? AND s.magic_token != '' AND s.status = 'active'`,
		pubID, token).Scan(&id)
	return id, err
}

// ReaderByToken resolves a magic-link token regardless of subscription
// status — used to locate the workspace a reader link belongs to, even
// after the reader unsubscribed (sql.ErrNoRows when invalid).
func (db *DB) ReaderByToken(ctx context.Context, pubID int64, token string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		SELECT r.id FROM readers r
		JOIN subscriptions s ON s.reader_id = r.id
		WHERE r.publication_id = ? AND s.magic_token = ? AND s.magic_token != ''`,
		pubID, token).Scan(&id)
	return id, err
}

// OutboxEmail is one simulated outgoing message stored per workspace.
type OutboxEmail struct {
	ID        int64
	Kind      string
	To        string
	Subject   string
	BodyMD    string
	BodyHTML  string
	CreatedAt string
}

// SaveOutboxEmail records a simulated email in the workspace outbox.
func (db *DB) SaveOutboxEmail(ctx context.Context, kind, to, subject, bodyMD, bodyHTML string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO outbox_emails (kind, to_addr, subject, body_md, body_html) VALUES (?,?,?,?,?)`,
		kind, to, subject, bodyMD, bodyHTML)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// OutboxEmails lists recorded emails, newest first.
func (db *DB) OutboxEmails(ctx context.Context) ([]OutboxEmail, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, kind, to_addr, subject, created_at FROM outbox_emails ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxEmail
	for rows.Next() {
		var e OutboxEmail
		if err := rows.Scan(&e.ID, &e.Kind, &e.To, &e.Subject, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetOutboxEmail loads one email including bodies.
func (db *DB) GetOutboxEmail(ctx context.Context, id int64) (*OutboxEmail, error) {
	var e OutboxEmail
	err := db.QueryRowContext(ctx,
		`SELECT id, kind, to_addr, subject, body_md, body_html, created_at FROM outbox_emails WHERE id = ?`, id).
		Scan(&e.ID, &e.Kind, &e.To, &e.Subject, &e.BodyMD, &e.BodyHTML, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// Progress returns a demo-progress flag ("" when unset).
func (db *DB) Progress(ctx context.Context, key string) string {
	var v string
	if err := db.QueryRowContext(ctx,
		`SELECT value FROM demo_progress WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// SetProgress marks a demo-progress flag.
func (db *DB) SetProgress(ctx context.Context, key string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO demo_progress (key, value) VALUES (?, '1')
		 ON CONFLICT(key) DO NOTHING`, key)
	return err
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
		"outbox_emails", "demo_progress",
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
