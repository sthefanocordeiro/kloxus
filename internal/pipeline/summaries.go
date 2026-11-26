package pipeline

// Ranking G4 (KLOX-111): one-line card summary + type for the run's
// top stories, cached globally in item_summaries.
//
// Decision (owner freeze: zero paid AI, llm_mode stays off): the
// summary is a deterministic template built from item metadata only —
// type, outlet and publish date — never from source prose. Nothing
// here can call a model, reorder, filter or score a story; it only
// decorates the card. Cap or lookup trouble means the card simply has
// no summary; source text is never pasted in its place. The table's
// model column leaves room for an LLM-written row later (one batch
// call per run, cheapest model, inside the per-run cap) if the owner
// ever authorises paid AI — that would be a new decision.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const (
	// summaryTopN is how many of the run's best stories get a summary.
	summaryTopN = 30
	// summaryMaxRunes is the card summary bound.
	summaryMaxRunes = 160
	// summaryModelDet marks template-written rows.
	summaryModelDet = "deterministic"
)

// SummaryKinds are the card types (slug -> PT label).
var SummaryKinds = map[string]string{
	"noticia": "Notícia", "analise": "Análise", "lancamento": "Lançamento",
	"regulacao": "Regulação", "opiniao": "Opinião", "outro": "Conteúdo",
}

// kindKeywords are folded-title cues, checked in order.
var kindKeywords = []struct {
	kind string
	cues []string
}{
	{"opiniao", []string{"opiniao", "coluna", "editorial", "artigo de opiniao"}},
	{"regulacao", []string{"lei", "regulamenta", "regulacao", "anpd", "anatel", "stf", "cade", "congresso", "senado", "decreto", "multa", "proibe"}},
	{"lancamento", []string{"lanca", "lancamento", "lancado", "anuncia", "apresenta", "chega", "ganha"}},
	{"analise", []string{"analise", "entenda", "por que", "como funciona", "o que muda", "explica"}},
}

// classifySummaryKind types an item from its shared enrichment kind
// when present, else from title cues; default "noticia".
func classifySummaryKind(title string, enr *store.ItemEnrichment) string {
	if enr != nil {
		switch enr.Kind {
		case "noticia":
			return "noticia"
		case "analise":
			return "analise"
		case "lancamento":
			return "lancamento"
		case "outro":
			// fall through to the title cues
		}
	}
	folded := " " + foldText(title) + " "
	for _, k := range kindKeywords {
		for _, c := range k.cues {
			if strings.Contains(folded, " "+c+" ") || (len(c) > 4 && strings.Contains(folded, " "+c)) {
				return k.kind
			}
		}
	}
	return "noticia"
}

// templateSummary builds the metadata-only one-liner.
func templateSummary(kind string, it *store.Item) string {
	label := SummaryKinds[kind]
	if label == "" {
		label = SummaryKinds["outro"]
	}
	outlet := strings.TrimSpace(it.SourceName)
	var b strings.Builder
	b.WriteString(label)
	if outlet != "" {
		b.WriteString(" de " + outlet)
	}
	if t, ok := parsePublished(it.PublishedAt); ok {
		b.WriteString(fmt.Sprintf(", publicada em %s", t.Format("02/01")))
	}
	b.WriteString(".")
	s := b.String()
	if utf8.RuneCountInString(s) > summaryMaxRunes {
		s = string([]rune(s)[:summaryMaxRunes-1]) + "…"
	}
	return s
}

// SummaryPool is the process-wide pool for the global summary cache;
// main wires it. Nil disables summaries (cards simply have none).
var SummaryPool *store.Pool

// SummarizeTop caches summaries for the representative item of the
// top-N visible stories that lack one. Fail-soft: errors are logged,
// never surfaced. Returns the number of new rows.
func SummarizeTop(ctx context.Context, pool *store.Pool, stories []store.Story, scores []float64, hidden []bool, enrich map[int64]*store.ItemEnrichment) int {
	if pool == nil || len(stories) == 0 || len(scores) != len(stories) {
		return 0
	}
	type pick struct {
		it   *store.Item
		kind string
	}
	var picks []pick
	var ids []int64
	for _, idx := range storyOrder(stories, scores) {
		if len(picks) == summaryTopN {
			break
		}
		if idx < len(hidden) && hidden[idx] {
			continue
		}
		it := earliestItem(&stories[idx])
		if it == nil {
			continue
		}
		picks = append(picks, pick{it, classifySummaryKind(it.Title, enrich[it.ID])})
		ids = append(ids, it.ID)
	}
	have, err := pool.ItemSummaryIDs(ctx, ids)
	if err != nil {
		slog.Warn("summaries: cache lookup failed", "err", err)
		return 0
	}
	var rows []store.ItemSummary
	for _, p := range picks {
		if have[p.it.ID] {
			continue
		}
		rows = append(rows, store.ItemSummary{
			ItemID: p.it.ID, Summary: templateSummary(p.kind, p.it), Kind: p.kind, Model: summaryModelDet,
		})
	}
	if len(rows) == 0 {
		return 0
	}
	if err := pool.SaveItemSummaries(ctx, rows); err != nil {
		slog.Warn("summaries: save failed", "err", err)
		return 0
	}
	return len(rows)
}
