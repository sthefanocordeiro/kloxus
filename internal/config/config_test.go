package config

import (
	"strings"
	"testing"
)

const validYAML = `
name: Futuro Próximo
sections:
  - title: The big one
    items: 1
    commentary: required
sources:
  - type: rss
    url: fixtures/feeds/radar-tech.xml
paywall:
  enabled: true
  paid_sections: [The big one]
ai_disclosure: short
`

func TestParsePublicationValid(t *testing.T) {
	p, err := ParsePublication([]byte(validYAML))
	if err != nil {
		t.Fatalf("ParsePublication: %v", err)
	}
	if p.Slug != "futuro-proximo" {
		t.Errorf("slug = %q, want futuro-proximo", p.Slug)
	}
	if p.Sections[0].Commentary != "required" {
		t.Errorf("section commentary = %q", p.Sections[0].Commentary)
	}
	if !p.Paywall.Enabled || len(p.Paywall.PaidSections) != 1 {
		t.Errorf("paywall = %+v", p.Paywall)
	}
}

func TestParsePublicationRequiresName(t *testing.T) {
	y := `sections: [{title: x, items: 1, commentary: none}]`
	if _, err := ParsePublication([]byte(y)); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParsePublicationValidatesFields(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"no sections", "name: x\n"},
		{"bad commentary", "name: x\nsections: [{title: s, items: 1, commentary: sometimes}]"},
		{"bad source type", "name: x\nsections: [{title: s, items: 1, commentary: none}]\nsources: [{type: pigeon, url: u}]"},
		{"bad disclosure", "name: x\nsections: [{title: s, items: 1, commentary: none}]\nai_disclosure: vague"},
	}
	for _, c := range cases {
		if _, err := ParsePublication([]byte(c.yaml)); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestParseDotEnv(t *testing.T) {
	env := parseDotEnv(strings.NewReader(`
# comment
KLOXUS_PORT=9090
QUOTED="hello world"
SINGLE='x'
BROKEN_LINE
EMPTY=
`))
	if env["KLOXUS_PORT"] != "9090" {
		t.Errorf("KLOXUS_PORT = %q", env["KLOXUS_PORT"])
	}
	if env["QUOTED"] != "hello world" {
		t.Errorf("QUOTED = %q", env["QUOTED"])
	}
	if env["SINGLE"] != "x" {
		t.Errorf("SINGLE = %q", env["SINGLE"])
	}
	if _, ok := env["BROKEN_LINE"]; ok {
		t.Error("BROKEN_LINE should be skipped")
	}
	if v, ok := env["EMPTY"]; !ok || v != "" {
		t.Errorf("EMPTY = %q present=%v", v, ok)
	}
}

func TestLLMMode(t *testing.T) {
	t.Setenv("KLOXUS_LLM_MODE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLMMode != "auto" || cfg.LLMOff() {
		t.Fatalf("default LLMMode = %q, want auto", cfg.LLMMode)
	}
	t.Setenv("KLOXUS_LLM_MODE", "off")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load(off): %v", err)
	}
	if !cfg.LLMOff() {
		t.Fatal("LLMOff() = false with KLOXUS_LLM_MODE=off")
	}
	t.Setenv("KLOXUS_LLM_MODE", "cheap")
	if _, err := Load(); err == nil {
		t.Fatal("expected validation error for KLOXUS_LLM_MODE=cheap")
	}
}

func TestBind(t *testing.T) {
	t.Setenv("KLOXUS_BIND", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Bind != "127.0.0.1" {
		t.Fatalf("default Bind = %q, want 127.0.0.1", cfg.Bind)
	}
	t.Setenv("KLOXUS_BIND", "0.0.0.0")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Bind != "0.0.0.0" {
		t.Fatalf("Bind = %q, want 0.0.0.0", cfg.Bind)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Futuro Próximo":  "futuro-proximo",
		"Café & Código!":  "cafe-codigo",
		"  Açúcar  ":      "acucar",
		"already-slugged": "already-slugged",
		"São Paulo -- SP": "sao-paulo-sp",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
