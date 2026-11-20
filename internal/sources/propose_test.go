package sources

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
)

// ProposeFromPage is pure extraction — these tests need no DB and never
// touch a provider (KLOX-99: no paid calls in tests).

func TestProposeFromPageMapsHeadings(t *testing.T) {
	page := &ScrapedPage{
		URL:      "https://radar.example.com/edition-42",
		Title:    "Radar Fintech | Semana 42",
		Language: "pt-BR",
		Markdown: strings.Join([]string{
			"# Radar Fintech",
			"Edição semanal com bastante texto para contar palavras o suficiente.",
			strings.Repeat("conteúdo editorial de verdade. ", 30),
			"## O pix fora do ar",
			"texto",
			"## Bancos sob pressão",
			"texto",
			"## Subscribe",
			"## Links da semana",
		}, "\n"),
	}
	prop := ProposeFromPage(page, page.URL)
	if prop.Name != "Radar Fintech" {
		t.Fatalf("name = %q, want %q", prop.Name, "Radar Fintech")
	}
	if prop.Language != "pt-BR" {
		t.Fatalf("language = %q", prop.Language)
	}
	if len(prop.Sections) != 3 {
		t.Fatalf("sections = %d, want 3: %+v", len(prop.Sections), prop.Sections)
	}
	if prop.Sections[0].Title != "O pix fora do ar" || prop.Sections[0].Format != config.SectionFeatured {
		t.Fatalf("first section = %+v", prop.Sections[0])
	}
	if prop.Sections[1].Format != config.SectionNews {
		t.Fatalf("middle section format = %q", prop.Sections[1].Format)
	}
	if prop.Sections[2].Title != "Links da semana" || prop.Sections[2].Format != config.SectionLinks {
		t.Fatalf("last section = %+v", prop.Sections[2])
	}
	// "Subscribe" is chrome, not a section
	for _, s := range prop.Sections {
		if strings.EqualFold(s.Title, "Subscribe") {
			t.Fatalf("boilerplate heading became a section: %+v", prop.Sections)
		}
	}
	for _, w := range prop.Warnings {
		if w == "generic_sections" {
			t.Fatal("detected headings should not warn generic_sections")
		}
	}
}

func TestProposeFromPageFallbackSkeleton(t *testing.T) {
	page := &ScrapedPage{
		URL:      "https://blog.algo.dev/",
		Title:    "Something - Substack",
		Markdown: strings.Repeat("words and more words ", 40),
	}
	prop := ProposeFromPage(page, page.URL)
	if prop.Name != "Something" {
		t.Fatalf("platform suffix not stripped, name = %q", prop.Name)
	}
	if len(prop.Sections) != 3 || prop.Sections[0].Format != config.SectionFeatured {
		t.Fatalf("canonical skeleton missing: %+v", prop.Sections)
	}
	found := false
	for _, w := range prop.Warnings {
		if w == "generic_sections" {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want generic_sections", prop.Warnings)
	}
}

func TestProposeFromPageThinContent(t *testing.T) {
	page := &ScrapedPage{
		URL:      "https://pay.example.com/post",
		Title:    "Paywall Post",
		Markdown: "Subscribe to continue reading this story.",
	}
	prop := ProposeFromPage(page, page.URL)
	if prop.Words >= MinWords {
		t.Fatalf("words = %d, want < %d", prop.Words, MinWords)
	}
	found := false
	for _, w := range prop.Warnings {
		if w == "thin_content" {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want thin_content", prop.Warnings)
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		declared, md, want string
	}{
		{"pt-BR", "", "pt-BR"},
		{"en", "", "en"},
		{"en-US", "", "en"},
		{"", "the cat and the dog are in the house with the sun", "en"},
		{"", "o gato e o cão estão na casa com o sol para ver", "pt-BR"},
		{"", "", "pt-BR"},
	}
	for _, c := range cases {
		if got := detectLanguage(c.declared, c.md); got != c.want {
			t.Fatalf("detectLanguage(%q, %q) = %q, want %q", c.declared, c.md, got, c.want)
		}
	}
}

func TestCleanTitle(t *testing.T) {
	cases := []struct{ title, fallback, want string }{
		{"Radar Fintech | Semana 42", "x", "Radar Fintech"},
		{"The Daily Byte — Substack", "x", "The Daily Byte"},
		{"My Blog on Medium", "x", "My Blog"},
		{"  ", "fallback", "fallback"},
		{"A", "fallback", "fallback"},
		{"Plain Name", "x", "Plain Name"},
	}
	for _, c := range cases {
		if got := cleanTitle(c.title, c.fallback); got != c.want {
			t.Fatalf("cleanTitle(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestFakeScraperIsDeterministicAndOffline(t *testing.T) {
	f := FakeScraper{}
	p, err := f.Scrape(t.Context(), "https://meu-blog.com.br/sobre")
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if f.Name() != "firecrawl (simulated)" {
		t.Fatalf("name = %q", f.Name())
	}
	if p.Title == "" || p.Markdown == "" {
		t.Fatalf("empty fake page: %+v", p)
	}
	// deterministic: same URL → same page
	p2, _ := f.Scrape(t.Context(), "https://meu-blog.com.br/sobre")
	if p.Title != p2.Title || p.Markdown != p2.Markdown {
		t.Fatalf("fake scrape not deterministic: %+v vs %+v", p, p2)
	}
	if _, err := f.Scrape(t.Context(), ""); err == nil {
		t.Fatal("expected error for empty URL")
	}
}
