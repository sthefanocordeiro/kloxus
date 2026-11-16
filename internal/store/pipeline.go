package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

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

// Story is a deduplicated group of items covering the same story.
type Story struct {
	ID            int64
	PublicationID int64
	Title         string
	TriageScore   float64
	TriageReason  string
	// DedupeKey is the canonical URL shared by every member item; empty
	// when the cluster merged on near-identical text (SimHash) alone.
	DedupeKey string
	Items     []Item
}

// Outlets returns the distinct source URLs covering this story.
func (s Story) Outlets() []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range s.Items {
		if !seen[it.SourceURL] {
			seen[it.SourceURL] = true
			out = append(out, it.SourceURL)
		}
	}
	return out
}

// ClearStories removes a publication's stories (for a fresh run).
// Old issue_items keep a dangling story_id by design: their rendered
// content lives in dossiers and the issue body, not in the stories table
// — which is why issue_items.story_id carries no foreign key.
func (db *DB) ClearStories(ctx context.Context, pubID int64) error {
	if _, err := db.ExecContext(ctx,
		`DELETE FROM story_items WHERE workspace_id = $1 AND story_id IN
			(SELECT id FROM stories WHERE workspace_id = $1 AND publication_id = $2)`, db.ws, pubID); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx,
		`DELETE FROM stories WHERE workspace_id = $1 AND publication_id = $2`, db.ws, pubID)
	return err
}

// CreateStory inserts a story group. dedupeKey is the canonical URL the
// cluster formed around, or "" for a text-similarity cluster.
func (db *DB) CreateStory(ctx context.Context, pubID int64, title string, simhash int64, dedupeKey string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO stories (workspace_id, publication_id, title, simhash, dedupe_key) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		db.ws, pubID, title, simhash, dedupeKey).Scan(&id)
	return id, err
}

// AddStoryItem links an item to a story.
func (db *DB) AddStoryItem(ctx context.Context, storyID, itemID int64) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO story_items (workspace_id, story_id, item_id) VALUES ($1,$2,$3)
		 ON CONFLICT (story_id, item_id) DO NOTHING`, db.ws, storyID, itemID)
	return err
}

// StoryDraft is one dedupe cluster ready to persist: display title,
// SimHash of the representative member, the shared dedupe key and the
// member item ids.
type StoryDraft struct {
	Title     string
	Simhash   int64
	DedupeKey string
	ItemIDs   []int64
}

// CreateStories inserts every dedupe cluster and its member links in a
// single transaction — two statements total. The per-cluster and
// per-item INSERTs it replaces cost a remote round-trip each, which at
// shared-catalog scale (~5k items → ~8–9k calls) overran the 15-minute
// run timeout (KLOX-41). Draft order is preserved: the new story ids
// come back positionally and pair with each draft's member list.
func (db *DB) CreateStories(ctx context.Context, pubID int64, drafts []StoryDraft) error {
	if len(drafts) == 0 {
		return nil
	}
	titles := make([]string, len(drafts))
	simhashes := make([]int64, len(drafts))
	keys := make([]string, len(drafts))
	for i, d := range drafts {
		titles[i], simhashes[i], keys[i] = d.Title, d.Simhash, d.DedupeKey
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// unnest WITH ORDINALITY carries the draft order through the INSERT
	// so RETURNING emits the new ids in the same sequence — Postgres
	// streams RETURNING rows as the tuples go in.
	rows, err := tx.QueryContext(ctx, `
		INSERT INTO stories (workspace_id, publication_id, title, simhash, dedupe_key)
		SELECT $1, $2, d.title, d.simhash, d.dedupe_key
		FROM unnest($3::text[], $4::bigint[], $5::text[])
		     WITH ORDINALITY AS d(title, simhash, dedupe_key, ord)
		ORDER BY d.ord
		RETURNING id`, db.ws, pubID, titles, simhashes, keys)
	if err != nil {
		return err
	}
	storyIDs := make([]int64, 0, len(drafts))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		storyIDs = append(storyIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(storyIDs) != len(drafts) {
		return fmt.Errorf("CreateStories: got %d story ids for %d drafts", len(storyIDs), len(drafts))
	}
	var linkStory, linkItem []int64
	for i, d := range drafts {
		for _, itemID := range d.ItemIDs {
			linkStory = append(linkStory, storyIDs[i])
			linkItem = append(linkItem, itemID)
		}
	}
	if len(linkItem) > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO story_items (workspace_id, story_id, item_id)
			SELECT $1, m.story_id, m.item_id
			FROM unnest($2::bigint[], $3::bigint[]) AS m(story_id, item_id)
			ON CONFLICT (story_id, item_id) DO NOTHING`,
			db.ws, linkStory, linkItem); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetStoryTriage stores the triage verdict on a story so alternative
// candidates can be ranked after the run.
func (db *DB) SetStoryTriage(ctx context.Context, storyID int64, score float64, reason string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE stories SET triage_score = $1, triage_reason = $2 WHERE workspace_id = $3 AND id = $4`,
		score, reason, db.ws, storyID)
	return err
}

// StoryTriage pairs a story id with its triage verdict for the batch
// update.
type StoryTriage struct {
	StoryID int64
	Score   float64
	Reason  string
}

// SetStoriesTriage applies every verdict in one UPDATE. Deterministic
// triage scores all of a publication's stories — thousands at shared-
// catalog scale — where the per-story form cost a remote round-trip
// each.
func (db *DB) SetStoriesTriage(ctx context.Context, updates []StoryTriage) error {
	if len(updates) == 0 {
		return nil
	}
	ids := make([]int64, len(updates))
	scores := make([]float64, len(updates))
	reasons := make([]string, len(updates))
	for i, u := range updates {
		ids[i], scores[i], reasons[i] = u.StoryID, u.Score, u.Reason
	}
	_, err := db.ExecContext(ctx, `
		UPDATE stories s SET triage_score = u.score, triage_reason = u.reason
		FROM unnest($2::bigint[], $3::float8[], $4::text[]) AS u(id, score, reason)
		WHERE s.workspace_id = $1 AND s.id = u.id`,
		db.ws, ids, scores, reasons)
	return err
}

// StoryWithItems loads one story and its member items.
func (db *DB) StoryWithItems(ctx context.Context, storyID int64) (*Story, error) {
	var s Story
	err := db.QueryRowContext(ctx,
		`SELECT id, publication_id, title, COALESCE(triage_score,0), triage_reason, dedupe_key FROM stories
		 WHERE workspace_id = $1 AND id = $2`,
		db.ws, storyID).Scan(&s.ID, &s.PublicationID, &s.Title, &s.TriageScore, &s.TriageReason, &s.DedupeKey)
	if err != nil {
		return nil, err
	}
	items, err := db.storyItems(ctx, storyID)
	if err != nil {
		return nil, err
	}
	s.Items = items
	return &s, nil
}

// BestAlternative returns the highest-triaged story of the publication
// that is not already linked to the issue (in any status). Returns
// sql.ErrNoRows when no candidate exists.
func (db *DB) BestAlternative(ctx context.Context, pubID, issueID int64) (*Story, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		SELECT s.id FROM stories s
		WHERE s.workspace_id = $1 AND s.publication_id = $2
		  AND s.id NOT IN (SELECT story_id FROM issue_items WHERE workspace_id = $1 AND issue_id = $3)
		ORDER BY COALESCE(s.triage_score,0) DESC, s.id
		LIMIT 1`, db.ws, pubID, issueID).Scan(&id)
	if err != nil {
		return nil, err
	}
	return db.StoryWithItems(ctx, id)
}

func (db *DB) storyItems(ctx context.Context, storyID int64) ([]Item, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+itemCols+`
		FROM story_items si
		JOIN items i ON i.id = si.item_id
		LEFT JOIN sources s ON s.id = i.source_id
		LEFT JOIN source_catalog c ON c.id = i.catalog_id
		WHERE si.workspace_id = $1 AND si.story_id = $2 ORDER BY i.id`, db.ws, storyID)
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

// StoryOutlet is one member item's outlet, in item order — name with
// the URL as fallback, matching how the board labels coverage.
type StoryOutlet struct {
	Name string
	URL  string
}

// StoryOutlets batches member-outlet lookups for many stories into one
// query — the board's candidate view calls it once per request instead
// of loading every story's items row by row.
func (db *DB) StoryOutlets(ctx context.Context, storyIDs []int64) (map[int64][]StoryOutlet, error) {
	out := map[int64][]StoryOutlet{}
	if len(storyIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT si.story_id, COALESCE(s.name, c.name, ''), COALESCE(s.url, c.url, '')
		FROM story_items si
		JOIN items i ON i.id = si.item_id
		LEFT JOIN sources s ON s.id = i.source_id
		LEFT JOIN source_catalog c ON c.id = i.catalog_id
		WHERE si.workspace_id = $1 AND si.story_id = ANY($2)
		ORDER BY si.story_id, i.id`, db.ws, storyIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var storyID int64
		var o StoryOutlet
		if err := rows.Scan(&storyID, &o.Name, &o.URL); err != nil {
			return nil, err
		}
		out[storyID] = append(out[storyID], o)
	}
	return out, rows.Err()
}

// StoriesWithItems loads all stories and their member items.
func (db *DB) StoriesWithItems(ctx context.Context, pubID int64) ([]Story, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, title, COALESCE(triage_score,0), COALESCE(triage_reason,''), dedupe_key
		 FROM stories WHERE workspace_id = $1 AND publication_id = $2 ORDER BY id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[int64]int{}
	var out []Story
	for rows.Next() {
		var s Story
		if err := rows.Scan(&s.ID, &s.Title, &s.TriageScore, &s.TriageReason, &s.DedupeKey); err != nil {
			return nil, err
		}
		s.PublicationID = pubID
		idx[s.ID] = len(out)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	itemRows, err := db.QueryContext(ctx, `
		SELECT si.story_id, `+itemCols+`
		FROM story_items si
		JOIN items i ON i.id = si.item_id
		LEFT JOIN sources s ON s.id = i.source_id
		LEFT JOIN source_catalog c ON c.id = i.catalog_id
		WHERE si.workspace_id = $1
		  AND si.story_id IN (SELECT id FROM stories WHERE workspace_id = $1 AND publication_id = $2)
		ORDER BY si.story_id, i.id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()
	for itemRows.Next() {
		var storyID int64
		var it Item
		var sourceID, catalogID sql.NullInt64
		var externalID, canon, published, author sql.NullString
		var meta string
		if err := itemRows.Scan(&storyID, &it.ID, &sourceID, &catalogID, &it.SourceName, &it.SourceURL,
			&it.SourceType, &externalID, &it.URL, &canon, &it.Title, &author,
			&published, &it.ContentText, &it.Kind, &meta, &it.BodyTruncated, &it.FetchedAt); err != nil {
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
		if pos, ok := idx[storyID]; ok {
			out[pos].Items = append(out[pos].Items, it)
		}
	}
	return out, itemRows.Err()
}

// MergeStories moves all items from the secondary stories into the primary
// one and deletes the secondaries.
func (db *DB) MergeStories(ctx context.Context, primary int64, secondaries []int64) error {
	for _, sec := range secondaries {
		if _, err := db.ExecContext(ctx,
			`UPDATE story_items si SET story_id = $1
			 WHERE workspace_id = $2 AND story_id = $3
			   AND NOT EXISTS (SELECT 1 FROM story_items x WHERE x.story_id = $1 AND x.item_id = si.item_id)`,
			primary, db.ws, sec); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx,
			`DELETE FROM story_items WHERE workspace_id = $1 AND story_id = $2`, db.ws, sec); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx,
			`DELETE FROM stories WHERE workspace_id = $1 AND id = $2`, db.ws, sec); err != nil {
			return err
		}
	}
	return nil
}

// IssueItem is one scored, sectioned entry of an issue.
type IssueItem struct {
	ID       int64
	IssueID  int64
	StoryID  int64
	Section  string
	Score    float64
	Reason   string
	Status   string
	Origin   string // phaseA | alternative | manual
	Position int    // slot order inside its section, set by the lineup
	// OrigSection is the section Phase A first slotted the story into.
	// The board mutates Section when the creator moves a story; the
	// canonical replay input (demo fixtures) reads OrigSection so its
	// prompt keys stay stable no matter how the lineup changes.
	OrigSection string
}

// CreateIssueItem inserts an issue_item row.
func (db *DB) CreateIssueItem(ctx context.Context, ii IssueItem) (int64, error) {
	if ii.Origin == "" {
		ii.Origin = "phaseA"
	}
	if ii.OrigSection == "" {
		ii.OrigSection = ii.Section
	}
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO issue_items (workspace_id, issue_id, story_id, section, score, reason, status, origin, position, orig_section)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		db.ws, ii.IssueID, ii.StoryID, ii.Section, ii.Score, ii.Reason, ii.Status, ii.Origin, ii.Position, ii.OrigSection).Scan(&id)
	return id, err
}

// IssueItems lists an issue's items ordered by score descending.
func (db *DB) IssueItems(ctx context.Context, issueID int64) ([]IssueItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, issue_id, story_id, section, score, reason, status, origin, position, orig_section
		 FROM issue_items WHERE workspace_id = $1 AND issue_id = $2 ORDER BY score DESC`, db.ws, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IssueItem
	for rows.Next() {
		var ii IssueItem
		if err := rows.Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin, &ii.Position, &ii.OrigSection); err != nil {
			return nil, err
		}
		out = append(out, ii)
	}
	return out, rows.Err()
}

// IssueItemDossier pairs an issue item with its stored dossier JSON
// (empty when the item has no dossier yet).
type IssueItemDossier struct {
	Item    IssueItem
	Dossier string
}

// IssueItemDossiers lists an issue's items together with their dossier
// JSON in one query, same order as IssueItems. The issue document
// builder reads dossiers for every lineup item; the join replaces the
// per-item Dossier lookups that made every document build N+1 against
// remote Postgres (KLOX-47).
func (db *DB) IssueItemDossiers(ctx context.Context, issueID int64) ([]IssueItemDossier, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT ii.id, ii.issue_id, ii.story_id, ii.section, ii.score, ii.reason, ii.status, ii.origin, ii.position, ii.orig_section,
		       COALESCE(d.json, '')
		FROM issue_items ii
		LEFT JOIN dossiers d ON d.workspace_id = ii.workspace_id AND d.issue_item_id = ii.id
		WHERE ii.workspace_id = $1 AND ii.issue_id = $2 ORDER BY ii.score DESC`, db.ws, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IssueItemDossier
	for rows.Next() {
		var p IssueItemDossier
		ii := &p.Item
		if err := rows.Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin, &ii.Position, &ii.OrigSection, &p.Dossier); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// IssueDocBundle is everything the issue-document path needs, fetched
// in a single Postgres round-trip: the issue row, its publication, the
// issue items joined to their dossiers, and every item the dossiers
// cite. On hosted Postgres each statement pays a real network RTT
// (~150 ms to the remote pool), so the five-statement chain that used
// to build a document is collapsed into one JSON-aggregated SELECT
// (KLOX-47 latency criteria).
type IssueDocBundle struct {
	Issue *Issue
	Pub   *Publication
	Pairs []IssueItemDossier
	Items map[int64]*Item
}

// bundlePair mirrors one issue_items⋈dossiers row as json_agg emits it.
type bundlePair struct {
	ID          int64   `json:"id"`
	IssueID     int64   `json:"issue_id"`
	StoryID     int64   `json:"story_id"`
	Section     string  `json:"section"`
	Score       float64 `json:"score"`
	Reason      string  `json:"reason"`
	Status      string  `json:"status"`
	Origin      string  `json:"origin"`
	Position    int     `json:"position"`
	OrigSection string  `json:"orig_section"`
	Dossier     string  `json:"dossier"`
}

// bundleItem mirrors one items row with the source/coalesce aliases
// itemCols produces — nullable columns decode through pointers.
type bundleItem struct {
	ID            int64   `json:"id"`
	SourceID      *int64  `json:"source_id"`
	CatalogID     *int64  `json:"catalog_id"`
	SourceName    string  `json:"source_name"`
	SourceURL     string  `json:"source_url"`
	SourceType    string  `json:"source_type"`
	ExternalID    *string `json:"external_id"`
	URL           string  `json:"url"`
	CanonicalURL  *string `json:"canonical_url"`
	Title         string  `json:"title"`
	Author        *string `json:"author"`
	PublishedAt   *string `json:"published_at"`
	ContentText   string  `json:"content_text"`
	Kind          string  `json:"kind"`
	Meta          string  `json:"meta_json"`
	BodyTruncated bool    `json:"body_truncated"`
	FetchedAt     string  `json:"fetched_at"`
}

// IssueDocBundle loads the whole document context in one query — see
// IssueDocBundle for why. sql.ErrNoRows when the issue (or its
// publication) is missing.
func (db *DB) IssueDocBundle(ctx context.Context, issueID int64) (*IssueDocBundle, error) {
	var iss Issue
	var pub Publication
	var flags, commentary, sections, paywall, pairsJSON, itemsJSON string
	err := db.QueryRowContext(ctx, `
		SELECT i.id, i.publication_id, i.number, i.status, i.subject, i.body_md,
		       i.flags_json, i.commentary_json, i.created_at, i.scheduled_at, i.test_sent_at,
		       p.id, p.slug, p.name, p.language, p.topic, p.audience, p.editorial_line,
		       p.tone, p.frequency, p.sections_json, p.paywall_json, p.ai_disclosure, p.custom,
		       (SELECT COALESCE(json_agg(row_to_json(x) ORDER BY x.score DESC), '[]')
		        FROM (
		            SELECT ii.id, ii.issue_id, ii.story_id, ii.section, ii.score, ii.reason,
		                   ii.status, ii.origin, ii.position, ii.orig_section,
		                   COALESCE(d.json, '') AS dossier
		            FROM issue_items ii
		            LEFT JOIN dossiers d ON d.workspace_id = ii.workspace_id AND d.issue_item_id = ii.id
		            WHERE ii.workspace_id = i.workspace_id AND ii.issue_id = i.id
		            ORDER BY ii.score DESC) x),
		       (SELECT COALESCE(json_agg(row_to_json(y)), '[]')
		        FROM (
		            SELECT it.id, it.source_id, it.catalog_id,
		                   COALESCE(s.name, c.name, '') AS source_name,
		                   COALESCE(s.url, c.url, '') AS source_url,
		                   COALESCE(s.type, 'rss') AS source_type,
		                   it.external_id, it.url, it.canonical_url, it.title, it.author,
		                   it.published_at, it.content_text, it.kind, it.meta_json,
		                   it.body_truncated, it.fetched_at
		            FROM items it
		            LEFT JOIN sources s ON s.id = it.source_id
		            LEFT JOIN source_catalog c ON c.id = it.catalog_id
		            WHERE (it.workspace_id = i.workspace_id OR it.catalog_id IS NOT NULL)
		              AND it.id = ANY(
		                  SELECT DISTINCT NULLIF(el->>'item_id','')::bigint
		                  FROM issue_items ii2
		                  JOIN dossiers d2 ON d2.workspace_id = ii2.workspace_id AND d2.issue_item_id = ii2.id
		                  CROSS JOIN LATERAL jsonb_array_elements(
		                              COALESCE(NULLIF(d2.json,'')::jsonb -> 'sources', '[]'::jsonb)) el
		                  WHERE ii2.workspace_id = i.workspace_id AND ii2.issue_id = i.id)) y)
		FROM issues i
		JOIN publications p ON p.workspace_id = i.workspace_id AND p.id = i.publication_id
		WHERE i.workspace_id = $1 AND i.id = $2`, db.ws, issueID).
		Scan(&iss.ID, &iss.PublicationID, &iss.Number, &iss.Status, &iss.Subject,
			&iss.BodyMD, &flags, &commentary, &iss.CreatedAt, &iss.ScheduledAt, &iss.TestSentAt,
			&pub.ID, &pub.Slug, &pub.Name, &pub.Language, &pub.Topic, &pub.Audience,
			&pub.EditorialLine, &pub.Tone, &pub.Frequency, &sections, &paywall,
			&pub.AIDisclosure, &pub.Custom, &pairsJSON, &itemsJSON)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(flags), &iss.Flags)
	json.Unmarshal([]byte(commentary), &iss.Commentary)
	if err := json.Unmarshal([]byte(sections), &pub.Sections); err != nil {
		return nil, fmt.Errorf("decoding sections for %q: %w", pub.Slug, err)
	}
	if err := json.Unmarshal([]byte(paywall), &pub.Paywall); err != nil {
		return nil, fmt.Errorf("decoding paywall for %q: %w", pub.Slug, err)
	}
	b := &IssueDocBundle{Issue: &iss, Pub: &pub, Items: map[int64]*Item{}}
	var pairs []bundlePair
	if err := json.Unmarshal([]byte(pairsJSON), &pairs); err != nil {
		return nil, fmt.Errorf("decoding issue items for %d: %w", issueID, err)
	}
	for _, p := range pairs {
		b.Pairs = append(b.Pairs, IssueItemDossier{
			Item: IssueItem{
				ID: p.ID, IssueID: p.IssueID, StoryID: p.StoryID, Section: p.Section,
				Score: p.Score, Reason: p.Reason, Status: p.Status, Origin: p.Origin,
				Position: p.Position, OrigSection: p.OrigSection,
			},
			Dossier: p.Dossier,
		})
	}
	var items []bundleItem
	if err := json.Unmarshal([]byte(itemsJSON), &items); err != nil {
		return nil, fmt.Errorf("decoding cited items for %d: %w", issueID, err)
	}
	for _, it := range items {
		item := &Item{
			ID: it.ID, SourceName: it.SourceName, SourceURL: it.SourceURL,
			SourceType: it.SourceType, URL: it.URL, Title: it.Title,
			ContentText: it.ContentText, Kind: it.Kind,
			BodyTruncated: it.BodyTruncated, FetchedAt: it.FetchedAt,
		}
		if it.SourceID != nil {
			item.SourceID = *it.SourceID
		}
		if it.CatalogID != nil {
			item.CatalogID = *it.CatalogID
		}
		if it.ExternalID != nil {
			item.ExternalID = *it.ExternalID
		}
		if it.CanonicalURL != nil {
			item.CanonicalURL = *it.CanonicalURL
		}
		if it.Author != nil {
			item.Author = *it.Author
		}
		if it.PublishedAt != nil {
			item.PublishedAt = *it.PublishedAt
		}
		if it.Meta != "" && it.Meta != "{}" {
			json.Unmarshal([]byte(it.Meta), &item.Meta)
		}
		b.Items[item.ID] = item
	}
	return b, nil
}

// IssueEdit carries one document mutation's writes so they reach
// Postgres in a single statement — the body UPDATE, the flag list, the
// approval revert (status + receipt + queued send) and the decision row
// all ride one CTE instead of six round-trips (KLOX-47 latency
// criteria).
type IssueEdit struct {
	PubID       int64
	IssueItemID int64 // decision target; 0 stores NULL
	Body        string
	Flags       []Flag // nil leaves flags_json untouched
	Decision    string // kind; "" skips the decisions insert
	Reason      string
	// RevertApproved mirrors revertApproved: when the stored issue is
	// still 'approved', the edit puts it back to 'drafted', drops its
	// approval receipt and cancels the queued send — the EXISTS guards
	// read the pre-statement snapshot, so a stale caller flag cannot
	// touch a non-approved issue's receipt or jobs.
	RevertApproved bool
}

// SaveIssueEdit applies e in one round-trip.
func (db *DB) SaveIssueEdit(ctx context.Context, issueID int64, e IssueEdit) error {
	var flagsJSON any
	if e.Flags != nil {
		data, err := json.Marshal(e.Flags)
		if err != nil {
			return err
		}
		flagsJSON = string(data)
	}
	var ii any
	if e.IssueItemID != 0 {
		ii = e.IssueItemID
	}
	_, err := db.ExecContext(ctx, `
		WITH u AS (
			UPDATE issues
			SET body_md = $2, flags_json = COALESCE($3::text, flags_json),
			    status = CASE WHEN $9 AND status = 'approved' THEN 'drafted' ELSE status END
			WHERE workspace_id = $1 AND id = $4),
		d AS (
			DELETE FROM approval_receipts
			WHERE workspace_id = $1 AND issue_id = $4 AND $9
			  AND EXISTS (SELECT 1 FROM issues
			              WHERE workspace_id = $1 AND id = $4 AND status = 'approved')),
		j AS (
			DELETE FROM jobs
			WHERE workspace_id = $1 AND kind = 'send_issue' AND ref_id = $4
			  AND status = 'pending' AND $9
			  AND EXISTS (SELECT 1 FROM issues
			              WHERE workspace_id = $1 AND id = $4 AND status = 'approved'))
		INSERT INTO decisions (workspace_id, publication_id, issue_item_id, kind, reason)
		SELECT $1, $5, $6, $7, $8 WHERE $7 <> ''`,
		db.ws, e.Body, flagsJSON, issueID, e.PubID, ii, e.Decision, e.Reason, e.RevertApproved)
	return err
}

// SetIssueItemPlacement moves an issue item to a section slot. The empty
// status keeps the current one.
func (db *DB) SetIssueItemPlacement(ctx context.Context, iiID int64, section string, position int, status string) error {
	if status == "" {
		_, err := db.ExecContext(ctx,
			`UPDATE issue_items SET section = $1, position = $2 WHERE workspace_id = $3 AND id = $4`,
			section, position, db.ws, iiID)
		return err
	}
	_, err := db.ExecContext(ctx,
		`UPDATE issue_items SET section = $1, position = $2, status = $3 WHERE workspace_id = $4 AND id = $5`,
		section, position, status, db.ws, iiID)
	return err
}

// RankedCandidate is one triaged story offered to the issue lineup:
// either already in the issue or still on the bench.
type RankedCandidate struct {
	Story      *Story
	Item       *IssueItem // nil when the story is not in this issue
	Rank       int        // 1 = best triage score of this run
	Duplicates int        // same-event stories merged into this one
	Dossier    bool
}

// RankedCandidates lists the run's ranked stories for an issue: every
// triaged story of the publication that is not already placed in another
// issue, best score first. `inIssue` tells which are currently in.
//
// One round-trip: the issue's own items are joined (deduplicated to the
// first row per story, matching IssueItemByStory's single-row read) and
// the dossier flag is an EXISTS on the joined row — at ~160 candidates
// per-candidate lookups turn the endpoint into a Fly proxy 502.
func (db *DB) RankedCandidates(ctx context.Context, pubID, issueID int64) ([]RankedCandidate, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.title, COALESCE(s.triage_score,0), COALESCE(s.triage_reason,''),
		       (SELECT COUNT(*) FROM story_items si WHERE si.story_id = s.id),
		       ii.id, ii.section, ii.score, ii.reason, ii.status, ii.origin, ii.position, ii.orig_section,
		       EXISTS(SELECT 1 FROM dossiers d WHERE d.workspace_id = $1 AND d.issue_item_id = ii.id)
		FROM stories s
		LEFT JOIN (
		  SELECT DISTINCT ON (story_id) * FROM issue_items
		  WHERE workspace_id = $1 AND issue_id = $3
		  ORDER BY story_id, id
		) ii ON ii.story_id = s.id
		WHERE s.workspace_id = $1 AND s.publication_id = $2
		  AND s.triage_score IS NOT NULL
		  AND s.id NOT IN (
		    SELECT ii.story_id FROM issue_items ii
		    JOIN issues i ON i.id = ii.issue_id
		    WHERE ii.workspace_id = $1 AND i.id != $3 AND i.publication_id = s.publication_id)
		ORDER BY COALESCE(s.triage_score,0) DESC, s.id`, db.ws, pubID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RankedCandidate{}
	for rows.Next() {
		var (
			storyID, members        int64
			title, reason           string
			score                   float64
			iiID, iiPos             sql.NullInt64
			iiSec, iiStatus, iiOrig sql.NullString
			iiScore                 sql.NullFloat64
			iiReason, iiOrigSec     sql.NullString
			dossier                 bool
		)
		if err := rows.Scan(&storyID, &title, &score, &reason, &members,
			&iiID, &iiSec, &iiScore, &iiReason, &iiStatus, &iiOrig, &iiPos, &iiOrigSec,
			&dossier); err != nil {
			return nil, err
		}
		c := RankedCandidate{
			Story: &Story{ID: storyID, Title: title, TriageScore: score, TriageReason: reason},
			Rank:  len(out) + 1, Duplicates: int(members) - 1, Dossier: dossier,
		}
		if iiID.Valid {
			c.Item = &IssueItem{
				ID: iiID.Int64, IssueID: issueID, StoryID: storyID,
				Section: iiSec.String, Score: iiScore.Float64, Reason: iiReason.String,
				Status: iiStatus.String, Origin: iiOrig.String,
				Position: int(iiPos.Int64), OrigSection: iiOrigSec.String,
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveDossier upserts the dossier JSON for an issue item.
func (db *DB) SaveDossier(ctx context.Context, issueItemID int64, json string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO dossiers (workspace_id, issue_item_id, json) VALUES ($1,$2,$3)
		 ON CONFLICT (issue_item_id) DO UPDATE SET json = excluded.json`, db.ws, issueItemID, json)
	return err
}

// Dossier returns the dossier JSON for an issue item.
func (db *DB) Dossier(ctx context.Context, issueItemID int64) (string, error) {
	var j string
	err := db.QueryRowContext(ctx,
		`SELECT json FROM dossiers WHERE workspace_id = $1 AND issue_item_id = $2`, db.ws, issueItemID).Scan(&j)
	return j, err
}

// UpdateIssue sets status, body and flags on an issue. A re-draft
// invalidates any stored approval receipt.
func (db *DB) UpdateIssue(ctx context.Context, issueID int64, status, bodyMD, flagsJSON string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE issues SET status = $1, body_md = $2, flags_json = $3 WHERE workspace_id = $4 AND id = $5`,
		status, bodyMD, flagsJSON, db.ws, issueID)
	if err != nil {
		return err
	}
	if status != "approved" {
		_, err = db.ExecContext(ctx,
			`DELETE FROM approval_receipts WHERE workspace_id = $1 AND issue_id = $2`, db.ws, issueID)
	}
	return err
}

// RecordDecision stores an approve/reject/edit/alternative decision.
// IssueItemID 0 means a publication-level decision and is stored as NULL.
func (db *DB) RecordDecision(ctx context.Context, pubID int64, issueItemID int64, kind, reason string) error {
	var ii any
	if issueItemID != 0 {
		ii = issueItemID
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO decisions (workspace_id, publication_id, issue_item_id, kind, reason) VALUES ($1,$2,$3,$4,$5)`,
		db.ws, pubID, ii, kind, reason)
	return err
}

// LatestDecision returns the most recent decision on an issue item
// (sql.ErrNoRows when none).
func (db *DB) LatestDecision(ctx context.Context, issueItemID int64) (kind, reason string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT kind, reason FROM decisions WHERE workspace_id = $1 AND issue_item_id = $2 ORDER BY id DESC LIMIT 1`,
		db.ws, issueItemID).Scan(&kind, &reason)
	return kind, reason, err
}

// Decision is a stored review decision.
type Decision struct {
	ItemID    int64
	Kind      string
	Reason    string
	CreatedAt string
}

// Decisions lists a publication's decisions, oldest first.
func (db *DB) Decisions(ctx context.Context, pubID int64) ([]Decision, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(issue_item_id, 0), kind, reason, created_at FROM decisions
		 WHERE workspace_id = $1 AND publication_id = $2 ORDER BY id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.ItemID, &d.Kind, &d.Reason, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveGuide stores a new versioned editorial guide. When content matches
// the latest version nothing is written — the current version comes back.
func (db *DB) SaveGuide(ctx context.Context, pubID int64, content string) (int, error) {
	var curV int
	var curC string
	err := db.QueryRowContext(ctx,
		`SELECT version, content FROM editorial_guides WHERE workspace_id = $1 AND publication_id = $2 ORDER BY version DESC LIMIT 1`,
		db.ws, pubID).Scan(&curV, &curC)
	if err == nil && strings.TrimSpace(curC) == strings.TrimSpace(content) {
		return curV, nil
	}
	var version int
	err = db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM editorial_guides WHERE workspace_id = $1 AND publication_id = $2`,
		db.ws, pubID).Scan(&version)
	if err != nil {
		return 0, err
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO editorial_guides (workspace_id, publication_id, version, content) VALUES ($1,$2,$3,$4)`,
		db.ws, pubID, version, content); err != nil {
		return 0, err
	}
	return version, nil
}
