package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageDraft fills draftByRef for every item: the honest empty-body
// compose skeleton in deterministic mode, one model call otherwise —
// a spend-capped model exits early through ComposeSkeleton. Alternative
// items get a body synthesized from the dossier since they were never
// part of the canonical draft input.
func (r *Runner) stageDraft(ctx context.Context, pub *store.Publication, st *phaseBState) error {
	modelEntries := st.modelEntries
	modelDossiers := st.modelDossiers
	entries := st.entries
	dossierByRef := st.dossierByRef
	res := st.res
	issueID := st.issueID
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
	draftByRef := map[int]draftEntry{}
	aiDraftRefs := map[int]bool{}
	if r.Deterministic {
		// No-model draft = honest compose skeleton: headline + sources,
		// empty body. Assembling source excerpts into prose would put
		// words in the draft nobody wrote (KLOX-48).
		for _, e := range entries {
			draftByRef[e.ref] = draftEntry{e.title, ""}
		}
		r.emit("draft", map[string]int{"entries": len(entries)})
		r.record(ctx, pub.ID, issueID, "draft", "deterministic", start, llm.Response{})
	} else {
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
			if _, ok := llm.AsBlocked(err); ok {
				// Capped model = same honest fallback as model-off:
				// store the compose skeleton, never excerpts (KLOX-48).
				st.done, err = r.ComposeSkeleton(ctx, pub, issueID)
				return err
			}
			return fmt.Errorf("draft: %w", err)
		}
		res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
		r.emit("draft", map[string]int{"entries": len(drafted.Entries)})
		r.record(ctx, pub.ID, issueID, "draft", r.DraftModel, start, resp)
		for _, de := range drafted.Entries {
			draftByRef[de.Ref] = draftEntry{de.Headline, de.BodyMD}
			// The pauta body arrived AI-written — mark it "Gerado por IA"
			// until the author edits (KLOX-67). Synthesized alternative
			// bodies below are deterministic assembly, not AI text.
			if strings.TrimSpace(de.BodyMD) != "" {
				aiDraftRefs[de.Ref] = true
			}
		}
		// Alternative items are never part of the canonical draft input, so
		// they have no model-drafted body: synthesize one from the dossier
		// (summary carries [n] citations, so it passes the citation rules).
		for _, e := range entries {
			if _, ok := draftByRef[e.ref]; !ok {
				draftByRef[e.ref] = draftEntry{e.title, synthBody(dossierByRef[e.ref])}
			}
		}
	}
	st.draftByRef = draftByRef
	st.aiDraftRefs = aiDraftRefs
	return nil
}
