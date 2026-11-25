-- KLOX-93: Daily Journal returns as the primary news source (owner
-- directive, KLOX-92 — partially reverses D41) + pgvector semantic
-- layer.
--
-- The catalog row is re-seeded (kind='api', policy='launch'), items now
-- store Daily Journal's full aggregated body as kind='article' — DJ's
-- own text, always cited as "Daily Journal" with its link, which is
-- what their terms and llms.txt ask for. The api_day/api_count columns
-- come back for the shared 350-requests/day counter; the durable
-- `cursor` column stays gone — DJ's own poll guidance starts every
-- scheduled poll at the first page and detects changes by comparing
-- id + updated_at.
ALTER TABLE source_catalog
  ADD COLUMN IF NOT EXISTS api_day   TEXT    NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS api_count INTEGER NOT NULL DEFAULT 0;

INSERT INTO source_catalog
  (slug, name, kind, url, feed_url, policy, domain, crawl_delay_ms, poll_minutes, body_mode, credit_author, strip_css, note) VALUES
  ('daily-journal', 'Daily Journal', 'api', 'https://dailyjournal.news', 'https://dailyjournal.news/api/public/news',
   'launch', 'dailyjournal.news', 0, 30, 'full', FALSE, '',
   'Primary source — public API only. Stores DJ''s aggregate body, always cited as Daily Journal; 350 requests/day hard cap.')
ON CONFLICT (slug) DO NOTHING;

-- Publications that already picked catalog sources get the Daily
-- Journal pick automatically — it is the primary source now (the
-- 'rss' stored type mirrors AddCatalogSource's display-only mapping).
INSERT INTO sources (workspace_id, publication_id, type, url, name, catalog_id)
SELECT p.workspace_id, p.id, 'rss', c.url, c.name, c.id
FROM publications p, source_catalog c
WHERE c.slug = 'daily-journal'
  AND EXISTS (SELECT 1 FROM sources s
              WHERE s.publication_id = p.id AND s.catalog_id IS NOT NULL)
ON CONFLICT (publication_id, url) DO NOTHING;

-- pgvector, when the database image ships it (Supabase does;
-- postgres:16-alpine in CI does not). The fallback column type is
-- TEXT holding the same '[f,f,...]' literal — Go-side cosine covers
-- similarity on vector-less databases.
DO $$
BEGIN
  BEGIN
    CREATE EXTENSION IF NOT EXISTS vector;
  EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'pgvector unavailable — item_embedding.embedding stays TEXT';
  END;
END $$;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
    EXECUTE 'CREATE TABLE IF NOT EXISTS item_embedding (
      item_id     BIGINT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
      embedding   vector(256) NOT NULL,
      model       TEXT NOT NULL DEFAULT '''',
      embedded_at TEXT NOT NULL DEFAULT (now_utc())
    )';
  ELSE
    EXECUTE 'CREATE TABLE IF NOT EXISTS item_embedding (
      item_id     BIGINT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
      embedding   TEXT NOT NULL,
      model       TEXT NOT NULL DEFAULT '''',
      embedded_at TEXT NOT NULL DEFAULT (now_utc())
    )';
  END IF;
END $$;
