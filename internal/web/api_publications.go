package web

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/opml"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func (s *Server) apiWorkspace(w http.ResponseWriter, r *http.Request) {
	ws := reqWS(r)
	owner := s.workspaceOwner(r.Context(), db(r))
	mail := "outbox"
	// mail_transport is the sender's own wire identity — positive
	// server-side evidence for verification preflights: "outbox" (no
	// sender), "resend" (the real provider), "synthetic" (the loopback
	// fake), "custom" (any other injected sender).
	transport := "outbox"
	// mail_webhook reports whether the app can verify Resend delivery
	// events (RESEND_WEBHOOK_SECRET set); mail_stale_sent counts
	// provider-accepted sends whose delivery event never arrived —
	// together they are the posture a dead endpoint cannot fake
	// (KLOX-98). Outbox mode has no webhook, so both stay inert.
	mailWebhook := "n/a"
	staleSent := 0
	if s.liveMail() {
		mail = "live"
		transport = "custom"
		if t, ok := s.d.Mailer.(interface{ Transport() string }); ok {
			transport = t.Transport()
		}
		mailWebhook = "unconfigured"
		if s.d.Cfg.ResendWebhookSecret != "" {
			mailWebhook = "configured"
		}
		staleSent = s.staleSent(r.Context(), db(r))
	}
	// the account-menu "Receber as notícias do dia por e-mail" toggle
	// (KLOX-257) — ON for saved accounts until stamped off
	dailyMail, err := db(r).DailyMailEnabled(r.Context())
	if err != nil {
		apiErr(w, 500, "internal", "could not read mail settings")
		return
	}
	apiJSON(w, 200, map[string]any{
		"id": ws.ID, "label": ws.Label, "mode": s.d.Cfg.Mode, "hosted": s.d.WS.Hosted(),
		"llm_mode": s.d.Cfg.LLMMode,
		"claimed":  owner != "", "owner_email": owner, "mail": mail,
		// the account's saved interface language — "" when the owner
		// never picked one (KLOX-232)
		"lang":           db(r).UILang(r.Context()),
		"mail_transport": transport, "mail_webhook": mailWebhook,
		"mail_stale_sent": staleSent,
		"daily_mail":      dailyMail,
		"publishing":      s.d.Cfg.PublishingEnabled,
	})
}

func (s *Server) apiPublications(w http.ResponseWriter, r *http.Request) {
	pubs, err := db(r).ListPublications(r.Context())
	if apiDBErr(w, r, err, "publications") {
		return
	}
	out := []apiPublication{}
	for _, p := range pubs {
		out = append(out, publicationView(p))
	}
	apiJSON(w, 200, map[string]any{"publications": out})
}

func (s *Server) apiPublication(w http.ResponseWriter, r *http.Request) {
	// detail view shows the stored (creator-edited) blocks — canonicalPub
	// applies only to prompt/section semantics
	pub, err := db(r).GetPublication(r.Context(), r.PathValue("id"))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	resp := map[string]any{"publication": publicationView(pub)}
	if iss, err := db(r).LatestIssue(r.Context(), pub.ID); err == nil {
		resp["latest_issue"] = s.issueSummary(r, iss)
	}
	apiJSON(w, 200, resp)
}

// apiSourceRow is one source with monitor state.
type apiSourceRow struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	URL          string         `json:"url"`
	CanonicalURL string         `json:"canonical_url"`
	CatalogID    int64          `json:"catalog_id,omitempty"`
	Policy       string         `json:"policy,omitempty"`
	LastFetchAt  *string        `json:"last_fetch_at"`
	LastStatus   *string        `json:"last_status"`
	ItemsPerWeek int            `json:"items_per_week"`
	Monitor      apiMonitorView `json:"monitor"`
}

type apiMonitorView struct {
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (s *Server) sourceRow(r *http.Request, src store.Source) apiSourceRow {
	row := apiSourceRow{
		ID: src.ID, Name: src.Name, Type: src.Type, URL: src.URL,
		CanonicalURL: dedupe.Canonical(src.URL),
		CatalogID:    src.CatalogID, Policy: src.Policy,
	}
	if src.LastFetchAt.Valid {
		row.LastFetchAt = &src.LastFetchAt.String
	}
	if src.LastStatus.Valid {
		row.LastStatus = &src.LastStatus.String
	}
	p, mid, st, _ := db(r).SourceMonitor(r.Context(), src.ID)
	row.Monitor = apiMonitorView{Provider: p, ID: mid, Status: st}
	row.ItemsPerWeek, _ = db(r).ItemsPerWeek(r.Context(), src.ID)
	return row
}

func (s *Server) apiSources(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	srcs, _ := db(r).Sources(r.Context(), pub.ID)
	out := []apiSourceRow{}
	for _, src := range srcs {
		out = append(out, s.sourceRow(r, src))
	}
	apiJSON(w, 200, map[string]any{"sources": out})
}

func (s *Server) apiSourceAdd(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Type string `json:"type"`
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	typ, rawURL := strings.TrimSpace(in.Type), strings.TrimSpace(in.URL)
	in.Name = strings.TrimSpace(in.Name)
	if rawURL == "" || (typ != "rss" && typ != "page") {
		apiErr(w, 400, "validation", "type rss|page and url are required")
		return
	}
	if s.d.Cfg.Mode != "demo" && s.d.Pool != nil {
		s.realSourceAdd(w, r, pub, rawURL, in.Name)
		return
	}
	note := ""
	if s.d.Cfg.Mode == "demo" {
		// Demo mode never fetches a visitor-typed URL (SSRF surface):
		// validate the format, store, simulate.
		if strings.HasPrefix(rawURL, "fixtures/") {
			if _, err := fs.Stat(examples.FS, rawURL); err != nil {
				apiErr(w, 400, "invalid_source",
					fmt.Sprintf("no fixture at %q — demo sources live under fixtures/...", rawURL))
				return
			}
		} else {
			u, err := url.Parse(rawURL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				apiErr(w, 400, "invalid_source", fmt.Sprintf("invalid source URL %q", rawURL))
				return
			}
			note = "Simulated in demo mode: sources are not fetched on the hosted demo."
		}
	} else {
		u, err := url.Parse(rawURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			apiErr(w, 400, "invalid_source",
				fmt.Sprintf("invalid source URL %q — real-mode sources must be absolute http(s) URLs", rawURL))
			return
		}
	}
	id, err := db(r).AddSource(r.Context(), pub.ID, typ, rawURL, in.Name)
	if apiDBErr(w, r, err, "source") {
		return
	}
	var row apiSourceRow
	for _, src := range mustSources(r, pub.ID) {
		if src.ID == id || (row.ID == 0 && src.URL == rawURL) {
			row = s.sourceRow(r, src)
		}
	}
	if row.ID == 0 {
		row.ID = id
		row.Type, row.URL = typ, rawURL
		row.CanonicalURL = dedupe.Canonical(rawURL)
	}
	resp := map[string]any{"source": row}
	if note != "" {
		resp["note"] = note
	}
	apiJSON(w, 201, resp)
}

func mustSources(r *http.Request, pubID int64) []store.Source {
	srcs, _ := db(r).Sources(r.Context(), pubID)
	return srcs
}

// apiSourceDelete removes a source — the way out for a failing one.
func (s *Server) apiSourceDelete(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	if apiDBErr(w, r, db(r).DeleteSource(r.Context(), pub.ID, sid), "source") {
		return
	}
	apiJSON(w, 200, map[string]any{"ok": true})
}

// apiSourceCatalog lists the sources a publication can pick. Demo mode
// offers the sample fixture sources; real mode serves the global
// catalog — launch/signal entries only; hold/blocked entries are never
// listed to users (D29/D31/KLOX-49).
func (s *Server) apiSourceCatalog(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	if s.d.Cfg.Mode != "demo" && s.d.Pool != nil {
		s.apiRealCatalog(w, r, pub)
		return
	}
	def, err := s.sampleDef()
	if err != nil {
		apiErr(w, 500, "internal", "source catalog unavailable")
		return
	}
	existing := map[string]bool{}
	for _, src := range mustSources(r, pub.ID) {
		existing[src.URL] = true
	}
	var offered []map[string]any
	for _, sd := range def.Sources {
		if existing[sd.URL] {
			continue
		}
		row := map[string]any{"type": sd.Type, "url": sd.URL, "name": sd.Name, "items_per_week": 0}
		// the fixture fetch is local — count what the source would bring
		// in so the row shows a real items-per-week before it's added
		if items, err := s.d.Fetcher.Fetch(r.Context(), sd.Type, sd.URL); err == nil {
			row["items_per_week"] = len(items)
		}
		offered = append(offered, row)
	}
	if offered == nil {
		offered = []map[string]any{}
	}
	apiJSON(w, 200, map[string]any{"offered": offered})
}

// apiSourcePreview shows the three latest items a source would bring in.
// Fixture URLs read from the embedded files; a pasted URL is validated
// for format only — demo mode never fetches it.
func (s *Server) apiSourcePreview(w http.ResponseWriter, r *http.Request) {
	_, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	typ, rawURL := r.URL.Query().Get("type"), strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" || (typ != "rss" && typ != "page") {
		apiErr(w, 400, "validation", "type rss|page and url are required")
		return
	}
	if s.d.Cfg.Mode != "demo" && s.d.Pool != nil {
		if s.realCatalogPreview(w, r, rawURL) {
			return
		}
		apiErr(w, 400, "catalog_only",
			"that source is not in the catalog — pick from the listed sources")
		return
	}
	if !strings.HasPrefix(rawURL, "fixtures/") && s.d.Cfg.Mode == "demo" {
		u, perr := url.Parse(rawURL)
		if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			apiErr(w, 400, "invalid_source", fmt.Sprintf("invalid source URL %q", rawURL))
			return
		}
		apiJSON(w, 200, map[string]any{
			"items": []any{},
			"note":  "Simulated in demo mode: this URL is never fetched — in live mode the three latest items appear here.",
		})
		return
	}
	items, err := s.d.Fetcher.Fetch(r.Context(), typ, rawURL)
	if err != nil {
		apiErr(w, 400, "invalid_source", "cannot read that source: "+err.Error())
		return
	}
	var out []map[string]any
	for i, it := range items {
		if i == 3 {
			break
		}
		out = append(out, map[string]any{
			"title": it.Title, "url": it.URL, "outlet": it.Outlet, "published_at": it.PublishedAt,
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	apiJSON(w, 200, map[string]any{"items": out})
}

// apiSourceConfirm accepts the selected offered fixture sources and marks
// onboarding done — the client then starts the first run itself. An
// optional {"include": [...urls]} body limits which offered sources are
// added; no body accepts them all.
func (s *Server) apiSourceConfirm(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Include []string `json:"include"`
	}
	decoded, err := apiDecode(r, &in)
	if err != nil {
		apiErr(w, 400, "validation", "invalid JSON body")
		return
	}
	if s.d.Cfg.Mode != "demo" && s.d.Pool != nil {
		s.realSourceConfirm(w, r, pub, in.Include, decoded)
		return
	}
	if s.d.Cfg.Mode == "demo" {
		if def, err := s.sampleDef(); err == nil {
			existing := map[string]bool{}
			for _, src := range mustSources(r, pub.ID) {
				existing[src.URL] = true
			}
			want := map[string]bool{}
			for _, u := range in.Include {
				want[u] = true
			}
			for _, sd := range def.Sources {
				if existing[sd.URL] || (decoded && !want[sd.URL]) {
					continue
				}
				db(r).AddSource(r.Context(), pub.ID, sd.Type, sd.URL, sd.Name)
			}
		}
	}
	db(r).SetProgress(r.Context(), "sources_confirmed")
	out := []apiSourceRow{}
	for _, src := range mustSources(r, pub.ID) {
		out = append(out, s.sourceRow(r, src))
	}
	apiJSON(w, 200, map[string]any{"sources": out})
}

// apiSample serves the landing page's cited dossier — workspace-free, so
// the first screen doesn't create a demo workspace before Start. It is
// always the fictional Valdória sample (KLOX-49): the landing shows only
// content we are allowed to display — the recorded real issue carried
// dossiers from outlets outside the catalog (g1), so it no longer ships.
func (s *Server) apiSample(w http.ResponseWriter, r *http.Request) {
	data, err := examples.FS.ReadFile("landing/sample.json")
	if err != nil {
		apiErr(w, 500, "internal", "sample dossier unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(data)
}

// apiProposal returns the sample newsletter's proposed definition — the
// five editable blocks plus carried settings and the sources offered next.
func (s *Server) apiProposal(w http.ResponseWriter, r *http.Request) {
	def, err := s.sampleDef()
	if err != nil {
		apiErr(w, 500, "internal", "sample proposal unavailable")
		return
	}
	secs := []apiSection{}
	for _, sec := range config.NormalizeSections(def.Sections, def.Paywall.PaidSections) {
		secs = append(secs, apiSection{
			ID: sec.ID, Title: sec.Title, Format: sec.Format,
			Items: sec.Items, Commentary: sec.Commentary, Access: sec.Access,
			Topic: sec.Topic,
		})
	}
	srcs := []map[string]string{}
	for _, sd := range def.Sources {
		srcs = append(srcs, map[string]string{"type": sd.Type, "url": sd.URL, "name": sd.Name})
	}
	apiJSON(w, 200, map[string]any{"proposal": map[string]any{
		"name": def.Name, "topic": def.Topic, "audience": def.Audience,
		"editorial_line": localizeEditorialLine(def.EditorialLine, def.Language),
		"reject_terms":   def.RejectTerms, "tone": def.Tone,
		"language": def.Language, "frequency": def.Frequency,
		"ai_disclosure": def.AIDisclosure,
		"sections":      secs, "sources": srcs,
		"paywall": apiPaywall{Enabled: def.Paywall.Enabled, PaidSections: def.Paywall.PaidSections, Price: localizePrice(def.Paywall.Price, def.Language)},
	}})
}

// apiPublicationCreate stores a creator-made publication from the
// (possibly edited) proposal blocks. Sources are added separately on the
// sources screen.
func (s *Server) apiPublicationCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireClaimed(w, r) {
		return
	}
	var in struct {
		Name          string           `json:"name"`
		Topic         string           `json:"topic"`
		Audience      string           `json:"audience"`
		EditorialLine string           `json:"editorial_line"`
		RejectTerms   string           `json:"reject_terms"`
		Tone          string           `json:"tone"`
		Language      string           `json:"language"`
		Frequency     string           `json:"frequency"`
		AIDisclosure  string           `json:"ai_disclosure"`
		Sections      []config.Section `json:"sections"`
		Paywall       config.Paywall   `json:"paywall"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	secs, reasons := validateSections(in.Sections)
	if len(reasons) > 0 {
		apiErr(w, 400, "validation", "the section list needs a fix", reasons...)
		return
	}
	// the builder's per-section access flag is the source of truth for
	// the paywall's title list — the two must never drift apart
	in.Paywall.PaidSections = config.PaidSectionTitles(secs)
	def := &config.Publication{
		Name: in.Name, Topic: in.Topic, Audience: in.Audience,
		EditorialLine: in.EditorialLine, RejectTerms: in.RejectTerms, Tone: in.Tone,
		Language: in.Language, Frequency: in.Frequency,
		AIDisclosure: in.AIDisclosure, Sections: secs, Paywall: in.Paywall,
	}
	if err := def.Validate(); err != nil {
		apiErr(w, 400, "validation", err.Error())
		return
	}
	// One newsletter per account (owner amendment, KLOX-66): the rule
	// lives in the API, not just the UI — any existing publication makes
	// the account "has a newsletter", including a template-seeded one.
	// Workspaces that already hold several publications (pre-amendment
	// data) keep them all; the guard only stops new growth, and deleting
	// back to zero reopens onboarding.
	pubs, err := db(r).ListPublications(r.Context())
	if apiDBErr(w, r, err, "publications") {
		return
	}
	if len(pubs) > 0 {
		apiErr(w, 409, "one_newsletter",
			"this account already has a newsletter — one newsletter per account")
		return
	}
	id, err := db(r).CreatePublication(r.Context(), def)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	db(r).SetProgress(r.Context(), "publication_created")
	pub, _ := db(r).GetPublication(r.Context(), fmt.Sprint(id))
	apiJSON(w, 201, map[string]any{"publication": publicationView(pub)})
}

// validateSections applies the builder's rules and returns the
// normalized list (ids, formats and access defaults filled, paywall
// titles resolved) — the value to persist. Bound checks come back as
// structured reasons so the client can localize each one.
func validateSections(in []config.Section) ([]config.Section, []apiReason) {
	var reasons []apiReason
	secs := config.NormalizeSections(in, nil)
	if len(secs) == 0 {
		reasons = append(reasons, apiReason{Code: "sections_empty",
			Message: "add at least one section"})
		return nil, reasons
	}
	ids, titles := map[string]bool{}, map[string]bool{}
	total := 0
	for i, sc := range secs {
		target := &apiTarget{Kind: "section", Ref: sc.Title}
		if strings.TrimSpace(sc.Title) == "" {
			reasons = append(reasons, apiReason{Code: "section_title_empty",
				Message: fmt.Sprintf("section %d needs a name", i+1), Target: target})
		} else if len([]rune(sc.Title)) > 40 {
			reasons = append(reasons, apiReason{Code: "section_title_long",
				Message: fmt.Sprintf("section %q runs past 40 characters", sc.Title), Target: target})
		} else if titles[strings.ToLower(sc.Title)] {
			reasons = append(reasons, apiReason{Code: "section_title_dup",
				Message: fmt.Sprintf("section %q is repeated", sc.Title), Target: target})
		}
		titles[strings.ToLower(sc.Title)] = true
		if ids[sc.ID] {
			reasons = append(reasons, apiReason{Code: "section_id_dup",
				Message: fmt.Sprintf("section id %q is repeated", sc.ID), Target: target})
		}
		ids[sc.ID] = true
		if sc.Items < 1 || sc.Items > 10 {
			reasons = append(reasons, apiReason{Code: "section_items",
				Message: fmt.Sprintf("section %q asks for %d stories — pick 1 to 10", sc.Title, sc.Items), Target: target})
		} else {
			total += sc.Items
		}
		if !config.ValidSectionFormat(sc.Format) {
			reasons = append(reasons, apiReason{Code: "section_format",
				Message: fmt.Sprintf("section %q has an unknown format %q", sc.Title, sc.Format), Target: target})
		}
		switch sc.Commentary {
		case "required", "optional", "none":
		default:
			reasons = append(reasons, apiReason{Code: "section_commentary",
				Message: fmt.Sprintf("section %q has an unknown commentary mode %q", sc.Title, sc.Commentary), Target: target})
		}
		if !config.ValidSectionAccess(sc.Access) {
			reasons = append(reasons, apiReason{Code: "section_access",
				Message: fmt.Sprintf("section %q has an unknown access level %q", sc.Title, sc.Access), Target: target})
		}
	}
	if total > 20 {
		reasons = append(reasons, apiReason{Code: "sections_total",
			Message: fmt.Sprintf("the edition asks for %d stories — the limit is 20", total)})
	}
	if len(reasons) > 0 {
		return nil, reasons
	}
	return secs, nil
}

// apiPublicationUpdate rewrites the creator-editable blocks of a
// publication — identity copy, cadence, sections and the paywall
// block. The change lands on the next edition: issues already in
// progress keep the sections they were built with (KLOX-65).
func (s *Server) apiPublicationUpdate(w http.ResponseWriter, r *http.Request) {
	// the write path reads the stored row — canonicalPub swaps a custom
	// publication's prompt fields for the demo sample, and persisting
	// those fallbacks would silently overwrite the creator's edits
	pub, err := db(r).GetPublication(r.Context(), r.PathValue("id"))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Name          string           `json:"name"`
		Audience      string           `json:"audience"`
		EditorialLine string           `json:"editorial_line"`
		Tone          string           `json:"tone"`
		Frequency     string           `json:"frequency"`
		AIDisclosure  string           `json:"ai_disclosure"`
		Sections      []config.Section `json:"sections"`
		Paywall       *apiPaywall      `json:"paywall"`
		// Topic and RejectTerms ride pointers so "clear the field" ("")
		// is a real write while an absent key keeps the stored value —
		// the keep-if-empty convention can't tell the two apart (KLOX-94).
		Topic       *string `json:"topic"`
		RejectTerms *string `json:"reject_terms"`
		// Niches rides a pointer so an absent key keeps the stored
		// editorias while an explicit empty list clears them (KLOX-274).
		Niches *[]string `json:"niches"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	def := &config.Publication{
		Name: in.Name, Topic: pub.Topic, Niches: pub.Niches, Audience: in.Audience,
		EditorialLine: in.EditorialLine, RejectTerms: pub.RejectTerms, Tone: in.Tone,
		Language: pub.Language, Slug: pub.Slug,
		Frequency:    in.Frequency,
		AIDisclosure: in.AIDisclosure,
		Sections:     config.NormalizeSections(pub.Sections, pub.Paywall.PaidSections),
		Paywall:      pub.Paywall,
	}
	// every block the request leaves out keeps the stored value — the
	// builder's sections-only save must not wipe the identity fields
	// (KLOX-65)
	if def.Name == "" {
		def.Name = pub.Name
	}
	if in.Topic != nil || in.Niches != nil {
		// publication_niches carries the chip state. Topic remains only
		// creator prose plus the selected labels folded as ranking
		// vocabulary; it is never mined again to infer chips (KLOX-274).
		niches := orderedNiches(pub.Niches)
		if in.Niches != nil {
			niches = cleanNiches(*in.Niches)
		}
		base := topicWithoutNiches(pub.Topic, pub.Niches)
		if in.Topic != nil {
			base = topicWithoutNiches(strings.TrimSpace(*in.Topic), niches)
		}
		def.Topic = topicWithNiches(base, niches)
		def.Niches = niches
	}
	if def.Audience == "" {
		def.Audience = pub.Audience
	}
	if def.EditorialLine == "" {
		def.EditorialLine = pub.EditorialLine
	}
	if def.Tone == "" {
		def.Tone = pub.Tone
	}
	if def.Frequency == "" {
		def.Frequency = pub.Frequency
	}
	if def.AIDisclosure == "" {
		def.AIDisclosure = pub.AIDisclosure
	}
	if in.RejectTerms != nil {
		def.RejectTerms = strings.TrimSpace(*in.RejectTerms)
	}
	if strings.TrimSpace(def.Name) == "" {
		apiErr(w, 400, "validation", "the newsletter needs a name",
			apiReason{Code: "name_empty", Message: "give the newsletter a name"})
		return
	}
	if in.Sections != nil {
		secs, reasons := validateSections(in.Sections)
		if len(reasons) > 0 {
			apiErr(w, 400, "validation", "the section list needs a fix", reasons...)
			return
		}
		def.Sections = secs
	}
	if in.Paywall != nil {
		def.Paywall.Enabled = in.Paywall.Enabled
		def.Paywall.Price = in.Paywall.Price
	}
	def.Paywall.PaidSections = config.PaidSectionTitles(def.Sections)
	if err := def.Validate(); err != nil {
		apiErr(w, 400, "validation", err.Error())
		return
	}
	if apiDBErr(w, r, db(r).UpdatePublication(r.Context(), pub.ID, def), "publication") {
		return
	}
	fresh, err := db(r).GetPublication(r.Context(), fmt.Sprint(pub.ID))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	apiJSON(w, 200, map[string]any{"publication": publicationView(fresh)})
}

// apiPublicationDelete removes a publication and everything it owns —
// issues, stories, sources, readers, guides. The call must carry an
// explicit {"confirm": true} so a stray request can never delete a
// newsletter (KLOX-65).
func (s *Server) apiPublicationDelete(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !in.Confirm {
		apiErr(w, 400, "validation", "deleting a newsletter requires confirm: true",
			apiReason{Code: "confirm_required", Message: "confirm the deletion"})
		return
	}
	if err := db(r).DeletePublication(r.Context(), pub.ID); apiDBErr(w, r, err, "publication") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiSourceWatch(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	srcURL, err := s.sourceURL(r, sid)
	if apiDBErr(w, r, err, "source") {
		return
	}
	mid, err := s.d.Monitor.Watch(r.Context(), srcURL)
	if err != nil {
		apiErr(w, 502, "monitor", "monitor: "+err.Error())
		return
	}
	if apiDBErr(w, r, db(r).SetSourceMonitor(r.Context(), sid, s.d.Monitor.Name(), mid, "monitoring"), "source") {
		return
	}
	var row apiSourceRow
	for _, src := range mustSources(r, pub.ID) {
		if src.ID == sid {
			row = s.sourceRow(r, src)
		}
	}
	apiJSON(w, 200, map[string]any{"source": row})
}

func (s *Server) apiSourceCheck(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	if _, err := s.sourceURL(r, sid); apiDBErr(w, r, err, "source") {
		return
	}
	_, mid, _, _ := db(r).SourceMonitor(r.Context(), sid)
	res, err := s.d.Monitor.Check(r.Context(), mid)
	if err != nil {
		apiErr(w, 502, "monitor", "check: "+err.Error())
		return
	}
	status := "monitoring"
	var details []string
	for _, rr := range res {
		status = rr.Status
		details = append(details, rr.Detail)
	}
	db(r).SetSourceMonitor(r.Context(), sid, s.d.Monitor.Name(), mid, status+": "+strings.Join(details, "; "))
	var row apiSourceRow
	for _, src := range mustSources(r, pub.ID) {
		if src.ID == sid {
			row = s.sourceRow(r, src)
		}
	}
	apiJSON(w, 200, map[string]any{"source": row})
}

// sourceURL returns the URL of a source owned by the publication in the
// request path, guarding against cross-publication source ids.
func (s *Server) sourceURL(r *http.Request, sid int64) (string, error) {
	pub, err := s.pub(r)
	if err != nil {
		return "", err
	}
	var url string
	err = db(r).QueryRowContext(r.Context(),
		`SELECT url FROM sources WHERE workspace_id = $1 AND id = $2 AND publication_id = $3`,
		db(r).Workspace(), sid, pub.ID).Scan(&url)
	return url, err
}

// apiSourceImportOPML imports feeds from an OPML export (Feedly etc.).
// Body: {"opml": "<xml>", "apply": false, "include": [urls]}. Without
// apply it is a dry run: every feed is classified new | duplicate |
// invalid. With apply the new ones (optionally limited to include) are
// stored as rss sources. Duplicates are matched by scheme/www/trailing-
// slash-insensitive URL against existing sources and within the file;
// feeds sharing a domain with an existing source are flagged, not blocked.
// Demo mode validates and stores but never fetches, like apiSourceAdd.
func (s *Server) apiSourceImportOPML(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		OPML    string   `json:"opml"`
		Apply   bool     `json:"apply"`
		Include []string `json:"include"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, opml.MaxBytes*2)
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON (or exceeds 1 MB)")
		return
	}
	feeds, err := opml.Parse(strings.NewReader(in.OPML))
	if err != nil {
		apiErr(w, 400, "invalid_opml", err.Error())
		return
	}
	seen := map[string]bool{}
	domains := map[string]bool{}
	for _, src := range mustSources(r, pub.ID) {
		seen[opml.Key(src.URL)] = true
		domains[opml.Domain(src.URL)] = true
	}
	var only map[string]bool
	if len(in.Include) > 0 {
		only = map[string]bool{}
		for _, u := range in.Include {
			only[opml.Key(u)] = true
		}
	}
	type row struct {
		opml.Feed
		Status     string `json:"status"` // new | duplicate | invalid | skipped
		SameDomain bool   `json:"same_domain,omitempty"`
		Imported   bool   `json:"imported,omitempty"`
	}
	rows := []row{}
	counts := map[string]int{}
	for _, f := range feeds {
		rw := row{Feed: f}
		key := opml.Key(f.URL)
		switch {
		case !opml.Valid(f.URL):
			rw.Status = "invalid"
		case seen[key]:
			rw.Status = "duplicate"
		case only != nil && !only[key]:
			rw.Status = "skipped"
		default:
			rw.Status = "new"
			rw.SameDomain = domains[opml.Domain(f.URL)]
			seen[key] = true
			if in.Apply {
				if _, err := db(r).AddSource(r.Context(), pub.ID, "rss", f.URL, f.Name); err != nil {
					apiDBErr(w, r, err, "source")
					return
				}
				rw.Imported = true
			}
		}
		counts[rw.Status]++
		rows = append(rows, rw)
	}
	resp := map[string]any{"feeds": rows, "counts": counts, "applied": in.Apply}
	if s.d.Cfg.Mode == "demo" {
		resp["note"] = "Simulated in demo mode: sources are not fetched on the hosted demo."
	}
	apiJSON(w, 200, resp)
}
