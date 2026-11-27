package web

import "testing"

// KLOX-232/KLOX-274: selected editoria labels still fold into the stored
// topic as ranking vocabulary, but explicit chip state decides what the
// display strips back out. Free text never becomes chip state by itself.
func TestTopicNicheFoldRoundTrip(t *testing.T) {
	niches := []string{"economia", "mercado-financeiro"}
	stored := topicWithNiches("Pix, open finance, Drex", niches)
	if stored != "Pix, open finance, Drex, economia, mercado financeiro e investimentos" {
		t.Fatalf("stored topic = %q", stored)
	}
	if got := topicWithoutNiches(stored, niches); got != "Pix, open finance, Drex" {
		t.Fatalf("view topic = %q", got)
	}
	// A label already present as one whole comma part is not duplicated.
	if got := topicWithNiches("fintech, economia", []string{"economia"}); got != "fintech, economia" {
		t.Fatalf("re-fold duplicated the label: %q", got)
	}
	// The same exact comma part stays visible when its chip was not picked.
	if got := topicWithoutNiches("fintech, economia", nil); got != "fintech, economia" {
		t.Fatalf("unselected label stripped: %q", got)
	}
	// A label embedded mid-sentence is the creator's own words — kept —
	// and it no longer prevents the selected label's exact ranking beat.
	embedded := topicWithNiches("agronegócio familiar, pecuária", []string{"agronegocio"})
	if embedded != "agronegócio familiar, pecuária, agronegócio" {
		t.Fatalf("embedded fold topic = %q", embedded)
	}
	if got := topicWithoutNiches(embedded, []string{"agronegocio"}); got != "agronegócio familiar, pecuária" {
		t.Fatalf("embedded view topic = %q", got)
	}
	if got := topicWithoutNiches(embedded, nil); got != embedded {
		t.Fatalf("unselected embedded view topic = %q", got)
	}

	// KLOX-274's smoke case: the typed theme exactly matches the picked
	// editoria. It is represented once as chip state; the visible themes
	// keep the creator's other topics.
	smoke := topicWithNiches("agronegócio, pecuária, clima no campo, commodities agrícolas", []string{"agronegocio"})
	if got := topicWithoutNiches(smoke, []string{"agronegocio"}); got != "pecuária, clima no campo, commodities agrícolas" {
		t.Fatalf("smoke view topic = %q", got)
	}

	// Canonical labels with uppercase letters still are not duplicated.
	if got := topicWithNiches("brasil e cidades", []string{"brasil-cidades"}); got != "brasil e cidades" {
		t.Fatalf("case-folded label duplicated: %q", got)
	}
	if got := cleanNiches([]string{"economia", "bogus", "economia", " agronegocio "}); len(got) != 2 || got[0] != "economia" || got[1] != "agronegocio" {
		t.Fatalf("cleanNiches = %v", got)
	}
}

func TestOrderedNichesUsesCadastroOrder(t *testing.T) {
	got := orderedNiches([]string{"saude", "economia", "bogus", "agronegocio"})
	if len(got) != 3 || got[0] != "economia" || got[1] != "agronegocio" || got[2] != "saude" {
		t.Fatalf("orderedNiches = %v, want [economia agronegocio saude]", got)
	}
	if got := orderedNiches(nil); got == nil || len(got) != 0 {
		t.Fatalf("orderedNiches(nil) = %#v, want non-nil empty slice", got)
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
