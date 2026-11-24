-- Global ingestion (D29): feeds are fetched once into shared items,
-- workspaces only pick which catalog sources their editorial runs read.
-- The catalog is seeded here. The fetcher refuses any policy other than
-- launch or signal, enforced in code (internal/ingest).

CREATE TABLE source_catalog (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  slug           TEXT NOT NULL UNIQUE,
  name           TEXT NOT NULL,
  kind           TEXT NOT NULL DEFAULT 'rss',    -- rss | api
  url            TEXT NOT NULL,                  -- site URL shown to creators
  feed_url       TEXT NOT NULL DEFAULT '',       -- polled feed/API URL
  policy         TEXT NOT NULL,                  -- launch | signal | hold | blocked
  domain         TEXT NOT NULL DEFAULT '',
  crawl_delay_ms INTEGER NOT NULL DEFAULT 0,     -- min delay between requests to this host
  poll_minutes   INTEGER NOT NULL DEFAULT 45,    -- how often the scheduler polls
  body_mode      TEXT NOT NULL DEFAULT 'full',   -- full | quotes | links
  credit_author  BOOLEAN NOT NULL DEFAULT FALSE, -- show the individual author (TabNews)
  strip_css      TEXT NOT NULL DEFAULT '',       -- comma-separated class substrings to strip
  note           TEXT NOT NULL DEFAULT '',       -- explains hold/blocked to the UI
  enabled        BOOLEAN NOT NULL DEFAULT TRUE,
  suspended      BOOLEAN NOT NULL DEFAULT FALSE, -- robots turned us away
  suspend_reason TEXT NOT NULL DEFAULT '',
  etag           TEXT NOT NULL DEFAULT '',
  last_modified  TEXT NOT NULL DEFAULT '',
  last_fetch_at  TEXT,
  last_status    TEXT NOT NULL DEFAULT '',
  robots_at      TEXT,                           -- last robots.txt check
  cursor         TEXT NOT NULL DEFAULT '',       -- Daily Journal poll cursor
  api_day        TEXT NOT NULL DEFAULT '',       -- UTC day the api_count applies to
  api_count      INTEGER NOT NULL DEFAULT 0,     -- requests made that day
  created_at     TEXT NOT NULL DEFAULT (now_utc())
);

-- One row per fetch attempt — the per-source status view is built on it.
CREATE TABLE fetch_log (
  id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  catalog_id  BIGINT NOT NULL REFERENCES source_catalog(id) ON DELETE CASCADE,
  fetched_at  TEXT NOT NULL DEFAULT (now_utc()),
  status      TEXT NOT NULL,          -- ok | not_modified | error | suspended
  http_status INTEGER NOT NULL DEFAULT 0,
  items       INTEGER NOT NULL DEFAULT 0,
  new_items   INTEGER NOT NULL DEFAULT 0,
  detail      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_fetch_log_src ON fetch_log(catalog_id, id DESC);

-- Items become shared: a catalog item has catalog_id set and no
-- workspace/source owner (workspace_id stays NULL). Fixture-mode items
-- keep their per-workspace source_id. UNIQUE(source_id, external_id)
-- treats NULLs as distinct, so catalog items need their own index.
ALTER TABLE items ALTER COLUMN workspace_id DROP NOT NULL;
ALTER TABLE items ALTER COLUMN source_id DROP NOT NULL;
ALTER TABLE items ADD COLUMN catalog_id BIGINT REFERENCES source_catalog(id);
ALTER TABLE items ADD COLUMN kind TEXT NOT NULL DEFAULT 'article'; -- article | signal
ALTER TABLE items ADD COLUMN meta_json TEXT NOT NULL DEFAULT '{}'; -- signal metadata (Daily Journal fields)
ALTER TABLE items ADD COLUMN body_truncated BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX idx_items_catalog_ext ON items(catalog_id, external_id) WHERE catalog_id IS NOT NULL;
CREATE INDEX idx_items_catalog ON items(catalog_id, fetched_at);

-- A workspace's pick from the catalog.
ALTER TABLE sources ADD COLUMN catalog_id BIGINT REFERENCES source_catalog(id);

-- The catalog (owner audit of 2026-11-12, D29/D31). Policies:
--   launch  — full-text RSS, fetched and stored as evidence
--   signal  — links/summaries only (Hacker News, Daily Journal)
--   hold    — known source not yet cleared — never fetched
--   blocked — publisher disallows — never fetched, no workaround
INSERT INTO source_catalog
  (slug, name, kind, url, feed_url, policy, domain, crawl_delay_ms, poll_minutes, body_mode, credit_author, strip_css, note) VALUES
  ('tecnoblog', 'Tecnoblog', 'rss', 'https://tecnoblog.net', 'https://tecnoblog.net/feed/',
   'launch', 'tecnoblog.net', 0, 45, 'full', FALSE, 'oferta,offer', ''),
  ('olhar-digital', 'Olhar Digital', 'rss', 'https://olhardigital.com.br', 'https://olhardigital.com.br/feed/',
   'launch', 'olhardigital.com.br', 0, 45, 'full', FALSE, '', ''),
  ('tabnews', 'TabNews', 'rss', 'https://www.tabnews.com.br', 'https://www.tabnews.com.br/recentes/rss',
   'launch', 'www.tabnews.com.br', 0, 45, 'full', TRUE, '', ''),
  ('macmagazine', 'MacMagazine', 'rss', 'https://macmagazine.com.br', 'https://macmagazine.com.br/feed/',
   'launch', 'macmagazine.com.br', 10000, 45, 'full', FALSE, '', 'Root feed only — 10 s crawl delay.'),
  ('meiobit', 'Meio Bit', 'rss', 'https://meiobit.com', 'https://meiobit.com/feed/',
   'launch', 'meiobit.com', 0, 45, 'full', FALSE, '', ''),
  ('startups', 'Startups.com.br', 'rss', 'https://startups.com.br', 'https://startups.com.br/feed/',
   'launch', 'startups.com.br', 0, 45, 'quotes', FALSE, '', 'Summaries and short quotes only.'),
  ('hacker-news', 'Hacker News', 'rss', 'https://news.ycombinator.com', 'https://news.ycombinator.com/rss',
   'signal', 'news.ycombinator.com', 30000, 45, 'links', FALSE, '', 'Links only — 30 s crawl delay.'),
  ('daily-journal', 'Daily Journal', 'api', 'https://dailyjournal.news', 'https://dailyjournal.news/api/public/news',
   'signal', 'dailyjournal.news', 0, 15, 'links', FALSE, '',
   'Signal via the public API only: pre-clustered stories across Brazilian outlets — capped at 350 requests/day.'),
  ('canaltech', 'Canaltech', 'rss', 'https://canaltech.com.br', 'https://canaltech.com.br/rss/',
   'blocked', 'canaltech.com.br', 0, 60, 'full', FALSE, '', 'Blocks automated fetchers — no licensed mirror — deliberately not fetched.'),
  ('mobiletime', 'MobileTime', 'rss', 'https://www.mobiletime.com.br', '',
   'blocked', 'www.mobiletime.com.br', 0, 60, 'full', FALSE, '', 'Terms disallow automated access — the wp-json workaround is deliberately not used.'),
  ('techcrunch', 'TechCrunch', 'rss', 'https://techcrunch.com', 'https://techcrunch.com/feed/',
   'blocked', 'techcrunch.com', 0, 60, 'full', FALSE, '', 'Terms disallow automated access.'),
  ('wired', 'Wired', 'rss', 'https://www.wired.com', 'https://www.wired.com/feed/rss',
   'blocked', 'www.wired.com', 0, 60, 'full', FALSE, '', 'Terms disallow automated access.'),
  ('the-verge', 'The Verge', 'rss', 'https://www.theverge.com', 'https://www.theverge.com/rss/index.xml',
   'blocked', 'www.theverge.com', 0, 60, 'full', FALSE, '', 'Terms disallow automated access.'),
  ('exame', 'Exame', 'rss', 'https://exame.com', 'https://exame.com/feed/',
   'hold', 'exame.com', 0, 60, 'full', FALSE, '', 'Held pending the publisher''s permission.'),
  ('ars-technica', 'Ars Technica', 'rss', 'https://arstechnica.com', 'https://arstechnica.com/feed/',
   'hold', 'arstechnica.com', 0, 60, 'full', FALSE, '', 'Held pending terms review.'),
  ('gizmodo-br', 'Gizmodo Brasil', 'rss', 'https://gizmodo.uol.com.br', 'https://gizmodo.uol.com.br/feed/',
   'hold', 'gizmodo.uol.com.br', 0, 60, 'full', FALSE, '', 'Held pending terms review.'),
  ('tecmundo', 'TecMundo', 'rss', 'https://www.tecmundo.com.br', 'https://www.tecmundo.com.br/rss',
   'hold', 'www.tecmundo.com.br', 0, 60, 'full', FALSE, '', 'Held pending terms review.')
ON CONFLICT (slug) DO NOTHING;
