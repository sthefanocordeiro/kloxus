package store

import (
	"context"
	"database/sql"
)

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
	return db.ActivateSubscriptionUntil(ctx, readerID, provider, ref, magicToken, "")
}

// ActivateSubscriptionUntil is ActivateSubscription with an expiry —
// one-time checkouts grant a fixed window (D56); an empty paidUntil
// keeps the subscription lifetime-scoped (fake provider, demo paths).
// One row per reader (subscriptions_reader_key): the upsert is atomic so
// concurrent activations can't write a second row (KLOX-220).
func (db *DB) ActivateSubscriptionUntil(ctx context.Context, readerID int64, provider, ref, magicToken, paidUntil string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO subscriptions (workspace_id, reader_id, status, provider, provider_ref, magic_token, paid_until)
		 VALUES ($1,$2,'active',$3,$4,$5,$6)
		 ON CONFLICT (workspace_id, reader_id) DO UPDATE SET
		   status = 'active', provider = excluded.provider, provider_ref = excluded.provider_ref,
		   magic_token = excluded.magic_token, paid_until = excluded.paid_until`,
		db.ws, readerID, provider, ref, magicToken, paidUntil)
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
// Both writes commit together — a half-written unsubscribe would either keep
// sending to the reader or make an active subscription look unusable.
func (db *DB) UnsubscribeReader(ctx context.Context, readerID int64) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE readers SET unsubscribed_at = $1 WHERE workspace_id = $2 AND id = $3`,
		NowUTC(), db.ws, readerID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE subscriptions SET status = 'canceled' WHERE workspace_id = $1 AND reader_id = $2 AND status = 'active'`,
		db.ws, readerID); err != nil {
		return err
	}
	return tx.Commit()
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
		WHERE r.workspace_id = $1 AND r.publication_id = $2 AND s.status = 'active' AND r.unsubscribed_at = ''
		  AND (s.paid_until = '' OR s.paid_until > $3)`,
		db.ws, pubID, NowUTC()).Scan(&n)
	return n, err
}

// HasActiveSubscription reports an active, unexpired subscription; an
// empty paid_until never lapses (D56).
func (db *DB) HasActiveSubscription(ctx context.Context, readerID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM subscriptions WHERE workspace_id = $1 AND reader_id = $2 AND status = 'active' AND (paid_until = '' OR paid_until > $3)`,
		db.ws, readerID, NowUTC()).Scan(&n)
	return n > 0, err
}

// ReaderByMagicToken resolves a magic-link token to a reader id within a
// publication (sql.ErrNoRows when invalid or the access window lapsed).
func (db *DB) ReaderByMagicToken(ctx context.Context, pubID int64, token string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		SELECT r.id FROM readers r
		JOIN subscriptions s ON s.reader_id = r.id
		WHERE r.workspace_id = $1 AND r.publication_id = $2 AND s.magic_token = $3 AND s.magic_token != '' AND s.status = 'active'
		  AND (s.paid_until = '' OR s.paid_until > $4)`,
		db.ws, pubID, token, NowUTC()).Scan(&id)
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
