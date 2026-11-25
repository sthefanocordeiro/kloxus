-- KLOX-111 (ranking G4): one-line card summary + type for the run's
-- top stories. Global cache keyed by item — the same item is never
-- summarised twice, by any workspace or run. model='deterministic'
-- rows come from the zero-cost template (metadata only, never source
-- prose); a model name marks an LLM-written row if one is ever enabled.
CREATE TABLE IF NOT EXISTS item_summaries (
  item_id    BIGINT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  summary    TEXT NOT NULL,
  kind       TEXT NOT NULL,
  model      TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'))
);
