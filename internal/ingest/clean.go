package ingest

// Feed normalization (D28): RSS bodies arrive as HTML — they are
// reduced to readable text, elements matching the source's strip list
// (tracking widgets, offer boxes) are dropped first, and the body is
// capped with a truncated flag so oversized posts can't bloat storage.
import (
	"html"
	"net/url"
	"strings"

	xhtml "golang.org/x/net/html"
)

// maxBody is the stored-body cap; longer feeds set body_truncated.
const maxBody = 100_000

var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "iframe": true,
	"svg": true, "form": true, "button": true, "select": true,
	"nav": true, "footer": true, "header": true, "aside": true,
}

var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "ul": true, "ol": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "blockquote": true,
	"table": true, "tr": true, "section": true, "article": true, "figure": true,
}

// CleanHTML renders feed HTML to text. strip holds class/id substrings —
// any element carrying one (e.g. Tecnoblog's "oferta" offer boxes) is
// removed with its subtree before rendering.
func CleanHTML(raw string, strip []string) (text string, truncated bool) {
	if raw == "" {
		return "", false
	}
	doc, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return plainText(raw), false
	}
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type != xhtml.ElementNode {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			return
		}
		if skipTags[n.Data] || hasStripAttr(n, strip) {
			return
		}
		if blockTags[n.Data] {
			b.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if blockTags[n.Data] {
			b.WriteString("\n")
		}
	}
	walk(doc)
	out := normalizeText(b.String())
	if len(out) > maxBody {
		out = out[:maxBody]
		truncated = true
	}
	return out, truncated
}

func hasStripAttr(n *xhtml.Node, strip []string) bool {
	if len(strip) == 0 {
		return false
	}
	for _, a := range n.Attr {
		if a.Key != "class" && a.Key != "id" {
			continue
		}
		for _, s := range strip {
			if s != "" && strings.Contains(a.Val, s) {
				return true
			}
		}
	}
	return false
}

// plainText is the fallback for HTML that doesn't parse: drop tags and
// unescape entities.
func plainText(raw string) string {
	var b strings.Builder
	inTag := false
	for _, r := range raw {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return normalizeText(html.UnescapeString(b.String()))
}

func normalizeText(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blank := false
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

var dropParams = map[string]bool{
	"utm_source": true, "utm_medium": true, "utm_campaign": true,
	"utm_term": true, "utm_content": true, "utm_name": true,
	"fbclid": true, "gclid": true, "dclid": true, "msclkid": true,
	"mc_cid": true, "mc_eid": true, "igshid": true, "ref": true,
}

// CanonicalURL strips tracking parameters and normalizes scheme/host so
// the same article reached through two feed URLs dedupes to one row.
func CanonicalURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		if dropParams[strings.ToLower(k)] {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}
