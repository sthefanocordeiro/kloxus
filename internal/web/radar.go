package web

// Onboarding from zero (KLOX-123, pivot Block 2): one free-text answer
// creates the radar, a short Sim/Não calibration trains its ranking, and
// the sources arrive pre-selected by niche. Nothing here calls a model.

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const (
	calibrationCards = 8
	aboutMaxRunes    = 280
	nameMaxRunes     = 60
)

// nicheSources maps an onboarding niche chip to catalog slugs. The
// catalog is what the global scheduler ingests (D29) — the person only
// unchecks from this list; their own sources stay in settings.
var nicheSources = map[string][]string{
	"fintech":    {"finsiders", "infomoney", "seu-dinheiro", "money-times", "startups", "mobiletime", "tecnoblog", "techcrunch"},
	"cripto":     {"infomoney", "money-times", "seu-dinheiro", "tecnoblog", "techcrunch"},
	"regulacao":  {"finsiders", "agencia-brasil-economia", "infomoney", "poder360", "camara-noticias", "mobiletime"},
	"ia":         {"techcrunch", "wired", "ars-technica", "the-verge", "tecnoblog", "canaltech", "olhar-digital"},
	"startups":   {"startups", "techcrunch", "tecnoblog", "infomoney", "money-times"},
	"tecnologia": {"tecnoblog", "canaltech", "olhar-digital", "tecmundo", "meiobit", "the-verge", "ars-technica", "gizmodo-br", "macmagazine"},
	"mercado":    {"infomoney", "seu-dinheiro", "money-times", "agencia-brasil-economia", "startups", "valor"},
	"economia":   {"infomoney", "seu-dinheiro", "money-times", "agencia-brasil-economia", "g1", "cnn-brasil", "valor"},
	"negocios":   {"infomoney", "money-times", "seu-dinheiro", "startups", "agencia-brasil-economia", "valor"},
	"carreira":   {"mundo-rh", "rh-portal", "infomoney", "agencia-brasil-economia", "valor"},
}

// paywalledSources are catalog slugs behind a reader paywall: a radar only
// gets them when the person asks for them (KLOX-169) — a pauta whose links
// the reader cannot open is worse than a narrower one.
var paywalledSources = map[string]bool{"valor": true, "folha": true}

// defaultNicheSources is what a person who picked no chip gets.
var defaultNicheSources = []string{"tecnoblog", "canaltech", "infomoney", "techcrunch"}

var nicheLabels = map[string]string{
	"fintech": "fintech", "cripto": "cripto", "regulacao": "regulação financeira",
	"ia": "inteligência artificial", "startups": "startups", "tecnologia": "tecnologia", "mercado": "mercado",
	"economia": "economia", "negocios": "negócios", "carreira": "carreira",
}

// radarName is a short display name from the free text.
func radarName(about string) string {
	name := "Radar: " + about
	if utf8.RuneCountInString(name) > 40 {
		name = strings.TrimSpace(string([]rune(name)[:40])) + "…"
	}
	return name
}

// apiRadarCreate creates the visitor's radar from "Sobre o que você
// fala?" and pre-selects its sources by niche. One radar per account,
// like the publication create it wraps.
func (s *Server) apiRadarCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireClaimed(w, r) {
		return
	}
	var in struct {
		Name   string   `json:"name"`   // the radar's display name (D4)
		About  string   `json:"about"`  // the topics, free text separated by commas
		Topics string   `json:"topics"` // alias of about
		Niches []string `json:"niches"` // optional editorias
		// AllowPaywall lists paywalled catalog slugs the person marked to
		// include (e.g. "valor"); none by default.
		AllowPaywall []string `json:"allow_paywall"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if in.About == "" {
		in.About = in.Topics
	}
	about := strings.Join(strings.Fields(in.About), " ")
	radarLabel := strings.Join(strings.Fields(in.Name), " ")
	if utf8.RuneCountInString(radarLabel) > nameMaxRunes {
		apiErr(w, 400, "validation", "that name is too long",
			apiReason{Code: "name_long", Message: "keep the name under 60 characters"})
		return
	}
	var niches []string
	for _, n := range in.Niches {
		if _, ok := nicheSources[n]; ok {
			niches = append(niches, n)
		}
	}
	if about == "" && len(niches) == 0 {
		apiErr(w, 400, "validation", "say what you talk about",
			apiReason{Code: "about_empty", Message: "describe your topic or pick a niche"})
		return
	}
	if utf8.RuneCountInString(about) > aboutMaxRunes {
		apiErr(w, 400, "validation", "that is too long",
			apiReason{Code: "about_long", Message: "keep it under 280 characters"})
		return
	}
	pubs, err := db(r).ListPublications(r.Context())
	if apiDBErr(w, r, err, "publications") {
		return
	}
	if len(pubs) > 0 {
		apiErr(w, 409, "one_newsletter", "this account already has a radar")
		return
	}
	// the profile text the ranking embeds: the person's words, then the
	// chosen niches
	topic := about
	for _, n := range niches {
		if !strings.Contains(strings.ToLower(topic), nicheLabels[n]) {
			if topic != "" {
				topic += ", "
			}
			topic += nicheLabels[n]
		}
	}
	name := radarName(topic)
	if about != "" {
		name = radarName(about)
	}
	if radarLabel != "" {
		name = radarLabel
	}
	def := &config.Publication{
		Name: name, Topic: topic, Language: "pt", Frequency: "weekly",
		Tone: "neutral", AIDisclosure: "none",
		Sections: config.NormalizeSections([]config.Section{
			{Title: "Pauta", Format: config.SectionNews, Items: 8, Commentary: "optional", Access: config.AccessAll},
		}, nil),
	}
	if err := def.Validate(); err != nil {
		apiErr(w, 400, "validation", err.Error())
		return
	}
	id, err := db(r).CreatePublication(r.Context(), def)
	if apiDBErr(w, r, err, "radar") {
		return
	}
	pub, err := db(r).GetPublication(r.Context(), fmt.Sprint(id))
	if apiDBErr(w, r, err, "radar") {
		return
	}
	s.attachNicheSources(r, pub, niches, in.AllowPaywall)
	db(r).SetProgress(r.Context(), "publication_created")
	db(r).SetProgress(r.Context(), "sources_confirmed")
	out := []apiSourceRow{}
	for _, src := range mustSources(r, pub.ID) {
		out = append(out, s.sourceRow(r, src))
	}
	apiJSON(w, 201, map[string]any{"publication": publicationView(pub), "sources": out})
}

// nicheSlugs is the catalog slugs a radar starts with: the union of its
// niches (the default set when none), minus paywalled outlets the person
// did not mark.
func nicheSlugs(niches, allowPaywall []string) map[string]bool {
	allow := map[string]bool{}
	for _, sl := range allowPaywall {
		allow[strings.TrimSpace(sl)] = true
	}
	want := map[string]bool{}
	for _, n := range niches {
		for _, slug := range nicheSources[n] {
			want[slug] = true
		}
	}
	if len(want) == 0 {
		for _, slug := range defaultNicheSources {
			want[slug] = true
		}
	}
	for slug := range want {
		if paywalledSources[slug] && !allow[slug] {
			delete(want, slug)
		}
	}
	return want
}

// attachNicheSources adds the catalog sources of the chosen niches (the
// union; the default set when none). Demo mode attaches the fixture
// sources instead — that mode has no live catalog.
func (s *Server) attachNicheSources(r *http.Request, pub *store.Publication, niches, allowPaywall []string) {
	if s.d.Cfg.Mode == "demo" || s.d.Pool == nil {
		if def, err := s.sampleDef(); err == nil {
			for _, sd := range def.Sources {
				db(r).AddSource(r.Context(), pub.ID, sd.Type, sd.URL, sd.Name)
			}
		}
		return
	}
	want := nicheSlugs(niches, allowPaywall)
	cat, err := s.d.Pool.Catalog(r.Context())
	if err != nil {
		return
	}
	for i := range cat {
		cs := &cat[i]
		if !want[cs.Slug] || !pickable(cs) {
			continue
		}
		db(r).AddCatalogSource(r.Context(), pub.ID, cs)
	}
}

type pautaCardView struct {
	StoryID  int64    `json:"story_id"`
	Title    string   `json:"title"`
	Outlet   string   `json:"outlet"`
	Outlets  int      `json:"outlets"`
	Summary  string   `json:"summary"`
	Topics   []string `json:"topics"`
	URL      string   `json:"url"`
	AgeHours *float64 `json:"age_hours"`
}

func pautaViews(cards []pipeline.PautaCard) []pautaCardView {
	out := make([]pautaCardView, 0, len(cards))
	for _, c := range cards {
		v := pautaCardView{StoryID: c.StoryID, Title: c.Title, Outlet: c.Outlet, Outlets: c.Outlets,
			Summary: c.Summary, Topics: c.Topics, URL: c.URL}
		if c.AgeHours >= 0 {
			a := c.AgeHours
			v.AgeHours = &a
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) pautaRunner(r *http.Request) *pipeline.Runner {
	return &pipeline.Runner{
		DB: db(r), Fetcher: s.d.Fetcher,
		DemoMode:      s.d.Cfg.Mode == "demo",
		Deterministic: true,
	}
}

// apiCalibration builds the 8-card calibration deck: real stories of the
// last 48 hours, stage-1 ranked, MMR-spread. Posting again re-deals it
// with the answers so far already in the profile.
func (s *Server) apiCalibration(w http.ResponseWriter, r *http.Request) {
	pub, err := db(r).GetPublication(r.Context(), r.PathValue("id"))
	if apiDBErr(w, r, err, "radar") {
		return
	}
	run := s.pautaRunner(r)
	stories, err := run.BuildStories(r.Context(), pub)
	if apiDBErr(w, r, err, "stories") {
		return
	}
	dropPautaDay(r, db(r), pub.ID) // the deck was re-dealt — story ids changed
	cards := run.Pauta(r.Context(), pub, stories, calibrationCards, pipeline.CalibrationWindow)
	apiJSON(w, 200, map[string]any{"cards": pautaViews(cards), "window_hours": int(pipeline.CalibrationWindow / time.Hour)})
}

// apiCalibrationAnswer folds one Sim/Não into the Rocchio profile.
func (s *Server) apiCalibrationAnswer(w http.ResponseWriter, r *http.Request) {
	pub, err := db(r).GetPublication(r.Context(), r.PathValue("id"))
	if apiDBErr(w, r, err, "radar") {
		return
	}
	var in struct {
		StoryID int64  `json:"story_id"`
		Answer  string `json:"answer"`
	}
	if _, err := apiDecode(r, &in); err != nil || in.StoryID == 0 || (in.Answer != "yes" && in.Answer != "no") {
		apiErr(w, 400, "validation", "story_id and answer (yes|no) are required")
		return
	}
	// feedback never fails the answer (no vectors → silent no-op)
	_ = pipeline.RecordStoryFeedback(r.Context(), db(r), pub.ID, in.StoryID, in.Answer == "yes")
	dropPautaDay(r, db(r), pub.ID) // the answer re-ranks today's pauta
	apiJSON(w, 200, map[string]any{"ok": true})
}

// apiSkipTopic adds a topic to the radar's skip list (reject terms):
// the ranking demotes it and the board hides it under "fora do tema".
func (s *Server) apiSkipTopic(w http.ResponseWriter, r *http.Request) {
	pub, err := db(r).GetPublication(r.Context(), r.PathValue("id"))
	if apiDBErr(w, r, err, "radar") {
		return
	}
	var in struct {
		Topic string `json:"topic"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	topic := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(in.Topic, ",", " ")))
	if topic == "" || utf8.RuneCountInString(topic) > 40 {
		apiErr(w, 400, "validation", "topic is required (max 40 characters)")
		return
	}
	terms := []string{}
	for _, t := range strings.Split(pub.RejectTerms, ",") {
		if t = strings.TrimSpace(t); t != "" {
			terms = append(terms, t)
		}
	}
	for _, t := range terms {
		if strings.EqualFold(t, topic) {
			apiJSON(w, 200, map[string]any{"publication": publicationView(pub)})
			return
		}
	}
	def := &config.Publication{
		Name: pub.Name, Topic: pub.Topic, Audience: pub.Audience, EditorialLine: pub.EditorialLine,
		RejectTerms: strings.Join(append(terms, topic), ", "), Tone: pub.Tone, Language: pub.Language,
		Slug: pub.Slug, Frequency: pub.Frequency, AIDisclosure: pub.AIDisclosure,
		Sections: config.NormalizeSections(pub.Sections, pub.Paywall.PaidSections), Paywall: pub.Paywall,
	}
	if err := def.Validate(); err != nil {
		apiErr(w, 400, "validation", err.Error())
		return
	}
	if apiDBErr(w, r, db(r).UpdatePublication(r.Context(), pub.ID, def), "radar") {
		return
	}
	dropPautaDay(r, db(r), pub.ID) // a new reject term re-ranks today's pauta
	fresh, err := db(r).GetPublication(r.Context(), fmt.Sprint(pub.ID))
	if apiDBErr(w, r, err, "radar") {
		return
	}
	apiJSON(w, 200, map[string]any{"publication": publicationView(fresh)})
}
