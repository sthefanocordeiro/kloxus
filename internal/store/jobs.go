package store

import (
	"context"
	"encoding/json"
	"time"
)

// --- workspaces ---

// EnsureWorkspace inserts the workspace row when absent (idempotent —
// the local/demo workspace and test workspaces are created on demand).
func (p *Pool) EnsureWorkspace(ctx context.Context, id string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO workspaces (id) VALUES ($1) ON CONFLICT (id) DO NOTHING`, id)
	return err
}

// WorkspaceExists reports whether the workspace row is present.
func (p *Pool) WorkspaceExists(ctx context.Context, id string) (bool, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM workspaces WHERE id = $1`, id).Scan(&n)
	return n > 0, err
}

// CreateWorkspace inserts a new workspace row.
func (p *Pool) CreateWorkspace(ctx context.Context, id string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO workspaces (id) VALUES ($1)`, id)
	return err
}

// TouchWorkspace refreshes last_seen_at so the idle sweeper keeps the
// workspace alive.
func (p *Pool) TouchWorkspace(ctx context.Context, id string) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE workspaces SET last_seen_at = $1 WHERE id = $2`, NowUTC(), id)
	return err
}

// DeleteWorkspace removes one workspace and every row it owns (the
// workspace_id cascade). Tests use it for cleanup.
func (p *Pool) DeleteWorkspace(ctx context.Context, id string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = $1`, id)
	return err
}

// DeleteIdleWorkspaces removes workspaces not seen since cutoff (a
// TSUTC-formatted time). The workspace_id cascade wipes every tenant
// row with them.
func (p *Pool) DeleteIdleWorkspaces(ctx context.Context, cutoff string) (int64, error) {
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM workspaces WHERE last_seen_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- jobs ---

// JobKindSendIssue is enqueued when an issue is scheduled: at run_at the
// dispatcher sends it to readers and marks it sent.
const JobKindSendIssue = "send_issue"

// Job is one queued unit of deferred work.
type Job struct {
	ID          int64
	WorkspaceID string
	Kind        string
	RefID       int64
	PayloadJSON string
	RunAt       string
	Attempts    int
}

// EnqueueJob stores a job for this workspace, due at runAt.
func (db *DB) EnqueueJob(ctx context.Context, kind string, refID int64, payload any, runAt time.Time) (int64, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	var id int64
	err = db.QueryRowContext(ctx,
		`INSERT INTO jobs (workspace_id, kind, ref_id, payload_json, run_at) VALUES ($1,$2,$3,$4,$5)
		 RETURNING id`,
		db.ws, kind, refID, string(body), TSUTC(runAt)).Scan(&id)
	return id, err
}

// CancelJobs drops pending jobs of a kind/ref for this workspace (e.g. a
// scheduled send whose issue went back to draft on Hold).
func (db *DB) CancelJobs(ctx context.Context, kind string, refID int64) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM jobs WHERE workspace_id = $1 AND kind = $2 AND ref_id = $3 AND status = 'pending'`,
		db.ws, kind, refID)
	return err
}

// ClaimDueJobs atomically marks up to limit due jobs running and returns
// them. FOR UPDATE SKIP LOCKED keeps concurrent dispatchers from
// taking the same row — a crashed dispatcher's row is recovered by
// RequeueStaleJobs.
func (p *Pool) ClaimDueJobs(ctx context.Context, limit int) ([]Job, error) {
	rows, err := p.db.QueryContext(ctx, `
		UPDATE jobs SET status = 'running', attempts = attempts + 1
		WHERE id IN (
			SELECT id FROM jobs
			WHERE status = 'pending' AND run_at <= $1
			ORDER BY id LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, workspace_id, kind, ref_id, payload_json, run_at, attempts`,
		NowUTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.WorkspaceID, &j.Kind, &j.RefID, &j.PayloadJSON, &j.RunAt, &j.Attempts); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// FinishJob marks a claimed job done.
func (p *Pool) FinishJob(ctx context.Context, jobID int64) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'done', last_error = '' WHERE id = $1`, jobID)
	return err
}

// FailJob records the failure; after maxAttempts the job is dead.
func (p *Pool) FailJob(ctx context.Context, jobID int64, cause error, maxAttempts int) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE jobs SET status = CASE WHEN attempts >= $2 THEN 'dead' ELSE 'pending' END,
			last_error = $3
		WHERE id = $1`, jobID, maxAttempts, cause.Error())
	return err
}

// RequeueStaleJobs returns jobs stuck in 'running' since before cutoff
// (a dispatcher that died mid-claim) to pending.
func (p *Pool) RequeueStaleJobs(ctx context.Context, cutoff string) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'pending' WHERE status = 'running' AND created_at < $1`, cutoff)
	return err
}
