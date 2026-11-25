package web

import "testing"

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
