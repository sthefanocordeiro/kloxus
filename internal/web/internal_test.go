package web

import (
	"strings"
	"testing"
)

// TestRemoveSentenceDropsCitationMarker: removing a flagged sentence
// must take its trailing "[n]" too — no orphaned citation left behind.
func TestRemoveSentenceDropsCitationMarker(t *testing.T) {
	body := "### H <!-- item 1 -->\n\nO custo segue três vezes o da banda larga urbana. [1] Outra afirmação sem fonte."
	out := removeSentence(body, "O custo segue três vezes o da banda larga urbana. [1]")
	if strings.Contains(out, "[1]") {
		t.Fatalf("citation marker left behind: %q", out)
	}
	if !strings.Contains(out, "Outra afirmação sem fonte.") {
		t.Fatalf("kept sentence lost: %q", out)
	}

	// flag recorded without the marker (period-then-citation form)
	out = removeSentence(body, "O custo segue três vezes o da banda larga urbana.")
	if strings.Contains(out, "[1]") {
		t.Fatalf("orphaned citation left behind: %q", out)
	}

	// the sentence's terminal punctuation goes with it — no ". ." stray
	out = removeSentence("Duas pediram prazo maior [1]. Especialistas divergem.",
		"Duas pediram prazo maior [1]")
	if strings.Contains(out, ". .") || !strings.HasPrefix(out, "Especialistas divergem.") {
		t.Fatalf("stray punctuation left behind: %q", out)
	}
}

// TestGateSections: a paid section keeps its heading, the first item's
// headline and opening lines, then a single subscribers-only note.
// Everything after the note — the rest of that item and every other
// item in the section — must not reach the gated output; free sections
// pass through untouched.
func TestGateSections(t *testing.T) {
	body := strings.Join([]string{
		"# Title",
		"",
		"## Paid",
		"### Item A",
		"First sentence of A. Second sentence of A. Third of A.",
		"",
		"Second paragraph of A leaks if not skipped.",
		"**Sources:** [1] [X](http://x)",
		"### Item B",
		"Only sentence of B.",
		"",
		"> **Comentário do editor:** paid commentary must be hidden",
		"",
		"## Free",
		"### Item C",
		"All of C stays visible. Every sentence of it.",
		"",
		"**Sources:** [1] [Y](http://y)",
	}, "\n")

	got := gateSections(body, []string{"Paid"}, "en")

	for _, want := range []string{
		"## Paid", "### Item A",
		"First sentence of A.",
		"## Free", "### Item C",
		"All of C stays visible. Every sentence of it.",
		"**Sources:** [1] [Y](http://y)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("gated body missing %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{
		"Second sentence of A", "Third of A",
		"Second paragraph of A leaks",
		"### Item B", "Only sentence of B",
		"Comentário do editor",
		"[X](http://x)",
	} {
		if strings.Contains(got, leak) {
			t.Fatalf("gated body leaked %q:\n%s", leak, got)
		}
	}
	// exactly one wall per paid section, naming the hidden count
	if c := strings.Count(got, "Subscribers only"); c != 1 {
		t.Fatalf("expected one paywall note, got %d:\n%s", c, got)
	}
	if !strings.Contains(got, "1 more stories") {
		t.Fatalf("note should say one item is hidden:\n%s", got)
	}

	// a single-item paid section gets the plain note
	single := "## Paid\n### Only\nOne sentence. Another one."
	if out := gateSections(single, []string{"Paid"}, "en"); !strings.Contains(out, "subscribe to keep reading") {
		t.Fatalf("single-item note should not carry a count:\n%s", out)
	}
	// no paid sections configured → body passes through unchanged
	if out := gateSections(body, nil, "en"); out != body {
		t.Fatalf("empty paid list should pass body through:\n%s", out)
	}
	// a section name that merely resembles a paid one is not gated
	if out := gateSections("## Paid extra\n### I\nFull text stays.", []string{"Paid"}, "en"); !strings.Contains(out, "Full text stays.") {
		t.Fatalf("non-matching section was gated:\n%s", out)
	}
}

func TestPaywallNoteLocalized(t *testing.T) {
	if n := paywallNote("pt-BR", 0); !strings.Contains(n, "assinantes") {
		t.Fatalf("pt note expected, got %q", n)
	}
	if n := paywallNote("pt-BR", 3); !strings.Contains(n, "mais 3 matérias") {
		t.Fatalf("pt counted note expected, got %q", n)
	}
	if n := paywallNote("en", 0); !strings.Contains(n, "Subscribers only") {
		t.Fatalf("en note expected, got %q", n)
	}
	if n := paywallNote("en", 2); !strings.Contains(n, "2 more stories") {
		t.Fatalf("en counted note expected, got %q", n)
	}
}
