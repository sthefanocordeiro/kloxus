-- KLOX-125 (pivot block 4): per-format renditions of an approved piece.
-- A rendition forks the piece's text through a fixed template (no model)
-- and is then edited independently. base_hash is the hash of the
-- template output at fork/refresh time, so "base changed" is a content
-- comparison rather than a version guess.
CREATE TABLE IF NOT EXISTS renditions (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_id     BIGINT NOT NULL,
  format       TEXT NOT NULL CHECK (format IN ('linkedin','x','html','markdown')),
  body_text    TEXT NOT NULL DEFAULT '',
  body_html    TEXT NOT NULL DEFAULT '',
  body_md      TEXT NOT NULL DEFAULT '',
  base_hash    TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (now_utc()),
  updated_at   TEXT NOT NULL DEFAULT (now_utc()),
  UNIQUE (workspace_id, issue_id, format)
);
CREATE INDEX IF NOT EXISTS idx_renditions_issue ON renditions(issue_id);
