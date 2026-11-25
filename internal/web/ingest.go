package web

// Real-mode source catalog + ingestion admin surface (D29/D30).
// The catalog is global: picks are launch/signal entries the scheduler
// already polls; hold/blocked entries never appear in user-facing
// source lists (owner decision, KLOX-45). The admin view is the
// operator's fetch-log surface — gated by KLOXUS_MASTER_KEY.
import (
	"net/http"
	"sort"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// apiRealCatalog answers GET .../sources/catalog in real mode.
func (s *Server) apiRealCatalog(w http.ResponseWriter, r *http.Request, pub *store.Publication) {
	cat, err := s.d.Pool.Catalog(r.Context())
	if apiDBErr(w, r, err, "catalog") {
		return
	}
	existing := map[string]bool{}
	for _, src := range mustSources(r, pub.ID) {
		existing[src.URL] = true
	}
	ordered := offeredOrder(cat, existing)
	offered := []map[string]any{}
	for i := range ordered {
		cs := &ordered[i]
		if !pickable(cs) {
			// hold/blocked are never fetched — and never listed as
			// user-facing source options (KLOX-45).
			continue
		}
		if existing[cs.URL] {
			continue
		}
		row := map[string]any{
			"type": "rss", "url": cs.URL, "name": cs.Name,
			"policy": cs.Policy, "kind": cs.Kind,
		}
		if cs.Suspended {
			row["suspended"] = true
			row["note"] = "Paused: " + cs.SuspendReason
		}
		if n, err := s.d.Pool.CatalogItemsPerWeek(r.Context(), cs.ID); err == nil {
			row["items_per_week"] = n
		}
		offered = append(offered, row)
	}
	apiJSON(w, 200, map[string]any{"offered": offered})
}

// offeredOrder ranks the catalog for the suggested-sources list by
// adherence to what the radar already reads: a source scores the sum,
// over each onboarding desk it belongs to, of that desk's already-added
// sources — a fintech radar sees finance feeds before tech ones
// (KLOX-207, teste-final §5.13). Ties keep the catalog's order.
func offeredOrder(cat []store.CatalogSource, added map[string]bool) []store.CatalogSource {
	byURL := map[string]string{}
	for i := range cat {
		byURL[cat[i].URL] = cat[i].Slug
	}
	addedSlugs := map[string]bool{}
	for u := range added {
		if slug, ok := byURL[u]; ok {
			addedSlugs[slug] = true
		}
	}
	score := map[string]int{}
	for _, desk := range nicheSources {
		n := 0
		for _, slug := range desk {
			if addedSlugs[slug] {
				n++
			}
		}
		for _, slug := range desk {
			score[slug] += n
		}
	}
	out := append([]store.CatalogSource(nil), cat...)
	sort.SliceStable(out, func(i, j int) bool {
		return score[out[i].Slug] > score[out[j].Slug]
	})
	return out
}

// realCatalogPreview serves stored items for a catalog source's site URL.
func (s *Server) realCatalogPreview(w http.ResponseWriter, r *http.Request, rawURL string) bool {
	cs, err := s.d.Pool.CatalogEntry(r.Context(), rawURL)
	if err != nil || cs == nil {
		return false
	}
	if !pickable(cs) {
		apiErr(w, 400, "source_policy", notPickableMsg(cs))
		return true
	}
	items, _ := s.d.Pool.CatalogItemsPreview(r.Context(), cs.ID, 3)
	out := []map[string]any{}
	for _, it := range items {
		row := map[string]any{
			"title": it.Title, "url": it.URL,
			"outlet": cs.Name, "published_at": it.PublishedAt,
			"kind": it.Kind,
		}
		out = append(out, row)
	}
	resp := map[string]any{"items": out}
	if len(out) == 0 {
		resp["note"] = "No items yet — the global collector polls this source on its own schedule."
	}
	apiJSON(w, 200, resp)
	return true
}

// realSourceAdd resolves a URL to a catalog entry and records the pick.
// Anonymous workspaces only get catalog sources — pasted URLs are an
// SSRF surface and land with claimed workspaces (KLOX-11+).
func (s *Server) realSourceAdd(w http.ResponseWriter, r *http.Request, pub *store.Publication, rawURL, name string) {
	cs, err := s.d.Pool.CatalogEntry(r.Context(), rawURL)
	if err != nil || cs == nil {
		apiErr(w, 400, "catalog_only",
			"that source is not in the catalog — anonymous workspaces pick from the listed sources")
		return
	}
	if !pickable(cs) {
		apiErr(w, 400, "source_policy", notPickableMsg(cs))
		return
	}
	id, err := db(r).AddCatalogSource(r.Context(), pub.ID, cs)
	if apiDBErr(w, r, err, "source") {
		return
	}
	var row apiSourceRow
	for _, src := range mustSources(r, pub.ID) {
		if src.ID == id {
			row = s.sourceRow(r, src)
		}
	}
	if row.ID == 0 {
		row.ID, row.Name, row.URL, row.Type = id, cs.Name, cs.URL, "rss"
	}
	apiJSON(w, 201, map[string]any{"source": row})
}

// realSourceConfirm picks catalog sources by URL (or all fetchable
// ones when the body lists none) and marks onboarding done.
func (s *Server) realSourceConfirm(w http.ResponseWriter, r *http.Request, pub *store.Publication, include []string, decoded bool) {
	cat, err := s.d.Pool.Catalog(r.Context())
	if apiDBErr(w, r, err, "catalog") {
		return
	}
	existing := map[string]bool{}
	for _, src := range mustSources(r, pub.ID) {
		existing[src.URL] = true
	}
	want := map[string]bool{}
	for _, u := range include {
		want[strings.TrimSpace(u)] = true
	}
	for i := range cat {
		cs := &cat[i]
		if !pickable(cs) {
			continue
		}
		if existing[cs.URL] || (decoded && len(want) > 0 && !want[cs.URL] && !want[cs.Slug]) {
			continue
		}
		db(r).AddCatalogSource(r.Context(), pub.ID, cs)
	}
	db(r).SetProgress(r.Context(), "sources_confirmed")
	out := []apiSourceRow{}
	for _, src := range mustSources(r, pub.ID) {
		out = append(out, s.sourceRow(r, src))
	}
	apiJSON(w, 200, map[string]any{"sources": out})
}

// apiAdminIngestion is the operator's fetch-status view. It answers
// only when KLOXUS_MASTER_KEY is configured and the caller presents it
// (X-Admin-Key or ?key=) — without a key the route looks absent.
func (s *Server) apiAdminIngestion(w http.ResponseWriter, r *http.Request) {
	if s.d.Cfg.MasterKey == "" || s.d.Pool == nil {
		s.apiNotFound(w, r)
		return
	}
	key := r.Header.Get("X-Admin-Key")
	if key == "" {
		key = r.URL.Query().Get("key")
	}
	if key != s.d.Cfg.MasterKey {
		apiErr(w, 403, "forbidden", "invalid admin key")
		return
	}
	cat, err := s.d.Pool.Catalog(r.Context())
	if apiDBErr(w, r, err, "catalog") {
		return
	}
	type srcView struct {
		store.CatalogSource
		ItemsWeek int                 `json:"items_week"`
		Items     int                 `json:"items"`
		Log       []store.FetchLogRow `json:"log"`
	}
	out := []srcView{}
	for i := range cat {
		cs := &cat[i]
		v := srcView{CatalogSource: *cs}
		v.ItemsWeek, _ = s.d.Pool.CatalogItemsPerWeek(r.Context(), cs.ID)
		v.Items, _ = s.d.Pool.CatalogItemsCount(r.Context(), cs.ID)
		v.Log, _ = s.d.Pool.FetchLog(r.Context(), cs.ID, 10)
		if v.Log == nil {
			v.Log = []store.FetchLogRow{}
		}
		out = append(out, v)
	}
	apiJSON(w, 200, map[string]any{"sources": out})
}

// pickable reports whether a catalog source may be offered to or picked by
// a workspace: launch/signal policy and not disabled (KLOX-152 — a disabled
// source such as TabNews must not reappear in onboarding).
func pickable(cs *store.CatalogSource) bool {
	return cs.Enabled && (cs.Policy == "launch" || cs.Policy == "signal")
}

// notPickableMsg explains why a catalog source cannot be picked.
func notPickableMsg(cs *store.CatalogSource) string {
	if !cs.Enabled {
		return cs.Name + " is disabled (" + cs.Note + ") — it is never fetched"
	}
	return cs.Name + " is " + cs.Policy + " (" + cs.Note + ") — it is never fetched"
}
