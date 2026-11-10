// Package delivery sends or exports issues. Demo mode writes files to
// the outbox; real mode adds Resend (milestone 4).
package delivery

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Mailer delivers an issue to readers.
type Mailer interface {
	Send(subject, bodyMD, bodyHTML string, to []string) (string, error)
}

// Outbox "sends" by writing .eml and .html files under dir (demo mode).
type Outbox struct{ Dir string }

// Send implements Mailer.
func (o Outbox) Send(subject, bodyMD, bodyHTML string, to []string) (string, error) {
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	eml := fmt.Sprintf("Subject: %s\r\nTo: %s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s",
		subject, strings.Join(to, ", "), bodyHTML)
	path := filepath.Join(o.Dir, "issue-"+stamp+".eml")
	if err := os.WriteFile(path, []byte(eml), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(o.Dir, "issue-"+stamp+".html"), []byte(bodyHTML), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

var (
	reH3     = regexp.MustCompile(`^### (.+?)\s*<!--.*?-->\s*$`)
	reH2     = regexp.MustCompile(`^## (.+)$`)
	reH1     = regexp.MustCompile(`^# (.+)$`)
	reBold   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic = regexp.MustCompile(`_(.+?)_`)
	reLink   = regexp.MustCompile(`\[(https?://[^\]]+)\]`)
)

// RenderHTML converts the issue markdown subset to a simple HTML document.
func RenderHTML(title, bodyMD string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) +
		"</title><style>body{font-family:system-ui,sans-serif;max-width:44em;margin:2em auto;padding:0 1em;line-height:1.55;color:#1a1a1a}" +
		"h1{border-bottom:2px solid #333;padding-bottom:.3em}h2{margin-top:2em;border-bottom:1px solid #ccc}h3{margin-bottom:.2em}" +
		"blockquote{border-left:3px solid #e0a030;margin:1em 0;padding:.5em 1em;background:#fdf6e3;color:#7a5b00}" +
		".sources{color:#666;font-size:.85em}</style></head><body>")
	for _, line := range strings.Split(bodyMD, "\n") {
		t := strings.TrimSpace(line)
		inline := func(s string) string {
			s = html.EscapeString(s)
			s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
			s = reItalic.ReplaceAllString(s, "<em>$1</em>")
			s = reLink.ReplaceAllString(s, `<a href="$1">$1</a>`)
			return s
		}
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
