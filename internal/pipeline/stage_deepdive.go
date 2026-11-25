package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageDeepdive enriches the top-scored items' dossiers with extra
// facts and unknowns, saves them again, and flips the issue to
// "reviewing" — Phase A's hand-off to the board.
func (r *Runner) stageDeepdive(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	entries := st.entries
	dossierByRef := st.dossierByRef
	r.stepStart("deepdive")
	start := time.Now()
	topN := r.MaxDeepDives
	if topN <= 0 || topN > 3 {
		topN = 3
	}
	sorted := append([]entry{}, entries...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].score > sorted[b].score })
	if len(sorted) > topN {
		sorted = sorted[:topN]
	}
	type ddItem struct {
		Ref     int
		Title   string
		Dossier string
		Sources []dSrc
	}
	var ddItems []ddItem
	for _, e := range sorted {
		d := *dossierByRef[e.ref]
		d.Title = ""
		dj, _ := json.Marshal(d)
		di := ddItem{Ref: e.ref, Title: e.title, Dossier: string(dj)}
		for j, it := range e.sources {
			di.Sources = append(di.Sources, dSrc{Ref: j + 1, URL: it.URL, Text: truncate(it.ContentText, 1200)})
		}
		ddItems = append(ddItems, di)
	}
	var deep []struct {
		Ref       int `json:"ref"`
		DeepFacts []struct {
			Text string `json:"text"`
			Ref  int    `json:"ref"`
		} `json:"deep_facts"`
		ExtraUnknowns []string `json:"extra_unknowns"`
	}
	resp, err := r.call(ctx, r.DraftModel, "deepdive", map[string]any{
		"Publication": pub.Name,
		"Language":    pub.Language,
		"Items":       ddItems,
	}, &deep)
	if err != nil {
		return fmt.Errorf("deepdive: %w", err)
	}
	st.res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
	r.emit("deepdive", map[string]int{"items": len(ddItems)})
	r.record(ctx, pub.ID, st.issueID, "deepdive", r.DraftModel, start, resp)
	for _, dd := range deep {
		d, ok := dossierByRef[dd.Ref]
		if !ok {
			continue
		}
		for _, f := range dd.DeepFacts {
			d.DeepFacts = append(d.DeepFacts, DossierFact{Text: f.Text, Ref: f.Ref})
		}
		d.Unknowns = append(d.Unknowns, dd.ExtraUnknowns...)
	}
	for _, e := range entries {
		data, _ := json.Marshal(dossierByRef[e.ref])
		if err := r.DB.SaveDossier(ctx, e.iiID, string(data)); err != nil {
			return err
		}
	}
	return r.DB.SetIssueStatus(ctx, st.res.IssueID, "reviewing")
}
