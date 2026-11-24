package store

// Stage-1 ranking persistence (KLOX-109): item vectors, the cached
// publication profile and the FTS fallback rank. Vector columns are
// pgvector on Supabase and TEXT '[f,f,...]' on CI's plain postgres, so
// every read casts to text and parses in Go - the formula stays in Go.

import (
	"context"
	"database/sql"
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
	rows, err := db.db.QueryContext(ctx,
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

// PublicationProfile is the cached per-publication vectors (G1 table).
type PublicationProfile struct {
	ThemeVec     []float32
	SkipVec      []float32
	AcceptedMean []float32
	RejectedMean []float32
	NAccepted    int
	NRejected    int
	ProfileHash  string
}

// GetPublicationProfile returns the stored profile (sql.ErrNoRows when
// the publication has none yet).
func (db *DB) GetPublicationProfile(ctx context.Context, pubID int64) (*PublicationProfile, error) {
	var theme, skip, acc, rej sql.NullString
	p := &PublicationProfile{}
	err := db.db.QueryRowContext(ctx,
		`SELECT pp.theme_vec::text, pp.skip_vec::text, pp.accepted_mean::text, pp.rejected_mean::text,
		        pp.n_accepted, pp.n_rejected, pp.profile_hash
		 FROM publication_profiles pp
		 JOIN publications pu ON pu.id = pp.publication_id
		 WHERE pp.publication_id = $1 AND pu.workspace_id = $2`, pubID, db.ws).
		Scan(&theme, &skip, &acc, &rej, &p.NAccepted, &p.NRejected, &p.ProfileHash)
	if err != nil {
		return nil, err
	}
	p.ThemeVec, p.SkipVec = ParseVector(theme.String), ParseVector(skip.String)
	p.AcceptedMean, p.RejectedMean = ParseVector(acc.String), ParseVector(rej.String)
	return p, nil
}

// SavePublicationProfile upserts theme_vec, skip_vec and profile_hash.
// The Rocchio means and counts are owned by feedback (G5) and are left
// untouched on conflict. literals are '[f,...]' strings ("" = NULL).
func (db *DB) SavePublicationProfile(ctx context.Context, pubID int64, themeLit, skipLit, hash string) error {
	cast := ""
	if db.vectorOK(ctx) {
		cast = "::vector"
	}
	nul := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	_, err := db.db.ExecContext(ctx,
		`INSERT INTO publication_profiles (publication_id, theme_vec, skip_vec, profile_hash)
		 SELECT $1, $2`+cast+`, $3`+cast+`, $4
		 WHERE EXISTS (SELECT 1 FROM publications WHERE id = $1 AND workspace_id = $5)
		 ON CONFLICT (publication_id) DO UPDATE SET
		   theme_vec = EXCLUDED.theme_vec, skip_vec = EXCLUDED.skip_vec,
		   profile_hash = EXCLUDED.profile_hash, updated_at = now_utc()`,
		pubID, nul(themeLit), nul(skipLit), hash, db.ws)
	return err
}

// vectorOK reports whether the database has pgvector (see Pool.probeVector).
func (db *DB) vectorOK(ctx context.Context) bool {
	var n int
	if err := db.db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_extension WHERE extname = 'vector'`).Scan(&n); err != nil {
		return false
	}
	return n > 0
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
	rows, err := db.db.QueryContext(ctx,
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
