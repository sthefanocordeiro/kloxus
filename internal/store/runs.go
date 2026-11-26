package store

import (
	"context"
)

// AttachRuns links unattributed pipeline step rows (recorded before the
// issue row existed) to the issue that followed them.
func (db *DB) AttachRuns(ctx context.Context, pubID, issueID int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE runs SET issue_id = $1 WHERE workspace_id = $2 AND publication_id = $3 AND issue_id IS NULL`,
		issueID, db.ws, pubID)
	return err
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
