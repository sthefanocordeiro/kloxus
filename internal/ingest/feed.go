package ingest

// FeedFetcher executes one catalog source's poll: robots check, host
// crawl-delay, conditional GET, gofeed parse, normalization, and the
// shared-items upsert. All requests identify as KloxusBot with a
// contact URL (D27). hold/blocked sources are refused before any
// network access — see CatalogSource.Fetchable.
import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// FetchOutcome summarizes one poll for the fetch log.
type FetchOutcome struct {
	Status     string // ok | not_modified | error | suspended
	HTTPStatus int
	Items      int
	NewItems   int
	Detail     string
}

type hostThrottler interface {
	wait(ctx context.Context, host string, delay time.Duration) error
	mark(host string)
}

// Store is the persistence the ingest layer needs — *store.Pool in
// production, an in-memory fake in tests (no Postgres required).
type Store interface {
	Catalog(ctx context.Context) ([]store.CatalogSource, error)
	CatalogMarkFetched(ctx context.Context, id int64, etag, lastModified, status string) error
	CatalogSetSuspended(ctx context.Context, id int64, suspended bool, reason string) error
	CatalogTouchRobots(ctx context.Context, id int64) error
	CatalogSetCursor(ctx context.Context, id int64, cursor string) error
	CatalogAPICount(ctx context.Context, id int64, day string) (int, error)
	CatalogAPIIncr(ctx context.Context, id int64, day string) error
	SaveCatalogItems(ctx context.Context, catalogID int64, raw []store.CatalogItem) (int, error)
	UpdateCatalogItemMeta(ctx context.Context, catalogID int64, externalID string, meta map[string]any) error
	LogFetch(ctx context.Context, catalogID int64, status string, httpStatus, items, newItems int, detail string) error
	PurgeOldBodies(ctx context.Context, olderThan time.Time) (int64, error)
	IsBlockedDomain(ctx context.Context, domain string) (bool, error)
}

// FeedFetcher fetches RSS catalog sources.
type FeedFetcher struct {
	http      *http.Client
	userAgent string
	agentName string // token matched against robots User-agent groups
	store     Store
	throttle  hostThrottler
	robotsTTL time.Duration
	now       func() time.Time
}

// NewFeedFetcher builds a fetcher over the shared pool. throttle
// serializes per-host request spacing for the caller's scheduler; nil
// disables it (tests).
func NewFeedFetcher(p Store, hc *http.Client, userAgent, agentName string, throttle hostThrottler) *FeedFetcher {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &FeedFetcher{
		http: hc, userAgent: userAgent, agentName: agentName,
		store: p, throttle: throttle,
		robotsTTL: 24 * time.Hour,
		now:       time.Now,
	}
}

// Fetch polls one RSS source once and records the outcome: fetch_log
// row, etag/last-modified, suspension state. Policy is checked first —
// hold and blocked sources return an error outcome without touching
// the network.
func (f *FeedFetcher) Fetch(ctx context.Context, cs *store.CatalogSource) FetchOutcome {
	if cs.Policy != "launch" && cs.Policy != "signal" {
		return FetchOutcome{Status: "error", Detail: "policy " + cs.Policy + " is never fetched"}
	}
	if !cs.Enabled || cs.Suspended {
		return FetchOutcome{Status: "suspended", Detail: cs.SuspendReason}
	}
	if cs.FeedURL == "" {
		return FetchOutcome{Status: "error", Detail: "no feed url"}
	}

	// Daily robots re-check; a source whose rules turned hostile is
	// suspended rather than fetched.
	if stale(cs.RobotsAt, f.robotsTTL, f.now()) {
		rules, err := f.checkRobots(ctx, cs.FeedURL)
		if err != nil {
			if rules.fetched {
				f.suspend(ctx, cs, err.Error())
				return FetchOutcome{Status: "suspended", Detail: err.Error()}
			}
			return f.fail(ctx, cs, fmt.Errorf("robots check: %w", err))
		}
		_ = f.store.CatalogTouchRobots(ctx, cs.ID)
		if rules.crawlDelay > 0 {
			if err := f.wait(ctx, cs, rules.crawlDelay); err != nil {
				return f.fail(ctx, cs, err)
			}
		}
	} else if err := f.wait(ctx, cs, time.Duration(cs.CrawlDelayMS)*time.Millisecond); err != nil {
		return f.fail(ctx, cs, err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", cs.FeedURL, nil)
	if err != nil {
		return f.fail(ctx, cs, err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*;q=0.1")
	if cs.ETag != "" {
		req.Header.Set("If-None-Match", cs.ETag)
	}
	if cs.LastModified != "" {
		req.Header.Set("If-Modified-Since", cs.LastModified)
	}
	res, err := f.http.Do(req)
	if err != nil {
		return f.fail(ctx, cs, err)
	}
	defer res.Body.Close()

	switch {
	case res.StatusCode == http.StatusNotModified:
		return f.finish(ctx, cs, FetchOutcome{Status: "not_modified", HTTPStatus: 304, Detail: "not modified"}, res)
	case res.StatusCode == http.StatusTooManyRequests:
		return f.fail(ctx, cs, fmt.Errorf("HTTP 429 (retry-after %q)", res.Header.Get("Retry-After")))
	case res.StatusCode != http.StatusOK:
		return f.fail(ctx, cs, fmt.Errorf("HTTP %d", res.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024))
	if err != nil {
		return f.fail(ctx, cs, err)
	}
	fp := gofeed.NewParser()
	feed, err := fp.ParseString(string(body))
	if err != nil {
		return f.fail(ctx, cs, fmt.Errorf("parse feed: %w", err))
	}
	items := normalizeFeed(feed, cs)
	added, err := f.store.SaveCatalogItems(ctx, cs.ID, items)
	if err != nil {
		return f.fail(ctx, cs, err)
	}
	return f.finish(ctx, cs, FetchOutcome{
		Status: "ok", HTTPStatus: res.StatusCode,
		Items: len(items), NewItems: added,
	}, res)
}

// wait applies the per-host crawl delay through the scheduler's
// throttler; without one (tests) delays under a second are skipped.
func (f *FeedFetcher) wait(ctx context.Context, cs *store.CatalogSource, d time.Duration) error {
	if f.throttle == nil || d <= 0 {
		return nil
	}
	u, err := url.Parse(cs.FeedURL)
	if err != nil {
		return err
	}
	if err := f.throttle.wait(ctx, u.Host, d); err != nil {
		return err
	}
	f.throttle.mark(u.Host)
	return nil
}

func stale(checkedAt string, ttl time.Duration, now time.Time) bool {
	if checkedAt == "" {
		return true
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", checkedAt, time.UTC)
	return err != nil || t.Add(ttl).Before(now)
}

func (f *FeedFetcher) suspend(ctx context.Context, cs *store.CatalogSource, reason string) {
	_ = f.store.CatalogSetSuspended(ctx, cs.ID, true, reason)
	_ = f.store.LogFetch(ctx, cs.ID, "suspended", 0, 0, 0, reason)
}

func (f *FeedFetcher) fail(ctx context.Context, cs *store.CatalogSource, err error) FetchOutcome {
	_ = f.store.CatalogMarkFetched(ctx, cs.ID, cs.ETag, cs.LastModified, "error")
	_ = f.store.LogFetch(ctx, cs.ID, "error", 0, 0, 0, err.Error())
	return FetchOutcome{Status: "error", Detail: err.Error()}
}

func (f *FeedFetcher) finish(ctx context.Context, cs *store.CatalogSource, o FetchOutcome, res *http.Response) FetchOutcome {
	etag := res.Header.Get("ETag")
	lm := res.Header.Get("Last-Modified")
	if etag == "" {
		etag = cs.ETag
	}
	if lm == "" {
		lm = cs.LastModified
	}
	_ = f.store.CatalogMarkFetched(ctx, cs.ID, etag, lm, o.Status)
	_ = f.store.LogFetch(ctx, cs.ID, o.Status, o.HTTPStatus, o.Items, o.NewItems, o.Detail)
	return o
}

// normalizeFeed converts gofeed entries to shared catalog items,
// honoring the source's body_mode:
//
//	full   — cleaned article HTML (the citation body)
//	quotes — feed summary only (licensing-conservative sources)
//	links  — signal rows: title + URL, no stored body, never evidence
func normalizeFeed(feed *gofeed.Feed, cs *store.CatalogSource) []store.CatalogItem {
	out := make([]store.CatalogItem, 0, len(feed.Items))
	for _, e := range feed.Items {
		link := e.Link
		if link == "" {
			continue
		}
		ext := e.GUID
		if ext == "" {
			ext = link
		}
		author := ""
		if len(e.Authors) > 0 && e.Authors[0] != nil {
			author = e.Authors[0].Name
		}
		if author == "" && e.Author != nil {
			author = e.Author.Name
		}
		if !cs.CreditAuthor {
			author = "" // outlet-level attribution only
		}
		pub := ""
		if e.PublishedParsed != nil {
			pub = e.PublishedParsed.UTC().Format(time.RFC3339)
		} else if e.UpdatedParsed != nil {
			pub = e.UpdatedParsed.UTC().Format(time.RFC3339)
		}
		item := store.CatalogItem{
			ExternalID:   ext,
			URL:          link,
			CanonicalURL: CanonicalURL(link),
			Title:        strings.TrimSpace(e.Title),
			Author:       author,
			PublishedAt:  pub,
			Kind:         "article",
			Meta:         map[string]any{},
		}
		switch cs.BodyMode {
		case "links":
			item.Kind = "signal"
		case "quotes":
			summary := e.Description
			if summary == "" {
				summary = e.Content
			}
			text, trunc := CleanHTML(summary, cs.StripCSS)
			if trunc {
				// Summaries stay summaries: cap hard rather than mark.
				r := []rune(text)
				if len(r) > 2000 {
					text = string(r[:2000])
				}
			}
			item.ContentText = text
		default:
			raw := e.Content
			if raw == "" {
				raw = e.Description
			}
			item.ContentText, item.BodyTruncated = CleanHTML(raw, cs.StripCSS)
		}
		out = append(out, item)
	}
	return out
}
