// Package sources collects raw items from a publication's sources.
// Demo mode uses FixtureFetcher (embedded files); real mode adds RSS/HTTP
// fetchers with robots.txt checks (milestone 4).
package sources

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
)

// RawItem is one collected item before deduplication.
type RawItem struct {
	ExternalID  string
	URL         string
	Title       string
	Author      string
	PublishedAt string
	ContentText string
}

// Fetcher collects raw items from one source definition.
type Fetcher interface {
	Fetch(ctx context.Context, typ, url string) ([]RawItem, error)
}

// FixtureFetcher reads demo fixtures from an fs.FS. Source URLs are paths
// inside that FS (e.g. "fixtures/feeds/radar-tech.xml").
type FixtureFetcher struct{ FS fs.FS }

// Fetch implements Fetcher for fixture files.
func (f FixtureFetcher) Fetch(_ context.Context, typ, url string) ([]RawItem, error) {
	switch typ {
	case "rss":
		return f.fetchRSS(url)
	case "page":
		return f.fetchPage(url)
	default:
		return nil, fmt.Errorf("unknown source type %q", typ)
	}
}

func (f FixtureFetcher) fetchRSS(url string) ([]RawItem, error) {
	file, err := f.FS.Open(url)
	if err != nil {
		return nil, fmt.Errorf("opening fixture %q: %w", url, err)
	}
	defer file.Close()
	feed, err := gofeed.NewParser().Parse(file)
	if err != nil {
		return nil, fmt.Errorf("parsing feed %q: %w", url, err)
	}
	var out []RawItem
	for _, it := range feed.Items {
		id := it.GUID
		if id == "" {
			id = it.Link
		}
		published := ""
		if it.PublishedParsed != nil {
			published = it.PublishedParsed.UTC().Format(time.RFC3339)
		} else {
			published = it.Published
		}
		content := it.Content
		if content == "" {
			content = it.Description
		}
		out = append(out, RawItem{
			ExternalID:  id,
			URL:         it.Link,
			Title:       strings.TrimSpace(it.Title),
			Author:      authorName(it),
			PublishedAt: published,
			ContentText: strings.TrimSpace(content),
		})
	}
	return out, nil
}

func authorName(it *gofeed.Item) string {
	if it.Author != nil {
		return it.Author.Name
	}
	return ""
}

// fetchPage extracts items from a fixture HTML index page. Each entry is an
// <article class="entry"> with an <h2><a> link/title, a <time datetime>, and
// a <p class="summary">.
func (f FixtureFetcher) fetchPage(url string) ([]RawItem, error) {
	file, err := f.FS.Open(url)
	if err != nil {
		return nil, fmt.Errorf("opening fixture %q: %w", url, err)
	}
	defer file.Close()
	doc, err := html.Parse(file)
	if err != nil {
		return nil, fmt.Errorf("parsing page %q: %w", url, err)
	}
	var out []RawItem
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "article" && hasClass(n, "entry") {
			if item, ok := parseEntry(n); ok {
				out = append(out, item)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(out) == 0 {
		return nil, fmt.Errorf("page %q: no article entries found", url)
	}
	return out, nil
}

func parseEntry(n *html.Node) (RawItem, bool) {
	var item RawItem
	var find func(m *html.Node)
	find = func(m *html.Node) {
		switch {
		case m.Type == html.ElementNode && m.Data == "a" && item.URL == "":
			item.URL = attr(m, "href")
			item.Title = strings.TrimSpace(textOf(m))
		case m.Type == html.ElementNode && m.Data == "time":
			item.PublishedAt = attr(m, "datetime")
		case m.Type == html.ElementNode && m.Data == "p" && hasClass(m, "summary"):
			item.ContentText = strings.TrimSpace(textOf(m))
		}
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(n)
	item.ExternalID = item.URL
	return item, item.URL != "" && item.Title != ""
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(m *html.Node)
	walk = func(m *html.Node) {
		if m.Type == html.TextNode {
			b.WriteString(m.Data)
		}
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
