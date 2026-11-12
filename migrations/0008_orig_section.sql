-- the board mutates section when the creator moves a story; keep
-- the Phase A pick's original section so demo-mode canonical replay
-- input stays fixture-stable
ALTER TABLE issue_items ADD COLUMN orig_section TEXT NOT NULL DEFAULT '';
UPDATE issue_items SET orig_section = section WHERE orig_section = '';
