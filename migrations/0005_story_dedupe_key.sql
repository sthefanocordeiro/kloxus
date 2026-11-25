-- KLOX-31: the story's deterministic dedupe key — the canonical URL
-- shared by every member item, or '' when the cluster merged on
-- near-identical text (SimHash) alone. Populated for new stories;
-- pre-existing rows keep ''.
ALTER TABLE stories ADD COLUMN dedupe_key TEXT NOT NULL DEFAULT '';
