ALTER TABLE stories ADD COLUMN triage_score  REAL;
ALTER TABLE stories ADD COLUMN triage_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE issue_items ADD COLUMN origin TEXT NOT NULL DEFAULT 'phaseA'; -- phaseA | alternative

ALTER TABLE readers ADD COLUMN name TEXT NOT NULL DEFAULT '';

ALTER TABLE subscriptions ADD COLUMN magic_token TEXT NOT NULL DEFAULT '';

ALTER TABLE sources ADD COLUMN name TEXT NOT NULL DEFAULT '';

CREATE TABLE outbox_emails (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL DEFAULT 'issue', -- issue | test | magic_link
  to_addr    TEXT NOT NULL DEFAULT '',
  subject    TEXT NOT NULL DEFAULT '',
  body_md    TEXT NOT NULL DEFAULT '',
  body_html  TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE demo_progress (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT '1'
);
