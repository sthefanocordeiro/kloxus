package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/config"
)

// CreateNextIssue inserts an issue at the publication's next sequential
// number and returns both ids. The publication row lock serializes number
// allocation across concurrent callers (including other app processes), and
// the UNIQUE(publication_id, number) constraint remains the database-level
// guard. subjectForNumber is invoked inside the transaction after the number
// is allocated; nil stores an empty subject.
func (db *DB) CreateNextIssue(ctx context.Context, pubID int64, subjectForNumber func(int) string, bodyMD string, sections []config.Section) (int64, int, error) {
	secs, _ := json.Marshal(sections)
	var id int64
	var number int
	err := db.WithTx(ctx, func(txdb *DB) error {
		var locked int64
		if err := txdb.QueryRowContext(ctx,
			`SELECT id FROM publications WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
			db.ws, pubID).Scan(&locked); err != nil {
			return err
		}
		if err := txdb.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(number), 0) + 1 FROM issues WHERE workspace_id = $1 AND publication_id = $2`,
			db.ws, pubID).Scan(&number); err != nil {
			return err
		}
		subject := ""
		if subjectForNumber != nil {
			subject = subjectForNumber(number)
		}
		return txdb.QueryRowContext(ctx,
			`INSERT INTO issues (workspace_id, publication_id, number, subject, body_md, sections_json) VALUES ($1,$2,$3,$4,$5,$6)
			 RETURNING id`,
			db.ws, pubID, number, subject, bodyMD, string(secs)).Scan(&id)
	})
	if err != nil {
		return 0, 0, err
	}
	return id, number, nil
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

// IssueBody returns the stored markdown body of an issue.
func (db *DB) IssueBody(ctx context.Context, pubID int64, number int) (string, error) {
	var body string
	err := db.QueryRowContext(ctx,
		`SELECT body_md FROM issues WHERE workspace_id = $1 AND publication_id = $2 AND number = $3`,
		db.ws, pubID, number).Scan(&body)
	return body, err
}

// UpdateIssue sets status, body and flags on an issue. A re-draft
// invalidates any stored approval receipt. Both writes commit together so a
// failed receipt cleanup cannot leave an edited issue looking approved.
func (db *DB) UpdateIssue(ctx context.Context, issueID int64, status, bodyMD, flagsJSON string) error {
	return db.WithTx(ctx, func(txdb *DB) error {
		if _, err := txdb.ExecContext(ctx,
			`UPDATE issues SET status = $1, body_md = $2, flags_json = $3 WHERE workspace_id = $4 AND id = $5`,
			status, bodyMD, flagsJSON, db.ws, issueID); err != nil {
			return err
		}
		if status != IssueApproved {
			if _, err := txdb.ExecContext(ctx,
				`DELETE FROM approval_receipts WHERE workspace_id = $1 AND issue_id = $2`, db.ws, issueID); err != nil {
				return err
			}
		}
		return nil
	})
}

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
	// Sections snapshots the publication's section definitions at run
	// time — an edition in progress keeps its own sections while the
	// creator edits the builder for the next one (KLOX-65). Empty on
	// issues written before the snapshot existed; callers fall back to
	// the publication's current sections.
	Sections    []config.Section
	CreatedAt   string
	ScheduledAt string
	TestSentAt  string
}

// Flag is a citation-check flag stored in issues.flags_json.
type Flag struct {
	Item     int64  `json:"item"`
	Sentence string `json:"sentence"`
	Note     string `json:"note"`
}

func (db *DB) scanIssue(row *sql.Row) (*Issue, error) {
	var i Issue
	var flags, commentary, sections string
	err := row.Scan(&i.ID, &i.PublicationID, &i.Number, &i.Status, &i.Subject,
		&i.BodyMD, &flags, &commentary, &sections, &i.CreatedAt, &i.ScheduledAt, &i.TestSentAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(flags), &i.Flags); err != nil {
		slog.Warn("issue row: malformed flags json", "issue", i.ID, "err", err)
	}
	if err := json.Unmarshal([]byte(commentary), &i.Commentary); err != nil {
		slog.Warn("issue row: malformed commentary json", "issue", i.ID, "err", err)
	}
	if err := json.Unmarshal([]byte(sections), &i.Sections); err != nil {
		slog.Warn("issue row: malformed sections json", "issue", i.ID, "err", err)
	}
	return &i, nil
}

const issueCols = `id, publication_id, number, status, subject, body_md, flags_json, commentary_json, sections_json, created_at, scheduled_at, test_sent_at`

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
		var flags, commentary, sections string
		if err := rows.Scan(&i.ID, &i.PublicationID, &i.Number, &i.Status,
			&i.Subject, &i.BodyMD, &flags, &commentary, &sections, &i.CreatedAt, &i.ScheduledAt, &i.TestSentAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(flags), &i.Flags); err != nil {
			slog.Warn("issue row: malformed flags json", "issue", i.ID, "err", err)
		}
		if err := json.Unmarshal([]byte(commentary), &i.Commentary); err != nil {
			slog.Warn("issue row: malformed commentary json", "issue", i.ID, "err", err)
		}
		if err := json.Unmarshal([]byte(sections), &i.Sections); err != nil {
			slog.Warn("issue row: malformed sections json", "issue", i.ID, "err", err)
		}
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

// SetIssueSubject updates an issue's subject — the pauta rename stores
// the author's name here (KLOX-199); it feeds the mail subject and the
// public page heading, never the exported text.
func (db *DB) SetIssueSubject(ctx context.Context, id int64, subject string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE issues SET subject = $1 WHERE workspace_id = $2 AND id = $3`, subject, db.ws, id)
	return err
}

// MarkIssueTestSent stamps a test send on the issue without touching
// its status — the after-sending screen and public page read the
// marker, the lifecycle stays editable (KLOX-53).
func (db *DB) MarkIssueTestSent(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE issues SET test_sent_at = $1 WHERE workspace_id = $2 AND id = $3`,
		NowUTC(), db.ws, id)
	return err
}

// ScheduleIssue locks an issue for a future send and queues the
// dispatch job that delivers it at that time. The status flip, replacement
// of any prior pending job, and the new queue entry commit together.
func (db *DB) ScheduleIssue(ctx context.Context, id int64, at string) error {
	// Parse first: a malformed 'at' must not commit the status flip.
	// The stored value is normalized RFC3339 UTC (KLOX-223) — callers
	// may still hand the plain timestamp shape in.
	t, err := parseScheduledAt(at)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"issue_id": id})
	if err != nil {
		return err
	}
	tx, err := db.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE issues SET status = 'scheduled', scheduled_at = $1 WHERE workspace_id = $2 AND id = $3`,
		t.UTC().Format(time.RFC3339), db.ws, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM jobs WHERE workspace_id = $1 AND kind = $2 AND ref_id = $3 AND status = 'pending'`,
		db.ws, JobKindSendIssue, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO jobs (workspace_id, kind, ref_id, payload_json, run_at) VALUES ($1,$2,$3,$4,$5)`,
		db.ws, JobKindSendIssue, id, string(payload), TSUTC(t)); err != nil {
		return err
	}
	return tx.Commit()
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
// queued send goes with it. The three cleanup writes are one transaction:
// a failure must not leave a drafted issue still holding a receipt or a
// pending send job.
func (db *DB) RevertIssueToDraft(ctx context.Context, id int64) error {
	tx, err := db.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE issues SET status = $1, scheduled_at = '' WHERE workspace_id = $2 AND id = $3`,
		IssueDrafted, db.ws, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM jobs WHERE workspace_id = $1 AND kind = $2 AND ref_id = $3 AND status = 'pending'`,
		db.ws, JobKindSendIssue, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM approval_receipts WHERE workspace_id = $1 AND issue_id = $2`, db.ws, id); err != nil {
		return err
	}
	return tx.Commit()
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
		`SELECT id, issue_id, story_id, section, score, reason, status, origin, position, orig_section, ai_draft, title
		 FROM issue_items WHERE workspace_id = $1 AND id = $2`, db.ws, id).
		Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin, &ii.Position, &ii.OrigSection, &ii.AIDraft, &ii.Title)
	if err != nil {
		return nil, err
	}
	return &ii, nil
}

// LatestPublicIssue returns the newest sent, scheduled or test-sent
// issue — what a public issue URL may resolve (sql.ErrNoRows when none
// is out yet).
func (db *DB) LatestPublicIssue(ctx context.Context, pubID int64) (*Issue, error) {
	return db.scanIssue(db.QueryRowContext(ctx,
		`SELECT `+issueCols+` FROM issues WHERE workspace_id = $1 AND publication_id = $2
		 AND (status IN ('sent','scheduled') OR test_sent_at <> '')
		 ORDER BY number DESC LIMIT 1`, db.ws, pubID))
}

// PublicIssues lists the publication's publicly visible issues — sent,
// scheduled or test-sent — newest first, capped at limit: the editions
// archive the /p/{slug} newsletter page offers (KLOX-102). Drafted or
// approved-but-unsent issues stay private.
func (db *DB) PublicIssues(ctx context.Context, pubID int64, limit int) ([]*Issue, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+issueCols+` FROM issues WHERE workspace_id = $1 AND publication_id = $2
		 AND (status IN ('sent','scheduled') OR test_sent_at <> '')
		 ORDER BY number DESC LIMIT $3`, db.ws, pubID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Issue
	for rows.Next() {
		var i Issue
		var flags, commentary, sections string
		if err := rows.Scan(&i.ID, &i.PublicationID, &i.Number, &i.Status,
			&i.Subject, &i.BodyMD, &flags, &commentary, &sections, &i.CreatedAt, &i.ScheduledAt, &i.TestSentAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(flags), &i.Flags)
		json.Unmarshal([]byte(commentary), &i.Commentary)
		json.Unmarshal([]byte(sections), &i.Sections)
		out = append(out, &i)
	}
	return out, rows.Err()
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
		`SELECT id, issue_id, story_id, section, score, reason, status, origin, position, orig_section, title
		 FROM issue_items WHERE workspace_id = $1 AND issue_id = $2 AND story_id = $3`, db.ws, issueID, storyID).
		Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin, &ii.Position, &ii.OrigSection, &ii.Title)
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
