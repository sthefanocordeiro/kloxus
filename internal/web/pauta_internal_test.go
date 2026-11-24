package web

import (
	"reflect"
	"testing"
)

func TestPautaLines(t *testing.T) {
	got := pautaLines("# Titulo\r\n\n> citação [1] aqui\n  - item <!-- x -->\n")
	want := []string{"Titulo", "", "citação (1) aqui", "item  x -->"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pautaLines = %q, want %q", got, want)
	}
	if len(pautaLines("  \n\n")) != 0 {
		t.Fatal("blank text must yield no lines")
	}
	// interior blank lines are the author's paragraph breaks — kept
	if got := pautaLines("um\n\n\ndois\n"); !reflect.DeepEqual(got, []string{"um", "", "", "dois"}) {
		t.Fatalf("pautaLines blanks = %q", got)
	}
}

// KLOX-181: the item's Text field is the stored fact lines verbatim —
// the '. ' the sentence split eats stays, [n] markers go, interior
// blank lines live, the sources list and structural padding stay out.
func TestItemTextVerbatim(t *testing.T) {
	body := "# Pub — Issue 1\n\n## Top\n\n" +
		"### Story <!-- item 1 -->\n\n" +
		"Com ressalvas. Vale ler os detalhes. [1]\n" +
		"linha dois 🎯\n\n" +
		"outro parágrafo [1]\n\n" +
		"**Sources:** [1] [Outlet](/a/1)\n"
	doc := testDoc(t, body)
	it := doc.Sections[0].Items[0]
	want := "Com ressalvas. Vale ler os detalhes.\nlinha dois 🎯\n\noutro parágrafo"
	if it.Text != want {
		t.Fatalf("text = %q, want %q", it.Text, want)
	}
	// mid-line citations leave no trace in the text either
	doc = testDoc(t, "## Top\n\n### H <!-- item 1 -->\n\nUm fato [1] e outro [2] aqui.\n")
	if got := doc.Sections[0].Items[0].Text; got != "Um fato e outro aqui." {
		t.Fatalf("mid-line cites text = %q", got)
	}
}
