package store

// payments.go — paid-section checkout rows (KLOX-13): creation, lookup
// by our ref or the provider's billing id, the pending→paid transition
// that makes the grant idempotent, and the throttled poll timestamp.

import (
	"context"
	"database/sql"
	"fmt"
)

// Payment is one checkout: pending until the webhook or the status
// poll marks it paid.
type Payment struct {
	ID            int64
	PublicationID int64
	ReaderID      int64
	Ref           string // pay_... — our handle, sent to the provider as externalId
	Provider      string // 'abacatepay' | 'fake'
	ProviderRef   string // bill_... — set once the billing exists
	Status        string // pending | paid | expired | cancelled
	AmountCents   int
	CheckoutURL   string
	PaidUntil     string
	LastPolledAt  string
	CreatedAt     string
}

const paymentCols = `id, publication_id, reader_id, ref, provider, provider_ref, status, amount_cents, checkout_url, paid_until, last_polled_at, created_at`

func scanPayment(row *sql.Row) (*Payment, error) {
	var p Payment
	err := row.Scan(&p.ID, &p.PublicationID, &p.ReaderID, &p.Ref, &p.Provider,
		&p.ProviderRef, &p.Status, &p.AmountCents, &p.CheckoutURL, &p.PaidUntil,
		&p.LastPolledAt, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// CreatePayment opens a checkout row in pending state.
func (db *DB) CreatePayment(ctx context.Context, pubID, readerID int64, ref, provider string, amountCents int) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO payments (workspace_id, publication_id, reader_id, ref, provider, amount_cents) VALUES ($1,$2,$3,$4,$5,$6)`,
		db.ws, pubID, readerID, ref, provider, amountCents)
	return err
}

// SetPaymentCheckout records the provider billing id + hosted URL after
// a successful create; a provider failure switches the row to the fake
// provider so the fallback grant stays auditable on the same ref.
func (db *DB) SetPaymentCheckout(ctx context.Context, id int64, provider, providerRef, checkoutURL string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE payments SET provider = $1, provider_ref = $2, checkout_url = $3 WHERE id = $4 AND workspace_id = $5`,
		provider, providerRef, checkoutURL, id, db.ws)
	return err
}

// PaymentByRef loads a payment by our local ref, workspace-scoped and
// pinned to the publication the reader is paying for.
func (db *DB) PaymentByRef(ctx context.Context, pubID int64, ref string) (*Payment, error) {
	return scanPayment(db.QueryRowContext(ctx,
		`SELECT `+paymentCols+` FROM payments WHERE workspace_id = $1 AND ref = $2 AND publication_id = $3`,
		db.ws, ref, pubID))
}

// PendingPaymentForReader returns the reader's still-payable checkout
// for a publication, or nil — reuse it instead of opening a second
// billing when the reader clicks Subscribe again.
func (db *DB) PendingPaymentForReader(ctx context.Context, pubID, readerID int64) (*Payment, error) {
	p, err := scanPayment(db.QueryRowContext(ctx,
		`SELECT `+paymentCols+` FROM payments WHERE workspace_id = $1 AND reader_id = $2 AND publication_id = $3 AND status = 'pending' AND provider_ref <> '' ORDER BY id DESC`,
		db.ws, readerID, pubID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// FindPaymentByProviderRef resolves a webhook's billing id across
// workspaces and returns the workspace id so the handler can scope
// itself. provider_ref is unique per provider (bill ids are global).
func (p *Pool) FindPaymentByProviderRef(ctx context.Context, provider, providerRef string) (*Payment, string, error) {
	var pay Payment
	var ws string
	err := p.db.QueryRowContext(ctx,
		`SELECT `+paymentCols+`, workspace_id FROM payments WHERE provider = $1 AND provider_ref = $2`,
		provider, providerRef).Scan(
		&pay.ID, &pay.PublicationID, &pay.ReaderID, &pay.Ref, &pay.Provider,
		&pay.ProviderRef, &pay.Status, &pay.AmountCents, &pay.CheckoutURL,
		&pay.PaidUntil, &pay.LastPolledAt, &pay.CreatedAt, &ws)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("payment lookup %s/%s: %w", provider, providerRef, err)
	}
	return &pay, ws, nil
}

// FindPaymentByRef resolves a payment by our local ref (the externalId
// echoed on the event) when the billing id itself is absent or stale.
func (p *Pool) FindPaymentByRef(ctx context.Context, ref string) (*Payment, string, error) {
	var pay Payment
	var ws string
	err := p.db.QueryRowContext(ctx,
		`SELECT `+paymentCols+`, workspace_id FROM payments WHERE ref = $1`, ref).Scan(
		&pay.ID, &pay.PublicationID, &pay.ReaderID, &pay.Ref, &pay.Provider,
		&pay.ProviderRef, &pay.Status, &pay.AmountCents, &pay.CheckoutURL,
		&pay.PaidUntil, &pay.LastPolledAt, &pay.CreatedAt, &ws)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("payment lookup %s: %w", ref, err)
	}
	return &pay, ws, nil
}

// MarkPaymentPaid flips pending→paid once; rows-affected==0 means a
// webhook replay or a raced poll already granted it — the caller must
// not send a second magic link.
func (db *DB) MarkPaymentPaid(ctx context.Context, id int64, paidUntil string) (bool, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE payments SET status = 'paid', paid_until = $1 WHERE id = $2 AND workspace_id = $3 AND status = 'pending'`,
		paidUntil, id, db.ws)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetPaymentStatus ends a checkout that can no longer be paid.
func (db *DB) SetPaymentStatus(ctx context.Context, id int64, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE payments SET status = $1 WHERE id = $2 AND workspace_id = $3`, status, id, db.ws)
	return err
}

// TouchPaymentPoll throttles the status endpoint's provider calls.
func (db *DB) TouchPaymentPoll(ctx context.Context, id int64, at string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE payments SET last_polled_at = $1 WHERE id = $2 AND workspace_id = $3`, at, id, db.ws)
	return err
}
