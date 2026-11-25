package store

import (
	"context"
)

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
