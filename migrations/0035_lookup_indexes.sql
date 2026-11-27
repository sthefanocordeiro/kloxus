-- KLOX-263 (DBA review KLOX-189, M4): indexes for lookup paths that are
-- not covered by left-prefix primary keys or tenant-only indexes.
CREATE INDEX IF NOT EXISTS idx_story_items_item ON story_items(item_id);
CREATE INDEX IF NOT EXISTS idx_outbox_provider_id ON outbox_emails(provider_id);
CREATE INDEX IF NOT EXISTS idx_decisions_issue_item ON decisions(issue_item_id);
