-- KLOX-65 custom section builder: an edition in progress snapshots the
-- publication's section definitions at run time, so renames, reorders
-- and deletes applied in the builder only take effect on the next
-- edition. Issues written before this column decode to an empty list
-- and fall back to the publication's current sections.
ALTER TABLE issues
  ADD COLUMN sections_json TEXT NOT NULL DEFAULT '[]';
