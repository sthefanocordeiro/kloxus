package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const testFeed = `<?xml version="1.0"?>
<rss version="2.0"><channel><title>T</title>
<item><title>Regulador abre consulta</title><link>https://a.example/1</link><guid>g1</guid>
<description>O regulador abriu consulta pública sobre IA em contratações.</description></item>
<item><title>Rumor de gadget</title><link>https://a.example/2</link><guid>g2</guid>
<description>Um rumor sobre um gadget que não existe.</description></item>
</channel></rss>`

var canned = map[string]string{
	"triage":    `[{"ref":1,"score":9.0,"reason":"afeta leitores"},{"ref":2,"score":1.5,"reason":"rumor sem fonte"}]`,
	"storylist": `{"items":[{"refs":[1],"title":"Consulta do regulador","theme":"regulação","why":"afeta quem contrata","section":"Lead"}]}`,
	"dossier":   `[{"ref":1,"summary":"Resumo original [1].","facts":[{"text":"Consulta aberta","ref":1}],"quote":{"text":"","attribution":""},"unknowns":["prazo"],"interpretation":["impacto"],"sources":[{"ref":1,"url":"https://a.example/1"}]}]`,
	"deepdive":  `[{"ref":1,"deep_facts":[{"text":"fundo extra","ref":1}],"extra_unknowns":[]}]`,
	"draft":     `{"entries":[{"ref":1,"headline":"Regulador quer ouvir o mercado","body_md":"O regulador abriu consulta sobre IA em contratações [1]."}]}`,
	"citecheck": `[]`,
}

func TestRunHeadless(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	def := &config.Publication{
		Slug: "p", Name: "Pub", Language: "pt-BR",
		Sections: []config.Section{{Title: "Lead", Items: 1, Commentary: "required"}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "feeds/a.xml"}},
	}
	if _, err := db.SeedPublication(ctx, def); err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}

	fsys := fstest.MapFS{"feeds/a.xml": {Data: []byte(testFeed)}}
	fetcher := sources.FixtureFetcher{FS: fsys}
	var deepPrompt string
	provider := llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
		if req.Template == "deepdive" {
			deepPrompt = req.User
		}
		text, ok := canned[req.Template]
		if !ok {
			return llm.Response{}, nil
		}
		return llm.Response{Text: text, InputTokens: 1000, OutputTokens: 200}, nil
	})

	outDir := t.TempDir()
	res, err := RunHeadless(ctx, db, pub, outDir, fetcher, provider, "haiku", "sonnet", 3, false)
	if err != nil {
		t.Fatalf("RunHeadless: %v", err)
	}
	if res.Items != 2 || res.Stories != 2 || res.Selected != 1 {
		t.Fatalf("counts: %+v", res)
	}
	if res.CostUSD <= 0 {
		t.Errorf("expected nonzero cost, got %f", res.CostUSD)
	}
	body, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	for _, want := range []string{"## Lead", "### Regulador quer ouvir o mercado", "[1]", "commentary", "Sources:"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("output missing %q", want)
		}
	}
	var runs int
	db.QueryRow(`SELECT count(*) FROM runs WHERE publication_id = ?`, pub.ID).Scan(&runs)
	if runs != len(Steps) {
		t.Errorf("runs = %d, want %d", runs, len(Steps))
	}
	var status string
	db.QueryRow(`SELECT status FROM issues WHERE id = ?`, res.IssueID).Scan(&status)
	if status != "drafted" {
		t.Errorf("issue status = %q", status)
	}
	if strings.Contains(deepPrompt, `"title":`) {
		t.Errorf("deepdive prompt contains stored dossier title")
	}
	var dossierJSON string
	db.QueryRow(`SELECT d.json FROM dossiers d JOIN issue_items ii ON ii.id = d.issue_item_id WHERE ii.issue_id = ?`, res.IssueID).Scan(&dossierJSON)
	if !strings.Contains(dossierJSON, `"title":"Consulta do regulador"`) {
		t.Errorf("stored dossier lost picked title: %s", dossierJSON)
	}
}

func TestPhaseBReplayUsesCanonicalItems(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	def := &config.Publication{
		Slug: "p", Name: "Pub", Language: "pt-BR",
		Sections: []config.Section{{Title: "Lead", Items: 2, Commentary: "none"}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "feeds/a.xml"}},
	}
	if _, err := db.SeedPublication(ctx, def); err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}

	responses := map[string]string{
		"triage":    canned["triage"],
		"storylist": `{"items":[{"refs":[1],"title":"Consulta do regulador","theme":"regulação","why":"afeta quem contrata","section":"Lead"},{"refs":[2],"title":"Rumor verificado","theme":"rumor","why":"cobertura suficiente","section":"Lead"}]}`,
		"dossier": `[
			{"ref":1,"summary":"Resumo um [1].","facts":[{"text":"Fato um","ref":1}],"quote":{"text":"","attribution":""},"unknowns":[],"interpretation":[],"sources":[{"ref":1,"url":"https://a.example/1"}]},
			{"ref":2,"summary":"Resumo dois [1].","facts":[{"text":"Fato dois","ref":1}],"quote":{"text":"","attribution":""},"unknowns":[],"interpretation":[],"sources":[{"ref":1,"url":"https://a.example/2"}]}
		]`,
		"deepdive": `[]`,
	}
	fsys := fstest.MapFS{"feeds/a.xml": {Data: []byte(testFeed)}}
	runner := &Runner{
		DB: db, Fetcher: sources.FixtureFetcher{FS: fsys}, TriageModel: "haiku", DraftModel: "sonnet", MaxDeepDives: 3,
		DemoMode: true,
		LLM: llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
			return llm.Response{Text: responses[req.Template], InputTokens: 1, OutputTokens: 1}, nil
		}),
	}
	resA, err := runner.RunPhaseA(ctx, pub)
	if err != nil {
		t.Fatalf("RunPhaseA: %v", err)
	}
	iis, err := db.IssueItems(ctx, resA.IssueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	if len(iis) != 2 {
		t.Fatalf("issue items = %d, want 2", len(iis))
	}
	if err := db.SetIssueItemStatus(ctx, iis[0].ID, "rejected"); err != nil {
		t.Fatalf("SetIssueItemStatus: %v", err)
	}

	allEntries, allDossiers, err := runner.loadEntries(ctx, resA.IssueID, true)
	if err != nil {
		t.Fatalf("loadEntries: %v", err)
	}
	type drItem struct {
		Ref            int
		Section        string
		Score          float64
		Title          string
		Summary        string
		Facts          []DossierFact
		DeepFacts      []DossierFact
		Interpretation []string
	}
	drItems := make([]drItem, len(allEntries))
	for i, e := range allEntries {
		d := allDossiers[e.ref]
		drItems[i] = drItem{Ref: e.ref, Section: e.section, Score: e.score, Title: e.title,
			Summary: d.Summary, Facts: d.Facts, DeepFacts: d.DeepFacts, Interpretation: d.Interpretation}
	}
	draftUser, err := RenderPrompt("draft", map[string]any{
		"Publication": pub.Name, "Audience": pub.Audience, "Language": pub.Language,
		"Tone": pub.Tone, "Items": drItems,
	})
	if err != nil {
		t.Fatalf("RenderPrompt draft: %v", err)
	}

	draftText := `{"entries":[{"ref":1,"headline":"Um","body_md":"Fato um [1]."},{"ref":2,"headline":"Dois","body_md":"Fato dois [1]."}]}`
	draftByRef := map[int]struct {
		headline, body string
	}{
		1: {headline: "Um", body: "Fato um [1]."},
		2: {headline: "Dois", body: "Fato dois [1]."},
	}
	sectionOrder := []string{pub.Sections[0].Title}
	checkBody := assembleIssue(pub, resA.Number, allEntries, draftByRef, allDossiers, sectionOrder)
	type cSent struct {
		ID   int
		Item int
		Refs string
		Text string
	}
	type cSource struct {
		Ref int
		URL string
	}
	type cItem struct {
		Ref     int
		Sources []cSource
	}
	var cs []cSent
	for _, s := range extractSentences(checkBody) {
		cs = append(cs, cSent{ID: s.id, Item: s.itemRef, Refs: s.refs, Text: s.text})
	}
	var ci []cItem
	for _, e := range allEntries {
		c := cItem{Ref: e.ref}
		for _, s := range allDossiers[e.ref].Sources {
			c.Sources = append(c.Sources, cSource{Ref: s.Ref, URL: s.URL})
		}
		ci = append(ci, c)
	}
	checkUser, err := RenderPrompt("citecheck", map[string]any{
		"Publication": pub.Name, "Sentences": cs, "Items": ci,
	})
	if err != nil {
		t.Fatalf("RenderPrompt citecheck: %v", err)
	}

	fixtures := fstest.MapFS{}
	addFixture := func(req llm.Request, text string) {
		data, err := json.Marshal(map[string]any{
			"model": req.Model, "text": text, "input_tokens": 1, "output_tokens": 1,
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		fixtures["fixtures/llm/"+llm.Key(req)+".json"] = &fstest.MapFile{Data: data}
	}
	addFixture(llm.Request{Model: "sonnet", Template: "draft", User: draftUser}, draftText)
	addFixture(llm.Request{Model: "haiku", Template: "citecheck", User: checkUser},
		`[{"id":1,"verdict":"unsupported","note":"rejected item"},{"id":2,"verdict":"unsupported","note":"active item"}]`)

	runner.LLM = llm.Replay{FS: fixtures, Dir: "fixtures/llm"}
	resB, err := runner.RunPhaseB(ctx, pub, resA.IssueID)
	if err != nil {
		t.Fatalf("RunPhaseB: %v", err)
	}
	if resB.Selected != 1 {
		t.Fatalf("selected = %d, want 1", resB.Selected)
	}
	iss, err := db.GetIssue(ctx, resA.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if strings.Contains(iss.BodyMD, "<!-- item 1 -->") {
		t.Errorf("rejected item present in issue body")
	}
	if !strings.Contains(iss.BodyMD, "<!-- item 2 -->") {
		t.Errorf("active item missing from issue body")
	}
	if len(iss.Flags) != 1 || iss.Flags[0].Item != 2 || iss.Flags[0].Note != "active item" {
		t.Fatalf("flags = %+v, want only active item flag", iss.Flags)
	}
}
