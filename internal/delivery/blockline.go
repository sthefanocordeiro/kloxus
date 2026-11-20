package delivery

// Composer block storage (KLOX-66) rendered to its stored markdown
// line — shared by the edition block ops (internal/web) and the
// newsletter-template materializer (internal/pipeline, KLOX-72), so
// every writer of block syntax validates against the same rules.

import "strings"

// BlockSafeURL reports whether u can be stored as a block URL —
// http(s) links out, or an uploaded image's /m/ path.
func BlockSafeURL(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") ||
		strings.HasPrefix(u, "/m/")
}

// unsafeDocLine reports whether a line of author-supplied text would
// become structural markup if stored as its own body line: HTML
// comments close the running item and <klox-embed opens a raw-HTML
// fence — both are document structure, never author text (KLOX-66 D3).
func unsafeDocLine(t string) bool {
	return strings.HasPrefix(t, "<!--") || strings.HasPrefix(t, "<klox-embed")
}

// BlockLine renders a block's stored markdown line — the only shapes
// the document parser accepts back. Single-line kinds refuse CR/LF: a
// newline in the payload would splice structural markup into the
// stored body (KLOX-66 D3).
func BlockLine(kind, text, url string) (string, bool) {
	t := strings.TrimSpace(text)
	switch kind {
	case "heading":
		if t == "" || strings.Contains(t, "<!--") || strings.ContainsAny(t, "\r\n") {
			return "", false
		}
		return "#### " + t, true
	case "divider":
		return "---", true
	case "link":
		u := strings.TrimSpace(url)
		if u == "" || strings.ContainsAny(u, " \t()\r\n") || !BlockSafeURL(u) {
			return "", false
		}
		if t == "" || strings.ContainsAny(t, "[]\r\n") {
			t = u
		}
		return "- [" + t + "](" + u + ")", true
	case "image":
		u := strings.TrimSpace(url)
		if u == "" || strings.ContainsAny(u, " \t()\r\n") || !BlockSafeURL(u) {
			return "", false
		}
		if strings.ContainsAny(t, "[]\r\n") {
			return "", false
		}
		return "![" + t + "](" + u + ")", true
	case "embed":
		// the stored body keeps the sanitized form only — the raw
		// paste never reaches a reader surface (KLOX-66 D3)
		clean := SanitizeEmbed(t)
		if clean == "" {
			return "", false
		}
		return EmbedFenceOpen + "\n" + clean + "\n" + EmbedFenceClose, true
	case "text":
		if t == "" {
			return "", false
		}
		var b strings.Builder
		for _, ln := range strings.Split(t, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" {
				continue
			}
			if unsafeDocLine(ln) {
				return "", false
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("> " + ln)
		}
		if b.Len() == 0 {
			return "", false
		}
		return b.String(), true
	}
	return "", false
}
