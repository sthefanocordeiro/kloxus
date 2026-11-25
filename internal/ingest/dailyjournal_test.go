package ingest

// Daily Journal ingestion tests (KLOX-93) — every request goes through
// an httptest server; the live API is never touched.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const djListJSON = `{"items":[
 {"id":"dj-1","slug":"pix-recorde","url":"https://dailyjournal.news/n/pix-recorde",
  "title":"Pix bate recorde de transações","description":"O BC divulgou os números.",
  "published_at":"2026-11-20T10:00:00Z","updated_at":"2026-11-20T10:00:00Z",
  "categories":["finance","brazil"],
  "topics":[{"slug":"fintech","title":"Fintech"}],
  "source_count":4,"outlet_count":3},
 {"id":"dj-2","slug":"anatel-5g","url":"https://dailyjournal.news/n/anatel-5g",
  "title":"Anatel publica regulamentação do 5G","description":"Nova regra em vigor.",
  "published_at":"2026-11-20T09:00:00Z","updated_at":"2026-11-20T09:00:00Z",
  "categories":["technology","politics"],"topics":[],
  "source_count":2,"outlet_count":2}
],"next_cursor":""}`

const djDetailJSON = `{"id":"dj-1","slug":"pix-recorde",
 "url":"https://dailyjournal.news/n/pix-recorde",
 "title":"Pix bate recorde de transações",
 "description":"O BC divulgou os números.",
 "published_at":"2026-11-20T10:00:00Z","updated_at":"2026-11-20T11:00:00Z",
 "categories":["finance","brazil"],"topics":[{"slug":"fintech","title":"Fintech"}],
 "source_count":4,"outlet_count":3,
 "body":"O Banco Central divulgou que o Pix bateu novo recorde diário de transações na quinta-feira, com forte uso em comércio eletrônico.",
 "bullets":["Recorde diário","Uso em e-commerce puxa"],
 "sources":[{"title":"Pix recorde","url":"https://uol.com.br/a?utm_source=x","outlet":"UOL"},
            {"title":"BC dados","url":"https://g1.globo.com/b","outlet":"g1"}]}`

// djServer serves the list endpoint plus detail stubs for the slugs in
// detailBodies (empty map → all details 404).
func djServer(t *testing.T, list string, detailBodies map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/public/news" {
			fmt.Fprint(w, list)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/public/news/") {
			slug := strings.TrimPrefix(r.URL.Path, "/api/public/news/")
			if body, ok := detailBodies[slug]; ok {
				fmt.Fprint(w, body)
				return
			}
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(404)
	}))
}

func djCatalogSource(url string) *store.CatalogSource {
	return &store.CatalogSource{
		ID: 9, Slug: "daily-journal", Name: "Daily Journal", Kind: "api",
		URL: url, FeedURL: url + "/api/public/news",
		Policy: "signal", Domain: "dailyjournal.news",
		Enabled: true, BodyMode: "full",
	}
}

// TestDJPollPaginates: Poll follows next_cursor until it empties and
// each page is one counted request.
func TestDJPollPaginates(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			fmt.Fprint(w, `{"items":[{"id":"a","slug":"a","url":"u","title":"t"}],"next_cursor":"p2"}`)
			return
		}
		fmt.Fprint(w, `{"items":[{"id":"b","slug":"b","url":"u","title":"t2"}],"next_cursor":""}`)
	}))
	defer srv.Close()

	d := NewDailyJournal(&testCounter{}, srv.Client(), "ua", srv.URL)
	items, err := d.Poll(context.Background(), 5)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(items) != 2 || items[0].ID != "a" || items[1].ID != "b" {
		t.Fatalf("items=%v", items)
	}
	if atomic.LoadInt64(&hits) != 2 {
		t.Fatalf("hits=%d want 2", hits)
	}
}

// testCounter is an in-memory APICounter for the cap/retry tests.
type testCounter struct{ n int }

func (c *testCounter) APICount(context.Context, string) (int, error) { return c.n, nil }
func (c *testCounter) APIIncr(context.Context, string) error         { c.n++; return nil }

func TestDJDailyCap(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
	}))
	defer srv.Close()
	c := &testCounter{n: DailyAPIDailyCap}
	d := NewDailyJournal(c, srv.Client(), "ua", srv.URL)
	if _, err := d.Poll(context.Background(), 3); !errors.Is(err, ErrCapped) {
		t.Fatalf("err=%v want ErrCapped", err)
	}
	if atomic.LoadInt64(&hits) != 0 {
		t.Fatalf("request made past cap: %d", hits)
	}
}

func TestDJRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	d := NewDailyJournal(&testCounter{}, srv.Client(), "ua", srv.URL)
	_, err := d.Poll(context.Background(), 3)
	rl, ok := err.(*ErrRateLimited)
	if !ok {
		t.Fatalf("err=%v want *ErrRateLimited", err)
	}
	if rl.RetryAfter != 120*time.Second {
		t.Fatalf("retry-after=%v want 120s", rl.RetryAfter)
	}
}

// TestDJ5xxRetriesTwice: a flaky 5xx endpoint gets exactly three
// attempts (initial + 2 retries), each counted.
func TestDJ5xxRetriesTwice(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	d := NewDailyJournal(&testCounter{}, srv.Client(), "ua", srv.URL)
	_, err := d.Detail(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err=%v want HTTP 500", err)
	}
	if got := atomic.LoadInt64(&hits); got != 3 {
		t.Fatalf("hits=%d want 3 (2 retries)", got)
	}
}

// TestDJ5xxRecovers: a 500-then-200 endpoint succeeds on the retry.
func TestDJ5xxRecovers(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&hits, 1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, djDetailJSON)
	}))
	defer srv.Close()
	d := NewDailyJournal(&testCounter{}, srv.Client(), "ua", srv.URL)
	it, err := d.Detail(context.Background(), "pix-recorde")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if !strings.Contains(it.Body, "recorde") {
		t.Fatalf("body=%q", it.Body)
	}
}

// TestDJToCatalogItem: the aggregate body lands in content_text with
// detail_fetched; cited source links canonicalize; DJ meta rides along.
func TestDJToCatalogItem(t *testing.T) {
	var it DJItem
	if err := json.Unmarshal([]byte(djDetailJSON), &it); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	d := NewDailyJournal(&testCounter{}, nil, "ua", "http://x")
	ci := d.ToCatalogItem(it)
	if ci.ExternalID != "dj-1" || ci.Kind != "article" {
		t.Fatalf("item=%+v", ci)
	}
	if !strings.Contains(ci.ContentText, "recorde diário") {
		t.Fatalf("body not stored: %q", ci.ContentText)
	}
	if ci.Meta["detail_fetched"] != true {
		t.Fatalf("detail_fetched=%v", ci.Meta["detail_fetched"])
	}
	cited := ci.Meta["cited_urls"].([]string)
	if len(cited) != 2 || cited[0] != "https://uol.com.br/a" {
		t.Fatalf("cited_urls=%v", cited)
	}
	if cats := ci.Meta["categories"].([]string); len(cats) != 2 {
		t.Fatalf("categories=%v", cats)
	}
}

// TestDJSchedulerPollAndIdempotent: an api-kind catalog row polls the
// list, stores items, fills bodies from the detail endpoint inside the
// budget, and a re-poll changes nothing.
func TestDJSchedulerPollAndIdempotent(t *testing.T) {
	var detailHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/public/news" {
			fmt.Fprint(w, djListJSON)
			return
		}
		atomic.AddInt64(&detailHits, 1)
		if strings.HasSuffix(r.URL.Path, "/pix-recorde") {
			fmt.Fprint(w, djDetailJSON)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	fs := newFakeStore()
	fs.cat = []store.CatalogSource{*djCatalogSource(srv.URL)}
	s := NewScheduler(fs, "KloxusBot/1.0 (+test)", nil)
	s.Daily = NewDailyJournal(Counter(fs, 9), srv.Client(), "ua", srv.URL)
	s.PollOnce(context.Background())

	items := fs.items[9]
	if len(items) != 2 {
		t.Fatalf("stored=%d want 2", len(items))
	}
	var filled int
	for _, it := range items {
		if it.Meta["detail_fetched"] == true {
			filled++
		}
	}
	if filled != 1 {
		t.Fatalf("details filled=%d want 1", filled)
	}
	if atomic.LoadInt64(&detailHits) != 2 {
		t.Fatalf("detail hits=%d want 2", detailHits)
	}

	// Re-poll: due() is bypassed by resetting last_fetch — the upsert
	// itself must be the idempotency (same payload, no changes).
	fs.entry(9).LastFetchAt = ""
	var changedLogs int
	s.PollOnce(context.Background())
	for _, l := range fs.logs {
		if strings.Contains(l.detail, "updated") {
			changedLogs++
		}
	}
	if changedLogs != 0 {
		t.Fatalf("identical re-poll reported updates")
	}
	if len(fs.items[9]) != 2 {
		t.Fatalf("re-poll duplicated items: %d", len(fs.items[9]))
	}
}

// TestDJBackfillSkipsGoneDetails locks in the F1 fix: the newest
// pending slug 404s on the detail endpoint. The backfill must mark it
// gone and keep filling the rest of the queue — one dead slug can no
// longer starve every item behind it on every poll.
func TestDJBackfillSkipsGoneDetails(t *testing.T) {
	var pixHits, anatelHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/public/news" {
			fmt.Fprint(w, djListJSON)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pix-recorde"):
			atomic.AddInt64(&pixHits, 1)
			w.WriteHeader(404) // story removed upstream — permanently gone
		case strings.HasSuffix(r.URL.Path, "/anatel-5g"):
			atomic.AddInt64(&anatelHits, 1)
			fmt.Fprint(w, `{"id":"dj-2","slug":"anatel-5g",
			 "url":"https://dailyjournal.news/n/anatel-5g",
			 "title":"Anatel publica regulamentação do 5G",
			 "description":"Nova regra em vigor.",
			 "published_at":"2026-11-20T09:00:00Z","updated_at":"2026-11-20T09:30:00Z",
			 "categories":["technology","politics"],"topics":[],
			 "source_count":2,"outlet_count":2,
			 "body":"A Anatel publicou a regulamentação do 5G com novas regras para operadoras.",
			 "bullets":["Novas regras"],"sources":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	fs := newFakeStore()
	fs.cat = []store.CatalogSource{*djCatalogSource(srv.URL)}
	s := NewScheduler(fs, "KloxusBot/1.0 (+test)", nil)
	s.Daily = NewDailyJournal(Counter(fs, 9), srv.Client(), "ua", srv.URL)
	s.PollOnce(context.Background())

	// pix-recorde heads the pending queue and 404s; anatel-5g behind it
	// must still be fetched and filled in the same pass.
	var filled, gone int
	for _, it := range fs.items[9] {
		switch it.Meta["detail_fetched"] {
		case true:
			filled++
		case "gone":
			gone++
		}
	}
	if filled != 1 || gone != 1 {
		t.Fatalf("filled=%d gone=%d want 1,1", filled, gone)
	}
	if pixHits != 1 || anatelHits != 1 {
		t.Fatalf("detail hits pix=%d anatel=%d want 1,1", pixHits, anatelHits)
	}

	// Next poll: the gone mark survives the list refresh (the story
	// was not revised), so neither slug is requested again.
	fs.entry(9).LastFetchAt = ""
	s.PollOnce(context.Background())
	if pixHits != 1 || anatelHits != 1 {
		t.Fatalf("re-poll re-fetched details: pix=%d anatel=%d", pixHits, anatelHits)
	}
	if len(fs.items[9]) != 2 {
		t.Fatalf("re-poll duplicated items: %d", len(fs.items[9]))
	}
}

// TestDJSchedulerUnknownAPISlug: an api source with a slug we don't
// own is skipped — no requests, no crash.
func TestDJSchedulerUnknownAPISlug(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
	}))
	defer srv.Close()
	fs := newFakeStore()
	cs := djCatalogSource(srv.URL)
	cs.Slug = "other-api"
	fs.cat = []store.CatalogSource{*cs}
	s := NewScheduler(fs, "ua", nil)
	s.Daily = NewDailyJournal(Counter(fs, 9), srv.Client(), "ua", srv.URL)
	s.PollOnce(context.Background())
	if atomic.LoadInt64(&hits) != 0 {
		t.Fatalf("unknown api source fetched: %d hits", hits)
	}
}

// TestDJUpdateInvalidates: a changed story re-saves (changed>0) so the
// store can drop stale semantic rows.
func TestDJUpdateInvalidates(t *testing.T) {
	fs := newFakeStore()
	d := NewDailyJournal(&testCounter{}, nil, "ua", "http://x")
	var list struct {
		Items []DJItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(djListJSON), &list); err != nil {
		t.Fatal(err)
	}
	raw := []store.CatalogItem{d.ToCatalogItem(list.Items[0])}
	if a, c, _ := fs.SaveDJItems(context.Background(), 9, raw); a != 1 || c != 0 {
		t.Fatalf("first save a=%d c=%d", a, c)
	}
	if a, c, _ := fs.SaveDJItems(context.Background(), 9, raw); a != 0 || c != 0 {
		t.Fatalf("replay a=%d c=%d — not idempotent", a, c)
	}
	list.Items[0].Title = "Pix bate recorde — atualizado"
	raw2 := []store.CatalogItem{d.ToCatalogItem(list.Items[0])}
	if a, c, _ := fs.SaveDJItems(context.Background(), 9, raw2); a != 0 || c != 1 {
		t.Fatalf("update a=%d c=%d want 0,1", a, c)
	}
}
