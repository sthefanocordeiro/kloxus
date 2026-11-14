// Package config loads process configuration (env + .env) and
// publication definitions (YAML).
package config

import (
	"bufio"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds process-level settings sourced from env and .env.
type Config struct {
	Mode string // demo | real
	Port int
	// Bind is the interface the HTTP listener binds to. Loopback by
	// default so local dev and test runs never prompt the host firewall;
	// the deployed image sets KLOXUS_BIND=0.0.0.0 (the Dockerfile ENV)
	// because Fly routes to the machine's private address.
	Bind string
	// DatabaseURL is the Postgres connection string (DATABASE_URL) —
	// the Supabase direct or session-pooler address in production, a
	// local Postgres in development.
	DatabaseURL string
	OutDir      string
	// DataDir, when set, enables hosted multi-workspace mode: one
	// workspace row per visitor in the shared database, addressed by a
	// signed cookie. The directory itself only persists the cookie
	// signing secret.
	DataDir      string
	CookieSecret string

	AnthropicAPIKey string
	ModelTriage     string
	ModelDraft      string
	MaxDeepDives    int
	// LLMMode gates every model call: "auto" (default) runs the normal
	// pipeline; "off" runs the deterministic pipeline and any model call
	// attempted fails loudly (llm.None is the configured provider).
	LLMMode string
	MasterKey       string
	ResendAPIKey    string
	FromAddress     string
	FirecrawlAPIKey string
	AbacatePayKey   string
	PublicURL       string
	WebhookSecret   string
	// ResendWebhookSecret verifies Svix signatures on /webhooks/resend.
	ResendWebhookSecret string
	// EmailDailyCap bounds real sends across all workspaces per UTC day
	// (Resend free tier is 100/day); EmailWorkspaceDailyCap bounds one
	// workspace. Both are persisted in the outbox ledger.
	EmailDailyCap          int
	EmailWorkspaceDailyCap int
}

// Load reads .env (if present, without overriding real env vars) then env.
func Load() (*Config, error) {
	if f, err := os.Open(".env"); err == nil {
		for k, v := range parseDotEnv(f) {
			if _, exists := os.LookupEnv(k); !exists {
				os.Setenv(k, v)
			}
		}
		f.Close()
	}
	cfg := &Config{
		Mode:            getenv("KLOXUS_MODE", "demo"),
		Port:            getint("KLOXUS_PORT", 8080),
		Bind:            getenv("KLOXUS_BIND", "127.0.0.1"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		OutDir:          getenv("KLOXUS_OUT_DIR", "out"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		ModelTriage:     getenv("KLOXUS_MODEL_TRIAGE", "claude-haiku-5-5"),
		ModelDraft:      getenv("KLOXUS_MODEL_DRAFT", "claude-sonnet-5-5"),
		MaxDeepDives:    getint("KLOXUS_MAX_DEEP_DIVES", 3),
		LLMMode:         getenv("KLOXUS_LLM_MODE", "auto"),
		MasterKey:       os.Getenv("KLOXUS_MASTER_KEY"),
		ResendAPIKey:    os.Getenv("RESEND_API_KEY"),
		FromAddress:     os.Getenv("KLOXUS_FROM_ADDRESS"),
		FirecrawlAPIKey: os.Getenv("FIRECRAWL_API_KEY"),
		AbacatePayKey:   os.Getenv("ABACATEPAY_API_KEY"),
		PublicURL:       os.Getenv("KLOXUS_PUBLIC_URL"),
		WebhookSecret:   os.Getenv("KLOXUS_WEBHOOK_SECRET"),
		DataDir:         os.Getenv("KLOXUS_DATA_DIR"),
		CookieSecret:    os.Getenv("KLOXUS_COOKIE_SECRET"),

		ResendWebhookSecret:    os.Getenv("RESEND_WEBHOOK_SECRET"),
		EmailDailyCap:          getint("KLOXUS_EMAIL_DAILY_CAP", 80),
		EmailWorkspaceDailyCap: getint("KLOXUS_EMAIL_WS_DAILY_CAP", 25),
	}
	if cfg.Mode != "demo" && cfg.Mode != "real" {
		return nil, fmt.Errorf("KLOXUS_MODE must be \"demo\" or \"real\", got %q", cfg.Mode)
	}
	if cfg.LLMMode != "auto" && cfg.LLMMode != "off" {
		return nil, fmt.Errorf("KLOXUS_LLM_MODE must be \"auto\" or \"off\", got %q", cfg.LLMMode)
	}
	if cfg.ResendAPIKey != "" {
		if err := ValidFromAddress(cfg.FromAddress); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// LLMOff reports whether the deterministic no-model mode is on.
func (c *Config) LLMOff() bool { return c.LLMMode == "off" }

// ValidFromAddress requires the full "Name <local@domain>" form — a
// bare domain or bare address is a config error that would surface only
// when the first send is rejected (CTO note on KLOX-11).
func ValidFromAddress(s string) error {
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address == "" || !strings.Contains(a.Address, "@") {
		return fmt.Errorf("KLOXUS_FROM_ADDRESS must be a full \"Name <local@domain>\" sender address, got %q", s)
	}
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("KLOXUS_FROM_ADDRESS must include a display name (\"Name <local@domain>\"), got %q", s)
	}
	return nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getint(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}

func parseDotEnv(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		// Strip one pair of surrounding matching quotes, if present.
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}

// Section is one issue section from a publication definition.
type Section struct {
	Title      string `yaml:"title" json:"title"`
	Items      int    `yaml:"items" json:"items"`
	Commentary string `yaml:"commentary" json:"commentary"` // required | optional | none
}

// SourceDef is one collection source: a name, an RSS feed or an open web page.
type SourceDef struct {
	Name string `yaml:"name" json:"name"`
	Type string `yaml:"type" json:"type"` // rss | page
	URL  string `yaml:"url" json:"url"`
}

// Paywall holds the paid-issue settings.
type Paywall struct {
	Enabled      bool     `yaml:"enabled" json:"enabled"`
	PaidSections []string `yaml:"paid_sections" json:"paid_sections"`
	Price        string   `yaml:"price" json:"price"`
}

// Publication is a creator's publication definition.
type Publication struct {
	Slug          string      `yaml:"slug" json:"slug"`
	Name          string      `yaml:"name" json:"name"`
	Language      string      `yaml:"language" json:"language"`
	Topic         string      `yaml:"topic" json:"topic"`
	Audience      string      `yaml:"audience" json:"audience"`
	EditorialLine string      `yaml:"editorial_line" json:"editorial_line"`
	Tone          string      `yaml:"tone" json:"tone"`
	Frequency     string      `yaml:"frequency" json:"frequency"`
	Sections      []Section   `yaml:"sections" json:"sections"`
	Sources       []SourceDef `yaml:"sources" json:"sources"`
	Paywall       Paywall     `yaml:"paywall" json:"paywall"`
	AIDisclosure  string      `yaml:"ai_disclosure" json:"ai_disclosure"` // none | short | detailed
}

// ParsePublication decodes and validates a publication YAML document.
func ParsePublication(data []byte) (*Publication, error) {
	var p Publication
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing publication yaml: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate fills defaults and rejects invalid definitions.
func (p *Publication) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("publication: name is required")
	}
	if p.Slug == "" {
		p.Slug = slugify(p.Name)
	}
	if len(p.Sections) == 0 {
		return fmt.Errorf("publication %q: at least one section is required", p.Name)
	}
	for _, s := range p.Sections {
		switch s.Commentary {
		case "required", "optional", "none":
		default:
			return fmt.Errorf("publication %q: section %q has invalid commentary %q (want required|optional|none)", p.Name, s.Title, s.Commentary)
		}
	}
	for _, src := range p.Sources {
		if src.Type != "rss" && src.Type != "page" {
			return fmt.Errorf("publication %q: source %q has invalid type %q (want rss|page)", p.Name, src.URL, src.Type)
		}
		if strings.TrimSpace(src.URL) == "" {
			return fmt.Errorf("publication %q: source with empty url", p.Name)
		}
	}
	switch p.AIDisclosure {
	case "", "none", "short", "detailed":
	default:
		return fmt.Errorf("publication %q: invalid ai_disclosure %q (want none|short|detailed)", p.Name, p.AIDisclosure)
	}
	return nil
}

var accents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a",
	"é", "e", "ê", "e",
	"í", "i",
	"ó", "o", "ô", "o", "õ", "o",
	"ú", "u", "ü", "u",
	"ç", "c",
)

func slugify(s string) string {
	s = accents.Replace(strings.ToLower(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		switch {
		case ok:
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
