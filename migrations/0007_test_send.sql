-- KLOX-53: a test send must not move the issue to `sent` — it stamps
-- test_sent_at instead, so the after-sending screen and the public
-- page can tell a test went out while the issue stays editable.
ALTER TABLE issues
  ADD COLUMN test_sent_at TEXT NOT NULL DEFAULT '';
