package store

import (
	"context"
)

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
