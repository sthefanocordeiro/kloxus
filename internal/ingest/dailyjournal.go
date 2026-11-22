package ingest

// Daily Journal ingestion (KLOX-93 client; dormant since KLOX-114 —
// DJ left the catalog and the flag defaults off). Public JSON API
// only — no HTML pages are ever fetched. The list endpoint carries
// title + description + categories; the detail endpoint carries the
// aggregate body, bullets and cited source articles, which we store as
// the item's content_text under the source's 'full' body mode. DJ's
// own text is always cited as "Daily Journal" with its link — the
// citation their terms and llms.txt ask for; the stored body is never
// presented as another outlet's reporting.
//
// Requests are hard-capped at 350 per UTC day, counted on the catalog
// row so the limit holds across restarts and across workspace runs
// (D29: ingestion is global). HTTP 429 honors Retry-After; 5xx retries
// at most twice.
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

// DailyDetailReserve is the daily request share kept for list polls:
// detail backfills stop once the day's count reaches cap-reserve, so
// the next day's list pages and retries always fit the budget.
const DailyDetailReserve = 50

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

// ErrHTTP is a non-OK response other than 429 — the status tells the
// caller whether the same request could ever succeed.
type ErrHTTP struct{ Status int }

func (e *ErrHTTP) Error() string { return fmt.Sprintf("HTTP %d", e.Status) }

// Permanent reports whether retrying the identical request is futile:
// 4xx means the story is gone or forbidden for good. 5xx is transient
// — the next poll may find the endpoint healthy again.
func (e *ErrHTTP) Permanent() bool { return e.Status >= 400 && e.Status < 500 }

// ErrCapped reports that the daily request budget is spent; the
// request was never made.
var ErrCapped = fmt.Errorf("daily request cap reached (%d/day)", DailyAPIDailyCap)

// DJItem is one story from the list/detail payloads. List responses
// carry everything except Body/Bullets/Sources, which only the detail
// endpoint fills.
type DJItem struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	URL         string   `json:"url"`
	APIURL      string   `json:"api_url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	PublishedAt string   `json:"published_at"`
	UpdatedAt   string   `json:"updated_at"`
	Categories  []string `json:"categories"`
	Topics      []struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	} `json:"topics"`
	SourceCount int      `json:"source_count"`
	OutletCount int      `json:"outlet_count"`
	Body        string   `json:"body"`
	Bullets     []string `json:"bullets"`
	Sources     []struct {
		Title  string `json:"title"`
		URL    string `json:"url"`
		Outlet string `json:"outlet"`
	} `json:"sources"`
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
			return &ErrHTTP{Status: res.StatusCode}
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return &ErrHTTP{Status: res.StatusCode}
		}
		err = json.NewDecoder(res.Body).Decode(dst)
		res.Body.Close()
		return err
	}
	return fmt.Errorf("unreachable")
}

// Poll scans the newest stories — at most maxPages list pages, always
// starting at the first page. DJ's own polling guidance (poll-news.mjs)
// scans the recently-changed window on every run rather than holding a
// durable cursor: results order by latest source activity, so a story
// that gained coverage returns near the top again. Change detection
// happens on the caller side by comparing id + updated_at.
func (d *DailyJournal) Poll(ctx context.Context, maxPages int) ([]DJItem, error) {
	var all []DJItem
	var next string
	for page := 0; page < maxPages; page++ {
		q := url.Values{"limit": {"50"}}
		if next != "" {
			q.Set("cursor", next)
		}
		var res djListResponse
		if err := d.request(ctx, d.base+"/api/public/news?"+q.Encode(), &res); err != nil {
			return all, err
		}
		all = append(all, res.Items...)
		if res.NextCursor == "" || len(res.Items) == 0 {
			return all, nil
		}
		next = res.NextCursor
	}
	return all, nil
}

// Detail fetches one story's full record — the aggregate body, the
// bullets and the cited source articles.
func (d *DailyJournal) Detail(ctx context.Context, slug string) (*DJItem, error) {
	var it DJItem
	err := d.request(ctx, d.base+"/api/public/news/"+url.PathEscape(slug), &it)
	if err != nil {
		return nil, err
	}
	return &it, nil
}

// ToCatalogItem maps an API record to a shared catalog row. DJ items
// are kind='article' now: the aggregate body is DJ's own editorial
// text, cited as Daily Journal — the store-all posture of KLOX-93
// (D31's links-only signal treatment is gone).
func (d *DailyJournal) ToCatalogItem(it DJItem) store.CatalogItem {
	cited := make([]string, 0, len(it.Sources))
	outlets := make([]string, 0, len(it.Sources))
	for _, s := range it.Sources {
		if s.URL != "" {
			cited = append(cited, CanonicalURL(s.URL))
		}
		if s.Outlet != "" {
			outlets = append(outlets, s.Outlet)
		}
	}
	body := it.Body
	detail := body != ""
	if !detail {
		body = it.Description
	}
	topics := make([]string, 0, len(it.Topics))
	for _, t := range it.Topics {
		if t.Slug != "" {
			topics = append(topics, t.Slug)
		}
	}
	return store.CatalogItem{
		ExternalID:   it.ID,
		URL:          it.URL,
		CanonicalURL: CanonicalURL(it.URL),
		Title:        it.Title,
		PublishedAt:  it.PublishedAt,
		ContentText:  body,
		Kind:         "article",
		Meta: map[string]any{
			"description":    it.Description,
			"categories":     it.Categories,
			"topics":         topics,
			"outlet_count":   it.OutletCount,
			"source_count":   it.SourceCount,
			"dj_url":         it.URL,
			"slug":           it.Slug,
			"updated_at":     it.UpdatedAt,
			"detail_fetched": detail,
			"bullets":        it.Bullets,
			"cited_urls":     cited,
			"cited_outlets":  outlets,
		},
	}
}
