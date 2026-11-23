-- KLOX-108 (ranking engine G1): storage for Voyage embeddings, the
-- publication profile, rerank cache and one-line summaries, plus a
-- generated tsvector column for the FTS fallback.
--
-- pgvector ships on Supabase but not on CI's postgres:16-alpine, so the
-- vector tables use the same dual shape as item_embedding (0008): real
-- vector(512) + HNSW cosine index when the extension exists, TEXT
-- holding the '[f,f,...]' literal otherwise (Go-side cosine).
DO $$
BEGIN
  BEGIN
    CREATE EXTENSION IF NOT EXISTS vector;
  EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'pgvector unavailable - ranking vectors stay TEXT';
  END;
END $$;

DO $$
DECLARE vt TEXT := 'TEXT';
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
    vt := 'vector(512)';
  END IF;

  EXECUTE 'CREATE TABLE IF NOT EXISTS item_embeddings (
    item_id    BIGINT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    model      TEXT NOT NULL,
    dims       INTEGER NOT NULL,
    embedding  ' || vt || ' NOT NULL,
    created_at TEXT NOT NULL DEFAULT (now_utc())
  )';

  EXECUTE 'CREATE TABLE IF NOT EXISTS publication_profiles (
    publication_id BIGINT PRIMARY KEY REFERENCES publications(id) ON DELETE CASCADE,
    theme_vec      ' || vt || ',
    skip_vec       ' || vt || ',
    accepted_mean  ' || vt || ',
    rejected_mean  ' || vt || ',
    n_accepted     INTEGER NOT NULL DEFAULT 0,
    n_rejected     INTEGER NOT NULL DEFAULT 0,
    profile_hash   TEXT NOT NULL DEFAULT '''',
    updated_at     TEXT NOT NULL DEFAULT (now_utc())
  )';

  IF vt <> 'TEXT' THEN
    EXECUTE 'CREATE INDEX IF NOT EXISTS idx_item_embeddings_hnsw
             ON item_embeddings USING hnsw (embedding vector_cosine_ops)';
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS rerank_scores (
  publication_id BIGINT NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
  item_id        BIGINT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  profile_hash   TEXT NOT NULL,
  model          TEXT NOT NULL,
  score          REAL NOT NULL,
  created_at     TEXT NOT NULL DEFAULT (now_utc()),
  PRIMARY KEY (publication_id, item_id, profile_hash)
);

CREATE TABLE IF NOT EXISTS item_summaries (
  item_id    BIGINT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  summary    TEXT NOT NULL,
  kind       TEXT NOT NULL,
  model      TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (now_utc())
);

-- FTS fallback. unaccent() is not IMMUTABLE so it cannot sit inside a
-- generated column; the 'portuguese' config already stems and the
-- query side applies the same config (decision recorded in D60).
ALTER TABLE items ADD COLUMN IF NOT EXISTS fts tsvector
  GENERATED ALWAYS AS (to_tsvector('portuguese'::regconfig,
    title || ' ' || left(content_text, 2000))) STORED;
CREATE INDEX IF NOT EXISTS idx_items_fts ON items USING gin (fts);
