package store

// Ranking-engine persistence (KLOX-108 G1): Voyage item vectors and
// the embed/rerank lines of the cost ledger. Vectors live in
// item_embeddings (vector(512) on pgvector databases, TEXT literal on
// CI's plain postgres); same dual shape as item_embedding (KLOX-93).
import (
	"context"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/embed"
)

// PendingEmbeddings lists non-promo items lacking a vector for model,
// newest first, inside the candidate window.
func (p *Pool) PendingEmbeddings(ctx context.Context, model string, since time.Time, limit int) ([]embed.Pending, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT it.id, it.title, it.content_text
		FROM items it
		LEFT JOIN item_embeddings ie ON ie.item_id = it.id AND ie.model = $1
		WHERE ie.item_id IS NULL AND it.kind <> 'promo'
		  AND COALESCE(it.published_ts, it.fetched_ts) >= $2
		ORDER BY COALESCE(it.published_ts, it.fetched_ts) DESC NULLS LAST, it.id DESC
		LIMIT $3`, model, since.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []embed.Pending
	for rows.Next() {
		var e embed.Pending
		if err := rows.Scan(&e.ID, &e.Title, &e.Body); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PendingStats counts pending items and the characters embed.ItemText
// would send for them.
func (p *Pool) PendingStats(ctx context.Context, model string, since time.Time) (int, int64, error) {
	var n int
	var chars int64
	err := p.db.QueryRowContext(ctx, `
		SELECT count(*),
		       COALESCE(sum(length(it.title) + 1 + LEAST(length(it.content_text), $3)), 0)
		FROM items it
		LEFT JOIN item_embeddings ie ON ie.item_id = it.id AND ie.model = $1
		WHERE ie.item_id IS NULL AND it.kind <> 'promo'
		  AND COALESCE(it.published_ts, it.fetched_ts) >= $2`,
		model, since.UTC(), embed.ItemBodyChars).Scan(&n, &chars)
	return n, chars, err
}

// SaveItemEmbeddings upserts vectors; replaying a batch is a no-op
// rewrite. The ::vector cast applies only when the database has the
// extension (the column is TEXT otherwise).
func (p *Pool) SaveItemEmbeddings(ctx context.Context, model string, dims int, rows []embed.Vector) error {
	cast := ""
	if p.probeVector(ctx) {
		cast = "::vector"
	}
	for _, r := range rows {
		if _, err := p.db.ExecContext(ctx,
			`INSERT INTO item_embeddings (item_id, model, dims, embedding)
			 VALUES ($1, $2, $3, $4`+cast+`)
			 ON CONFLICT (item_id) DO UPDATE SET
			   model = EXCLUDED.model, dims = EXCLUDED.dims,
			   embedding = EXCLUDED.embedding, created_at = now_utc()`,
			r.ItemID, model, dims, r.Literal); err != nil {
			return err
		}
	}
	return nil
}

// VoyageUsage records one Voyage response's total_tokens in the cost
// ledger under kind 'embed' or 'rerank', priced at list. Kept out of
// kind='call' so the LLM daily cap (LLMSpendSince) never counts it.
func (p *Pool) VoyageUsage(ctx context.Context, kind, model string, tokens int, costUSD float64) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO llm_ledger (workspace_id, kind, model, input_tokens, output_tokens, cost_usd)
		 VALUES ('', $1, $2, $3, 0, $4)`, kind, model, tokens, costUSD)
	return err
}

// VoyageSpendSince sums recorded USD for an embed/rerank ledger kind
// at or after the UTC timestamp since ('YYYY-MM-DD HH24:MI:SS').
func (p *Pool) VoyageSpendSince(ctx context.Context, kind, since string) (float64, error) {
	var usd float64
	err := p.db.QueryRowContext(ctx,
		`SELECT COALESCE(sum(cost_usd), 0) FROM llm_ledger WHERE kind = $1 AND created_at >= $2`,
		kind, since).Scan(&usd)
	return usd, err
}
