package store

// Renditions (KLOX-125): per-format exports of an approved piece.
import (
	"context"
	"database/sql"
)

// Rendition is one format-specific, independently editable copy of a
// piece. Only the body column matching its format is used.
type Rendition struct {
	ID        int64  `json:"id"`
	IssueID   int64  `json:"issue_id"`
	Format    string `json:"format"`
	BodyText  string `json:"body_text"`
	BodyHTML  string `json:"body_html"`
	BodyMD    string `json:"body_md"`
	BaseHash  string `json:"-"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const renditionCols = `id, issue_id, format, body_text, body_html, body_md, base_hash, created_at, updated_at`

func scanRendition(sc interface{ Scan(...any) error }) (*Rendition, error) {
	var r Rendition
	err := sc.Scan(&r.ID, &r.IssueID, &r.Format, &r.BodyText, &r.BodyHTML, &r.BodyMD,
		&r.BaseHash, &r.CreatedAt, &r.UpdatedAt)
	return &r, err
}

// Renditions lists an issue's renditions, oldest first.
func (db *DB) Renditions(ctx context.Context, issueID int64) ([]Rendition, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+renditionCols+` FROM renditions
		 WHERE workspace_id = $1 AND issue_id = $2 ORDER BY id`, db.ws, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rendition{}
	for rows.Next() {
		r, err := scanRendition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetRendition reads one rendition of the issue — sql.ErrNoRows when it
// belongs to another issue or workspace.
func (db *DB) GetRendition(ctx context.Context, issueID, id int64) (*Rendition, error) {
	return scanRendition(db.QueryRowContext(ctx,
		`SELECT `+renditionCols+` FROM renditions
		 WHERE workspace_id = $1 AND issue_id = $2 AND id = $3`, db.ws, issueID, id))
}

// CreateRendition forks a rendition; one per (issue, format), so a
// repeat fork returns the existing row untouched.
func (db *DB) CreateRendition(ctx context.Context, issueID int64, format, text, html, md, baseHash string) (*Rendition, error) {
	_, err := db.ExecContext(ctx,
		`INSERT INTO renditions (workspace_id, issue_id, format, body_text, body_html, body_md, base_hash)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (workspace_id, issue_id, format) DO NOTHING`,
		db.ws, issueID, format, text, html, md, baseHash)
	if err != nil {
		return nil, err
	}
	return scanRendition(db.QueryRowContext(ctx,
		`SELECT `+renditionCols+` FROM renditions
		 WHERE workspace_id = $1 AND issue_id = $2 AND format = $3`, db.ws, issueID, format))
}

// UpdateRenditionBody saves an author edit; the base hash is untouched
// so "base changed" keeps tracking the piece, not the edits.
func (db *DB) UpdateRenditionBody(ctx context.Context, issueID, id int64, text, html, md string) (*Rendition, error) {
	return scanRendition(db.QueryRowContext(ctx,
		`UPDATE renditions SET body_text=$4, body_html=$5, body_md=$6, updated_at=now_utc()
		 WHERE workspace_id = $1 AND issue_id = $2 AND id = $3
		 RETURNING `+renditionCols, db.ws, issueID, id, text, html, md))
}

// RefreshRendition overwrites the bodies with a fresh template fork and
// records the new base hash.
func (db *DB) RefreshRendition(ctx context.Context, issueID, id int64, text, html, md, baseHash string) (*Rendition, error) {
	return scanRendition(db.QueryRowContext(ctx,
		`UPDATE renditions SET body_text=$4, body_html=$5, body_md=$6, base_hash=$7, updated_at=now_utc()
		 WHERE workspace_id = $1 AND issue_id = $2 AND id = $3
		 RETURNING `+renditionCols, db.ws, issueID, id, text, html, md, baseHash))
}

// DeleteRendition removes a rendition — sql.ErrNoRows when absent.
func (db *DB) DeleteRendition(ctx context.Context, issueID, id int64) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM renditions WHERE workspace_id = $1 AND issue_id = $2 AND id = $3`,
		db.ws, issueID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
