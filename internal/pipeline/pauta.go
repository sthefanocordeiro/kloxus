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
	// Keys are the story's stable feedback identifiers — the client
	// echoes them on a Sim/Não so the answer still lands when the
	// stories table was rebuilt since the card was dealt (KLOX-236).
	Keys []string
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

	// Sim/Não answers persist on stable story keys (KLOX-183): a "no"
	// story never reaches Hoje or the re-dealt calibration deck, and
	// look-alike stories sink — the deterministic counterpart of the
	// Rocchio reject mean, which needs embeddings to exist at all.
	rejected, rejectedTitles := r.rejectedAnswers(ctx, pub.ID)

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
		if storyKeysRejected(&stories[i], rejected) {
			continue
		}
		age := -1.0
		if t, ok := storyNewest(&stories[i]); ok {
			age = now.Sub(t).Hours()
			if age < 0 {
				age = 0
			}
		}
		c := cand{idx: i, age: age, rank: s1.Scores[i] - noAnswerPenalty(&stories[i], rejectedTitles)}
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
	// keep the MMR pool bounded — wide enough that the per-outlet cap
	// below still finds other outlets when one feed dominates the top
	if len(pool) > 150 {
		pool = pool[:150]
	}
	scores := make([]float64, len(pool))
	vecs := make([][]float32, len(pool))
	for i, c := range pool {
		scores[i] = c.rank / stage1Scale
		if c.idx < len(s1.StoryVecs) {
			vecs[i] = s1.StoryVecs[c.idx]
		}
	}
	var ordered []PautaCard
	for _, oi := range rank.MMROrder(scores, vecs, 0) {
		c := pool[oi]
		ordered = append(ordered, pautaCard(&stories[c.idx], enrich, c.age, c.rank))
	}
	return capPerOutlet(ordered, n)
}

// rejectedAnswers loads the publication's "no" answers as a key set
// plus the distinct rejected titles the similarity demotion reads.
// A load failure degrades to no feedback — the deck still builds.
func (r *Runner) rejectedAnswers(ctx context.Context, pubID int64) (map[string]bool, []string) {
	answers, err := r.DB.RejectedStoryFeedback(ctx, pubID)
	if err != nil || len(answers) == 0 {
		return nil, nil
	}
	keys := map[string]bool{}
	var titles []string
	seen := map[string]bool{}
	for _, a := range answers {
		keys[a.Key] = true
		if a.Title != "" && !seen[a.Title] {
			seen[a.Title] = true
			titles = append(titles, a.Title)
		}
	}
	return keys, titles
}

// storyKeysRejected reports whether any of the story's stable
// FeedbackKeys carries a "no" answer.
func storyKeysRejected(s *store.Story, rejected map[string]bool) bool {
	if len(rejected) == 0 {
		return false
	}
	for _, k := range s.FeedbackKeys() {
		if rejected[k] {
			return true
		}
	}
	return false
}

// noAnswerMinOverlap is the shared-content-token ratio (against the
// rejected title) from which a story counts as a look-alike.
const noAnswerMinOverlap = 0.5

// noAnswerPenaltyMax caps the demotion a look-alike story takes.
// Scores run 0–10, so the penalty drops look-alikes below the fresh
// coverage without hiding them when the deck is thin.
const noAnswerPenaltyMax = 8.0

// noAnswerPenalty sinks a story that reads like one the creator
// answered "Não" — the deterministic half of "um Não reduz o peso de
// notícias parecidas" (KLOX-183). The overlap is graded: sharing half
// the rejected headline's content words costs half the cap, sharing
// all of them the full noAnswerPenaltyMax; below noAnswerMinOverlap
// the story keeps its rank.
func noAnswerPenalty(s *store.Story, rejectedTitles []string) float64 {
	if len(rejectedTitles) == 0 {
		return 0
	}
	var b strings.Builder
	b.WriteString(s.Title)
	for i := range s.Items {
		b.WriteString(" ")
		b.WriteString(s.Items[i].Title)
	}
	cand := contentTokens(b.String())
	if len(cand) == 0 {
		return 0
	}
	best := 0.0
	for _, rt := range rejectedTitles {
		rej := contentTokens(rt)
		if len(rej) == 0 {
			continue
		}
		shared := 0
		for w := range rej {
			if wordHit(w, cand) {
				shared++
			}
		}
		if o := float64(shared) / float64(len(rej)); o > best {
			best = o
		}
	}
	if best < noAnswerMinOverlap {
		return 0
	}
	return noAnswerPenaltyMax * best
}

// contentTokens is the folded content-word set of a title — the same
// vocabulary rules the topic/reject matchers use (folded, ≥3 chars,
// no function words).
func contentTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range textToks(s) {
		if len(w) >= 3 && !termStopwords[w] {
			out[w] = true
		}
	}
	return out
}

// capPerOutlet takes n cards from the ranked list with at most
// MaxPerOutlet of one outlet inside the first DiversityWindow slots
// (KLOX-169): one prolific feed must not fill the pauta. Capped cards
// only fill the deck when no other outlet has anything left.
func capPerOutlet(ordered []PautaCard, n int) []PautaCard {
	var out, spill []PautaCard
	perOutlet := map[string]int{}
	for _, card := range ordered {
		if len(out) >= n {
			break
		}
		key := strings.ToLower(strings.TrimSpace(card.Outlet))
		if len(out) < DiversityWindow && key != "" && perOutlet[key] >= MaxPerOutlet {
			spill = append(spill, card)
			continue
		}
		perOutlet[key]++
		out = append(out, card)
	}
	for _, card := range spill {
		if len(out) >= n {
			break
		}
		out = append(out, card)
	}
	return out
}

// MaxPerOutlet caps how many of one outlet's stories may sit among the
// first DiversityWindow cards of a pauta.
const (
	MaxPerOutlet    = 3
	DiversityWindow = 10
)

func pautaCard(s *store.Story, enrich map[int64]*store.ItemEnrichment, age, score float64) PautaCard {
	card := PautaCard{StoryID: s.ID, Title: s.Title, Outlets: len(s.Outlets()), AgeHours: age, Score: score, Topics: []string{}, Keys: s.FeedbackKeys()}
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
	// no enriched summary: the opening of the first item that has a body
	// (not only item 0 — signal items often carry none)
	for _, it := range s.Items {
		if card.Summary != "" {
			break
		}
		card.Summary = firstSentence(it.ContentText, 180)
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

// RecordStoryFeedback persists the Sim/Não answer on the story's
// stable keys — a "no" keeps it out of Hoje and the re-dealt deck —
// and folds it into the publication's Rocchio means when embeddings
// exist. Best-effort: feedback never fails the answer, and with no
// vectors the Rocchio half is a silent no-op while the persisted
// answer still applies (KLOX-183).
func RecordStoryFeedback(ctx context.Context, db *store.DB, pubID, storyID int64, accept bool) error {
	answer := "no"
	if accept {
		answer = "yes"
	}
	st, err := db.StoryWithItems(ctx, storyID)
	if err != nil || st == nil || st.PublicationID != pubID {
		return nil // unknown or foreign story — nothing to record
	}
	if err := db.SaveStoryAnswers(ctx, pubID, st.FeedbackKeys(), answer, st.Title); err != nil {
		return fmt.Errorf("story answer: %w", err)
	}
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
