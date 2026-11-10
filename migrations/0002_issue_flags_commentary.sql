ALTER TABLE issues ADD COLUMN flags_json      TEXT NOT NULL DEFAULT '[]';
ALTER TABLE issues ADD COLUMN commentary_json TEXT NOT NULL DEFAULT '{}';

CREATE UNIQUE INDEX idx_dossiers_issue_item ON dossiers(issue_item_id);
