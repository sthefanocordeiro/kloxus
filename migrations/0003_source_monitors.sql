ALTER TABLE sources ADD COLUMN monitor_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE sources ADD COLUMN monitor_id       TEXT NOT NULL DEFAULT '';
ALTER TABLE sources ADD COLUMN monitor_status   TEXT NOT NULL DEFAULT '';
