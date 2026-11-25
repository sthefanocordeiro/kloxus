-- KLOX-220 (DBA review KLOX-189, M3): integrity constraints the newer
-- tables shipped without. Each constrain is preceded by a backfill that
-- removes or repoints the rows it would reject.

-- media.publication_id and standard_messages.publication_id had no FK:
-- a publication delete only worked because DeletePublication's manual
-- cascade happens to list them. Purge orphans, then constrain; CASCADE
-- keeps any publication delete path safe.
DELETE FROM media m WHERE NOT EXISTS (SELECT 1 FROM publications p WHERE p.id = m.publication_id);
DELETE FROM standard_messages s WHERE NOT EXISTS (SELECT 1 FROM publications p WHERE p.id = s.publication_id);
ALTER TABLE media
  ADD CONSTRAINT media_publication_fk FOREIGN KEY (publication_id) REFERENCES publications(id) ON DELETE CASCADE;
ALTER TABLE standard_messages
  ADD CONSTRAINT standard_messages_publication_fk FOREIGN KEY (publication_id) REFERENCES publications(id) ON DELETE CASCADE;

-- subscriptions: ActivateSubscriptionUntil did SELECT then INSERT
-- outside a transaction, so concurrent activations could write two rows
-- per reader and token lookups returned an arbitrary one. Keep the best
-- row per reader (active beats inactive, then latest paid_until, then
-- the newest row), then enforce one row per reader.
WITH ranked AS (
  SELECT id,
         ROW_NUMBER() OVER (
           PARTITION BY workspace_id, reader_id
           ORDER BY (status = 'active') DESC, paid_until DESC, id DESC
         ) AS rn
  FROM subscriptions
)
DELETE FROM subscriptions WHERE id IN (SELECT id FROM ranked WHERE rn > 1);
ALTER TABLE subscriptions
  ADD CONSTRAINT subscriptions_reader_key UNIQUE (workspace_id, reader_id);

-- issue_items: one row per story per issue. RankedCandidates already
-- defends against duplicates with DISTINCT ON (story_id) ordered by id,
-- so the lowest id is the surviving row. Decisions keep their history by
-- moving to the survivor; the survivor's dossier wins, and a
-- duplicate's dossier is kept only when the survivor has none.
WITH ranked AS (
  SELECT id, issue_id,
         ROW_NUMBER() OVER (PARTITION BY issue_id, story_id ORDER BY id) AS rn,
         MIN(id) OVER (PARTITION BY issue_id, story_id) AS keep_id
  FROM issue_items
),
upd_dec AS (
  UPDATE decisions d SET issue_item_id = r.keep_id
  FROM ranked r
  WHERE d.issue_item_id = r.id AND r.rn > 1
  RETURNING d.id
),
del_dos AS (
  DELETE FROM dossiers d
  USING ranked r
  WHERE d.issue_item_id = r.id AND r.rn > 1
    AND (r.rn > 2 OR EXISTS (SELECT 1 FROM dossiers k WHERE k.issue_item_id = r.keep_id))
  RETURNING d.id
),
upd_dos AS (
  UPDATE dossiers d SET issue_item_id = r.keep_id
  FROM ranked r
  WHERE d.issue_item_id = r.id AND r.rn = 2
    AND NOT EXISTS (SELECT 1 FROM dossiers k WHERE k.issue_item_id = r.keep_id)
  RETURNING d.id
)
DELETE FROM issue_items WHERE id IN (SELECT id FROM ranked WHERE rn > 1);
ALTER TABLE issue_items
  ADD CONSTRAINT issue_items_story_key UNIQUE (issue_id, story_id);
