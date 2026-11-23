package web

// Editoria management (KLOX-163): the radar's full, auditable desk
// list — every editoria in the catalog taxonomy with its sources and
// pick state — plus add/remove. Removing a desk unpicks its catalog
// sources through the same DeleteSource path a single-source removal
// uses: shared items, stories and past issues keep their rows, so the
// desk stops feeding the next collect/rank cycle while history stays.
// The desk's label token is also stripped back out of the topic the
// creation flow folded in, so the ranking profile follows.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

type apiEditoriaSource struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Available bool   `json:"available"` // catalog pick allowed today (launch|signal + enabled)
	Picked    bool   `json:"picked"`
	SourceID  int64  `json:"source_id,omitempty"`
	Suspended bool   `json:"suspended,omitempty"`
}

type apiEditoria struct {
	Key      string              `json:"key"`
	Label    string              `json:"label"`
	Selected bool                `json:"selected"` // recorded on publications.editorias
	Active   bool                `json:"active"`   // feeds the radar — at least one picked source
	Sources  []apiEditoriaSource `json:"sources"`
}

type apiCustomSource struct {
	SourceID int64  `json:"source_id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
}

// editoriasView renders the full desk list for a radar: every editoria
// in the taxonomy, its catalog sources with pick state, and the picked
// rows no desk claims (custom sources, or catalog rows the taxonomy
// does not cover).
func (s *Server) editoriasView(r *http.Request, pub *store.Publication) (map[string]any, error) {
	catByID, catBySlug, err := s.catalogMaps(r)
	if err != nil {
		return nil, err
	}
	srcs, err := db(r).Sources(r.Context(), pub.ID)
	if err != nil {
		return nil, err
	}
	pickedSlug := map[string]int64{} // catalog slug → sources.id
	custom := []apiCustomSource{}
	for _, src := range srcs {
		cs := catByID[src.CatalogID]
		if cs == nil || len(editoriasOf(cs.Slug)) == 0 {
			custom = append(custom, apiCustomSource{SourceID: src.ID, Name: src.Name, URL: src.URL})
			continue
		}
		pickedSlug[cs.Slug] = src.ID
	}
	selected := map[string]bool{}
	for _, k := range pub.Editorias {
		selected[k] = true
	}
	out := []apiEditoria{}
	for _, e := range editorias {
		ed := apiEditoria{Key: e.key, Label: e.label, Selected: selected[e.key],
			Sources: []apiEditoriaSource{}}
		for _, slug := range e.slugs {
			cs := catBySlug[slug]
			sid, isPicked := pickedSlug[slug]
			if cs == nil || (!pickable(cs) && !isPicked) {
				// absent from this catalog, or hold/blocked/disabled —
				// never listed as a pickable option (KLOX-45); a kept
				// pick still shows so removal stays reachable.
				continue
			}
			ed.Sources = append(ed.Sources, apiEditoriaSource{
				Slug: slug, Name: cs.Name, URL: cs.URL,
				Available: pickable(cs), Picked: isPicked, SourceID: sid,
				Suspended: cs.Suspended,
			})
			if isPicked {
				ed.Active = true
			}
		}
		out = append(out, ed)
	}
	return map[string]any{"editorias": out, "custom": custom}, nil
}

// catalogMaps indexes the global catalog by id and by slug. Demo mode
// has no catalog — both maps come back empty and picked sources land
// in the custom group.
func (s *Server) catalogMaps(r *http.Request) (map[int64]*store.CatalogSource, map[string]*store.CatalogSource, error) {
	byID, bySlug := map[int64]*store.CatalogSource{}, map[string]*store.CatalogSource{}
	if s.d.Pool == nil || s.d.Cfg.Mode == "demo" {
		return byID, bySlug, nil
	}
	cat, err := s.d.Pool.Catalog(r.Context())
	if err != nil {
		return nil, nil, err
	}
	for i := range cat {
		byID[cat[i].ID] = &cat[i]
		bySlug[cat[i].Slug] = &cat[i]
	}
	return byID, bySlug, nil
}

// apiEditorias lists every editoria with its sources and pick state.
func (s *Server) apiEditorias(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	v, err := s.editoriasView(r, pub)
	if apiDBErr(w, r, err, "editorias") {
		return
	}
	apiJSON(w, 200, v)
}

// pubDef carries a stored publication into the config.Publication
// shape UpdatePublication writes, so a handler that changes one field
// never drops the others.
func pubDef(pub *store.Publication) *config.Publication {
	return &config.Publication{
		Name: pub.Name, Topic: pub.Topic, Audience: pub.Audience,
		EditorialLine: pub.EditorialLine, RejectTerms: pub.RejectTerms,
		Tone: pub.Tone, Language: pub.Language, Slug: pub.Slug,
		Frequency: pub.Frequency, AIDisclosure: pub.AIDisclosure,
		Sections:  config.NormalizeSections(pub.Sections, pub.Paywall.PaidSections),
		Paywall:   pub.Paywall, Editorias: pub.Editorias,
	}
}

// apiEditoriaAdd selects a desk: records it on the radar, attaches its
// fetchable catalog sources (real mode — the demo catalog is a
// fixture, not the pool) and folds its label into the topic the
// ranking embeds, unless the words already appear there.
func (s *Server) apiEditoriaAdd(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Key string `json:"key"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	key := strings.ToLower(strings.TrimSpace(in.Key))
	if editoriaIndex(key) < 0 {
		apiErr(w, 400, "validation", "unknown editoria",
			apiReason{Code: "unknown_editoria", Message: "that desk is not in the catalog"})
		return
	}
	sel := map[string]bool{}
	for _, k := range pub.Editorias {
		sel[k] = true
	}
	sel[key] = true
	if s.d.Pool != nil && s.d.Cfg.Mode != "demo" {
		if cat, err := s.d.Pool.Catalog(r.Context()); err == nil {
			slugSet := map[string]bool{}
			for _, sl := range editoriaSlugs(key) {
				slugSet[sl] = true
			}
			for i := range cat {
				if slugSet[cat[i].Slug] && pickable(&cat[i]) {
					db(r).AddCatalogSource(r.Context(), pub.ID, &cat[i])
				}
			}
		}
	}
	def := pubDef(pub)
	def.Editorias = keySet(sel)
	def.Topic = foldEditoriaLabel(pub.Topic, key)
	if err := def.Validate(); err != nil {
		apiErr(w, 400, "validation", err.Error())
		return
	}
	if apiDBErr(w, r, db(r).UpdatePublication(r.Context(), pub.ID, def), "editorias") {
		return
	}
	s.editoriasResponse(w, r, pub.ID)
}

// apiEditoriaRemove deselects a desk and unpicks its sources. A pick
// survives when another desk that stays on also serves it — staying on
// means still selected, or holding a picked source the removed desk
// does not claim. Items, stories and past issues keep their rows:
// removal only drops the pick, so the desk stops feeding the next
// collect/rank cycle while history stays (KLOX-163 decision).
func (s *Server) apiEditoriaRemove(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	key := r.PathValue("key")
	if editoriaIndex(key) < 0 {
		apiErr(w, 400, "validation", "unknown editoria",
			apiReason{Code: "unknown_editoria", Message: "that desk is not in the catalog"})
		return
	}
	removedSlugs := map[string]bool{}
	for _, sl := range editoriaSlugs(key) {
		removedSlugs[sl] = true
	}
	var newSel []string
	sel := map[string]bool{}
	for _, k := range pub.Editorias {
		sel[k] = true
		if k != key {
			newSel = append(newSel, k)
		}
	}
	catByID, _, err := s.catalogMaps(r)
	if apiDBErr(w, r, err, "editorias") {
		return
	}
	srcs, err := db(r).Sources(r.Context(), pub.ID)
	if apiDBErr(w, r, err, "editorias") {
		return
	}
	pickedSlugs := map[string]bool{}
	for _, src := range srcs {
		if cs := catByID[src.CatalogID]; cs != nil {
			pickedSlugs[cs.Slug] = true
		}
	}
	// Desks that stay on: still selected, or active through a picked
	// source outside the removed desk. Their slugs shield a shared
	// source — removing fintech must not gut tecnologia's Tecnoblog.
	shield := map[string]bool{}
	for _, e := range editorias {
		if e.key == key {
			continue
		}
		on := sel[e.key]
		if !on {
			for _, sl := range e.slugs {
				if pickedSlugs[sl] && !removedSlugs[sl] {
					on = true
					break
				}
			}
		}
		if on {
			for _, sl := range e.slugs {
				shield[sl] = true
			}
		}
	}
	for _, src := range srcs {
		cs := catByID[src.CatalogID]
		if cs == nil || !removedSlugs[cs.Slug] || shield[cs.Slug] {
			continue
		}
		db(r).DeleteSource(r.Context(), pub.ID, src.ID)
	}
	def := pubDef(pub)
	def.Editorias = newSel
	def.Topic = stripEditoriaLabel(pub.Topic, key)
	if err := def.Validate(); err != nil {
		apiErr(w, 400, "validation", err.Error())
		return
	}
	if apiDBErr(w, r, db(r).UpdatePublication(r.Context(), pub.ID, def), "editorias") {
		return
	}
	dropPautaDay(r, db(r), pub.ID) // the profile changed — today's pauta rebuilds
	s.editoriasResponse(w, r, pub.ID)
}

// editoriasResponse answers add/remove with the fresh publication and
// desk list in one payload.
func (s *Server) editoriasResponse(w http.ResponseWriter, r *http.Request, pubID int64) {
	fresh, err := db(r).GetPublication(r.Context(), fmt.Sprint(pubID))
	if apiDBErr(w, r, err, "editorias") {
		return
	}
	v, err := s.editoriasView(r, fresh)
	if apiDBErr(w, r, err, "editorias") {
		return
	}
	v["publication"] = publicationView(fresh)
	apiJSON(w, 200, v)
}

// keySet flattens a selection set to a stable-ordered key list.
func keySet(sel map[string]bool) []string {
	var out []string
	for _, e := range editorias {
		if sel[e.key] {
			out = append(out, e.key)
		}
	}
	return out
}

// foldEditoriaLabel appends the desk's label to the topic unless the
// words already appear as a comma token — creation and desk selection
// fold the same way.
func foldEditoriaLabel(topic, key string) string {
	label := editoriaLabel(key)
	for _, t := range commaTokens(topic) {
		if strings.EqualFold(t, label) || strings.EqualFold(t, key) {
			return topic
		}
	}
	if strings.TrimSpace(topic) == "" {
		return label
	}
	return strings.TrimSpace(topic) + ", " + label
}

// stripEditoriaLabel removes the desk's label when it appears as a
// whole comma token in the topic — the reverse of the creation fold.
// A label embedded in free text ("fintechs brasileiras") is left alone.
func stripEditoriaLabel(topic, key string) string {
	label := editoriaLabel(key)
	var kept []string
	for _, t := range commaTokens(topic) {
		if strings.EqualFold(t, label) || strings.EqualFold(t, key) {
			continue
		}
		kept = append(kept, t)
	}
	return strings.Join(kept, ", ")
}

func commaTokens(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
