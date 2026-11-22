-- KLOX-72: the newsletter's default edition template — an ordered
-- block list (same kinds as the composer, plus section slots) that
-- edition creation materializes into the stored body. New table-free
-- column: the template is per-publication runtime state like
-- sections_json. Empty means the legacy layout (sections in order,
-- no fixed blocks).
ALTER TABLE publications ADD COLUMN IF NOT EXISTS template_json TEXT NOT NULL DEFAULT '[]';
