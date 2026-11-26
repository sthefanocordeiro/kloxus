package pipeline

import "testing"

func TestSplitSentencesKeepsQuoteWhole(t *testing.T) {
	in := `Ele disse “O plano é simples. Vamos executar agora.” e saiu. Depois voltou.`
	got := SplitSentences(in)
	if len(got) != 2 {
		t.Fatalf("got %d pieces %q, want 2", len(got), got)
	}
	if got := SplitSentences("Primeira frase. Segunda frase."); len(got) != 2 {
		t.Fatalf("plain split broke: %q", got)
	}
}
