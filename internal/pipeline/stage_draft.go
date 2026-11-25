package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageDraft asks the model to draft every model-facing item, then
// synthesizes a body (dossier summary + facts) for any approved item
// that was never part of the canonical draft input — board alternatives.
func (r *Runner) stageDraft(ctx context.Context, pub *store.Publication, st *phaseBState) error {
	modelEntries := st.modelEntries
	modelDossiers := st.modelDossiers
	r.stepStart("draft")
	start := time.Now()
	type drItem struct {
		Ref            int
		Section        string
		Score          float64
		Title          string
		Summary        string
		Facts          []DossierFact
		DeepFacts      []DossierFact
		Interpretation []string
	}
	drItems := make([]drItem, len(modelEntries))
	for i, e := range modelEntries {
		d, ok := modelDossiers[e.ref]
		if !ok {
			return fmt.Errorf("draft: no dossier for item ref %d", e.ref)
		}
		drItems[i] = drItem{
			Ref: e.ref, Section: e.section, Score: e.score, Title: e.title,
			Summary: d.Summary, Facts: d.Facts, DeepFacts: d.DeepFacts,
			Interpretation: d.Interpretation,
		}
	}
	var drafted struct {
		Entries []struct {
			Ref      int    `json:"ref"`
			Headline string `json:"headline"`
			BodyMD   string `json:"body_md"`
		} `json:"entries"`
	}
	resp, err := r.call(ctx, r.DraftModel, "draft", map[string]any{
		"Publication": pub.Name,
		"Audience":    pub.Audience,
		"Language":    pub.Language,
		"Tone":        pub.Tone,
		"Items":       drItems,
	}, &drafted)
	if err != nil {
		return fmt.Errorf("draft: %w", err)
	}
	st.res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
	r.emit("draft", map[string]int{"entries": len(drafted.Entries)})
	r.record(ctx, pub.ID, st.issueID, "draft", r.DraftModel, start, resp)
	draftByRef := map[int]draftEntry{}
	for _, de := range drafted.Entries {
		draftByRef[de.Ref] = draftEntry{de.Headline, de.BodyMD}
	}
	// Alternative items are never part of the canonical draft input, so
	// they have no model-drafted body: synthesize one from the dossier
	// (summary carries [n] citations, so it passes the citation rules).
	for _, e := range st.entries {
		if _, ok := draftByRef[e.ref]; !ok {
			d := st.dossierByRef[e.ref]
			body := strings.TrimSpace(d.Summary)
			for _, f := range d.Facts {
				if s := strings.TrimSpace(f.Text); s != "" && !strings.Contains(body, s) {
					body += " " + ensureRefSuffix(s, f.Ref)
				}
			}
			draftByRef[e.ref] = draftEntry{e.title, body}
		}
	}
	st.draftByRef = draftByRef
	return nil
}
