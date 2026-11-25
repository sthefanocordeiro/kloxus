package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// stageCollect fetches the publication's own sources and counts the
// shared-catalog items the scheduler already ingested. When nothing is
// new, no previous issue exists and the unused bench is empty it sets
// res.NoNewItems so RunPhaseA can stop early.
func (r *Runner) stageCollect(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	res := st.res
	r.stepStart("collect")
	start := time.Now()
	srcs, err := r.DB.Sources(ctx, pub.ID)
	if err != nil {
		return err
	}
	hasCatalog := false
	for _, s := range srcs {
		if s.CatalogID != 0 {
			// Catalog sources are ingested globally by the scheduler —
			// a run never fetches them itself (D29: one request per
			// interval no matter how many workspaces run).
			hasCatalog = true
			r.DB.MarkSourceFetched(ctx, s.ID, s.Name, "ok: shared catalog")
			continue
		}
		if r.DemoMode && !strings.HasPrefix(s.URL, "fixtures/") {
			// Visitor-added sources are never fetched on the hosted demo
			// (SSRF surface); they are stored but skipped at collect time.
			r.DB.MarkSourceFetched(ctx, s.ID, "", "simulated in demo mode: sources are not fetched")
			continue
		}
		raw, err := r.Fetcher.Fetch(ctx, s.Type, s.URL)
		if err != nil {
			r.DB.MarkSourceFetched(ctx, s.ID, "", "error: "+truncate(err.Error(), 120))
			continue // one bad source must not abort the run
		}
		n, err := r.DB.SaveItems(ctx, s.ID, raw, dedupe.Canonical)
		if err != nil {
			return err
		}
		outlet := ""
		if len(raw) > 0 {
			outlet = raw[0].Outlet
		}
		r.DB.MarkSourceFetched(ctx, s.ID, outlet, fmt.Sprintf("ok: %d items, %d new", len(raw), n))
		res.Items += len(raw)
		res.New += n
	}
	if hasCatalog {
		// What's "new" for a shared source is what the scheduler stored
		// after this publication's last issue, not what this run fetched.
		total, fresh, err := r.DB.CatalogItemsStats(ctx, pub.ID)
		if err != nil {
			return err
		}
		res.Items += total
		res.New += fresh
	}
	if res.New == 0 {
		poolAvailable := false
		if _, err := r.DB.LatestIssue(ctx, pub.ID); err == nil {
			// "Nothing new" means nothing to DRAFT, not nothing fetched:
			// a fetch that turns up zero new items must not block a next
			// edition while the bench still holds unused suggestions
			// (KLOX-100). Only when the unused pool is empty is the run
			// genuinely idle. A count error plays it safe and proceeds —
			// an empty board is recoverable, a false "nothing new" is not.
			unused, uerr := r.DB.UnusedCandidateCount(ctx, pub.ID)
			if uerr == nil && unused == 0 {
				r.emit("collect", map[string]int{"items_read": res.Items, "new_items": res.New})
				res.NoNewItems = true
				return nil
			}
			poolAvailable = true
		}
		if res.Items == 0 && !poolAvailable {
			return fmt.Errorf("no items collected — check the publication's sources")
		}
	}
	r.emit("collect", map[string]int{"items_read": res.Items, "new_items": res.New})
	r.record(ctx, pub.ID, 0, "collect", "", start, llm.Response{})
	return nil
}
