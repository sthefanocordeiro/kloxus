package ingest

// Daily Journal signal ingestion (D30/D31). Public JSON API only — no
// HTML pages are ever fetched, and the API's `body` field is never
// stored or shown as evidence. Requests are hard-capped at 350 per UTC
// day, counted on the catalog row so the limit holds across restarts
// and across workspace runs (D29: ingestion is global). HTTP 429
// honors Retry-After; 5xx retries at most twice.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// DailyAPIDailyCap is the request hard limit per UTC day.
const DailyAPIDailyCap = 350

// APICounter is the persistence side of the request cap — the catalog
// row's api_day/api_count counters.
type APICounter interface {
	APICount(ctx context.Context, day string) (int, error)
	APIIncr(ctx context.Context, day string) error
}

// storeCounter adapts the ingest Store for a specific catalog row.
type storeCounter struct {
	s  Store
	id int64
}

func (c storeCounter) APICount(ctx context.Context, day string) (int, error) {
	return c.s.CatalogAPICount(ctx, c.id, day)
}
func (c storeCounter) APIIncr(ctx context.Context, day string) error {
	return c.s.CatalogAPIIncr(ctx, c.id, day)
}

// Counter exposes a catalog row's request counter so the API cap
// survives restarts and is shared by every caller.
func Counter(s Store, catalogID int64) APICounter {
	return storeCounter{s: s, id: catalogID}
}

// ErrRateLimited reports an HTTP 429 honoring Retry-After.
type ErrRateLimited struct{ RetryAfter time.Duration }

func (e *ErrRateLimited) Error() string { return "HTTP 429 (rate limited)" }

// ErrCapped reports that the daily request budget is spent; the
// request was never made.
var ErrCapped = fmt.Errorf("daily request cap reached (%d/day)", DailyAPIDailyCap)

// DJItem is one story from the list/detail payloads.
type DJItem struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	URL         string   `json:"url"`
	APIURL      string   `json:"api_url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	PublishedAt string   `json:"published_at"`
	Categories  []string `json:"categories"`
	Topics      []struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	} `json:"topics"`
	SourceCount int      `json:"source_count"`
	OutletCount int      `json:"outlet_count"`
	Sources     []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"sources"`
	// Body intentionally has no struct field: it is never decoded,
	// stored, or shown (Daily Journal terms, D30).
}

type djListResponse struct {
	Items      []DJItem `json:"items"`
	NextCursor string   `json:"next_cursor"`
}

// DailyJournal is the public-API client.
type DailyJournal struct {
	http    *http.Client
	base    string // e.g. https://dailyjournal.news
	ua      string
	counter APICounter
	now     func() time.Time
}

// NewDailyJournal builds the client over the given request counter —
// PoolAPICounter for the catalog row in production, a fake in tests.
func NewDailyJournal(counter APICounter, hc *http.Client, ua, base string) *DailyJournal {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &DailyJournal{
		http: hc, base: base, ua: ua,
		counter: counter, now: time.Now,
	}
}

// request performs one counted GET; retries 5xx at most twice and
// surfaces 429 as ErrRateLimited. The cap is checked before every
// attempt so retries can't spend past it.
func (d *DailyJournal) request(ctx context.Context, rawURL string, dst any) error {
	day := d.now().UTC().Format("2006-01-02")
	for attempt := 0; attempt < 3; attempt++ {
		n, err := d.counter.APICount(ctx, day)
		if err != nil {
			return err
		}
		if n >= DailyAPIDailyCap {
			return ErrCapped
		}
		if err := d.counter.APIIncr(ctx, day); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", d.ua)
		req.Header.Set("Accept", "application/json")
		res, err := d.http.Do(req)
		if err != nil {
			return err // transport errors are not retried inside request
		}
		if res.StatusCode == http.StatusTooManyRequests {
			ra := res.Header.Get("Retry-After")
			res.Body.Close()
			wait := time.Minute
			if s, err := strconv.Atoi(ra); err == nil {
				wait = time.Duration(s) * time.Second
			}
			return &ErrRateLimited{RetryAfter: wait}
		}
		if res.StatusCode >= 500 {
			res.Body.Close()
			if attempt < 2 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
					continue
				}
			}
			return fmt.Errorf("HTTP %d", res.StatusCode)
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return fmt.Errorf("HTTP %d", res.StatusCode)
		}
		err = json.NewDecoder(res.Body).Decode(dst)
		res.Body.Close()
		return err
	}
	return fmt.Errorf("unreachable")
}

// Poll lists new stories since the stored cursor (bounded pages).
// from optionally restricts to stories published at/after it; the
// caller persists next_cursor only on success.
func (d *DailyJournal) Poll(ctx context.Context, cursor string, from time.Time, maxPages int) ([]DJItem, string, error) {
	var all []DJItem
	next := cursor
	for page := 0; page < maxPages; page++ {
		q := url.Values{"limit": {"50"}}
		if next != "" {
			q.Set("cursor", next)
		} else if !from.IsZero() {
			q.Set("date_from", from.UTC().Format("2006-01-02"))
		}
		var res djListResponse
		if err := d.request(ctx, d.base+"/api/public/news?"+q.Encode(), &res); err != nil {
			return all, cursor, err
		}
		all = append(all, res.Items...)
		if res.NextCursor == "" || len(res.Items) == 0 {
			return all, next, nil
		}
		next = res.NextCursor
	}
	return all, next, nil
}

// Detail fetches one story's full record on demand — the sources[]
// links a story cites. The body field exists in the payload but is
// never mapped.
func (d *DailyJournal) Detail(ctx context.Context, slug string) (*DJItem, error) {
	var it DJItem
	err := d.request(ctx, d.base+"/api/public/news/"+url.PathEscape(slug), &it)
	if err != nil {
		return nil, err
	}
	return &it, nil
}

// ToCatalogItem maps an API record to a shared signal row. Description
// is stored as the display summary — it is a one-line blurb, not the
// story body, and signals never serve as evidence anyway.
func (d *DailyJournal) ToCatalogItem(it DJItem) store.CatalogItem {
	cited := make([]string, 0, len(it.Sources))
	for _, s := range it.Sources {
		if s.URL != "" {
			cited = append(cited, CanonicalURL(s.URL))
		}
	}
	return store.CatalogItem{
		ExternalID:   it.ID,
		URL:          it.URL,
		CanonicalURL: it.URL,
		Title:        it.Title,
		PublishedAt:  it.PublishedAt,
		ContentText:  it.Description,
		Kind:         "signal",
		Meta: map[string]any{
			"description":  it.Description,
			"categories":   it.Categories,
			"topics":       it.Topics,
			"outlet_count": it.OutletCount,
			"source_count": it.SourceCount,
			"dj_url":       it.URL,
			"slug":         it.Slug,
			"cited_urls":   cited,
		},
	}
}
