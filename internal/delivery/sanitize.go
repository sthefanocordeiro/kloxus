package delivery

// Embed sanitization (KLOX-66 D3). The composer accepts author-pasted
// raw HTML for embed blocks; everything that renders it — the editor
// canvas, the preview, the reader page and the sent email — shows the
// sanitized form only. The boundary is a tag/attribute allowlist:
// active content (script/style/form/event handlers) is dropped with
// its subtree, unknown formatting tags unwrap to their children, and
// remote embeds are limited to iframes on the provider allowlist.

import (
	"html"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// EmbedMaxLen caps one embed block's stored HTML. Generous for a
// provider widget, small enough to keep a body_md line parseable.
const EmbedMaxLen = 64 << 10

// embedTags are tags kept verbatim. Anything else either drops with
// its subtree (embedDropTags) or unwraps so its children survive.
var embedTags = map[string]bool{
	"p": true, "br": true, "hr": true, "div": true, "span": true,
	"strong": true, "b": true, "em": true, "i": true, "u": true,
	"s": true, "del": true, "small": true, "sub": true, "sup": true,
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
	"blockquote": true, "pre": true, "code": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"figure": true, "figcaption": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true,
	"tr": true, "td": true, "th": true, "caption": true,
	"a": true, "img": true, "iframe": true,
}

// embedDropTags drop the node and everything inside it — active or
// interactive content has no place in an email/reader surface.
var embedDropTags = map[string]bool{
	"script": true, "style": true, "form": true, "input": true,
	"textarea": true, "select": true, "option": true, "button": true,
	"object": true, "embed": true, "applet": true, "link": true,
	"meta": true, "base": true, "noscript": true, "svg": true,
	"math": true, "video": true, "audio": true, "source": true,
	"track": true, "picture": true, "template": true, "slot": true,
	"canvas": true, "map": true, "area": true, "frame": true,
	"frameset": true, "portal": true, "dialog": true, "details": true,
	"summary": true,
}

// embedIframeHosts can appear as an iframe src — the usual newsletter
// embed providers. https only.
var embedIframeHosts = map[string]bool{
	"youtube.com": true, "www.youtube.com": true, "m.youtube.com": true,
	"youtube-nocookie.com": true, "www.youtube-nocookie.com": true,
	"player.vimeo.com": true,
	"open.spotify.com": true,
	"w.soundcloud.com": true,
	"player.twitch.tv": true,
	"www.facebook.com": true, // plugins/video.php embeds
}

var embedNum = regexp.MustCompile(`^[0-9]{1,4}$`)

func embedSafeHref(v string) bool {
	v = strings.TrimSpace(v)
	low := strings.ToLower(v)
	return strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://") ||
		strings.HasPrefix(low, "mailto:") || strings.HasPrefix(v, "#")
}

func embedSafeImgSrc(v string) bool {
	v = strings.TrimSpace(v)
	low := strings.ToLower(v)
	return strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://") ||
		strings.HasPrefix(v, "/m/")
}

func embedSafeIframeSrc(v string) bool {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(strings.ToLower(v), "https://") {
		return false
	}
	rest := v[len("https://"):]
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 { // strip :port
		rest = rest[:i]
	}
	return embedIframeHosts[strings.ToLower(rest)]
}

// sanitizeNode appends the allowed form of n to out. Element handling:
// keep allowlisted tags with their allowlisted attributes, unwrap
// other elements (children still get processed), drop dropTags
// entirely, and escape text/comments.
func sanitizeNode(n *xhtml.Node, out *strings.Builder) {
	switch n.Type {
	case xhtml.TextNode:
		out.WriteString(html.EscapeString(n.Data))
	case xhtml.ElementNode:
		tag := strings.ToLower(n.Data)
		if embedDropTags[tag] {
			return
		}
		if !embedTags[tag] {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				sanitizeNode(c, out)
			}
			return
		}
		attrs := embedAttrs(tag, n.Attr)
		if tag == "iframe" && attrs == "" {
			return // an iframe without a valid src is an empty box
		}
		out.WriteByte('<')
		out.WriteString(tag)
		out.WriteString(attrs)
		if embedVoid(tag) {
			out.WriteString(">")
			return
		}
		out.WriteByte('>')
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			sanitizeNode(c, out)
		}
		out.WriteString("</")
		out.WriteString(tag)
		out.WriteByte('>')
	}
}

func embedVoid(tag string) bool {
	return tag == "br" || tag == "hr" || tag == "img"
}

// embedAttrs returns the serialized keep-list for tag, or "" for an
// iframe whose src fails the provider allowlist.
func embedAttrs(tag string, attrs []xhtml.Attribute) string {
	var b strings.Builder
	put := func(k, v string) {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(html.EscapeString(v))
		b.WriteByte('"')
	}
	switch tag {
	case "a":
		for _, a := range attrs {
			if a.Key == "href" && embedSafeHref(a.Val) {
				put("href", a.Val)
				put("target", "_blank")
				put("rel", "noopener noreferrer")
			}
		}
	case "img":
		for _, a := range attrs {
			switch {
			case a.Key == "src" && embedSafeImgSrc(a.Val):
				put("src", a.Val)
			case a.Key == "alt":
				put("alt", a.Val)
			case (a.Key == "width" || a.Key == "height") && embedNum.MatchString(a.Val):
				put(a.Key, a.Val)
			}
		}
	case "td", "th":
		for _, a := range attrs {
			if (a.Key == "colspan" || a.Key == "rowspan") && embedNum.MatchString(a.Val) {
				put(a.Key, a.Val)
			}
		}
	case "iframe":
		src := ""
		w, h := "", ""
		for _, a := range attrs {
			switch {
			case a.Key == "src" && embedSafeIframeSrc(a.Val):
				src = a.Val
			case a.Key == "width" && embedNum.MatchString(a.Val):
				w = a.Val
			case a.Key == "height" && embedNum.MatchString(a.Val):
				h = a.Val
			}
		}
		if src == "" {
			return ""
		}
		if w == "" {
			w = "560"
		}
		if h == "" {
			h = "315"
		}
		put("src", src)
		put("width", w)
		put("height", h)
		put("loading", "lazy")
		put("referrerpolicy", "strict-origin-when-cross-origin")
		put("allowfullscreen", "allowfullscreen")
	}
	return b.String()
}

// SanitizeEmbed reduces pasted embed HTML to the email/reader-safe
// allowlist. The result is what gets stored, rendered and sent — the
// raw paste never reaches an output surface.
func SanitizeEmbed(in string) string {
	if len(in) > EmbedMaxLen {
		in = in[:EmbedMaxLen]
	}
	nodes, err := xhtml.ParseFragment(strings.NewReader(in), &xhtml.Node{
		Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div,
	})
	if err != nil {
		return ""
	}
	var out strings.Builder
	for _, n := range nodes {
		sanitizeNode(n, &out)
	}
	return out.String()
}
