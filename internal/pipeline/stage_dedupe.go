package pipeline

import (
	"context"
	"sort"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageDedupe clusters the publication's collected items into stories
// (same-event merges) and rebuilds the story table.
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
	for i, it := range items {
		urls[i] = it.URL
		texts[i] = it.Title + "\n" + it.ContentText
	}
	clusters := dedupe.Assign(urls, texts)
	if err := r.DB.ClearStories(ctx, pub.ID); err != nil {
		return err
	}
	groups := map[int][]store.Item{}
	for i, it := range items {
		groups[clusters[i]] = append(groups[clusters[i]], it)
	}
	ordered := make([][]store.Item, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a][0].ID < ordered[b][0].ID })
	for _, g := range ordered {
		title := g[0].Title
		for _, it := range g {
			if len(it.Title) > len(title) {
				title = it.Title
			}
		}
		storyID, err := r.DB.CreateStory(ctx, pub.ID, title, int64(dedupe.SimHash(g[0].Title+"\n"+g[0].ContentText)))
		if err != nil {
			return err
		}
		for _, it := range g {
			if err := r.DB.AddStoryItem(ctx, storyID, it.ID); err != nil {
				return err
			}
		}
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
