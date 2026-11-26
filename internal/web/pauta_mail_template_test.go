package web

import (
	"strings"
	"testing"
)

func TestRenderPautaMailHTMLPT(t *testing.T) {
	age := 3.0
	stories := []pautaMailStory{
		{Card: pautaCardView{StoryID: 11, Title: `Banco <central> & "risco"`, Outlet: "Valor Econômico", Summary: "Resumo curto.", URL: "https://valor.example/a", AgeHours: &age}, PostURL: "https://kloxus.test/claim?t=a&lang=pt&next=%2Fcompor%3Fpub%3D1%26noticia%3D11"},
		{Card: pautaCardView{StoryID: 12, Title: "Pix muda", Outlet: "Fintech Lab", Summary: "Outro resumo.", URL: "javascript:alert(1)"}, PostURL: "https://kloxus.test/claim?t=b&lang=pt&next=%2Fcompor%3Fpub%3D1%26noticia%3D12"},
	}
	h := renderPautaMailHTML("pt", "2026-11-24", "Fintech para bancários", stories,
		"https://kloxus.test/claim?t=open&lang=pt", "https://kloxus.test/daily-mail/unsub?t=u")

	for _, want := range []string{
		`lang="pt-BR"`,
		"Seu Radar de hoje",
		"terça-feira, 24 de novembro de 2026",
		"2 notícias",
		"Criar post com essa notícia →",
		"Descadastrar deste Radar",
		"Abrir no Kloxus",
		"Fintech para bancários",
		"Valor Econômico",
		"há 3 h",
		"next=%2Fcompor%3Fpub%3D1%26noticia%3D11",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	if n := strings.Count(h, "Criar post com essa notícia →"); n != 2 {
		t.Errorf("story CTAs = %d, want 2", n)
	}
	for _, banned := range []string{"Seu comentário", "commentary", "<script", "<link", "javascript:alert"} {
		if strings.Contains(h, banned) {
			t.Errorf("HTML contains banned %q", banned)
		}
	}
	if !strings.Contains(h, "Banco &lt;central&gt; &amp; &#34;risco&#34;") {
		t.Error("story title was not HTML-escaped")
	}
	if !strings.Contains(h, "<table role=\"presentation\"") {
		t.Error("template is not table-based")
	}
	if strings.Contains(h, "%!") {
		t.Error("HTML contains a fmt formatting artifact")
	}
}

func TestRenderPautaMailHTMLEn(t *testing.T) {
	stories := []pautaMailStory{{Card: pautaCardView{StoryID: 11, Title: "Rates move", Outlet: "Fintech Lab"}, PostURL: "https://kloxus.test/claim?t=a&lang=en&next=%2Fcompor%3Fpub%3D1%26noticia%3D11"}}
	h := renderPautaMailHTML("en", "2026-11-24", "Fintech desk", stories,
		"https://kloxus.test/claim?t=open&lang=en", "https://kloxus.test/daily-mail/unsub?t=u")
	for _, want := range []string{
		`lang="en"`, "Your Radar today", "Tuesday, November 24, 2026", "1 story",
		"Create a post with this story →", "Unsubscribe from this Radar", "Open in Kloxus",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("EN HTML lacks %q", want)
		}
	}
}

func TestClaimNextTarget(t *testing.T) {
	cases := map[string]string{
		"":                              "/start",
		"/compor?pub=1&noticia=2":       "/compor?pub=1&noticia=2",
		"/publications/1/today":         "/publications/1/today",
		"https://evil.example/compor":   "/start",
		"//evil.example/compor":         "/start",
		"/api/v1/workspace":             "/start",
		"/claim?t=x":                    "/start",
		"/compor/../publications/1":     "/start",
		"javascript:alert(1)":           "/start",
		"/publications/1/today?x=1":     "/publications/1/today?x=1",
		"/publications/1/today?x=%20":   "/publications/1/today?x=%20",
		"/compor?pub=1&noticia=2&lang=": "/compor?pub=1&noticia=2&lang=",
	}
	for in, want := range cases {
		if got := claimNextTarget(in); got != want {
			t.Errorf("claimNextTarget(%q) = %q, want %q", in, got, want)
		}
	}
}
