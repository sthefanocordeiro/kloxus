-- KLOX-66 (D3): newsletter composer support tables. Both are new
-- tables — no shared-table schema changes.

-- media stores author-uploaded images (banners, inline photos) so sent
-- email and reader pages embed same-origin bytes. The token makes the
-- public /m/{id}-{token} URL unguessable.
CREATE TABLE IF NOT EXISTS media (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL,
  token          TEXT NOT NULL,
  content_type   TEXT NOT NULL,
  data           BYTEA NOT NULL,
  created_at     TEXT NOT NULL DEFAULT (now_utc())
);
CREATE INDEX IF NOT EXISTS idx_media_pub ON media(publication_id);

-- standard_messages is the reusable greeting/footer/signature library:
-- authored snippets the creator inserts into an edition without
-- retyping. Inserted text becomes an ordinary text block and stays
-- fully editable — a IA nunca escreve aqui.
CREATE TABLE IF NOT EXISTS standard_messages (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL,
  name           TEXT NOT NULL,
  body           TEXT NOT NULL,
  created_at     TEXT NOT NULL DEFAULT (now_utc())
);
CREATE INDEX IF NOT EXISTS idx_standard_messages_pub ON standard_messages(publication_id);
