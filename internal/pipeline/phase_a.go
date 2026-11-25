package pipeline

import (
	"context"
	"fmt"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// phaseAState carries what one Phase A stage hands to the next:
// collect fills the res counters, dedupe produces the stories,
// triage scores them by ref (and leaves the deterministic-ranking
// context storylist needs), storylist turns the picks into issue
// entries, and dossier/deepdive enrich dossierByRef.
type phaseAState struct {
	res          *Result
	issueID      int64
	stories      []store.Story
	scoreByRef   map[int]triageScore
	detScores    []float64
	detReasons   []string
	hiddenStory  []bool
	lineupVec    *lineupVectors
	enrich       map[int64]*store.ItemEnrichment
	entries      []entry
	dossierByRef map[int]*Dossier
}

// triageScore is the score and reason for one story ref — either the
// model's or the deterministic ranker's.
type triageScore struct {
	score  float64
	reason string
}

// createIssue allocates the issue row mid-run so all model steps attach
// their run records to it, then reports it to OnIssue. The subject uses
// the publication's localized edition word and the issue snapshots the
// section definitions as they are now.
func (r *Runner) createIssue(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	number, err := r.DB.NextIssueNumber(ctx, pub.ID)
	if err != nil {
		return err
	}
	st.res.Number = number
	// the stored subject mirrors assembleIssue's body heading — the
	// issue chrome, mail subject, and export title all render it as-is
	// (KLOX-56).
	word := "issue"
	if ptLang(pub.Language) {
		word = "edição"
	}
	// The issue snapshots the publication's section definitions: builder
	// edits land on the next edition, not the one already in progress
	// (KLOX-65).
	issueID, err := r.DB.CreateIssue(ctx, pub.ID, number,
		fmt.Sprintf("%s — %s %d", pub.Name, word, number), "", pub.Sections)
	if err != nil {
		return err
	}
	st.issueID = issueID
	st.res.IssueID = issueID
	r.DB.AttachRuns(ctx, pub.ID, issueID)
	if r.OnIssue != nil {
		r.OnIssue(issueID)
	}
	return nil
}
