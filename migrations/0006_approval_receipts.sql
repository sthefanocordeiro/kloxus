-- Approval receipts: one row per issue approval, kept for the audit
-- trail. Deleted when the issue reverts to drafted (any post-approval
-- edit), so a re-approval records a fresh receipt.
CREATE TABLE IF NOT EXISTS approval_receipts (
    issue_id         INTEGER PRIMARY KEY REFERENCES issues(id) ON DELETE CASCADE,
    number           INTEGER NOT NULL,
    document_version TEXT NOT NULL,
    approver         TEXT NOT NULL,
    checks_json      TEXT NOT NULL DEFAULT '[]',
    approved_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
