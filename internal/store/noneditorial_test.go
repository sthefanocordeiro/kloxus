package store

import "testing"

// TestNonEditorialClassifier pins the KLOX-55 contract: release lists,
// where-to-watch guides and the outlet's own supporter campaign are
// flagged; real coverage — including match and launch reporting —
// passes through. Matching ignores case and accents.
func TestNonEditorialClassifier(t *testing.T) {
	noise := []string{
		"Onde assistir: Corinthians x Palmeiras ao vivo neste sábado",
		"O que assistir neste fim de semana",
		"Como assistir ao jogo do Brasil online",
		"Estreias da semana: 12 filmes e séries",
		"Estreias de novembro: o que chega ao streaming",
		"O que chega à Netflix esta semana",
		"Tudo que chega ao catálogo do streaming",
		"Lançamentos da semana nos cinemas",
		"Novidades da semana no catálogo",
		"Guia de estreias do mês",
		"Where to watch the final this Sunday",
		"How to watch the match live tonight",
		"New on Netflix this month",
		"What's coming to streaming in March",
		"Release schedule for spring shows",
	}
	for _, title := range noise {
		if !IsNonEditorialTitle(title, "Feedz") {
			t.Errorf("non-editorial title not flagged: %q", title)
		}
	}
	// self-promo: a funding ask naming the outlet or its own operation
	promos := []struct{ title, outlet string }{
		{"Apoie o Olhar Digital", "Olhar Digital"},
		{"Ajude o Olhar Digital a continuar existindo", "olhar digital"},
		{"Apoie o jornalismo independente", "Terceiro"},
		{"Seja sócio do nosso canal", "Canal"},
		{"Seja membro da newsletter", "Boletim"},
		{"Become a supporter of our journalism", "Tech Daily"},
		{"Support us and become a member", "Tech Daily"},
		{"Apoie nosso trabalho com uma vaquinha", "Veículo"},
	}
	for _, c := range promos {
		if !IsNonEditorialTitle(c.title, c.outlet) {
			t.Errorf("self-promo title not flagged: %q (outlet %q)", c.title, c.outlet)
		}
	}
	news := []struct{ title, outlet string }{
		{"Corinthians vence clássico por 2 a 0", "GE"},
		{"Netflix aumenta preço do plano padrão para R$ 44,90", "Canaltech"},
		{"iPhone 17 chega ao Brasil em setembro", "Tecnoblog"},
		{"Grupo abre rodada de apoiadores para ONG local", "Portal"},
		{"Campanha de financiamento coletivo para a escola", "Diário"},
		{"Adoeceu após trégua: clube confirma lesão", "GE"},
		{"Regulador abre consulta sobre assinatura digital", "Radar"},
		{"Startup ajude pequenos negócios a vender online", "Fintech"},
	}
	for _, c := range news {
		if IsNonEditorialTitle(c.title, c.outlet) {
			t.Errorf("news title wrongly flagged: %q", c.title)
		}
	}
}
