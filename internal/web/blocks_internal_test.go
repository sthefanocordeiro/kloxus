package web

// DB-free unit tests for the block parser (KLOX-66): the section block
// list's order, the <!-- /item --> boundary that keeps an author text
// block from folding into an item's interpretation sentences, and the
// stored-line shapes blockLine writes and parseBlockLine reads.

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func testDoc(t *testing.T, body string) *issueDocument {
	t.Helper()
	iss := &store.Issue{ID: 1, Status: "drafted", BodyMD: body}
	pub := &store.Publication{Sections: []config.Section{
		{Title: "Top", Format: "briefing", Commentary: "none"},
	}}
	return parseIssueDocument(iss, pub, dcFromPairs(nil, nil))
}

func blockList(t *testing.T, doc *issueDocument) []docBlock {
	t.Helper()
	if len(doc.Sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(doc.Sections))
	}
	return doc.Sections[0].Blocks
}

func kinds(blocks []docBlock) []string {
	out := []string{}
	for _, b := range blocks {
		out = append(out, b.Kind)
	}
	return out
}

func TestParseBlocksOrder(t *testing.T) {
	body := "# Pub — Issue 1\n\n## Top\n\n" +
		"### Story one <!-- item 1 -->\n\n" +
		"A fact [1].\n\n" +
		"**Sources:** [1] [Outlet](/a/1)\n\n" +
		"<!-- /item -->\n\n" +
		"> An author note.\n\n" +
		"#### Also on my radar\n\n" +
		"---\n\n" +
		"- [Deep dive](https://example.com/x)\n"
	blocks := blockList(t, testDoc(t, body))
	want := []string{"item", "text", "heading", "divider", "link"}
	got := kinds(blocks)
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
	if blocks[1].Text != "An author note." || blocks[2].Text != "Also on my radar" ||
		blocks[4].Text != "Deep dive" || blocks[4].URL != "https://example.com/x" {
		t.Fatalf("block contents = %+v", blocks)
	}
}

// Without the marker a `> ` after an item is an interpretation
// sentence; with it the same line is a section-level text block and
// the item keeps only its own sentences.
func TestEndItemMarkerBoundary(t *testing.T) {
	body := "# Pub — Issue 1\n\n## Top\n\n" +
		"### Story one <!-- item 1 -->\n\n" +
		"A fact [1].\n\n" +
		"> The author's read inside the story.\n\n" +
		"<!-- /item -->\n\n" +
		"> A note for readers between stories.\n\n" +
		"### Story two <!-- item 2 -->\n\n" +
		"Another fact [1].\n"
	doc := testDoc(t, body)
	sec := doc.Sections[0]
	got := kinds(sec.Blocks)
	want := []string{"item", "text", "item"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if len(sec.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(sec.Items))
	}
	// the in-item `> ` stays an interpretation sentence of item 1
	if len(sec.Items[0].Sentences) != 2 || sec.Items[0].Sentences[1].Kind != "interpretation" {
		t.Fatalf("item1 sentences = %+v", sec.Items[0].Sentences)
	}
	if len(sec.Items[1].Sentences) != 1 {
		t.Fatalf("item2 sentences = %+v", sec.Items[1].Sentences)
	}
	if sec.Blocks[1].Text != "A note for readers between stories." {
		t.Fatalf("text block = %q", sec.Blocks[1].Text)
	}
}

// A `> ` line still inside the item (no marker) keeps legacy behavior:
// it parses as the item's interpretation, never a section block.
func TestTextWithoutMarkerStaysInterpretation(t *testing.T) {
	body := "# Pub — Issue 1\n\n## Top\n\n" +
		"### Story one <!-- item 1 -->\n\n" +
		"A fact [1].\n\n" +
		"> Trailing interpretation.\n"
	doc := testDoc(t, body)
	sec := doc.Sections[0]
	if got := kinds(sec.Blocks); len(got) != 1 || got[0] != "item" {
		t.Fatalf("kinds = %v, want [item]", got)
	}
	if len(sec.Items[0].Sentences) != 2 || sec.Items[0].Sentences[1].Kind != "interpretation" {
		t.Fatalf("sentences = %+v", sec.Items[0].Sentences)
	}
}

// blockLine/parseBlockLine round-trip every stored shape.
func TestBlockLineRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		kind, text, url string
	}{
		{"heading", "A title", ""},
		{"divider", "", ""},
		{"link", "Label", "https://example.com/x"},
		{"link", "", "https://example.com/bare"},
		{"text", "A note.", ""},
	} {
		line, ok := blockLine(tc.kind, tc.text, tc.url)
		if !ok {
			t.Fatalf("blockLine %v refused", tc)
		}
		blk, isBlock := parseBlockLine(line)
		if !isBlock {
			t.Fatalf("parseBlockLine did not read %q back", line)
		}
		if blk.Kind != tc.kind {
			t.Fatalf("kind = %q, want %q (line %q)", blk.Kind, tc.kind, line)
		}
	}
	// invalid content is refused, never stored half-formed — including
	// newline payloads that would splice a raw <klox-embed> fence into
	// the stored body (KLOX-66 D3 embed-allowlist bypass, QA on PR #53)
	for _, bad := range [][3]string{
		{"heading", " ", ""},
		{"heading", "title\n<klox-embed>\n<p>x</p>\n</klox-embed>", ""},
		{"link", "x", "not a url"},
		{"link", "x", "https://example.com/x\n<klox-embed>"},
		{"image", "alt\n<klox-embed>", "/m/1-ab"},
		{"image", "alt", "/m/1-ab\n<klox-embed>"},
		{"text", "", ""},
		{"text", "note\n<klox-embed>\n<p>x</p>\n</klox-embed>", ""},
		{"video", "x", ""},
	} {
		if line, ok := blockLine(bad[0], bad[1], bad[2]); ok {
			t.Fatalf("blockLine %v accepted %q", bad, line)
		}
	}
}

// `<!-- item N link -->` marks a pauta link-only (KLOX-66 D2): the
// parser surfaces it, and the stored item needs no sentences.
func TestLinkOnlyItemMarker(t *testing.T) {
	body := "# Pub — Issue 1\n\n## Top\n\n" +
		"### Story one <!-- item 1 link -->\n\n" +
		"**Sources:** [1] [Outlet](/a/1)\n\n" +
		"### Story two <!-- item 2 -->\n\n" +
		"A fact [1].\n"
	doc := testDoc(t, body)
	sec := doc.Sections[0]
	if len(sec.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(sec.Items))
	}
	if !sec.Items[0].LinkOnly {
		t.Fatal("item 1 link marker not parsed")
	}
	if sec.Items[1].LinkOnly {
		t.Fatal("item 2 wrongly marked link-only")
	}
	if got := kinds(sec.Blocks); len(got) != 2 || got[0] != "item" || got[1] != "item" {
		t.Fatalf("kinds = %v, want [item item]", got)
	}
}

// Image and embed blocks parse at section level; the embed's stored
// text is what the fence holds (already sanitized on write).
func TestImageAndEmbedBlocks(t *testing.T) {
	body := "# Pub — Issue 1\n\n## Top\n\n" +
		"### Story one <!-- item 1 -->\n\n" +
		"A fact [1].\n\n" +
		"<!-- /item -->\n\n" +
		"![Cover](/m/12-ab34)\n\n" +
		"<klox-embed>\n" +
		"<p>embedded <strong>thing</strong></p>\n" +
		"</klox-embed>\n"
	blocks := blockList(t, testDoc(t, body))
	want := []string{"item", "image", "embed"}
	got := kinds(blocks)
	if len(got) != len(want) || got[0] != "item" || got[1] != "image" || got[2] != "embed" {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if blocks[1].URL != "/m/12-ab34" || blocks[1].Text != "Cover" {
		t.Fatalf("image block = %+v", blocks[1])
	}
	if blocks[2].Text != "<p>embedded <strong>thing</strong></p>" {
		t.Fatalf("embed block = %+v", blocks[2])
	}
}

// An unclosed embed fence is stray markup — its line is skipped, no
// embed block appears.
func TestUnclosedEmbedFenceIgnored(t *testing.T) {
	body := "# Pub — Issue 1\n\n## Top\n\n" +
		"<klox-embed>\n<p>oops\n"
	blocks := blockList(t, testDoc(t, body))
	for _, b := range blocks {
		if b.Kind == "embed" {
			t.Fatalf("unclosed fence produced embed block: %+v", b)
		}
	}
}

// A fence that bypassed the write path is re-sanitized when the
// document is parsed — the composer canvas injects block text raw, so
// stored active markup must never survive the read (KLOX-66 D3, QA
// FAIL on PR #53).
func TestEmbedBlockResanitizedAtParse(t *testing.T) {
	body := "# Pub — Issue 1\n\n## Top\n\n" +
		"<klox-embed>\n" +
		"<img src=\"x\" onerror=\"alert(1)\">\n" +
		"<iframe src=\"https://evil.example/x\"></iframe>\n" +
		"<script>alert(2)</script>\n" +
		"<iframe src=\"https://www.youtube.com/embed/abc\"></iframe>\n" +
		"</klox-embed>\n"
	blocks := blockList(t, testDoc(t, body))
	if len(blocks) != 1 || blocks[0].Kind != "embed" {
		t.Fatalf("blocks = %+v", blocks)
	}
	text := blocks[0].Text
	for _, bad := range []string{"onerror", "<script", "evil.example", "alert"} {
		if strings.Contains(text, bad) {
			t.Fatalf("embed text leaked %q: %s", bad, text)
		}
	}
	if !strings.Contains(text, "youtube.com/embed/abc") {
		t.Fatalf("allowed iframe dropped: %s", text)
	}
}

// Author text that reaches the sentence write paths may not carry
// document markup — a newline or a fence would splice raw lines into
// the stored body (KLOX-66 D3).
func TestAuthorTextOKRejectsMarkup(t *testing.T) {
	for _, bad := range []string{
		"line one\nline two",
		"trailing\r\nnewline",
		"plain <!-- marker --> text",
		"<klox-embed>\npayload",
		"prefix <klox-embed",
	} {
		if authorTextOK(bad) {
			t.Fatalf("authorTextOK accepted %q", bad)
		}
	}
	for _, ok := range []string{
		"A normal sentence.",
		"Inline <b>html</b> is harmless as sentence text.",
		"a < b and c > d",
	} {
		if !authorTextOK(ok) {
			t.Fatalf("authorTextOK rejected %q", ok)
		}
	}
}
