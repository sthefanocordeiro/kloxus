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
	want := "Pix & cripto: a semana\n\nSemana intensa.\n\nAbertura.\n\n" +
		"O BC publicou a norma. Vale desde segunda. [1] [2]\n\nPara mim, é um avanço.\n\n" +
		"Fontes:\n[1] Valor — https://valor.example/a?x=(1)\n[2] Folha — https://folha.example/b\n"
	if text != want {
		t.Fatalf("text template:\n%q\nwant\n%q", text, want)
	}
}

func TestRenditionHTMLAndMarkdownTemplates(t *testing.T) {
	_, h, _, _ := renditionBodies(renditionFixture(), FmtHTML)
	for _, frag := range []string{
		"<h1>Pix &amp; cripto: a semana</h1>", "<h2>Regulação</h2>", "<h3>BC muda regra</h3>",
		`<a href="https://folha.example/b">[2]</a>`, "<h2>Fontes</h2>", "<ol>",
	} {
		if !strings.Contains(h, frag) {
			t.Errorf("html missing %q in:\n%s", frag, h)
		}
	}
	_, _, md, _ := renditionBodies(renditionFixture(), FmtMarkdown)
	for _, frag := range []string{
		"# Pix & cripto: a semana", "## Regulação", "### BC muda regra",
		`[\[1\]](https://valor.example/a?x=%281%29)`, "## Fontes", "2. [Folha](https://folha.example/b)",
	} {
		if !strings.Contains(md, frag) {
			t.Errorf("markdown missing %q in:\n%s", frag, md)
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
