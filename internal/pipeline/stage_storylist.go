package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageStorylist picks and sections the issue's items — the
// deterministic lineup picker or one model call — then creates the
// pending issue items, merging same-event stories, skipping picks with
// no evidence body and re-slotting any section a skip left empty.
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
	var picks []lineupPick
	if r.Deterministic {
		picks = pickLineupVec(stories, st.detScores, st.detReasons, pub, st.enrich, st.hiddenStory, st.lineupVec)
		r.record(ctx, pub.ID, issueID, "storylist", "deterministic", start, llm.Response{})
	} else {
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
			"RejectTerms":   pub.RejectTerms,
			"Guide":         r.guide(ctx, pub.ID),
			"Stories":       ss,
		}, &picked)
		if err != nil {
			return fmt.Errorf("storylist: %w", err)
		}
		res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
		r.record(ctx, pub.ID, issueID, "storylist", r.TriageModel, start, resp)
		for _, p := range picked.Items {
			picks = append(picks, lineupPick{Refs: p.Refs, Title: p.Title, Why: p.Why, Section: p.Section})
		}
	}

	// --- create issue items, merging same-event stories ---

	entries := []entry{}
	used := make([]bool, len(stories))
	for idx, p := range picks {
		if len(p.Refs) == 0 {
			continue
		}
		primaryIdx := p.Refs[0] - 1
		if primaryIdx < 0 || primaryIdx >= len(stories) {
			return fmt.Errorf("storylist: ref %d out of range", p.Refs[0])
		}
		if used[primaryIdx] {
			// The model picked the same story twice (as a primary or
			// inside an earlier merge) — skip it rather than violate
			// issue_items' one-row-per-story constraint (KLOX-220).
			continue
		}
		used[primaryIdx] = true
		primary := stories[primaryIdx]
		var merges []int64
		memberItems := append([]store.Item{}, primary.Items...)
		for _, ref := range p.Refs[1:] {
			if ref-1 < 0 || ref-1 >= len(stories) {
				continue
			}
			used[ref-1] = true
			sec := stories[ref-1]
			merges = append(merges, sec.ID)
			memberItems = append(memberItems, sec.Items...)
		}
		if len(merges) > 0 {
			if err := r.DB.MergeStories(ctx, primary.ID, merges); err != nil {
				return err
			}
		}
		memberItems = capEntrySources(evidenceItems(memberItems))
		if len(memberItems) == 0 {
			// A story with only signal members (a bare HN/DJ link) has
			// no evidence body — it can't be drafted or cited. Its
			// section is re-slotted below rather than left stranded.
			continue
		}
		sc := scoreByRef[p.Refs[0]]
		iiID, err := r.DB.CreateIssueItem(ctx, store.IssueItem{
			IssueID: issueID,
			StoryID: primary.ID,
			Section: p.Section,
			Score:   sc.score,
			Reason:  p.Why,
			Status:  store.ItemPending,
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
	// Re-slot any section a skipped pick left empty: the best still-
	// unused story that carries evidence takes the slot, so a
	// collection can't strand a required section while draftable
	// stories remain in the pool (KLOX-60 — the deterministic picker
	// already skips evidence-less stories; this covers a model
	// storylist that doesn't). A section only stays empty when the
	// pool truly has no story that can back an item.
	sectionScores := make([]float64, len(stories))
	for i := range stories {
		sectionScores[i] = scoreByRef[i+1].score
	}
	filled := map[string]int{}
	for _, e := range entries {
		filled[e.section]++
	}
	nextRef := len(picks)
	reslotted := 0
	for _, sec := range pub.Sections {
		if filled[sec.Title] > 0 {
			continue
		}
		for _, idx := range storyOrder(stories, sectionScores) {
			if used[idx] || !hasEvidence(&stories[idx]) {
				continue
			}
			used[idx] = true
			reslotted++
			sc := scoreByRef[idx+1]
			why := sc.reason
			if why != "" {
				why += " · "
			}
			if ptLang(pub.Language) {
				why += "realocada para preencher a seção vazia"
			} else {
				why += "re-slotted to fill an empty section"
			}
			iiID, err := r.DB.CreateIssueItem(ctx, store.IssueItem{
				IssueID: issueID,
				StoryID: stories[idx].ID,
				Section: sec.Title,
				Score:   sc.score,
				Reason:  why,
				Status:  store.ItemPending,
			})
			if err != nil {
				return err
			}
			nextRef++
			entries = append(entries, entry{
				iiID: iiID, storyID: stories[idx].ID, ref: nextRef,
				title: stories[idx].Title, section: sec.Title, why: why,
				score:   sc.score,
				sources: capEntrySources(evidenceItems(stories[idx].Items)),
			})
			break
		}
	}

	// The "selected" count the header shows is the lineup the issue
	// actually renders: picks with no evidence body (signal-only
	// stories) create no issue item and don't count (KLOX-55).
	res.Selected = len(entries)
	r.emit("storylist", map[string]int{"selected": res.Selected, "reslotted": reslotted})
	st.entries = entries
	return nil
}
