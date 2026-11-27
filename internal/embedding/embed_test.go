package embedding

import (
	"math"
	"strings"
	"testing"
)

func TestEmbedDeterministic(t *testing.T) {
	a := Embed("Nubank lança nova conta para PJ no Pix")
	b := Embed("Nubank lança nova conta para PJ no Pix")
	if len(a) != Dim {
		t.Fatalf("dim=%d want %d", len(a), Dim)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("same input produced different vectors")
		}
	}
	// Unit-normalized.
	var n float64
	for _, x := range a {
		n += float64(x) * float64(x)
	}
	if math.Abs(n-1) > 1e-4 {
		t.Fatalf("norm=%f want 1", n)
	}
}

func TestCosineSimilarityRanks(t *testing.T) {
	fintech1 := Embed("Banco Central eleva Selic e Pix bate recorde de transações")
	fintech2 := Embed("Pix cresce e BC discute nova regra para bancos digitais")
	sport := Embed("Brasil vence amistoso no futebol com gol aos 45 minutos")
	if Cosine(fintech1, fintech2) <= Cosine(fintech1, sport) {
		t.Fatalf("related texts should outscore unrelated: rel=%.3f unrel=%.3f",
			Cosine(fintech1, fintech2), Cosine(fintech1, sport))
	}
	if got := Cosine(nil, fintech1); got != 0 {
		t.Fatalf("empty vector: %f", got)
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	v := Embed("texto qualquer de teste")
	s := Format(v)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		t.Fatalf("literal: %s", s[:20])
	}
	back, err := Parse(s)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(back) != len(v) {
		t.Fatalf("parsed dim=%d want %d", len(back), len(v))
	}
	if Cosine(v, back) < 0.999 {
		t.Fatalf("round-trip drift: cosine=%f", Cosine(v, back))
	}
	if _, err := Parse("not a vector"); err == nil {
		t.Fatal("expected parse error")
	}
}
