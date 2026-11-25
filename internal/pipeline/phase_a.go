package pipeline

import "github.com/sthefanocordeiro/kloxus/internal/store"

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
