package web

import (
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func TestNicheSlugsPaywallAndSpread(t *testing.T) {
	got := nicheSlugs([]string{"economia", "mercado-financeiro"}, nil)
	if got["valor"] {
		t.Error("paywalled valor included without the user's mark")
	}
	if len(got) < 5 {
		t.Errorf("economia+mercado attach only %d sources: %v", len(got), got)
	}
	if !nicheSlugs([]string{"economia"}, []string{"valor"})["valor"] {
		t.Error("valor must be included when the user marks it")
	}
	for niche, slugs := range nicheSources {
		if len(slugs) < 4 {
			t.Errorf("niche %s has only %d sources", niche, len(slugs))
		}
	}
	if len(nicheSlugs(nil, nil)) == 0 {
		t.Error("default set empty")
	}
}

// KLOX-207: the editorias chips recover from the theme text — cadastro
// folded each picked editoria's label into the topic at create time, so
// radars that predate the field report theirs too, in cadastro order.
func TestNichesFromTopic(t *testing.T) {
	got := nichesFromTopic("fintech, Pix e regulação, mercado financeiro e investimentos")
	if len(got) != 1 || got[0] != "mercado-financeiro" {
		t.Fatalf("fintech topic: got %v, want [mercado-financeiro]", got)
	}
	got = nichesFromTopic("x, saúde, economia")
	if len(got) != 2 || got[0] != "economia" || got[1] != "saude" {
		t.Fatalf("multi topic: got %v, want cadastro order [economia saude]", got)
	}
	if got := nichesFromTopic(""); len(got) != 0 {
		t.Fatalf("empty topic: got %v, want []", got)
	}
	if got := nichesFromTopic("jogos de celular"); len(got) != 0 {
		t.Fatalf("unrelated topic: got %v, want []", got)
	}
}

// KLOX-207: suggested sources rank by adherence — a source scores each
// onboarding desk it belongs to by how many of that desk's feeds the
// radar already added, so finance feeds beat tech toys on a fintech
// radar. Ties keep the catalog order, and paywalled sources always sit
// at the end however well they score (KLOX-235).
func TestOfferedOrder(t *testing.T) {
	cat := []store.CatalogSource{
		{Slug: "tecnoblog", URL: "u:tecnoblog"},
		{Slug: "infomoney", URL: "u:infomoney"},
		{Slug: "exame", URL: "u:exame"},
		{Slug: "poder360", URL: "u:poder360"},
		{Slug: "mundo-rh", URL: "u:mundo-rh"},
		{Slug: "valor", URL: "u:valor"},
		{Slug: "folha", URL: "u:folha"},
	}
	got := offeredOrder(cat, map[string]bool{"u:infomoney": true})
	slugs := []string{}
	for _, cs := range got {
		slugs = append(slugs, cs.Slug)
	}
	// infomoney shares 6 desks with itself, exame 5; tecnologia touches
	// infomoney via the startups desk (1 pt, tie with mundo-rh keeps
	// catalog order); política's desk holds no added feed. valor scores
	// 3 through the economia/negócios/carreira desks — still last with
	// folha, because a suggestion the reader cannot open is the weakest
	// pick; between themselves they keep score order (valor 3, folha 0).
	want := []string{"infomoney", "exame", "tecnoblog", "mundo-rh", "poder360", "valor", "folha"}
	for i, w := range want {
		if slugs[i] != w {
			t.Fatalf("order %v, want %v", slugs, want)
		}
	}
}
