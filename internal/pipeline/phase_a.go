package pipeline

import (
	"context"
	"fmt"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// phaseAState carries what one Phase A stage hands to the next:
// collect fills the res counters, dedupe produces the stories,
// triage scores them by ref, storylist turns the picks into issue
// entries, and dossier/deepdive enrich dossierByRef.
type phaseAState struct {
	res          *Result
	issueID      int64
	stories      []store.Story
	scoreByRef   map[int]triageScore
	entries      []entry
	dossierByRef map[int]*Dossier
}

// triageScore is the model's score and reason for one story ref.
type triageScore struct {
	score  float64
	reason string
}

// createIssue allocates the issue row mid-run so all model steps attach
// their run records to it, then reports it to OnIssue.
func (r *Runner) createIssue(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	number, err := r.DB.NextIssueNumber(ctx, pub.ID)
	if err != nil {
		return err
	}
	st.res.Number = number
	issueID, err := r.DB.CreateIssue(ctx, pub.ID, number,
		fmt.Sprintf("%s — issue %d", pub.Name, number), "")
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
