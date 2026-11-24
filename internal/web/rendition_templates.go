package web

// Fixed templates that fork an approved piece into per-format
// renditions (KLOX-125, KLOX-170). No model is involved: the output is a
// pure function of the issue document, so the same piece always forks to
// the same text. Every story leads with its title linked to the original
// article (D8); there is no automatic document title, no [n] markers and
// no raw interpretation markup.
import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/config"
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

// rdBlock is one rendered unit: heading (level 2 section, 3 story),
// para or link. A story heading carries the article URL.
type rdBlock struct {
	Kind  string
	Level int
	Text  string
	URL   string
}

type rdDoc struct {
	Blocks []rdBlock
}

// buildRenditionDoc flattens the issue document into the neutral model
// every template renders from.
func buildRenditionDoc(doc *issueDocument) *rdDoc {
	out := &rdDoc{}
	for _, sec := range doc.Sections {
		if t := strings.TrimSpace(sec.Title); t != "" && t != config.PostSectionTitle {
			out.Blocks = append(out.Blocks, rdBlock{Kind: "heading", Level: 2, Text: t})
		}
		if t := cleanExportText(sec.Commentary.Text); t != "" {
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
					out.Blocks = append(out.Blocks, rdBlock{Kind: "heading", Level: 3, Text: h, URL: itemURL(it)})
				}
				var facts []string
				var interp []rdBlock
				for _, s := range it.Sentences {
					t := cleanExportText(s.Text)
					if t == "" {
						continue
					}
					if s.Kind == "interpretation" {
						interp = append(interp, rdBlock{Kind: "para", Text: t})
						continue
					}
					facts = append(facts, t)
				}
				if len(facts) > 0 {
					out.Blocks = append(out.Blocks, rdBlock{Kind: "para", Text: strings.Join(facts, " ")})
				}
				out.Blocks = append(out.Blocks, interp...)
			case "heading":
				if t := strings.TrimSpace(b.Text); t != "" {
					out.Blocks = append(out.Blocks, rdBlock{Kind: "heading", Level: 3, Text: t})
				}
			case "text":
				if t := cleanExportText(b.Text); t != "" {
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

// itemURL is the story's article link: its first source with a URL.
func itemURL(it *docItem) string {
	for _, src := range it.Sources {
		if src.URL != "" {
			return src.URL
		}
	}
	return ""
}

// cleanExportText strips the stored interpretation wrapper
// ("> _Interpretação: …_"), however many times it was nested, so no raw
// markdown symbols reach an export.
func cleanExportText(t string) string {
	lines := strings.Split(strings.TrimSpace(t), "\n")
	for i, l := range lines {
		for {
			tl := strings.TrimSpace(l)
			next := interpretationText(tl)
			if next == tl {
				break
			}
			l = next
		}
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// rdStory is a story (a level-3 heading and what follows it). Text that
// precedes the first story, or follows a section heading, is a headless
// story.
type rdStory struct {
	Title, URL string
	Paras      []string
}

func groupStories(d *rdDoc) []rdStory {
	var out []rdStory
	cur := -1
	for _, bl := range d.Blocks {
		switch bl.Kind {
		case "heading":
			if bl.Level == 3 {
				out = append(out, rdStory{Title: bl.Text, URL: bl.URL})
				cur = len(out) - 1
			} else {
				cur = -1
			}
		case "para", "link":
			txt := bl.Text
			if bl.Kind == "link" {
				txt = bl.URL
				if bl.Text != "" {
					txt = bl.Text + ": " + bl.URL
				}
			}
			if cur < 0 {
				out = append(out, rdStory{})
				cur = len(out) - 1
			}
			out[cur].Paras = append(out[cur].Paras, txt)
		}
	}
	return out
}

func (st rdStory) head() string {
	if st.Title == "" {
		return ""
	}
	if st.URL == "" {
		return st.Title
	}
	return st.Title + "\n" + st.URL
}

// renderText is the LinkedIn template: per story the title, the URL on
// the line below, then the text.
func renderText(d *rdDoc) string {
	var parts []string
	for _, st := range groupStories(d) {
		p := append([]string{}, st.Paras...)
		if h := st.head(); h != "" {
			p = append([]string{h}, p...)
		}
		if len(p) > 0 {
			parts = append(parts, strings.Join(p, "\n\n"))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n") + "\n"
}

const (
	xURLLen  = 23  // X counts every link as 23 characters
	xLimit   = 280 // post length cap
	xNumRoom = 7   // room a " 12/12" thread counter needs
)

// xLen counts a post the way X does.
func xLen(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		words := strings.Fields(line)
		for i, w := range words {
			if strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://") {
				n += xURLLen
			} else {
				n += len([]rune(w))
			}
			if i > 0 {
				n++
			}
		}
		n++ // newline
	}
	return n - 1
}

// renderX is the X template: the opening (text before the first story)
// is the first post and then there is one post per story — title, URL
// (23 characters), text. A story over the limit continues in follow-up
// posts cut at word boundaries. In the stored body a blank line is
// always a post boundary — the export copies one block per post — so
// the lines inside a post join with single breaks and no "---"
// separators appear.
func renderX(d *rdDoc) string {
	stories := groupStories(d)
	posts := xPosts(stories, xLimit)
	if len(posts) > 1 {
		// The export appends a " n/N" counter to every post; keep room
		// for it so a numbered post still fits.
		posts = xPosts(stories, xLimit-xNumRoom)
	}
	if len(posts) == 0 {
		return ""
	}
	return strings.Join(posts, "\n\n") + "\n"
}

// xPosts packs each story into its own posts of at most limit
// characters; a story's first post carries its title+URL head.
func xPosts(stories []rdStory, limit int) []string {
	var posts []string
	for _, st := range stories {
		head := st.head()
		cur := head
		flush := func() {
			if strings.TrimSpace(cur) != "" {
				posts = append(posts, strings.TrimSpace(cur))
			}
			cur = ""
		}
		add := func(sep, s string) {
			if cur == "" {
				cur = s
			} else {
				cur += sep + s
			}
		}
		for _, p := range st.Paras {
			sep := "\n"
			if cur == "" {
				sep = ""
			}
			if xLen(cur+sep+p) <= limit {
				add(sep, p)
				continue
			}
			if cur != "" && cur != head {
				flush()
			}
			for _, w := range strings.Fields(p) {
				wsep := " "
				if cur == "" {
					wsep = ""
				} else if cur == head {
					wsep = "\n"
				}
				if cur != "" && cur != head && xLen(cur+wsep+w) > limit {
					flush()
					wsep = ""
				}
				add(wsep, w)
			}
		}
		flush()
	}
	return posts
}

// renderHTML is the blog / newsletter template: per story an <h3> whose
// text links to the article, then the text. The markup stays inside what
// the TipTap schema round-trips.
func renderHTML(d *rdDoc) string {
	var b strings.Builder
	for _, bl := range d.Blocks {
		switch bl.Kind {
		case "heading":
			t := html.EscapeString(bl.Text)
			if bl.URL != "" {
				t = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(bl.URL), t)
			}
			fmt.Fprintf(&b, "<h%d>%s</h%d>\n", bl.Level, t, bl.Level)
		case "para":
			b.WriteString("<p>" + html.EscapeString(bl.Text) + "</p>\n")
		case "link":
			t := bl.Text
			if t == "" {
				t = bl.URL
			}
			fmt.Fprintf(&b, "<p><a href=\"%s\">%s</a></p>\n", html.EscapeString(bl.URL), html.EscapeString(t))
		}
	}
	return b.String()
}

var mdEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", `\<`)

func mdURL(u string) string {
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29").Replace(u)
}

// renderMarkdown is the Ghost / Notion / Obsidian template:
// "### [Title](url)" then the text.
func renderMarkdown(d *rdDoc) string {
	var b strings.Builder
	for _, bl := range d.Blocks {
		switch bl.Kind {
		case "heading":
			t := mdEscaper.Replace(bl.Text)
			if bl.URL != "" {
				t = "[" + t + "](" + mdURL(bl.URL) + ")"
			}
			b.WriteString("\n" + strings.Repeat("#", bl.Level) + " " + t + "\n")
		case "para":
			b.WriteString("\n" + mdEscaper.Replace(bl.Text) + "\n")
		case "link":
			t := bl.Text
			if t == "" {
				t = bl.URL
			}
			fmt.Fprintf(&b, "\n[%s](%s)\n", mdEscaper.Replace(t), mdURL(bl.URL))
		}
	}
	return strings.TrimLeft(b.String(), "\n")
}

// renditionBodies forks the document into the column of one format and
// reports the hash of all templates (the "base" a later refresh compares
// against).
func renditionBodies(doc *issueDocument, format string) (text, htm, md, hash string) {
	d := buildRenditionDoc(doc)
	t, x, h, m := renderText(d), renderX(d), renderHTML(d), renderMarkdown(d)
	sum := sha256.Sum256([]byte(t + "\x00" + x + "\x00" + h + "\x00" + m))
	hash = hex.EncodeToString(sum[:])
	switch format {
	case FmtLinkedIn:
		text = t
	case FmtX:
		text = x
	case FmtHTML:
		htm = h
	case FmtMarkdown:
		md = m
	}
	return
}
