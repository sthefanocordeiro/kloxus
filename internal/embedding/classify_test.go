package embedding

import "testing"

func topicsOf(r Result) map[string]bool {
	m := map[string]bool{}
	for _, t := range r.Topics {
		m[t] = true
	}
	return m
}

func TestClassifyFintech(t *testing.T) {
	r := Classify("Nubank testa novo recurso no Pix",
		"O banco digital liberou pagamentos instantâneos para contas PJ nesta semana.",
		[]string{"finance"})
	if !topicsOf(r)["fintech"] {
		t.Fatalf("topics=%v want fintech", r.Topics)
	}
	if !topicsOf(r)["economia"] {
		t.Fatalf("finance category should tag economia: %v", r.Topics)
	}
	if r.Language != "pt" {
		t.Fatalf("lang=%q want pt", r.Language)
	}
}

func TestClassifyRegulacao(t *testing.T) {
	r := Classify("Anatel publica nova regulamentação para telecom",
		"A agência reguladora abriu consulta pública sobre o decreto que muda regras do setor.",
		[]string{"politics"})
	if !topicsOf(r)["regulacao"] {
		t.Fatalf("topics=%v want regulacao", r.Topics)
	}
	if !topicsOf(r)["politica"] {
		t.Fatalf("politics category should tag politica: %v", r.Topics)
	}
}

func TestClassifyCriativo(t *testing.T) {
	r := Classify("Festival de cinema abre mostra de filmes nacionais",
		"A programação inclui estreias de diretores brasileiros e shows ao vivo.",
		[]string{"entertainment"})
	if !topicsOf(r)["midia"] {
		t.Fatalf("topics=%v want midia", r.Topics)
	}
	if r.Kind != "entretenimento" {
		t.Fatalf("kind=%q want entretenimento", r.Kind)
	}
}

func TestClassifySportsKind(t *testing.T) {
	r := Classify("Brasil vence por 2 a 0", "A seleção dominou o jogo.", []string{"sports"})
	if r.Kind != "esporte" {
		t.Fatalf("kind=%q want esporte", r.Kind)
	}
}

func TestClassifyIAUppercaseOnly(t *testing.T) {
	// Folded "ia" is a verb form — it must not tag IA.
	r := Classify("Ele ia ao mercado quando choveu",
		"O homem ia caminhando pela cidade e depois voltou para casa.", nil)
	if topicsOf(r)["IA"] {
		t.Fatalf("verb 'ia' must not tag IA: %v", r.Topics)
	}
	r2 := Classify("Startup levanta rodada para IA generativa",
		"A empresa aposta em inteligência artificial para o setor.", nil)
	if !topicsOf(r2)["IA"] {
		t.Fatalf("IA expected: %v", r2.Topics)
	}
}

func TestClassifyFallbackOutro(t *testing.T) {
	r := Classify("Xqzl wqpk znv", "jqfz mnpq tvlk xwz.", nil)
	if len(r.Topics) != 1 || r.Topics[0] != "outro" {
		t.Fatalf("topics=%v want [outro]", r.Topics)
	}
}

func TestClassifyCapFiveTopics(t *testing.T) {
	text := "fintech pix cripto bitcoin regulação banco central política eleição " +
		"economia selic negócios empresa startup tecnologia app segurança hacker " +
		"consumidor preço games videogame mídia streaming ciência pesquisa " +
		"inteligência artificial"
	r := Classify(text, text, nil)
	if len(r.Topics) > 5 {
		t.Fatalf("topics=%v exceeds cap", r.Topics)
	}
}

func TestDetectLanguage(t *testing.T) {
	pt := "O presidente disse que a nova medida entra em vigor na próxima semana e que o governo vai acompanhar os resultados de perto."
	en := "The president said the new measure takes effect next week and that the government will track the results closely."
	if DetectLanguage(pt) != "pt" {
		t.Fatalf("pt: %q", DetectLanguage(pt))
	}
	if DetectLanguage(en) != "en" {
		t.Fatalf("en: %q", DetectLanguage(en))
	}
	if DetectLanguage("short") != "" {
		t.Fatal("short text should be undecidable")
	}
}
