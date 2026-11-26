package store

import (
	"context"
)

// RecordDecision stores an approve/reject/edit/alternative decision.
// IssueItemID 0 means a publication-level decision and is stored as NULL.
func (db *DB) RecordDecision(ctx context.Context, pubID int64, issueItemID int64, kind, reason string) error {
	var ii any
	if issueItemID != 0 {
		ii = issueItemID
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO decisions (workspace_id, publication_id, issue_item_id, kind, reason) VALUES ($1,$2,$3,$4,$5)`,
		db.ws, pubID, ii, kind, reason)
	return err
}

// LatestDecision returns the most recent decision on an issue item
// (sql.ErrNoRows when none).
func (db *DB) LatestDecision(ctx context.Context, issueItemID int64) (kind, reason string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT kind, reason FROM decisions WHERE workspace_id = $1 AND issue_item_id = $2 ORDER BY id DESC LIMIT 1`,
		db.ws, issueItemID).Scan(&kind, &reason)
	return kind, reason, err
}

// DecisionExists reports whether a publication carries a decision with
// the given kind and exact reason — used to detect "this issue was once
// approved" after a post-approval edit voided the receipt (KLOX-66).
func (db *DB) DecisionExists(ctx context.Context, pubID int64, kind, reason string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM decisions WHERE workspace_id = $1 AND publication_id = $2 AND kind = $3 AND reason = $4`,
		db.ws, pubID, kind, reason).Scan(&n)
	return n > 0, err
}

// Decision is a stored review decision.
type Decision struct {
	ItemID    int64
	Kind      string
	Reason    string
	CreatedAt string
}

// Decisions lists a publication's decisions, oldest first.
func (db *DB) Decisions(ctx context.Context, pubID int64) ([]Decision, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(issue_item_id, 0), kind, reason, created_at FROM decisions
		 WHERE workspace_id = $1 AND publication_id = $2 ORDER BY id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.ItemID, &d.Kind, &d.Reason, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
