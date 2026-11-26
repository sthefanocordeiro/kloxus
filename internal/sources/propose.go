package sources

// Deterministic proposal extraction (KLOX-99): a scraped newsletter or
// blog page becomes a suggested publication definition — name, topic,
// sections, editorial-line starter — that the creator reviews and edits
// before creating anything. No model call ever runs here: the product
// rule is that proposal text is extracted or templated, never
// AI-written, so the flow works under llm_mode=off and costs nothing
// beyond the scrape itself.
import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sthefanocordeiro/kloxus/internal/config"
)

// ProposedPublication is the suggestion a scraped page yields — the
// same blocks the create screen shows editable before creation.
type ProposedPublication struct {
	Name          string
	Topic         string
	Audience      string
	EditorialLine string
	Tone          string
	Language      string // "pt-BR" | "en"
	Frequency     string
	AIDisclosure  string
	Sections      []config.Section
	// Warnings carry stable codes the client localizes — honest about
	// what the page gave up (thin content → a generic skeleton).
	Warnings []string
	// Words is the rough word count of the extracted markdown — shown
	// in the "what we extracted" preview.
	Words int
}

// MinWords is the readable-text floor: a page with less than this reads
// as a sign-in wall or a failed render, so the handler refuses with
// scrape_blocked instead of proposing a hollow skeleton.
const MinWords = 30

var (
	mdHeading  = regexp.MustCompile(`^#{1,3}\s+(.+?)\s*#*\s*$`)
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdImage    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdEmphasis = regexp.MustCompile(`[*_~` + "`" + `]+`)
	mdURL      = regexp.MustCompile(`https?://\S+`)
)

// boilerplateHeadings are the nav/site chrome headings a scrape carries
// that are never newsletter sections (pt + en).
var boilerplateHeadings = map[string]bool{
	"subscribe": true, "sign in": true, "sign up": true, "log in": true,
	"login": true, "share": true, "comments": true, "read more": true,
	"related": true, "related posts": true, "recommended": true,
	"newsletter": true, "home": true, "about": true, "menu": true,
	"search": true, "tags": true, "archive": true, "archives": true,
	"latest": true, "top posts": true, "more": true, "footer": true,
	"inscreva-se": true, "assine": true, "entrar": true, "cadastre-se": true,
	"compartilhe": true, "comentários": true, "leia mais": true,
	"relacionados": true, "recomendados": true, "sobre": true,
	"buscar": true, "arquivo": true, "últimas": true, "mais": true,
	"rodapé": true, "início": true,
}

// platformSuffix matches the site-builder tag a page title often trails
// with ("… - Substack", "… on Medium").
var platformSuffix = regexp.MustCompile(`(?i)\s+([-–—|·]|on|no)\s+(substack|medium|beehiiv|wordpress|ghost|revue|buttondown|blog|newsletter)s?\.?\s*$`)

// ProposeFromPage turns a scraped page into a proposal. It never fails:
// thin input degrades to the canonical three-section skeleton plus a
// warning, and the caller decides MinWords first.
func ProposeFromPage(p *ScrapedPage, pageURL string) *ProposedPublication {
	out := &ProposedPublication{
		Frequency:    "weekly",
		AIDisclosure: "short",
	}
	host := hostOf(pageURL)
	fallbackName := siteName(strings.TrimPrefix(host, "www."))

	out.Name = cleanTitle(p.Title, fallbackName)
	out.Language = detectLanguage(p.Language, p.Markdown)
	out.Topic = firstNonEmpty(clipRunes(p.Description, 160),
		strings.Join(p.Keywords[:min(len(p.Keywords), 5)], ", "), out.Name)
	out.Words = len(strings.Fields(mdLink.ReplaceAllString(mdImage.ReplaceAllString(p.Markdown, " "), "$1")))
	out.Sections = sectionsFromHeadings(headings(p.Markdown), out.Language, out.Name)
	if out.Sections == nil {
		out.Sections = canonicalSections(out.Language)
		out.Warnings = append(out.Warnings, "generic_sections")
	}
	if out.Words < 300 {
		out.Warnings = append(out.Warnings, "thin_content")
	}
	topicShort := clipRunes(out.Topic, 80)
	if out.Language == "pt-BR" {
		out.Audience = "Leitores que acompanham " + topicShort
		out.EditorialLine = "Priorize notícias e análises sobre " + topicShort +
			".\nPule conteúdo promocional e temas fora do foco."
		out.Tone = "Direto e informativo"
	} else {
		out.Audience = "Readers who follow " + topicShort
		out.EditorialLine = "Prioritize news and analysis on " + topicShort +
			".\nSkip promotional and off-topic content."
		out.Tone = "Direct and informative"
	}
	return out
}

// cleanTitle trims a page title down to the publication name: trailing
// platform tags and the usual " | section" suffixes come off; an empty
// or unusable title falls back to the site name.
func cleanTitle(title, fallback string) string {
	t := strings.TrimSpace(platformSuffix.ReplaceAllString(title, ""))
	for _, sep := range []string{" | ", " — ", " – ", " :: "} {
		if i := strings.Index(t, sep); i > 0 {
			t = t[:i]
		}
	}
	// " - " is ambiguous (real titles use it) — cut only when the right
	// side is a platform tag, which platformSuffix already removed;
	// whatever is left keeps its hyphen.
	t = strings.TrimSpace(t)
	if utf8.RuneCountInString(t) < 2 {
		t = fallback
	}
	return clipRunes(t, 80)
}

// headings pulls cleaned, deduped section candidates out of markdown:
// up to four headings that are not site chrome, short enough to be a
// section title and distinct from the publication name.
func headings(md string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(md, "\n") {
		m := mdHeading.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		h := cleanHeading(m[1])
		if h == "" {
			continue
		}
		key := strings.ToLower(h)
		if seen[key] || boilerplateHeadings[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
		if len(out) == 4 {
			break
		}
	}
	return out
}

// cleanHeading strips inline markdown (links keep their label) and
// enforces the section-title bounds the builder validates (≤40 runes).
func cleanHeading(s string) string {
	s = mdImage.ReplaceAllString(s, "")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdEmphasis.ReplaceAllString(s, "")
	s = strings.TrimSpace(strings.Trim(s, "# "))
	if n := utf8.RuneCountInString(s); n < 3 || n > 40 {
		return ""
	}
	return s
}

// sectionsFromHeadings maps detected headings onto the builder's
// formats: the first reads as the edition's featured slot, a third or
// fourth closes as the links bucket, the rest are news sections. Fewer
// than two headings yields no mapping — the skeleton is a better guess.
func sectionsFromHeadings(hs []string, lang, name string) []config.Section {
	if len(hs) < 2 {
		return nil
	}
	// a heading that just restates the publication name is not a section
	filtered := hs[:0]
	for _, h := range hs {
		if !strings.EqualFold(h, name) {
			filtered = append(filtered, h)
		}
	}
	if len(filtered) < 2 {
		return nil
	}
	out := make([]config.Section, len(filtered))
	for i, h := range filtered {
		sc := config.Section{Title: h, Items: 3, Commentary: "optional", Access: config.AccessAll}
		switch {
		case i == 0:
			sc.Format, sc.Items, sc.Commentary = config.SectionFeatured, 1, "required"
		case i == len(filtered)-1 && len(filtered) >= 3:
			sc.Format, sc.Items, sc.Commentary = config.SectionLinks, 5, "none"
		default:
			sc.Format = config.SectionNews
		}
		out[i] = sc
	}
	// the total-stories bound is 20 — a four-heading page lands at 12
	return out
}

// canonicalSections is the sample's proven three-section skeleton in
// the detected language — the fallback when the page's headings do not
// map cleanly.
func canonicalSections(lang string) []config.Section {
	if lang == "pt-BR" {
		return []config.Section{
			{Title: "O destaque", Format: config.SectionFeatured, Items: 1, Commentary: "required", Access: config.AccessAll},
			{Title: "Vale saber", Format: config.SectionNews, Items: 4, Commentary: "optional", Access: config.AccessAll},
			{Title: "Links rápidos", Format: config.SectionLinks, Items: 5, Commentary: "none", Access: config.AccessAll},
		}
	}
	return []config.Section{
		{Title: "The big one", Format: config.SectionFeatured, Items: 1, Commentary: "required", Access: config.AccessAll},
		{Title: "Worth knowing", Format: config.SectionNews, Items: 4, Commentary: "optional", Access: config.AccessAll},
		{Title: "Quick links", Format: config.SectionLinks, Items: 5, Commentary: "none", Access: config.AccessAll},
	}
}

var ptMarkers = []string{
	" de ", " da ", " do ", " que ", " para ", " com ", " uma ", " não ",
	" em ", " os ", " as ", " é ", " se ",
}
var enMarkers = []string{
	" the ", " and ", " for ", " with ", " that ", " this ", " is ",
	" to ", " in ", " of ",
}

// detectLanguage settles on the two languages the proposal supports:
// the page's declared language wins; without one, a stopword count over
// the extracted text decides; a tie defaults to pt-BR (the product's
// primary market).
func detectLanguage(declared, markdown string) string {
	d := strings.ToLower(strings.TrimSpace(declared))
	if strings.HasPrefix(d, "pt") {
		return "pt-BR"
	}
	if strings.HasPrefix(d, "en") {
		return "en"
	}
	text := " " + strings.ToLower(mdURL.ReplaceAllString(markdown, " ")) + " "
	if len(text) > 4000 {
		text = text[:4000]
	}
	var pt, en int
	for _, m := range ptMarkers {
		pt += strings.Count(text, m)
	}
	for _, m := range enMarkers {
		en += strings.Count(text, m)
	}
	if en > pt {
		return "en"
	}
	return "pt-BR"
}

func clipRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
