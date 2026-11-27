package store

import (
	"context"
	"strconv"
	"strings"
)

// ParseVector reads a pgvector/TEXT '[f,f,...]' literal. Empty or
// malformed input returns nil.
func ParseVector(s string) []float32 {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]float32, 0, len(parts))
	for _, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil
		}
		out = append(out, float32(f))
	}
	return out
}

// ItemVectors loads the Voyage vectors (model) for the given items.
// Items without a vector are absent from the map.
func (db *DB) ItemVectors(ctx context.Context, model string, ids []int64) (map[int64][]float32, error) {
	out := map[int64][]float32{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT item_id, embedding::text FROM item_embeddings
		 WHERE model = $1 AND item_id = ANY($2)`, model, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var lit string
		if err := rows.Scan(&id, &lit); err != nil {
			return nil, err
		}
		if v := ParseVector(lit); v != nil {
			out[id] = v
		}
	}
	return out, rows.Err()
}

// FTSRank returns ts_rank_cd of each item's tsvector against an OR
// tsquery built from terms (already sanitized lowercase alphanumeric
// words). Items with no match are absent. Used when a story has no
// vector yet or embeddings are off (spec section 7).
func (db *DB) FTSRank(ctx context.Context, ids []int64, terms []string) (map[int64]float64, error) {
	out := map[int64]float64{}
	if len(ids) == 0 || len(terms) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, ts_rank_cd(fts, to_tsquery('portuguese', $2))
		 FROM items WHERE id = ANY($1) AND fts @@ to_tsquery('portuguese', $2)`,
		ids, strings.Join(terms, " | "))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var r float64
		if err := rows.Scan(&id, &r); err != nil {
			return nil, err
		}
		out[id] = r
	}
	return out, rows.Err()
}
