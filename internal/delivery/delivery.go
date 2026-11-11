// Package delivery renders issues for readers and records simulated
// outgoing mail in the workspace outbox. Demo mode stores every message
// in the outbox_emails table; real delivery lands with milestone 4.
package delivery

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"

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
	id, err := o.DB.SaveOutboxEmail(ctx, kind, strings.Join(to, ", "), subject, bodyMD, bodyHTML)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(id), nil
}

// EML renders a stored outbox row as a downloadable .eml message.
func EML(e *store.OutboxEmail) string {
	return fmt.Sprintf("Subject: %s\r\nTo: %s\r\nDate: %s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s",
		e.Subject, e.To, e.CreatedAt, e.BodyHTML)
}

// aiDisclosure returns the disclosure line for approved output, or "".
func aiDisclosure(pub *store.Publication) string {
	switch pub.AIDisclosure {
	case "short":
		return "_Prepared with AI assistance and reviewed by the editor._"
	case "detailed":
		return "_This issue was prepared by an AI editorial assistant from the sources listed below; every fact links to its source. All commentary and final review are the editor's._"
	}
	return ""
}

var (
	itemMarkerRe = regexp.MustCompile(`\s*<!--\s*item \d+\s*-->`)
	relLinkRe    = regexp.MustCompile(`\]\((/a/\d+)\)`)
	sectionRe    = regexp.MustCompile(`^## (.+)$`)
)

// ReaderBody renders the reader-facing markdown for an issue:
//   - the AI disclosure is prepended only once the issue is approved/sent;
//   - the editor's commentary is appended at the end of its section,
//     clearly attributed;
//   - internal item markers are stripped;
//   - relative /a/<id> source links become absolute when base is given.
func ReaderBody(iss *store.Issue, pub *store.Publication, base string) string {
	lines := strings.Split(iss.BodyMD, "\n")
	var out []string
	inFirstSection := false
	flush := func(section string) {
		if section == "" {
			return
		}
		if c := strings.TrimSpace(iss.Commentary[section]); c != "" {
			out = append(out, "> **Comentário do editor:** "+c, "")
		}
	}
	current := ""
	for _, line := range lines {
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			if inFirstSection {
				flush(current)
			}
			inFirstSection = true
			current = m[1]
		}
		out = append(out, itemMarkerRe.ReplaceAllString(line, ""))
	}
	flush(current)
	body := strings.Join(out, "\n")
	if iss.Status == "approved" || iss.Status == "sent" {
		if d := aiDisclosure(pub); d != "" {
			if i := strings.Index(body, "\n"); i >= 0 {
				body = body[:i+1] + "\n" + d + "\n" + body[i+1:]
			} else {
				body = d + "\n\n" + body
			}
		}
	}
	if base != "" {
		body = relLinkRe.ReplaceAllString(body, "]("+base+"$1)")
	}
	return body
}

var (
	reH3     = regexp.MustCompile(`^### (.+?)\s*$`)
	reH2     = regexp.MustCompile(`^## (.+)$`)
	reH1     = regexp.MustCompile(`^# (.+)$`)
	reBold   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic = regexp.MustCompile(`_(.+?)_`)
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	reBare   = regexp.MustCompile(`\[(https?://[^\]]+)\]`)
)

// RenderHTML converts the issue markdown subset to a simple HTML
// document. Markdown links [text](url) become <a href>; a bare [url]
// becomes a self-linked anchor; base absolutizes root-relative links.
func RenderHTML(title, bodyMD, base string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) +
		"</title><style>body{font-family:system-ui,sans-serif;max-width:44em;margin:2em auto;padding:0 1em;line-height:1.55;color:#1a1a1a}" +
		"h1{border-bottom:2px solid #333;padding-bottom:.3em}h2{margin-top:2em;border-bottom:1px solid #ccc}h3{margin-bottom:.2em}" +
		"a{color:#2d5a8a}blockquote{border-left:3px solid #e0a030;margin:1em 0;padding:.5em 1em;background:#fdf6e3;color:#7a5b00}" +
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
		for i, m := range links {
			var anchor string
			if sub := reLink.FindStringSubmatch(m); sub != nil {
				href := sub[2]
				if strings.HasPrefix(href, "/") && base != "" {
					href = base + href
				}
				anchor = `<a href="` + html.EscapeString(href) + `">` + html.EscapeString(sub[1]) + `</a>`
			} else if sub := reBare.FindStringSubmatch(m); sub != nil {
				anchor = `<a href="` + html.EscapeString(sub[1]) + `">` + html.EscapeString(sub[1]) + `</a>`
			}
			s = strings.ReplaceAll(s, fmt.Sprintf("\x00%d\x00", i), anchor)
		}
		return s
	}
	for _, line := range strings.Split(bodyMD, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case reH3.MatchString(t):
			b.WriteString("<h3>" + inline(reH3.FindStringSubmatch(t)[1]) + "</h3>")
		case reH2.MatchString(t):
			b.WriteString("<h2>" + inline(reH2.FindStringSubmatch(t)[1]) + "</h2>")
		case reH1.MatchString(t):
			b.WriteString("<h1>" + inline(reH1.FindStringSubmatch(t)[1]) + "</h1>")
		case strings.HasPrefix(t, "> "):
			b.WriteString("<blockquote>" + inline(t[2:]) + "</blockquote>")
		case strings.HasPrefix(t, "**Sources:**"):
			b.WriteString("<p class=\"sources\">" + inline(t) + "</p>")
		default:
			b.WriteString("<p>" + inline(t) + "</p>")
		}
	}
	b.WriteString("</body></html>")
	return b.String()
}
