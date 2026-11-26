-- KLOX-257: daily pauta mail unsubscribe. '' in daily_mail_off_at means
-- the 07:00 "notícias do dia" mail is wanted — the default for saved
-- accounts; the account-menu toggle and the mailed one-click link stamp
-- the same column. The token column keeps only the SHA-256 of the
-- unsubscribe token each mail carries — the raw token lives only in
-- the email, never in the database.
ALTER TABLE workspaces
  ADD COLUMN IF NOT EXISTS daily_mail_off_at TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS daily_mail_token_hash TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_workspaces_daily_mail_token
  ON workspaces(daily_mail_token_hash) WHERE daily_mail_token_hash <> '';
