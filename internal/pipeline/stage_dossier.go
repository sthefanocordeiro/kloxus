package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// dSrc is one numbered source inside a dossier/deepdive prompt item.
type dSrc struct {
	Ref  int
	URL  string
	Text string
}

// stageDossier asks the model for one research dossier per picked item,
// then rewrites each dossier's source list from the member items so the
// stored [n] refs match the sources the draft will cite.
func (r *Runner) stageDossier(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	entries := st.entries
	r.stepStart("dossier")
	start := time.Now()
	type dItem struct {
		Ref     int
		Title   string
		Sources []dSrc
	}
	dItems := make([]dItem, len(entries))
	for i, e := range entries {
		for j, it := range e.sources {
			dItems[i].Sources = append(dItems[i].Sources, dSrc{Ref: j + 1, URL: it.URL, Text: truncate(it.ContentText, 1200)})
		}
		dItems[i].Ref = e.ref
		dItems[i].Title = e.title
	}
	var dossiers []Dossier
	resp, err := r.call(ctx, r.DraftModel, "dossier", map[string]any{
		"Publication": pub.Name,
		"Language":    pub.Language,
		"Items":       dItems,
	}, &dossiers)
	if err != nil {
		return fmt.Errorf("dossier: %w", err)
	}
	st.res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
	r.emit("dossier", map[string]int{"dossiers": len(dossiers)})
	r.record(ctx, pub.ID, st.issueID, "dossier", r.DraftModel, start, resp)
	dossierByRef := map[int]*Dossier{}
	for i := range dossiers {
		dossierByRef[dossiers[i].Ref] = &dossiers[i]
	}
	for i, e := range entries {
		d, ok := dossierByRef[e.ref]
		if !ok {
			return fmt.Errorf("dossier: missing item %d", e.ref)
		}
		d.Title = e.title
		d.fillSources(e.sources)
		data, _ := json.Marshal(d)
		if err := r.DB.SaveDossier(ctx, e.iiID, string(data)); err != nil {
			return err
		}
		entries[i].sources = e.sources // keep for later steps
	}
	st.dossierByRef = dossierByRef
	return nil
}
