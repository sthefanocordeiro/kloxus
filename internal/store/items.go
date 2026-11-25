package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/sthefanocordeiro/kloxus/internal/sources"
)

// Item is a raw collected item. Catalog items (catalog_id set) are
// shared across workspaces — the global ingest scheduler fetched them
// once for everyone (D29); fixture items keep a per-workspace source.
type Item struct {
	ID            int64
	SourceID      int64
	CatalogID     int64
	SourceName    string
	SourceURL     string
	SourceType    string
	ExternalID    string
	URL           string
	CanonicalURL  string
	Title         string
	Author        string
	PublishedAt   string
	ContentText   string
	Kind          string // article | signal
	Meta          ItemMeta
	BodyTruncated bool
	FetchedAt     string
}

// ItemMeta carries signal-source metadata — the links a signal story
// cites, used to merge it onto the article it points at.
type ItemMeta struct {
	CitedURLs []string `json:"cited_urls,omitempty"`
}

// Signal reports whether the item is a link-level signal (Hacker News)
// rather than a full-text article: signals rank and attribute stories
// but never serve as citation evidence.
func (it Item) Signal() bool { return it.Kind == "signal" }

// itemCols is the shared select list for the dual-source item queries:
// a workspace's fixture sources (s) or the catalog entries (c) its
// source rows point at. Source fields coalesce s → c.
const itemCols = `i.id, i.source_id, i.catalog_id,
	COALESCE(s.name, c.name, ''), COALESCE(s.url, c.url, ''), COALESCE(s.type, 'rss'),
	i.external_id, i.url, i.canonical_url, i.title, i.author, i.published_at,
	i.content_text, i.kind, i.meta_json, i.body_truncated, i.fetched_at`

func scanItem(row interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var sourceID, catalogID sql.NullInt64
	var externalID, canon, published, author sql.NullString
	var meta string
	err := row.Scan(&it.ID, &sourceID, &catalogID, &it.SourceName, &it.SourceURL,
		&it.SourceType, &externalID, &it.URL, &canon, &it.Title, &author,
		&published, &it.ContentText, &it.Kind, &meta, &it.BodyTruncated, &it.FetchedAt)
	if err != nil {
		return nil, err
	}
	it.SourceID = sourceID.Int64
	it.CatalogID = catalogID.Int64
	it.ExternalID = externalID.String
	it.CanonicalURL = canon.String
	it.Author = author.String
	it.PublishedAt = published.String
	if meta != "" && meta != "{}" {
		json.Unmarshal([]byte(meta), &it.Meta)
	}
	return &it, nil
}

// SaveItems inserts collected items (idempotent per source+external_id).
// Workspace-source items are never promo-tagged here: the deals filter
// is a catalog-ingestion concern (D41) — these items come from the
// editor's own picks, and tagging them would change the recorded/demo
// candidate sets the deterministic replays depend on. Returns the
// number of new rows.
func (db *DB) SaveItems(ctx context.Context, sourceID int64, raw []sources.RawItem, canonical func(string) string) (int, error) {
	added := 0
	for _, r := range raw {
		res, err := db.ExecContext(ctx, `INSERT INTO items
			(workspace_id, source_id, external_id, url, canonical_url, title, author, published_at, content_text, kind)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'article')
			ON CONFLICT (source_id, external_id) DO NOTHING`,
			db.ws, sourceID, r.ExternalID, r.URL, canonical(r.URL), r.Title, r.Author, r.PublishedAt, r.ContentText)
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	return added, nil
}

// ItemsForPublication returns the run's candidate items across a
// publication's sources: the workspace's own items plus the shared
// catalog items its catalog picks have collected (D29). The candidate
// set is bounded (owner decision, KLOX-45): kind='promo' items never
// enter it; catalog items are limited to the last 7 days — published
// date first, fetched_at when the stored date is unparseable — and to
// the 60 newest per source. Per-workspace source items pass through
// unbounded by design: they come from the editor's own picks, and a
// date window or cap there would age the static demo/recorded fixtures
// out of every deterministic replay (D41).
//
// The AI enrichment adds a second, shared exclusion (KLOX-67 §7.2): an
// item the enrichment marked promotional or a non-editorial kind
// (buying guide, entertainment, sport, almanac, advertorial) never
// becomes a suggestion in any newsletter. Un-enriched items pass —
// enrichment fills in asynchronously after ingest.
func (db *DB) ItemsForPublication(ctx context.Context, pubID int64) ([]Item, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+itemCols+`
		FROM (
		  SELECT i.*,
		    ROW_NUMBER() OVER (
		      PARTITION BY COALESCE('s' || i.source_id, 'c' || i.catalog_id)
		      ORDER BY COALESCE(safe_ts(i.published_at), safe_ts(i.fetched_at)) DESC NULLS LAST,
		               i.id DESC
		    ) AS rn,
		    COALESCE(safe_ts(i.published_at), safe_ts(i.fetched_at)) AS eff
		  FROM items i
		  WHERE i.kind <> 'promo'
		    AND NOT EXISTS (
		      SELECT 1 FROM item_enrichment e
		      WHERE e.item_id = i.id
		        AND (e.promotional OR e.ai_kind IN
		             ('lista','publieditorial','entretenimento','esporte','efemeride','tutorial','review')))
		) i
		LEFT JOIN sources s ON s.id = i.source_id
		LEFT JOIN source_catalog c ON c.id = i.catalog_id
		WHERE (s.id IS NOT NULL AND s.workspace_id = $1 AND s.publication_id = $2)
		   OR (c.id IS NOT NULL AND EXISTS (
		         SELECT 1 FROM sources ws
		         WHERE ws.workspace_id = $1 AND ws.publication_id = $2
		           AND ws.catalog_id = i.catalog_id))
		  AND (i.catalog_id IS NULL
		       OR (i.rn <= 60 AND (i.eff IS NULL
		           OR i.eff >= now() - interval '7 days')))
		  -- A story used in an earlier edition must not resurface as a
		  -- suggestion in the next one (KLOX-100). Two durable trails
		  -- mark "used": the current generation's story_items →
		  -- issue_items link (this query runs before ClearStories
		  -- rebuilds the table), and any dossier source citation —
		  -- dossiers persist across story rebuilds.
		  AND NOT EXISTS (
		    SELECT 1 FROM story_items si
		    JOIN issue_items ii ON ii.story_id = si.story_id
		    JOIN issues iss ON iss.id = ii.issue_id
		    WHERE si.workspace_id = $1 AND iss.publication_id = $2
		      AND si.item_id = i.id)
		  AND NOT EXISTS (
		    SELECT 1 FROM dossiers d
		    JOIN issue_items ii ON ii.id = d.issue_item_id
		    JOIN issues iss ON iss.id = ii.issue_id
		    CROSS JOIN LATERAL jsonb_array_elements(
		      COALESCE(NULLIF(d.json,'')::jsonb -> 'sources', '[]'::jsonb)) el
		    WHERE d.workspace_id = $1 AND iss.publication_id = $2
		      AND CASE WHEN el->>'item_id' ~ '^\d+$'
		               THEN (el->>'item_id')::bigint END = i.id)
		ORDER BY i.id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

// MarkSourceFetched records a fetch outcome on a source, along with the
// outlet name when the fetch yielded one (feed channel title, page h1).
func (db *DB) MarkSourceFetched(ctx context.Context, sourceID int64, name, status string) error {
	if name != "" {
		_, err := db.ExecContext(ctx,
			`UPDATE sources SET last_fetch_at = $1, last_status = $2, name = $3 WHERE workspace_id = $4 AND id = $5`,
			NowUTC(), status, name, db.ws, sourceID)
		return err
	}
	_, err := db.ExecContext(ctx,
		`UPDATE sources SET last_fetch_at = $1, last_status = $2 WHERE workspace_id = $3 AND id = $4`,
		NowUTC(), status, db.ws, sourceID)
	return err
}

// GetItem loads one item with its source's outlet name. Shared catalog
// items (workspace_id NULL) resolve for any workspace — reader links
// cite them across tenants by design.
func (db *DB) GetItem(ctx context.Context, id int64) (*Item, error) {
	return scanItem(db.QueryRowContext(ctx, `
		SELECT `+itemCols+`
		FROM items i
		LEFT JOIN sources s ON s.id = i.source_id
		LEFT JOIN source_catalog c ON c.id = i.catalog_id
		WHERE i.id = $2 AND (i.workspace_id = $1 OR i.catalog_id IS NOT NULL)`,
		db.ws, id))
}

// GetItems loads many items by id in one query — the same row shape
// and visibility rule as GetItem (workspace items or shared catalog
// rows). The issue document's source excerpts cite tens of items; one
// round-trip replaces the per-source GetItem loop that made every
// document build N+1 against remote Postgres (KLOX-47).
func (db *DB) GetItems(ctx context.Context, ids []int64) (map[int64]*Item, error) {
	out := map[int64]*Item{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT `+itemCols+`
		FROM items i
		LEFT JOIN sources s ON s.id = i.source_id
		LEFT JOIN source_catalog c ON c.id = i.catalog_id
		WHERE i.id = ANY($2) AND (i.workspace_id = $1 OR i.catalog_id IS NOT NULL)`,
		db.ws, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out[it.ID] = it
	}
	return out, rows.Err()
}
