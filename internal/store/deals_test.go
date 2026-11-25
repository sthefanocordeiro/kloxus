package store

import "testing"

// TestPromoClassifier pins the owner-required contract (KLOX-45): 15
// deal/coupon/buying-guide titles are detected, 15 news titles — several
// of which mention prices without being deals — pass through. Matching
// must ignore case and accents.
func TestPromoClassifier(t *testing.T) {
	deals := []string{
		"Promoção: iPhone 15 com 30% de desconto na Amazon",
		"OFERTA relâmpago: Galaxy S24 por R$ 2.999",
		"Cupom Americanas: R$ 50 em eletrônicos selecionados",
		"Notebook Lenovo com 40% OFF nesta terça",
		"Black Friday 2026: as melhores ofertas de TVs",
		"Esquenta Black November no Magalu: descontos de até 70%",
		"11.11 no AliExpress: guia completo de ofertas",
		"Cyber Monday: fones de ouvido pela metade do preço",
		"Air Fryer atinge menor preço histórico: R$ 329",
		"Selecionados da semana: 20 achados com cupom",
		"Veja uma seleção de smart TVs em promoção",
		"Smartwatch por R$ 199 em até 12x sem juros",
		"Echo Dot 5 na Amazon por R$ 299",
		"Monitor gamer no Mercado Livre com frete grátis",
		"Os melhores descontos da Black da Americanas",
	}
	news := []string{
		"iPhone 17 chega ao Brasil por R$ 7.999",
		"Nintendo Switch 2 custará R$ 4.499 no lançamento",
		"Gasolina sobe para R$ 6,20 e pressiona inflação",
		"Apple vende 5 milhões de iPhones no primeiro fim de semana",
		"Pix movimenta R$ 2 trilhões em outubro, diz Banco Central",
		"Netflix aumenta preço do plano padrão para R$ 44,90",
		"Mercado de games faturou US$ 200 bilhões em 2026",
		"Startup brasileira levanta R$ 50 milhões em rodada",
		"TSMC investirá US$ 100 bilhões em novas fábricas",
		"Dólar fecha a R$ 5,40 após decisão do Copom",
		"iFood registra 100 milhões de pedidos em outubro",
		"Preço do lítio cai 40% e muda conta dos elétricos",
		"Apple lança MacBook Pro M5 a partir de US$ 1.999",
		"Inflação acumulada em 12 meses fica em 4,2%",
		"Samsung abre fábrica de R$ 2 bilhões em São Paulo",
	}
	if len(deals) != 15 || len(news) != 15 {
		t.Fatalf("test contract changed: %d deals / %d news", len(deals), len(news))
	}
	for _, title := range deals {
		if !IsPromoTitle(title) {
			t.Errorf("deal title not flagged: %q", title)
		}
	}
	for _, title := range news {
		if IsPromoTitle(title) {
			t.Errorf("news title wrongly flagged as promo: %q", title)
		}
	}
}

// TestPromoFold covers the matching rules the worded cases cannot:
// accents fold both ways, case is ignored and whitespace collapses.
func TestPromoFold(t *testing.T) {
	for title, want := range map[string]bool{
		"PROMOÇÕES de verão em tudo":          true,
		"Oferta do dia: kindle por R$ 549":    true,
		"CUPOM de primeira compra":            true,
		"preço histórico no Pixel 9":          true,
		"EM   ATÉ   12X  no site":             true,
		"Achados do Tecnoblog desta semana":   true,
		"Comitê seleciona novo diretor do BC": false, // "seleciona" is not "selecionados"
		"Selecionamos as notícias do dia":     false, // the listed term is "selecionados" (D41)
		"Guia de compra de notebooks":         false,
		"Apple anuncia evento em Cupertino":   false,
	} {
		if got := IsPromoTitle(title); got != want {
			t.Errorf("IsPromoTitle(%q) = %v, want %v", title, got, want)
		}
	}
}
