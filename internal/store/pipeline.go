package store

import (
	"context"
	"database/sql"

	"github.com/sthefanocordeiro/kloxus/internal/sources"
)

// Item is a raw collected item.
type Item struct {
	ID           int64
	SourceID     int64
	SourceURL    string
	SourceType   string
	ExternalID   string
	URL          string
	CanonicalURL string
	Title        string
	Author       string
	PublishedAt  string
	ContentText  string
}

// SaveItems inserts collected items (idempotent per source+external_id).
// Returns the number of new rows.
func (db *DB) SaveItems(ctx context.Context, sourceID int64, raw []sources.RawItem, canonical func(string) string) (int, error) {
	added := 0
	for _, r := range raw {
		res, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO items
			(source_id, external_id, url, canonical_url, title, author, published_at, content_text)
			VALUES (?,?,?,?,?,?,?,?)`,
			sourceID, r.ExternalID, r.URL, canonical(r.URL), r.Title, r.Author, r.PublishedAt, r.ContentText)
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	return added, nil
}

// ItemsForPublication returns all items across a publication's sources.
func (db *DB) ItemsForPublication(ctx context.Context, pubID int64) ([]Item, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT i.id, i.source_id, s.url, s.type, i.external_id, i.url,
		       i.canonical_url, i.title, i.author, i.published_at, i.content_text
		FROM items i JOIN sources s ON s.id = i.source_id
		WHERE s.publication_id = ? ORDER BY i.id`, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var canon sql.NullString
		if err := rows.Scan(&it.ID, &it.SourceID, &it.SourceURL, &it.SourceType,
			&it.ExternalID, &it.URL, &canon, &it.Title, &it.Author, &it.PublishedAt, &it.ContentText); err != nil {
			return nil, err
		}
		it.CanonicalURL = canon.String
		out = append(out, it)
	}
	return out, rows.Err()
}

// MarkSourceFetched records a fetch outcome on a source.
func (db *DB) MarkSourceFetched(ctx context.Context, sourceID int64, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sources SET last_fetch_at = datetime('now'), last_status = ? WHERE id = ?`,
		status, sourceID)
	return err
}

// Story is a deduplicated group of items covering the same story.
type Story struct {
	ID            int64
	PublicationID int64
	Title         string
	Items         []Item
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
// content lives in dossiers and the issue body, not in the stories table.
// FK checks are suspended only for the duration of this delete.
func (db *DB) ClearStories(ctx context.Context, pubID int64) error {
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer db.Exec(`PRAGMA foreign_keys = ON`)
	if _, err := db.ExecContext(ctx,
		`DELETE FROM story_items WHERE story_id IN (SELECT id FROM stories WHERE publication_id = ?)`, pubID); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `DELETE FROM stories WHERE publication_id = ?`, pubID)
	return err
}

// CreateStory inserts a story group.
func (db *DB) CreateStory(ctx context.Context, pubID int64, title string, simhash int64) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO stories (publication_id, title, simhash) VALUES (?,?,?)`, pubID, title, simhash)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddStoryItem links an item to a story.
func (db *DB) AddStoryItem(ctx context.Context, storyID, itemID int64) error {
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO story_items (story_id, item_id) VALUES (?,?)`, storyID, itemID)
	return err
}

// StoriesWithItems loads all stories and their member items.
func (db *DB) StoriesWithItems(ctx context.Context, pubID int64) ([]Story, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, title FROM stories WHERE publication_id = ? ORDER BY id`, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[int64]int{}
	var out []Story
	for rows.Next() {
		var s Story
		if err := rows.Scan(&s.ID, &s.Title); err != nil {
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
		SELECT si.story_id, i.id, i.source_id, s.url, s.type, i.external_id, i.url,
		       i.canonical_url, i.title, i.author, i.published_at, i.content_text
		FROM story_items si
		JOIN items i ON i.id = si.item_id
		JOIN sources s ON s.id = i.source_id
		ORDER BY si.story_id, i.id`)
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()
	for itemRows.Next() {
		var storyID int64
		var it Item
		var canon sql.NullString
		if err := itemRows.Scan(&storyID, &it.ID, &it.SourceID, &it.SourceURL, &it.SourceType,
			&it.ExternalID, &it.URL, &canon, &it.Title, &it.Author, &it.PublishedAt, &it.ContentText); err != nil {
			return nil, err
		}
		it.CanonicalURL = canon.String
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
			`UPDATE OR IGNORE story_items SET story_id = ? WHERE story_id = ?`, primary, sec); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM story_items WHERE story_id = ?`, sec); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM stories WHERE id = ?`, sec); err != nil {
			return err
		}
	}
	return nil
}

// IssueItem is one scored, sectioned entry of an issue.
type IssueItem struct {
	ID      int64
	IssueID int64
	StoryID int64
	Section string
	Score   float64
	Reason  string
	Status  string
}

// CreateIssueItem inserts an issue_item row.
func (db *DB) CreateIssueItem(ctx context.Context, ii IssueItem) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO issue_items (issue_id, story_id, section, score, reason, status) VALUES (?,?,?,?,?,?)`,
		ii.IssueID, ii.StoryID, ii.Section, ii.Score, ii.Reason, ii.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// IssueItems lists an issue's items ordered by score descending.
func (db *DB) IssueItems(ctx context.Context, issueID int64) ([]IssueItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, issue_id, story_id, section, score, reason, status FROM issue_items WHERE issue_id = ? ORDER BY score DESC`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IssueItem
	for rows.Next() {
		var ii IssueItem
		if err := rows.Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status); err != nil {
			return nil, err
		}
		out = append(out, ii)
	}
	return out, rows.Err()
}

// SaveDossier upserts the dossier JSON for an issue item.
func (db *DB) SaveDossier(ctx context.Context, issueItemID int64, json string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO dossiers (issue_item_id, json) VALUES (?,?)
		 ON CONFLICT(issue_item_id) DO UPDATE SET json = excluded.json`, issueItemID, json)
	return err
}

// Dossier returns the dossier JSON for an issue item.
func (db *DB) Dossier(ctx context.Context, issueItemID int64) (string, error) {
	var j string
	err := db.QueryRowContext(ctx,
		`SELECT json FROM dossiers WHERE issue_item_id = ?`, issueItemID).Scan(&j)
	return j, err
}

// UpdateIssue sets status, body and flags on an issue.
func (db *DB) UpdateIssue(ctx context.Context, issueID int64, status, bodyMD, flagsJSON string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE issues SET status = ?, body_md = ?, flags_json = ? WHERE id = ?`,
		status, bodyMD, flagsJSON, issueID)
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
		`INSERT INTO decisions (publication_id, issue_item_id, kind, reason) VALUES (?,?,?,?)`,
		pubID, ii, kind, reason)
	return err
}

// Decision is a stored review decision.
type Decision struct {
	Kind      string
	Reason    string
	CreatedAt string
}

// Decisions lists a publication's decisions, oldest first.
func (db *DB) Decisions(ctx context.Context, pubID int64) ([]Decision, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT kind, reason, created_at FROM decisions WHERE publication_id = ? ORDER BY id`, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.Kind, &d.Reason, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveGuide stores a new versioned editorial guide.
func (db *DB) SaveGuide(ctx context.Context, pubID int64, content string) (int, error) {
	var version int
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM editorial_guides WHERE publication_id = ?`, pubID).Scan(&version)
	if err != nil {
		return 0, err
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO editorial_guides (publication_id, version, content) VALUES (?,?,?)`,
		pubID, version, content); err != nil {
		return 0, err
	}
	return version, nil
}
