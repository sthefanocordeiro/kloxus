-- KLOX-114: Daily Journal leaves the catalog again (owner decision,
-- 2026-11-21 — reverses D57's re-add). The 21/11 E2E audit found DJ
-- back as the catalog's largest source (~778 items/week) feeding
-- general politics into fintech newsletters.
--
-- Stored items are deleted rather than merely stop-ingested — the same
-- option KLOX-45 took (migration 0003): their general-politics bulk is
-- what pollutes candidate sets, and the 7-day window would keep them
-- on the board for another week. Order matters: story_items holds a
-- hard FK to items, workspace picks hold none on the catalog side, and
-- fetch_log / item_embedding / item_enrichment cascade with the row.
-- The api_day/api_count columns stay: catalogCols scans them for every
-- row and the dormant client needs them if DJ is ever re-seeded with
-- KLOXUS_INGEST_DAILY_JOURNAL=1 (opt-in since this issue).
DELETE FROM story_items WHERE item_id IN (
  SELECT id FROM items WHERE catalog_id =
    (SELECT id FROM source_catalog WHERE slug = 'daily-journal'));
DELETE FROM sources WHERE catalog_id =
  (SELECT id FROM source_catalog WHERE slug = 'daily-journal');
DELETE FROM items WHERE catalog_id =
  (SELECT id FROM source_catalog WHERE slug = 'daily-journal');
DELETE FROM source_catalog WHERE slug = 'daily-journal';
