package store

// Global ingestion repositories (D29). Unlike the DB methods these run
// on Pool: the catalog and the shared items belong to no workspace —
// every tenant reads them through its own source selection.
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// CatalogSource is one row of the global source catalog.
type CatalogSource struct {
	ID            int64    `json:"id"`
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"` // rss | api
	URL           string   `json:"url"`
	FeedURL       string   `json:"feed_url"`
	Policy        string   `json:"policy"` // launch | signal | hold | blocked
	Domain        string   `json:"domain"`
	CrawlDelayMS  int      `json:"crawl_delay_ms"`
	PollMinutes   int      `json:"poll_minutes"`
	BodyMode      string   `json:"body_mode"` // full | quotes | links
	CreditAuthor  bool     `json:"credit_author"`
	StripCSS      []string `json:"strip_css"`
	Note          string   `json:"note"`
	Enabled       bool     `json:"enabled"`
	Suspended     bool     `json:"suspended"`
	SuspendReason string   `json:"suspend_reason"`
	ETag          string   `json:"etag"`
	LastModified  string   `json:"last_modified"`
	LastFetchAt   string   `json:"last_fetch_at"`
	LastStatus    string   `json:"last_status"`
	RobotsAt      string   `json:"robots_at"`
	APIDay        string   `json:"api_day"`
	APICount      int      `json:"api_count"`
}

// Fetchable reports whether the scheduler may request this source at
// all. hold and blocked are never fetched — this is the single policy
// gate every fetch path goes through.
func (c CatalogSource) Fetchable() bool {
	return c.Enabled && !c.Suspended &&
		(c.Policy == "launch" || c.Policy == "signal")
}

// catalogCols mirrors scanCatalog row order.
const catalogCols = `id, slug, name, kind, url, feed_url, policy, domain, crawl_delay_ms,
	poll_minutes, body_mode, credit_author, strip_css, note, enabled, suspended,
	suspend_reason, etag, last_modified, last_fetch_at, last_status, robots_at,
	api_day, api_count`

func scanCatalog(row interface{ Scan(...any) error }) (*CatalogSource, error) {
	var c CatalogSource
	var strip, lastFetch, lastStatus, robotsAt sql.NullString
	err := row.Scan(&c.ID, &c.Slug, &c.Name, &c.Kind, &c.URL, &c.FeedURL,
		&c.Policy, &c.Domain, &c.CrawlDelayMS, &c.PollMinutes, &c.BodyMode,
		&c.CreditAuthor, &strip, &c.Note, &c.Enabled, &c.Suspended,
		&c.SuspendReason, &c.ETag, &c.LastModified, &lastFetch, &lastStatus,
		&robotsAt, &c.APIDay, &c.APICount)
	if err != nil {
		return nil, err
	}
	if strip.Valid && strip.String != "" {
		c.StripCSS = strings.Split(strip.String, ",")
	}
	c.LastFetchAt = lastFetch.String
	c.LastStatus = lastStatus.String
	c.RobotsAt = robotsAt.String
	return &c, nil
}

// Catalog returns every source in declaration order.
func (p *Pool) Catalog(ctx context.Context) ([]CatalogSource, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+catalogCols+` FROM source_catalog ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogSource
	for rows.Next() {
		c, err := scanCatalog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// CatalogEntry resolves one catalog row by slug or numeric id.
func (p *Pool) CatalogEntry(ctx context.Context, ref string) (*CatalogSource, error) {
	var id int64
	if _, err := fmt.Sscan(ref, &id); err == nil {
		return scanCatalog(p.db.QueryRowContext(ctx,
			`SELECT `+catalogCols+` FROM source_catalog WHERE id = $1`, id))
	}
	if c, err := scanCatalog(p.db.QueryRowContext(ctx,
		`SELECT `+catalogCols+` FROM source_catalog WHERE slug = $1`, ref)); err == nil {
		return c, nil
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	// Last resort: match the site or feed URL (the UI addresses catalog
	// entries by URL, like fixture sources).
	return scanCatalog(p.db.QueryRowContext(ctx,
		`SELECT `+catalogCols+` FROM source_catalog WHERE url = $1 OR feed_url = $1`, ref))
}

// CatalogDue reports whether the source is fetchable and its poll
// interval has elapsed since last_fetch_at.
func (c CatalogSource) Due(now time.Time) bool {
	if !c.Fetchable() {
		return false
	}
	if c.LastFetchAt == "" {
		return true
	}
	last, err := time.ParseInLocation(tsLayout, c.LastFetchAt, time.UTC)
	if err != nil {
		return true
	}
	ivl := c.PollMinutes
	if ivl <= 0 {
		ivl = 45
	}
	return last.Add(time.Duration(ivl) * time.Minute).Before(now)
}

// CatalogMarkFetched records a fetch outcome on the catalog row.
func (p *Pool) CatalogMarkFetched(ctx context.Context, id int64, etag, lastModified, status string) error {
	_, err := p.db.ExecContext(ctx, `UPDATE source_catalog SET
		last_fetch_at = $1, last_status = $2, etag = $3, last_modified = $4
		WHERE id = $5`,
		NowUTC(), status, etag, lastModified, id)
	return err
}

// CatalogSetSuspended flips the self-suspension flag (robots denial).
func (p *Pool) CatalogSetSuspended(ctx context.Context, id int64, suspended bool, reason string) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE source_catalog SET suspended = $1, suspend_reason = $2 WHERE id = $3`,
		suspended, reason, id)
	return err
}

// CatalogTouchRobots stamps the daily robots re-check.
func (p *Pool) CatalogTouchRobots(ctx context.Context, id int64) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE source_catalog SET robots_at = $1 WHERE id = $2`, NowUTC(), id)
	return err
}

// CatalogAPICount returns how many API requests the source has used on
// the current UTC day (the counter resets when the day rolls over).
func (p *Pool) CatalogAPICount(ctx context.Context, id int64, day string) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT CASE WHEN api_day = $1 THEN api_count ELSE 0 END FROM source_catalog WHERE id = $2`,
		day, id).Scan(&n)
	return n, err
}

// CatalogAPIIncr counts one API request on the current UTC day.
func (p *Pool) CatalogAPIIncr(ctx context.Context, id int64, day string) error {
	_, err := p.db.ExecContext(ctx, `UPDATE source_catalog SET
		api_day = $1, api_count = CASE WHEN api_day = $1 THEN api_count + 1 ELSE 1 END
		WHERE id = $2`, day, id)
	return err
}

// CatalogItem is what the ingest scheduler stores per fetched entry.
type CatalogItem struct {
	ExternalID    string
	URL           string
	CanonicalURL  string
	Title         string
	Author        string
	PublishedAt   string
	ContentText   string
	Kind          string // article | signal
	Meta          map[string]any
	BodyTruncated bool
}

// SaveCatalogItems upserts fetched items into the shared items table.
// fetched_at is kept from the first insert — a re-poll must not make an
// old story look new to workspace runs.
func (p *Pool) SaveCatalogItems(ctx context.Context, catalogID int64, raw []CatalogItem) (int, error) {
	added := 0
	for _, r := range raw {
		kind := r.Kind
		if kind == "" {
			kind = "article"
		}
		if IsPromoTitle(r.Title) {
			kind = "promo"
		}
		meta := r.Meta
		if meta == nil {
			meta = map[string]any{}
		}
		data, _ := json.Marshal(meta)
		res, err := p.db.ExecContext(ctx, `INSERT INTO items
			(source_id, catalog_id, external_id, url, canonical_url, title, author, published_at, content_text, kind, meta_json, body_truncated)
			VALUES (NULL,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (catalog_id, external_id) WHERE catalog_id IS NOT NULL DO NOTHING`,
			catalogID, r.ExternalID, r.URL, r.CanonicalURL, r.Title, r.Author, NormalizeTS(r.PublishedAt), r.ContentText, kind, string(data), r.BodyTruncated)
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	return added, nil
}

// MergeDJDetail preserves a stored detail fetch when a newer list
// payload arrives without one: the aggregate body and its detail-only
// meta keys survive until the story's updated_at moves — which marks
// the revision stale and re-queues a detail fetch. A permanently
// failed fetch (detail_fetched='gone') survives the same way so a dead
// slug is not re-requested on every poll; a revision re-queues it.
// Exported so the ingest test fake mirrors the store's merge
// semantics.
func MergeDJDetail(storedMeta map[string]any, storedText string, in *CatalogItem) {
	idf, _ := in.Meta["detail_fetched"].(bool)
	if idf {
		return // incoming IS a detail payload — it wins
	}
	stored := storedMeta["detail_fetched"]
	hasDetail, _ := stored.(bool)
	gone, _ := stored.(string)
	if !hasDetail && gone != "gone" {
		return // nothing stored to preserve
	}
	// A strictly-newer updated_at on the list side means the story was
	// revised after our detail fetch — drop it and re-queue. The stored
	// detail's timestamp is often newer than the list snapshot it was
	// fetched under, so compare as time, not equality.
	st, _ := storedMeta["updated_at"].(string)
	it, _ := in.Meta["updated_at"].(string)
	if st != "" && it != "" {
		t1, e1 := time.Parse(time.RFC3339, it)
		t0, e0 := time.Parse(time.RFC3339, st)
		if e1 == nil && e0 == nil && t1.After(t0) {
			return // revised — stored detail is stale
		}
	}
	if gone == "gone" {
		in.Meta["detail_fetched"] = "gone"
		return
	}
	in.ContentText = storedText
	// Detail-sourced fields are preserved as a set — updated_at and
	// description included — or every list refresh would churn the row
	// between the two payload shapes.
	for _, k := range []string{
		"detail_fetched", "updated_at", "description",
		"bullets", "cited_urls", "cited_outlets",
	} {
		if v, ok := storedMeta[k]; ok {
			in.Meta[k] = v
		}
	}
}

// SaveDJItems upserts Daily Journal stories (KLOX-93). Unlike
// SaveCatalogItems — insert-only, for feeds — a stored row refreshes
// when the incoming payload actually changed (DJ revises stories and
// the list ordering tracks the latest source activity), with one
// exception: a detail-fetched aggregate body survives list-shape
// refreshes that carry only the blurb (MergeDJDetail). New and changed
// ids come back so the caller can log them; changed rows also lose
// their deterministic semantic rows — the embedding and the local
// classification no longer describe the text — and the sweep
// recomputes them on the next pass. Idempotent: replaying the same
// payload changes nothing.
func (p *Pool) SaveDJItems(ctx context.Context, catalogID int64, raw []CatalogItem) (added, changed int, err error) {
	// Load the stored rows' meta + body once so list-only refreshes
	// don't clobber a detail fetch (MergeDJDetail).
	extIDs := make([]string, 0, len(raw))
	for _, r := range raw {
		extIDs = append(extIDs, r.ExternalID)
	}
	stored := map[string]struct {
		meta map[string]any
		text string
	}{}
	if len(extIDs) > 0 {
		srows, err := p.db.QueryContext(ctx,
			`SELECT external_id, content_text, meta_json FROM items
			 WHERE catalog_id = $1 AND external_id = ANY($2)`, catalogID, extIDs)
		if err != nil {
			return 0, 0, err
		}
		defer srows.Close()
		for srows.Next() {
			var ext, text, mj string
			if err := srows.Scan(&ext, &text, &mj); err != nil {
				return 0, 0, err
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(mj), &m); err != nil {
				slog.Warn("catalog item: malformed meta json", "err", err)
			}
			stored[ext] = struct {
				meta map[string]any
				text string
			}{m, text}
		}
		if err := srows.Err(); err != nil {
			return 0, 0, err
		}
	}
	var changedIDs []int64
	for _, r := range raw {
		if sm, ok := stored[r.ExternalID]; ok {
			MergeDJDetail(sm.meta, sm.text, &r)
		}
		kind := r.Kind
		if kind == "" {
			kind = "article"
		}
		if IsPromoTitle(r.Title) {
			kind = "promo"
		}
		meta := r.Meta
		if meta == nil {
			meta = map[string]any{}
		}
		data, _ := json.Marshal(meta)
		var id int64
		var inserted bool
		err := p.db.QueryRowContext(ctx, `INSERT INTO items
			(source_id, catalog_id, external_id, url, canonical_url, title, author, published_at, content_text, kind, meta_json, body_truncated)
			VALUES (NULL,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (catalog_id, external_id) WHERE catalog_id IS NOT NULL
			DO UPDATE SET title = EXCLUDED.title, published_at = EXCLUDED.published_at,
				content_text = EXCLUDED.content_text, kind = EXCLUDED.kind,
				meta_json = EXCLUDED.meta_json, body_truncated = EXCLUDED.body_truncated
			WHERE items.title IS DISTINCT FROM EXCLUDED.title
			   OR items.published_at IS DISTINCT FROM EXCLUDED.published_at
			   OR items.content_text IS DISTINCT FROM EXCLUDED.content_text
			   OR items.meta_json IS DISTINCT FROM EXCLUDED.meta_json
			RETURNING id, (xmax = 0)`,
			catalogID, r.ExternalID, r.URL, r.CanonicalURL, r.Title, r.Author,
			NormalizeTS(r.PublishedAt), r.ContentText, kind, string(data), r.BodyTruncated).
			Scan(&id, &inserted)
		if err == sql.ErrNoRows {
			continue // unchanged — the DO UPDATE WHERE skipped it
		}
		if err != nil {
			return added, changed, err
		}
		if inserted {
			added++
		} else {
			changed++
			changedIDs = append(changedIDs, id)
		}
	}
	if len(changedIDs) > 0 {
		// Stale deterministic semantic rows are dropped, never
		// rewritten in place — the sweep recomputes them next pass.
		// AI enrichment rows (model not 'local-%') survive.
		if _, err := p.db.ExecContext(ctx,
			`DELETE FROM item_embedding WHERE item_id = ANY($1)`, changedIDs); err != nil {
			return added, changed, err
		}
		if _, err := p.db.ExecContext(ctx,
			`DELETE FROM item_enrichment WHERE item_id = ANY($1) AND model LIKE 'local-%'`, changedIDs); err != nil {
			return added, changed, err
		}
	}
	return added, changed, nil
}

// DJPendingDetails lists the catalog source's stored items that still
// lack the detail-fetched body (meta.detail_fetched neither 'true' nor
// 'gone' — the permanent-failure mark), newest first — the Daily
// Journal detail backfill works this queue down inside the daily
// request budget.
func (p *Pool) DJPendingDetails(ctx context.Context, catalogID int64, limit int) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT i.meta_json::jsonb ->> 'slug'
		FROM items i
		WHERE i.catalog_id = $1
		  AND COALESCE(i.meta_json::jsonb ->> 'detail_fetched', '') NOT IN ('true', 'gone')
		ORDER BY COALESCE(i.published_ts, i.fetched_ts) DESC NULLS LAST,
		         i.id DESC
		LIMIT $2`, catalogID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

// DJMarkDetailFailed takes a slug out of the pending-details queue
// after a permanent (4xx) fetch failure — meta.detail_fetched='gone'
// no longer matches the pending predicate. MergeDJDetail carries the
// mark across list refreshes until the story is revised.
func (p *Pool) DJMarkDetailFailed(ctx context.Context, catalogID int64, slug string) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE items
		SET meta_json = (meta_json::jsonb || '{"detail_fetched":"gone"}'::jsonb)::text
		WHERE catalog_id = $1 AND meta_json::jsonb ->> 'slug' = $2`,
		catalogID, slug)
	return err
}

// IsBlockedDomain reports whether the catalog marks a domain as
// blocked — a signal may cite it for display, but it is never fetched.
func (p *Pool) IsBlockedDomain(ctx context.Context, domain string) (bool, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM source_catalog WHERE policy = 'blocked' AND domain = $1`,
		strings.ToLower(strings.TrimSpace(domain))).Scan(&n)
	return n > 0, err
}

// LogFetch appends one fetch outcome to the per-source log.
func (p *Pool) LogFetch(ctx context.Context, catalogID int64, status string, httpStatus, items, newItems int, detail string) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO fetch_log
		(catalog_id, status, http_status, items, new_items, detail) VALUES ($1,$2,$3,$4,$5,$6)`,
		catalogID, status, httpStatus, items, newItems, detail)
	return err
}

// FetchLogRow is one fetch_log entry.
type FetchLogRow struct {
	ID         int64  `json:"id"`
	CatalogID  int64  `json:"catalog_id"`
	FetchedAt  string `json:"fetched_at"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status"`
	Items      int    `json:"items"`
	NewItems   int    `json:"new_items"`
	Detail     string `json:"detail"`
}

// FetchLog returns a source's recent fetch outcomes, newest first.
func (p *Pool) FetchLog(ctx context.Context, catalogID int64, limit int) ([]FetchLogRow, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, catalog_id, fetched_at, status, http_status, items, new_items, detail
		 FROM fetch_log WHERE catalog_id = $1 ORDER BY id DESC LIMIT $2`, catalogID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FetchLogRow
	for rows.Next() {
		var f FetchLogRow
		if err := rows.Scan(&f.ID, &f.CatalogID, &f.FetchedAt, &f.Status, &f.HTTPStatus, &f.Items, &f.NewItems, &f.Detail); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CatalogItemsPreview returns a source's newest stored items.
func (p *Pool) CatalogItemsPreview(ctx context.Context, catalogID int64, limit int) ([]Item, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT i.id, i.external_id, i.url, i.canonical_url, i.title, i.author,
		       i.published_at, i.kind, c.name, i.content_text
		FROM items i JOIN source_catalog c ON c.id = i.catalog_id
		WHERE i.catalog_id = $1 ORDER BY i.id DESC LIMIT $2`, catalogID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var canon, title, author, published sql.NullString
		if err := rows.Scan(&it.ID, &it.ExternalID, &it.URL, &canon, &title,
			&author, &published, &it.Kind, &it.SourceName, &it.ContentText); err != nil {
			return nil, err
		}
		it.CanonicalURL = canon.String
		it.Title = title.String
		it.Author = author.String
		it.PublishedAt = published.String
		out = append(out, it)
	}
	return out, rows.Err()
}

// CatalogItemsCount totals a source's stored items.
func (p *Pool) CatalogItemsCount(ctx context.Context, catalogID int64) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM items WHERE catalog_id = $1`, catalogID).Scan(&n)
	return n, err
}

// CatalogItemsPerWeek counts a source's stored items over 7 days.
func (p *Pool) CatalogItemsPerWeek(ctx context.Context, catalogID int64) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM items WHERE catalog_id = $1 AND fetched_ts > $2`,
		catalogID, time.Now().Add(-7*24*time.Hour).UTC()).Scan(&n)
	return n, err
}

// CatalogItemsStats reports, for the catalog sources a publication
// picked, how many shared items the run's candidate set would see and
// how many arrived after the last issue — the collect step's "new
// since last run" signal for globally-ingested sources. The same
// bounds as ItemsForPublication apply: no promo rows, the 7-day window
// (published date, fetched_at fallback) and at most 60 per source.
func (db *DB) CatalogItemsStats(ctx context.Context, pubID int64) (total, fresh int, err error) {
	err = db.QueryRowContext(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE i.fetched_ts > COALESCE(
		           to_utc_ts((SELECT max(created_at) FROM issues
		            WHERE workspace_id = $1 AND publication_id = $2)),
		            '1970-01-01'::timestamptz))
		FROM (
		  SELECT i.fetched_ts,
		    ROW_NUMBER() OVER (
		      PARTITION BY 'c' || i.catalog_id
		      ORDER BY COALESCE(i.published_ts, i.fetched_ts) DESC NULLS LAST,
		               i.id DESC
		    ) AS rn,
		    COALESCE(i.published_ts, i.fetched_ts) AS eff
		  FROM items i
		  WHERE i.kind <> 'promo'
		    AND i.catalog_id IN (SELECT catalog_id FROM sources
		                         WHERE workspace_id = $1 AND publication_id = $2
		                           AND catalog_id IS NOT NULL)
		) i
		WHERE i.rn <= 60 AND (i.eff IS NULL OR i.eff >= now() - interval '7 days')`,
		db.ws, pubID).Scan(&total, &fresh)
	return total, fresh, err
}

// PurgeOldBodies clears stored article bodies older than the retention
// window (30 days, D28's 500 MB bound). Title, link and metadata stay.
func (p *Pool) PurgeOldBodies(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx,
		`UPDATE items SET content_text = '' WHERE fetched_ts < $1 AND content_text <> ''`,
		olderThan.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
