package store

import (
	"context"
	"strings"
)

// LatestGuide returns the newest editorial guide for a publication.
// Returns sql.ErrNoRows when none exists yet.
func (db *DB) LatestGuide(ctx context.Context, pubID int64) (version int, content string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT version, content FROM editorial_guides WHERE workspace_id = $1 AND publication_id = $2 ORDER BY version DESC LIMIT 1`,
		db.ws, pubID).Scan(&version, &content)
	return version, content, err
}

// LatestGuides returns up to n newest guide versions, newest first.
func (db *DB) LatestGuides(ctx context.Context, pubID int64, n int) ([]struct {
	Version int
	Content string
}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT version, content FROM editorial_guides WHERE workspace_id = $1 AND publication_id = $2 ORDER BY version DESC LIMIT $3`,
		db.ws, pubID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		Version int
		Content string
	}
	for rows.Next() {
		var g struct {
			Version int
			Content string
		}
		if err := rows.Scan(&g.Version, &g.Content); err != nil {
			return nil, err
		}
		out = append(out, g)
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
