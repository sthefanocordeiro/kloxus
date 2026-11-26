-- KLOX-184: a pauta's name must survive the deck rebuild. Stories are
-- recreated from scratch every run (D13) and issue_items.story_id is
-- deliberately left dangling, so a list title read off the stories
-- row dies at the next build — snapshot it on the item at lineup
-- time instead.
ALTER TABLE issue_items ADD COLUMN title TEXT NOT NULL DEFAULT '';

-- Backfill: the live story when it still exists, else the dossier's
-- storylist-picked title (dossiers persist across story rebuilds).
UPDATE issue_items ii SET title = s.title
FROM stories s
WHERE s.workspace_id = ii.workspace_id AND s.id = ii.story_id
  AND ii.title = '';

UPDATE issue_items ii SET title = t.title
FROM dossiers d,
     LATERAL (SELECT NULLIF(d.json, '')::jsonb ->> 'title' AS title) t
WHERE d.workspace_id = ii.workspace_id AND d.issue_item_id = ii.id
  AND ii.title = '' AND COALESCE(t.title, '') <> '';
