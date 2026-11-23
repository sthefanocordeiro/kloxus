package store

// Semantic-layer persistence (KLOX-93): the deterministic per-item
// embedding lives in item_embedding — a pgvector column when the
// database ships the extension, a TEXT column holding the same
// '[f,f,...]' literal otherwise. The table is global like the rest of
// the shared AI layer: one vector per item, computed once, reused by
// every newsletter.
import (
	"context"
	"database/sql"
)

// ItemEmbedding is one stored semantic row.
type ItemEmbedding struct {
	ItemID int64
	// Vector is the '[f,f,...]' literal — identical whether the column
	// is pgvector or the TEXT fallback.
	Vector string
	Model  string // e.g. local-hash-v1
}

// probeVector caches the pgvector probe per Pool — the extension is
// either there at boot or it is not. Only a completed lookup caches:
// a query that fails (cancelled context on the first call, a transient
// connection drop) retries on the next call rather than permanently
// disabling pgvector — a false negative would make SaveEmbeddings
// insert the '[f,f,...]' text literal into a real vector column.
func (p *Pool) probeVector(ctx context.Context) bool {
	p.vectorMu.Lock()
	defer p.vectorMu.Unlock()
	if p.vectorDone {
		return p.vectorOK
	}
	var n int
	if err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_extension WHERE extname = 'vector'`).Scan(&n); err != nil {
		return false
	}
	p.vectorOK = n > 0
	p.vectorDone = true
	return p.vectorOK
}

// VectorEnabled reports whether the database has pgvector — the
// embedding column is then the real vector type and similarity can run
// in SQL; without it the column is TEXT and callers compare in Go.
func (p *Pool) VectorEnabled(ctx context.Context) bool {
	return p.probeVector(ctx)
}

// SaveEmbeddings upserts embedding rows. Replaying the same item list
// stores the same vectors — the embedder is deterministic, so the
// sweep is idempotent by construction.
func (p *Pool) SaveEmbeddings(ctx context.Context, rows []ItemEmbedding) error {
	cast := ""
	if p.probeVector(ctx) {
		cast = "::vector"
	}
	for _, e := range rows {
		if _, err := p.db.ExecContext(ctx,
			`INSERT INTO item_embedding (item_id, embedding, model)
			 VALUES ($1, $2`+cast+`, $3)
			 ON CONFLICT (item_id) DO UPDATE SET
			   embedding = EXCLUDED.embedding, model = EXCLUDED.model,
			   embedded_at = now_utc()`,
			e.ItemID, e.Vector, e.Model); err != nil {
			return err
		}
	}
	return nil
}

// UnembeddedRow is an item awaiting its semantic pass.
type UnembeddedRow struct {
	ID          int64
	Title       string
	ContentText string
	MetaJSON    string // raw meta_json — DJ categories ride in it
}

// UnembeddedItems lists items missing an embedding row at the given
// model version, newest first — the semantic sweep's work queue and
// the backfill path for everything stored before embeddings existed.
// Promo rows are skipped: they never enter candidate sets.
func (p *Pool) UnembeddedItems(ctx context.Context, model string, limit int) ([]UnembeddedRow, error) {
	return p.UnembeddedItemsFor(ctx, "", model, limit)
}

// UnembeddedItemsFor is UnembeddedItems scoped to one workspace: only
// items owned by ws (plus catalog items, which have no workspace —
// they are shared) are listed. An empty ws keeps the global scan.
// Tests use the scoped form so a sweep in one package never classifies
// another package's test items on the shared DATABASE_URL.
func (p *Pool) UnembeddedItemsFor(ctx context.Context, ws, model string, limit int) ([]UnembeddedRow, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT it.id, it.title, it.content_text, it.meta_json
		FROM items it
		LEFT JOIN item_embedding ie ON ie.item_id = it.id AND ie.model = $1
		WHERE ie.item_id IS NULL AND it.kind <> 'promo'
		  AND ($2 = '' OR it.workspace_id IS NULL OR it.workspace_id = $2)
		ORDER BY it.fetched_at DESC, it.id DESC
		LIMIT $3`, model, ws, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnembeddedRow
	for rows.Next() {
		var u UnembeddedRow
		if err := rows.Scan(&u.ID, &u.Title, &u.ContentText, &u.MetaJSON); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// EmbeddingText loads the stored vector literal for items — the
// '[f,f,...]' form regardless of column type, for Go-side cosine.
func (p *Pool) EmbeddingText(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT item_id, embedding::text FROM item_embedding WHERE item_id = ANY($1)`, ids)
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
		out[id] = lit
	}
	return out, rows.Err()
}

// NearestItems returns up to limit items ordered by vector distance to
// the query literal — the pgvector fast path for topic/section
// matching. It answers sql.ErrNoRows when the database has no vector
// extension; callers then fall back to Go-side cosine over
// EmbeddingText.
func (p *Pool) NearestItems(ctx context.Context, queryLit string, limit int) ([]int64, error) {
	if !p.probeVector(ctx) {
		return nil, sql.ErrNoRows
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT ie.item_id
		FROM item_embedding ie
		JOIN items it ON it.id = ie.item_id AND it.kind <> 'promo'
		ORDER BY ie.embedding <=> $1::vector
		LIMIT $2`, queryLit, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
