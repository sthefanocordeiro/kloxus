-- KLOX-123 (pivot Block 2): the daily pauta. The global scheduler builds
-- each active radar's "pauta de hoje" once a day at 06:30
-- America/Sao_Paulo and the Hoje screen serves the stored cards; the
-- 07:00 mail to claimed radars stamps emailed_at so it never sends twice.
CREATE TABLE pauta_days (
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL REFERENCES publications(id),
  day            TEXT NOT NULL,           -- 'YYYY-MM-DD' in America/Sao_Paulo
  cards_json     TEXT NOT NULL DEFAULT '[]',
  built_at       TEXT NOT NULL DEFAULT (now_utc()),
  emailed_at     TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (workspace_id, publication_id, day)
);
