package ingest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Feed boilerplate (KLOX-48): one real item body per fetchable catalog
// source — captured from the live feeds — must come out of CleanHTML
// free of link-back footers, photo credits, affiliate calls and AI
// summary blocks, while the actual reporting survives.

var boilerplateAnywhere = []*regexp.Regexp{
	regexp.MustCompile(`(?i)apareceu primeiro em`),
	regexp.MustCompile(`(?i)participe dos canais de ofertas`),
	regexp.MustCompile(`(?i)(?:foto|imagem|ilustra\S*)\s*:`),
	regexp.MustCompile(`(?i)^\s*resumo\s+ia\b`),
	regexp.MustCompile(`(?i)^\s*gerado com ia\b`),
}

func TestCleanHTMLFeedBoilerplateRealSamples(t *testing.T) {
	// stripCSS mirrors the catalog rows in migrations/0003_ingest.sql.
	cases := []struct {
		source   string
		stripCSS []string
		keeps    []string // real reporting that must survive
	}{
		{"tecnoblog", []string{"oferta", "offer"}, []string{"Claro", "pregão"}},
		{"olhar-digital", nil, []string{"sono"}},
		{"startups", nil, []string{"Zoop"}},
		{"macmagazine", nil, nil},
		{"meiobit", nil, nil},
		{"tabnews", nil, nil},
		{"hacker-news", nil, nil},
	}
	for _, tc := range cases {
		raw, err := os.ReadFile(filepath.Join("testdata", tc.source+".html"))
		if err != nil {
			t.Fatalf("%s: read sample: %v", tc.source, err)
		}
		text, _ := CleanHTML(string(raw), tc.stripCSS)
		for _, re := range boilerplateAnywhere {
			if m := re.FindString(text); m != "" {
				t.Errorf("%s: boilerplate %q survived cleaning", tc.source, m)
			}
		}
		for _, keep := range tc.keeps {
			if !strings.Contains(text, keep) {
				t.Errorf("%s: real content %q lost by cleaning", tc.source, keep)
			}
		}
	}
}

func TestNormalizeTextBoilerplateLines(t *testing.T) {
	in := strings.Join([]string{
		"O regulador abriu consulta pública sobre o tema.",
		"O post Consulta pública apareceu primeiro em Olhar Digital.",
		"Participe dos canais de ofertas do Tecnoblog",
		"Painel discutiu o tema | Foto: Divulgação",
		"Genoma associado ao sono – Imagem: Alena A/Shutterstock",
		"Legenda curta (imagem: Autor/Veículo)",
		"Crédito solto (foto: arquivo)",
		"Conclusão importante. Imagem: Lightspring – Shutterstock",
		"Resumo IA",
		"Ponto inventado pelo modelo um.",
		"Ponto inventado pelo modelo dois.",
		"Gerado com IA e revisado pela redação",
		"Parágrafo real depois do bloco.",
	}, "\n\n")
	out := normalizeText(in)
	for _, gone := range []string{
		"apareceu primeiro", "Participe dos canais",
		"Foto:", "Imagem:", "imagem:", "foto:",
		"Resumo IA", "modelo um", "modelo dois", "Gerado com IA",
	} {
		if strings.Contains(out, gone) {
			t.Errorf("boilerplate %q survived:\n%s", gone, out)
		}
	}
	for _, keep := range []string{
		"O regulador abriu consulta pública",
		"Painel discutiu o tema",
		"Genoma associado ao sono",
		"Legenda curta",
		"Conclusão importante.",
		"Parágrafo real depois do bloco.",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("content %q lost:\n%s", keep, out)
		}
	}
}

// A caption like "| Fotos tiradas de dia" (no colon) is prose, not a
// credit — it must survive.
func TestNormalizeTextKeepsCaptionWithoutColon(t *testing.T) {
	out := normalizeText("Fotos tiradas de dia, com luz frontal. | Fotos tiradas de dia, com luz frontal")
	if !strings.Contains(out, "Fotos tiradas de dia") {
		t.Fatalf("legit caption dropped: %q", out)
	}
}
