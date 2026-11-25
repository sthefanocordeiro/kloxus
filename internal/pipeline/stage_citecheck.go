package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageCitecheck has the model verify every sentence of the checked
// body against its sources, flags what it can't verify (plus the
// deterministic no-citation rules), and saves the drafted body with
// its flags on the issue.
func (r *Runner) stageCitecheck(ctx context.Context, pub *store.Publication, st *phaseBState) error {
	res := st.res
	r.stepStart("citecheck")
	start := time.Now()
	sents := extractSentences(st.checkBody)
	type cSent struct {
		ID   int
		Item int
		Refs string
		Text string
	}
	type cItem struct {
		Ref     int
		Sources []struct {
			Ref int
			URL string
		}
	}
	cs := []cSent{}
	ci := []cItem{}
	for _, e := range st.checkEntries {
		d, ok := st.checkDossiers[e.ref]
		if !ok {
			return fmt.Errorf("citecheck: no dossier for item ref %d", e.ref)
		}
		c := cItem{Ref: e.ref}
		for _, s := range d.Sources {
			c.Sources = append(c.Sources, struct {
				Ref int
				URL string
			}{s.Ref, s.URL})
		}
		ci = append(ci, c)
	}
	for _, s := range sents {
		cs = append(cs, cSent{ID: s.id, Item: s.itemRef, Refs: s.refs, Text: s.text})
	}
	var checks []struct {
		ID      int    `json:"id"`
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
	}
	resp, err := r.call(ctx, r.TriageModel, "citecheck", map[string]any{
		"Publication": pub.Name,
		"Sentences":   cs,
		"Items":       ci,
	}, &checks)
	if err != nil {
		return fmt.Errorf("citecheck: %w", err)
	}
	res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
	r.record(ctx, pub.ID, st.issueID, "citecheck", r.TriageModel, start, resp)

	sentByID := map[int]sentRec{}
	for _, s := range sents {
		sentByID[s.id] = s
	}
	seen := map[string]bool{}
	addFlag := func(item int64, sentence, note string) {
		k := fmt.Sprintf("%d|%s", item, sentence)
		if seen[k] {
			return
		}
		seen[k] = true
		res.Flagged = append(res.Flagged, Flagged{Item: item, Sentence: sentence, Note: note})
	}
	activeRef := map[int]bool{}
	for _, e := range st.entries {
		activeRef[e.ref] = true
	}
	for _, c := range checks {
		if c.Verdict != "unsupported" {
			continue
		}
		s, ok := sentByID[c.ID]
		if !ok || !activeRef[s.itemRef] {
			continue
		}
		addFlag(int64(s.itemRef), s.text, c.Note)
	}
	// deterministic rules: a body sentence with no citation, or an entry
	// with no citation at all, is flagged without needing the model.
	for _, s := range extractSentences(st.body) {
		if s.refs == "" {
			addFlag(int64(s.itemRef), s.text, "sentence has no citation")
		}
	}
	for _, e := range st.entries {
		de, ok := st.draftByRef[e.ref]
		if ok && !refRe.MatchString(de.body) {
			addFlag(int64(e.ref), truncate(de.body, 120), "entry has no citations")
		}
	}
	flagsJSON, _ := json.Marshal(res.Flagged)
	if res.Flagged == nil {
		flagsJSON = []byte("[]")
	}
	if err := r.DB.UpdateIssue(ctx, st.issueID, "drafted", st.body, string(flagsJSON)); err != nil {
		return err
	}
	r.emit("citecheck", map[string]int{"checked": len(sents), "flagged": len(res.Flagged)})
	res.Number = st.number
	return nil
}
