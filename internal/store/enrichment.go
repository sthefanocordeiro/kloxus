package store

// Shared AI layer persistence (KLOX-67): item enrichment and story
// research are global tables — one enrichment per article, one research
// payload per story, reused by every newsletter — so they are Pool
// methods like the catalog. paid_actions and the issue_items.ai_draft
// marker are per-workspace and stay DB methods.
import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// ItemEnrichment is the shared per-item AI classification written once
// and never recomputed.
type ItemEnrichment struct {
	ItemID      int64
	Kind        string // ai_kind slug; see pipeline.EnrichmentKinds
	Promotional bool
	Topics      []string // <=5 fixed-list tags
	SummaryPT   string   // one original PT sentence, <=25 words
	Language    string
	Model       string
}

// excludedEnrichmentKinds are the non-editorial kinds (§7.2) that never
// become suggestions: buying guides, entertainment, sport, almanac and
// advertorial content, plus tutorials and product reviews (KLOX-158).
// Keep in sync with the SQL kind lists in pipeline.go.
var excludedEnrichmentKinds = map[string]bool{
	"lista":          true, // lista/guia de compra
	"publieditorial": true,
	"entretenimento": true,
	"esporte":        true,
	"efemeride":      true,
	"tutorial":       true, // skipped by default (KLOX-158)
	"review":         true, // product review — skipped by default (KLOX-158)
}

// Excluded reports whether the enrichment rules this item out of every
// suggestion set.
func (e *ItemEnrichment) Excluded() bool {
	return e.Promotional || excludedEnrichmentKinds[e.Kind]
}

// SaveEnrichments upserts enrichment rows (idempotent: a second write
// for the same item stores the same data).
func (p *Pool) SaveEnrichments(ctx context.Context, list []ItemEnrichment) error {
	for _, e := range list {
		topics := []byte("[]")
		if e.Topics != nil {
			var err error
			topics, err = json.Marshal(e.Topics)
			if err != nil {
				return err
			}
		}
		if _, err := p.db.ExecContext(ctx,
			`INSERT INTO item_enrichment (item_id, ai_kind, promotional, topics_json, summary_pt, language, model)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)
			 ON CONFLICT (item_id) DO UPDATE SET
			   ai_kind = excluded.ai_kind, promotional = excluded.promotional,
			   topics_json = excluded.topics_json, summary_pt = excluded.summary_pt,
			   language = excluded.language, model = excluded.model`,
			e.ItemID, e.Kind, e.Promotional, string(topics), e.SummaryPT, e.Language, e.Model); err != nil {
			return err
		}
	}
	return nil
}

// EnrichmentsForItems batch-loads enrichment rows for the given item
// ids; missing items simply have no map entry. Available on both Pool
// and DB — the table is global.
func (p *Pool) EnrichmentsForItems(ctx context.Context, ids []int64) (map[int64]*ItemEnrichment, error) {
	return enrichmentsForItems(ctx, p.db, ids)
}

// EnrichmentsForItems is the workspace-scoped convenience for the same
// global table.
func (db *DB) EnrichmentsForItems(ctx context.Context, ids []int64) (map[int64]*ItemEnrichment, error) {
	return enrichmentsForItems(ctx, db.db, ids)
}

func enrichmentsForItems(ctx context.Context, q *sql.DB, ids []int64) (map[int64]*ItemEnrichment, error) {
	out := map[int64]*ItemEnrichment{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx,
		`SELECT item_id, ai_kind, promotional, topics_json, summary_pt, language, model
		 FROM item_enrichment WHERE item_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e ItemEnrichment
		var topics string
		if err := rows.Scan(&e.ItemID, &e.Kind, &e.Promotional, &topics, &e.SummaryPT, &e.Language, &e.Model); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(topics), &e.Topics)
		out[e.ItemID] = &e
	}
	return out, rows.Err()
}

// UnenrichedItem is a to-be-enriched row: article text from either the
// shared catalog or a workspace source that has no enrichment yet.
type UnenrichedItem struct {
	ID         int64
	Title      string
	SourceName string
	URL        string
	Excerpt    string
}

// UnenrichedItems lists up to limit article-kind items with no
// enrichment row, newest first. Promo rows are already out by the
// deterministic prefilter and never get an AI classification. Rows
// carrying only a 'local-%' (deterministic, KLOX-93) classification
// still count as un-enriched — the AI sweep upgrades them in place.
func (p *Pool) UnenrichedItems(ctx context.Context, limit int) ([]UnenrichedItem, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT it.id, it.title,
		       COALESCE(s.name, c.name, '') AS source_name,
		       it.url, left(it.content_text, 1500)
		FROM items it
		LEFT JOIN sources s ON s.id = it.source_id
		LEFT JOIN source_catalog c ON c.id = it.catalog_id
		LEFT JOIN item_enrichment e ON e.item_id = it.id AND e.model NOT LIKE 'local-%'
		WHERE e.item_id IS NULL AND it.kind = 'article'
		ORDER BY it.fetched_at DESC, it.id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnenrichedItem
	for rows.Next() {
		var u UnenrichedItem
		if err := rows.Scan(&u.ID, &u.Title, &u.SourceName, &u.URL, &u.Excerpt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// StorySummaries returns each story's one-line AI summary — the
// summary_pt of its first enriched member item (§7.2: story cards show
// a one-line summary). Stories without enrichment have no map entry.
func (db *DB) StorySummaries(ctx context.Context, storyIDs []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(storyIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT ON (si.story_id) si.story_id, e.summary_pt
		FROM story_items si
		JOIN item_enrichment e ON e.item_id = si.item_id
		WHERE si.story_id = ANY($1) AND e.summary_pt <> ''
		ORDER BY si.story_id, si.item_id`, storyIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var summary string
		if err := rows.Scan(&id, &summary); err != nil {
			return nil, err
		}
		out[id] = summary
	}
	return out, rows.Err()
}

// StoryCard is the per-story enrichment a suggestion card shows
// (KLOX-95): the first enriched member's summary, kind and language,
// the union of member topic tags, and the newest member's publication
// time — title, kind and language answer "what kind of story is this",
// the topics feed the off-topic filter, the time feeds "published
// when". Fields a member never classified stay empty — the card
// degrades to title/outlet, never a placeholder.
type StoryCard struct {
	Summary     string
	Kind        string
	Language    string
	Topics      []string
	PublishedAt string // RFC3339; "" when no member parses a date

	g4Summary, g4Kind string
}

// StoryCards batch-loads the card enrichment for the given stories in
// one query: member items in id order, reduced per story. Every story
// with members gets a card — publish time is item metadata, not AI
// output — but a story whose members were never classified keeps the
// AI fields empty; only member-less stories have no entry.
func (db *DB) StoryCards(ctx context.Context, storyIDs []int64) (map[int64]*StoryCard, error) {
	out := map[int64]*StoryCard{}
	if len(storyIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT si.story_id,
		       e.ai_kind, e.summary_pt, e.language, e.topics_json,
		       COALESCE(safe_ts(it.published_at), safe_ts(it.fetched_at)) AS ts,
		       g.summary, g.kind
		FROM story_items si
		JOIN items it ON it.id = si.item_id
		LEFT JOIN item_enrichment e ON e.item_id = si.item_id
		LEFT JOIN item_summaries g ON g.item_id = si.item_id
		WHERE si.workspace_id = $1 AND si.story_id = ANY($2)
		ORDER BY si.story_id, si.item_id`, db.ws, storyIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	newest := map[int64]time.Time{}
	for rows.Next() {
		var sid int64
		var kind, summary, lang, topics, gSummary, gKind sql.NullString
		var ts sql.NullTime
		if err := rows.Scan(&sid, &kind, &summary, &lang, &topics, &ts, &gSummary, &gKind); err != nil {
			return nil, err
		}
		c := out[sid]
		if c == nil {
			c = &StoryCard{}
			out[sid] = c
		}
		// G4 (KLOX-111) top-30 summaries: used only where the shared
		// enrichment has nothing for this story.
		if c.g4Kind == "" && gKind.Valid {
			c.g4Kind = gKind.String
		}
		if c.g4Summary == "" && gSummary.Valid {
			c.g4Summary = gSummary.String
		}
		if c.Kind == "" && kind.Valid {
			c.Kind = kind.String
		}
		if c.Summary == "" && summary.Valid {
			c.Summary = summary.String
		}
		if c.Language == "" && lang.Valid {
			c.Language = lang.String
		}
		if topics.Valid {
			var tags []string
			json.Unmarshal([]byte(topics.String), &tags)
			for _, tag := range tags {
				dup := false
				for _, have := range c.Topics {
					if have == tag {
						dup = true
						break
					}
				}
				if !dup {
					c.Topics = append(c.Topics, tag)
				}
			}
		}
		if ts.Valid && ts.Time.After(newest[sid]) {
			newest[sid] = ts.Time
			c.PublishedAt = ts.Time.UTC().Format(time.RFC3339)
		}
	}
	for _, c := range out {
		if c.Summary == "" {
			c.Summary = c.g4Summary
		}
		if c.Kind == "" {
			c.Kind = c.g4Kind
		}
	}
	return out, rows.Err()
}

// StoryResearch is the cached research/draft payload for one story.
type StoryResearch struct {
	Key   string
	JSON  string
	Model string
}

// ResearchGet returns the cached payload (sql.ErrNoRows on miss).
func (p *Pool) ResearchGet(ctx context.Context, key string) (*StoryResearch, error) {
	var r StoryResearch
	err := p.db.QueryRowContext(ctx,
		`SELECT research_key, json, model FROM story_research WHERE research_key = $1`, key).
		Scan(&r.Key, &r.JSON, &r.Model)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ResearchSave stores a story's research payload (idempotent upsert).
func (p *Pool) ResearchSave(ctx context.Context, key, payload, model string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO story_research (research_key, json, model) VALUES ($1,$2,$3)
		 ON CONFLICT (research_key) DO UPDATE SET json = excluded.json, model = excluded.model`,
		key, payload, model)
	return err
}

// Paid-action kinds counted by the §7.3 counters.
const (
	PaidActionResearch  = "research"
	PaidActionCiteCheck = "citecheck"
	PaidActionRerank    = "rerank"
	// PaidActionScrape counts Firecrawl scrapes for the "paste a link"
	// proposal flow (KLOX-99), capped by KLOXUS_SCRAPES_PER_DAY with
	// ref_id 0 — no publication exists yet during onboarding.
	PaidActionScrape = "scrape"
)

// PaidActionCount returns how many paid actions of kind against refID
// the workspace recorded today (UTC). refID 0 scopes to the kind only.
func (db *DB) PaidActionCount(ctx context.Context, kind string, refID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(calls),0) FROM paid_actions
		 WHERE workspace_id = $1 AND kind = $2 AND ref_id = $3 AND day = to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD')`,
		db.ws, kind, refID).Scan(&n)
	return n, err
}

// PaidActionsToday returns the workspace's total paid actions today
// (UTC) across kinds — the ≤5/day bound of §7.3.
func (db *DB) PaidActionsToday(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(calls),0) FROM paid_actions
		 WHERE workspace_id = $1 AND day = to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD')`,
		db.ws).Scan(&n)
	return n, err
}

// RecordPaidAction increments the workspace's (kind, ref) counter for
// today after a paid call went through.
func (db *DB) RecordPaidAction(ctx context.Context, kind string, refID int64) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO paid_actions (workspace_id, kind, ref_id, calls) VALUES ($1,$2,$3,1)
		 ON CONFLICT (workspace_id, kind, ref_id, day)
		 DO UPDATE SET calls = paid_actions.calls + 1`,
		db.ws, kind, refID)
	return err
}

// SetIssueItemAIDraft flips the AI-draft marker on one issue item —
// set when a pauta body arrives AI-written, cleared when the author
// touches the item.
func (db *DB) SetIssueItemAIDraft(ctx context.Context, iiID int64, on bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE issue_items SET ai_draft = $1 WHERE workspace_id = $2 AND id = $3`,
		on, db.ws, iiID)
	return err
}
