package store

// Durable state for the LLM spend guards (D32/D36): a ledger of run
// starts and real model calls, plus a key/value settings table holding
// the persisted kill switch. Ledger rows deliberately carry no
// workspace FK — deleting or resetting a workspace must not refund its
// share of the global daily cap.
import "context"

// LLMRunStart records that a workspace started one pipeline run.
func (p *Pool) LLMRunStart(ctx context.Context, ws string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO llm_ledger (workspace_id, kind) VALUES ($1, 'run_start')`, ws)
	return err
}

// LLMRunUndo drops the workspace's newest recorded run start — a run
// that collected nothing made no model calls, so the budget refunds it.
func (p *Pool) LLMRunUndo(ctx context.Context, ws string) error {
	_, err := p.db.ExecContext(ctx,
		`DELETE FROM llm_ledger WHERE id = (
			SELECT id FROM llm_ledger
			WHERE workspace_id = $1 AND kind = 'run_start'
			ORDER BY id DESC LIMIT 1)`, ws)
	return err
}

// LLMCall records one real model call's usage and dollar cost.
func (p *Pool) LLMCall(ctx context.Context, ws, model string, in, out int, cost float64) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO llm_ledger (workspace_id, kind, model, input_tokens, output_tokens, cost_usd)
		 VALUES ($1, 'call', $2, $3, $4, $5)`, ws, model, in, out, cost)
	return err
}

// LLMRunsSince counts a workspace's recorded run starts at or after the
// stored timestamp (UTC day start, 'YYYY-MM-DD HH24:MI:SS').
func (p *Pool) LLMRunsSince(ctx context.Context, ws, since string) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM llm_ledger WHERE workspace_id = $1 AND kind = 'run_start' AND created_at >= $2`,
		ws, since).Scan(&n)
	return n, err
}

// LLMSpendSince sums the recorded dollar cost of real calls at or after
// the stored timestamp — the basis of the global daily cap.
func (p *Pool) LLMSpendSince(ctx context.Context, since string) (float64, error) {
	var total float64
	err := p.db.QueryRowContext(ctx,
		`SELECT COALESCE(sum(cost_usd), 0) FROM llm_ledger WHERE kind = 'call' AND created_at >= $1`,
		since).Scan(&total)
	return total, err
}

// AppSetting reads a global setting; sql.ErrNoRows when unset.
func (p *Pool) AppSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := p.db.QueryRowContext(ctx,
		`SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	return v, err
}

// SetAppSetting upserts a global setting.
func (p *Pool) SetAppSetting(ctx context.Context, key, value string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO app_settings (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now_utc()`,
		key, value)
	return err
}
