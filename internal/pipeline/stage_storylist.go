package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageStorylist asks the model to pick and section the issue's items,
// then creates the pending issue items — merging same-event stories the
// pick grouped under one ref.
func (r *Runner) stageStorylist(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	stories := st.stories
	scoreByRef := st.scoreByRef
	res := st.res
	issueID := st.issueID
	r.stepStart("storylist")
	start := time.Now()
	type slStory struct {
		Ref    int
		Score  float64
		Title  string
		Reason string
	}
	ss := make([]slStory, len(stories))
	for i, s := range stories {
		sr := scoreByRef[i+1]
		ss[i] = slStory{Ref: i + 1, Score: sr.score, Title: s.Title, Reason: sr.reason}
	}
	var picked struct {
		Items []struct {
			Refs    []int  `json:"refs"`
			Title   string `json:"title"`
			Theme   string `json:"theme"`
			Why     string `json:"why"`
			Section string `json:"section"`
		} `json:"items"`
	}
	resp, err := r.call(ctx, r.TriageModel, "storylist", map[string]any{
		"Publication":   pub.Name,
		"Audience":      pub.Audience,
		"Sections":      pub.Sections,
		"EditorialLine": pub.EditorialLine,
		"Guide":         r.guide(ctx, pub.ID),
		"Stories":       ss,
	}, &picked)
	if err != nil {
		return fmt.Errorf("storylist: %w", err)
	}
	res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
	res.Selected = len(picked.Items)
	r.emit("storylist", map[string]int{"selected": res.Selected})
	r.record(ctx, pub.ID, issueID, "storylist", r.TriageModel, start, resp)

	// --- create issue items, merging same-event stories ---

	entries := []entry{}
	for idx, p := range picked.Items {
		if len(p.Refs) == 0 {
			continue
		}
		primaryIdx := p.Refs[0] - 1
		if primaryIdx < 0 || primaryIdx >= len(stories) {
			return fmt.Errorf("storylist: ref %d out of range", p.Refs[0])
		}
		primary := stories[primaryIdx]
		var merges []int64
		memberItems := append([]store.Item{}, primary.Items...)
		for _, ref := range p.Refs[1:] {
			if ref-1 < 0 || ref-1 >= len(stories) {
				continue
			}
			sec := stories[ref-1]
			merges = append(merges, sec.ID)
			memberItems = append(memberItems, sec.Items...)
		}
		if len(merges) > 0 {
			if err := r.DB.MergeStories(ctx, primary.ID, merges); err != nil {
				return err
			}
		}
		sc := scoreByRef[p.Refs[0]]
		iiID, err := r.DB.CreateIssueItem(ctx, store.IssueItem{
			IssueID: issueID,
			StoryID: primary.ID,
			Section: p.Section,
			Score:   sc.score,
			Reason:  p.Why,
			Status:  "pending",
		})
		if err != nil {
			return err
		}
		entries = append(entries, entry{
			iiID: iiID, storyID: primary.ID, ref: idx + 1,
			title: p.Title, section: p.Section, why: p.Why,
			score: sc.score, sources: memberItems,
		})
	}
	st.entries = entries
	return nil
}
