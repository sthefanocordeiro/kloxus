package pipeline

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func TestFTSTermsAreSafeAndDeduped(t *testing.T) {
	pub := &store.Publication{
		Topic:         "Fintech, banco central e Pix; criptomoedas (cripto) & Pix",
		EditorialLine: "Priorize regulação. Pule games e promoções.",
	}
	terms := ftsTerms(pub)
	seen := map[string]bool{}
	for _, tm := range terms {
		if seen[tm] {
			t.Fatalf("duplicate term %q in %v", tm, terms)
		}
		seen[tm] = true
		for _, c := range tm {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				t.Fatalf("term %q is not tsquery-safe", tm)
			}
		}
	}
	for _, want := range []string{"fintech", "banco", "pix"} {
		if !seen[want] {
			t.Fatalf("missing %q in %v", want, terms)
		}
	}
	if seen["games"] {
		t.Fatalf("skip clause leaked into the theme vocabulary: %v", terms)
	}
}

func TestProfileTextSeparatesSkipClauses(t *testing.T) {
	pub := &store.Publication{
		Name: "Radar Fintech", Topic: "fintech e Pix",
		EditorialLine: "Cubra regulação bancária. Pule games e promoções.",
		RejectTerms:   "futebol",
	}
	if txt := profileText(pub); !strings.Contains(txt, "Radar Fintech") || strings.Contains(strings.ToLower(txt), "games") {
		t.Fatalf("profile text = %q", txt)
	}
	sk := skipText(pub)
	if !strings.Contains(sk, "futebol") || !strings.Contains(sk, "games") {
		t.Fatalf("skip text = %q", sk)
	}
	h1 := profileHash(pub, "voyage-4-lite")
	if h1 == profileHash(pub, "other-model") {
		t.Fatal("the model must be part of the profile hash")
	}
	pub2 := *pub
	pub2.Topic = "fintech, Pix e cripto"
	if h1 == profileHash(&pub2, "voyage-4-lite") {
		t.Fatal("a theme change must change the hash")
	}
}

func TestEditorialPenaltyKeepsPolicyDemotions(t *testing.T) {
	pub := &store.Publication{EditorialLine: "Pule games."}
	games := &store.Story{Title: "GTA 6 e games chegam", Items: []store.Item{{Title: "GTA 6 e games chegam"}}}
	if got := editorialPenalty(games, pub, nil); got != -rejectPenalty {
		t.Fatalf("skip-clause hit penalty = %v, want %v", got, -rejectPenalty)
	}
	clean := &store.Story{Title: "Pix tem novas regras", Items: []store.Item{{Title: "Pix tem novas regras"}}}
	if got := editorialPenalty(clean, pub, nil); got != 0 {
		t.Fatalf("clean story penalised: %v", got)
	}
}
