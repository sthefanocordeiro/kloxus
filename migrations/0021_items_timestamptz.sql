-- KLOX-223 (KLOX-189 A3): items timestamps as real timestamptz columns.
--
-- published_at/fetched_at stay TEXT (additive + reversible — nothing
-- that reads them breaks), but every ordering/range read moves to the
-- new *_ts columns: safe_ts() is STABLE, so filters like
-- COALESCE(safe_ts(published_at), safe_ts(fetched_at)) >= $cut ran
-- per-row and could never use an index.

-- to_utc_ts parses a stored timestamp leniently, like safe_ts, but
-- pins the canonical 'YYYY-MM-DD HH24:MI:SS' shape (and its 'T'
-- variant) to UTC explicitly instead of the session TimeZone — a plain
-- v::timestamptz cast reads no-offset input in the session TZ, which
-- silently shifts every stored timestamp when TZ <> UTC. Values that
-- carry their own offset (RFC3339) still go through the lenient cast;
-- garbage returns NULL so reads fall back to fetched_ts.
CREATE OR REPLACE FUNCTION to_utc_ts(v TEXT) RETURNS TIMESTAMPTZ AS $$
BEGIN
  IF v ~ '^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2})?(\.\d+)?$' THEN
    RETURN replace(v, 'T', ' ')::timestamp AT TIME ZONE 'UTC';
  END IF;
  RETURN v::timestamptz;
EXCEPTION WHEN OTHERS THEN
  RETURN NULL;
END;
$$ LANGUAGE plpgsql STABLE STRICT;

ALTER TABLE items
  ADD COLUMN IF NOT EXISTS published_ts TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS fetched_ts  TIMESTAMPTZ;

-- The trigger keeps *_ts in sync on every write path — Go inserts,
-- raw test/seed INSERTs, and SaveDJItems' DO UPDATE all fire it, so no
-- caller can forget to maintain the columns. UPDATE OF limits firing
-- to the two columns: bulk sweeps like PurgeOldBodies (content_text
-- only) skip it entirely.
CREATE OR REPLACE FUNCTION items_ts_sync() RETURNS TRIGGER AS $$
BEGIN
  NEW.published_ts := to_utc_ts(NEW.published_at);
  NEW.fetched_ts := to_utc_ts(NEW.fetched_at);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_items_ts ON items;
CREATE TRIGGER trg_items_ts
  BEFORE INSERT OR UPDATE OF published_at, fetched_at ON items
  FOR EACH ROW EXECUTE FUNCTION items_ts_sync();

-- Backfill rows written before the columns existed. to_utc_ts never
-- raises (garbage -> NULL), so the sweep cannot fail on bad data.
UPDATE items SET
  published_ts = to_utc_ts(published_at),
  fetched_ts   = to_utc_ts(fetched_at);

-- Normalize the stored published_at text to RFC3339 UTC — the one
-- format the feed producers already emit — so the TEXT column becomes
-- uniform (dedupe equality + any lexical read stays consistent).
-- Unparseable values keep their raw text; they never parsed anyway.
UPDATE items SET published_at =
  to_char(published_ts AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
WHERE published_ts IS NOT NULL
  AND published_at <>
      to_char(published_ts AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"');

-- Same normalization for the API-echoed scheduled send time.
UPDATE issues SET scheduled_at =
  to_char(to_utc_ts(scheduled_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
WHERE to_utc_ts(scheduled_at) IS NOT NULL;

-- The effective-time expression the candidate window, sweeps and
-- ordering all share.
CREATE INDEX IF NOT EXISTS idx_items_eff_ts
  ON items ((COALESCE(published_ts, fetched_ts)));
CREATE INDEX IF NOT EXISTS idx_items_fetched_ts
  ON items (fetched_ts);

-- safe_ts has no remaining call sites: every read moved to the *_ts
-- columns and the write path parses through to_utc_ts. Dropping it
-- keeps a future query from quietly reintroducing a per-row parse.
DROP FUNCTION IF EXISTS safe_ts(TEXT);
