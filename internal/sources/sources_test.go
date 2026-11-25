package sources

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/sthefanocordeiro/kloxus/examples"
)

func TestFixtureFetcherRSS(t *testing.T) {
	f := FixtureFetcher{FS: examples.FS}
	items, err := f.Fetch(context.Background(), "rss", "fixtures/feeds/radar-tech.xml")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 10 {
		t.Fatalf("got %d items, want 10", len(items))
	}
	for _, it := range items {
		if it.Title == "" || it.URL == "" || it.ContentText == "" {
			t.Errorf("incomplete item: %+v", it)
		}
	}
}

// Visitor-typed URLs are declined, never fetched — fetching them on the
// hosted demo would be an SSRF surface.
func TestFixtureFetcherSkipsNonFixtureURL(t *testing.T) {
	f := FixtureFetcher{FS: examples.FS}
	_, err := f.Fetch(context.Background(), "rss", "https://example.com/feed.xml")
	var skip *SkipError
	if !errors.As(err, &skip) {
		t.Fatalf("err = %v, want *SkipError", err)
	}
}

// Captured feeds live under feeds/, not fixtures/ — replay must not be
// declined by the demo-mode guard (CI: TestRecordedRunReplays).
func TestCapturedFetcherServesRecordedFeed(t *testing.T) {
	url := "https://real.example/feed.xml"
	fsys := fstest.MapFS{
		"feeds/" + FeedFixtureName("rss", url): {Data: []byte(`<?xml version="1.0"?>
<rss version="2.0"><channel><title>Real Outlet</title>
<item><guid>g1</guid><link>https://real.example/a1</link><title>Story</title><description>d</description></item>
</channel></rss>`)},
	}
	items, err := (CapturedFetcher{FS: fsys}).Fetch(context.Background(), "rss", url)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Story" {
		t.Fatalf("items = %+v", items)
	}
}

func TestFixtureFetcherPage(t *testing.T) {
	f := FixtureFetcher{FS: examples.FS}
	items, err := f.Fetch(context.Background(), "page", "fixtures/pages/observatorio-aberto.html")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 6 {
		t.Fatalf("got %d items, want 6", len(items))
	}
	first := items[0]
	if first.Title != "Pesquisa mapeia como pequenas empresas usam chatbots de IA" {
		t.Errorf("first title = %q", first.Title)
	}
	if first.URL == "" || first.PublishedAt == "" || first.ContentText == "" {
		t.Errorf("incomplete entry: %+v", first)
	}
}

// TestLiveFetcherDecodesNonUTF8Page: html.Parse assumes UTF-8, so a
// Latin-1 index page used to surface as mojibake on the sources,
// pautas and issue screens (KLOX-79). The HTTP charset carries the
// label and the decode happens before parsing.
func TestLiveFetcherDecodesNonUTF8Page(t *testing.T) {
	page := []byte("<html><head><title>Observat\xf3rio Aberto</title></head><body>" +
		"<article class=\"entry\"><h2><a href=\"/a1\">Servi\xe7o p\xfablico digital</a></h2>" +
		"<time datetime=\"2026-11-18\"></time><p class=\"summary\">a\xe7\xe3o r\xe1pida</p></article></body></html>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		w.Write(page)
	}))
	defer srv.Close()

	items, err := (LiveFetcher{HTTP: srv.Client()}).Fetch(context.Background(), "page", srv.URL+"/idx")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Title != "Serviço público digital" {
		t.Fatalf("title=%q — latin-1 page not decoded", items[0].Title)
	}
	if items[0].Outlet != "Observatório Aberto" {
		t.Fatalf("outlet=%q", items[0].Outlet)
	}
}
