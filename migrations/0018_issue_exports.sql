-- KLOX-184: copying or downloading a rendition marks the piece as
-- exported. One row per format the author took out of Kloxus — the
-- lists read "Exportada" and show which formats left.
CREATE TABLE IF NOT EXISTS issue_exports (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_id     BIGINT NOT NULL,
  format       TEXT NOT NULL CHECK (format IN ('linkedin','x','html','markdown')),
  exported_at  TEXT NOT NULL DEFAULT (now_utc()),
  PRIMARY KEY (workspace_id, issue_id, format)
);
CREATE INDEX IF NOT EXISTS idx_issue_exports_issue ON issue_exports(issue_id);
