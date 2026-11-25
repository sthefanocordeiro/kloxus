-- KLOX-45 candidate set bounds + Daily Journal removal (reverses D31).
--
-- safe_ts parses a stored timestamp leniently: the columns are TEXT by
-- design (now_utc() format or RFC3339 from feed normalization), and an
-- unparseable feed date must fall back to fetched_at instead of failing
-- the row. STRICT returns NULL on NULL input.
CREATE OR REPLACE FUNCTION safe_ts(v TEXT) RETURNS TIMESTAMPTZ AS $$
BEGIN
  RETURN v::timestamptz;
EXCEPTION WHEN OTHERS THEN
  RETURN NULL;
END;
$$ LANGUAGE plpgsql STABLE STRICT;

-- Items stored before the deals filter existed get backmarked so no
-- stored deal can enter a run. The folded (lowercase, accents stripped)
-- term list mirrors internal/store/deals.go — update both together.
UPDATE items SET kind = 'promo'
WHERE kind <> 'promo' AND EXISTS (
  SELECT 1 FROM unnest(ARRAY[
    'promocao', 'promocoes', 'oferta', 'desconto', 'cupom', '% off',
    'black friday', 'black november', 'black da', 'esquenta black',
    '11.11', 'cyber monday', 'menor preco', 'preco historico',
    'selecionados', 'veja uma selecao', 'em ate 12x',
    'na amazon', 'no magalu', 'no mercado livre',
    'aliexpress', 'achados do'
  ]) AS t(term)
  WHERE strpos(
    regexp_replace(
      translate(lower(items.title),
        'áàâãäéèêëíìîïóòôõöúùûüçñ',
        'aaaaaeeeeiiiiooooouuuucn'),
      '[[:space:]]+', ' ', 'g'),
    t.term) > 0
);

-- Daily Journal leaves the catalog and its stored items are deleted.
-- Order matters: story links hold a hard FK to items, workspace picks
-- hold none on the catalog side, and fetch_log cascades with the row.
DELETE FROM story_items WHERE item_id IN (
  SELECT id FROM items WHERE catalog_id =
    (SELECT id FROM source_catalog WHERE slug = 'daily-journal'));
DELETE FROM sources WHERE catalog_id =
  (SELECT id FROM source_catalog WHERE slug = 'daily-journal');
DELETE FROM items WHERE catalog_id =
  (SELECT id FROM source_catalog WHERE slug = 'daily-journal');
DELETE FROM source_catalog WHERE slug = 'daily-journal';

-- The API-poll bookkeeping columns existed only for Daily Journal.
-- No API-backed sources remain, so the columns leave with the row.
ALTER TABLE source_catalog
  DROP COLUMN IF EXISTS cursor,
  DROP COLUMN IF EXISTS api_day,
  DROP COLUMN IF EXISTS api_count;
