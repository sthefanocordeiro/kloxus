-- KLOX-152: TabNews leaves the active catalog (owner decision,
-- 2026-11-23): it is a forum/community, outside the editorial
-- positioning (serious news portals). Soft removal, history kept:
-- the catalog row stays but is disabled (Fetchable() is false, the
-- scheduler skips it), and the workspace picks are dropped so its
-- already-stored items stop feeding candidate sets. Stored items,
-- fetch_log and embeddings are NOT deleted. Re-enable with
-- UPDATE source_catalog SET enabled = TRUE WHERE slug = 'tabnews'.
UPDATE source_catalog
   SET enabled = FALSE,
       note = 'Forum/community — outside the news-portal positioning (owner decision, KLOX-152). Disabled, history kept.'
 WHERE slug = 'tabnews';
DELETE FROM sources
 WHERE catalog_id = (SELECT id FROM source_catalog WHERE slug = 'tabnews');

-- Same owner decision (2026-11-23): Hacker News is an aggregator/forum,
-- also out. Same soft treatment as TabNews.
UPDATE source_catalog
   SET enabled = FALSE,
       note = 'Aggregator/forum — outside the news-portal positioning (owner decision, KLOX-152). Disabled, history kept.'
 WHERE slug = 'hacker-news';
DELETE FROM sources
 WHERE catalog_id = (SELECT id FROM source_catalog WHERE slug = 'hacker-news');
