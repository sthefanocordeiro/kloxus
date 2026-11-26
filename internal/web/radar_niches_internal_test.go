package web

import "testing"

// KLOX-232: the editorias' labels still fold into the stored topic for
// ranking, but the API strips them back out — the theme field shows the
// creator's words alone while the beats live as chips.
func TestTopicNicheFoldRoundTrip(t *testing.T) {
	stored := topicWithNiches("Pix, open finance, Drex", []string{"economia", "mercado-financeiro"})
	if stored != "Pix, open finance, Drex, economia, mercado financeiro e investimentos" {
		t.Fatalf("stored topic = %q", stored)
	}
	if got := topicWithoutNiches(stored); got != "Pix, open finance, Drex" {
		t.Fatalf("view topic = %q", got)
	}
	// a label already in the text is not duplicated
	if got := topicWithNiches("fintech, economia", []string{"economia"}); got != "fintech, economia" {
		t.Fatalf("re-fold duplicated the label: %q", got)
	}
	// a label mid-sentence is the creator's own words — kept
	if got := topicWithoutNiches("economia circular e reúso"); got != "economia circular e reúso" {
		t.Fatalf("embedded label stripped: %q", got)
	}
	// chips still derive from the stored text
	if n := nichesFromTopic(stored); len(n) != 2 {
		t.Fatalf("niches = %v", n)
	}
}

func TestNicheMapsCover18Editorias(t *testing.T) {
	if len(nicheSources) != 18 || len(nicheLabels) != 18 {
		t.Fatalf("editorias: sources=%d labels=%d, want 18", len(nicheSources), len(nicheLabels))
	}
	for k, slugs := range nicheSources {
		if nicheLabels[k] == "" || len(slugs) == 0 {
			t.Errorf("editoria %q lacks a label or sources", k)
		}
	}
	if len(nicheOrder) != len(nicheSources) {
		t.Fatalf("nicheOrder covers %d editorias, want %d", len(nicheOrder), len(nicheSources))
	}
	for _, k := range nicheOrder {
		if nicheLabels[k] == "" {
			t.Errorf("nicheOrder lists unknown editoria %q", k)
		}
	}
}
