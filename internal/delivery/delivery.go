// Package delivery renders issues for readers and records simulated
// outgoing mail in the workspace outbox. Demo mode stores every message
// in the outbox_emails table; real delivery lands with milestone 4.
package delivery

import (
	"context"
	"fmt"
	"html"
	"mime"
	"regexp"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// Mailer delivers an issue to readers.
type Mailer interface {
	Send(subject, bodyMD, bodyHTML string, to []string) (string, error)
}

// Outbox "sends" by recording the message in the workspace outbox table
// (demo mode). Returns the outbox email id as a string.
type Outbox struct {
	DB    *store.DB
	Kind  string // issue | test | magic_link
	PubID int64
	Ctx   context.Context
}

// sendTimeout caps an outbox write when the caller's ctx has no
// deadline (the scheduler passes its root ctx) — a dead pooled conn
// can't wedge the calling worker.
const sendTimeout = 5 * time.Minute

// Send implements Mailer.
func (o Outbox) Send(subject, bodyMD, bodyHTML string, to []string) (string, error) {
	kind := o.Kind
	if kind == "" {
		kind = "issue"
	}
	ctx := o.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	id, err := o.DB.SaveOutboxEmail(ctx, kind, strings.Join(to, ", "), subject, bodyMD, bodyHTML)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(id), nil
}

// EML renders a stored outbox row as a downloadable .eml message with
// standard headers (RFC 5322 date, MIME-Version, encoded-word subject).
func EML(e *store.OutboxEmail) string {
	when := time.Now().UTC()
	if t, err := time.Parse("2006-01-02 15:04:05", e.CreatedAt); err == nil {
		when = t.UTC()
	} else if t, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
		when = t.UTC()
	}
	subject := mime.QEncoding.Encode("utf-8", e.Subject)
	return fmt.Sprintf(
		"From: Kloxus <demo@trendfi.com.br>\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s",
		e.To, subject, when.Format("Mon, 02 Jan 2006 15:04:05 +0000"), e.BodyHTML)
}

// aiDisclosure returns the disclosure line for approved output, or "".
// The line speaks the publication's own language (KLOX-56).
func aiDisclosure(pub *store.Publication) string {
	pt := strings.HasPrefix(strings.ToLower(pub.Language), "pt")
	switch pub.AIDisclosure {
	case "short":
		if pt {
			return "_Preparado com auxílio de IA e revisado pelo editor._"
		}
		return "_Prepared with AI assistance and reviewed by the editor._"
	case "detailed":
		if pt {
			return "_Esta edição foi preparada por um assistente editorial de IA a partir das fontes listadas abaixo; cada fato remete à sua fonte. Todo comentário e a revisão final são do editor._"
		}
		return "_This issue was prepared by an AI editorial assistant from the sources listed below; every fact links to its source. All commentary and final review are the editor's._"
	}
	return ""
}

var (
	itemMarkerRe  = regexp.MustCompile(`\s*<!--\s*item \d+(?:\s+link)?\s*-->`)
	relLinkRe     = regexp.MustCompile(`\]\((/a/\d+)\)`)
	sectionRe     = regexp.MustCompile(`^## (.+)$`)
	blankRunRe    = regexp.MustCompile(`\n{3,}`)
	trailSpacesRe = regexp.MustCompile(`[ \t]+\n`)
	// commentRe is any remaining structural comment (<!-- /item -->
	// boundaries) — they must never reach a reader surface.
	commentRe = regexp.MustCompile(`^\s*<!--.*-->\s*$`)
)

// ReaderBody renders the reader-facing markdown for an issue:
//   - the AI disclosure is prepended once the issue is approved/sent —
//     and only when a model actually ran for it (KLOX-56);
//   - the editor's commentary is appended at the end of its section,
//     clearly attributed;
//   - internal item markers are stripped;
//   - collapsed runs of blank lines and a full stop on the Sources
//     line keep the export clean;
//   - relative /a/<id> source links become absolute when base is given.
func ReaderBody(iss *store.Issue, pub *store.Publication, base string, aiUsed bool) string {
	lines := strings.Split(iss.BodyMD, "\n")
	var out []string
	inFirstSection := false
	pt := strings.HasPrefix(strings.ToLower(pub.Language), "pt")
	commentaryLabel := "Editor's note:"
	if pt {
		commentaryLabel = "Comentário do editor:"
	}
	flush := func(section string) {
		if section == "" {
			return
		}
		if c := strings.TrimSpace(iss.Commentary[section]); c != "" {
			out = append(out, "> **"+commentaryLabel+"** "+c, "")
		}
	}
	current := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			if inFirstSection {
				flush(current)
			}
			inFirstSection = true
			current = m[1]
		}
		line = itemMarkerRe.ReplaceAllString(line, "")
		if commentRe.MatchString(line) {
			continue
		}
		if strings.TrimSpace(line) == EmbedFenceOpen {
			// consume the whole fence region verbatim: inner lines are
			// HTML, not markdown, so none of the line rewrites apply —
			// and an unclosed fence swallows the rest of the body, the
			// same shape the reader renderer gives it.
			out = append(out, line)
			for i+1 < len(lines) {
				i++
				out = append(out, lines[i])
				if strings.TrimSpace(lines[i]) == EmbedFenceClose {
					break
				}
			}
			continue
		}
		// the sources footer is a sentence — it earns its full stop
		// like every other line in the export (KLOX-56).
		if strings.HasPrefix(strings.TrimSpace(line), "**Sources:**") &&
			!strings.HasSuffix(strings.TrimSpace(line), ".") {
			line = strings.TrimRight(line, " \t") + "."
		}
		out = append(out, line)
	}
	flush(current)
	body := strings.Join(out, "\n")
	// empty item bodies and marker strips can leave blank runs —
	// collapse them so exports show no stray empty lines (KLOX-56).
	body = trailSpacesRe.ReplaceAllString(body, "\n")
	body = blankRunRe.ReplaceAllString(body, "\n\n")
	if (iss.Status == "approved" || iss.Status == "sent") && aiUsed {
		if d := aiDisclosure(pub); d != "" {
			if i := strings.Index(body, "\n"); i >= 0 {
				body = body[:i+1] + "\n" + d + "\n" + body[i+1:]
			} else {
				body = d + "\n\n" + body
			}
		}
	}
	// normalise citation placement: "urbana. [1]" → "urbana [1]."
	body = citePlaceRe.ReplaceAllString(body, " $1.")
	if base != "" {
		body = relLinkRe.ReplaceAllString(body, "]("+base+"$1)")
	}
	// The reader-page renderer injects fence content raw, so every
	// embed region in the emitted markdown is re-sanitized on the way
	// out — including fences smuggled in past the write path or inside
	// multi-line commentary (KLOX-66 D3).
	body = SanitizeEmbedFences(body)
	return body
}

// SanitizeEmbedFences re-parses every <klox-embed> region of a rendered
// markdown body through the embed allowlist. Any surface that injects
// fence content raw — the reader page, the composer canvas, the
// preview — can only ever receive sanitized HTML: a fence that
// bypassed the write path loses its payload here.
func SanitizeEmbedFences(md string) string {
	if !strings.Contains(md, EmbedFenceOpen) {
		return md
	}
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)
		if strings.TrimSpace(line) != EmbedFenceOpen {
			continue
		}
		var inner strings.Builder
		closed := false
		for i+1 < len(lines) {
			i++
			if strings.TrimSpace(lines[i]) == EmbedFenceClose {
				closed = true
				break
			}
			inner.WriteString(lines[i])
			inner.WriteByte('\n')
		}
		out = append(out, SanitizeEmbed(inner.String()))
		if closed {
			out = append(out, lines[i])
		}
	}
	return strings.Join(out, "\n")
}

// citePlaceRe finds a citation marker left dangling after a period so it
// can be moved inside the sentence it cites (a "." after the marker is
// absorbed, avoiding "x [1]..").
var citePlaceRe = regexp.MustCompile(`\.\s*(\[\d+\])\.?`)

var (
	reH4     = regexp.MustCompile(`^#### (.+?)\s*$`)
	reH3     = regexp.MustCompile(`^### (.+?)\s*$`)
	reH2     = regexp.MustCompile(`^## (.+)$`)
	reH1     = regexp.MustCompile(`^# (.+)$`)
	reHR     = regexp.MustCompile(`^(---+|\*\*\*+|___+)\s*$`)
	reList   = regexp.MustCompile(`^[-*] (.+)$`)
	reBold   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic = regexp.MustCompile(`_(.+?)_`)
	reStarIt = regexp.MustCompile(`\*([^*]+)\*`)
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	reBare   = regexp.MustCompile(`\[(https?://[^\]]+)\]`)
	reImage  = regexp.MustCompile(`^!\[([^\]]*)\]\((\S+)\)\s*$`)
)

// EmbedFenceOpen/EmbedFenceClose delimit a stored embed block inside
// an issue body: the sanitizer's output sits between them and is the
// only raw HTML allowed through to reader/email surfaces.
const EmbedFenceOpen = "<klox-embed>"
const EmbedFenceClose = "</klox-embed>"

// renderSafeHref reports whether href may ship inside <a href> — web
// schemes, mailto, and root-relative/anchor links only.
func renderSafeHref(href string) bool {
	l := strings.ToLower(strings.TrimSpace(href))
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") ||
		strings.HasPrefix(l, "mailto:") || strings.HasPrefix(l, "/") ||
		strings.HasPrefix(l, "#")
}

// renderSafeImgSrc reports whether src may ship inside <img src> —
// remote web images or the uploaded /m/ media path (any root-relative
// path is same-origin).
func renderSafeImgSrc(src string) bool {
	l := strings.ToLower(strings.TrimSpace(src))
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") ||
		strings.HasPrefix(src, "/")
}

// RenderHTML converts the issue markdown subset to a simple HTML
// document. Markdown links [text](url) become <a href>; a bare [url]
// becomes a self-linked anchor; base absolutizes root-relative links.
func RenderHTML(title, bodyMD, base string) string {
	var b strings.Builder
	// The email body reads like the finished newsletter (KLOX-66):
	// Source Serif 4 with a serif fallback, the teal brand accent on
	// rules and links, and real <hr>/<ul>/<em> elements — no raw
	// markdown markers may reach the reader (§8.6).
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) +
		"</title><style>body{font-family:'Source Serif 4',Georgia,serif;max-width:44em;margin:2em auto;padding:0 1em;line-height:1.55;color:#1a1a1a}" +
		"h1{border-bottom:2px solid #0e7a6d;padding-bottom:.3em}h2{margin-top:2em;border-bottom:1px solid #ccc}" +
		"h3{margin-bottom:.2em}h4{margin:1.4em 0 .3em;color:#0e7a6d}" +
		"hr{border:0;border-top:1px solid #ddd;margin:1.6em 0}ul{padding-left:1.4em}" +
		"a{color:#0e7a6d}blockquote{border-left:3px solid #e0a030;margin:1em 0;padding:.5em 1em;background:#fdf6e3;color:#7a5b00}" +
		".sources{color:#666;font-size:.85em}</style></head><body>")
	inline := func(s string) string {
		// Protect markdown links from inline escaping, then restore them
		// as anchors so URL characters (_, *, [) are never mangled.
		var links []string
		s = reLink.ReplaceAllStringFunc(s, func(m string) string {
			links = append(links, m)
			return fmt.Sprintf("\x00%d\x00", len(links)-1)
		})
		s = reBare.ReplaceAllStringFunc(s, func(m string) string {
			links = append(links, m)
			return fmt.Sprintf("\x00%d\x00", len(links)-1)
		})
		s = html.EscapeString(s)
		s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
		s = reItalic.ReplaceAllString(s, "<em>$1</em>")
		s = reStarIt.ReplaceAllString(s, "<em>$1</em>")
		for i, m := range links {
			var anchor string
			if sub := reLink.FindStringSubmatch(m); sub != nil {
				href := sub[2]
				if strings.HasPrefix(href, "/") && base != "" {
					href = base + href
				}
				if renderSafeHref(href) {
					anchor = `<a href="` + html.EscapeString(href) + `">` + html.EscapeString(sub[1]) + `</a>`
				} else {
					// a non-web scheme (javascript:, data:) renders as
					// its label text — never a clickable URL
					anchor = html.EscapeString(sub[1])
				}
			} else if sub := reBare.FindStringSubmatch(m); sub != nil {
				anchor = `<a href="` + html.EscapeString(sub[1]) + `">` + html.EscapeString(sub[1]) + `</a>`
			}
			s = strings.ReplaceAll(s, fmt.Sprintf("\x00%d\x00", i), anchor)
		}
		return s
	}
	inList := false
	inQuote := false
	closeList := func() {
		if inList {
			b.WriteString("</ul>")
			inList = false
		}
	}
	closeQuote := func() {
		if inQuote {
			b.WriteString("</blockquote>")
			inQuote = false
		}
	}
	lines := strings.Split(bodyMD, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimSpace(line)
		if !reList.MatchString(t) {
			closeList()
		}
		if !strings.HasPrefix(t, "> ") {
			closeQuote()
		}
		switch {
		case t == "":
		case strings.HasPrefix(t, "<!--"):
			// structural markers (item refs, /item boundaries) never render
		case t == EmbedFenceOpen:
			// embed block: the stored content is already sanitized at
			// write time; sanitize again on render as defense in depth
			// and emit it raw inside a marker div (KLOX-66 D3).
			var inner strings.Builder
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != EmbedFenceClose; i++ {
				inner.WriteString(lines[i])
				inner.WriteByte('\n')
			}
			b.WriteString(`<div class="embed">` + SanitizeEmbed(inner.String()) + "</div>")
		case reHR.MatchString(t):
			b.WriteString("<hr>")
		case reImage.MatchString(t):
			m := reImage.FindStringSubmatch(t)
			src := m[2]
			if !renderSafeImgSrc(src) {
				// non-web image schemes (javascript:, data:, file:)
				// render as their literal markdown — never a tag
				b.WriteString("<p>" + inline(t) + "</p>")
				break
			}
			if strings.HasPrefix(src, "/") && base != "" {
				src = base + src
			}
			b.WriteString(`<p class="img"><img src="` + html.EscapeString(src) +
				`" alt="` + html.EscapeString(m[1]) + `" style="max-width:100%;height:auto"></p>`)
		case reH4.MatchString(t):
			b.WriteString("<h4>" + inline(itemMarkerRe.ReplaceAllString(reH4.FindStringSubmatch(t)[1], "")) + "</h4>")
		case reH3.MatchString(t):
			b.WriteString("<h3>" + inline(itemMarkerRe.ReplaceAllString(reH3.FindStringSubmatch(t)[1], "")) + "</h3>")
		case reH2.MatchString(t):
			b.WriteString("<h2>" + inline(reH2.FindStringSubmatch(t)[1]) + "</h2>")
		case reH1.MatchString(t):
			b.WriteString("<h1>" + inline(reH1.FindStringSubmatch(t)[1]) + "</h1>")
		case reList.MatchString(t):
			if !inList {
				b.WriteString("<ul>")
				inList = true
			}
			b.WriteString("<li>" + inline(reList.FindStringSubmatch(t)[1]) + "</li>")
		case strings.HasPrefix(t, "> "):
			if !inQuote {
				b.WriteString("<blockquote>")
				inQuote = true
			} else {
				b.WriteString("<br>")
			}
			b.WriteString(inline(t[2:]))
		case strings.HasPrefix(t, "**Sources:**"):
			b.WriteString("<p class=\"sources\">" + inline(t) + "</p>")
		default:
			b.WriteString("<p>" + inline(t) + "</p>")
		}
	}
	closeList()
	closeQuote()
	b.WriteString("</body></html>")
	return b.String()
}
