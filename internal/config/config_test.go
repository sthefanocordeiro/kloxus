package config

import (
	"os"
	"path/filepath"
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

// TestDailyJournalDefault pins the KLOX-114 regression: the Daily
// Journal is opt-in. Unset/empty env keeps it off (the KLOX-93
// default-on is what flooded the catalog with general politics);
// =1/true/yes opt back in for a re-seeded catalog row.
func TestDailyJournalDefault(t *testing.T) {
	t.Setenv("KLOXUS_INGEST_DAILY_JOURNAL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DailyJournalEnabled {
		t.Fatal("DailyJournalEnabled = true by default, want opt-in off")
	}
	for _, v := range []string{"1", "true", "yes"} {
		t.Setenv("KLOXUS_INGEST_DAILY_JOURNAL", v)
		cfg, err = Load()
		if err != nil {
			t.Fatalf("Load(%q): %v", v, err)
		}
		if !cfg.DailyJournalEnabled {
			t.Errorf("DailyJournalEnabled = false with KLOXUS_INGEST_DAILY_JOURNAL=%q", v)
		}
	}
	t.Setenv("KLOXUS_INGEST_DAILY_JOURNAL", "0")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load(0): %v", err)
	}
	if cfg.DailyJournalEnabled {
		t.Error("DailyJournalEnabled = true with =0")
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

// TestFakeResendConfig pins the verification-only boundary
// (KLOX-32): the fake endpoint is validated at Load, is mutually
// exclusive with a real credential and still needs a valid From.
func TestFakeResendConfig(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("KLOXUS_FROM_ADDRESS", "Kloxus <n@x.com>")

	t.Setenv("KLOXUS_FAKE_RESEND_URL", "http://127.0.0.1:8399")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with valid fake endpoint: %v", err)
	}
	if cfg.FakeResendURL != "http://127.0.0.1:8399" {
		t.Fatalf("FakeResendURL = %q", cfg.FakeResendURL)
	}

	// A real key next to the fake endpoint is ambiguous — refuse
	// startup rather than decide where credentials might go.
	t.Setenv("RESEND_API_KEY", "re_real_key")
	if _, err := Load(); err == nil {
		t.Fatal("expected mutual-exclusion error for RESEND_API_KEY + KLOXUS_FAKE_RESEND_URL")
	}
	t.Setenv("RESEND_API_KEY", "")

	for _, bad := range []string{
		"https://api.resend.com", "http://example.com", "http://localhost:8399",
		"http://user:pw@127.0.0.1:8399", "http://127.0.0.1:8399/?q=1",
		"http://127.0.0.1:8399/emails", "https://127.0.0.1:8399", "garbage",
	} {
		t.Setenv("KLOXUS_FAKE_RESEND_URL", bad)
		if _, err := Load(); err == nil {
			t.Errorf("expected startup rejection for KLOXUS_FAKE_RESEND_URL=%q", bad)
		}
	}

	// The fake still needs a proper sender name.
	t.Setenv("KLOXUS_FAKE_RESEND_URL", "http://127.0.0.1:8399")
	t.Setenv("KLOXUS_FROM_ADDRESS", "bare-domain.com")
	if _, err := Load(); err == nil {
		t.Fatal("expected From validation under the fake transport")
	}
}

// TestNoDotenv proves KLOXUS_NO_DOTENV keeps an inherited .env out of
// the process: verification servers run with it so stray local config
// cannot leak credentials or endpoints in.
func TestNoDotenv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("KLOXUS_LLM_MODE=off\nRESEND_API_KEY=re_leak\nKLOXUS_FROM_ADDRESS=Kloxus <n@x.com>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	// These must be genuinely unset — t.Setenv with "" still counts as
	// set for .env's "don't override real env" rule.
	for _, k := range []string{"KLOXUS_LLM_MODE", "RESEND_API_KEY", "KLOXUS_NO_DOTENV"} {
		if v, had := os.LookupEnv(k); had {
			os.Unsetenv(k)
			t.Cleanup(func() { os.Setenv(k, v) })
		}
	}

	t.Setenv("KLOXUS_NO_DOTENV", "1")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLMMode != "auto" || cfg.ResendAPIKey != "" {
		t.Fatalf("NO_DOTENV leaked .env: llm=%q resend=%q", cfg.LLMMode, cfg.ResendAPIKey)
	}

	// Without the guard, .env fills in what real env does not define —
	// but never overrides it.
	os.Unsetenv("KLOXUS_NO_DOTENV")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLMMode != "off" || cfg.ResendAPIKey != "re_leak" {
		t.Fatalf("expected .env values to load, got llm=%q resend=%q", cfg.LLMMode, cfg.ResendAPIKey)
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

// TestEnvTable walks the declarative env→Config tables: with every key
// empty each field lands on its declared fallback, no-fallback rows
// stay empty, set values land, and unparseable int/float input falls
// back instead of failing Load.
func TestEnvTable(t *testing.T) {
	clear := func(t *testing.T) {
		t.Setenv("KLOXUS_NO_DOTENV", "1")
		for _, e := range envStrings {
			t.Setenv(e.key, "")
		}
		for _, e := range envInts {
			t.Setenv(e.key, "")
		}
		for _, e := range envFloats {
			t.Setenv(e.key, "")
		}
	}

	t.Run("defaults", func(t *testing.T) {
		clear(t)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		for _, e := range envStrings {
			if got := *e.dst(cfg); got != e.def {
				t.Errorf("%s = %q, want default %q", e.key, got, e.def)
			}
		}
		for _, e := range envInts {
			if got := *e.dst(cfg); got != e.def {
				t.Errorf("%s = %d, want default %d", e.key, got, e.def)
			}
		}
		for _, e := range envFloats {
			if got := *e.dst(cfg); got != e.def {
				t.Errorf("%s = %v, want default %v", e.key, got, e.def)
			}
		}
	})

	t.Run("env overrides", func(t *testing.T) {
		clear(t)
		t.Setenv("KLOXUS_PORT", "9090")
		t.Setenv("KLOXUS_MASTER_KEY", "mk-1")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Port != 9090 {
			t.Errorf("Port = %d, want 9090", cfg.Port)
		}
		if cfg.MasterKey != "mk-1" {
			t.Errorf("MasterKey = %q, want mk-1", cfg.MasterKey)
		}
	})

	t.Run("invalid parse falls back", func(t *testing.T) {
		clear(t)
		t.Setenv("KLOXUS_PORT", "not-a-number")
		t.Setenv("KLOXUS_LLM_RUN_CAP_USD", "lots")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Port != 8080 {
			t.Errorf("Port = %d, want fallback 8080", cfg.Port)
		}
		if cfg.LLMRunCap != 0.25 {
			t.Errorf("LLMRunCap = %v, want fallback 0.25", cfg.LLMRunCap)
		}
	})
}

func TestPublishingFlagDefaultsOff(t *testing.T) {
	t.Setenv("KLOXUS_NO_DOTENV", "1")
	t.Setenv("KLOXUS_PUBLISHING", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublishingEnabled {
		t.Fatal("publishing must default to off (KLOX-122)")
	}
	t.Setenv("KLOXUS_PUBLISHING", "on")
	if cfg, _ = Load(); !cfg.PublishingEnabled {
		t.Fatal("KLOXUS_PUBLISHING=on must enable publishing")
	}
}
