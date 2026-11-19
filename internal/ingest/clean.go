package ingest

// Feed normalization (D28): RSS bodies arrive as HTML — they are
// reduced to readable text, elements matching the source's strip list
// (tracking widgets, offer boxes) are dropped first, and the body is
// capped with a truncated flag so oversized posts can't bloat storage.
import (
	"html"
	"net/url"
	"regexp"
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

// Feed boilerplate (KLOX-48): rendered text still carries publisher
// chrome that must never become a sentence — WordPress link-back
// footers, photo credits, affiliate calls and AI-summary blocks.
var (
	// "O post <title> apareceu primeiro em <outlet>." — the feed's
	// link-back footer (Olhar Digital, Startups, others).
	feedFooterRe = regexp.MustCompile(`(?i)^O post .+ apareceu primeiro em .+\.?$`)
	// "Participe dos canais de ofertas…" — Tecnoblog's offer-widget call.
	offerCallRe = regexp.MustCompile(`(?i)participe dos canais de ofertas`)
	// A trailing photo credit: "| Foto: X", "– Imagem: X", "(foto: X)",
	// "(imagens originais: X)". Requires the keyword and a colon so a
	// caption like "| Fotos tiradas de dia" (no colon) is kept.
	creditTailRe = regexp.MustCompile(`(?i)(?:[|–—-]\s*|\()(?:fotos?|image[mn]s?|ilustra\S*)\b[^)\n:]{0,60}:[^)\n]*\)?\s*$`)
	// …or a capitalized bare tail, ". Imagem: Lightspring – Shutterstock" —
	// case-sensitive so a lowercase mid-sentence "a imagem do produto"
	// is never mistaken for a credit.
	creditCapRe = regexp.MustCompile(`(?:^|\s)(?:Foto|Imagem|Ilustra\S*)\s*:[^)\n]{0,120}$`)
	// "… | Crédito: Agência X" — wire-service caption credits (KLOX-67
	// prefilter). The colon keeps a bare "créditos" mid-sentence safe.
	creditoTailRe = regexp.MustCompile(`(?i)(?:[|–—-]\s*|\()cr[eé]ditos?\b[^)\n:]{0,60}:[^)\n]*\)?\s*$`)
	// Tecnoblog's AI-summary box renders as a "Resumo IA" heading
	// followed by bullet paragraphs and a "Gerado com IA…" tail — the
	// whole block is model filler, not reporting.
	resumoHeadRe = regexp.MustCompile(`(?i)^resumo\s+ia\b`)
	resumoTailRe = regexp.MustCompile(`(?i)^gerado com ia\b`)
)

// resumoBlockMax bounds how many non-blank lines a "Resumo IA" block
// may span when its "Gerado com IA" tail never arrives.
const resumoBlockMax = 12

func normalizeText(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blank := false
	resumo := 0 // non-blank lines still droppable inside a Resumo IA block
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		if resumo > 0 {
			resumo--
			if resumoTailRe.MatchString(l) {
				resumo = 0
			}
			continue
		}
		switch {
		case resumoHeadRe.MatchString(l):
			resumo = resumoBlockMax
			continue
		case feedFooterRe.MatchString(l), offerCallRe.MatchString(l):
			continue
		}
		for _, re := range []*regexp.Regexp{creditTailRe, creditCapRe, creditoTailRe} {
			if c := re.FindString(l); c != "" {
				l = strings.TrimSpace(l[:len(l)-len(c)])
			}
		}
		if l == "" {
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
