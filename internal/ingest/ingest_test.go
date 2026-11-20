package ingest

// Unit tests for global ingestion — all network access goes through
// httptest servers, nothing leaves the test process.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func fakeNow() string { return time.Now().UTC().Format("2006-01-02 15:04:05") }

// fakeStore implements the ingest Store in memory so the fetcher and
// scheduler tests need no Postgres.
type fakeStore struct {
	cat      []store.CatalogSource
	items    map[int64][]store.CatalogItem
	logs     []fakeLog
	purged   int64
	apiDay   map[int64]string
	apiCount map[int64]int
	apiTotal map[int64]int // every counted request, across days
}

type fakeLog struct {
	catalogID int64
	status    string
	http      int
	items     int
	detail    string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		items:    map[int64][]store.CatalogItem{},
		apiDay:   map[int64]string{},
		apiCount: map[int64]int{},
		apiTotal: map[int64]int{},
	}
}

func (f *fakeStore) Catalog(context.Context) ([]store.CatalogSource, error) { return f.cat, nil }

func (f *fakeStore) entry(id int64) *store.CatalogSource {
	for i := range f.cat {
		if f.cat[i].ID == id {
			return &f.cat[i]
		}
	}
	return nil
}

func (f *fakeStore) CatalogMarkFetched(_ context.Context, id int64, etag, lm, status string) error {
	e := f.entry(id)
	e.ETag, e.LastModified, e.LastStatus, e.LastFetchAt = etag, lm, status, fakeNow()
	return nil
}

func (f *fakeStore) CatalogSetSuspended(_ context.Context, id int64, suspended bool, reason string) error {
	e := f.entry(id)
	e.Suspended, e.SuspendReason = suspended, reason
	return nil
}

func (f *fakeStore) CatalogTouchRobots(_ context.Context, id int64) error {
	f.entry(id).RobotsAt = fakeNow()
	return nil
}

func (f *fakeStore) SaveCatalogItems(_ context.Context, id int64, raw []store.CatalogItem) (int, error) {
	seen := map[string]bool{}
	for _, it := range f.items[id] {
		seen[it.ExternalID] = true
	}
	added := 0
	for _, it := range raw {
		if !seen[it.ExternalID] {
			f.items[id] = append(f.items[id], it)
			added++
		}
	}
	return added, nil
}

func (f *fakeStore) CatalogAPICount(_ context.Context, id int64, day string) (int, error) {
	if f.apiDay[id] != day {
		return 0, nil
	}
	return f.apiCount[id], nil
}

func (f *fakeStore) CatalogAPIIncr(_ context.Context, id int64, day string) error {
	if f.apiDay[id] != day {
		f.apiDay[id] = day
		f.apiCount[id] = 0
	}
	f.apiCount[id]++
	f.apiTotal[id]++
	return nil
}

// SaveDJItems mirrors the Postgres upsert: insert new external_ids,
// update when any stored field differs, leave identical rows alone.
func (f *fakeStore) SaveDJItems(_ context.Context, id int64, raw []store.CatalogItem) (int, int, error) {
	idx := map[string]int{}
	for i, it := range f.items[id] {
		idx[it.ExternalID] = i
	}
	var added, changed int
	for _, it := range raw {
		i, ok := idx[it.ExternalID]
		if !ok {
			idx[it.ExternalID] = len(f.items[id])
			f.items[id] = append(f.items[id], it)
			added++
			continue
		}
		old := f.items[id][i]
		store.MergeDJDetail(old.Meta, old.ContentText, &it)
		om, _ := json.Marshal(old.Meta)
		nm, _ := json.Marshal(it.Meta)
		if old.Title != it.Title || old.PublishedAt != it.PublishedAt ||
			old.ContentText != it.ContentText || string(om) != string(nm) {
			f.items[id][i] = it
			changed++
		}
	}
	return added, changed, nil
}

// DJPendingDetails returns slugs whose meta lacks detail_fetched, in
// stored order — the fake keeps the store contract's shape, not its
// ordering (tests control the slice anyway).
func (f *fakeStore) DJPendingDetails(_ context.Context, id int64, limit int) ([]string, error) {
	var out []string
	for _, it := range f.items[id] {
		if len(out) >= limit {
			break
		}
		v, _ := it.Meta["detail_fetched"]
		if v == true || v == "true" || v == "gone" {
			continue
		}
		if slug, _ := it.Meta["slug"].(string); slug != "" {
			out = append(out, slug)
		}
	}
	return out, nil
}

// DJMarkDetailFailed mirrors the store's meta merge: the slug's row
// gets detail_fetched='gone' and leaves the pending queue.
func (f *fakeStore) DJMarkDetailFailed(_ context.Context, id int64, slug string) error {
	for i, it := range f.items[id] {
		if s, _ := it.Meta["slug"].(string); s == slug {
			f.items[id][i].Meta["detail_fetched"] = "gone"
		}
	}
	return nil
}

func (f *fakeStore) LogFetch(_ context.Context, id int64, status string, httpStatus, items, newItems int, detail string) error {
	f.logs = append(f.logs, fakeLog{id, status, httpStatus, items, detail})
	return nil
}

func (f *fakeStore) PurgeOldBodies(context.Context, time.Time) (int64, error) { return f.purged, nil }

const testFeed = `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Test Feed</title>
<item><title>Story one</title><link>http://example.com/a?utm_source=feed&amp;x=1</link>
<guid>g1</guid><pubDate>Thu, 13 Nov 2026 10:00:00 GMT</pubDate>
<content:encoded xmlns:content="http://purl.org/rss/1.0/modules/content/"><![CDATA[<p>Body <b>one</b></p><div class="oferta-box">ad</div>]]></content:encoded>
</item></channel></rss>`

func rssSource(feedURL string, policy string) *store.CatalogSource {
	return &store.CatalogSource{
		ID: 1, Slug: "test", Name: "Test", Kind: "rss", URL: "http://example.com",
		FeedURL: feedURL, Policy: policy, Domain: "example.com",
		Enabled: true, StripCSS: []string{"oferta"},
	}
}

// TestPolicyRefused: hold and blocked sources are never requested —
// the fetcher refuses before any network access.
func TestPolicyRefused(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
	}))
	defer srv.Close()

	for _, policy := range []string{"hold", "blocked"} {
		fs := newFakeStore()
		cs := rssSource(srv.URL+"/feed", policy)
		fs.cat = []store.CatalogSource{*cs}
		f := NewFeedFetcher(fs, srv.Client(), "KloxusBot/1.0 (+test)", "KloxusBot", nil)
		o := f.Fetch(context.Background(), cs)
		if o.Status != "error" {
			t.Fatalf("policy %s: status=%q want error", policy, o.Status)
		}
	}
	if got := atomic.LoadInt64(&hits); got != 0 {
		t.Fatalf("hold/blocked sources were requested: %d hits", got)
	}
}

// TestRobotsSuspends: a robots.txt that disallows the feed path
// suspends the source instead of fetching it.
func TestRobotsSuspends(t *testing.T) {
	var feedHits, robotsHits int64
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&robotsHits, 1)
		fmt.Fprintln(w, "User-agent: *\nDisallow: /feed")
	})
	mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&feedHits, 1)
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, testFeed)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	fs := newFakeStore()
	cs := rssSource(srv.URL+"/feed", "launch")
	fs.cat = []store.CatalogSource{*cs}
	f := NewFeedFetcher(fs, srv.Client(), "KloxusBot/1.0 (+test)", "KloxusBot", nil)
	o := f.Fetch(context.Background(), cs)
	if o.Status != "suspended" {
		t.Fatalf("status=%q want suspended (%s)", o.Status, o.Detail)
	}
	if !fs.entry(1).Suspended {
		t.Fatal("catalog row not marked suspended")
	}
	if atomic.LoadInt64(&feedHits) != 0 {
		t.Fatal("feed was fetched despite robots denial")
	}
	if atomic.LoadInt64(&robotsHits) != 1 {
		t.Fatalf("robots fetched %d times, want 1", robotsHits)
	}
}

// TestConditionalGet304: a stored ETag travels as If-None-Match; a 304
// is logged not_modified and no body is parsed.
func TestConditionalGet304(t *testing.T) {
	var fetches int64
	var lastINM string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404)
			return
		}
		atomic.AddInt64(&fetches, 1)
		lastINM = r.Header.Get("If-None-Match")
		if lastINM == `"v1"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, testFeed)
	}))
	defer srv.Close()

	fs := newFakeStore()
	cs := rssSource(srv.URL+"/feed", "launch")
	fs.cat = []store.CatalogSource{*cs}
	f := NewFeedFetcher(fs, srv.Client(), "KloxusBot/1.0 (+test)", "KloxusBot", nil)

	o1 := f.Fetch(context.Background(), cs)
	if o1.Status != "ok" || o1.Items != 1 || o1.NewItems != 1 {
		t.Fatalf("first fetch: %+v", o1)
	}
	if fs.entry(1).ETag != `"v1"` {
		t.Fatalf("etag not stored: %q", fs.entry(1).ETag)
	}
	// Robots is fresh now (touched above), so the second fetch goes
	// straight to the conditional GET.
	o2 := f.Fetch(context.Background(), fs.entry(1))
	if o2.Status != "not_modified" || o2.HTTPStatus != 304 {
		t.Fatalf("second fetch: %+v", o2)
	}
	if lastINM != `"v1"` {
		t.Fatalf("If-None-Match=%q want \"v1\"", lastINM)
	}
	if len(fs.items[1]) != 1 {
		t.Fatalf("304 added items: %d", len(fs.items[1]))
	}
}

// TestFeedNormalize: launch items store cleaned full text, tracking
// params are canonicalized away, strip classes remove ad boxes.
func TestFeedNormalize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, testFeed)
	}))
	defer srv.Close()

	fs := newFakeStore()
	cs := rssSource(srv.URL+"/feed", "launch")
	fs.cat = []store.CatalogSource{*cs}
	f := NewFeedFetcher(fs, srv.Client(), "KloxusBot/1.0 (+test)", "KloxusBot", nil)
	if o := f.Fetch(context.Background(), cs); o.Status != "ok" {
		t.Fatalf("fetch: %+v", o)
	}
	it := fs.items[1][0]
	if it.CanonicalURL != "http://example.com/a?x=1" {
		t.Fatalf("canonical url: %q", it.CanonicalURL)
	}
	if !strings.Contains(it.ContentText, "Body one") || strings.Contains(it.ContentText, "oferta") {
		t.Fatalf("body not normalized: %q", it.ContentText)
	}
	if it.Kind != "article" {
		t.Fatalf("kind=%q want article", it.Kind)
	}
}

// TestLinksBodyModeSignals: body_mode=links sources store signal rows —
// title + URL, no body, kind=signal (never evidence).
func TestLinksBodyModeSignals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, testFeed)
	}))
	defer srv.Close()

	fs := newFakeStore()
	cs := rssSource(srv.URL+"/feed", "signal")
	cs.BodyMode = "links"
	fs.cat = []store.CatalogSource{*cs}
	f := NewFeedFetcher(fs, srv.Client(), "KloxusBot/1.0 (+test)", "KloxusBot", nil)
	if o := f.Fetch(context.Background(), cs); o.Status != "ok" {
		t.Fatalf("fetch: %+v", o)
	}
	it := fs.items[1][0]
	if it.Kind != "signal" || it.ContentText != "" {
		t.Fatalf("signal row wrong: kind=%q text=%q", it.Kind, it.ContentText)
	}
}

// TestParseRobots covers the RFC 9309 subset the checker implements.
func TestParseRobots(t *testing.T) {
	rules := parseRobots(`User-agent: BadBot
Disallow: /

User-agent: KloxusBot
Disallow: /private
Allow: /private/public

User-agent: *
Disallow: /tmp`, "KloxusBot")
	if !rules.allowed("/feed") {
		t.Error("KloxusBot should be allowed at /feed")
	}
	if rules.allowed("/private/x") {
		t.Error("/private should be denied")
	}
	if !rules.allowed("/private/public/x") {
		t.Error("Allow should win on longer match")
	}

	star := parseRobots("User-agent: *\nDisallow: /feeds", "KloxusBot")
	if star.allowed("/feeds/rss") {
		t.Error("wildcard group should apply")
	}
}

// TestSchedulerFetchesEachSourceOnce: PollOnce walks the catalog once —
// the fetch count is bounded by the catalog, not by anything per-
// workspace (workspaces never reach this code at all).
func TestSchedulerFetchesEachSourceOnce(t *testing.T) {
	var feedHits int64
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&feedHits, 1)
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, testFeed)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	fs := newFakeStore()
	fs.cat = []store.CatalogSource{
		*rssSource(srv.URL+"/feed", "launch"),
		{ID: 2, Slug: "api-src", Name: "API", Kind: "api", URL: srv.URL, FeedURL: srv.URL + "/api",
			Policy: "signal", Domain: srv.URL, Enabled: true},
		{ID: 3, Slug: "held", Name: "Held", Kind: "rss", URL: srv.URL, FeedURL: srv.URL + "/feed",
			Policy: "hold", Domain: srv.URL, Enabled: true},
		{ID: 4, Slug: "blockd", Name: "Blocked", Kind: "rss", URL: srv.URL, FeedURL: srv.URL + "/feed",
			Policy: "blocked", Domain: srv.URL, Enabled: true},
	}
	s := NewScheduler(fs, "KloxusBot/1.0 (+test)", nil)
	s.PollOnce(context.Background())
	if atomic.LoadInt64(&feedHits) != 1 {
		t.Fatalf("feed fetched %d times, want 1", feedHits)
	}
	// Second poll: nothing is due yet (last_fetch_at was just set), and
	// non-RSS/hold/blocked sources are skipped every pass.
	s.PollOnce(context.Background())
	if atomic.LoadInt64(&feedHits) != 1 {
		t.Fatalf("not-due sources re-polled: feed=%d", feedHits)
	}
}

// TestLatin1FeedDecodes: a feed whose only encoding hint is the HTTP
// Content-Type charset (the XML prolog is silent) must land in stored
// items as UTF-8 — pre-fix it arrived as the mojibake the owner saw on
// ingested titles (KLOX-79).
func TestLatin1FeedDecodes(t *testing.T) {
	latin1 := []byte("<?xml version=\"1.0\"?><rss version=\"2.0\"><channel>" +
		"<title>Feed</title><item><title>Servi\xe7o p\xfablico digital</title>" +
		"<link>http://example.com/l1</link><guid>l1</guid></item></channel></rss>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=iso-8859-1")
		w.Write(latin1)
	}))
	defer srv.Close()

	fs := newFakeStore()
	cs := rssSource(srv.URL+"/feed", "launch")
	fs.cat = []store.CatalogSource{*cs}
	f := NewFeedFetcher(fs, srv.Client(), "KloxusBot/1.0 (+test)", "KloxusBot", nil)
	if o := f.Fetch(context.Background(), cs); o.Status != "ok" {
		t.Fatalf("fetch: %+v", o)
	}
	if got := fs.items[1][0].Title; got != "Serviço público digital" {
		t.Fatalf("title=%q — latin-1 feed not decoded", got)
	}
}
