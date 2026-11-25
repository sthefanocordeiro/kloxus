-- KLOX-112 (ranking G5): per-section topic vectors ("Que tipo de pauta
-- entra aqui?"). topic_hash lets stage 1 re-embed a section only when
-- its topic text changed. vec is pgvector on Supabase, TEXT on plain
-- postgres (same pattern as publication_profiles).
DO $$
DECLARE vt text := 'TEXT';
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
    vt := 'vector(512)';
  END IF;
  EXECUTE 'CREATE TABLE IF NOT EXISTS section_profiles (
    publication_id BIGINT NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    section_id     TEXT NOT NULL,
    vec            ' || vt || ',
    topic_hash     TEXT NOT NULL DEFAULT '''',
    updated_at     TEXT NOT NULL DEFAULT (now_utc()),
    PRIMARY KEY (publication_id, section_id)
  )';
END $$;
