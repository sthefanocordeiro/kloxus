package store

import "context"

// PubOverview is one publication line of the KLOX-110 evaluation
// export: enough to pick fixtures, no tenant content.
type PubOverview struct {
	ID          int64
	WorkspaceID string
	Name        string
	Language    string
	Topic       string
	Stories     int
}

// PublicationsOverview lists every publication with its stored story
// count (read-only; operator/eval tooling, never wired to a request).
func (p *Pool) PublicationsOverview(ctx context.Context) ([]PubOverview, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT p.id, p.workspace_id, p.name, p.language, p.topic,
		        (SELECT count(*) FROM stories s WHERE s.publication_id = p.id)
		 FROM publications p ORDER BY 6 DESC, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PubOverview
	for rows.Next() {
		var o PubOverview
		if err := rows.Scan(&o.ID, &o.WorkspaceID, &o.Name, &o.Language, &o.Topic, &o.Stories); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
