// Package config loads process configuration (env + .env) and
// publication definitions (YAML).
package config

import (
	"bufio"
	"fmt"
	"io"
	"net/mail"
	"net/netip"
	"net/url"
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
	// Spend guards (D32/D36/D40): per-workspace daily run budget, per-run
	// token budget, per-run dollar cap, global daily dollar cap and the
	// kill-switch env — all persisted in Postgres so restarts keep the
	// counters.
	RunsPerDay   int
	TokensPerRun int
	LLMRunCap    float64
	LLMDailyCap  float64
	// Paid-action caps (KLOX-67 §7.3): the user-triggered model calls —
	// story research, citation check, editorial rerank — are bounded per
	// workspace per UTC day; rerank and citecheck carry their own
	// per-newsletter / per-edition limits.
	PaidActionsPerDay    int
	ReranksPerDay        int
	CiteChecksPerEdition int
	LLMKillEnv           bool   // KLOXUS_LLM_KILL at boot; also checked live per call
	AdminToken           string // bearer token for /api/v1/admin/*
	MasterKey            string
	ResendAPIKey         string
	FromAddress          string
	// FakeResendURL points the mail transport at a literal-loopback fake
	// provider (scripts/fake-resend.mjs) — verification only. The sender
	// carries a synthetic credential, follows no redirects and falls back
	// to nothing: the value is validated at Load and an invalid one stops
	// the process. Mutually exclusive with RESEND_API_KEY — the fake
	// never touches a real credential. Never set on a deployed app.
	FakeResendURL   string
	FirecrawlAPIKey string
	// ScrapesPerDay bounds Firecrawl scrapes (the "paste a link"
	// proposal flow, KLOX-99) per workspace per UTC day — persisted in
	// the paid_actions ledger like the §7.3 AI counters.
	ScrapesPerDay int
	AbacatePayKey string
	// AbacatePayWebhookSecret verifies /webhooks/abacatepay deliveries:
	// the dashboard sends it as X-Webhook-Secret / ?webhookSecret=
	// (KLOX-13). Empty fails the endpoint closed like the other
	// receivers.
	AbacatePayWebhookSecret string
	PublicURL               string
	WebhookSecret           string
	// ResendWebhookSecret verifies Svix signatures on /webhooks/resend.
	ResendWebhookSecret string
	// EmailDailyCap bounds real sends across all workspaces per UTC day
	// (Resend free tier is 100/day); EmailWorkspaceDailyCap bounds one
	// workspace. Both are persisted in the outbox ledger.
	EmailDailyCap          int
	EmailWorkspaceDailyCap int

	// Ingest governs the global source scheduler (D29). IngestEnabled
	// starts the poll loop (real mode defaults it on, demo off).
	IngestEnabled bool
	// DailyJournalEnabled polls the Daily Journal public API. Off by
	// default since KLOX-114 (owner decision — DJ left the catalog
	// again); KLOXUS_INGEST_DAILY_JOURNAL=1 opts back in when a
	// 'daily-journal' catalog row is seeded.
	DailyJournalEnabled bool

	// PublishingEnabled turns the reader-facing product back on:
	// sending to readers, outbox, subscribe/readers, public /p pages,
	// paywall and payments (KLOX-122, pivot to editor de pauta).
	// KLOXUS_PUBLISHING=on re-enables; the default is off. Code and
	// tables stay - this only hides the surface.
	PublishingEnabled bool

	// DailyPautaMail turns the 07:00 "stories ready" mail on
	// (KLOXUS_DAILY_PAUTA_MAIL, KLOX-256). Off by default — the deployed
	// app sets it on in fly.toml. The 06:30 build and transactional
	// mail (magic links) run either way.
	DailyPautaMail bool

	// Ranking engine (KLOX-108). VoyageKey is the Fly secret
	// VOYAGE_API_SECRET - never logged. Limits default to half the
	// account's limits; caps are daily USD per model group.
	VoyageKey       string
	EmbedProvider   string // KLOXUS_EMBED_PROVIDER voyage|off
	EmbedModel      string // KLOXUS_EMBED_MODEL
	VoyageEmbedRPM  int
	VoyageEmbedTPM  int
	VoyageRerankRPM int
	VoyageRerankTPM int
	EmbedDailyUSD   float64
	RerankDailyUSD  float64
	RerankProvider  string  // KLOXUS_RERANK_PROVIDER voyage|off (KLOX-110)
	RerankModel     string  // KLOXUS_RERANK_MODEL
	RerankK         int     // KLOXUS_RERANK_K shortlist size
	RerankFloor     float64 // KLOXUS_RERANK_FLOOR; 0 disables the cut
}

// envVar is one row of the declarative env→Config table: the variable
// name, its fallback and the Config field it fills. An empty def means
// "no fallback" — secrets and credentials stay empty unless set.
type envVar struct {
	key string
	def string
	dst func(*Config) *string
}

type envIntVar struct {
	key string
	def int
	dst func(*Config) *int
}

type envFloatVar struct {
	key string
	def float64
	dst func(*Config) *float64
}

var envStrings = []envVar{
	{"KLOXUS_MODE", "demo", func(c *Config) *string { return &c.Mode }},
	{"KLOXUS_BIND", "127.0.0.1", func(c *Config) *string { return &c.Bind }},
	{"DATABASE_URL", "", func(c *Config) *string { return &c.DatabaseURL }},
	{"KLOXUS_OUT_DIR", "out", func(c *Config) *string { return &c.OutDir }},
	{"ANTHROPIC_API_KEY", "", func(c *Config) *string { return &c.AnthropicAPIKey }},
	{"KLOXUS_MODEL_TRIAGE", "claude-haiku-5-5", func(c *Config) *string { return &c.ModelTriage }},
	{"KLOXUS_MODEL_DRAFT", "claude-haiku-5-5", func(c *Config) *string { return &c.ModelDraft }},
	{"KLOXUS_LLM_MODE", "auto", func(c *Config) *string { return &c.LLMMode }},
	{"KLOXUS_ADMIN_TOKEN", "", func(c *Config) *string { return &c.AdminToken }},
	{"KLOXUS_MASTER_KEY", "", func(c *Config) *string { return &c.MasterKey }},
	{"RESEND_API_KEY", "", func(c *Config) *string { return &c.ResendAPIKey }},
	{"KLOXUS_FROM_ADDRESS", "", func(c *Config) *string { return &c.FromAddress }},
	{"KLOXUS_FAKE_RESEND_URL", "", func(c *Config) *string { return &c.FakeResendURL }},
	{"FIRECRAWL_API_KEY", "", func(c *Config) *string { return &c.FirecrawlAPIKey }},
	{"ABACATEPAY_API_KEY", "", func(c *Config) *string { return &c.AbacatePayKey }},
	{"ABACATEPAY_WEBHOOK_SECRET", "", func(c *Config) *string { return &c.AbacatePayWebhookSecret }},
	{"VOYAGE_API_SECRET", "", func(c *Config) *string { return &c.VoyageKey }},
	{"KLOXUS_EMBED_PROVIDER", "voyage", func(c *Config) *string { return &c.EmbedProvider }},
	{"KLOXUS_EMBED_MODEL", "voyage-4-lite", func(c *Config) *string { return &c.EmbedModel }},
	{"KLOXUS_RERANK_PROVIDER", "voyage", func(c *Config) *string { return &c.RerankProvider }},
	{"KLOXUS_RERANK_MODEL", "rerank-2.5-lite", func(c *Config) *string { return &c.RerankModel }},
	{"KLOXUS_PUBLIC_URL", "", func(c *Config) *string { return &c.PublicURL }},
	{"KLOXUS_WEBHOOK_SECRET", "", func(c *Config) *string { return &c.WebhookSecret }},
	{"KLOXUS_DATA_DIR", "", func(c *Config) *string { return &c.DataDir }},
	{"KLOXUS_COOKIE_SECRET", "", func(c *Config) *string { return &c.CookieSecret }},
	{"RESEND_WEBHOOK_SECRET", "", func(c *Config) *string { return &c.ResendWebhookSecret }},
}

var envInts = []envIntVar{
	{"KLOXUS_PORT", 8080, func(c *Config) *int { return &c.Port }},
	{"KLOXUS_MAX_DEEP_DIVES", 3, func(c *Config) *int { return &c.MaxDeepDives }},
	{"KLOXUS_RUNS_PER_DAY", 2, func(c *Config) *int { return &c.RunsPerDay }},
	{"KLOXUS_TOKENS_PER_RUN", 200000, func(c *Config) *int { return &c.TokensPerRun }},
	{"KLOXUS_PAID_ACTIONS_PER_DAY", 5, func(c *Config) *int { return &c.PaidActionsPerDay }},
	{"KLOXUS_RERANKS_PER_DAY", 2, func(c *Config) *int { return &c.ReranksPerDay }},
	{"KLOXUS_CITECHECKS_PER_EDITION", 3, func(c *Config) *int { return &c.CiteChecksPerEdition }},
	{"KLOXUS_SCRAPES_PER_DAY", 5, func(c *Config) *int { return &c.ScrapesPerDay }},
	{"KLOXUS_VOYAGE_EMBED_RPM", 1000, func(c *Config) *int { return &c.VoyageEmbedRPM }},
	{"KLOXUS_VOYAGE_EMBED_TPM", 8000000, func(c *Config) *int { return &c.VoyageEmbedTPM }},
	{"KLOXUS_VOYAGE_RERANK_RPM", 1000, func(c *Config) *int { return &c.VoyageRerankRPM }},
	{"KLOXUS_VOYAGE_RERANK_TPM", 2000000, func(c *Config) *int { return &c.VoyageRerankTPM }},
	{"KLOXUS_RERANK_K", 100, func(c *Config) *int { return &c.RerankK }},
	{"KLOXUS_EMAIL_DAILY_CAP", 80, func(c *Config) *int { return &c.EmailDailyCap }},
	{"KLOXUS_EMAIL_WS_DAILY_CAP", 25, func(c *Config) *int { return &c.EmailWorkspaceDailyCap }},
}

var envFloats = []envFloatVar{
	{"KLOXUS_LLM_RUN_CAP_USD", 0.25, func(c *Config) *float64 { return &c.LLMRunCap }},
	{"KLOXUS_LLM_DAILY_CAP_USD", 1.00, func(c *Config) *float64 { return &c.LLMDailyCap }},
	{"KLOXUS_EMBED_DAILY_USD", 0.05, func(c *Config) *float64 { return &c.EmbedDailyUSD }},
	{"KLOXUS_RERANK_DAILY_USD", 0.10, func(c *Config) *float64 { return &c.RerankDailyUSD }},
	{"KLOXUS_RERANK_FLOOR", 0, func(c *Config) *float64 { return &c.RerankFloor }},
}

// Load reads .env (if present, without overriding real env vars) then env.
// KLOXUS_NO_DOTENV skips the file entirely — verification runs set it so an
// inherited .env can never leak credentials or endpoints into the process.
func Load() (*Config, error) {
	loadDotEnv()
	cfg := &Config{}
	for _, e := range envStrings {
		*e.dst(cfg) = getenv(e.key, e.def)
	}
	for _, e := range envInts {
		*e.dst(cfg) = getint(e.key, e.def)
	}
	for _, e := range envFloats {
		*e.dst(cfg) = getfloat(e.key, e.def)
	}
	cfg.LLMKillEnv = os.Getenv("KLOXUS_LLM_KILL") != ""
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.applyDerived()
	return cfg, nil
}

// loadDotEnv folds .env into the process environment without overriding
// variables that are already set.
func loadDotEnv() {
	if os.Getenv("KLOXUS_NO_DOTENV") != "" {
		return
	}
	f, err := os.Open(".env")
	if err != nil {
		return
	}
	defer f.Close()
	for k, v := range parseDotEnv(f) {
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

// validate rejects combinations no run can survive: an unknown mode and
// a mail transport that cannot be trusted end to end.
func (c *Config) validate() error {
	if c.Mode != "demo" && c.Mode != "real" {
		return fmt.Errorf("KLOXUS_MODE must be \"demo\" or \"real\", got %q", c.Mode)
	}
	if c.LLMMode != "auto" && c.LLMMode != "off" {
		return fmt.Errorf("KLOXUS_LLM_MODE must be \"auto\" or \"off\", got %q", c.LLMMode)
	}
	if c.ResendAPIKey != "" {
		if err := ValidFromAddress(c.FromAddress); err != nil {
			return err
		}
	}
	if c.FakeResendURL != "" {
		if c.ResendAPIKey != "" {
			return fmt.Errorf("KLOXUS_FAKE_RESEND_URL and RESEND_API_KEY are mutually exclusive — the verification transport never carries a real credential")
		}
		if err := ValidateLoopbackEndpoint(c.FakeResendURL); err != nil {
			return fmt.Errorf("KLOXUS_FAKE_RESEND_URL: %w", err)
		}
		if err := ValidFromAddress(c.FromAddress); err != nil {
			return err
		}
	}
	return nil
}

// applyDerived resolves the env-driven feature flags that depend on the
// parsed mode or carry multi-value on/off spellings.
func (c *Config) applyDerived() {
	// Real mode ingests by default; KLOXUS_INGEST=0/opt-out disables it.
	// Demo mode stays fixture-only unless explicitly opted in.
	c.IngestEnabled = c.Mode == "real"
	if v := os.Getenv("KLOXUS_INGEST"); v != "" {
		c.IngestEnabled = v == "1" || v == "true" || v == "yes"
	}
	// Daily Journal is opt-in since KLOX-114 — off by default;
	// KLOXUS_INGEST_DAILY_JOURNAL=1 re-enables it for a re-seeded
	// 'daily-journal' catalog row.
	c.DailyJournalEnabled = false
	if v := os.Getenv("KLOXUS_INGEST_DAILY_JOURNAL"); v != "" {
		c.DailyJournalEnabled = v == "1" || v == "true" || v == "yes"
	}
	c.PublishingEnabled = false
	switch strings.ToLower(os.Getenv("KLOXUS_PUBLISHING")) {
	case "on", "1", "true", "yes":
		c.PublishingEnabled = true
	}
	// The morning mail is opt-in like publishing (KLOX-256): unset
	// keeps it off everywhere; the hosted fly.toml sets it on.
	c.DailyPautaMail = false
	switch strings.ToLower(os.Getenv("KLOXUS_DAILY_PAUTA_MAIL")) {
	case "on", "1", "true", "yes":
		c.DailyPautaMail = true
	}
}

// ValidateLoopbackEndpoint restricts a URL to a literal loopback HTTP
// endpoint — no userinfo, query, fragment or path — so a verification
// transport can never be pointed at (or leak to) a remote host. The host
// must be a loopback IP literal (127.0.0.0/8 or ::1); a DNS name such as
// "localhost" is not literal proof and is rejected.
func ValidateLoopbackEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "http" {
		return fmt.Errorf("must be a plain http URL, got scheme %q", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("must not carry userinfo")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("must not carry a query or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("must not carry a path, got %q", u.Path)
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsLoopback() {
		return fmt.Errorf("host %q is not a literal loopback address", u.Hostname())
	}
	return nil
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

func getfloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
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

// Section is one issue section from a publication definition. ID is the
// section's stable identity — it survives renames so past editions and
// paywall rules never dangle (KLOX-65). Format shapes the section's
// item rendering: featured | news | links. Access: all | subscribers.
type Section struct {
	ID         string `yaml:"id" json:"id"`
	Title      string `yaml:"title" json:"title"`
	Format     string `yaml:"format" json:"format"` // featured | news | links
	Items      int    `yaml:"items" json:"items"`
	Commentary string `yaml:"commentary" json:"commentary"` // required | optional | none
	Access     string `yaml:"access" json:"access"`         // all | subscribers
	// Topic is the creator's "que tipo de pauta entra aqui?" — free-text
	// topics/keywords the aderência ranking matches stories against
	// when filling this section's slots (KLOX-94). Empty sections keep
	// the global best-first order.
	Topic string `yaml:"topic" json:"topic"`
}

// Section formats and access levels the builder offers (KLOX-65).
const (
	SectionFeatured   = "featured"
	SectionNews       = "news"
	SectionLinks      = "links"
	AccessAll         = "all"
	AccessSubscribers = "subscribers"
)

// NormalizeSections fills the fields older stored rows and YAML
// definitions never carried: a positional id (stable across renames —
// it follows the slot, not the title), a format inferred from the
// well-known template titles, and the access level the paywall's
// paid_sections list used to encode by title.
func NormalizeSections(secs []Section, paidSections []string) []Section {
	paid := map[string]bool{}
	for _, t := range paidSections {
		paid[t] = true
	}
	out := make([]Section, len(secs))
	for i, s := range secs {
		if s.ID == "" {
			s.ID = fmt.Sprintf("sec-%d", i+1)
		}
		if s.Format == "" {
			s.Format = inferSectionFormat(s.Title)
		}
		if s.Access == "" {
			if paid[s.Title] {
				s.Access = AccessSubscribers
			} else {
				s.Access = AccessAll
			}
		}
		out[i] = s
	}
	return out
}

// inferSectionFormat maps the legacy template titles onto a format —
// the featured slot and the links bucket read differently in the
// skeleton preview and the edition. Everything else is a news section.
func inferSectionFormat(title string) string {
	switch t := strings.ToLower(strings.TrimSpace(title)); {
	case t == "o destaque" || t == "the big one":
		return SectionFeatured
	case strings.Contains(t, "links"):
		return SectionLinks
	default:
		return SectionNews
	}
}

// ValidSectionFormat reports whether f is a known section format.
func ValidSectionFormat(f string) bool {
	return f == SectionFeatured || f == SectionNews || f == SectionLinks
}

// ValidSectionAccess reports whether a is a known access level.
func ValidSectionAccess(a string) bool {
	return a == AccessAll || a == AccessSubscribers
}

// PaidSectionTitles derives the paywall's title list from the
// per-section access level — the field that used to carry it by hand.
func PaidSectionTitles(secs []Section) []string {
	var out []string
	for _, s := range secs {
		if s.Access == AccessSubscribers {
			out = append(out, s.Title)
		}
	}
	return out
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
	// RejectTerms lists the themes the editorial line excludes —
	// comma-separated terms the aderência ranking demotes and the
	// auto-lineup never slots (KLOX-94).
	RejectTerms string `yaml:"reject_terms" json:"reject_terms"`
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

// PostSectionTitle names the single section a Post lives in (KLOX-157):
// a post is the ordered list of stories the author picked, with no
// template, no slots and no required commentary. The title is internal
// plumbing — exports and the editor never print it.
const PostSectionTitle = "Notícias"

// PostSections is the one-section snapshot every new Post is created
// with. Capacity is generous because the author, not a template,
// decides how many stories the post holds.
func PostSections() []Section {
	return []Section{{
		ID: "post", Title: PostSectionTitle, Format: SectionNews,
		Items: 50, Commentary: "none", Access: AccessAll,
	}}
}
