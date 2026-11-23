package web

// Fixed templates that fork an approved piece into per-format
// renditions (KLOX-125). No model is involved: the output is a pure
// function of the issue document, so the same piece always forks to
// the same text. Citation markers carry over as [n] (plain text) or as
// links to the cited source (HTML / Markdown), and every format ends
// with the numbered source list.
import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"strings"
)

// Rendition formats.
const (
	FmtLinkedIn = "linkedin"
	FmtX        = "x"
	FmtHTML     = "html"
	FmtMarkdown = "markdown"
)

func validRenditionFormat(f string) bool {
	switch f {
	case FmtLinkedIn, FmtX, FmtHTML, FmtMarkdown:
		return true
	}
	return false
}

type rdSource struct {
	N      int
	Outlet string
	URL    string
}

// rdBlock is one rendered unit: heading (level 2 section, 3 story),
// para or link.
type rdBlock struct {
	Kind  string
	Level int
	Text  string
	Cites []int // global source numbers
	URL   string
}

type rdDoc struct {
	Title   string
	Blocks  []rdBlock
	Sources []rdSource
}

// buildRenditionDoc flattens the issue document into the neutral model
// every template renders from. Sources are numbered globally by URL in
// order of first citation.
func buildRenditionDoc(doc *issueDocument) *rdDoc {
	out := &rdDoc{Title: strings.TrimSpace(doc.Subject)}
	if out.Title == "" {
		out.Title = fmt.Sprintf("Peça %d", doc.Number)
	}
	byURL := map[string]int{}
	number := func(s docSource) int {
		key := s.URL
		if key == "" {
			key = "name:" + s.Name
		}
		if n, ok := byURL[key]; ok {
			return n
		}
		n := len(out.Sources) + 1
		byURL[key] = n
		out.Sources = append(out.Sources, rdSource{N: n, Outlet: s.Name, URL: s.URL})
		return n
	}
	for _, sec := range doc.Sections {
		if t := strings.TrimSpace(sec.Title); t != "" {
			out.Blocks = append(out.Blocks, rdBlock{Kind: "heading", Level: 2, Text: t})
		}
		if t := strings.TrimSpace(sec.Commentary.Text); t != "" {
			out.Blocks = append(out.Blocks, rdBlock{Kind: "para", Text: t})
		}
		for _, b := range sec.Blocks {
			switch b.Kind {
			case "item":
				it := doc.findItem(b.Ref)
				if it == nil {
					continue
				}
				if h := strings.TrimSpace(it.Headline); h != "" {
					out.Blocks = append(out.Blocks, rdBlock{Kind: "heading", Level: 3, Text: h})
				}
				var facts []string
				var cites []int
				var interp []rdBlock
				for _, s := range it.Sentences {
					var sc []int
					for _, ref := range s.Citations {
						for _, src := range it.Sources {
							if src.Ref == ref {
								sc = append(sc, number(src))
								break
							}
						}
					}
					if s.Kind == "interpretation" {
						interp = append(interp, rdBlock{Kind: "para", Text: s.Text, Cites: sc})
						continue
					}
					facts = append(facts, s.Text)
					cites = append(cites, sc...)
				}
				if len(facts) > 0 {
					out.Blocks = append(out.Blocks, rdBlock{Kind: "para", Text: strings.Join(facts, " "), Cites: cites})
				}
				out.Blocks = append(out.Blocks, interp...)
			case "heading":
				if t := strings.TrimSpace(b.Text); t != "" {
					out.Blocks = append(out.Blocks, rdBlock{Kind: "heading", Level: 3, Text: t})
				}
			case "text":
				if t := strings.TrimSpace(b.Text); t != "" {
					out.Blocks = append(out.Blocks, rdBlock{Kind: "para", Text: t})
				}
			case "link":
				if b.URL != "" {
					out.Blocks = append(out.Blocks, rdBlock{Kind: "link", Text: b.Text, URL: b.URL})
				}
			}
		}
	}
	return out
}

func dedupInts(in []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, n := range in {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func srcLabel(s rdSource) string {
	if s.Outlet != "" {
		return s.Outlet
	}
	return s.URL
}

func srcURLs(d *rdDoc) map[int]string {
	m := map[int]string{}
	for _, s := range d.Sources {
		m[s.N] = s.URL
	}
	return m
}

// renderText is the LinkedIn / X template: title, then the text and
// comments as blank-line separated paragraphs, then "Fontes:".
func renderText(d *rdDoc) string {
	var b strings.Builder
	b.WriteString(d.Title)
	for _, bl := range d.Blocks {
		switch bl.Kind {
		case "para":
			b.WriteString("\n\n" + bl.Text)
			for _, n := range dedupInts(bl.Cites) {
				fmt.Fprintf(&b, " [%d]", n)
			}
		case "link":
			b.WriteString("\n\n")
			if bl.Text != "" {
				b.WriteString(bl.Text + ": ")
			}
			b.WriteString(bl.URL)
		}
	}
	if len(d.Sources) > 0 {
		b.WriteString("\n\nFontes:")
		for _, s := range d.Sources {
			fmt.Fprintf(&b, "\n[%d] %s", s.N, srcLabel(s))
			if s.URL != "" && s.Outlet != "" {
				b.WriteString(" — " + s.URL)
			}
		}
	}
	return b.String() + "\n"
}

// renderHTML is the newsletter / blog / intranet template: a heading per
// section, paragraphs, citations as links, sources at the end. The
// markup stays inside what the TipTap schema round-trips.
func renderHTML(d *rdDoc) string {
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(d.Title) + "</h1>\n")
	srcURL := srcURLs(d)
	for _, bl := range d.Blocks {
		switch bl.Kind {
		case "heading":
			fmt.Fprintf(&b, "<h%d>%s</h%d>\n", bl.Level, html.EscapeString(bl.Text), bl.Level)
		case "para":
			b.WriteString("<p>" + html.EscapeString(bl.Text))
			for _, n := range dedupInts(bl.Cites) {
				if u := srcURL[n]; u != "" {
					fmt.Fprintf(&b, ` <a href="%s">[%d]</a>`, html.EscapeString(u), n)
				} else {
					fmt.Fprintf(&b, " [%d]", n)
				}
			}
			b.WriteString("</p>\n")
		case "link":
			t := bl.Text
			if t == "" {
				t = bl.URL
			}
			fmt.Fprintf(&b, "<p><a href=\"%s\">%s</a></p>\n", html.EscapeString(bl.URL), html.EscapeString(t))
		}
	}
	if len(d.Sources) > 0 {
		b.WriteString("<h2>Fontes</h2>\n<ol>\n")
		for _, s := range d.Sources {
			if s.URL != "" {
				fmt.Fprintf(&b, "<li><a href=\"%s\">%s</a></li>\n", html.EscapeString(s.URL), html.EscapeString(srcLabel(s)))
			} else {
				fmt.Fprintf(&b, "<li>%s</li>\n", html.EscapeString(srcLabel(s)))
			}
		}
		b.WriteString("</ol>\n")
	}
	return b.String()
}

var mdEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", `\<`)

func mdURL(u string) string {
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29").Replace(u)
}

// renderMarkdown is the Ghost / Notion / Obsidian template.
func renderMarkdown(d *rdDoc) string {
	var b strings.Builder
	b.WriteString("# " + mdEscaper.Replace(d.Title) + "\n")
	srcURL := srcURLs(d)
	for _, bl := range d.Blocks {
		switch bl.Kind {
		case "heading":
			b.WriteString("\n" + strings.Repeat("#", bl.Level) + " " + mdEscaper.Replace(bl.Text) + "\n")
		case "para":
			b.WriteString("\n" + mdEscaper.Replace(bl.Text))
			for _, n := range dedupInts(bl.Cites) {
				if u := srcURL[n]; u != "" {
					fmt.Fprintf(&b, " [\\[%d\\]](%s)", n, mdURL(u))
				} else {
					fmt.Fprintf(&b, " \\[%d\\]", n)
				}
			}
			b.WriteString("\n")
		case "link":
			t := bl.Text
			if t == "" {
				t = bl.URL
			}
			fmt.Fprintf(&b, "\n[%s](%s)\n", mdEscaper.Replace(t), mdURL(bl.URL))
		}
	}
	if len(d.Sources) > 0 {
		b.WriteString("\n## Fontes\n\n")
		for _, s := range d.Sources {
			if s.URL != "" {
				fmt.Fprintf(&b, "%d. [%s](%s)\n", s.N, mdEscaper.Replace(srcLabel(s)), mdURL(s.URL))
			} else {
				fmt.Fprintf(&b, "%d. %s\n", s.N, mdEscaper.Replace(srcLabel(s)))
			}
		}
	}
	return b.String()
}

// renditionBodies forks the document into the column of one format and
// reports the hash of all three templates (the "base" a later refresh
// compares against).
func renditionBodies(doc *issueDocument, format string) (text, htm, md, hash string) {
	d := buildRenditionDoc(doc)
	t, h, m := renderText(d), renderHTML(d), renderMarkdown(d)
	sum := sha256.Sum256([]byte(t + "\x00" + h + "\x00" + m))
	hash = hex.EncodeToString(sum[:])
	switch format {
	case FmtLinkedIn, FmtX:
		text = t
	case FmtHTML:
		htm = h
	case FmtMarkdown:
		md = m
	}
	return
}
