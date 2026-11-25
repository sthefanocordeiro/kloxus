package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// phaseBState carries the draft-time working set. entries/dossierByRef
// are the approved items that actually ship; modelEntries/modelDossiers
// are the model-facing input — the canonical Phase A set in demo mode
// so the replay fixture keys stay stable.
type phaseBState struct {
	res           *Result
	issueID       int64
	entries       []entry
	dossierByRef  map[int]*Dossier
	modelEntries  []entry
	modelDossiers map[int]*Dossier
	draftByRef    map[int]draftEntry
	number        int
	sectionOrder  []string
	body          string
	checkBody     string
	checkEntries  []entry
	checkDossiers map[int]*Dossier
}

// draftEntry is the model-drafted (or synthesized) headline and body
// for one item ref.
type draftEntry struct {
	headline string
	body     string
}

// loadEntries rebuilds the entry list for an existing issue from the DB:
// issue items, their dossiers and story titles.
//
// canonical=true returns every Phase A item regardless of review status —
// the stable input that keeps demo-mode draft/citecheck replay keys
// fixed. canonical=false returns only approved items (plus pending? no —
// the draft contains approved items only), which is what actually ships.
func (r *Runner) loadEntries(ctx context.Context, issueID int64, canonical bool) ([]entry, map[int]*Dossier, error) {
	iis, err := r.DB.IssueItems(ctx, issueID)
	if err != nil {
		return nil, nil, err
	}
	// issue items were inserted in picked order; ref numbers were assigned
	// positionally then, but dossiers store their own ref, so reload in id order
	sort.Slice(iis, func(a, b int) bool { return iis[a].ID < iis[b].ID })
	var entries []entry
	dossierByRef := map[int]*Dossier{}
	for _, ii := range iis {
		if canonical {
			// Only the AI-picked items belong to the recorded
			// canonical input — alternatives and manual adds would
			// change the replay keys.
			if ii.Origin != "phaseA" {
				continue
			}
		} else if ii.Status != "approved" {
			continue
		}
		djson, err := r.DB.Dossier(ctx, ii.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("issue_item %d: no dossier", ii.ID)
		}
		var d Dossier
		if err := json.Unmarshal([]byte(djson), &d); err != nil {
			return nil, nil, fmt.Errorf("issue_item %d: %w", ii.ID, err)
		}
		title := d.Title
		if title == "" {
			title, _ = r.DB.StoryTitle(ctx, ii.StoryID)
		}
		section := ii.Section
		if canonical && ii.OrigSection != "" {
			// replay input must look exactly like the recorded Phase A
			// output — board moves mutate Section, OrigSection holds the
			// pick
			section = ii.OrigSection
		}
		entries = append(entries, entry{
			iiID: ii.ID, storyID: ii.StoryID, ref: d.Ref,
			title: title, section: section, why: ii.Reason, score: ii.Score,
			position: ii.Position,
		})
		dd := d
		dossierByRef[d.Ref] = &dd
	}
	return entries, dossierByRef, nil
}

// ensureDossiers generates dossiers for lineup items that lack one —
// stories the creator added on the board get theirs at draft time,
// so the draft run can pick up any board composition.
func (r *Runner) ensureDossiers(ctx context.Context, pub *store.Publication, issueID int64) error {
	iis, err := r.DB.IssueItems(ctx, issueID)
	if err != nil {
		return err
	}
	maxRef := 0
	var missing []store.IssueItem
	for _, ii := range iis {
		if dj, err := r.DB.Dossier(ctx, ii.ID); err == nil {
			var d Dossier
			if json.Unmarshal([]byte(dj), &d) == nil && d.Ref > maxRef {
				maxRef = d.Ref
			}
			continue
		}
		if ii.Status == "approved" || ii.Status == "pending" {
			missing = append(missing, ii)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	r.stepStart("dossier")
	for _, ii := range missing {
		cand, err := r.DB.StoryWithItems(ctx, ii.StoryID)
		if err != nil {
			return err
		}
		maxRef++
		d := r.alternativeDossier(ctx, pub, cand, maxRef)
		data, _ := json.Marshal(d)
		if err := r.DB.SaveDossier(ctx, ii.ID, string(data)); err != nil {
			return err
		}
	}
	r.emit("dossier", map[string]int{"dossiers": len(missing)})
	return nil
}
