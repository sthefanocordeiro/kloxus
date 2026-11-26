package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

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
	// AIDraft marks a pauta whose body text arrived AI-written (KLOX-67):
	// the composer shows it as generated content until the author's
	// first edit clears the flag.
	AIDraft bool
	// Title is the story's title snapshotted at lineup time (KLOX-184):
	// stories are rebuilt every run and story_id goes dangling, so the
	// lists name the pauta from this copy, not the stories row.
	Title string
}

// CreateIssueItem inserts an issue_item row. The title is copied off
// the story at insert time — stories are rebuilt every run (D13), so
// the pauta's name can't keep living there.
func (db *DB) CreateIssueItem(ctx context.Context, ii IssueItem) (int64, error) {
	if ii.Origin == "" {
		ii.Origin = "phaseA"
	}
	if ii.OrigSection == "" {
		ii.OrigSection = ii.Section
	}
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO issue_items (workspace_id, issue_id, story_id, section, score, reason, status, origin, position, orig_section, title)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,
		         COALESCE(NULLIF($11,''),
		                  (SELECT title FROM stories WHERE workspace_id = $1 AND id = $3), ''))
		 RETURNING id`,
		db.ws, ii.IssueID, ii.StoryID, ii.Section, ii.Score, ii.Reason, ii.Status, ii.Origin, ii.Position, ii.OrigSection, ii.Title).Scan(&id)
	return id, err
}

// IssueItems lists an issue's items ordered by score descending.
func (db *DB) IssueItems(ctx context.Context, issueID int64) ([]IssueItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, issue_id, story_id, section, score, reason, status, origin, position, orig_section, ai_draft, title
		 FROM issue_items WHERE workspace_id = $1 AND issue_id = $2 ORDER BY score DESC`, db.ws, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IssueItem
	for rows.Next() {
		var ii IssueItem
		if err := rows.Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin, &ii.Position, &ii.OrigSection, &ii.AIDraft, &ii.Title); err != nil {
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
		SELECT ii.id, ii.issue_id, ii.story_id, ii.section, ii.score, ii.reason, ii.status, ii.origin, ii.position, ii.orig_section, ii.ai_draft, ii.title,
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
		if err := rows.Scan(&ii.ID, &ii.IssueID, &ii.StoryID, &ii.Section, &ii.Score, &ii.Reason, &ii.Status, &ii.Origin, &ii.Position, &ii.OrigSection, &ii.AIDraft, &ii.Title, &p.Dossier); err != nil {
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
	AIDraft     bool    `json:"ai_draft"`
	Title       string  `json:"title"`
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
	var flags, commentary, issSections, sections, paywall, pairsJSON, itemsJSON string
	err := db.QueryRowContext(ctx, `
		SELECT i.id, i.publication_id, i.number, i.status, i.subject, i.body_md,
		       i.flags_json, i.commentary_json, i.sections_json, i.created_at, i.scheduled_at, i.test_sent_at,
		       p.id, p.slug, p.name, p.language, p.topic, p.audience, p.editorial_line,
		       p.tone, p.frequency, p.sections_json, p.paywall_json, p.ai_disclosure, p.custom,
		       (SELECT COALESCE(json_agg(row_to_json(x) ORDER BY x.score DESC), '[]')
		        FROM (
		            SELECT ii.id, ii.issue_id, ii.story_id, ii.section, ii.score, ii.reason,
		                   ii.status, ii.origin, ii.position, ii.orig_section, ii.ai_draft, ii.title,
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
			&iss.BodyMD, &flags, &commentary, &issSections, &iss.CreatedAt, &iss.ScheduledAt, &iss.TestSentAt,
			&pub.ID, &pub.Slug, &pub.Name, &pub.Language, &pub.Topic, &pub.Audience,
			&pub.EditorialLine, &pub.Tone, &pub.Frequency, &sections, &paywall,
			&pub.AIDisclosure, &pub.Custom, &pairsJSON, &itemsJSON)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(flags), &iss.Flags)
	json.Unmarshal([]byte(commentary), &iss.Commentary)
	json.Unmarshal([]byte(issSections), &iss.Sections)
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
				Position: p.Position, OrigSection: p.OrigSection, AIDraft: p.AIDraft,
				Title: p.Title,
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
	// RevertApproved mirrors the editor's approval-revert rule: when the
	// stored issue is still 'approved', the edit puts it back to 'drafted',
	// drops its approval receipt and cancels the queued send — the EXISTS guards
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
		WITH ai AS (
			-- any author edit on an AI-drafted pauta clears its marker —
			-- the text is now theirs (KLOX-67)
			UPDATE issue_items SET ai_draft = FALSE
			WHERE workspace_id = $1 AND id = $6 AND $6 IS NOT NULL),
		u AS (
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
	// Hidden is the stage-1 filter flag (KLOX-109): "", "off_topic" or
	// "english".
	Hidden string
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
		       (SELECT COUNT(*) FROM story_items si WHERE si.story_id = s.id), s.stage1_hidden,
		       ii.id, ii.section, ii.score, ii.reason, ii.status, ii.origin, ii.position, ii.orig_section, ii.ai_draft, ii.title,
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
		  AND EXISTS (
		    SELECT 1 FROM story_items si JOIN items it2 ON it2.id = si.item_id
		    WHERE si.story_id = s.id
		      AND NOT EXISTS (
		        SELECT 1 FROM item_enrichment e
		        WHERE e.item_id = it2.id
		          AND (e.promotional OR e.ai_kind IN
		               ('lista','publieditorial','entretenimento','esporte','efemeride','tutorial','review'))))
		ORDER BY COALESCE(s.triage_score,0) DESC, s.id`, db.ws, pubID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RankedCandidate{}
	for rows.Next() {
		var (
			storyID, members        int64
			title, reason, hidden   string
			score                   float64
			iiID, iiPos             sql.NullInt64
			iiSec, iiStatus, iiOrig sql.NullString
			iiScore                 sql.NullFloat64
			iiReason, iiOrigSec     sql.NullString
			iiDraft                 sql.NullBool
			iiTitle                 sql.NullString
			dossier                 bool
		)
		if err := rows.Scan(&storyID, &title, &score, &reason, &members, &hidden,
			&iiID, &iiSec, &iiScore, &iiReason, &iiStatus, &iiOrig, &iiPos, &iiOrigSec, &iiDraft,
			&iiTitle, &dossier); err != nil {
			return nil, err
		}
		c := RankedCandidate{
			Story: &Story{ID: storyID, Title: title, TriageScore: score, TriageReason: reason},
			Rank:  len(out) + 1, Duplicates: int(members) - 1, Dossier: dossier, Hidden: hidden,
		}
		if iiID.Valid {
			c.Item = &IssueItem{
				ID: iiID.Int64, IssueID: issueID, StoryID: storyID,
				Section: iiSec.String, Score: iiScore.Float64, Reason: iiReason.String,
				Status: iiStatus.String, Origin: iiOrig.String,
				Position: int(iiPos.Int64), OrigSection: iiOrigSec.String,
				AIDraft: iiDraft.Bool, Title: iiTitle.String,
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UnusedCandidateCount reports how many triaged stories remain
// available for a new edition — not placed in any issue of the
// publication and carrying at least one editorially usable member.
// "Preparar próxima edição" can still draft from this pool (KLOX-100):
// zero newly fetched items means "nothing new" only when the pool is
// empty too.
func (db *DB) UnusedCandidateCount(ctx context.Context, pubID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM stories s
		WHERE s.workspace_id = $1 AND s.publication_id = $2
		  AND s.triage_score IS NOT NULL
		  AND s.id NOT IN (
		    SELECT ii.story_id FROM issue_items ii
		    JOIN issues i ON i.id = ii.issue_id
		    WHERE ii.workspace_id = $1 AND i.publication_id = $2)
		  AND EXISTS (
		    SELECT 1 FROM story_items si JOIN items it2 ON it2.id = si.item_id
		    WHERE si.story_id = s.id
		      AND NOT EXISTS (
		        SELECT 1 FROM item_enrichment e
		        WHERE e.item_id = it2.id
		          AND (e.promotional OR e.ai_kind IN
		               ('lista','publieditorial','entretenimento','esporte','efemeride','tutorial','review'))))`,
		db.ws, pubID).Scan(&n)
	return n, err
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
