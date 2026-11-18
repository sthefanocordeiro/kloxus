package delivery

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// TestReaderBodyNormalizesCitationPlacement: "urbana. [1]" renders as
// "urbana [1]." in every reader-facing output.
func TestReaderBodyNormalizesCitationPlacement(t *testing.T) {
	iss := &store.Issue{
		Status:  "approved",
		Subject: "Edição 1",
		BodyMD:  "# Pub — issue 1\n\n## S\n\n### H <!-- item 1 -->\n\nO custo segue três vezes o da banda larga urbana. [1] Outra frase aqui.",
	}
	pub := &store.Publication{}
	md := ReaderBody(iss, pub, "", false)
	if strings.Contains(md, "urbana. [1]") {
		t.Fatalf("citation not normalised:\n%s", md)
	}
	if !strings.Contains(md, "urbana [1].") {
		t.Fatalf("expected 'urbana [1].':\n%s", md)
	}
	// already-correct placement is untouched
	if !strings.Contains(md, "Outra frase aqui.") {
		t.Fatal("unrelated sentence mangled")
	}
}

// TestReaderBodyDisclosureFollowsAIUse: an approved issue shows the
// configured disclosure only when a model actually ran for it — a
// fully deterministic draft carries no AI claim (KLOX-56).
func TestReaderBodyDisclosureFollowsAIUse(t *testing.T) {
	pub := &store.Publication{AIDisclosure: "short", Language: "en"}
	iss := &store.Issue{Status: "approved", BodyMD: "# Pub — issue 1\n\n## S\n\nText."}

	with := ReaderBody(iss, pub, "", true)
	if !strings.Contains(with, "Prepared with AI assistance") {
		t.Fatalf("aiUsed=true: disclosure missing:\n%s", with)
	}
	without := ReaderBody(iss, pub, "", false)
	if strings.Contains(without, "AI assistance") {
		t.Fatalf("aiUsed=false: disclosure leaked:\n%s", without)
	}
	// a draft never discloses, AI run or not
	draft := &store.Issue{Status: "draft", BodyMD: iss.BodyMD}
	if md := ReaderBody(draft, pub, "", true); strings.Contains(md, "AI assistance") {
		t.Fatalf("draft: disclosure leaked:\n%s", md)
	}
	// pt publications disclose in pt (KLOX-56)
	ptPub := &store.Publication{AIDisclosure: "short", Language: "pt-BR"}
	if md := ReaderBody(iss, ptPub, "", true); !strings.Contains(md, "Preparado com auxílio de IA") {
		t.Fatalf("pt disclosure missing:\n%s", md)
	}
}

// TestReaderBodyFooterAndBlankRuns: the sources footer earns a full
// stop and marker stripping leaves no blank runs (KLOX-56).
func TestReaderBodyFooterAndBlankRuns(t *testing.T) {
	iss := &store.Issue{
		Status: "draft",
		BodyMD: "# Pub — issue 1\n\n## S\n\nText. [1]\n\n\n\n**Sources:** [1] Outlet — https://x.test  ",
	}
	md := ReaderBody(iss, &store.Publication{}, "", false)
	if strings.Contains(md, "\n\n\n") {
		t.Fatalf("blank run survived:\n%q", md)
	}
	if !strings.Contains(md, "https://x.test.") {
		t.Fatalf("sources footer lacks its full stop:\n%s", md)
	}
	if strings.Contains(md, "  \n") || strings.HasSuffix(md, "  ") {
		t.Fatalf("trailing spaces survived:\n%q", md)
	}
}

// Structural markers never reach a reader surface: `<!-- item -->`
// markers, `<!-- /item -->` boundaries and the embed fences are
// document structure, not content (KLOX-66 D3).
func TestRenderHTMLStripsStructuralMarkers(t *testing.T) {
	body := "# Pub — issue 1\n\n## S\n\n" +
		"### H <!-- item 1 link -->\n\n" +
		"**Sources:** [1] [O](/a/1)\n\n" +
		"<!-- /item -->\n\n" +
		"> A note.\n"
	out := RenderHTML("T", body, "")
	for _, bad := range []string{"<!--", "item 1", "klox-embed"} {
		if strings.Contains(out, bad) {
			t.Fatalf("structural marker %q leaked:\n%s", bad, out)
		}
	}
}

func TestRenderHTMLEmbedSanitizedAgain(t *testing.T) {
	body := "# Pub — issue 1\n\n## S\n\n" +
		"<klox-embed>\n" +
		"<p>ok <script>alert(1)</script></p>\n" +
		"</klox-embed>\n"
	out := RenderHTML("T", body, "")
	if strings.Contains(out, "<script") || !strings.Contains(out, "<p>ok") {
		t.Fatalf("embed render wrong:\n%s", out)
	}
}

// The reader-page renderer injects fence content raw, so ReaderBody
// re-sanitizes it on the way out: markup that bypassed the write path
// (the QA-reproduced <klox-embed> splice on PR #53) still reaches no
// reader surface (KLOX-66 D3).
func TestReaderBodyResanitizesEmbed(t *testing.T) {
	iss := &store.Issue{
		Status: "approved",
		BodyMD: "# Pub — issue 1\n\n## S\n\n" +
			"<klox-embed>\n" +
			"<img src=\"x\" onerror=\"alert(1)\">\n" +
			"<iframe src=\"https://evil.example/x\"></iframe>\n" +
			"</klox-embed>\n\n" +
			"<klox-embed>\n" +
			"<iframe src=\"https://www.youtube.com/embed/abc\"></iframe>\n" +
			"</klox-embed>\n",
	}
	md := ReaderBody(iss, &store.Publication{}, "", false)
	for _, bad := range []string{"onerror", "evil.example", "alert"} {
		if strings.Contains(md, bad) {
			t.Fatalf("reader body leaked %q:\n%s", bad, md)
		}
	}
	if !strings.Contains(md, "youtube.com/embed/abc") {
		t.Fatalf("allowed iframe dropped:\n%s", md)
	}
	if strings.Count(md, "<klox-embed>") != 2 {
		t.Fatalf("fences not preserved:\n%s", md)
	}
}

// Non-web schemes never become live <img>/<a> output (KLOX-66 D3
// review note): a stored ![x](javascript:…) or [x](data:…) renders as
// plain text, never a tag.
func TestRenderHTMLUnsafeSchemes(t *testing.T) {
	body := "# Pub — issue 1\n\n## S\n\n" +
		"![x](javascript:alert(1))\n\n" +
		"[click](javascript:alert(1))\n\n" +
		"![d](data:text/html;base64,PGI+eDwvYj4)\n"
	out := RenderHTML("T", body, "")
	for _, bad := range []string{"javascript:", "data:text/html", "<img src=\"javascript"} {
		if strings.Contains(out, bad) {
			t.Fatalf("unsafe scheme leaked %q:\n%s", bad, out)
		}
	}
	// good schemes still render
	body = "# Pub — issue 1\n\n## S\n\n![Cover](/m/12-ab)\n\n[ok](https://example.com/x)\n"
	out = RenderHTML("T", body, "")
	if !strings.Contains(out, `<img src="/m/12-ab"`) {
		t.Fatalf("media image dropped:\n%s", out)
	}
	if !strings.Contains(out, `href="https://example.com/x"`) {
		t.Fatalf("http link dropped:\n%s", out)
	}
}

func TestRenderHTMLImage(t *testing.T) {
	body := "# Pub — issue 1\n\n## S\n\n![Cover](/m/12-ab)\n"
	out := RenderHTML("T", body, "")
	if !strings.Contains(out, `<img`) || !strings.Contains(out, `/m/12-ab`) {
		t.Fatalf("image not rendered:\n%s", out)
	}
}
