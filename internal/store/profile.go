package store

import (
	"context"
	"database/sql"
)

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
