package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
)

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
	// Simhash is the fingerprint of the representative member's text —
	// stable across the stories-table rebuilds between runs.
	Simhash int64
	Items   []Item
}

// FeedbackKeys are the stable identifiers a Sim/Não answer records
// (KLOX-183): the canonical dedupe URL and the SimHash of the
// representative member. Story ids do not survive ClearStories/
// CreateStories, so feedback keys on these instead.
func (s Story) FeedbackKeys() []string {
	var out []string
	if s.DedupeKey != "" {
		out = append(out, "u:"+s.DedupeKey)
	}
	if s.Simhash != 0 {
		out = append(out, "s:"+strconv.FormatInt(s.Simhash, 10))
	}
	return out
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
// — which is why issue_items.story_id carries no foreign key. The member
// links and story rows are deleted in one transaction so a failure cannot
// leave link rows pointing at stories that are gone (or vice versa).
func (db *DB) ClearStories(ctx context.Context, pubID int64) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM story_items WHERE workspace_id = $1 AND story_id IN
			(SELECT id FROM stories WHERE workspace_id = $1 AND publication_id = $2)`, db.ws, pubID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM stories WHERE workspace_id = $1 AND publication_id = $2`, db.ws, pubID); err != nil {
		return err
	}
	return tx.Commit()
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
	// Rel and Hidden carry the stage-1 verdict (KLOX-109): relevance
	// 0-1 and "" | "off_topic" | "english". Left zero by the paid path.
	Rel    float64
	Hidden string
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
	rels := make([]float64, len(updates))
	hidden := make([]string, len(updates))
	for i, u := range updates {
		ids[i], scores[i], reasons[i], rels[i], hidden[i] = u.StoryID, u.Score, u.Reason, u.Rel, u.Hidden
	}
	_, err := db.ExecContext(ctx, `
		UPDATE stories s SET triage_score = u.score, triage_reason = u.reason,
		       rel = u.rel, stage1_hidden = u.hidden
		FROM unnest($2::bigint[], $3::float8[], $4::text[], $5::float8[], $6::text[])
		     AS u(id, score, reason, rel, hidden)
		WHERE s.workspace_id = $1 AND s.id = u.id`,
		db.ws, ids, scores, reasons, rels, hidden)
	return err
}

// StoryWithItems loads one story and its member items.
func (db *DB) StoryWithItems(ctx context.Context, storyID int64) (*Story, error) {
	var s Story
	err := db.QueryRowContext(ctx,
		`SELECT id, publication_id, title, COALESCE(triage_score,0), triage_reason, dedupe_key, COALESCE(simhash,0) FROM stories
		 WHERE workspace_id = $1 AND id = $2`,
		db.ws, storyID).Scan(&s.ID, &s.PublicationID, &s.Title, &s.TriageScore, &s.TriageReason, &s.DedupeKey, &s.Simhash)
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
		`SELECT id, title, COALESCE(triage_score,0), COALESCE(triage_reason,''), dedupe_key, COALESCE(simhash,0)
		 FROM stories WHERE workspace_id = $1 AND publication_id = $2 ORDER BY id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[int64]int{}
	var out []Story
	for rows.Next() {
		var s Story
		if err := rows.Scan(&s.ID, &s.Title, &s.TriageScore, &s.TriageReason, &s.DedupeKey, &s.Simhash); err != nil {
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
// one and deletes the secondaries. All merges commit together — a failure on
// one duplicate must not leave a half-merged cluster behind.
func (db *DB) MergeStories(ctx context.Context, primary int64, secondaries []int64) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sec := range secondaries {
		if _, err := tx.ExecContext(ctx,
			`UPDATE story_items si SET story_id = $1
			 WHERE workspace_id = $2 AND story_id = $3
			   AND NOT EXISTS (SELECT 1 FROM story_items x WHERE x.story_id = $1 AND x.item_id = si.item_id)`,
			primary, db.ws, sec); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM story_items WHERE workspace_id = $1 AND story_id = $2`, db.ws, sec); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM stories WHERE workspace_id = $1 AND id = $2`, db.ws, sec); err != nil {
			return err
		}
	}
	return tx.Commit()
}
