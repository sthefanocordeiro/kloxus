-- KLOX-218 / KLOX-189 A3: make the effective item timestamp sargable.
--
-- items.published_at is feed-sourced (RFC3339, offsets, occasional
-- garbage); items.fetched_at is the internal 'YYYY-MM-DD HH24:MI:SS'
-- UTC shape. Every hot read orders or filters by
-- COALESCE(safe_ts(published_at), safe_ts(fetched_at)) — a STABLE
-- function evaluated per row, so no index can serve it.
--
-- This migration is additive and reversible:
--   1. items.eff_at (timestamptz) stores the same coalesce result,
--      backfilled with a tolerant cast — unparseable input yields
--      NULL eff_at, never a failed migration.
--   2. A BEFORE INSERT/UPDATE trigger keeps eff_at in sync, so no
--      write path can bypass it (direct SQL fixtures included).
--   3. idx_items_eff_at serves the range filter and the
--      DESC NULLS LAST + id DESC ordering the candidate queries use.
--   4. Stored published_at / issues.scheduled_at values are
--      normalized to RFC3339 UTC ('YYYY-MM-DD"T"HH24:MI:SS"Z'): the
--      API emits published_at verbatim and RFC3339 is unambiguous in
--      the browser. Non-empty values that don't parse are counted and
--      nulled — their eff already fell back to fetched_at, so read
--      semantics are unchanged. Empty '' stays ''.
--   5. idx_items_fetched_at serves the retention purge and the
--      fetch-window scans that compare fetched_at directly.
--
-- The TEXT columns stay — nothing is renamed or dropped here. The
-- full type swap (safe_ts → direct cast) is a later migration.

-- safe_ts reads zone-less input in the *session* TimeZone; stored
-- values are UTC by contract, and the difference shows on any
-- non-UTC session (dev boxes, misconfigured pools). safe_ts_utc pins
-- the two zone-less shapes we actually store — the now_utc()
-- 'YYYY-MM-DD HH24:MI:SS' and the bare ISO date/'T' time — to +00
-- before casting, so the materialized instant is session-independent.
-- Anything else falls through to the lenient ::timestamptz cast,
-- whose explicit-offset inputs are already session-independent.
CREATE OR REPLACE FUNCTION safe_ts_utc(v TEXT) RETURNS TIMESTAMPTZ AS $$
BEGIN
  IF v ~ '^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2}(\.\d+)?)?$' THEN
    RETURN (v || '+00')::timestamptz;
  END IF;
  IF v ~ '^\d{4}-\d{2}-\d{2}$' THEN
    RETURN (v || ' 00:00:00+00')::timestamptz;
  END IF;
  RETURN v::timestamptz;
EXCEPTION WHEN OTHERS THEN
  RETURN NULL;
END;
$$ LANGUAGE plpgsql STABLE STRICT;

-- Extra determinism for this upgrade: anything safe_ts_utc still
-- defers to the bare cast (zone-less exotic strings) is read as UTC.
SET LOCAL TIME ZONE 'UTC';

ALTER TABLE items ADD COLUMN IF NOT EXISTS eff_at TIMESTAMPTZ;

-- Normalize stored published_at / issues.scheduled_at while the
-- values still round-trip through the tolerant cast: parseable values
-- become RFC3339 UTC; non-empty unparseable values are counted and
-- nulled (their eff already fell back to fetched_at).
DO $$
DECLARE
  bad_pub int;
  bad_sched int;
BEGIN
  SELECT count(*) INTO bad_pub FROM items
   WHERE published_at IS NOT NULL AND published_at <> ''
     AND safe_ts_utc(published_at) IS NULL;
  SELECT count(*) INTO bad_sched FROM issues
   WHERE scheduled_at <> '' AND safe_ts_utc(scheduled_at) IS NULL;
  IF bad_pub > 0 OR bad_sched > 0 THEN
    RAISE NOTICE 'KLOX-218: unparseable timestamps nulled — items.published_at: %, issues.scheduled_at: %',
      bad_pub, bad_sched;
  END IF;
END $$;

UPDATE items
   SET published_at = to_char(safe_ts_utc(published_at) AT TIME ZONE 'UTC',
                              'YYYY-MM-DD"T"HH24:MI:SS"Z"')
 WHERE published_at IS NOT NULL AND published_at <> ''
   AND safe_ts_utc(published_at) IS NOT NULL;

UPDATE items SET published_at = NULL
 WHERE published_at IS NOT NULL AND published_at <> ''
   AND safe_ts_utc(published_at) IS NULL;

UPDATE issues
   SET scheduled_at = to_char(safe_ts_utc(scheduled_at) AT TIME ZONE 'UTC',
                              'YYYY-MM-DD"T"HH24:MI:SS"Z"')
 WHERE scheduled_at <> '' AND safe_ts_utc(scheduled_at) IS NOT NULL;

UPDATE issues SET scheduled_at = ''
 WHERE scheduled_at <> '' AND safe_ts_utc(scheduled_at) IS NULL;

-- Backfill. items stays small (retention purge bounds bodies; rows are
-- metadata), so one pass inside the migration tx holds no long lock.
UPDATE items
   SET eff_at = COALESCE(safe_ts_utc(published_at), safe_ts_utc(fetched_at));

-- Keep eff_at correct on every later write: a repoll that revises
-- published_at, a first insert whose fetched_at default just landed.
CREATE OR REPLACE FUNCTION items_eff_at() RETURNS trigger AS $$
BEGIN
  NEW.eff_at := COALESCE(safe_ts_utc(NEW.published_at), safe_ts_utc(NEW.fetched_at));
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS items_eff_at_trg ON items;
CREATE TRIGGER items_eff_at_trg
  BEFORE INSERT OR UPDATE OF published_at, fetched_at ON items
  FOR EACH ROW EXECUTE FUNCTION items_eff_at();

CREATE INDEX IF NOT EXISTS idx_items_eff_at
    ON items (eff_at DESC NULLS LAST, id DESC);
CREATE INDEX IF NOT EXISTS idx_items_fetched_at
    ON items (fetched_at DESC);
