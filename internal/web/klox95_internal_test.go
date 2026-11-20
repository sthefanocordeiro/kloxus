package web

// KLOX-95 white-box tests: cardFlags derives the default-hidden
// traits purely from the publication's language/topic and the story's
// enrichment — no database needed.

import (
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func TestCardFlags(t *testing.T) {
	pub := &store.Publication{
		Language: "pt-BR", Topic: "tecnologia e IA",
		EditorialLine: "startups brasileiras",
	}
	cases := []struct {
		name                    string
		pub                     *store.Publication
		card                    *store.StoryCard
		wantForeign, wantOffTop bool
	}{
		{"no enrichment stays visible", pub, nil, false, false},
		{"empty card stays visible", pub, &store.StoryCard{}, false, false},
		{"en member vs pt pub is foreign", pub,
			&store.StoryCard{Language: "en", Topics: []string{"tecnologia"}}, true, false},
		{"pt-BR vs pt is not foreign", pub,
			&store.StoryCard{Language: "pt", Topics: []string{"tecnologia"}}, false, false},
		{"off-topic topics hide", pub,
			&store.StoryCard{Language: "pt", Topics: []string{"cripto", "games"}}, false, true},
		{"one matching topic keeps", pub,
			&store.StoryCard{Language: "pt", Topics: []string{"cripto", "startups"}}, false, false},
		{"topic-less card cannot be off-topic", pub,
			&store.StoryCard{Language: "pt"}, false, false},
		{"no declared topic cannot be off-topic",
			&store.Publication{Language: "pt"},
			&store.StoryCard{Language: "pt", Topics: []string{"cripto"}}, false, false},
		{"unknown pub language never hides", &store.Publication{Topic: "tecnologia"},
			&store.StoryCard{Language: "en", Topics: []string{"tecnologia"}}, false, false},
		{"foreign and off-topic can both hold", pub,
			&store.StoryCard{Language: "es", Topics: []string{"cripto"}}, true, true},
	}
	for _, tc := range cases {
		f, o := cardFlags(tc.pub, tc.card)
		if f != tc.wantForeign || o != tc.wantOffTop {
			t.Errorf("%s: flags = (%v,%v), want (%v,%v)",
				tc.name, f, o, tc.wantForeign, tc.wantOffTop)
		}
	}
}

func TestLangPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"pt-BR": "pt", "PT": "pt", "en": "en", "pt_BR": "pt", "": "", " es ": "es",
	} {
		if got := langPrefix(in); got != want {
			t.Errorf("langPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}
