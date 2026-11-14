package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
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
	ScheduledAt   string
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
		&i.BodyMD, &flags, &commentary, &i.CreatedAt, &i.ScheduledAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(flags), &i.Flags)
	json.Unmarshal([]byte(commentary), &i.Commentary)
	return &i, nil
}

const issueCols = `id, publication_id, number, status, subject, body_md, flags_json, commentary_json, created_at, scheduled_at`

// GetIssue loads an issue by id.
func (db *DB) GetIssue(ctx context.Context, id int64) (*Issue, error) {
	return db.scanIssue(db.QueryRowContext(ctx,
		`SELECT `+issueCols+` FROM issues WHERE workspace_id = $1 AND id = $2`, db.ws, id))
}

// ListIssues returns a publication's issues, newest first.
func (db *DB) ListIssues(ctx context.Context, pubID int64) ([]*Issue, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+issueCols+` FROM issues WHERE workspace_id = $1 AND publication_id = $2 ORDER BY number DESC`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Issue
	for rows.Next() {
		var i Issue
		var flags, commentary string
		if err := rows.Scan(&i.ID, &i.PublicationID, &i.Number, &i.Status,
			&i.Subject, &i.BodyMD, &flags, &commentary, &i.CreatedAt, &i.ScheduledAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(flags), &i.Flags)
		json.Unmarshal([]byte(commentary), &i.Commentary)
		out = append(out, &i)
	}
	return out, rows.Err()
}

// IssueByNumber loads one issue by publication and issue number — the
// pair a public issue URL like /p/{slug}/{num} addresses.
func (db *DB) IssueByNumber(ctx context.Context, pubID int64, number int) (*Issue, error) {
	return db.scanIssue(db.QueryRowContext(ctx,
		`SELECT `+issueCols+` FROM issues WHERE workspace_id = $1 AND publication_id = $2 AND number = $3`,
		db.ws, pubID, number))
}

// LatestIssue returns a publication's newest issue (sql.ErrNoRows if none).
func (db *DB) LatestIssue(ctx context.Context, pubID int64) (*Issue, error) {
	return db.scanIssue(db.QueryRowContext(ctx,
		`SELECT `+issueCols+` FROM issues WHERE workspace_id = $1 AND publication_id = $2 ORDER BY number DESC LIMIT 1`,
		db.ws, pubID))
}

// SetIssueItemStatus updates an issue item's review status.
func (db *DB) SetIssueItemStatus(ctx context.Context, id int64, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE issue_items SET status = $1 WHERE workspace_id = $2 AND id = $3`, status, db.ws, id)
	return err
}

// SetIssueStatus updates an issue's status. Moving to any status other
// than "scheduled" clears the queued send time.
func (db *DB) SetIssueStatus(ctx context.Context, id int64, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE issues SET status = $1, scheduled_at = CASE WHEN $2 = 'scheduled' THEN scheduled_at ELSE '' END
		 WHERE workspace_id = $3 AND id = $4`, status, status, db.ws, id)
	return err
}

// ScheduleIssue locks an issue for a future send and queues the
// dispatch job that delivers it at that time.
func (db *DB) ScheduleIssue(ctx context.Context, id int64, at string) error {
	if _, err := db.ExecContext(ctx,
		`UPDATE issues SET status = 'scheduled', scheduled_at = $1 WHERE workspace_id = $2 AND id = $3`,
		at, db.ws, id); err != nil {
		return err
	}
	t, err := parseScheduledAt(at)
	if err != nil {
		return err
	}
	if err := db.CancelJobs(ctx, JobKindSendIssue, id); err != nil {
		return err
	}
	_, err = db.EnqueueJob(ctx, JobKindSendIssue, id, map[string]any{"issue_id": id}, t)
	return err
}

// parseScheduledAt accepts the RFC3339 the API stores (and, for
// robustness, the plain timestamp shape).
func parseScheduledAt(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.ParseInLocation(tsLayout, s, time.UTC)
}

// RevertIssueToDraft returns an approved issue to drafted and drops its
// approval receipt — post-approval edits invalidate the approval. Any
// queued send goes with it.
func (db *DB) RevertIssueToDraft(ctx context.Context, id int64) error {
	if err := db.SetIssueStatus(ctx, id, "drafted"); err != nil {
		return err
	}
	if err := db.CancelJobs(ctx, JobKindSendIssue, id); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx,
		`DELETE FROM approval_receipts WHERE workspace_id = $1 AND issue_id = $2`, db.ws, id)
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
// receipt wins: a repeated approve keeps the original (ON CONFLICT DO
// NOTHING), so the returned row carries one stable timestamp.
func (db *DB) SaveApprovalReceipt(ctx context.Context, rec *ApprovalReceipt) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO approval_receipts (workspace_id, issue_id, number, document_version, approver, checks_json)
		 VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (issue_id) DO NOTHING`,
		db.ws, rec.IssueID, rec.Number, rec.DocumentVersion, rec.Approver, rec.ChecksJSON)
	return err
}

// ApprovalReceipt returns the stored receipt (sql.ErrNoRows if none).
func (db *DB) ApprovalReceipt(ctx context.Context, issueID int64) (*ApprovalReceipt, error) {
	var r ApprovalReceipt
	err := db.QueryRowContext(ctx,
		`SELECT issue_id, number, document_version, approver, checks_json, approved_at
		 FROM approval_receipts WHERE workspace_id = $1 AND issue_id = $2`, db.ws, issueID).
		Scan(&r.IssueID, &r.Number, &r.DocumentVersion, &r.Approver, &r.ChecksJSON, &r.ApprovedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// StoryTitle returns a story's title (empty if the story is gone).
func (db *DB) StoryTitle(ctx context.Context, storyID int64) (string, error) {
	var t string
	err := db.QueryRowContext(ctx,
		`SELECT title FROM stories WHERE workspace_id = $1 AND id = $2`, db.ws, storyID).Scan(&t)
	return t, err
}

// ResolveFlags replaces an issue's flag list (e.g. after dismissing one).
func (db *DB) ResolveFlags(ctx context.Context, issueID int64, flags []Flag) error {
	data, err := json.Marshal(flags)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE issues SET flags_json = $1 WHERE workspace_id = $2 AND id = $3`, string(data), db.ws, issueID)
	return err
}

// GetIssueItem loads one issue item.
func (db *DB) GetIssueItem(ctx context.Context, id int64) (*IssueItem, error) {
	var ii IssueItem
	err := db.QueryRowContext(ctx,
		`SELECT id, issue_id, story_id, section, score, reason, status, origin, position, orig_section
		 FROM issue_items WHERE workspace_id = $1 AND id = $2`, db.ws, id).
		Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin, &ii.Position, &ii.OrigSection)
	if err != nil {
		return nil, err
	}
	return &ii, nil
}

// LatestPublicIssue returns the newest sent or scheduled issue — what a
// public issue URL may resolve (sql.ErrNoRows when none is out yet).
func (db *DB) LatestPublicIssue(ctx context.Context, pubID int64) (*Issue, error) {
	return db.scanIssue(db.QueryRowContext(ctx,
		`SELECT `+issueCols+` FROM issues WHERE workspace_id = $1 AND publication_id = $2 AND status IN ('sent','scheduled')
		 ORDER BY number DESC LIMIT 1`, db.ws, pubID))
}

// IssueRejectionReasons returns the rejection reasons still standing on
// an issue's items — the rules the editorial guide learned from it. An
// undone rejection (whose latest decision is no longer "reject") drops
// out, matching how Rebuild condenses the guide.
func (db *DB) IssueRejectionReasons(ctx context.Context, issueID int64) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ii.id FROM issue_items ii WHERE ii.workspace_id = $1 AND ii.issue_id = $2`, db.ws, issueID)
	if err != nil {
		return nil, err
	}
	var itemIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		itemIDs = append(itemIDs, id)
	}
	rows.Close()
	var out []string
	for _, id := range itemIDs {
		kind, reason, err := db.LatestDecision(ctx, id)
		if err == nil && kind == "reject" && strings.TrimSpace(reason) != "" {
			out = append(out, reason)
		}
	}
	return out, nil
}

// LatestFinishedIssue returns the newest approved or sent issue — what a
// reader is allowed to see (sql.ErrNoRows when none).
func (db *DB) LatestFinishedIssue(ctx context.Context, pubID int64) (*Issue, error) {
	return db.scanIssue(db.QueryRowContext(ctx,
		`SELECT `+issueCols+` FROM issues WHERE workspace_id = $1 AND publication_id = $2 AND status IN ('approved','sent')
		 ORDER BY number DESC LIMIT 1`, db.ws, pubID))
}

// SetIssueBody replaces an issue's stored markdown body (sentence edits).
func (db *DB) SetIssueBody(ctx context.Context, id int64, bodyMD string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE issues SET body_md = $1 WHERE workspace_id = $2 AND id = $3`, bodyMD, db.ws, id)
	return err
}

// IssueItemByStory returns the issue item for (issue, story) if any.
func (db *DB) IssueItemByStory(ctx context.Context, issueID, storyID int64) (*IssueItem, error) {
	var ii IssueItem
	err := db.QueryRowContext(ctx,
		`SELECT id, issue_id, story_id, section, score, reason, status, origin, position, orig_section
		 FROM issue_items WHERE workspace_id = $1 AND issue_id = $2 AND story_id = $3`, db.ws, issueID, storyID).
		Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin, &ii.Position, &ii.OrigSection)
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
	_, err = db.ExecContext(ctx,
		`UPDATE issues SET commentary_json = $1 WHERE workspace_id = $2 AND id = $3`, string(data), db.ws, issueID)
	return err
}

// AddSource registers a new source on a publication.
func (db *DB) AddSource(ctx context.Context, pubID int64, typ, url, name string) (int64, error) {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sources (workspace_id, publication_id, type, url, name) VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (publication_id, url) DO UPDATE SET
		   name = CASE WHEN excluded.name != '' THEN excluded.name ELSE sources.name END`,
		db.ws, pubID, typ, url, name); err != nil {
		return 0, err
	}
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM sources WHERE workspace_id = $1 AND publication_id = $2 AND url = $3`, db.ws, pubID, url).Scan(&id)
	return id, err
}

// SetSourceMonitor records monitor provider state on a source.
func (db *DB) SetSourceMonitor(ctx context.Context, sourceID int64, provider, monitorID, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sources SET monitor_provider = $1, monitor_id = $2, monitor_status = $3 WHERE workspace_id = $4 AND id = $5`,
		provider, monitorID, status, db.ws, sourceID)
	return err
}

// SourceMonitor returns monitor fields for a source.
func (db *DB) SourceMonitor(ctx context.Context, sourceID int64) (provider, monitorID, status string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT monitor_provider, monitor_id, monitor_status FROM sources WHERE workspace_id = $1 AND id = $2`,
		db.ws, sourceID).Scan(&provider, &monitorID, &status)
	return
}

// FindSourceByMonitor resolves a source in this workspace by provider
// monitor id — the local-mode variant of the pool lookup.
func (db *DB) FindSourceByMonitor(ctx context.Context, monitorID string) (*Source, error) {
	var s Source
	err := db.QueryRowContext(ctx,
		`SELECT id, type, url, last_fetch_at, last_status FROM sources WHERE workspace_id = $1 AND monitor_id = $2`,
		db.ws, monitorID).Scan(&s.ID, &s.Type, &s.URL, &s.LastFetchAt, &s.LastStatus)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// MonitorSource is a source row plus its workspace — the global lookup
// the Firecrawl webhook needs, since monitor ids cross tenant
// boundaries by nature.
type MonitorSource struct {
	Source
	WorkspaceID string
}

// FindSourceByMonitor resolves a source from a provider monitor id
// across all workspaces — monitor ids are globally unique, and the
// webhook caller carries no tenant context.
func (p *Pool) FindSourceByMonitor(ctx context.Context, monitorID string) (*MonitorSource, error) {
	var s MonitorSource
	err := p.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, type, url, last_fetch_at, last_status FROM sources WHERE monitor_id = $1`,
		monitorID).Scan(&s.ID, &s.WorkspaceID, &s.Type, &s.URL, &s.LastFetchAt, &s.LastStatus)
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
		`SELECT id FROM readers WHERE workspace_id = $1 AND publication_id = $2 AND email = $3`,
		db.ws, pubID, email).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	err = db.QueryRowContext(ctx,
		`INSERT INTO readers (workspace_id, publication_id, email, name) VALUES ($1,$2,$3,$4) RETURNING id`,
		db.ws, pubID, email, name).Scan(&id)
	return id, err
}

// ActivateSubscription marks the reader's subscription active and stores
// the magic-link token that unlocks the reader view.
func (db *DB) ActivateSubscription(ctx context.Context, readerID int64, provider, ref, magicToken string) error {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM subscriptions WHERE workspace_id = $1 AND reader_id = $2`, db.ws, readerID).Scan(&id)
	if err == sql.ErrNoRows {
		_, err = db.ExecContext(ctx,
			`INSERT INTO subscriptions (workspace_id, reader_id, status, provider, provider_ref, magic_token)
			 VALUES ($1,$2,$3,$4,$5,$6)`, db.ws, readerID, "active", provider, ref, magicToken)
		return err
	}
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE subscriptions SET status = 'active', provider = $1, provider_ref = $2, magic_token = $3
		 WHERE workspace_id = $4 AND reader_id = $5`,
		provider, ref, magicToken, db.ws, readerID)
	return err
}

// CancelSubscription marks the reader's subscription canceled.
func (db *DB) CancelSubscription(ctx context.Context, readerID int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE subscriptions SET status = 'canceled' WHERE workspace_id = $1 AND reader_id = $2 AND status = 'active'`,
		db.ws, readerID)
	return err
}

// UnsubscribeReader records an email unsubscribe: the subscription is
// canceled and the reader row is stamped so sends can honor the opt-out.
func (db *DB) UnsubscribeReader(ctx context.Context, readerID int64) error {
	if _, err := db.ExecContext(ctx,
		`UPDATE readers SET unsubscribed_at = $1 WHERE workspace_id = $2 AND id = $3`,
		NowUTC(), db.ws, readerID); err != nil {
		return err
	}
	return db.CancelSubscription(ctx, readerID)
}

// ReaderUnsubscribed reports whether the reader opted out of emails.
func (db *DB) ReaderUnsubscribed(ctx context.Context, readerID int64) bool {
	var ts string
	err := db.QueryRowContext(ctx,
		`SELECT unsubscribed_at FROM readers WHERE workspace_id = $1 AND id = $2`, db.ws, readerID).Scan(&ts)
	return err == nil && ts != ""
}

// HasActiveSubscription reports whether the reader has an active sub.
// ReaderCount returns how many readers would receive an issue — every
// reader who hasn't unsubscribed. SubscriberCount counts the paying
// subset; the send dialog shows both ("N readers (M paying)").
func (db *DB) ReaderCount(ctx context.Context, pubID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM readers WHERE workspace_id = $1 AND publication_id = $2 AND unsubscribed_at = ''`,
		db.ws, pubID).Scan(&n)
	return n, err
}

// SubscriberCount returns how many readers hold an active subscription
// on a publication — the "recipients" line of the send confirmation.
func (db *DB) SubscriberCount(ctx context.Context, pubID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT s.reader_id) FROM subscriptions s
		JOIN readers r ON r.id = s.reader_id
		WHERE r.workspace_id = $1 AND r.publication_id = $2 AND s.status = 'active' AND r.unsubscribed_at = ''`,
		db.ws, pubID).Scan(&n)
	return n, err
}

func (db *DB) HasActiveSubscription(ctx context.Context, readerID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM subscriptions WHERE workspace_id = $1 AND reader_id = $2 AND status = 'active'`,
		db.ws, readerID).Scan(&n)
	return n > 0, err
}

// ReaderByMagicToken resolves a magic-link token to a reader id within a
// publication (sql.ErrNoRows when invalid).
func (db *DB) ReaderByMagicToken(ctx context.Context, pubID int64, token string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		SELECT r.id FROM readers r
		JOIN subscriptions s ON s.reader_id = r.id
		WHERE r.workspace_id = $1 AND r.publication_id = $2 AND s.magic_token = $3 AND s.magic_token != '' AND s.status = 'active'`,
		db.ws, pubID, token).Scan(&id)
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
		WHERE r.workspace_id = $1 AND r.publication_id = $2 AND s.magic_token = $3 AND s.magic_token != ''`,
		db.ws, pubID, token).Scan(&id)
	return id, err
}

// OutboxEmail is one outgoing message stored per workspace. Provider,
// ProviderID, Status and Error are the delivery-ledger fields: provider
// "resend" means the provider protocol was attempted (a real key or the
// loopback fake), "outbox" means it never left the app.
type OutboxEmail struct {
	ID         int64
	Kind       string
	To         string
	Subject    string
	BodyMD     string
	BodyHTML   string
	Provider   string
	ProviderID string
	Status     string
	Error      string
	CreatedAt  string
}

// SaveOutboxEmail records a simulated email in the workspace outbox.
func (db *DB) SaveOutboxEmail(ctx context.Context, kind, to, subject, bodyMD, bodyHTML string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO outbox_emails (workspace_id, kind, to_addr, subject, body_md, body_html)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		db.ws, kind, to, subject, bodyMD, bodyHTML).Scan(&id)
	return id, err
}

// OutboxEmails lists recorded emails, newest first.
func (db *DB) OutboxEmails(ctx context.Context) ([]OutboxEmail, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, kind, to_addr, subject, provider, provider_id, status, error, created_at
		 FROM outbox_emails WHERE workspace_id = $1 ORDER BY id DESC`, db.ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxEmail
	for rows.Next() {
		var e OutboxEmail
		if err := rows.Scan(&e.ID, &e.Kind, &e.To, &e.Subject,
			&e.Provider, &e.ProviderID, &e.Status, &e.Error, &e.CreatedAt); err != nil {
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
		`SELECT id, kind, to_addr, subject, body_md, body_html, provider, provider_id, status, error, created_at
		 FROM outbox_emails WHERE workspace_id = $1 AND id = $2`, db.ws, id).
		Scan(&e.ID, &e.Kind, &e.To, &e.Subject, &e.BodyMD, &e.BodyHTML,
			&e.Provider, &e.ProviderID, &e.Status, &e.Error, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// Progress returns a demo-progress flag ("" when unset).
func (db *DB) Progress(ctx context.Context, key string) string {
	var v string
	if err := db.QueryRowContext(ctx,
		`SELECT value FROM demo_progress WHERE workspace_id = $1 AND key = $2`, db.ws, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// SetProgress marks a demo-progress flag.
func (db *DB) SetProgress(ctx context.Context, key string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO demo_progress (workspace_id, key, value) VALUES ($1, $2, '1')
		 ON CONFLICT (workspace_id, key) DO NOTHING`, db.ws, key)
	return err
}

// Reset wipes all demo data in this workspace (used by "Reset demo"):
// the workspace row is deleted — the workspace_id cascade clears every
// tenant table — and recreated empty in the same transaction. Identity
// sequence values are not reused afterwards; nothing may assume them
// (D16's id-1 deep links died with the old id-sequence reset).
func (db *DB) Reset(ctx context.Context) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id = $1`, db.ws); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id) VALUES ($1)`, db.ws); err != nil {
		return err
	}
	return tx.Commit()
}
