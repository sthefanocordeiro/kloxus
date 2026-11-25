package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageCitecheck checks every body sentence against the citation
// floor: the model verdict (skipped when capped — the deterministic
// rules still land, KLOX-48) plus the lexical rules a verdict cannot
// waive. Flags are stored on the issue, AI-written items get their
// ai_draft marker, and the issue moves to "drafted".
func (r *Runner) stageCitecheck(ctx context.Context, pub *store.Publication, st *phaseBState) error {
	checkBody := st.checkBody
	checkEntries := st.checkEntries
	checkDossiers := st.checkDossiers
	dossierByRef := st.dossierByRef
	entries := st.entries
	draftByRef := st.draftByRef
	aiDraftRefs := st.aiDraftRefs
	body := st.body
	res := st.res
	issueID := st.issueID
	r.stepStart("citecheck")
	start := time.Now()
	sents := extractSentences(checkBody)
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
	for _, e := range checkEntries {
		d, ok := checkDossiers[e.ref]
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
	if r.Deterministic {
		// Deterministic citation checks only (no model call): the rules
		// below flag uncited sentences and citation-free entries.
		r.record(ctx, pub.ID, issueID, "citecheck", "deterministic", start, llm.Response{})
	} else {
		resp, err := r.call(ctx, r.TriageModel, "citecheck", map[string]any{
			"Publication": pub.Name,
			"Sentences":   cs,
			"Items":       ci,
		}, &checks)
		if err != nil {
			if _, blocked := llm.AsBlocked(err); !blocked {
				return fmt.Errorf("citecheck: %w", err)
			}
			// Capped citecheck still lands the deterministic rules —
			// the draft stays, flagged by the lexical floor (KLOX-48).
		} else {
			res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
			r.record(ctx, pub.ID, issueID, "citecheck", r.TriageModel, start, resp)
		}
	}

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
	for _, e := range entries {
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
	// deterministic rules (KLOX-48): these apply under the model too —
	// the lexical test is the floor a model verdict cannot waive.
	//   - a body sentence with no citation is flagged;
	//   - every [n] must be a source of its item, and the cited source's
	//     stored text must support the sentence ("not supported by [n]");
	//   - a sentence copying ≥10 consecutive source words is rejected
	//     ("too close to the source") unless it is a short attributed
	//     quotation;
	//   - an entry whose body carries no citation at all is flagged.
	textByItemID, err := r.sourceTexts(ctx, dossierByRef)
	if err != nil {
		return err
	}
	for _, s := range extractSentences(body) {
		d := dossierByRef[s.itemRef]
		if s.refs == "" {
			addFlag(int64(s.itemRef), s.text, "sentence has no citation")
		} else if d != nil {
			byRef := map[int]DossierSource{}
			for _, src := range d.Sources {
				byRef[src.Ref] = src
			}
			for _, n := range CitationNums(s.refs) {
				src, ok := byRef[n]
				if !ok {
					addFlag(int64(s.itemRef), s.text,
						fmt.Sprintf("cites source [%d] that does not exist", n))
					continue
				}
				corpus := textByItemID[src.ItemID]
				switch {
				case !SupportedBySource(s.text, corpus):
					addFlag(int64(s.itemRef), s.text,
						fmt.Sprintf("not supported by [%d]", n))
				case UnsupportedDate(s.text, corpus):
					addFlag(int64(s.itemRef), s.text,
						fmt.Sprintf("date not supported by [%d]", n))
				case NegationUnsupported(s.text, corpus):
					addFlag(int64(s.itemRef), s.text,
						fmt.Sprintf("negation not supported by [%d]", n))
				}
			}
		}
		if d != nil {
			for _, src := range d.Sources {
				if TooCloseToSource(s.text, textByItemID[src.ItemID]) {
					addFlag(int64(s.itemRef), s.text, "too close to the source")
					break
				}
			}
		}
	}
	for _, e := range entries {
		de, ok := draftByRef[e.ref]
		if ok && de.body != "" && !refRe.MatchString(de.body) {
			addFlag(int64(e.ref), truncate(de.body, 120), "entry has no citations")
		}
	}
	flagsJSON, _ := json.Marshal(res.Flagged)
	if res.Flagged == nil {
		flagsJSON = []byte("[]")
	}
	if err := r.DB.UpdateIssue(ctx, issueID, "drafted", body, string(flagsJSON)); err != nil {
		return err
	}
	for _, e := range entries {
		if aiDraftRefs[e.ref] {
			if err := r.DB.SetIssueItemAIDraft(ctx, e.iiID, true); err != nil {
				return err
			}
		}
	}
	r.emit("citecheck", map[string]int{"checked": len(sents), "flagged": len(res.Flagged)})
	res.Number = st.number
	return nil
}
