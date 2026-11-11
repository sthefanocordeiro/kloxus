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
