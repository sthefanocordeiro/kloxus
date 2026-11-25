-- KLOX-183: persist the Sim/Não answers on stories. The Rocchio fold
-- only shifts ranking weights (and is a no-op without embeddings); the
-- answer itself must survive the stories-table rebuild — ClearStories/
-- CreateStories run on every deck — so a "Não" keeps the story out of
-- Hoje and out of the re-dealt calibration cards. story_key is one of
-- the stable identifiers a story carries across rebuilds:
-- "u:<canonical dedupe URL>" or "s:<simhash of the representative
-- member>". The latest answer per key wins.
CREATE TABLE story_feedback (
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
  story_key      TEXT NOT NULL,
  answer         TEXT NOT NULL,           -- yes | no
  title          TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (now_utc()),
  PRIMARY KEY (publication_id, story_key)
);
