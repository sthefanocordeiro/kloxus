// Package opml parses OPML subscription exports (Feedly, Inoreader, ...)
// into feed candidates for the source catalog.
package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
)

// MaxBytes bounds an accepted OPML document.
const MaxBytes = 1 << 20

// Feed is one subscription found in an OPML document.
type Feed struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	SiteURL  string `json:"site_url,omitempty"`
	Category string `json:"category,omitempty"`
}

type outline struct {
	Type     string    `xml:"type,attr"`
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr"`
	XMLURL   string    `xml:"xmlUrl,attr"`
	HTMLURL  string    `xml:"htmlUrl,attr"`
	Children []outline `xml:"outline"`
}

type doc struct {
	XMLName xml.Name  `xml:"opml"`
	Body    []outline `xml:"body>outline"`
}

// Parse reads an OPML document and returns its feeds in document order.
// Outlines without an xmlUrl (folders) set the category of their children.
func Parse(r io.Reader) ([]Feed, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("OPML larger than %d bytes", MaxBytes)
	}
	var d doc
	if err := xml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("not a valid OPML document: %w", err)
	}
	var out []Feed
	var walk func(os []outline, cat string)
	walk = func(os []outline, cat string) {
		for _, o := range os {
			name := strings.TrimSpace(o.Title)
			if name == "" {
				name = strings.TrimSpace(o.Text)
			}
			if x := strings.TrimSpace(o.XMLURL); x != "" {
				out = append(out, Feed{Name: name, URL: x, SiteURL: strings.TrimSpace(o.HTMLURL), Category: cat})
				continue
			}
			sub := cat
			if sub == "" {
				sub = name
			}
			walk(o.Children, sub)
		}
	}
	walk(d.Body, "")
	if len(out) == 0 {
		return nil, fmt.Errorf("no feeds (outline xmlUrl) found in OPML")
	}
	return out, nil
}

// Key is the dedupe identity of a feed URL: scheme-insensitive, so the
// http:// and https:// forms of one feed collapse, plus dedupe.Canonical's
// host/www/tracking/trailing-slash normalization.
func Key(raw string) string {
	c := dedupe.Canonical(raw)
	u, err := url.Parse(c)
	if err != nil || u.Host == "" {
		return c
	}
	k := u.Host + u.Path
	if u.RawQuery != "" {
		k += "?" + u.RawQuery
	}
	return k
}

// Domain returns the lowercase host without www., "" when unparseable.
func Domain(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.")
}

// Valid reports whether a feed URL is an absolute http(s) URL.
func Valid(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}
