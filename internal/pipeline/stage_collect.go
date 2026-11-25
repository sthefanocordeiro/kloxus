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

// stageCollect fetches every configured source and stores the new
// items. When nothing is new and a previous issue exists it sets
// res.NoNewItems so RunPhaseA can stop early.
func (r *Runner) stageCollect(ctx context.Context, pub *store.Publication, st *phaseAState) error {
	res := st.res
	r.stepStart("collect")
	start := time.Now()
	srcs, err := r.DB.Sources(ctx, pub.ID)
	if err != nil {
		return err
	}
	for _, s := range srcs {
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
	if res.New == 0 {
		if _, err := r.DB.LatestIssue(ctx, pub.ID); err == nil {
			r.emit("collect", map[string]int{"items_read": res.Items, "new_items": res.New})
			res.NoNewItems = true
			return nil
		}
		if res.Items == 0 {
			return fmt.Errorf("no items collected — check the publication's sources")
		}
	}
	r.emit("collect", map[string]int{"items_read": res.Items, "new_items": res.New})
	r.record(ctx, pub.ID, 0, "collect", "", start, llm.Response{})
	return nil
}
