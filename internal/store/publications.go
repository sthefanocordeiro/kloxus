package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/sthefanocordeiro/kloxus/internal/config"
)

// Publication is a stored publication row with decoded JSON fields.
type Publication struct {
	ID            int64
	Slug          string
	Name          string
	Language      string
	Topic         string
	Audience      string
	EditorialLine string
	// RejectTerms: creator-declared themes the aderência ranking
	// demotes and the auto-lineup never slots (KLOX-94).
	RejectTerms  string
	Tone         string
	Frequency    string
	Sections     []config.Section
	Paywall      config.Paywall
	AIDisclosure string
	Custom       bool
	Template     []TemplateBlock
	// TemplateRaw is the stored template_json — the compare-and-swap
	// key template saves check so a concurrent write conflicts instead
	// of clobbering (KLOX-72).
	TemplateRaw string
}

// TemplateBlock is one entry in the newsletter's default edition
// template (KLOX-72): a fixed composer block copied into every new
// edition — or a "section" slot the week's pautas fill at
// materialization. Kind/text/url mirror the composer block schema;
// section carries the section title for kind=section.
type TemplateBlock struct {
	Kind    string `json:"kind"` // section | heading | divider | link | image | embed | text
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Section string `json:"section,omitempty"`
}

func (db *DB) scanPublication(row *sql.Row) (*Publication, error) {
	var p Publication
	var sections, paywall, template string
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Language, &p.Topic, &p.Audience,
		&p.EditorialLine, &p.RejectTerms, &p.Tone, &p.Frequency, &sections, &paywall, &p.AIDisclosure, &p.Custom, &template)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(sections), &p.Sections); err != nil {
		return nil, fmt.Errorf("decoding sections for %q: %w", p.Slug, err)
	}
	if err := json.Unmarshal([]byte(paywall), &p.Paywall); err != nil {
		return nil, fmt.Errorf("decoding paywall for %q: %w", p.Slug, err)
	}
	if err := json.Unmarshal([]byte(template), &p.Template); err != nil {
		return nil, fmt.Errorf("decoding template for %q: %w", p.Slug, err)
	}
	p.TemplateRaw = template
	return &p, nil
}

const publicationCols = `id, slug, name, language, topic, audience, editorial_line, reject_terms, tone, frequency, sections_json, paywall_json, ai_disclosure, custom, template_json`

// GetPublication resolves a publication by numeric id or slug.
func (db *DB) GetPublication(ctx context.Context, ref string) (*Publication, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return db.scanPublication(db.QueryRowContext(ctx,
			`SELECT `+publicationCols+` FROM publications WHERE workspace_id = $1 AND id = $2`, db.ws, id))
	}
	return db.scanPublication(db.QueryRowContext(ctx,
		`SELECT `+publicationCols+` FROM publications WHERE workspace_id = $1 AND slug = $2`, db.ws, ref))
}

// ListPublications returns all publications ordered by name.
func (db *DB) ListPublications(ctx context.Context) ([]*Publication, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+publicationCols+` FROM publications WHERE workspace_id = $1 ORDER BY name`, db.ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Publication
	for rows.Next() {
		var p Publication
		var sections, paywall, template string
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Language, &p.Topic, &p.Audience,
			&p.EditorialLine, &p.RejectTerms, &p.Tone, &p.Frequency, &sections, &paywall, &p.AIDisclosure, &p.Custom, &template); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(sections), &p.Sections)
		json.Unmarshal([]byte(paywall), &p.Paywall)
		json.Unmarshal([]byte(template), &p.Template)
		p.TemplateRaw = template
		out = append(out, &p)
	}
	return out, rows.Err()
}

// HasCanonicalPublication reports whether any publication is template-seeded
// (not creator-made). The workspace config refresh only touches canonical
// rows — a publication the creator wrote keeps its edited blocks.
func (db *DB) HasCanonicalPublication(ctx context.Context) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM publications WHERE workspace_id = $1 AND NOT custom`, db.ws).Scan(&n)
	return n > 0, err
}

// CreatePublication stores a creator-made publication (custom=true). Unlike
// SeedPublication it does not attach sources — onboarding offers them on a
// separate screen — and it slugifies the name into a unique slug.
func (db *DB) CreatePublication(ctx context.Context, p *config.Publication) (int64, error) {
	sections, _ := json.Marshal(p.Sections)
	paywall, _ := json.Marshal(p.Paywall)
	slug := p.Slug
	for i := 2; ; i++ {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM publications WHERE workspace_id = $1 AND slug = $2`, db.ws, slug).Scan(&n); err != nil {
			return 0, err
		}
		if n == 0 {
			break
		}
		slug = fmt.Sprintf("%s-%d", p.Slug, i)
	}
	var id int64
	err := db.QueryRowContext(ctx, `INSERT INTO publications
		(workspace_id, slug, name, language, topic, audience, editorial_line, reject_terms, tone, frequency, sections_json, paywall_json, ai_disclosure, custom)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,TRUE)
		RETURNING id`,
		db.ws, slug, p.Name, p.Language, p.Topic, p.Audience, p.EditorialLine, p.RejectTerms,
		p.Tone, p.Frequency, string(sections), string(paywall), p.AIDisclosure).Scan(&id)
	return id, err
}

// SeedPublication inserts a publication definition and its sources.
// Idempotent on slug; returns the publication id.
func (db *DB) SeedPublication(ctx context.Context, p *config.Publication) (int64, error) {
	var id int64
	sections, _ := json.Marshal(p.Sections)
	paywall, _ := json.Marshal(p.Paywall)
	err := db.QueryRowContext(ctx,
		`SELECT id FROM publications WHERE workspace_id = $1 AND slug = $2`, db.ws, p.Slug).Scan(&id)
	if err == nil {
		// Existing workspace: refresh publication config from the current
		// template (paywall price, sections, etc.) while keeping the
		// visitor's issues, decisions, sources and readers.
		if _, err := db.ExecContext(ctx, `UPDATE publications SET
			name=$1, language=$2, topic=$3, audience=$4, editorial_line=$5, reject_terms=$6, tone=$7,
			frequency=$8, sections_json=$9, paywall_json=$10, ai_disclosure=$11
			WHERE workspace_id = $12 AND id = $13`,
			p.Name, p.Language, p.Topic, p.Audience, p.EditorialLine, p.RejectTerms, p.Tone,
			p.Frequency, string(sections), string(paywall), p.AIDisclosure, db.ws, id); err != nil {
			return 0, err
		}
		for _, s := range p.Sources {
			if _, err := db.ExecContext(ctx,
				`INSERT INTO sources (workspace_id, publication_id, type, url) VALUES ($1,$2,$3,$4)
				 ON CONFLICT (publication_id, url) DO NOTHING`,
				db.ws, id, s.Type, s.URL); err != nil {
				return 0, err
			}
		}
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	err = db.QueryRowContext(ctx, `INSERT INTO publications
		(workspace_id, slug, name, language, topic, audience, editorial_line, reject_terms, tone, frequency, sections_json, paywall_json, ai_disclosure)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id`,
		db.ws, p.Slug, p.Name, p.Language, p.Topic, p.Audience, p.EditorialLine, p.RejectTerms,
		p.Tone, p.Frequency, string(sections), string(paywall), p.AIDisclosure).Scan(&id)
	if err != nil {
		return 0, err
	}
	for _, s := range p.Sources {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO sources (workspace_id, publication_id, type, url) VALUES ($1,$2,$3,$4)
			 ON CONFLICT (publication_id, url) DO NOTHING`,
			db.ws, id, s.Type, s.URL); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// UpdatePublication rewrites the creator-editable fields of a
// publication: identity copy, cadence, the section definitions the
// next edition uses, and the paywall block. Slug, language and the
// custom flag stay put — they anchor public URLs and template drift.
func (db *DB) UpdatePublication(ctx context.Context, id int64, p *config.Publication) error {
	sections, _ := json.Marshal(p.Sections)
	paywall, _ := json.Marshal(p.Paywall)
	res, err := db.ExecContext(ctx, `UPDATE publications SET
		name=$1, topic=$2, audience=$3, editorial_line=$4, reject_terms=$5, tone=$6, frequency=$7,
		sections_json=$8, paywall_json=$9, ai_disclosure=$10
		WHERE workspace_id = $11 AND id = $12`,
		p.Name, p.Topic, p.Audience, p.EditorialLine, p.RejectTerms, p.Tone, p.Frequency,
		string(sections), string(paywall), p.AIDisclosure, db.ws, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetPublicationTemplate replaces the newsletter's default edition
// template (KLOX-72). want is the template_json the caller read — the
// update is a compare-and-swap on it, so a concurrent save fails with
// sql.ErrNoRows instead of silently clobbering (the API maps that to a
// 409 like the document version contract).
func (db *DB) SetPublicationTemplate(ctx context.Context, pubID int64, want string, blocks []TemplateBlock) error {
	data, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx,
		`UPDATE publications SET template_json = $3
		 WHERE workspace_id = $1 AND id = $2 AND template_json = $4`,
		db.ws, pubID, string(data), want)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeletePublication removes a publication and every row it owns:
// issues and their items/dossiers/decisions, runs, stories and raw
// items, sources, readers and subscriptions, guides and payment
// accounts. The send ledger (outbox_emails) is workspace-scoped by
// design and stays — it is the legal record of what went out. Returns
// sql.ErrNoRows when the publication does not exist in the workspace.
func (db *DB) DeletePublication(ctx context.Context, pubID int64) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	issuesOfPub := `SELECT id FROM issues WHERE workspace_id = $1 AND publication_id = $2`
	itemsOfIssues := `SELECT id FROM issue_items WHERE workspace_id = $1 AND issue_id IN (` + issuesOfPub + `)`
	for _, q := range []string{
		`DELETE FROM decisions WHERE workspace_id = $1 AND (publication_id = $2 OR issue_item_id IN (` + itemsOfIssues + `))`,
		`DELETE FROM dossiers WHERE workspace_id = $1 AND issue_item_id IN (` + itemsOfIssues + `)`,
		`DELETE FROM issue_items WHERE workspace_id = $1 AND issue_id IN (` + issuesOfPub + `)`,
		`DELETE FROM jobs WHERE workspace_id = $1 AND kind = 'send_issue' AND ref_id IN (` + issuesOfPub + `)`,
		`DELETE FROM runs WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM renditions WHERE workspace_id = $1 AND issue_id IN (` + issuesOfPub + `)`,
		`DELETE FROM issue_exports WHERE workspace_id = $1 AND issue_id IN (` + issuesOfPub + `)`,
		`DELETE FROM issues WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM story_items WHERE workspace_id = $1 AND (story_id IN (SELECT id FROM stories WHERE workspace_id = $1 AND publication_id = $2)
			OR item_id IN (SELECT id FROM items WHERE workspace_id = $1 AND source_id IN (SELECT id FROM sources WHERE workspace_id = $1 AND publication_id = $2)))`,
		`DELETE FROM stories WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM items WHERE workspace_id = $1 AND source_id IN (SELECT id FROM sources WHERE workspace_id = $1 AND publication_id = $2)`,
		`DELETE FROM sources WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM subscriptions WHERE workspace_id = $1 AND reader_id IN (SELECT id FROM readers WHERE workspace_id = $1 AND publication_id = $2)`,
		`DELETE FROM readers WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM editorial_guides WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM media WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM standard_messages WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM payment_accounts WHERE workspace_id = $1 AND publication_id = $2`,
		`DELETE FROM pauta_days WHERE workspace_id = $1 AND publication_id = $2`,
	} {
		if _, err := tx.ExecContext(ctx, q, db.ws, pubID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM publications WHERE workspace_id = $1 AND id = $2`, db.ws, pubID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}
