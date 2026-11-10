CREATE TABLE publications (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  slug           TEXT UNIQUE NOT NULL,
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
  created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE sources (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  publication_id INTEGER NOT NULL REFERENCES publications(id),
  type           TEXT NOT NULL,              -- rss | page
  url            TEXT NOT NULL,
  last_fetch_at  TEXT,
  last_status    TEXT,
  UNIQUE (publication_id, url)
);

CREATE TABLE items (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  source_id      INTEGER NOT NULL REFERENCES sources(id),
  external_id    TEXT,
  url            TEXT,
  canonical_url  TEXT,
  title          TEXT NOT NULL,
  author         TEXT NOT NULL DEFAULT '',
  published_at   TEXT,
  content_text   TEXT NOT NULL DEFAULT '',
  fetched_at     TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (source_id, external_id)
);

CREATE TABLE stories (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  publication_id INTEGER NOT NULL REFERENCES publications(id),
  title          TEXT NOT NULL,
  simhash        INTEGER,
  created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE story_items (
  story_id INTEGER NOT NULL REFERENCES stories(id),
  item_id  INTEGER NOT NULL REFERENCES items(id),
  PRIMARY KEY (story_id, item_id)
);

CREATE TABLE issues (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  publication_id INTEGER NOT NULL REFERENCES publications(id),
  number         INTEGER NOT NULL,
  status         TEXT NOT NULL DEFAULT 'draft', -- draft | reviewing | approved | blocked | sent
  subject        TEXT NOT NULL DEFAULT '',
  body_md        TEXT NOT NULL DEFAULT '',
  body_html      TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (datetime('now')),
  approved_at    TEXT,
  sent_at        TEXT,
  UNIQUE (publication_id, number)
);

CREATE TABLE issue_items (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  issue_id INTEGER NOT NULL REFERENCES issues(id),
  story_id INTEGER NOT NULL REFERENCES stories(id),
  section  TEXT NOT NULL DEFAULT '',
  score    REAL,
  reason   TEXT NOT NULL DEFAULT '',
  status   TEXT NOT NULL DEFAULT 'pending'  -- pending | approved | rejected | alternative
);

CREATE TABLE dossiers (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  issue_item_id INTEGER NOT NULL REFERENCES issue_items(id),
  json          TEXT NOT NULL
);

CREATE TABLE decisions (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  publication_id INTEGER NOT NULL REFERENCES publications(id),
  issue_item_id  INTEGER REFERENCES issue_items(id),
  kind           TEXT NOT NULL,             -- approve | reject | edit | alternative
  reason         TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE editorial_guides (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  publication_id INTEGER NOT NULL REFERENCES publications(id),
  version        INTEGER NOT NULL,
  content        TEXT NOT NULL,
  created_at     TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (publication_id, version)
);

CREATE TABLE readers (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  publication_id INTEGER NOT NULL REFERENCES publications(id),
  email          TEXT NOT NULL,
  created_at     TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (publication_id, email)
);

CREATE TABLE subscriptions (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  reader_id    INTEGER NOT NULL REFERENCES readers(id),
  status       TEXT NOT NULL DEFAULT 'pending', -- pending | active | canceled
  provider     TEXT NOT NULL DEFAULT '',
  provider_ref TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE payment_accounts (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  publication_id INTEGER NOT NULL REFERENCES publications(id),
  provider       TEXT NOT NULL,
  encrypted_key  BLOB,
  created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE jobs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  kind         TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  run_at       TEXT,
  status       TEXT NOT NULL DEFAULT 'pending', -- pending | running | done | failed
  attempts     INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE runs (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  publication_id INTEGER NOT NULL REFERENCES publications(id),
  issue_id       INTEGER REFERENCES issues(id),
  step           TEXT NOT NULL,
  model          TEXT NOT NULL DEFAULT '',
  input_tokens   INTEGER NOT NULL DEFAULT 0,
  output_tokens  INTEGER NOT NULL DEFAULT 0,
  cost_usd       REAL NOT NULL DEFAULT 0,
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_items_source       ON items(source_id);
CREATE INDEX idx_stories_pub        ON stories(publication_id);
CREATE INDEX idx_issue_items_issue  ON issue_items(issue_id);
CREATE INDEX idx_decisions_pub      ON decisions(publication_id);
CREATE INDEX idx_runs_pub_issue     ON runs(publication_id, issue_id);
