package pipeline

// Deterministic semantic sweep (KLOX-93): every ingested item gets a
// stored embedding and a 'local-%' classification row — free, offline,
// and computed once per text revision. It hooks the same AfterPoll
// slot as the AI Enricher and runs BEFORE it: the AI sweep upgrades
// 'local-%' rows in place when it is enabled, and under the owner
// freeze the local rows are the classification suggestions fall back
// to. No paid call can ever happen here — there is no provider to
// call.
import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/embedding"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// SemanticSweep embeds + classifies items that lack a current
// embedding row. Backfill is the same code path: items stored before
// the layer existed appear in UnembeddedItems until swept. WS, when
// set, scopes the sweep to one workspace (plus shared catalog items)
// so a test in one package never classifies another package's test
// items on the shared DATABASE_URL; production leaves it empty and the
// sweep stays global.
type SemanticSweep struct {
	Pool *store.Pool
	WS   string
}

// Sweep embeds/classifies up to limit pending items. Idempotent —
// identical text produces identical vector and labels; a replay is a
// no-op write at worst. Fail-soft like the AI sweep: errors on one
// item never poison the batch.
func (s *SemanticSweep) Sweep(ctx context.Context, limit int) (int, error) {
	if s == nil || s.Pool == nil {
		return 0, nil
	}
	items, err := s.Pool.UnembeddedItemsFor(ctx, s.WS, embedding.Model, limit)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	enrich, err := s.Pool.EnrichmentsForItems(ctx, ids)
	if err != nil {
		return 0, err
	}
	var emb []store.ItemEmbedding
	var rows []store.ItemEnrichment
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		emb = append(emb, store.ItemEmbedding{
			ItemID: it.ID,
			Vector: embedding.Format(embedding.Embed(it.Title + "\n" + it.ContentText)),
			Model:  embedding.Model,
		})
		// Never overwrite an AI row — the 'local-%' tag is what lets
		// the Haiku sweep claim the item later (UnenrichedItems).
		if e := enrich[it.ID]; e != nil && !strings.HasPrefix(e.Model, "local-") {
			continue
		}
		rows = append(rows, localEnrichment(it))
	}
	if err := s.Pool.SaveEmbeddings(ctx, emb); err != nil {
		return 0, err
	}
	if err := s.Pool.SaveEnrichments(ctx, rows); err != nil {
		return 0, err
	}
	return len(emb), nil
}

// localEnrichment maps the deterministic classification onto the
// shared enrichment row. The summary is the item's own description
// when it carries one (DJ blurbs are exactly the one-liner the card
// shows), else the first 25 words of the body.
func localEnrichment(it store.UnembeddedRow) store.ItemEnrichment {
	var meta struct {
		Description string   `json:"description"`
		Categories  []string `json:"categories"`
	}
	_ = json.Unmarshal([]byte(it.MetaJSON), &meta)
	res := embedding.Classify(it.Title, it.ContentText, meta.Categories)
	summary := meta.Description
	if summary == "" {
		summary = it.ContentText
	}
	return store.ItemEnrichment{
		ItemID:    it.ID,
		Kind:      res.Kind,
		Topics:    res.Topics,
		SummaryPT: clampWords(summary, 25),
		Language:  res.Language,
		Model:     embedding.ClassifyModel,
	}
}
