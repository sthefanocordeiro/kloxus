package dedupe

import (
	"context"
	"testing"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
)

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.example.com/a/?utm_source=feed&utm_medium=rss": "https://example.com/a",
		"https://EXAMPLE.com/A#comments":                            "https://example.com/A",
		"https://example.com/a?fbclid=xyz&id=3":                     "https://example.com/a?id=3",
		"https://example.com/":                                      "https://example.com",
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAssignMergesIdenticalText(t *testing.T) {
	body := "O regulador abriu consulta pública sobre o uso de sistemas automatizados em processos de contratação. A minuta exige que empresas informem candidatos quando currículos forem triados por IA e prevê auditorias anuais."
	urls := []string{
		"https://a.example/story?utm_source=x",
		"https://a.example/story",
		"https://b.example/other",
		"https://c.example/same-story-different-outlet",
	}
	texts := []string{
		"Título um\n" + body,
		"Título um\n" + body,
		"Outra notícia\nConteúdo totalmente diferente sobre outro assunto relevante.",
		"Manchete diferente\n" + body + " (Republicação.)",
	}
	got := Assign(urls, texts)
	if got[0] != got[1] {
		t.Error("same canonical URL not merged")
	}
	if got[0] != got[3] {
		t.Error("near-identical text not merged")
	}
	if got[2] == got[0] {
		t.Error("different story merged incorrectly")
	}
}

func TestSimHashDistance(t *testing.T) {
	a := SimHash("O regulador abriu consulta pública sobre sistemas automatizados de contratação.")
	b := SimHash("O regulador abriu consulta pública sobre sistemas automatizados de contratação. (Republished with a correction.)")
	c := SimHash("Uma receita simples de bolo de cenoura com cobertura de chocolate.")
	if hamming(a, b) > HammingThreshold {
		t.Errorf("near-identical texts at distance %d", hamming(a, b))
	}
	if hamming(a, c) <= HammingThreshold {
		t.Error("unrelated texts too close")
	}
}

// TestCorpusDedupe runs the real fixture corpus: 35 items should collapse
// to 32 stories — the 3-way literal republish (AI-hiring consultation) and
// the remote-work republish merge; same-event rewrites stay separate.
func TestCorpusDedupe(t *testing.T) {
	f := sources.FixtureFetcher{FS: examples.FS}
	var items []struct {
		url, text string
	}
	for _, src := range []struct{ typ, url string }{
		{"rss", "fixtures/feeds/radar-tech.xml"},
		{"rss", "fixtures/feeds/pulso-digital.xml"},
		{"rss", "fixtures/feeds/correio-do-codigo.xml"},
		{"page", "fixtures/pages/observatorio-aberto.html"},
	} {
		raw, err := f.Fetch(context.Background(), src.typ, src.url)
		if err != nil {
			t.Fatalf("fetch %s: %v", src.url, err)
		}
		for _, it := range raw {
			items = append(items, struct {
				url, text string
			}{it.URL, it.Title + "\n" + it.ContentText})
		}
	}
	if len(items) != 35 {
		t.Fatalf("corpus has %d items, want 35", len(items))
	}
	urls := make([]string, len(items))
	texts := make([]string, len(items))
	for i, it := range items {
		urls[i], texts[i] = it.url, it.text
	}
	clusters := Assign(urls, texts)
	seen := map[int]bool{}
	for _, c := range clusters {
		seen[c] = true
	}
	if len(seen) != 32 {
		counts := map[int]int{}
		for _, c := range clusters {
			counts[c]++
		}
		for c, n := range counts {
			if n > 1 {
				t.Logf("cluster %d has %d items", c, n)
			}
		}
		t.Fatalf("corpus produced %d stories, want 32", len(seen))
	}
}
