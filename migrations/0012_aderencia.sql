-- KLOX-94 aderência: the newsletter-level reject terms the creator
-- declares next to the editorial line ("celular, games"). Deterministic
-- ranking demotes matching stories and the auto-lineup never slots
-- them; they stay on the bench, marked — never silently dropped.
-- Per-section topics need no column: sections live in sections_json.
ALTER TABLE publications
  ADD COLUMN reject_terms TEXT NOT NULL DEFAULT '';
