-- KLOX-278: RSS/Atom editorial categories as queryable item metadata.
-- The publisher's own labels stay verbatim (trimmed, first-seen dedupe in
-- ingest); matching folds case/accents in Go, so no normalization is baked
-- into storage. Empty means "no signal" and is never a penalty.
ALTER TABLE items
  ADD COLUMN IF NOT EXISTS categories JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Exact category lookups can use categories ? 'Agro'; the GIN index keeps
-- those probes off the shared items table's row count.
CREATE INDEX IF NOT EXISTS idx_items_categories ON items USING GIN (categories);

COMMENT ON COLUMN items.categories IS
  'Publisher-supplied RSS/Atom/API category labels, verbatim in stable order; [] means absent.';
