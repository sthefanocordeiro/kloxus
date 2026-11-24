package web

import (
	"strings"
	"testing"
)

func renditionFixture() *issueDocument {
	return &issueDocument{
		IssueID: 7, Number: 3, Subject: "Pix & cripto: a semana",
		Sections: []docSection{{
			Title:      "Regulação",
			Commentary: docCommentary{Mode: "optional", Text: "Semana intensa."},
			Items: []docItem{{
				Ref: 1, Headline: "BC muda regra",
				Sentences: []docSentence{
					{Text: "O BC publicou a norma.", Kind: "fact", Citations: []int{1}},
					{Text: "Vale desde segunda.", Kind: "fact", Citations: []int{1, 2}},
					{Text: "Para mim, é um avanço.", Kind: "interpretation"},
				},
				Sources: []docSource{
					{Ref: 1, Name: "Valor", URL: "https://valor.example/a?x=(1)"},
					{Ref: 2, Name: "Folha", URL: "https://folha.example/b"},
				},
			}},
			Blocks: []docBlock{
				{Kind: "text", Text: "Abertura."},
				{Kind: "item", Ref: 1},
			},
		}},
	}
}

func TestRenditionTextTemplate(t *testing.T) {
	text, _, _, _ := renditionBodies(renditionFixture(), FmtLinkedIn)
	want := "Semana intensa.\n\nAbertura.\n\n" +
		"BC muda regra\nhttps://valor.example/a?x=(1)\n\n" +
		"O BC publicou a norma. Vale desde segunda.\n\nPara mim, é um avanço.\n"
	if text != want {
		t.Fatalf("text template:\n%q\nwant\n%q", text, want)
	}
}

func TestRenditionHTMLAndMarkdownTemplates(t *testing.T) {
	_, h, _, _ := renditionBodies(renditionFixture(), FmtHTML)
	for _, frag := range []string{
		"<h2>Regulação</h2>", `<h3><a href="https://valor.example/a?x=(1)">BC muda regra</a></h3>`,
		"<p>O BC publicou a norma. Vale desde segunda.</p>",
	} {
		if !strings.Contains(h, frag) {
			t.Errorf("html missing %q in:\n%s", frag, h)
		}
	}
	if strings.Contains(h, "<h1>") || strings.Contains(h, "Fontes") || strings.Contains(h, "[1]") {
		t.Errorf("html must have no auto title, sources list or [n]:\n%s", h)
	}
	_, _, md, _ := renditionBodies(renditionFixture(), FmtMarkdown)
	for _, frag := range []string{
		"## Regulação", "### [BC muda regra](https://valor.example/a?x=%281%29)",
	} {
		if !strings.Contains(md, frag) {
			t.Errorf("markdown missing %q in:\n%s", frag, md)
		}
	}
	if strings.HasPrefix(md, "# ") || strings.Contains(md, "Fontes") || strings.Contains(md, "peça") {
		t.Errorf("markdown must have no auto title or sources list:\n%s", md)
	}
}

func TestRenditionNoRawInterpretationMarkup(t *testing.T) {
	doc := renditionFixture()
	doc.Sections[0].Items[0].Sentences[2].Text = "> _Interpretação: > _Interpretação: Para mim, é um avanço.__"
	for _, f := range []string{FmtLinkedIn, FmtX, FmtHTML, FmtMarkdown} {
		tx, h, m, _ := renditionBodies(doc, f)
		out := tx + h + m
		if strings.Contains(out, "Interpretação") || strings.Contains(out, "&gt;") ||
			strings.Contains(out, "> ") || strings.Contains(out, `\_`) {
			t.Errorf("%s leaks interpretation markup: %q", f, out)
		}
		if !strings.Contains(out, "Para mim, é um avanço.") {
			t.Errorf("%s lost interpretation text: %q", f, out)
		}
	}
}

func TestRenditionXSplitsPerStory(t *testing.T) {
	doc := &issueDocument{Sections: []docSection{{
		Title: "Posts",
		Items: []docItem{
			{Ref: 1, Headline: "Um", Sentences: []docSentence{{Text: "Texto um.", Kind: "fact"}},
				Sources: []docSource{{Ref: 1, URL: "https://a.example/um"}}},
			{Ref: 2, Headline: "Dois", Sentences: []docSentence{{Text: strings.Repeat("palavra ", 60), Kind: "fact"}},
				Sources: []docSource{{Ref: 1, URL: "https://b.example/dois"}}},
		},
		Blocks: []docBlock{{Kind: "item", Ref: 1}, {Kind: "item", Ref: 2}},
	}}}
	x, _, _, _ := renditionBodies(doc, FmtX)
	posts := strings.Split(strings.TrimSpace(x), "\n\n---\n\n")
	if len(posts) < 3 {
		t.Fatalf("want one post for story 1 and a split for story 2, got %d:\n%s", len(posts), x)
	}
	if posts[0] != "Um\nhttps://a.example/um\n\nTexto um." {
		t.Errorf("first post: %q", posts[0])
	}
	if !strings.HasPrefix(posts[1], "Dois\nhttps://b.example/dois") {
		t.Errorf("second story must open with title+URL: %q", posts[1])
	}
	for i, p := range posts {
		if xLen(p) > xLimit {
			t.Errorf("post %d is %d > %d: %q", i, xLen(p), xLimit, p)
		}
	}
}

func TestRenditionFormatsUseOwnColumnAndHashTracksBase(t *testing.T) {
	doc := renditionFixture()
	tx, h, m, hash1 := renditionBodies(doc, FmtX)
	if tx == "" || h != "" || m != "" {
		t.Fatalf("x fork must fill only body_text: %q %q %q", tx, h, m)
	}
	_, _, _, hash2 := renditionBodies(doc, FmtMarkdown)
	if hash1 != hash2 {
		t.Fatal("hash must not depend on the chosen format")
	}
	doc.Sections[0].Items[0].Sentences[0].Text = "O BC revisou a norma."
	_, _, _, hash3 := renditionBodies(doc, FmtMarkdown)
	if hash3 == hash1 {
		t.Fatal("hash must change when the piece text changes")
	}
}
