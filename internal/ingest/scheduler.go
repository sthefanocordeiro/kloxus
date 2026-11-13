package ingest

// The global ingestion scheduler (D29). One process-wide loop polls
// each catalog source at its own interval — feeds are fetched once for
// all workspaces, never per tenant, so N workspaces still mean one
// request per interval. Polls run serially; per-host crawl delays are
// enforced by the throttler regardless of which source is next.
import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// DefaultUserAgent identifies us to publishers (D27): name + contact.
const DefaultUserAgent = "KloxusBot/1.0 (+https://kloxus.fly.dev/about)"

// Scheduler polls the whole catalog on its own cadence.
type Scheduler struct {
	pool      Store
	feed      *FeedFetcher
	http      *http.Client
	ua        string
	tick      time.Duration // wake interval between due checks
	djEnabled bool
	log       *log.Logger
	now       func() time.Time
	lastPurge time.Time
}

// NewScheduler wires fetcher + DJ client + retention on the pool.
// djEnabled gates Daily Journal polling — the flag flips without a
// deploy and defaults off until the operator's written permission is
// confirmed (D31).
func NewScheduler(p Store, ua string, djEnabled bool, logger *log.Logger) *Scheduler {
	throttle := &hostThrottle{last: map[string]time.Time{}}
	hc := &http.Client{Timeout: 30 * time.Second}
	return &Scheduler{
		pool: p,
		feed: NewFeedFetcher(p, hc, ua, "KloxusBot", throttle),
		http: hc, ua: ua,
		tick:      time.Minute,
		djEnabled: djEnabled,
		log:       logger,
		now:       time.Now,
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
		if cs.Kind == "api" {
			if s.djEnabled && cs.Due(s.now()) {
				s.pollDailyJournal(ctx, cs)
			}
			continue
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
}

// pollDailyJournal runs the signal source's cursor poll: new stories
// become shared signal items; the cursor only advances on success.
func (s *Scheduler) pollDailyJournal(ctx context.Context, cs *store.CatalogSource) {
	base := cs.FeedURL
	if u, err := url.Parse(cs.FeedURL); err == nil {
		base = (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
	}
	dj := NewDailyJournal(Counter(s.pool, cs.ID), s.http, s.ua, base)
	items, cursor, err := dj.Poll(ctx, cs.Cursor, time.Time{}, 4)
	if err != nil {
		var rl *ErrRateLimited
		detail := err.Error()
		if errors.As(err, &rl) {
			detail = "429 rate limited, retry after " + rl.RetryAfter.String()
		}
		_ = s.pool.CatalogMarkFetched(ctx, cs.ID, "", "", "error")
		_ = s.pool.LogFetch(ctx, cs.ID, "error", 0, 0, 0, detail)
		s.logf("%s: %s", cs.Slug, detail)
		return
	}
	raw := make([]store.CatalogItem, 0, len(items))
	for _, it := range items {
		raw = append(raw, dj.ToCatalogItem(it))
	}
	added, err := s.pool.SaveCatalogItems(ctx, cs.ID, raw)
	if err != nil {
		s.logf("%s: save: %v", cs.Slug, err)
		return
	}
	_ = s.pool.CatalogSetCursor(ctx, cs.ID, cursor)
	_ = s.pool.CatalogMarkFetched(ctx, cs.ID, "", "", "ok")
	_ = s.pool.LogFetch(ctx, cs.ID, "ok", 200, len(items), added, "cursor="+cursor)

	// Detail calls are on-demand and budgeted by the daily cap: only
	// multi-outlet stories fetch theirs — those sources[] links are
	// what merge the signal onto the article it points at. Links
	// whose domain the catalog blocks are dropped, never fetched.
	blocked := func(d string) bool {
		b, _ := s.pool.IsBlockedDomain(ctx, d)
		return b
	}
	fetched := 0
	for _, it := range items {
		if it.OutletCount < 2 || fetched >= 10 || ctx.Err() != nil {
			continue
		}
		d, err := dj.Detail(ctx, it.Slug)
		if err != nil {
			break // cap/429: the rest waits for the next poll
		}
		fetched++
		cited := make([]string, 0, len(d.Sources))
		for _, src := range d.Sources {
			cited = append(cited, CanonicalURL(src.URL))
		}
		cited = CitedURLsAllowed(cited, blocked)
		_ = s.pool.UpdateCatalogItemMeta(ctx, cs.ID, it.ID, map[string]any{
			"cited_urls":   cited,
			"outlet_count": d.OutletCount,
			"source_count": d.SourceCount,
		})
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

// CitedURLsAllowed filters a signal's cited links down to domains the
// catalog does not block — a signal may cite a blocked outlet (e.g.
// Canaltech); we keep the link for display but never fetch it.
func CitedURLsAllowed(cited []string, blocked func(domain string) bool) []string {
	out := cited[:0:0]
	for _, u := range cited {
		host := ""
		if pu, err := url.Parse(u); err == nil {
			host = pu.Hostname()
		}
		if host == "" || !blocked(host) {
			out = append(out, u)
		}
	}
	return out
}
