package delivery

// Sanitizer tests (KLOX-66 D3): the allowlist boundary is the only
// thing standing between an author paste and every reader surface —
// pin the drop/keep/unwrap behavior.

import (
	"strings"
	"testing"
)

func TestSanitizeEmbedDropsActiveContent(t *testing.T) {
	for _, in := range []string{
		`<script>alert(1)</script>`,
		`<p onclick="x()">hi</p>`,
		`<form><input type="text"></form>`,
		`<svg><script>alert(1)</script></svg>`,
		`<iframe src="https://evil.example/x"></iframe>`,
		`<iframe src="javascript:alert(1)"></iframe>`,
		`<a href="javascript:alert(1)">x</a>`,
		`<img src="javascript:alert(1)">`,
	} {
		out := SanitizeEmbed(in)
		if strings.Contains(out, "script") || strings.Contains(out, "onclick") ||
			strings.Contains(out, "<form") || strings.Contains(out, "<input") ||
			strings.Contains(out, "<svg") || strings.Contains(out, "evil.example") ||
			strings.Contains(out, "javascript:") {
			t.Fatalf("SanitizeEmbed(%q) leaked %q", in, out)
		}
	}
}

func TestSanitizeEmbedKeepsProviderIframes(t *testing.T) {
	out := SanitizeEmbed(`<iframe src="https://www.youtube.com/embed/abc" width="560" height="315"></iframe>`)
	if !strings.Contains(out, `src="https://www.youtube.com/embed/abc"`) {
		t.Fatalf("youtube iframe dropped: %q", out)
	}
	if !strings.Contains(out, `referrerpolicy=`) || !strings.Contains(out, `loading="lazy"`) {
		t.Fatalf("iframe safety attrs missing: %q", out)
	}
	out = SanitizeEmbed(`<iframe src="https://player.vimeo.com/video/1"></iframe>`)
	if !strings.Contains(out, "player.vimeo.com") {
		t.Fatalf("vimeo iframe dropped: %q", out)
	}
}

func TestSanitizeEmbedUnwrapsUnknownTags(t *testing.T) {
	out := SanitizeEmbed(`<marquee><strong>keep me</strong></marquee>`)
	if strings.Contains(out, "marquee") || !strings.Contains(out, "<strong>keep me</strong>") {
		t.Fatalf("unwrap wrong: %q", out)
	}
}

func TestSanitizeEmbedEscapesText(t *testing.T) {
	out := SanitizeEmbed(`<p>a &amp; b</p>`)
	if !strings.Contains(out, "a &amp; b") || strings.Contains(out, "& b") {
		t.Fatalf("escape wrong: %q", out)
	}
}

func TestSanitizeEmbedLinkAttrs(t *testing.T) {
	out := SanitizeEmbed(`<a href="https://example.com/x" onclick="x()">go</a>`)
	if !strings.Contains(out, `href="https://example.com/x"`) ||
		!strings.Contains(out, `rel="noopener noreferrer"`) ||
		strings.Contains(out, "onclick") {
		t.Fatalf("link attrs wrong: %q", out)
	}
}

func TestSanitizeEmbedImages(t *testing.T) {
	out := SanitizeEmbed(`<img src="/m/12-ab" alt="pic" width="100" height="x">`)
	if !strings.Contains(out, `src="/m/12-ab"`) || !strings.Contains(out, `alt="pic"`) ||
		!strings.Contains(out, `width="100"`) || strings.Contains(out, `height`) {
		t.Fatalf("img attrs wrong: %q", out)
	}
}
