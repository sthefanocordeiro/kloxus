package store

// Ranking G4 (KLOX-111): global per-item card summaries. One row per
// item, written once and never replaced.
import "context"

// ItemSummary is the cached one-line card summary and type of an item.
type ItemSummary struct {
	ItemID  int64
	Summary string // <=160 chars, PT
	Kind    string // noticia|analise|lancamento|regulacao|opiniao|outro
	Model   string
}

// SaveItemSummaries inserts summaries; an item that already has one is
// left untouched (the cache is write-once).
func (p *Pool) SaveItemSummaries(ctx context.Context, list []ItemSummary) error {
	for _, s := range list {
		if _, err := p.db.ExecContext(ctx,
			`INSERT INTO item_summaries (item_id, summary, kind, model) VALUES ($1,$2,$3,$4)
			 ON CONFLICT (item_id) DO NOTHING`,
			s.ItemID, s.Summary, s.Kind, s.Model); err != nil {
			return err
		}
	}
	return nil
}

// ItemSummaryIDs returns which of the given items already have a
// cached summary.
func (p *Pool) ItemSummaryIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT item_id FROM item_summaries WHERE item_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
