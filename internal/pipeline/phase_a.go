package pipeline

import "github.com/sthefanocordeiro/kloxus/internal/store"

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
