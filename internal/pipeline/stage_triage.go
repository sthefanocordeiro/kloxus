package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageTriage asks the model to score every story for the publication's
// audience and stores each score/reason on the story row.
func (r *Runner) stageTriage(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	stories := st.stories
	r.stepStart("triage")
	start := time.Now()
	type triStory struct {
		Ref     int
		Title   string
		Outlets string
		Snippet string
	}
	ts := make([]triStory, len(stories))
	for i, s := range stories {
		snip := ""
		if len(s.Items) > 0 {
			snip = truncate(s.Items[0].ContentText, 400)
		}
		ts[i] = triStory{Ref: i + 1, Title: s.Title, Outlets: strings.Join(s.Outlets(), ", "), Snippet: snip}
	}
	var triage []struct {
		Ref    int     `json:"ref"`
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	resp, err := r.call(ctx, r.TriageModel, "triage", map[string]any{
		"Publication":   pub.Name,
		"Audience":      pub.Audience,
		"Language":      pub.Language,
		"EditorialLine": pub.EditorialLine,
		"Guide":         r.guide(ctx, pub.ID),
		"Stories":       ts,
	}, &triage)
	if err != nil {
		return fmt.Errorf("triage: %w", err)
	}
	st.res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
	r.emit("triage", map[string]int{"ranked": len(triage)})
	r.record(ctx, pub.ID, st.issueID, "triage", r.TriageModel, start, resp)
	scoreByRef := map[int]triageScore{}
	for _, t := range triage {
		scoreByRef[t.Ref] = triageScore{t.Score, t.Reason}
		if t.Ref-1 >= 0 && t.Ref-1 < len(stories) {
			r.DB.SetStoryTriage(ctx, stories[t.Ref-1].ID, t.Score, t.Reason)
		}
	}
	st.scoreByRef = scoreByRef
	return nil
}
