package web

import (
	"reflect"
	"testing"
	"time"
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

// TestPautaMailDueAtSaoPauloWindow (KLOX-268): the 07:00 schedule is
// evaluated in America/Sao_Paulo and has only a short catch-up grace —
// 10:00 UTC is 07:00 SP, while the reported 01:15 UTC next-day deploy
// tick (22:15 SP) is outside it.
func TestPautaMailDueAtSaoPauloWindow(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"before SP seven", time.Date(2026, 11, 24, 9, 59, 59, 0, time.UTC), false},
		{"exact SP seven", time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC), true},
		{"inside grace", time.Date(2026, 11, 24, 10, 29, 59, 0, time.UTC), true},
		{"grace end excluded", time.Date(2026, 11, 24, 10, 30, 0, 0, time.UTC), false},
		{"evening catchup refused", time.Date(2026, 11, 25, 1, 15, 0, 0, time.UTC), false},
	}
	for _, tc := range cases {
		if got := pautaMailDueAt(tc.at); got != tc.want {
			t.Errorf("%s: pautaMailDueAt(%s) = %v, want %v", tc.name, tc.at, got, tc.want)
		}
	}
}
