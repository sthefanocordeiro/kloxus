package web_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
	"github.com/sthefanocordeiro/kloxus/internal/web"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// KLOX-163: the desk list is complete and auditable (every editoria,
// every pickable source, pick state), the selection persists on the
// radar, and removing a desk unpicks its sources — except the ones a
// desk that stays on also serves — while the topic label is stripped.

func editoriasHosted(t *testing.T) (*web.Server, *store.Pool, *workspace.Manager) {
	t.Helper()
	pool := testdb.Pool(t)
	mgr, err := workspace.OpenHosted(t.Context(), pool, []byte("test-secret-key-32-bytes-padded-ok!"), nil)
	if err != nil {
		t.Fatalf("OpenHosted: %v", err)
	}
	t.Cleanup(mgr.Close)
	cfg := &config.Config{
		PublishingEnabled: true, Mode: "real", LLMMode: "off", Port: 8080,
		PublicURL: "https://kloxus.test",
	}
	srv := web.New(web.Deps{
		WS: mgr, Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.None{},
		Monitor: sources.FakeMonitor{}, Scraper: sources.FakeScraper{},
		Pool: pool,
	})
	return srv, pool, mgr
}

func editoriaOf(t *testing.T, list []any, key string) map[string]any {
	t.Helper()
	for _, e := range list {
		if m, ok := e.(map[string]any); ok && m["key"] == key {
			return m
		}
	}
	t.Fatalf("editoria %q not in %v", key, list)
	return nil
}

func sourceOf(t *testing.T, ed map[string]any, slug string) map[string]any {
	t.Helper()
	for _, s := range ed["sources"].([]any) {
		if m, ok := s.(map[string]any); ok && m["slug"] == slug {
			return m
		}
	}
	t.Fatalf("source %q not in %v", slug, ed)
	return nil
}

func TestEditoriasManage(t *testing.T) {
	srv, _, mgr := editoriasHosted(t)
	cookie, _ := pautaVisitor(t, mgr)

	rr := pautaServe(t, srv, "POST", "/api/v1/radar", cookie,
		map[string]any{"about": "fintech, Pix e regulação", "niches": []string{"fintech"}})
	if rr.Code != 201 {
		t.Fatalf("radar = %d %s", rr.Code, rr.Body.String())
	}
	pub := apiJSON(t, rr)["publication"].(map[string]any)
	pubID := int64(pub["id"].(float64))
	base := fmt.Sprintf("/api/v1/publications/%d", pubID)

	// creation persisted the selection
	if eds, _ := pub["editorias"].([]any); len(eds) != 1 || eds[0] != "fintech" {
		t.Fatalf("editorias = %v", pub["editorias"])
	}

	// the list is complete — every taxonomy desk, pick states readable
	rr = pautaServe(t, srv, "GET", base+"/editorias", cookie, nil)
	if rr.Code != 200 {
		t.Fatalf("editorias = %d %s", rr.Code, rr.Body.String())
	}
	eds, _ := apiJSON(t, rr)["editorias"].([]any)
	if len(eds) != 10 {
		t.Fatalf("editorias = %d, want the 10 desks", len(eds))
	}
	fin := editoriaOf(t, eds, "fintech")
	if fin["selected"] != true || fin["active"] != true {
		t.Fatalf("fintech = %v", fin)
	}
	if sourceOf(t, fin, "finsiders")["picked"] != true {
		t.Fatal("finsiders not picked after fintech radar create")
	}
	// tecnologia is not selected but shows active — the shared tecnoblog
	// pick feeds it; the audit sees the real feed, not only the choice
	tec := editoriaOf(t, eds, "tecnologia")
	if tec["active"] != true || tec["selected"] != false {
		t.Fatalf("tecnologia = %v", tec)
	}
	// hold/blocked slugs stay out of the offered source rows
	for _, e := range eds {
		for _, s := range e.(map[string]any)["sources"].([]any) {
			m := s.(map[string]any)
			if m["picked"] != true && m["available"] != true {
				t.Fatalf("unpickable source listed: %v", m)
			}
		}
	}

	// unknown desks are refused
	rr = pautaServe(t, srv, "POST", base+"/editorias", cookie, map[string]any{"key": "bogus"})
	if rr.Code != 400 {
		t.Fatalf("add bogus = %d", rr.Code)
	}
	rr = pautaServe(t, srv, "DELETE", base+"/editorias/bogus", cookie, nil)
	if rr.Code != 400 {
		t.Fatalf("remove bogus = %d", rr.Code)
	}

	// add a desk: selected + its pickable sources attach + label folds in
	rr = pautaServe(t, srv, "POST", base+"/editorias", cookie, map[string]any{"key": "carreira"})
	if rr.Code != 200 {
		t.Fatalf("add carreira = %d %s", rr.Code, rr.Body.String())
	}
	body := apiJSON(t, rr)
	pub = body["publication"].(map[string]any)
	if eds, _ := pub["editorias"].([]any); len(eds) != 2 {
		t.Fatalf("editorias after add = %v", pub["editorias"])
	}
	if topic, _ := pub["topic"].(string); !strings.Contains(
		strings.ToLower(topic), "carreira e rh") {
		t.Fatalf("topic after add = %q", topic)
	}
	car := editoriaOf(t, body["editorias"].([]any), "carreira")
	if car["selected"] != true || car["active"] != true {
		t.Fatalf("carreira = %v", car)
	}
	if sourceOf(t, car, "mundo-rh")["picked"] != true ||
		sourceOf(t, car, "rh-portal")["picked"] != true {
		t.Fatal("carreira sources did not attach")
	}

	// a PUT on settings must not wipe the selection
	rr = pautaServe(t, srv, "PUT", base, cookie, map[string]any{"name": "Radar Fintech"})
	if rr.Code != 200 {
		t.Fatalf("put = %d %s", rr.Code, rr.Body.String())
	}
	got := apiGet0(t, srv, base, cookie)["publication"].(map[string]any)
	if eds, _ := got["editorias"].([]any); len(eds) != 2 {
		t.Fatalf("editorias after PUT = %v", got["editorias"])
	}

	// remove carreira: its sources leave, its label leaves the topic
	rr = pautaServe(t, srv, "DELETE", base+"/editorias/carreira", cookie, nil)
	if rr.Code != 200 {
		t.Fatalf("remove carreira = %d %s", rr.Code, rr.Body.String())
	}
	body = apiJSON(t, rr)
	pub = body["publication"].(map[string]any)
	if eds, _ := pub["editorias"].([]any); len(eds) != 1 || eds[0] != "fintech" {
		t.Fatalf("editorias after remove = %v", pub["editorias"])
	}
	if topic, _ := pub["topic"].(string); strings.Contains(
		strings.ToLower(topic), "carreira") {
		t.Fatalf("topic after remove = %q", topic)
	}
	car = editoriaOf(t, body["editorias"].([]any), "carreira")
	if car["active"] == true || car["selected"] == true {
		t.Fatalf("carreira after remove = %v", car)
	}

	// select startups too, so the shared "startups" slug is claimed by
	// a desk that stays on — every startups slug is also in fintech's
	// set, so without this selection nothing would shield it
	rr = pautaServe(t, srv, "POST", base+"/editorias", cookie, map[string]any{"key": "startups"})
	if rr.Code != 200 {
		t.Fatalf("add startups = %d %s", rr.Code, rr.Body.String())
	}
	if st := editoriaOf(t, apiJSON(t, rr)["editorias"].([]any), "startups"); st["selected"] != true {
		t.Fatalf("startups after add = %v", st)
	}

	// remove fintech: finsiders goes; tecnoblog is shared with desks
	// that have no pick outside fintech — it goes too; startups survives
	// under the still-selected startups desk
	rr = pautaServe(t, srv, "DELETE", base+"/editorias/fintech", cookie, nil)
	if rr.Code != 200 {
		t.Fatalf("remove fintech = %d %s", rr.Code, rr.Body.String())
	}
	body = apiJSON(t, rr)
	fin = editoriaOf(t, body["editorias"].([]any), "fintech")
	// selected must clear; active stays true on purpose — the shielded
	// startups/techcrunch/exame picks are in fintech's slug list too,
	// and the audit shows the real feed, not only the choice
	if fin["selected"] == true || fin["active"] != true {
		t.Fatalf("fintech after remove = %v", fin)
	}
	for _, slug := range []string{"finsiders", "infomoney", "tecnoblog"} {
		if src := sourceOf(t, fin, slug); src["picked"] == true {
			t.Fatalf("fintech source %q still picked after remove: %v", slug, src)
		}
	}
	// the selected startups desk keeps its slugs — its row must still
	// show active, which shields nothing outside its own set
	if st := editoriaOf(t, body["editorias"].([]any), "startups"); st["active"] != true {
		t.Fatalf("startups after fintech remove = %v", st)
	}
	// and tecnoblog left: tecnologia has no picked source outside
	// fintech's set, so it could not shield the shared pick
	srcs := apiGet0(t, srv, base+"/sources", cookie)["sources"].([]any)
	names := map[string]bool{}
	for _, s := range srcs {
		names[s.(map[string]any)["name"].(string)] = true
	}
	if names["Finsiders Brasil"] || names["Tecnoblog"] || names["InfoMoney"] {
		t.Fatalf("removed desk sources still picked: %v", names)
	}
	if !names["Startups.com.br"] {
		t.Fatalf("startups pick lost on fintech remove: %v", names)
	}
}

// apiGet0 wraps apiCall for a plain GET with the workspace cookie.
func apiGet0(t *testing.T, srv *web.Server, path, cookie string) map[string]any {
	t.Helper()
	rr := pautaServe(t, srv, "GET", path, cookie, nil)
	if rr.Code != 200 {
		t.Fatalf("GET %s = %d %s", path, rr.Code, rr.Body.String())
	}
	return apiJSON(t, rr)
}
