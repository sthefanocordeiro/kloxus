package pipeline

// Pauta cards for the editor-de-pauta flow (KLOX-123): the calibration
// step of onboarding and, next, the "Hoje" screen. A pauta is the
// publication's stories ranked by stage 1 (embeddings or FTS) with MMR
// for variety — no model call happens here, so a visitor's first screen
// costs nothing. The creator's Sim/Não answers feed the same Rocchio
// means the board uses (RecordStoryFeedback).

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/rank"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// PautaCard is one story offered for a Sim/Não decision.
type PautaCard struct {
	StoryID int64
	Title   string
	Outlet  string
	Outlets int
	Summary string
	Topics  []string
	URL     string
	// AgeHours is -1 when the story has no parseable date.
	AgeHours float64
	Score    float64
}

// Window of the calibration sample (spec Block 2): real stories of the
// last 48 hours; older stories only fill the deck when fewer exist.
const CalibrationWindow = 48 * time.Hour

// BuildStories collects what the publication's sources hold and
// rebuilds its stories (cluster + dedupe), the same first half of a
// prepare run without creating an issue. Catalog sources are ingested by
// the global scheduler and are never fetched here; fixture sources (demo
// mode) are read locally.
func (r *Runner) BuildStories(ctx context.Context, pub *store.Publication) ([]store.Story, error) {
	srcs, err := r.DB.Sources(ctx, pub.ID)
	if err != nil {
		return nil, err
	}
	for _, s := range srcs {
		if s.CatalogID != 0 {
			continue
		}
		if r.DemoMode && !strings.HasPrefix(s.URL, "fixtures/") {
			continue
		}
		if r.Fetcher == nil {
			continue
		}
		raw, err := r.Fetcher.Fetch(ctx, s.Type, s.URL)
		if err != nil {
			continue // one bad source must not abort the deck
		}
		if _, err := r.DB.SaveItems(ctx, s.ID, raw, dedupe.Canonical); err != nil {
			return nil, err
		}
	}
	items, err := r.DB.ItemsForPublication(ctx, pub.ID)
	if err != nil {
		return nil, err
	}
	urls := make([]string, len(items))
	texts := make([]string, len(items))
	published := make([]time.Time, len(items))
	for i, it := range items {
		urls[i], texts[i] = it.URL, it.ContentText
		if t, ok := parsePublished(it.PublishedAt); ok {
			published[i] = t
		} else if t, ok := parsePublished(it.FetchedAt); ok {
			published[i] = t
		}
	}
	clusters := dedupe.Assign(urls, texts, published)
	if err := r.DB.ClearStories(ctx, pub.ID); err != nil {
		return nil, err
	}
	groups := map[int][]store.Item{}
	for i, it := range items {
		groups[clusters[i]] = append(groups[clusters[i]], it)
	}
	groups = mergeCitedGroups(groups)
	ordered := make([][]store.Item, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a][0].ID < ordered[b][0].ID })
	drafts := make([]store.StoryDraft, len(ordered))
	for i, g := range ordered {
		ids := make([]int64, len(g))
		for j, it := range g {
			ids[j] = it.ID
		}
		drafts[i] = store.StoryDraft{
			Title:     earliestTitle(g),
			Simhash:   int64(dedupe.SimHash(g[0].Title + "\n" + g[0].ContentText)),
			DedupeKey: dedupeKey(g),
			ItemIDs:   ids,
		}
	}
	if err := r.DB.CreateStories(ctx, pub.ID, drafts); err != nil {
		return nil, err
	}
	return r.DB.StoriesWithItems(ctx, pub.ID)
}

// Pauta ranks the publication's current stories and returns up to n
// cards: stage-1 score, off-topic/foreign stories dropped, stories
// newer than maxAge preferred (older ones only fill the deck), and an
// MMR pass so the n cards do not repeat one event.
func (r *Runner) Pauta(ctx context.Context, pub *store.Publication, stories []store.Story, n int, maxAge time.Duration) []PautaCard {
	if len(stories) == 0 || n <= 0 {
		return nil
	}
	var eids []int64
	for i := range stories {
		for j := range stories[i].Items {
			eids = append(eids, stories[i].Items[j].ID)
		}
	}
	enrich, _ := r.DB.EnrichmentsForItems(ctx, eids)
	_, base := scoreStoriesEnriched(stories, pub.Language, pub, enrich)
	s1 := r.rankStage1(ctx, pub, stories, base, enrich)

	now := time.Now()
	type cand struct {
		idx  int
		age  float64
		rank float64
	}
	var fresh, older []cand
	for i := range stories {
		if s1.Hidden[i] != "" {
			continue
		}
		age := -1.0
		if t, ok := storyNewest(&stories[i]); ok {
			age = now.Sub(t).Hours()
			if age < 0 {
				age = 0
			}
		}
		c := cand{idx: i, age: age, rank: s1.Scores[i]}
		if age >= 0 && age <= maxAge.Hours() {
			fresh = append(fresh, c)
		} else {
			older = append(older, c)
		}
	}
	byScore := func(cs []cand) {
		sort.SliceStable(cs, func(a, b int) bool { return cs[a].rank > cs[b].rank })
	}
	byScore(fresh)
	byScore(older)
	pool := fresh
	if len(pool) < n {
		pool = append(append([]cand{}, fresh...), older...)
	}
	// keep the MMR pool bounded: the best 60 are plenty to pick 8 from
	if len(pool) > 60 {
		pool = pool[:60]
	}
	scores := make([]float64, len(pool))
	vecs := make([][]float32, len(pool))
	for i, c := range pool {
		scores[i] = c.rank / stage1Scale
		if c.idx < len(s1.StoryVecs) {
			vecs[i] = s1.StoryVecs[c.idx]
		}
	}
	var out []PautaCard
	for _, oi := range rank.MMROrder(scores, vecs, n) {
		c := pool[oi]
		s := &stories[c.idx]
		out = append(out, pautaCard(s, enrich, c.age, c.rank))
	}
	return out
}

func pautaCard(s *store.Story, enrich map[int64]*store.ItemEnrichment, age, score float64) PautaCard {
	card := PautaCard{StoryID: s.ID, Title: s.Title, Outlets: len(s.Outlets()), AgeHours: age, Score: score, Topics: []string{}}
	if len(s.Items) > 0 {
		it := s.Items[0]
		card.Outlet, card.URL = it.SourceName, it.URL
		if card.Outlet == "" {
			card.Outlet = outletLabel(it.SourceURL)
		}
	}
	seen := map[string]bool{}
	for _, it := range s.Items {
		if e := enrich[it.ID]; e != nil {
			if card.Summary == "" {
				card.Summary = e.SummaryPT
			}
			for _, t := range e.Topics {
				if !seen[t] {
					seen[t] = true
					card.Topics = append(card.Topics, t)
				}
			}
		}
	}
	if card.Summary == "" && len(s.Items) > 0 {
		card.Summary = firstSentence(s.Items[0].ContentText, 180)
	}
	return card
}

// firstSentence is the opening of body, cut at a sentence end or at max
// runes on a word boundary — a snippet of the source's own words, shown
// as-is (the AI never writes commentary).
func firstSentence(body string, max int) string {
	body = strings.Join(strings.Fields(body), " ")
	r := []rune(body)
	if len(r) <= max {
		return body
	}
	cut := string(r[:max])
	if i := strings.LastIndexAny(cut, ".!?"); i > max/2 {
		return cut[:i+1]
	}
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}

// RecordStoryFeedback folds a Sim/Não answer on a story into the
// publication's Rocchio means. Best-effort and free: it only reads the
// stored vectors.
func RecordStoryFeedback(ctx context.Context, db *store.DB, pubID, storyID int64, accept bool) error {
	emb := DefaultEmbedder
	if emb == nil {
		return nil
	}
	kind := store.FeedbackReject
	if accept {
		kind = store.FeedbackAccept
	}
	if err := db.FeedbackForStory(ctx, pubID, storyID, emb.Model(), kind); err != nil {
		return fmt.Errorf("feedback: %w", err)
	}
	return nil
}
