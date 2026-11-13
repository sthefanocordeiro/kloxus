package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/sources"
)

// Item is a raw collected item.
type Item struct {
	ID           int64
	SourceID     int64
	SourceName   string
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
		res, err := db.ExecContext(ctx, `INSERT INTO items
			(workspace_id, source_id, external_id, url, canonical_url, title, author, published_at, content_text)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
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

// ItemsForPublication returns all items across a publication's sources.
func (db *DB) ItemsForPublication(ctx context.Context, pubID int64) ([]Item, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT i.id, i.source_id, s.name, s.url, s.type, i.external_id, i.url,
		       i.canonical_url, i.title, i.author, i.published_at, i.content_text
		FROM items i JOIN sources s ON s.id = i.source_id
		WHERE i.workspace_id = $1 AND s.publication_id = $2 ORDER BY i.id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var canon, sourceName sql.NullString
		if err := rows.Scan(&it.ID, &it.SourceID, &sourceName, &it.SourceURL, &it.SourceType,
			&it.ExternalID, &it.URL, &canon, &it.Title, &it.Author, &it.PublishedAt, &it.ContentText); err != nil {
			return nil, err
		}
		it.CanonicalURL = canon.String
		it.SourceName = sourceName.String
		out = append(out, it)
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

// GetItem loads one item with its source's outlet name.
func (db *DB) GetItem(ctx context.Context, id int64) (*Item, error) {
	var it Item
	var canon, sourceName sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT i.id, i.source_id, s.url, s.type, i.external_id, i.url,
		       i.canonical_url, i.title, i.author, i.published_at, i.content_text, s.name
		FROM items i JOIN sources s ON s.id = i.source_id
		WHERE i.workspace_id = $1 AND i.id = $2`, db.ws, id).
		Scan(&it.ID, &it.SourceID, &it.SourceURL, &it.SourceType,
			&it.ExternalID, &it.URL, &canon, &it.Title, &it.Author, &it.PublishedAt, &it.ContentText, &sourceName)
	if err != nil {
		return nil, err
	}
	it.CanonicalURL = canon.String
	it.SourceName = sourceName.String
	return &it, nil
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
	DedupeKey     string
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

// SetStoryTriage stores the triage verdict on a story so alternative
// candidates can be ranked after the run.
func (db *DB) SetStoryTriage(ctx context.Context, storyID int64, score float64, reason string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE stories SET triage_score = $1, triage_reason = $2 WHERE workspace_id = $3 AND id = $4`,
		score, reason, db.ws, storyID)
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
		SELECT i.id, i.source_id, s.name, s.url, s.type, i.external_id, i.url,
		       i.canonical_url, i.title, i.author, i.published_at, i.content_text
		FROM story_items si
		JOIN items i ON i.id = si.item_id
		JOIN sources s ON s.id = i.source_id
		WHERE si.workspace_id = $1 AND si.story_id = $2 ORDER BY i.id`, db.ws, storyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var canon, sourceName sql.NullString
		if err := rows.Scan(&it.ID, &it.SourceID, &sourceName, &it.SourceURL, &it.SourceType,
			&it.ExternalID, &it.URL, &canon, &it.Title, &it.Author, &it.PublishedAt, &it.ContentText); err != nil {
			return nil, err
		}
		it.CanonicalURL = canon.String
		it.SourceName = sourceName.String
		out = append(out, it)
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
		SELECT si.story_id, i.id, i.source_id, s.name, s.url, s.type, i.external_id, i.url,
		       i.canonical_url, i.title, i.author, i.published_at, i.content_text
		FROM story_items si
		JOIN items i ON i.id = si.item_id
		JOIN sources s ON s.id = i.source_id
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
		var canon, sourceName sql.NullString
		if err := itemRows.Scan(&storyID, &it.ID, &it.SourceID, &sourceName, &it.SourceURL, &it.SourceType,
			&it.ExternalID, &it.URL, &canon, &it.Title, &it.Author, &it.PublishedAt, &it.ContentText); err != nil {
			return nil, err
		}
		it.CanonicalURL = canon.String
		it.SourceName = sourceName.String
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
func (db *DB) RankedCandidates(ctx context.Context, pubID, issueID int64) ([]RankedCandidate, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.title, COALESCE(s.triage_score,0), COALESCE(s.triage_reason,''),
		       (SELECT COUNT(*) FROM story_items si WHERE si.story_id = s.id)
		FROM stories s
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
	type row struct {
		id      int64
		title   string
		score   float64
		reason  string
		members int
	}
	var rws []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.title, &r.score, &r.reason, &r.members); err != nil {
			return nil, err
		}
		rws = append(rws, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]RankedCandidate, 0, len(rws))
	for i, r := range rws {
		st := &Story{ID: r.id, Title: r.title, TriageScore: r.score, TriageReason: r.reason}
		ii, err := db.IssueItemByStory(ctx, issueID, r.id)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		dossier := false
		if ii != nil {
			if _, err := db.Dossier(ctx, ii.ID); err == nil {
				dossier = true
			}
		}
		out = append(out, RankedCandidate{
			Story: st, Item: ii, Rank: i + 1,
			Duplicates: r.members - 1, Dossier: dossier,
		})
	}
	return out, nil
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
