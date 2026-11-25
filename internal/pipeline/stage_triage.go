package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageTriage scores every story for the publication's audience. In
// deterministic mode it runs the free ranking (enrichment, stage-1
// embeddings, stage-2 rerank, summaries) and stores the whole result
// in one UPDATE; otherwise it asks the model once, capped by a cheap
// pre-rank when the batch overflows the prompt.
func (r *Runner) stageTriage(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	stories := st.stories
	res := st.res
	issueID := st.issueID
	r.stepStart("triage")
	start := time.Now()
	type triStory struct {
		Ref     int
		Title   string
		Outlets string
		Snippet string
	}
	// Real catalogs yield hundreds of stories and triage answers one row
	// per story — an unbounded response truncates mid-JSON, a scale the
	// fixture never reaches. Cap the prompt by a cheap pre-rank when the
	// batch overflows; leftovers keep score 0 and sit on the bench.
	// Under the cap the original order stands, so replay prompts are
	// unchanged.
	triIdx := make([]int, len(stories))
	for i := range triIdx {
		triIdx[i] = i
	}
	if len(triIdx) > maxTriageStories {
		triIdx = triageOrder(stories)[:maxTriageStories]
	}
	ts := make([]triStory, len(triIdx))
	for i, si := range triIdx {
		s := stories[si]
		snip := ""
		if len(s.Items) > 0 {
			snip = truncate(s.Items[0].ContentText, 400)
		}
		ts[i] = triStory{Ref: i + 1, Title: s.Title, Outlets: strings.Join(s.Outlets(), ", "), Snippet: snip}
	}
	scoreByRef := map[int]triageScore{}
	var detScores []float64
	var detReasons []string
	var hiddenStory []bool
	var lineupVec *lineupVectors
	var enrich map[int64]*store.ItemEnrichment
	if r.Deterministic {
		// The free ranking reads the shared enrichment when it exists
		// (KLOX-67 §7.2): topic tags matching the publication's topic or
		// editorial line and the story's kind weight adjust the
		// recency + diversity score. No paid call happens here — the
		// enrichment fills asynchronously after ingest.
		var eids []int64
		for i := range stories {
			for j := range stories[i].Items {
				eids = append(eids, stories[i].Items[j].ID)
			}
		}
		enrich, _ = r.DB.EnrichmentsForItems(ctx, eids)
		detScores, detReasons = scoreStoriesEnriched(stories, pub.Language, pub, enrich)
		// Stage 1 (KLOX-109): embedding cosine against the publication
		// profile (FTS when vectors are missing) replaces the recency +
		// diversity order; the editorial-line demotions stay on top.
		s1 := r.rankStage1(ctx, pub, stories, detReasons, enrich)
		r.rerankStage(ctx, pub, stories, &s1) // stage 2 (KLOX-110); silent on failure
		detScores, detReasons = s1.Scores, s1.Reasons
		hid := make([]bool, len(stories))
		for i, h := range s1.Hidden {
			hid[i] = h != ""
		}
		SummarizeTop(ctx, SummaryPool, stories, detScores, hid, enrich) // G4 (KLOX-111); fail-soft
		lineupVec = &lineupVectors{Story: s1.StoryVecs, Section: s1.SectionVecs}
		hiddenStory = make([]bool, len(stories))
		for i, h := range s1.Hidden {
			hiddenStory[i] = h != ""
		}
		updates := make([]store.StoryTriage, len(stories))
		for i := range stories {
			scoreByRef[i+1] = triageScore{detScores[i], detReasons[i]}
			updates[i] = store.StoryTriage{StoryID: stories[i].ID, Score: detScores[i], Reason: detReasons[i],
				Rel: s1.Rel[i], Hidden: s1.Hidden[i]}
		}
		// One UPDATE for the whole run: deterministic triage scores
		// every story, and at shared-catalog scale a call per story
		// is thousands of remote round-trips (KLOX-41).
		if err := r.DB.SetStoriesTriage(ctx, updates); err != nil {
			return err
		}
		r.emit("triage", map[string]int{"ranked": len(stories)})
		r.record(ctx, pub.ID, issueID, "triage", "deterministic", start, llm.Response{})
	} else {
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
			"RejectTerms":   pub.RejectTerms,
			"Guide":         r.guide(ctx, pub.ID),
			"Stories":       ts,
		}, &triage)
		if err != nil {
			return fmt.Errorf("triage: %w", err)
		}
		res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
		r.emit("triage", map[string]int{"ranked": len(triage)})
		r.record(ctx, pub.ID, issueID, "triage", r.TriageModel, start, resp)
		for _, t := range triage {
			if t.Ref-1 >= 0 && t.Ref-1 < len(triIdx) {
				si := triIdx[t.Ref-1]
				scoreByRef[si+1] = triageScore{t.Score, t.Reason}
				r.DB.SetStoryTriage(ctx, stories[si].ID, t.Score, t.Reason)
			}
		}
	}
	st.scoreByRef = scoreByRef
	st.detScores = detScores
	st.detReasons = detReasons
	st.hiddenStory = hiddenStory
	st.lineupVec = lineupVec
	st.enrich = enrich
	return nil
}
