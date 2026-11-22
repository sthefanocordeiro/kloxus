package pipeline

import (
	"strings"
	"testing"
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
