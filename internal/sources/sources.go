// Package sources collects raw items from a publication's sources.
// Demo mode uses FixtureFetcher (embedded files); real mode uses
// LiveFetcher (HTTP). Page monitoring lands via the Firecrawl webhook.
package sources

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
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
	Outlet      string // feed channel title / page name, when known
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

// CapturedFetcher answers a real source URL with its recorded capture
// inside FS at feeds/<FeedFixtureName(type,url)>. Unlike rewriting a
// definition's URLs to fixture paths, the real URL still reaches the
// pipeline — outlets keep their real names in prompts, so replay keys
// and model input match the original recorded run.
type CapturedFetcher struct{ FS fs.FS }

// Fetch implements Fetcher against the recorded captures.
func (f CapturedFetcher) Fetch(ctx context.Context, typ, url string) ([]RawItem, error) {
	return FixtureFetcher{FS: f.FS}.Fetch(ctx, typ, "feeds/"+FeedFixtureName(typ, url))
}

func (f FixtureFetcher) fetchRSS(url string) ([]RawItem, error) {
	file, err := f.FS.Open(url)
	if err != nil {
		return nil, fmt.Errorf("opening fixture %q: %w", url, err)
	}
	defer file.Close()
	return parseFeed(file, url)
}

// parseFeed decodes an RSS/Atom feed from r into raw items.
func parseFeed(r io.Reader, name string) ([]RawItem, error) {
	feed, err := gofeed.NewParser().Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parsing feed %q: %w", name, err)
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
			Outlet:      strings.TrimSpace(feed.Title),
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
	return parseIndexPage(file, url)
}

// parseIndexPage extracts items from an HTML index page using the
// article.entry markup described above. The page's <title> becomes the
// outlet name.
func parseIndexPage(r io.Reader, name string) ([]RawItem, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parsing page %q: %w", name, err)
	}
	outlet := pageTitle(doc)
	var out []RawItem
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "article" && hasClass(n, "entry") {
			if item, ok := parseEntry(n); ok {
				item.Outlet = outlet
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
		return nil, fmt.Errorf("page %q: no article entries found", name)
	}
	return out, nil
}

func pageTitle(doc *html.Node) string {
	var title string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if title != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "title" {
			title = strings.TrimSpace(textOf(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return title
}

// LiveFetcher fetches real sources over HTTP (real mode). RSS feeds are
// fetched and parsed; index pages are scanned for the same entry markup the
// demo fixtures use. Arbitrary page monitoring goes through the Firecrawl
// monitor and its webhook instead of this fetcher.
type LiveFetcher struct {
	HTTP *http.Client // defaults to a client with a 30s timeout
	// DumpDir, when set, saves each fetched body under
	// FeedFixtureName(typ, url) — `kloxus record --live` uses it to build
	// replayable feed fixtures alongside the recorded LLM responses.
	DumpDir string
}

// FeedFixtureName is the canonical fixture filename for a captured feed
// or page: stable on the source URL so a publication definition keeps
// pointing at the same capture across recordings.
func FeedFixtureName(typ, url string) string {
	ext := "html"
	if typ == "rss" {
		ext = "xml"
	}
	sum := sha1.Sum([]byte(url))
	return fmt.Sprintf("%s-%x.%s", typ, sum[:5], ext)
}

func (f LiveFetcher) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Fetch implements Fetcher over HTTP.
func (f LiveFetcher) Fetch(ctx context.Context, typ, url string) ([]RawItem, error) {
	if typ != "rss" && typ != "page" {
		return nil, fmt.Errorf("unknown source type %q", typ)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kloxus/0.1 (+https://github.com/sthefanocordeiro/kloxus)")
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching %q: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", url, err)
	}
	f.dump(typ, url, body)
	if typ == "rss" {
		return parseFeed(bytes.NewReader(body), url)
	}
	return parseIndexPage(bytes.NewReader(body), url)
}

// dump saves a fetched body as a fixture file under DumpDir. Failures
// are non-fatal — capturing fixtures is auxiliary to fetching.
func (f LiveFetcher) dump(typ, url string, body []byte) {
	if f.DumpDir == "" {
		return
	}
	if err := os.MkdirAll(f.DumpDir, 0o755); err != nil {
		return
	}
	os.WriteFile(filepath.Join(f.DumpDir, FeedFixtureName(typ, url)), body, 0o644)
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
