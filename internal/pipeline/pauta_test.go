package pipeline

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func TestFirstSentence(t *testing.T) {
	if got := firstSentence("  Curto   texto. ", 50); got != "Curto texto." {
		t.Fatalf("short = %q", got)
	}
	long := "Primeira frase completa aqui. " + strings.Repeat("palavra ", 40)
	if got := firstSentence(long, 50); got != "Primeira frase completa aqui." {
		t.Fatalf("cut at sentence end = %q", got)
	}
	noStop := strings.Repeat("palavra ", 40)
	got := firstSentence(noStop, 30)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > 31 {
		t.Fatalf("word-boundary cut = %q", got)
	}
}

// TestStoryKeysRejected: a "no" answer on any of the story's stable
// keys keeps it out of the pauta — story ids are rebuilt every deck,
// the keys are not (KLOX-183).
func TestStoryKeysRejected(t *testing.T) {
	rejected := map[string]bool{"u:https://x.example/a": true, "s:42": true}
	same := &store.Story{DedupeKey: "https://x.example/a"}
	if !storyKeysRejected(same, rejected) {
		t.Fatal("dedupe-key match must reject")
	}
	bySim := &store.Story{Simhash: 42}
	if !storyKeysRejected(bySim, rejected) {
		t.Fatal("simhash match must reject")
	}
	other := &store.Story{DedupeKey: "https://x.example/b", Simhash: 7}
	if storyKeysRejected(other, rejected) {
		t.Fatal("unrelated story must not reject")
	}
	if storyKeysRejected(other, nil) {
		t.Fatal("empty feedback must not reject")
	}
}

// TestNoAnswerPenalty: a "Não" demotes look-alike stories by graded
// title overlap and leaves unrelated coverage untouched (KLOX-183).
func TestNoAnswerPenalty(t *testing.T) {
	rejected := []string{"Mendonça manda soltar investigados do INSS"}

	lookalike := &store.Story{Title: "Mendonça manda soltar alvos do INSS"}
	if got := noAnswerPenalty(lookalike, rejected); got <= 0 {
		t.Fatalf("look-alike penalty = %v, want > 0", got)
	}

	unrelated := &store.Story{Title: "Prefeitura anuncia novo parque linear na zona sul"}
	if got := noAnswerPenalty(unrelated, rejected); got != 0 {
		t.Fatalf("unrelated penalty = %v, want 0", got)
	}

	if got := noAnswerPenalty(unrelated, nil); got != 0 {
		t.Fatalf("no feedback penalty = %v, want 0", got)
	}
}
