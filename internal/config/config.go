// Package config loads process configuration (env + .env) and
// publication definitions (YAML).
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds process-level settings sourced from env and .env.
type Config struct {
	Mode   string // demo | real
	Port   int
	DBPath string
	OutDir string

	AnthropicAPIKey string
	ModelTriage     string
	ModelDraft      string
	MaxDeepDives    int
	MasterKey       string
	ResendAPIKey    string
	FromAddress     string
	FirecrawlAPIKey string
	AbacatePayKey   string
	PublicURL       string
	WebhookSecret   string

	DemoResetInterval time.Duration
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
		Mode:              getenv("KLOXUS_MODE", "demo"),
		Port:              getint("KLOXUS_PORT", 8080),
		DBPath:            getenv("KLOXUS_DB_PATH", "kloxus.db"),
		OutDir:            getenv("KLOXUS_OUT_DIR", "out"),
		AnthropicAPIKey:   os.Getenv("ANTHROPIC_API_KEY"),
		ModelTriage:       getenv("KLOXUS_MODEL_TRIAGE", "claude-haiku-5-5"),
		ModelDraft:        getenv("KLOXUS_MODEL_DRAFT", "claude-sonnet-5-5"),
		MaxDeepDives:      getint("KLOXUS_MAX_DEEP_DIVES", 3),
		MasterKey:         os.Getenv("KLOXUS_MASTER_KEY"),
		ResendAPIKey:      os.Getenv("RESEND_API_KEY"),
		FromAddress:       os.Getenv("KLOXUS_FROM_ADDRESS"),
		FirecrawlAPIKey:   os.Getenv("FIRECRAWL_API_KEY"),
		AbacatePayKey:     os.Getenv("ABACATEPAY_API_KEY"),
		PublicURL:         os.Getenv("KLOXUS_PUBLIC_URL"),
		WebhookSecret:     os.Getenv("KLOXUS_WEBHOOK_SECRET"),
		DemoResetInterval: getduration("KLOXUS_DEMO_RESET_INTERVAL", 0),
	}
	if cfg.Mode != "demo" && cfg.Mode != "real" {
		return nil, fmt.Errorf("KLOXUS_MODE must be \"demo\" or \"real\", got %q", cfg.Mode)
	}
	return cfg, nil
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

func getduration(key string, fallback time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
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
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

// Section is one issue section from a publication definition.
type Section struct {
	Title      string `yaml:"title"`
	Items      int    `yaml:"items"`
	Commentary string `yaml:"commentary"` // required | optional | none
}

// SourceDef is one collection source: an RSS feed or an open web page.
type SourceDef struct {
	Type string `yaml:"type"` // rss | page
	URL  string `yaml:"url"`
}

// Paywall holds the paid-issue settings.
type Paywall struct {
	Enabled      bool     `yaml:"enabled"`
	PaidSections []string `yaml:"paid_sections"`
}

// Publication is a creator's publication definition.
type Publication struct {
	Slug          string      `yaml:"slug"`
	Name          string      `yaml:"name"`
	Language      string      `yaml:"language"`
	Topic         string      `yaml:"topic"`
	Audience      string      `yaml:"audience"`
	EditorialLine string      `yaml:"editorial_line"`
	Tone          string      `yaml:"tone"`
	Frequency     string      `yaml:"frequency"`
	Sections      []Section   `yaml:"sections"`
	Sources       []SourceDef `yaml:"sources"`
	Paywall       Paywall     `yaml:"paywall"`
	AIDisclosure  string      `yaml:"ai_disclosure"` // none | short | detailed
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
