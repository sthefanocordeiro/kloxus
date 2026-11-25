package pipeline

// Shared per-item enrichment (KLOX-67 §7.2): one Haiku call classifies
// up to 40 articles at once — kind, promotional flag, topic tags, a
// one-sentence PT summary and the language — and the result lives in
// the shared item_enrichment table, computed once per article and
// reused by every newsletter. The sweep runs after ingest as a
// background job: newsletter creation and edition prep never wait on
// (or pay for) it.
import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/strutil"
)

// enrichBatchSize is the §7.2 batch bound — up to 40 items per call.
const enrichBatchSize = 40

// enrichExcerptRunes bounds each item's excerpt inside the batch prompt
// so a 40-item call stays well under the 20k-input target.
const enrichExcerptRunes = 600

// enrichMaxTokens bounds the batch enrich reply — one small JSON row
// per item, so 8k covers a full 40-item batch with headroom.
const enrichMaxTokens = 8192

// EnrichmentKinds is the fixed editorial classification the model must
// pick from (§7.2). Slugs keep Portuguese accents out of the stored
// values; labels carry the prompt's PT names.
var EnrichmentKinds = []struct {
	Slug  string
	Label string
}{
	{"noticia", "notícia"},
	{"analise", "análise/opinião"},
	{"tutorial", "tutorial"},
	{"forum", "pergunta de fórum"},
	{"lancamento", "lançamento de produto"},
	{"review", "review/teste de produto"},
	{"lista", "lista/guia de compra"},
	{"publieditorial", "publieditorial/promoção"},
	{"entretenimento", "entretenimento"},
	{"esporte", "esporte"},
	{"efemeride", "efeméride"},
	{"outro", "outro"},
}

var enrichmentKindSet = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range EnrichmentKinds {
		m[k.Slug] = true
	}
	return m
}()

// EnrichmentTopics is the fixed tag list (§7.2): at most five per item.
var EnrichmentTopics = []string{
	"tecnologia", "IA", "fintech", "cripto", "startups",
	"politica", "economia", "ciencia", "seguranca", "consumo",
	"games", "negocios", "midia", "regulacao", "outro",
}

var enrichmentTopicSet = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range EnrichmentTopics {
		m[t] = true
	}
	return m
}()

// Enricher runs the shared enrichment sweep.
type Enricher struct {
	Pool  *store.Pool
	LLM   llm.Provider
	Model string
}

// enrichRow is one parsed enrichment answer.
type enrichRow struct {
	Ref         int      `json:"ref"`
	Kind        string   `json:"kind"`
	Promotional bool     `json:"promotional"`
	Topics      []string `json:"topics"`
	SummaryPT   string   `json:"summary_pt"`
	Language    string   `json:"language"`
}

// Sweep enriches up to limit un-enriched items, in batches of 40. It is
// idempotent and fail-soft: a missing fixture, a guard refusal or a bad
// answer leaves the items un-enriched for the next sweep — enrichment
// is never a user-facing failure.
func (e *Enricher) Sweep(ctx context.Context, limit int) (enriched int, err error) {
	if e == nil || e.Pool == nil || e.LLM == nil || e.Model == "" {
		return 0, nil
	}
	items, err := e.Pool.UnenrichedItems(ctx, limit)
	if err != nil {
		return 0, err
	}
	for start := 0; start < len(items); start += enrichBatchSize {
		if ctx.Err() != nil {
			return enriched, ctx.Err()
		}
		end := start + enrichBatchSize
		if end > len(items) {
			end = len(items)
		}
		n, err := e.enrichBatch(ctx, items[start:end])
		if err != nil {
			// A guard refusal (daily cap, kill switch) blocks every
			// later batch identically — stop the sweep; the pending
			// items wait for the next pass when the window reopens.
			if _, blocked := llm.AsBlocked(err); blocked {
				slog.Info("enrich sweep paused by spend guard", "err", err)
				return enriched, nil
			}
			// Any other batch failure is dropped, not retried — the
			// next sweep picks these items up again.
			slog.Warn("enrich batch failed", "items", end-start, "err", err)
			continue
		}
		enriched += n
	}
	return enriched, nil
}

// enrichBatch classifies one batch and stores the rows.
func (e *Enricher) enrichBatch(ctx context.Context, items []store.UnenrichedItem) (int, error) {
	type row struct {
		Ref     int
		Title   string
		Outlet  string
		Excerpt string
	}
	data := struct {
		Kinds  []struct{ Slug, Label string }
		Topics []string
		Items  []row
	}{Kinds: EnrichmentKinds, Topics: EnrichmentTopics}
	for i, it := range items {
		data.Items = append(data.Items, row{
			Ref: i + 1, Title: it.Title, Outlet: it.SourceName,
			Excerpt: strutil.Truncate(it.Excerpt, enrichExcerptRunes),
		})
	}
	user, err := RenderPrompt("enrich", data)
	if err != nil {
		return 0, err
	}
	req := llm.Request{Model: e.Model, Template: "enrich", User: user, MaxTokens: enrichMaxTokens}
	if err := llm.CheckSize(req); err != nil {
		return 0, err
	}
	resp, err := e.LLM.Complete(ctx, req)
	if err != nil {
		return 0, err
	}
	var rows []enrichRow
	if err := decodeJSON(resp.Text, &rows); err != nil {
		return 0, fmt.Errorf("enrich: %w", err)
	}
	var out []store.ItemEnrichment
	for _, r := range rows {
		if r.Ref < 1 || r.Ref > len(items) {
			continue
		}
		out = append(out, normalizeEnrichment(items[r.Ref-1].ID, r, e.Model))
	}
	if len(out) == 0 {
		return 0, fmt.Errorf("enrich: no usable rows in answer")
	}
	if err := e.Pool.SaveEnrichments(ctx, out); err != nil {
		return 0, err
	}
	return len(out), nil
}

// normalizeEnrichment validates a parsed row against the fixed lists and
// the §7.2 bounds: unknown kinds become "outro", topics are filtered to
// the fixed list and capped at five, the summary is clamped to 25
// words.
func normalizeEnrichment(itemID int64, r enrichRow, model string) store.ItemEnrichment {
	kind := strings.ToLower(strings.TrimSpace(r.Kind))
	if !enrichmentKindSet[kind] {
		kind = "outro"
	}
	var topics []string
	seen := map[string]bool{}
	for _, t := range r.Topics {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] || !enrichmentTopicSet[t] {
			continue
		}
		seen[t] = true
		topics = append(topics, t)
		if len(topics) == 5 {
			break
		}
	}
	return store.ItemEnrichment{
		ItemID:      itemID,
		Kind:        kind,
		Promotional: r.Promotional || kind == "publieditorial",
		Topics:      topics,
		SummaryPT:   clampWords(r.SummaryPT, 25),
		Language:    strings.ToLower(strings.TrimSpace(r.Language)),
		Model:       model,
	}
}

// clampWords keeps the first n words of a summary — the §7.2 "one
// sentence, ≤25 words" bound enforced regardless of model output.
func clampWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) <= n {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:n], " ")
}
