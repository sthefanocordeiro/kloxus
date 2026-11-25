package ingest

// The global ingestion scheduler (D29). One process-wide loop polls
// each catalog source at its own interval — feeds are fetched once for
// all workspaces, never per tenant, so N workspaces still mean one
// request per interval. Polls run serially; per-host crawl delays are
// enforced by the throttler regardless of which source is next.
import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/httpx"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// DefaultUserAgent identifies us to publishers (D27): name + contact.
const DefaultUserAgent = "KloxusBot/1.0 (+https://kloxus.fly.dev/about)"

// Scheduler polls the whole catalog on its own cadence.
type Scheduler struct {
	pool      Store
	feed      *FeedFetcher
	tick      time.Duration // wake interval between due checks
	log       *log.Logger
	now       func() time.Time
	lastPurge time.Time
	// Daily polls Daily Journal's public API. Dormant since KLOX-114
	// (the seed row left the catalog); SetupDailyJournal only wires it
	// when a 'daily-journal' row exists. Nil disables API sources;
	// tests inject an httptest client.
	Daily *DailyJournal
	// AfterPoll, when set, runs once after each PollOnce — the shared
	// AI enrichment sweep (KLOX-67) and the deterministic semantic
	// sweep (KLOX-93) hook in here so new items get classified without
	// blocking feed polls.
	AfterPoll func(ctx context.Context)
}

// NewScheduler wires the feed fetcher + retention on the pool.
func NewScheduler(p Store, ua string, logger *log.Logger) *Scheduler {
	throttle := &hostThrottle{last: map[string]time.Time{}}
	hc := httpx.Client(30 * time.Second)
	return &Scheduler{
		pool: p,
		feed: NewFeedFetcher(p, hc, ua, "KloxusBot", throttle),
		tick: time.Minute,
		log:  logger,
		now:  time.Now,
	}
}

// hostThrottle serializes per-host request spacing (in-process; the
// catalog's per-source intervals bound cross-process rates).
type hostThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (t *hostThrottle) wait(ctx context.Context, host string, delay time.Duration) error {
	t.mu.Lock()
	until := t.last[host].Add(delay)
	wait := time.Until(until)
	t.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

func (t *hostThrottle) mark(host string) {
	t.mu.Lock()
	t.last[host] = time.Now()
	t.mu.Unlock()
}

// Run is the daemon loop: check the catalog every tick, purge expired
// bodies hourly. It returns when ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.PollOnce(ctx)
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.PollOnce(ctx)
		}
	}
}

// PollOnce fetches every due source once — also the `kloxus ingest`
// one-shot path. Serial on purpose: politeness beats throughput.
func (s *Scheduler) PollOnce(ctx context.Context) {
	cat, err := s.pool.Catalog(ctx)
	if err != nil {
		s.logf("catalog load: %v", err)
		return
	}
	for i := range cat {
		cs := &cat[i]
		if ctx.Err() != nil {
			return
		}
		if !cs.Due(s.now()) {
			continue
		}
		switch cs.Kind {
		case "rss":
			o := s.feed.Fetch(ctx, cs)
			if o.Status == "error" {
				s.logf("%s: %s", cs.Slug, o.Detail)
			}
		case "api":
			s.pollAPI(ctx, cs)
		}
	}
	s.purgeBodies(ctx)
	if s.AfterPoll != nil {
		s.AfterPoll(ctx)
	}
}

// SetupDailyJournal wires the production Daily Journal client for the
// seeded catalog row — request counter bound to the row's api_day/
// api_count columns, base URL from the row. A missing or non-API row
// leaves Daily nil and the pollAPI branch stays inert.
func (s *Scheduler) SetupDailyJournal(ctx context.Context, ua string) {
	cat, err := s.pool.Catalog(ctx)
	if err != nil {
		s.logf("daily-journal setup: %v", err)
		return
	}
	for i := range cat {
		if cat[i].Kind == "api" && cat[i].Slug == "daily-journal" {
			s.Daily = NewDailyJournal(
				Counter(s.pool, cat[i].ID), nil, ua, cat[i].URL)
			return
		}
	}
}

// pollAPI dispatches API-backed catalog sources. Daily Journal is the
// only one the client knows (slug 'daily-journal' — out of the catalog
// since KLOX-114, kept for an opt-in re-seed); unknown API slugs are
// skipped rather than guessed.
func (s *Scheduler) pollAPI(ctx context.Context, cs *store.CatalogSource) {
	if cs.Slug != "daily-journal" || s.Daily == nil {
		return
	}
	if !cs.Enabled || cs.Suspended {
		return
	}
	if cs.Policy != "launch" && cs.Policy != "signal" {
		return // same gate as feeds: hold/blocked are never fetched
	}
	out := s.pollDailyJournal(ctx, cs)
	if out.Status == "error" {
		s.logf("%s: %s", cs.Slug, out.Detail)
	}
	// Detail backfill rides inside the same budget check: whatever
	// requests the day still has left — minus the reserve held back for
	// tomorrow's list pages — goes to filling missing aggregate bodies,
	// oldest-pending-first (the query orders newest first, so the limit
	// slices the fresh edge).
	if ctx.Err() != nil {
		return
	}
	s.backfillDetails(ctx, cs)
}

// DailyListPages bounds each poll's list sweep — 7×50 covers a full
// DJ day (~300 items) with headroom for bursts.
const DailyListPages = 7

// pollDailyJournal runs one list sweep: newest-first pages per DJ's
// own poll guidance, then the upsert decides what is actually new.
// Idempotent: re-scanning unchanged stories saves nothing.
func (s *Scheduler) pollDailyJournal(ctx context.Context, cs *store.CatalogSource) FetchOutcome {
	items, err := s.Daily.Poll(ctx, DailyListPages)
	if err != nil {
		detail := err.Error()
		if rl, ok := err.(*ErrRateLimited); ok {
			detail = "HTTP 429 (retry-after " + rl.RetryAfter.String() + ")"
		}
		if lerr := s.pool.CatalogMarkFetched(ctx, cs.ID, cs.ETag, cs.LastModified, "error"); lerr != nil {
			s.log.Printf("ingest: mark fetched failed: %v", lerr)
		}
		if lerr := s.pool.LogFetch(ctx, cs.ID, "error", 0, len(items), 0, detail); lerr != nil {
			s.log.Printf("ingest: log fetch failed: %v", lerr)
		}
		return FetchOutcome{Status: "error", Detail: detail}
	}
	raw := make([]store.CatalogItem, 0, len(items))
	for _, it := range items {
		raw = append(raw, s.Daily.ToCatalogItem(it))
	}
	added, changed, err := s.pool.SaveDJItems(ctx, cs.ID, raw)
	if err != nil {
		if lerr := s.pool.CatalogMarkFetched(ctx, cs.ID, cs.ETag, cs.LastModified, "error"); lerr != nil {
			s.log.Printf("ingest: mark fetched failed: %v", lerr)
		}
		if lerr := s.pool.LogFetch(ctx, cs.ID, "error", 0, len(raw), 0, err.Error()); lerr != nil {
			s.log.Printf("ingest: log fetch failed: %v", lerr)
		}
		return FetchOutcome{Status: "error", Detail: err.Error()}
	}
	if lerr := s.pool.CatalogMarkFetched(ctx, cs.ID, cs.ETag, cs.LastModified, "ok"); lerr != nil {
		s.log.Printf("ingest: mark fetched failed: %v", lerr)
	}
	detail := ""
	if changed > 0 {
		detail = fmt.Sprintf("%d updated", changed)
	}
	if lerr := s.pool.LogFetch(ctx, cs.ID, "ok", 200, len(raw), added, detail); lerr != nil {
		s.log.Printf("ingest: log fetch failed: %v", lerr)
	}
	return FetchOutcome{Status: "ok", Items: len(raw), NewItems: added}
}

// backfillDetails spends the day's leftover requests fetching
// aggregate bodies for stored DJ items that only have the list blurb.
// The shared counter is the source of truth — the sweep stops before
// the reserve, and ErrCapped/429 end it silently (next poll resumes).
func (s *Scheduler) backfillDetails(ctx context.Context, cs *store.CatalogSource) {
	day := s.now().UTC().Format("2006-01-02")
	used, err := s.pool.CatalogAPICount(ctx, cs.ID, day)
	if err != nil {
		return
	}
	remaining := DailyAPIDailyCap - DailyDetailReserve - used
	if remaining <= 0 {
		return
	}
	slugs, err := s.pool.DJPendingDetails(ctx, cs.ID, remaining)
	if err != nil || len(slugs) == 0 {
		return
	}
	var filled, gone int
	for _, slug := range slugs {
		if ctx.Err() != nil {
			return
		}
		it, err := s.Daily.Detail(ctx, slug)
		if err != nil {
			var he *ErrHTTP
			if errors.As(err, &he) && he.Permanent() {
				// The detail endpoint refuses this slug for good —
				// a removed/renamed story 404s every time. Mark it
				// so it leaves the pending queue and keep filling the
				// rest: one dead slug at the head must never starve
				// every item behind it.
				s.logf("daily-journal detail %s: %v — marked gone", slug, err)
				if lerr := s.pool.DJMarkDetailFailed(ctx, cs.ID, slug); lerr != nil {
					s.log.Printf("ingest: mark detail failed: %v", lerr)
				}
				gone++
				continue
			}
			return // capped, limited, transport or 5xx — next poll resumes
		}
		if it.Slug != slug {
			// A record that isn't this slug's (error payload decoded
			// as 200) must not be stored — skip it, retried next pass.
			s.logf("daily-journal detail %s: endpoint returned %q", slug, it.Slug)
			continue
		}
		raw := s.Daily.ToCatalogItem(*it)
		if _, _, err := s.pool.SaveDJItems(ctx, cs.ID, []store.CatalogItem{raw}); err != nil {
			s.logf("daily-journal detail %s: %v", slug, err)
			return
		}
		filled++
	}
	if filled > 0 || gone > 0 {
		detail := "detail backfill"
		if gone > 0 {
			detail = fmt.Sprintf("detail backfill, %d gone", gone)
		}
		if lerr := s.pool.LogFetch(ctx, cs.ID, "ok", 200, filled, 0, detail); lerr != nil {
			s.log.Printf("ingest: log fetch failed: %v", lerr)
		}
	}
}

// purgeBodies enforces the 30-day retention window hourly.
func (s *Scheduler) purgeBodies(ctx context.Context) {
	if s.now().Sub(s.lastPurge) < time.Hour {
		return
	}
	s.lastPurge = s.now()
	if n, err := s.pool.PurgeOldBodies(ctx, s.now().Add(-30*24*time.Hour)); err == nil && n > 0 {
		s.logf("retention: cleared %d item bodies", n)
	}
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.log != nil {
		s.log.Printf("ingest: "+format, args...)
	}
}
