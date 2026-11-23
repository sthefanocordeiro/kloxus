package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// ErrBadLoginLink is returned when a magic-link nonce is unknown, spent
// or expired.
var ErrBadLoginLink = errors.New("login link invalid or expired")

// Owner is the workspace owner's confirmed identity.
type Owner struct {
	Email        string
	Confirmed    bool
	LaunchInvite bool
}

// OwnerIdentity returns the workspace owner (zero value when none yet).
func (db *DB) OwnerIdentity(ctx context.Context) (Owner, error) {
	var o Owner
	var confirmed string
	var invite int
	err := db.QueryRowContext(ctx,
		`SELECT email, confirmed_at, launch_invite FROM owner_identity WHERE id = 1`).
		Scan(&o.Email, &confirmed, &invite)
	if err == sql.ErrNoRows {
		return Owner{}, nil
	}
	if err != nil {
		return Owner{}, err
	}
	o.Confirmed = confirmed != "" && o.Email != ""
	o.LaunchInvite = invite == 1
	return o, nil
}

// StartOwnerLogin records a pending e-mail with a fresh single-use nonce
// valid for ttl and returns the nonce. A new request replaces any older
// pending one; the confirmed identity is untouched until verification.
func (db *DB) StartOwnerLogin(ctx context.Context, email string, ttl time.Duration) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(b)
	exp := time.Now().UTC().Add(ttl).Format(time.RFC3339)
	_, err := db.ExecContext(ctx,
		`INSERT INTO owner_identity (id, pending_email, pending_nonce, pending_exp) VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET pending_email = excluded.pending_email,
		   pending_nonce = excluded.pending_nonce, pending_exp = excluded.pending_exp`,
		email, nonce, exp)
	return nonce, err
}

// ConfirmOwnerLogin spends the nonce: the pending e-mail becomes the
// confirmed owner and is marked for the launch invitation.
func (db *DB) ConfirmOwnerLogin(ctx context.Context, nonce string) (string, error) {
	if nonce == "" {
		return "", ErrBadLoginLink
	}
	var email, exp string
	err := db.QueryRowContext(ctx,
		`SELECT pending_email, pending_exp FROM owner_identity
		 WHERE id = 1 AND pending_nonce = ? AND pending_nonce != ''`, nonce).Scan(&email, &exp)
	if err == sql.ErrNoRows {
		return "", ErrBadLoginLink
	}
	if err != nil {
		return "", err
	}
	if t, perr := time.Parse(time.RFC3339, exp); perr != nil || time.Now().UTC().After(t) {
		return "", ErrBadLoginLink
	}
	_, err = db.ExecContext(ctx,
		`UPDATE owner_identity SET email = ?, confirmed_at = ?, launch_invite = 1,
		   pending_email = '', pending_nonce = '', pending_exp = '' WHERE id = 1`,
		email, time.Now().UTC().Format(time.RFC3339))
	return email, err
}
