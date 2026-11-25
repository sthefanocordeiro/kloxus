package store

import (
	"context"
	"strings"
)

// AddWaitlist records a pre-launch signup (KLOX-127). Idempotent: a
// repeat email is a silent no-op so callers cannot tell new from known.
func (p *Pool) AddWaitlist(ctx context.Context, email, source string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO waitlist (email, source) VALUES ($1, $2)
		 ON CONFLICT (lower(email)) DO NOTHING`,
		strings.ToLower(strings.TrimSpace(email)), source)
	return err
}

// WaitlistCount returns the number of distinct signups.
func (p *Pool) WaitlistCount(ctx context.Context) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM waitlist`).Scan(&n)
	return n, err
}

// ExecWaitlistCleanup removes one signup; test cleanup only.
func (p *Pool) ExecWaitlistCleanup(ctx context.Context, email string) {
	_, _ = p.db.ExecContext(ctx, `DELETE FROM waitlist WHERE lower(email) = lower($1)`, email)
}
