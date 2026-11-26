package store

// Real delivery (D33): workspace claims, magic links, reader double
// opt-in, the suppression list and the outbox send ledger. Outbox rows
// carry provider/status/idempotency state so caps survive restarts and
// the Resend webhook can resolve the message it reports on.
import (
	"context"
	"database/sql"
)

// --- workspace claim ---

// WorkspaceOwner returns the claiming owner's email and the claim
// timestamp; both empty when the workspace is still anonymous.
func (p *Pool) WorkspaceOwner(ctx context.Context, id string) (email, claimedAt string, err error) {
	err = p.db.QueryRowContext(ctx,
		`SELECT owner_email, claimed_at FROM workspaces WHERE id = $1`, id).Scan(&email, &claimedAt)
	return
}

// ClaimWorkspace marks the workspace owned by email.
func (p *Pool) ClaimWorkspace(ctx context.Context, id, email string) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE workspaces SET owner_email = $1, claimed_at = $2 WHERE id = $3`,
		email, NowUTC(), id)
	return err
}

// ClaimWorkspace is the local-mode variant on a workspace handle.
func (db *DB) ClaimWorkspace(ctx context.Context, email string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE workspaces SET owner_email = $1, claimed_at = $2 WHERE id = $3`,
		email, NowUTC(), db.ws)
	return err
}

// WorkspaceOwner is the local-mode variant on a workspace handle.
func (db *DB) WorkspaceOwner(ctx context.Context) (email, claimedAt string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT owner_email, claimed_at FROM workspaces WHERE id = $1`, db.ws).Scan(&email, &claimedAt)
	return
}

// UILang returns the interface language saved on the account ("" when
// the owner never picked one — the client then falls back to its own
// storage and the browser language, KLOX-232).
func (db *DB) UILang(ctx context.Context) string {
	var v string
	if err := db.QueryRowContext(ctx,
		`SELECT ui_lang FROM workspaces WHERE id = $1`, db.ws).Scan(&v); err != nil {
		return ""
	}
	return v
}

// SetUILang saves the interface language on the account so a magic link
// and a fresh browser land in the same language (KLOX-232).
func (db *DB) SetUILang(ctx context.Context, lang string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE workspaces SET ui_lang = $1 WHERE id = $2`, lang, db.ws)
	return err
}

// --- magic links ---

// SaveMagicLink stores a hashed claim token for this workspace.
func (db *DB) SaveMagicLink(ctx context.Context, tokenHash, email, expiresAt string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO magic_links (workspace_id, token_hash, email, expires_at) VALUES ($1,$2,$3,$4)`,
		db.ws, tokenHash, email, expiresAt)
	return err
}

// RecentMagicLinks counts links minted for this workspace since a
// timestamp — the claim rate limit.
func (db *DB) RecentMagicLinks(ctx context.Context, since string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM magic_links WHERE workspace_id = $1 AND created_at >= $2`,
		db.ws, since).Scan(&n)
	return n, err
}

// PeekMagicLink resolves a token without spending it — the GET side of
// the two-step claim, which mail-scanner prefetches must leave intact.
// Same validity rule as ConsumeMagicLink (sql.ErrNoRows on invalid,
// spent or expired).
func (p *Pool) PeekMagicLink(ctx context.Context, tokenHash, now string) (wsID, email string, err error) {
	err = p.db.QueryRowContext(ctx, `
		SELECT workspace_id, email FROM magic_links
		WHERE token_hash = $1 AND used_at = '' AND expires_at > $2`,
		tokenHash, now).Scan(&wsID, &email)
	return
}

// PeekMagicLink is the local-mode variant.
func (db *DB) PeekMagicLink(ctx context.Context, tokenHash, now string) (email string, err error) {
	err = db.QueryRowContext(ctx, `
		SELECT email FROM magic_links
		WHERE workspace_id = $3 AND token_hash = $1 AND used_at = '' AND expires_at > $2`,
		tokenHash, now, db.ws).Scan(&email)
	return
}

// ConsumeMagicLink atomically spends a token: it must exist, be unused
// and unexpired. Returns the workspace and email it was minted for
// (sql.ErrNoRows on anything else — the link is invalid, spent or
// expired, which the handler reports as one failure).
func (p *Pool) ConsumeMagicLink(ctx context.Context, tokenHash, now string) (wsID, email string, err error) {
	err = p.db.QueryRowContext(ctx, `
		UPDATE magic_links SET used_at = $2
		WHERE token_hash = $1 AND used_at = '' AND expires_at > $2
		RETURNING workspace_id, email`, tokenHash, now).Scan(&wsID, &email)
	return
}

// ConsumeMagicLink is the local-mode variant: the link can only have
// been minted by this workspace anyway.
func (db *DB) ConsumeMagicLink(ctx context.Context, tokenHash, now string) (email string, err error) {
	err = db.QueryRowContext(ctx, `
		UPDATE magic_links SET used_at = $2
		WHERE workspace_id = $3 AND token_hash = $1 AND used_at = '' AND expires_at > $2
		RETURNING email`, tokenHash, now, db.ws).Scan(&email)
	return
}

// --- account recovery sign-in links (KLOX-272) ---

// OwnedWorkspace is one workspace belonging to an email account, with
// enough shape to prefer the real Radar over accidental empty sessions.
type OwnedWorkspace struct {
	ID           string
	LastSeenAt   string
	ClaimedAt    string
	Publications int
}

// WorkspacesByOwner lists every workspace claimed by email, useful
// first: workspaces carrying publications come before empty duplicate
// sessions, then most recently seen first.
func (p *Pool) WorkspacesByOwner(ctx context.Context, email string) ([]OwnedWorkspace, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT w.id, w.last_seen_at, w.claimed_at, count(p.id)
		FROM workspaces w
		LEFT JOIN publications p ON p.workspace_id = w.id
		WHERE w.owner_email = $1
		GROUP BY w.id, w.last_seen_at, w.claimed_at
		ORDER BY count(p.id) DESC, w.last_seen_at DESC`, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OwnedWorkspace
	for rows.Next() {
		var ow OwnedWorkspace
		if err := rows.Scan(&ow.ID, &ow.LastSeenAt, &ow.ClaimedAt, &ow.Publications); err != nil {
			return nil, err
		}
		out = append(out, ow)
	}
	return out, rows.Err()
}

// SaveLoginLink stores a hashed account-recovery token.
func (p *Pool) SaveLoginLink(ctx context.Context, tokenHash, email, expiresAt string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO login_links (token_hash, email, expires_at) VALUES ($1,$2,$3)`,
		tokenHash, email, expiresAt)
	return err
}

// RecentLoginLinks counts recovery links minted for an email since a
// timestamp — the account-login rate limit.
func (p *Pool) RecentLoginLinks(ctx context.Context, email, since string) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM login_links WHERE email = $1 AND created_at >= $2`,
		email, since).Scan(&n)
	return n, err
}

// PeekLoginLink resolves a recovery token without spending it — the GET
// side of the two-step sign-in, which mail-scanner prefetches must leave
// intact (sql.ErrNoRows on invalid, spent or expired).
func (p *Pool) PeekLoginLink(ctx context.Context, tokenHash, now string) (email string, err error) {
	err = p.db.QueryRowContext(ctx, `
		SELECT email FROM login_links
		WHERE token_hash = $1 AND used_at = '' AND expires_at > $2`,
		tokenHash, now).Scan(&email)
	return
}

// ConsumeLoginLink atomically spends a recovery token: it must exist,
// be unused and unexpired (sql.ErrNoRows on anything else).
func (p *Pool) ConsumeLoginLink(ctx context.Context, tokenHash, now string) (email string, err error) {
	err = p.db.QueryRowContext(ctx, `
		UPDATE login_links SET used_at = $2
		WHERE token_hash = $1 AND used_at = '' AND expires_at > $2
		RETURNING email`, tokenHash, now).Scan(&email)
	return
}

// --- daily pauta mail (KLOX-257) ---

// DailyMailEnabled reports whether the account still wants the 07:00
// "notícias do dia" mail — saved accounts are opted in by default; the
// account-menu toggle and the mailed unsubscribe link stamp the same
// daily_mail_off_at column.
func (db *DB) DailyMailEnabled(ctx context.Context) (bool, error) {
	var off string
	err := db.QueryRowContext(ctx,
		`SELECT daily_mail_off_at FROM workspaces WHERE id = $1`, db.ws).Scan(&off)
	return off == "", err
}

// SetDailyMail flips the account-menu toggle for every workspace the
// owner holds — the opt-out is owner-keyed like the daily mail claim
// (KLOX-154), so one "Receber as notícias do dia" switch can't fight
// another claimed radar's stale stamp.
func (db *DB) SetDailyMail(ctx context.Context, owner string, enabled bool) error {
	off := ""
	if !enabled {
		off = NowUTC()
	}
	_, err := db.ExecContext(ctx,
		`UPDATE workspaces SET daily_mail_off_at = $1 WHERE owner_email = $2`, off, owner)
	return err
}

// SaveDailyMailToken stores the hash of the one-click unsubscribe token
// the next daily mail carries — each send replaces it, so only the
// newest mailed link resolves an account.
func (db *DB) SaveDailyMailToken(ctx context.Context, tokenHash string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE workspaces SET daily_mail_token_hash = $1 WHERE id = $2`, tokenHash, db.ws)
	return err
}

// DailyMailOptedOut reports whether the owner already stamped the
// unsubscribe on any of their workspaces — the check is owner-keyed
// because the one-mail-a-day claim is (KLOX-154): a click in one
// radar's mail must stop every radar's mail to that address.
func (p *Pool) DailyMailOptedOut(ctx context.Context, owner string) (bool, error) {
	return dailyMailOptedOut(ctx, p.db, owner)
}

// DailyMailOptedOut is the local-mode variant on a workspace handle.
func (db *DB) DailyMailOptedOut(ctx context.Context, owner string) (bool, error) {
	return dailyMailOptedOut(ctx, db, owner)
}

func dailyMailOptedOut(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM workspaces WHERE owner_email = $1 AND daily_mail_off_at <> ''`,
		owner).Scan(&n)
	return n > 0, err
}

// UnsubscribeDailyMail stamps the opt-out on every workspace the
// token's owner holds and returns the account's saved interface
// language for the confirmation page; found=false when no token hash
// matched. The update is one statement keyed on the token's owner so a
// second claimed radar can't keep mailing the address tomorrow.
func (p *Pool) UnsubscribeDailyMail(ctx context.Context, tokenHash string) (lang string, found bool, err error) {
	return unsubscribeDailyMail(ctx, p.db, tokenHash)
}

// UnsubscribeDailyMail is the local-mode variant on a workspace handle.
func (db *DB) UnsubscribeDailyMail(ctx context.Context, tokenHash string) (lang string, found bool, err error) {
	return unsubscribeDailyMail(ctx, db, tokenHash)
}

func unsubscribeDailyMail(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, tokenHash string) (lang string, found bool, err error) {
	res, err := q.ExecContext(ctx, `
		UPDATE workspaces SET daily_mail_off_at = $2
		WHERE owner_email <> ''
		  AND owner_email = (SELECT owner_email FROM workspaces
		                     WHERE daily_mail_token_hash = $1)`,
		tokenHash, NowUTC())
	if err != nil {
		return "", false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", false, nil
	}
	_ = q.QueryRowContext(ctx,
		`SELECT ui_lang FROM workspaces WHERE daily_mail_token_hash = $1`, tokenHash).Scan(&lang)
	return lang, true, nil
}

// --- readers ---

// Reader is a stored reader row with its confirmation state.
type Reader struct {
	ID           int64
	Email        string
	Name         string
	Confirmed    bool
	Unsubscribed bool
}

// ConfirmedReaders lists readers eligible to receive issues: confirmed
// by double opt-in and not unsubscribed. Suppression is applied by the
// sender, not here.
func (db *DB) ConfirmedReaders(ctx context.Context, pubID int64) ([]Reader, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, email, name FROM readers
		 WHERE workspace_id = $1 AND publication_id = $2 AND confirmed_at <> '' AND unsubscribed_at = ''`,
		db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reader
	for rows.Next() {
		var r Reader
		if err := rows.Scan(&r.ID, &r.Email, &r.Name); err != nil {
			return nil, err
		}
		r.Confirmed = true
		out = append(out, r)
	}
	return out, rows.Err()
}

// ConfirmedReaderCount counts double-opted-in readers — the sendable
// audience shown next to the send dialog.
func (db *DB) ConfirmedReaderCount(ctx context.Context, pubID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM readers WHERE workspace_id = $1 AND publication_id = $2
		 AND confirmed_at != '' AND unsubscribed_at = ''`,
		db.ws, pubID).Scan(&n)
	return n, err
}

// ReaderByEmail returns the reader row for (publication, email), or
// sql.ErrNoRows.
func (db *DB) ReaderByEmail(ctx context.Context, pubID int64, email string) (*Reader, error) {
	var r Reader
	var confirmed, unsub string
	err := db.QueryRowContext(ctx,
		`SELECT id, email, name, confirmed_at, unsubscribed_at FROM readers
		 WHERE workspace_id = $1 AND publication_id = $2 AND email = $3`,
		db.ws, pubID, email).Scan(&r.ID, &r.Email, &r.Name, &confirmed, &unsub)
	if err != nil {
		return nil, err
	}
	r.Confirmed = confirmed != ""
	r.Unsubscribed = unsub != ""
	return &r, nil
}

// MarkReaderConfirmed stamps the reader's double opt-in.
func (db *DB) MarkReaderConfirmed(ctx context.Context, readerID int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE readers SET confirmed_at = $1 WHERE workspace_id = $2 AND id = $3 AND confirmed_at = ''`,
		NowUTC(), db.ws, readerID)
	return err
}

// UnsubscribeEmail unsubscribes every reader row with this address in
// the workspace — the bounce/complaint path, which arrives without a
// reader id.
func (db *DB) UnsubscribeEmail(ctx context.Context, email string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE readers SET unsubscribed_at = $1 WHERE workspace_id = $2 AND email = $3 AND unsubscribed_at = ''`,
		NowUTC(), db.ws, email)
	return err
}

// --- suppression list ---

// The suppression table is deliberately global — it is not tenant data:
// a bounce anywhere means the address must never be mailed again from
// any workspace. Both pool- and workspace-scoped methods reach it.

// SuppressEmail adds an address to the global suppression list.
func (p *Pool) SuppressEmail(ctx context.Context, email, reason string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO email_suppressions (email, reason) VALUES ($1,$2) ON CONFLICT (email) DO NOTHING`,
		email, reason)
	return err
}

// SuppressEmail is the local-mode variant on a workspace handle — same
// global table.
func (db *DB) SuppressEmail(ctx context.Context, email, reason string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO email_suppressions (email, reason) VALUES ($1,$2) ON CONFLICT (email) DO NOTHING`,
		email, reason)
	return err
}

// Suppressed returns the set of suppressed addresses among the given
// ones — checked before every real send.
func (p *Pool) Suppressed(ctx context.Context, emails []string) (map[string]bool, error) {
	return suppressedQuery(ctx, p.db, emails)
}

// Suppressed is the local-mode variant on a workspace handle.
func (db *DB) Suppressed(ctx context.Context, emails []string) (map[string]bool, error) {
	return suppressedQuery(ctx, db.db, emails)
}

func suppressedQuery(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, emails []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(emails) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx,
		`SELECT email FROM email_suppressions WHERE email = ANY($1)`, emails)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out[e] = true
	}
	return out, rows.Err()
}

// --- send ledger (outbox_emails) ---

// OutboxMail is one ledger row: the message plus its delivery state
// (the OutboxEmail ledger fields) and its idempotency key.
type OutboxMail struct {
	OutboxEmail
	IdemKey string
}

// SaveMail records one outgoing message and returns (id, created).
// With an idempotency key the insert is deduped: a repeated key returns
// the existing row with created=false — the caller must not send again.
// A status='fallback' row is the exception: it records a send that never
// left the app, so it must not satisfy idempotency — the conflict
// requeues the row in place (fresh body, queued again, attempt stamped)
// and reports created=true so the retry really reaches the provider.
func (db *DB) SaveMail(ctx context.Context, kind, to, subject, bodyMD, bodyHTML, provider, status, idemKey string) (id int64, created bool, err error) {
	err = db.QueryRowContext(ctx, `
		INSERT INTO outbox_emails (workspace_id, kind, to_addr, subject, body_md, body_html, provider, status, idem_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (workspace_id, idem_key) WHERE idem_key <> ''
		DO UPDATE SET to_addr = EXCLUDED.to_addr, subject = EXCLUDED.subject,
			body_md = EXCLUDED.body_md, body_html = EXCLUDED.body_html,
			provider = EXCLUDED.provider, provider_id = '',
			status = EXCLUDED.status, error = '', created_at = EXCLUDED.created_at
		WHERE outbox_emails.status = 'fallback'
		RETURNING id`, db.ws, kind, to, subject, bodyMD, bodyHTML, provider, status, idemKey).Scan(&id)
	if err == sql.ErrNoRows {
		err = db.QueryRowContext(ctx,
			`SELECT id FROM outbox_emails WHERE workspace_id = $1 AND idem_key = $2`,
			db.ws, idemKey).Scan(&id)
		return id, false, err
	}
	return id, err == nil, err
}

// SaveMailRefused records a send that never left the app (cap hit,
// suppressed address) — visible in the outbox instead of dropped.
func (db *DB) SaveMailRefused(ctx context.Context, kind, to, subject, reason string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO outbox_emails (workspace_id, kind, to_addr, subject, provider, status, error)
		VALUES ($1,$2,$3,$4,'outbox','refused',$5) RETURNING id`,
		db.ws, kind, to, subject, reason).Scan(&id)
	return id, err
}

// MailSent marks a queued send delivered to the provider.
func (db *DB) MailSent(ctx context.Context, id int64, providerID string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE outbox_emails SET status = 'sent', provider_id = $1, error = '' WHERE workspace_id = $2 AND id = $3`,
		providerID, db.ws, id)
	return err
}

// MailFallback flips a failed provider send into an outbox copy: the
// message exists for the creator even though nothing was emailed.
func (db *DB) MailFallback(ctx context.Context, id int64, cause string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE outbox_emails SET status = 'fallback', error = $1 WHERE workspace_id = $2 AND id = $3`,
		cause, db.ws, id)
	return err
}

// GetMail loads one ledger row with its delivery state.
func (db *DB) GetMail(ctx context.Context, id int64) (*OutboxMail, error) {
	var m OutboxMail
	err := db.QueryRowContext(ctx,
		`SELECT id, kind, to_addr, subject, body_md, body_html, provider, provider_id, status, idem_key, error, created_at
		 FROM outbox_emails WHERE workspace_id = $1 AND id = $2`, db.ws, id).
		Scan(&m.ID, &m.Kind, &m.To, &m.Subject, &m.BodyMD, &m.BodyHTML,
			&m.Provider, &m.ProviderID, &m.Status, &m.IdemKey, &m.Error, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// MailByIdemKey returns the ledger row for an idempotency key, or
// sql.ErrNoRows.
func (db *DB) MailByIdemKey(ctx context.Context, idemKey string) (*OutboxMail, error) {
	var m OutboxMail
	err := db.QueryRowContext(ctx,
		`SELECT id, kind, to_addr, subject, body_md, body_html, provider, provider_id, status, idem_key, error, created_at
		 FROM outbox_emails WHERE workspace_id = $1 AND idem_key = $2`, db.ws, idemKey).
		Scan(&m.ID, &m.Kind, &m.To, &m.Subject, &m.BodyMD, &m.BodyHTML,
			&m.Provider, &m.ProviderID, &m.Status, &m.IdemKey, &m.Error, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// SendsToday counts attempted real sends since the start of the UTC
// day — provider='resend' rows, refused rows excepted since they never
// reached the API. day is 'YYYY-MM-DD'.
func (db *DB) SendsToday(ctx context.Context, day string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox_emails
		 WHERE workspace_id = $1 AND provider = 'resend'
		   AND created_at >= $2 AND created_at < ($2::date + 1)::text`,
		db.ws, day).Scan(&n)
	return n, err
}

// SendsTodayAll counts attempted real sends across every workspace —
// the global daily cap.
func (p *Pool) SendsTodayAll(ctx context.Context, day string) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox_emails
		 WHERE provider = 'resend'
		   AND created_at >= $1 AND created_at < ($1::date + 1)::text`,
		day).Scan(&n)
	return n, err
}

// StaleSentCount counts provider-accepted sends still at 'sent' past
// the given cutoff — Resend reports delivery only through the webhook,
// so rows stuck here mean the endpoint is dead or disabled (KLOX-98).
func (db *DB) StaleSentCount(ctx context.Context, cutoff string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox_emails
		 WHERE workspace_id = $1 AND provider = 'resend' AND status = 'sent' AND created_at < $2`,
		db.ws, cutoff).Scan(&n)
	return n, err
}

// MailTarget is the ledger row a provider webhook reports on.
type MailTarget struct {
	ID          int64
	WorkspaceID string
	To          string
	Status      string
}

// FindMailByProviderID resolves a provider email id to its ledger row —
// the webhook caller carries no tenant context.
func (p *Pool) FindMailByProviderID(ctx context.Context, providerID string) (*MailTarget, error) {
	var t MailTarget
	err := p.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, to_addr, status FROM outbox_emails WHERE provider_id = $1`,
		providerID).Scan(&t.ID, &t.WorkspaceID, &t.To, &t.Status)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// SetMailStatusByID records a delivery event on a ledger row — global
// like the lookup, because webhooks arrive without a workspace.
func (p *Pool) SetMailStatusByID(ctx context.Context, id int64, status string) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE outbox_emails SET status = $1 WHERE id = $2`, status, id)
	return err
}

// SetMailStatus is the workspace-scoped variant (local mode).
func (db *DB) SetMailStatus(ctx context.Context, id int64, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE outbox_emails SET status = $1 WHERE workspace_id = $2 AND id = $3`, status, db.ws, id)
	return err
}

// FindMailByProviderID is the workspace-scoped variant (local mode).
func (db *DB) FindMailByProviderID(ctx context.Context, providerID string) (*MailTarget, error) {
	var t MailTarget
	err := db.QueryRowContext(ctx,
		`SELECT id, workspace_id, to_addr, status FROM outbox_emails WHERE workspace_id = $1 AND provider_id = $2`,
		db.ws, providerID).Scan(&t.ID, &t.WorkspaceID, &t.To, &t.Status)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
