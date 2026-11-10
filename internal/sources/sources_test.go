package sources

import (
	"context"
	"testing"

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
