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

// TestGateSections: a paid section keeps each item's headline and first
// sentence plus the subscribers-only note; every other line — remaining
// sentences, sources, commentary — must not reach the gated output, and
// free sections pass through untouched.
func TestGateSections(t *testing.T) {
	note := paywallNote("en")
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
		"More of B that must be hidden.",
		"> **Comentário do editor:** paid commentary must be hidden",
		"",
		"## Free",
		"### Item C",
		"All of C stays visible. Every sentence of it.",
		"",
		"**Sources:** [1] [Y](http://y)",
	}, "\n")

	got := gateSections(body, []string{"Paid"}, note)

	for _, want := range []string{
		"## Paid", "### Item A", "### Item B",
		"First sentence of A.", "Only sentence of B.",
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
		"More of B that must be hidden",
		"Comentário do editor",
		"[X](http://x)",
	} {
		if strings.Contains(got, leak) {
			t.Fatalf("gated body leaked %q:\n%s", leak, got)
		}
	}
	if c := strings.Count(got, note); c != 2 {
		t.Fatalf("expected one paywall note per paid item, got %d:\n%s", c, got)
	}

	// no paid sections configured → body passes through unchanged
	if out := gateSections(body, nil, note); out != body {
		t.Fatalf("empty paid list should pass body through:\n%s", out)
	}
	// a section name that merely resembles a paid one is not gated
	if out := gateSections("## Paid extra\n### I\nFull text stays.", []string{"Paid"}, note); !strings.Contains(out, "Full text stays.") {
		t.Fatalf("non-matching section was gated:\n%s", out)
	}
}

func TestPaywallNoteLocalized(t *testing.T) {
	if n := paywallNote("pt-BR"); !strings.Contains(n, "assinantes") {
		t.Fatalf("pt note expected, got %q", n)
	}
	if n := paywallNote("en"); !strings.Contains(n, "Subscribers only") {
		t.Fatalf("en note expected, got %q", n)
	}
}
