package web

import "testing"

func TestNicheSlugsPaywallAndSpread(t *testing.T) {
	got := nicheSlugs([]string{"economia", "mercado-financeiro"}, nil)
	if got["valor"] {
		t.Error("paywalled valor included without the user's mark")
	}
	if len(got) < 5 {
		t.Errorf("economia+mercado attach only %d sources: %v", len(got), got)
	}
	if !nicheSlugs([]string{"economia"}, []string{"valor"})["valor"] {
		t.Error("valor must be included when the user marks it")
	}
	for niche, slugs := range nicheSources {
		if len(slugs) < 4 {
			t.Errorf("niche %s has only %d sources", niche, len(slugs))
		}
	}
	if len(nicheSlugs(nil, nil)) == 0 {
		t.Error("default set empty")
	}
}
