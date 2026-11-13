-- R4c (D33): real delivery through Resend, workspace claim by magic
-- link, reader double opt-in, send caps and the suppression list.

-- A workspace is claimed by proving control of an inbox: the owner
-- email gates test sends and unlocks reader sends.
ALTER TABLE workspaces
  ADD COLUMN owner_email TEXT NOT NULL DEFAULT '',
  ADD COLUMN claimed_at  TEXT NOT NULL DEFAULT '';

-- Magic-link tokens for the claim flow: only the SHA-256 hash is
-- stored, the link is single-use and expires in minutes.
CREATE TABLE magic_links (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  token_hash   TEXT NOT NULL UNIQUE,
  email        TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  used_at      TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (now_utc())
);
CREATE INDEX idx_magic_links_ws ON magic_links(workspace_id);

-- Readers confirm by double opt-in before any email reaches them.
ALTER TABLE readers ADD COLUMN confirmed_at TEXT NOT NULL DEFAULT '';

-- Global suppression list: a bounce or complaint anywhere stops every
-- future send to that address, whatever the workspace.
CREATE TABLE email_suppressions (
  email      TEXT PRIMARY KEY,
  reason     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (now_utc())
);

-- The outbox doubles as the send ledger: every attempted message is a
-- row, so caps survive restarts and the Resend webhook can find the
-- message it reports on. status: queued | sent | delivered | bounced |
-- complained | refused | fallback. provider: resend (a real API call
-- was attempted) | outbox (never left the app).
ALTER TABLE outbox_emails
  ADD COLUMN provider    TEXT NOT NULL DEFAULT 'outbox',
  ADD COLUMN provider_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN status      TEXT NOT NULL DEFAULT 'sent',
  ADD COLUMN idem_key    TEXT NOT NULL DEFAULT '',
  ADD COLUMN error       TEXT NOT NULL DEFAULT '';
-- Idempotency keys are per-workspace: issue ids and reader ids repeat
-- across tenants, so uniqueness must scope to (workspace, key).
CREATE UNIQUE INDEX idx_outbox_idem ON outbox_emails(workspace_id, idem_key) WHERE idem_key <> '';
CREATE INDEX idx_outbox_created ON outbox_emails(created_at);
