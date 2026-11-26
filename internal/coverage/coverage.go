// Package coverage measures whether Daily Journal alone covers a
// niche well enough to be the primary source (the KLOX-93 acceptance
// criterion; DJ left the catalog again under KLOX-114, so the report
// now only informs a possible future re-add). Everything is
// deterministic — the hashing embedder + lexicon classifier, no paid
// calls — so the report replays identically on the same API snapshot.
package coverage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/embedding"
	"github.com/sthefanocordeiro/kloxus/internal/ingest"
)

// Fetcher is the slice of the Daily Journal client coverage needs —
// *ingest.DailyJournal in production, a stub in tests.
type Fetcher interface {
	Poll(ctx context.Context, maxPages int) ([]ingest.DJItem, error)
}

// Niche is a newsletter audience expressed two ways: a seed paragraph
// embedded once for cosine scoring, and the classifier tags that
// count as topical hits.
type Niche struct {
	Key    string
	Label  string
	Seed   string
	Topics []string
}

// Niches covers the three audiences the issue requires — fintech,
// regulação, criativo — plus the generic-technology newsletter whose
// poor suggestions triggered the audit.
var Niches = []Niche{
	{
		Key:   "fintech",
		Label: "Fintech / bancos digitais / Pix / cripto",
		Seed: "fintech bancos digitais nubank pix meios de pagamento " +
			"criptomoedas bitcoin regulação financeira banco central open finance cartão crédito",
		Topics: []string{"fintech", "cripto"},
	},
	{
		Key:   "regulacao",
		Label: "Regulação e políticas públicas",
		Seed: "regulação agências reguladoras anatel banco central cvm congresso " +
			"senado decreto medida provisória projeto de lei política governo federal",
		Topics: []string{"regulacao", "politica"},
	},
	{
		Key:   "criativo",
		Label: "Economia criativa / cultura",
		Seed: "economia criativa cultura música cinema filmes streaming games " +
			"videogame design artes festival entretenimento shows",
		Topics: []string{"midia", "games"},
	},
	{
		Key:   "tecnologia",
		Label: "Tecnologia / IA (nicho da auditoria)",
		Seed: "tecnologia software aplicativos inteligência artificial startups " +
			"internet smartphones inovação digital",
		Topics: []string{"tecnologia", "IA", "startups"},
	},
}

// SemanticThreshold is the cosine cutoff for a semantic hit. The
// hashing embedder compresses vocabulary into 256 signed buckets, so
// genuinely related texts score modestly — 0.20 is deliberately the
// recall-side bound, validated on the live sample.
const SemanticThreshold = 0.20

// Row is one niche's coverage counts over the polled snapshot.
type Row struct {
	Niche    Niche
	Semantic int      // cosine ≥ SemanticThreshold
	Topical  int      // classifier tag matched niche.Topics
	Either   int      // union of the two
	Samples  []Sample // strongest semantic hits
}

// Sample is one item's evidence line in the report.
type Sample struct {
	Title string
	Slug  string
	Sim   float64
}

// Report is the rendered coverage measurement.
type Report struct {
	GeneratedAt time.Time
	Items       int
	Window      string // min–max published_at actually seen
	Rows        []Row
}

// Run polls up to pages list pages and scores every item against every
// niche. The list endpoint suffices — coverage measures the item
// surface (title + description + categories), not stored bodies.
func Run(ctx context.Context, f Fetcher, pages int) (*Report, error) {
	items, err := f.Poll(ctx, pages)
	if err != nil {
		return nil, err
	}
	rep := &Report{GeneratedAt: time.Now().UTC(), Items: len(items)}
	var minT, maxT time.Time
	seeds := make([][]float32, len(Niches))
	for i, n := range Niches {
		seeds[i] = embedding.Embed(n.Seed)
		rep.Rows = append(rep.Rows, Row{Niche: n})
	}
	for _, it := range items {
		if t, err := time.Parse(time.RFC3339, it.PublishedAt); err == nil {
			if minT.IsZero() || t.Before(minT) {
				minT = t
			}
			if t.After(maxT) {
				maxT = t
			}
		}
		text := it.Title + "\n" + it.Description
		vec := embedding.Embed(text)
		res := embedding.Classify(it.Title, it.Description, it.Categories)
		for i := range rep.Rows {
			n := rep.Rows[i].Niche
			sim := embedding.Cosine(vec, seeds[i])
			topical := false
			for _, tag := range res.Topics {
				for _, want := range n.Topics {
					if tag == want {
						topical = true
					}
				}
			}
			if sim >= SemanticThreshold {
				rep.Rows[i].Semantic++
			}
			if topical {
				rep.Rows[i].Topical++
			}
			if sim >= SemanticThreshold || topical {
				rep.Rows[i].Either++
				rep.Rows[i].Samples = append(rep.Rows[i].Samples,
					Sample{Title: it.Title, Slug: it.Slug, Sim: sim})
			}
		}
	}
	if !minT.IsZero() {
		rep.Window = fmt.Sprintf("%s → %s UTC",
			minT.Format("2006-01-02 15:04"), maxT.Format("2006-01-02 15:04"))
	}
	for i := range rep.Rows {
		s := rep.Rows[i].Samples
		sort.Slice(s, func(a, b int) bool { return s[a].Sim > s[b].Sim })
		if len(s) > 5 {
			rep.Rows[i].Samples = s[:5]
		}
	}
	return rep, nil
}

// Markdown renders the issue-comment body.
func (r *Report) Markdown() string {
	var b strings.Builder
	b.WriteString("## Daily Journal coverage report — KLOX-93\n\n")
	fmt.Fprintf(&b, "Snapshot: %d items, %s (generated %s UTC).\n\n",
		r.Items, r.Window, r.GeneratedAt.Format("2006-01-02 15:04"))
	b.WriteString("| Nicho | Semântico (cos ≥ 0.20) | Tópico (classifier) | União | Taxa |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, row := range r.Rows {
		rate := 0.0
		if r.Items > 0 {
			rate = 100 * float64(row.Either) / float64(r.Items)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %.1f%% |\n",
			row.Niche.Label, row.Semantic, row.Topical, row.Either, rate)
	}
	b.WriteString("\n### Amostras (top hits por similaridade)\n\n")
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "**%s**\n\n", row.Niche.Label)
		for _, s := range row.Samples {
			fmt.Fprintf(&b, "- %.2f — %s (`%s`)\n", s.Sim, s.Title, s.Slug)
		}
		if len(row.Samples) == 0 {
			b.WriteString("- nenhum hit\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Metodologia: snapshot do endpoint público " +
		"`/api/public/news` (list, ≤7 páginas × 50). Semântico = cosseno " +
		"entre o embedding hash-256 (title+description) e o seed do nicho " +
		"≥ 0.20; Tópico = o classificador léxico determinístico marcar uma " +
		"tag do nicho; União = qualquer um dos dois. Zero chamadas pagas.\n")
	return b.String()
}
