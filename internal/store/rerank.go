package store

// Rerank score cache (KLOX-110): one score per (publication, item,
// profile_hash), reused for 24 h so a second run on the same day only
// sends items not yet scored.

import (
	"context"
	"time"
)

// RerankCacheTTL is how long a cached score stays valid.
const RerankCacheTTL = 24 * time.Hour

// RerankScores returns cached scores for the items, ignoring rows older
// than RerankCacheTTL (relative to now) or from another model.
func (db *DB) RerankScores(ctx context.Context, pubID int64, hash, model string, ids []int64, now time.Time) (map[int64]float64, error) {
	out := map[int64]float64{}
	if len(ids) == 0 {
		return out, nil
	}
	since := now.UTC().Add(-RerankCacheTTL).Format(tsLayout)
	rows, err := db.QueryContext(ctx,
		`SELECT rs.item_id, rs.score FROM rerank_scores rs
		 JOIN publications pu ON pu.id = rs.publication_id
		 WHERE rs.publication_id = $1 AND pu.workspace_id = $2 AND rs.profile_hash = $3
		   AND rs.model = $4 AND rs.item_id = ANY($5) AND rs.created_at >= $6`,
		pubID, db.ws, hash, model, ids, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var s float64
		if err := rows.Scan(&id, &s); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

// SaveRerankScores upserts scores, refreshing created_at.
func (db *DB) SaveRerankScores(ctx context.Context, pubID int64, hash, model string, scores map[int64]float64, now time.Time) error {
	ts := now.UTC().Format(tsLayout)
	for id, s := range scores {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO rerank_scores (publication_id, item_id, profile_hash, model, score, created_at)
			 SELECT $1, $2, $3, $4, $5, $6
			 WHERE EXISTS (SELECT 1 FROM publications WHERE id = $1 AND workspace_id = $7)
			 ON CONFLICT (publication_id, item_id, profile_hash)
			 DO UPDATE SET score = EXCLUDED.score, model = EXCLUDED.model, created_at = EXCLUDED.created_at`,
			pubID, id, hash, model, s, ts, db.ws); err != nil {
			return err
		}
	}
	return nil
}
