package pipeline

import (
	"context"
	"sort"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageDedupe clusters the publication's collected items into stories —
// SimHash over body text plus the cited- and same-event merge passes —
// and rebuilds the story table.
func (r *Runner) stageDedupe(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	res := st.res
	r.stepStart("dedupe")
	start := time.Now()
	items, err := r.DB.ItemsForPublication(ctx, pub.ID)
	if err != nil {
		return err
	}
	urls := make([]string, len(items))
	texts := make([]string, len(items))
	published := make([]time.Time, len(items))
	for i, it := range items {
		urls[i] = it.URL
		// SimHash runs over body text only (KLOX-46): a republish's new
		// headline must not push the pair past the 4-bit threshold.
		texts[i] = it.ContentText
		// The 72 h merge window uses the published date first and the
		// fetched time when it is unparseable — the same fallback the
		// candidate query applies (published_ts → fetched_ts).
		if t, ok := parsePublished(it.PublishedAt); ok {
			published[i] = t
		} else if t, ok := parsePublished(it.FetchedAt); ok {
			published[i] = t
		}
	}
	clusters := dedupe.Assign(urls, texts, published)
	if err := r.DB.ClearStories(ctx, pub.ID); err != nil {
		return err
	}
	groups := map[int][]store.Item{}
	for i, it := range items {
		groups[clusters[i]] = append(groups[clusters[i]], it)
	}
	groups = mergeCitedGroups(groups)
	groups = mergeEventGroups(groups)
	ordered := make([][]store.Item, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a][0].ID < ordered[b][0].ID })
	drafts := make([]store.StoryDraft, len(ordered))
	for i, g := range ordered {
		itemIDs := make([]int64, len(g))
		for j, it := range g {
			itemIDs[j] = it.ID
		}
		drafts[i] = store.StoryDraft{
			Title:     earliestTitle(g),
			Simhash:   int64(dedupe.SimHash(g[0].Title + "\n" + g[0].ContentText)),
			DedupeKey: dedupeKey(g),
			ItemIDs:   itemIDs,
		}
	}
	if err := r.DB.CreateStories(ctx, pub.ID, drafts); err != nil {
		return err
	}
	stories, err := r.DB.StoriesWithItems(ctx, pub.ID)
	if err != nil {
		return err
	}
	st.stories = stories
	res.Stories = len(stories)
	r.emit("dedupe", map[string]int{"stories": res.Stories, "duplicates_merged": len(items) - res.Stories})
	r.record(ctx, pub.ID, 0, "dedupe", "", start, llm.Response{})
	return nil
}
