package ingest

// The global ingestion scheduler (D29). One process-wide loop polls
// each catalog source at its own interval — feeds are fetched once for
// all workspaces, never per tenant, so N workspaces still mean one
// request per interval. Polls run serially; per-host crawl delays are
// enforced by the throttler regardless of which source is next.
import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
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
	// AfterPoll, when set, runs once after each PollOnce — the shared
	// AI enrichment sweep (KLOX-67) hooks in here so new items get
	// classified without blocking feed polls.
	AfterPoll func(ctx context.Context)
}

// NewScheduler wires the feed fetcher + retention on the pool.
func NewScheduler(p Store, ua string, logger *log.Logger) *Scheduler {
	throttle := &hostThrottle{last: map[string]time.Time{}}
	hc := &http.Client{Timeout: 30 * time.Second}
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
		if cs.Kind != "rss" {
			continue // no API-backed sources remain after Daily Journal left (D41)
		}
		if !cs.Due(s.now()) {
			continue
		}
		o := s.feed.Fetch(ctx, cs)
		if o.Status == "error" {
			s.logf("%s: %s", cs.Slug, o.Detail)
		}
	}
	s.purgeBodies(ctx)
	if s.AfterPoll != nil {
		s.AfterPoll(ctx)
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
