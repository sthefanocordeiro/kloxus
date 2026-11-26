-- Kloxus schema on Postgres (D28): one shared database, workspace_id on
-- every tenant table. Timestamps stay TEXT in the 'YYYY-MM-DD HH24:MI:SS'
-- UTC shape the API has always emitted — comparisons are lexical, so
-- keep every write in the same format via now_utc().

CREATE OR REPLACE FUNCTION now_utc() RETURNS TEXT AS $$
  SELECT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
$$ LANGUAGE SQL STABLE;

CREATE TABLE workspaces (
  id           TEXT PRIMARY KEY,
  label        TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (now_utc()),
  last_seen_at TEXT NOT NULL DEFAULT (now_utc())
);

CREATE TABLE publications (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  slug           TEXT NOT NULL,
  name           TEXT NOT NULL,
  language       TEXT NOT NULL DEFAULT 'pt-BR',
  topic          TEXT NOT NULL DEFAULT '',
  audience       TEXT NOT NULL DEFAULT '',
  editorial_line TEXT NOT NULL DEFAULT '',
  tone           TEXT NOT NULL DEFAULT '',
  frequency      TEXT NOT NULL DEFAULT 'weekly',
  sections_json  TEXT NOT NULL DEFAULT '[]',
  paywall_json   TEXT NOT NULL DEFAULT '{}',
  ai_disclosure  TEXT NOT NULL DEFAULT 'short',
  custom         BOOLEAN NOT NULL DEFAULT FALSE,
  created_at     TEXT NOT NULL DEFAULT (now_utc()),
  UNIQUE (workspace_id, slug)
);

CREATE TABLE sources (
  id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id     TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id   BIGINT NOT NULL REFERENCES publications(id),
  type             TEXT NOT NULL,              -- rss | page
  url              TEXT NOT NULL,
  name             TEXT NOT NULL DEFAULT '',
  last_fetch_at    TEXT,
  last_status      TEXT,
  monitor_provider TEXT NOT NULL DEFAULT '',
  monitor_id       TEXT NOT NULL DEFAULT '',
  monitor_status   TEXT NOT NULL DEFAULT '',
  UNIQUE (publication_id, url)
);
CREATE INDEX idx_sources_monitor ON sources(monitor_id) WHERE monitor_id <> '';

CREATE TABLE items (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  source_id      BIGINT NOT NULL REFERENCES sources(id),
  external_id    TEXT,
  url            TEXT,
  canonical_url  TEXT,
  title          TEXT NOT NULL,
  author         TEXT NOT NULL DEFAULT '',
  published_at   TEXT,
  content_text   TEXT NOT NULL DEFAULT '',
  fetched_at     TEXT NOT NULL DEFAULT (now_utc()),
  UNIQUE (source_id, external_id)
);
CREATE INDEX idx_items_source ON items(source_id);

CREATE TABLE stories (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL REFERENCES publications(id),
  title          TEXT NOT NULL,
  simhash        BIGINT,
  triage_score   DOUBLE PRECISION,
  triage_reason  TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (now_utc())
);
CREATE INDEX idx_stories_pub ON stories(publication_id);

CREATE TABLE story_items (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  story_id     BIGINT NOT NULL REFERENCES stories(id),
  item_id      BIGINT NOT NULL REFERENCES items(id),
  PRIMARY KEY (story_id, item_id)
);

CREATE TABLE issues (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id  BIGINT NOT NULL REFERENCES publications(id),
  number          INTEGER NOT NULL,
  status          TEXT NOT NULL DEFAULT 'draft', -- draft | reviewing | approved | blocked | sent | scheduled
  subject         TEXT NOT NULL DEFAULT '',
  body_md         TEXT NOT NULL DEFAULT '',
  body_html       TEXT NOT NULL DEFAULT '',
  flags_json      TEXT NOT NULL DEFAULT '[]',
  commentary_json TEXT NOT NULL DEFAULT '{}',
  scheduled_at    TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL DEFAULT (now_utc()),
  approved_at     TEXT,
  sent_at         TEXT,
  UNIQUE (publication_id, number)
);

CREATE TABLE issue_items (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_id     BIGINT NOT NULL REFERENCES issues(id),
  -- story_id is deliberately NOT a foreign key: stories are rebuilt from
  -- scratch every run (D13) and old issue_items keep a dangling story_id.
  story_id     BIGINT NOT NULL,
  section      TEXT NOT NULL DEFAULT '',
  score        DOUBLE PRECISION,
  reason       TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL DEFAULT 'pending', -- pending | approved | rejected | alternative
  origin       TEXT NOT NULL DEFAULT 'phaseA',  -- phaseA | alternative
  position     INTEGER NOT NULL DEFAULT 0,
  orig_section TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_issue_items_issue ON issue_items(issue_id);

CREATE TABLE dossiers (
  id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_item_id BIGINT NOT NULL REFERENCES issue_items(id),
  json          TEXT NOT NULL,
  UNIQUE (issue_item_id)
);

CREATE TABLE decisions (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL REFERENCES publications(id),
  issue_item_id  BIGINT REFERENCES issue_items(id),
  kind           TEXT NOT NULL,             -- approve | reject | edit | alternative
  reason         TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (now_utc())
);
CREATE INDEX idx_decisions_pub ON decisions(publication_id);

CREATE TABLE editorial_guides (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL REFERENCES publications(id),
  version        INTEGER NOT NULL,
  content        TEXT NOT NULL,
  created_at     TEXT NOT NULL DEFAULT (now_utc()),
  UNIQUE (publication_id, version)
);

CREATE TABLE readers (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id  BIGINT NOT NULL REFERENCES publications(id),
  email           TEXT NOT NULL,
  name            TEXT NOT NULL DEFAULT '',
  unsubscribed_at TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL DEFAULT (now_utc()),
  UNIQUE (publication_id, email)
);

CREATE TABLE subscriptions (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  reader_id    BIGINT NOT NULL REFERENCES readers(id),
  status       TEXT NOT NULL DEFAULT 'pending', -- pending | active | canceled
  provider     TEXT NOT NULL DEFAULT '',
  provider_ref TEXT NOT NULL DEFAULT '',
  magic_token  TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (now_utc())
);

CREATE TABLE payment_accounts (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL REFERENCES publications(id),
  provider       TEXT NOT NULL,
  encrypted_key  BYTEA,
  created_at     TEXT NOT NULL DEFAULT (now_utc())
);

CREATE TABLE jobs (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,
  ref_id       BIGINT NOT NULL DEFAULT 0,
  payload_json TEXT NOT NULL DEFAULT '{}',
  run_at       TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'pending', -- pending | running | done | dead
  attempts     INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (now_utc())
);
CREATE INDEX idx_jobs_due ON jobs(status, run_at) WHERE status = 'pending';

CREATE TABLE runs (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL REFERENCES publications(id),
  issue_id       BIGINT REFERENCES issues(id),
  step           TEXT NOT NULL,
  model          TEXT NOT NULL DEFAULT '',
  input_tokens   INTEGER NOT NULL DEFAULT 0,
  output_tokens  INTEGER NOT NULL DEFAULT 0,
  cost_usd       DOUBLE PRECISION NOT NULL DEFAULT 0,
  duration_ms    BIGINT NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL DEFAULT (now_utc())
);
CREATE INDEX idx_runs_pub_issue ON runs(publication_id, issue_id);

CREATE TABLE approval_receipts (
  workspace_id     TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_id         BIGINT PRIMARY KEY REFERENCES issues(id) ON DELETE CASCADE,
  number           INTEGER NOT NULL,
  document_version TEXT NOT NULL,
  approver         TEXT NOT NULL,
  checks_json      TEXT NOT NULL DEFAULT '[]',
  approved_at      TEXT NOT NULL DEFAULT (now_utc())
);

CREATE TABLE outbox_emails (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL DEFAULT 'issue', -- issue | test | magic_link
  to_addr      TEXT NOT NULL DEFAULT '',
  subject      TEXT NOT NULL DEFAULT '',
  body_md      TEXT NOT NULL DEFAULT '',
  body_html    TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (now_utc())
);

CREATE TABLE demo_progress (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  key          TEXT NOT NULL,
  value        TEXT NOT NULL DEFAULT '1',
  PRIMARY KEY (workspace_id, key)
);
