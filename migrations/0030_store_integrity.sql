-- KLOX-195 (DBA review KLOX-189, A1/A4).
-- A4: stale-job detection must measure from the claim, not from enqueue.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS claimed_at TEXT NOT NULL DEFAULT '';

-- A1: renditions and issue_exports had no FK to issues, so deleting a
-- publication left them orphaned silently. Purge existing orphans, then
-- constrain. ON DELETE CASCADE keeps any issue delete path safe.
DELETE FROM renditions r WHERE NOT EXISTS (SELECT 1 FROM issues i WHERE i.id = r.issue_id);
DELETE FROM issue_exports e WHERE NOT EXISTS (SELECT 1 FROM issues i WHERE i.id = e.issue_id);
ALTER TABLE renditions
  ADD CONSTRAINT renditions_issue_fk FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE;
ALTER TABLE issue_exports
  ADD CONSTRAINT issue_exports_issue_fk FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE;
