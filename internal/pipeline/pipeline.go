// Package pipeline runs the per-issue editorial pipeline:
// collect → dedupe → triage → storylist → dossier → deepdive → draft →
// citecheck. Model calls go through llm.Provider so demo mode replays
// recorded fixtures and real mode calls the API.
package pipeline

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

//go:embed prompts
var promptFS embed.FS

// Steps is the canonical pipeline order, shared with progress displays.
var Steps = []string{
	"collect", "dedupe", "triage", "storylist", "dossier", "deepdive", "draft", "citecheck",
}

// StepsA is the Phase A (prepare) subset shown on the run page.
var StepsA = Steps[:6]

// Runner executes one pipeline run for a publication.
type Runner struct {
	DB           *store.DB
	LLM          llm.Provider
	Fetcher      sources.Fetcher
	TriageModel  string
	DraftModel   string
	MaxDeepDives int
	// DemoMode makes PhaseB send the canonical all-selected item set to
	// draft/citecheck (stable fixture keys) while the stored body and
	// flags are built from active items only.
	DemoMode bool
	// OnStep, if set, is called as each step starts (progress reporting).
	OnStep func(step string)
}

// Result summarizes a completed run.
type Result struct {
	IssueID    int64
	Number     int
	Path       string
	Items      int
	New        int
	Stories    int
	Selected   int
	Flagged    []Flagged
	CostUSD    float64
	NoNewItems bool // nothing new since the last issue
}

// Flagged is a sentence the citation check could not verify.
type Flagged struct {
	Item     int64  `json:"item"`
	Sentence string `json:"sentence"`
	Note     string `json:"note"`
}

// entry bundles an issue item with its merged member items.
type entry struct {
	iiID    int64
	storyID int64
	ref     int
	title   string
	section string
	why     string
	score   float64
	sources []store.Item
}

// RenderPrompt renders an embedded prompt template by name.
func RenderPrompt(name string, data any) (string, error) {
	t, err := template.ParseFS(promptFS, "prompts/"+name+".tmpl")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func decodeJSON(text string, out any) error {
	s := strings.TrimSpace(text)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if err := json.Unmarshal([]byte(s), out); err == nil {
		return nil
	}
	start := strings.IndexAny(s, "[{")
	end := strings.LastIndexAny(s, "]}")
	if start < 0 || end <= start {
		return fmt.Errorf("no JSON found in response")
	}
	return json.Unmarshal([]byte(s[start:end+1]), out)
}

// call renders a prompt, calls the model and decodes JSON output,
// retrying once on invalid JSON.
func (r *Runner) call(ctx context.Context, model, name string, data any, out any) (llm.Response, error) {
	user, err := RenderPrompt(name, data)
	if err != nil {
		return llm.Response{}, err
	}
	req := llm.Request{Model: model, Template: name, User: user, MaxTokens: 8192}
	resp, err := r.LLM.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if err := decodeJSON(resp.Text, out); err != nil {
		if r.DemoMode {
			// A retry would change the request and miss the fixture.
			return resp, fmt.Errorf("template %s: %w", name, err)
		}
		req.User = user + "\n\nYour previous reply was not valid JSON (" + err.Error() + "). Reply with ONLY the JSON."
		resp2, err2 := r.LLM.Complete(ctx, req)
		if err2 != nil {
			return resp2, err2
		}
		if err := decodeJSON(resp2.Text, out); err != nil {
			return resp2, fmt.Errorf("template %s: %w", name, err)
		}
		return resp2, nil
	}
	return resp, nil
}

func (r *Runner) record(ctx context.Context, pubID, issueID int64, step, model string, start time.Time, resp llm.Response) {
	r.DB.RecordRun(ctx, store.Run{
		PublicationID: pubID,
		IssueID:       issueID,
		Step:          step,
		Model:         model,
		InputTokens:   resp.InputTokens,
		OutputTokens:  resp.OutputTokens,
		CostUSD:       llm.CostUSD(model, resp.InputTokens, resp.OutputTokens),
		DurationMS:    time.Since(start).Milliseconds(),
	})
}

func (r *Runner) guide(ctx context.Context, pubID int64) string {
	_, content, err := r.DB.LatestGuide(ctx, pubID)
	if err != nil {
		return ""
	}
	return content
}

// Run executes the full pipeline and writes the issue file under outDir.
// Equivalent to RunPhaseA followed by RunPhaseB (headless mode).
func (r *Runner) Run(ctx context.Context, pub *store.Publication, outDir string) (*Result, error) {
	res, err := r.RunPhaseA(ctx, pub)
	if err != nil {
		return nil, err
	}
	if res.NoNewItems {
		return res, nil
	}
	// Headless run: no review step, so every selected item is approved.
	items, err := r.DB.IssueItems(ctx, res.IssueID)
	if err != nil {
		return nil, err
	}
	for _, ii := range items {
		if ii.Status == "pending" {
			if err := r.DB.SetIssueItemStatus(ctx, ii.ID, "approved"); err != nil {
				return nil, err
			}
		}
	}
	b, err := r.RunPhaseB(ctx, pub, res.IssueID)
	if err != nil {
		return nil, err
	}
	res.Flagged = b.Flagged
	res.CostUSD += b.CostUSD
	return r.finish(ctx, res, outDir)
}

func (r *Runner) stepStart(name string) {
	if r.OnStep != nil {
		r.OnStep(name)
	}
}

// RunPhaseA runs collect → dedupe → triage → storylist → dossier →
// deepdive, producing the issue and its items for review.
func (r *Runner) RunPhaseA(ctx context.Context, pub *store.Publication) (*Result, error) {
	res := &Result{}

	// --- collect ---
	r.stepStart("collect")
	start := time.Now()
	srcs, err := r.DB.Sources(ctx, pub.ID)
	if err != nil {
		return nil, err
	}
	for _, s := range srcs {
		if r.DemoMode && !strings.HasPrefix(s.URL, "fixtures/") {
			// Visitor-added sources are never fetched on the hosted demo
			// (SSRF surface); they are stored but skipped at collect time.
			r.DB.MarkSourceFetched(ctx, s.ID, "", "simulated in demo mode: sources are not fetched")
			continue
		}
		raw, err := r.Fetcher.Fetch(ctx, s.Type, s.URL)
		if err != nil {
			r.DB.MarkSourceFetched(ctx, s.ID, "", "error: "+truncate(err.Error(), 120))
			continue // one bad source must not abort the run
		}
		n, err := r.DB.SaveItems(ctx, s.ID, raw, dedupe.Canonical)
		if err != nil {
			return nil, err
		}
		outlet := ""
		if len(raw) > 0 {
			outlet = raw[0].Outlet
		}
		r.DB.MarkSourceFetched(ctx, s.ID, outlet, fmt.Sprintf("ok: %d items, %d new", len(raw), n))
		res.Items += len(raw)
		res.New += n
	}
	if res.New == 0 {
		if _, err := r.DB.LatestIssue(ctx, pub.ID); err == nil {
			res.NoNewItems = true
			return res, nil
		}
		if res.Items == 0 {
			return nil, fmt.Errorf("no items collected — check the publication's sources")
		}
	}
	r.record(ctx, pub.ID, 0, "collect", "", start, llm.Response{})

	// --- dedupe ---
	r.stepStart("dedupe")
	start = time.Now()
	items, err := r.DB.ItemsForPublication(ctx, pub.ID)
	if err != nil {
		return nil, err
	}
	urls := make([]string, len(items))
	texts := make([]string, len(items))
	for i, it := range items {
		urls[i] = it.URL
		texts[i] = it.Title + "\n" + it.ContentText
	}
	clusters := dedupe.Assign(urls, texts)
	if err := r.DB.ClearStories(ctx, pub.ID); err != nil {
		return nil, err
	}
	groups := map[int][]store.Item{}
	for i, it := range items {
		groups[clusters[i]] = append(groups[clusters[i]], it)
	}
	ordered := make([][]store.Item, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a][0].ID < ordered[b][0].ID })
	for _, g := range ordered {
		title := g[0].Title
		for _, it := range g {
			if len(it.Title) > len(title) {
				title = it.Title
			}
		}
		storyID, err := r.DB.CreateStory(ctx, pub.ID, title, int64(dedupe.SimHash(g[0].Title+"\n"+g[0].ContentText)))
		if err != nil {
			return nil, err
		}
		for _, it := range g {
			if err := r.DB.AddStoryItem(ctx, storyID, it.ID); err != nil {
				return nil, err
			}
		}
	}
	stories, err := r.DB.StoriesWithItems(ctx, pub.ID)
	if err != nil {
		return nil, err
	}
	res.Stories = len(stories)
	r.record(ctx, pub.ID, 0, "dedupe", "", start, llm.Response{})

	// create the issue now so all model steps attach to it
	number, err := r.DB.NextIssueNumber(ctx, pub.ID)
	if err != nil {
		return nil, err
	}
	res.Number = number
	issueID, err := r.DB.CreateIssue(ctx, pub.ID, number,
		fmt.Sprintf("%s — issue %d", pub.Name, number), "")
	if err != nil {
		return nil, err
	}
	res.IssueID = issueID
	r.DB.AttachRuns(ctx, pub.ID, issueID)

	// --- triage ---
	r.stepStart("triage")
	start = time.Now()
	type triStory struct {
		Ref     int
		Title   string
		Outlets string
		Snippet string
	}
	ts := make([]triStory, len(stories))
	for i, s := range stories {
		snip := ""
		if len(s.Items) > 0 {
			snip = truncate(s.Items[0].ContentText, 400)
		}
		ts[i] = triStory{Ref: i + 1, Title: s.Title, Outlets: strings.Join(s.Outlets(), ", "), Snippet: snip}
	}
	var triage []struct {
		Ref    int     `json:"ref"`
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	resp, err := r.call(ctx, r.TriageModel, "triage", map[string]any{
		"Publication":   pub.Name,
		"Audience":      pub.Audience,
		"Language":      pub.Language,
		"EditorialLine": pub.EditorialLine,
		"Guide":         r.guide(ctx, pub.ID),
		"Stories":       ts,
	}, &triage)
	if err != nil {
		return nil, fmt.Errorf("triage: %w", err)
	}
	res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
	r.record(ctx, pub.ID, issueID, "triage", r.TriageModel, start, resp)
	scoreByRef := map[int]struct {
		score  float64
		reason string
	}{}
	for i, t := range triage {
		scoreByRef[t.Ref] = struct {
			score  float64
			reason string
		}{t.Score, t.Reason}
		if t.Ref-1 >= 0 && t.Ref-1 < len(stories) {
			r.DB.SetStoryTriage(ctx, stories[t.Ref-1].ID, t.Score, t.Reason)
		}
		_ = i
	}

	// --- storylist ---
	r.stepStart("storylist")
	start = time.Now()
	type slStory struct {
		Ref    int
		Score  float64
		Title  string
		Reason string
	}
	ss := make([]slStory, len(stories))
	for i, s := range stories {
		sr := scoreByRef[i+1]
		ss[i] = slStory{Ref: i + 1, Score: sr.score, Title: s.Title, Reason: sr.reason}
	}
	var picked struct {
		Items []struct {
			Refs    []int  `json:"refs"`
			Title   string `json:"title"`
			Theme   string `json:"theme"`
			Why     string `json:"why"`
			Section string `json:"section"`
		} `json:"items"`
	}
	resp, err = r.call(ctx, r.TriageModel, "storylist", map[string]any{
		"Publication":   pub.Name,
		"Audience":      pub.Audience,
		"Sections":      pub.Sections,
		"EditorialLine": pub.EditorialLine,
		"Guide":         r.guide(ctx, pub.ID),
		"Stories":       ss,
	}, &picked)
	if err != nil {
		return nil, fmt.Errorf("storylist: %w", err)
	}
	res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
	res.Selected = len(picked.Items)
	r.record(ctx, pub.ID, issueID, "storylist", r.TriageModel, start, resp)

	// --- create issue items, merging same-event stories ---

	entries := []entry{}
	for idx, p := range picked.Items {
		if len(p.Refs) == 0 {
			continue
		}
		primaryIdx := p.Refs[0] - 1
		if primaryIdx < 0 || primaryIdx >= len(stories) {
			return nil, fmt.Errorf("storylist: ref %d out of range", p.Refs[0])
		}
		primary := stories[primaryIdx]
		var merges []int64
		memberItems := append([]store.Item{}, primary.Items...)
		for _, ref := range p.Refs[1:] {
			if ref-1 < 0 || ref-1 >= len(stories) {
				continue
			}
			sec := stories[ref-1]
			merges = append(merges, sec.ID)
			memberItems = append(memberItems, sec.Items...)
		}
		if len(merges) > 0 {
			if err := r.DB.MergeStories(ctx, primary.ID, merges); err != nil {
				return nil, err
			}
		}
		sc := scoreByRef[p.Refs[0]]
		iiID, err := r.DB.CreateIssueItem(ctx, store.IssueItem{
			IssueID: issueID,
			StoryID: primary.ID,
			Section: p.Section,
			Score:   sc.score,
			Reason:  p.Why,
			Status:  "pending",
		})
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{
			iiID: iiID, storyID: primary.ID, ref: idx + 1,
			title: p.Title, section: p.Section, why: p.Why,
			score: sc.score, sources: memberItems,
		})
	}

	// --- dossier ---
	r.stepStart("dossier")
	start = time.Now()
	type dSrc struct {
		Ref  int
		URL  string
		Text string
	}
	type dItem struct {
		Ref     int
		Title   string
		Sources []dSrc
	}
	dItems := make([]dItem, len(entries))
	for i, e := range entries {
		for j, it := range e.sources {
			dItems[i].Sources = append(dItems[i].Sources, dSrc{Ref: j + 1, URL: it.URL, Text: truncate(it.ContentText, 1200)})
		}
		dItems[i].Ref = e.ref
		dItems[i].Title = e.title
	}
	var dossiers []Dossier
	resp, err = r.call(ctx, r.DraftModel, "dossier", map[string]any{
		"Publication": pub.Name,
		"Language":    pub.Language,
		"Items":       dItems,
	}, &dossiers)
	if err != nil {
		return nil, fmt.Errorf("dossier: %w", err)
	}
	res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
	r.record(ctx, pub.ID, issueID, "dossier", r.DraftModel, start, resp)
	dossierByRef := map[int]*Dossier{}
	for i := range dossiers {
		dossierByRef[dossiers[i].Ref] = &dossiers[i]
	}
	for i, e := range entries {
		d, ok := dossierByRef[e.ref]
		if !ok {
			return nil, fmt.Errorf("dossier: missing item %d", e.ref)
		}
		d.Title = e.title
		d.fillSources(e.sources)
		data, _ := json.Marshal(d)
		if err := r.DB.SaveDossier(ctx, e.iiID, string(data)); err != nil {
			return nil, err
		}
		entries[i].sources = e.sources // keep for later steps
	}

	// --- deepdive (top N by score) ---
	r.stepStart("deepdive")
	start = time.Now()
	topN := r.MaxDeepDives
	if topN <= 0 || topN > 3 {
		topN = 3
	}
	sorted := append([]entry{}, entries...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].score > sorted[b].score })
	if len(sorted) > topN {
		sorted = sorted[:topN]
	}
	type ddItem struct {
		Ref     int
		Title   string
		Dossier string
		Sources []dSrc
	}
	var ddItems []ddItem
	for _, e := range sorted {
		d := *dossierByRef[e.ref]
		d.Title = ""
		dj, _ := json.Marshal(d)
		di := ddItem{Ref: e.ref, Title: e.title, Dossier: string(dj)}
		for j, it := range e.sources {
			di.Sources = append(di.Sources, dSrc{Ref: j + 1, URL: it.URL, Text: truncate(it.ContentText, 1200)})
		}
		ddItems = append(ddItems, di)
	}
	var deep []struct {
		Ref       int `json:"ref"`
		DeepFacts []struct {
			Text string `json:"text"`
			Ref  int    `json:"ref"`
		} `json:"deep_facts"`
		ExtraUnknowns []string `json:"extra_unknowns"`
	}
	resp, err = r.call(ctx, r.DraftModel, "deepdive", map[string]any{
		"Publication": pub.Name,
		"Language":    pub.Language,
		"Items":       ddItems,
	}, &deep)
	if err != nil {
		return nil, fmt.Errorf("deepdive: %w", err)
	}
	res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
	r.record(ctx, pub.ID, issueID, "deepdive", r.DraftModel, start, resp)
	for _, dd := range deep {
		d, ok := dossierByRef[dd.Ref]
		if !ok {
			continue
		}
		for _, f := range dd.DeepFacts {
			d.DeepFacts = append(d.DeepFacts, DossierFact{Text: f.Text, Ref: f.Ref})
		}
		d.Unknowns = append(d.Unknowns, dd.ExtraUnknowns...)
	}
	for _, e := range entries {
		data, _ := json.Marshal(dossierByRef[e.ref])
		if err := r.DB.SaveDossier(ctx, e.iiID, string(data)); err != nil {
			return nil, err
		}
	}
	if err := r.DB.SetIssueStatus(ctx, res.IssueID, "reviewing"); err != nil {
		return nil, err
	}
	return res, nil
}

// loadEntries rebuilds the entry list for an existing issue from the DB:
// issue items, their dossiers and story titles.
//
// canonical=true returns every Phase A item regardless of review status —
// the stable input that keeps demo-mode draft/citecheck replay keys
// fixed. canonical=false returns only approved items (plus pending? no —
// the draft contains approved items only), which is what actually ships.
func (r *Runner) loadEntries(ctx context.Context, issueID int64, canonical bool) ([]entry, map[int]*Dossier, error) {
	iis, err := r.DB.IssueItems(ctx, issueID)
	if err != nil {
		return nil, nil, err
	}
	// issue items were inserted in picked order; ref numbers were assigned
	// positionally then, but dossiers store their own ref, so reload in id order
	sort.Slice(iis, func(a, b int) bool { return iis[a].ID < iis[b].ID })
	var entries []entry
	dossierByRef := map[int]*Dossier{}
	for _, ii := range iis {
		if canonical {
			// Alternative replacements are never part of the recorded
			// canonical input.
			if ii.Origin == "alternative" {
				continue
			}
		} else if ii.Status != "approved" {
			continue
		}
		djson, err := r.DB.Dossier(ctx, ii.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("issue_item %d: no dossier", ii.ID)
		}
		var d Dossier
		if err := json.Unmarshal([]byte(djson), &d); err != nil {
			return nil, nil, fmt.Errorf("issue_item %d: %w", ii.ID, err)
		}
		title := d.Title
		if title == "" {
			title, _ = r.DB.StoryTitle(ctx, ii.StoryID)
		}
		entries = append(entries, entry{
			iiID: ii.ID, storyID: ii.StoryID, ref: d.Ref,
			title: title, section: ii.Section, why: ii.Reason, score: ii.Score,
		})
		dd := d
		dossierByRef[d.Ref] = &dd
	}
	return entries, dossierByRef, nil
}

// RunPhaseB runs draft → assemble → citecheck on an issue produced by
// RunPhaseA (and reviewed): the stored body carries approved items only.
func (r *Runner) RunPhaseB(ctx context.Context, pub *store.Publication, issueID int64) (*Result, error) {
	res := &Result{IssueID: issueID}
	entries, dossierByRef, err := r.loadEntries(ctx, issueID, false)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no approved items — approve at least one story before drafting")
	}
	res.Selected = len(entries)
	modelEntries, modelDossiers := entries, dossierByRef
	if r.DemoMode {
		modelEntries, modelDossiers, err = r.loadEntries(ctx, issueID, true)
		if err != nil {
			return nil, err
		}
	}

	// --- draft ---
	r.stepStart("draft")
	start := time.Now()
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
	drItems := make([]drItem, len(modelEntries))
	for i, e := range modelEntries {
		d, ok := modelDossiers[e.ref]
		if !ok {
			return nil, fmt.Errorf("draft: no dossier for item ref %d", e.ref)
		}
		drItems[i] = drItem{
			Ref: e.ref, Section: e.section, Score: e.score, Title: e.title,
			Summary: d.Summary, Facts: d.Facts, DeepFacts: d.DeepFacts,
			Interpretation: d.Interpretation,
		}
	}
	var drafted struct {
		Entries []struct {
			Ref      int    `json:"ref"`
			Headline string `json:"headline"`
			BodyMD   string `json:"body_md"`
		} `json:"entries"`
	}
	resp, err := r.call(ctx, r.DraftModel, "draft", map[string]any{
		"Publication": pub.Name,
		"Audience":    pub.Audience,
		"Language":    pub.Language,
		"Tone":        pub.Tone,
		"Items":       drItems,
	}, &drafted)
	if err != nil {
		return nil, fmt.Errorf("draft: %w", err)
	}
	res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
	r.record(ctx, pub.ID, issueID, "draft", r.DraftModel, start, resp)
	draftByRef := map[int]struct {
		headline, body string
	}{}
	for _, de := range drafted.Entries {
		draftByRef[de.Ref] = struct {
			headline, body string
		}{de.Headline, de.BodyMD}
	}
	// Alternative items are never part of the canonical draft input, so
	// they have no model-drafted body: synthesize one from the dossier
	// (summary carries [n] citations, so it passes the citation rules).
	for _, e := range entries {
		if _, ok := draftByRef[e.ref]; !ok {
			d := dossierByRef[e.ref]
			body := strings.TrimSpace(d.Summary)
			for _, f := range d.Facts {
				if s := strings.TrimSpace(f.Text); s != "" && !strings.Contains(body, s) {
					body += " " + ensureRefSuffix(s, f.Ref)
				}
			}
			draftByRef[e.ref] = struct {
				headline, body string
			}{e.title, body}
		}
	}

	// --- assemble issue markdown ---
	number := res.Number
	if number == 0 {
		if iss, err := r.DB.GetIssue(ctx, issueID); err == nil {
			number = iss.Number
		}
	}
	sectionOrder := []string{}
	for _, s := range pub.Sections {
		sectionOrder = append(sectionOrder, s.Title)
	}
	body := assembleIssue(pub, number, entries, draftByRef, dossierByRef, sectionOrder)
	checkBody := body
	checkEntries := entries
	checkDossiers := dossierByRef
	if r.DemoMode {
		checkBody = assembleIssue(pub, number, modelEntries, draftByRef, modelDossiers, sectionOrder)
		checkEntries = modelEntries
		checkDossiers = modelDossiers
	}

	// --- citecheck ---
	r.stepStart("citecheck")
	start = time.Now()
	sents := extractSentences(checkBody)
	type cSent struct {
		ID   int
		Item int
		Refs string
		Text string
	}
	type cItem struct {
		Ref     int
		Sources []struct {
			Ref int
			URL string
		}
	}
	cs := []cSent{}
	ci := []cItem{}
	for _, e := range checkEntries {
		d, ok := checkDossiers[e.ref]
		if !ok {
			return nil, fmt.Errorf("citecheck: no dossier for item ref %d", e.ref)
		}
		c := cItem{Ref: e.ref}
		for _, s := range d.Sources {
			c.Sources = append(c.Sources, struct {
				Ref int
				URL string
			}{s.Ref, s.URL})
		}
		ci = append(ci, c)
	}
	for _, s := range sents {
		cs = append(cs, cSent{ID: s.id, Item: s.itemRef, Refs: s.refs, Text: s.text})
	}
	var checks []struct {
		ID      int    `json:"id"`
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
	}
	resp, err = r.call(ctx, r.TriageModel, "citecheck", map[string]any{
		"Publication": pub.Name,
		"Sentences":   cs,
		"Items":       ci,
	}, &checks)
	if err != nil {
		return nil, fmt.Errorf("citecheck: %w", err)
	}
	res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
	r.record(ctx, pub.ID, issueID, "citecheck", r.TriageModel, start, resp)

	sentByID := map[int]sentRec{}
	for _, s := range sents {
		sentByID[s.id] = s
	}
	seen := map[string]bool{}
	addFlag := func(item int64, sentence, note string) {
		k := fmt.Sprintf("%d|%s", item, sentence)
		if seen[k] {
			return
		}
		seen[k] = true
		res.Flagged = append(res.Flagged, Flagged{Item: item, Sentence: sentence, Note: note})
	}
	activeRef := map[int]bool{}
	for _, e := range entries {
		activeRef[e.ref] = true
	}
	for _, c := range checks {
		if c.Verdict != "unsupported" {
			continue
		}
		s, ok := sentByID[c.ID]
		if !ok || !activeRef[s.itemRef] {
			continue
		}
		addFlag(int64(s.itemRef), s.text, c.Note)
	}
	// deterministic rules: a body sentence with no citation, or an entry
	// with no citation at all, is flagged without needing the model.
	for _, s := range extractSentences(body) {
		if s.refs == "" {
			addFlag(int64(s.itemRef), s.text, "sentence has no citation")
		}
	}
	for _, e := range entries {
		de, ok := draftByRef[e.ref]
		if ok && !refRe.MatchString(de.body) {
			addFlag(int64(e.ref), truncate(de.body, 120), "entry has no citations")
		}
	}
	flagsJSON, _ := json.Marshal(res.Flagged)
	if res.Flagged == nil {
		flagsJSON = []byte("[]")
	}
	if err := r.DB.UpdateIssue(ctx, issueID, "drafted", body, string(flagsJSON)); err != nil {
		return nil, err
	}
	res.Number = number
	return res, nil
}

// finish writes the stored issue body to outDir and returns the result.
func (r *Runner) finish(ctx context.Context, res *Result, outDir string) (*Result, error) {
	iss, err := r.DB.GetIssue(ctx, res.IssueID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	res.Path = filepath.Join(outDir, fmt.Sprintf("issue-%d.md", iss.Number))
	if err := os.WriteFile(res.Path, []byte(iss.BodyMD), 0o644); err != nil {
		return nil, err
	}
	return res, nil
}

// Dossier is the per-item research record stored as JSON.
type Dossier struct {
	Ref            int             `json:"ref"`
	Title          string          `json:"title,omitempty"` // storylist-picked title, set at save time
	Summary        string          `json:"summary"`
	Facts          []DossierFact   `json:"facts"`
	DeepFacts      []DossierFact   `json:"deep_facts,omitempty"`
	Quote          DossierQuote    `json:"quote"`
	Unknowns       []string        `json:"unknowns"`
	Interpretation []string        `json:"interpretation"`
	Sources        []DossierSource `json:"sources"`
}

// DossierFact is one sourced fact.
type DossierFact struct {
	Text string `json:"text"`
	Ref  int    `json:"ref"`
}

// DossierQuote is a short attributed quote.
type DossierQuote struct {
	Text        string `json:"text"`
	Attribution string `json:"attribution"`
}

// DossierSource maps a [n] ref to a source.
type DossierSource struct {
	Ref    int    `json:"ref"`
	URL    string `json:"url"` // canonical (fictional) article URL
	ItemID int64  `json:"item_id,omitempty"`
	Name   string `json:"name,omitempty"` // outlet name
}

// fillSources rebuilds the source list from member items so [n] refs in
// facts and the stored list stay consistent. URLs are canonicalized
// (tracking params stripped) and the outlet name comes from the item's
// source.
func (d *Dossier) fillSources(items []store.Item) {
	d.Sources = d.Sources[:0]
	for i, it := range items {
		url := it.CanonicalURL
		if url == "" {
			url = dedupe.Canonical(it.URL)
		}
		name := it.SourceName
		if name == "" {
			name = outletLabel(it.URL)
		}
		d.Sources = append(d.Sources, DossierSource{Ref: i + 1, URL: url, ItemID: it.ID, Name: name})
	}
}

// outletLabel derives a display name from a URL host when the source has
// no recorded outlet name.
func outletLabel(rawURL string) string {
	u := rawURL
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.TrimPrefix(u, "www.")
	if u == "" {
		return "source"
	}
	return u
}

// ensureRefSuffix appends "[n]" when a synthesized fact lacks a citation.
func ensureRefSuffix(s string, ref int) string {
	if refRe.MatchString(s) {
		return s
	}
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "!") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return fmt.Sprintf("%s [%d]", s, ref)
}

type sentRec struct {
	id      int
	itemRef int
	refs    string
	text    string
}

var sentEndRe = regexp.MustCompile(`[.!?]\s+`)
var refRe = regexp.MustCompile(`\[\d+\]`)

// stopAbbrev lists tokens (lowercase, period stripped) that should not end
// a sentence even when followed by a capital letter.
var stopAbbrev = map[string]bool{
	"dr": true, "mr": true, "mrs": true, "ms": true, "prof": true,
	"sr": true, "sra": true, "etc": true, "vs": true, "no": true,
	"e.g": true, "i.e": true, "p.ex": true, "fig": true,
	"inc": true, "ltd": true, "dept": true, "approx": true,
}

// sentenceStart reports whether r can begin a new sentence: an uppercase
// letter, a digit, or an opening quote/bracket.
func sentenceStart(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsDigit(r) ||
		strings.ContainsRune("([\"'“”‘’«»", r)
}

// lastToken returns the trailing whitespace-delimited word of s,
// lowercased and stripped of edge punctuation.
func lastToken(s string) string {
	if i := strings.LastIndexAny(s, " \t"); i >= 0 {
		s = s[i+1:]
	}
	return strings.Trim(strings.ToLower(s), `.,;:!?"'()[]`)
}

// splitSentences splits a paragraph into sentences. A boundary is only
// accepted at terminal punctuation followed by whitespace when the next
// rune can start a sentence and the previous token is not a common
// abbreviation (avoids splitting on "Dr.", "e.g.", decimals, etc.).
func splitSentences(line string) []string {
	var out []string
	start := 0
	for _, m := range sentEndRe.FindAllStringIndex(line, -1) {
		next, _ := utf8.DecodeRuneInString(line[m[1]:])
		if !sentenceStart(next) || stopAbbrev[lastToken(line[:m[0]])] {
			continue
		}
		out = append(out, line[start:m[0]])
		start = m[1]
	}
	return append(out, line[start:])
}

// extractSentences splits draft body paragraphs into sentences, recording
// which item and which [n] refs each carries. Sentences without refs are
// included — the deterministic rule flags them as uncited.
func extractSentences(body string) []sentRec {
	var out []sentRec
	id := 0
	curItem := 0
	itemRe := regexp.MustCompile(`^### .*<!-- item (\d+) -->`)
	for _, line := range strings.Split(body, "\n") {
		if m := itemRe.FindStringSubmatch(line); m != nil {
			var n int
			fmt.Sscanf(m[1], "%d", &n)
			curItem = n
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ">") ||
			strings.HasPrefix(trimmed, "**") || strings.HasPrefix(trimmed, "-") ||
			strings.HasPrefix(trimmed, "_") {
			continue
		}
		for _, s := range splitSentences(trimmed) {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			id++
			out = append(out, sentRec{
				id:      id,
				itemRef: curItem,
				refs:    strings.Join(refRe.FindAllString(s, -1), ""),
				text:    s,
			})
		}
	}
	return out
}

// assembleIssue builds the stored issue markdown. The stored body is the
// canonical reader draft: headlines, cited bodies and linked sources,
// with <!-- item N --> markers kept so the citation check can attribute
// flagged sentences. Scores, triage reasons, unknowns, placeholders and
// the disclosure line never enter the stored body — delivery.ReaderBody
// renders the reader-facing version (commentary, disclosure, absolute
// links) from it.
func assembleIssue(pub *store.Publication, number int, entries []entry, drafts map[int]struct {
	headline, body string
}, dossiers map[int]*Dossier, sectionOrder []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — issue %d\n\n", pub.Name, number)
	bySection := map[string][]entry{}
	for _, e := range entries {
		bySection[e.section] = append(bySection[e.section], e)
	}
	writeItem := func(e entry) {
		de := drafts[e.ref]
		d := dossiers[e.ref]
		headline := de.headline
		if headline == "" {
			headline = e.title
		}
		fmt.Fprintf(&b, "### %s <!-- item %d -->\n\n", headline, e.ref)
		b.WriteString(de.body + "\n\n")
		if len(d.Sources) > 0 {
			b.WriteString("**Sources:** ")
			parts := []string{}
			for _, s := range d.Sources {
				name := s.Name
				if name == "" {
					name = "source"
				}
				parts = append(parts, fmt.Sprintf("[%d] [%s](/a/%d)", s.Ref, name, s.ItemID))
			}
			b.WriteString(strings.Join(parts, " · ") + "\n\n")
		}
	}
	for _, sec := range sectionOrder {
		list, ok := bySection[sec]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", sec)
		for _, e := range list {
			writeItem(e)
		}
	}
	for sec, list := range bySection {
		found := false
		for _, s := range sectionOrder {
			if s == sec {
				found = true
			}
		}
		if !found {
			fmt.Fprintf(&b, "## %s\n\n", sec)
			for _, e := range list {
				writeItem(e)
			}
		}
	}
	return b.String()
}

// SubstituteAlternative replaces an issue item with the best-scored story
// not already in the issue (triage ranking), generating a dossier for it.
// The new item starts as pending; the old one is marked 'alternative'.
func (r *Runner) SubstituteAlternative(ctx context.Context, pub *store.Publication, iss *store.Issue, old *store.IssueItem) (*store.IssueItem, error) {
	cand, err := r.DB.BestAlternative(ctx, pub.ID, iss.ID)
	if err != nil {
		return nil, err // sql.ErrNoRows → no candidate
	}
	iis, err := r.DB.IssueItems(ctx, iss.ID)
	if err != nil {
		return nil, err
	}
	maxRef := 0
	for _, ii := range iis {
		if dj, err := r.DB.Dossier(ctx, ii.ID); err == nil {
			var d Dossier
			if json.Unmarshal([]byte(dj), &d) == nil && d.Ref > maxRef {
				maxRef = d.Ref
			}
		}
	}
	newRef := maxRef + 1
	newID, err := r.DB.CreateIssueItem(ctx, store.IssueItem{
		IssueID: iss.ID,
		StoryID: cand.ID,
		Section: old.Section,
		Score:   cand.TriageScore,
		Reason:  cand.TriageReason,
		Status:  "pending",
		Origin:  "alternative",
	})
	if err != nil {
		return nil, err
	}
	d := r.alternativeDossier(ctx, pub, cand, newRef)
	data, _ := json.Marshal(d)
	if err := r.DB.SaveDossier(ctx, newID, string(data)); err != nil {
		return nil, err
	}
	if err := r.DB.SetIssueItemStatus(ctx, old.ID, "alternative"); err != nil {
		return nil, err
	}
	return r.DB.GetIssueItem(ctx, newID)
}

// alternativeDossier builds the dossier for an alternative story. Real
// mode asks the model; demo mode falls back to a deterministic dossier
// synthesized from the story's items when no replay fixture exists.
func (r *Runner) alternativeDossier(ctx context.Context, pub *store.Publication, cand *store.Story, ref int) *Dossier {
	type dSrc struct {
		Ref  int
		URL  string
		Text string
	}
	var srcs []dSrc
	for j, it := range cand.Items {
		srcs = append(srcs, dSrc{Ref: j + 1, URL: it.URL, Text: truncate(it.ContentText, 1200)})
	}
	var dossiers []Dossier
	resp, err := r.call(ctx, r.DraftModel, "dossier", map[string]any{
		"Publication": pub.Name,
		"Language":    pub.Language,
		"Items":       []map[string]any{{"Ref": ref, "Title": cand.Title, "Sources": srcs}},
	}, &dossiers)
	if err == nil {
		for i := range dossiers {
			if dossiers[i].Ref == ref {
				d := &dossiers[i]
				d.Title = cand.Title
				d.fillSources(cand.Items)
				return d
			}
		}
	}
	_ = resp
	// Synthetic dossier: every claim comes from a member item, so citations
	// stay valid without a model call.
	var facts []DossierFact
	var summary string
	for i, it := range cand.Items {
		sents := splitSentences(it.ContentText)
		if len(sents) > 0 {
			summary += strings.TrimSpace(sents[0]) + fmt.Sprintf(" [%d] ", i+1)
		}
		for _, s := range sents[1:] {
			facts = append(facts, DossierFact{Text: strings.TrimSpace(s), Ref: i + 1})
			if len(facts) >= 4 {
				break
			}
		}
	}
	d := &Dossier{
		Ref: ref, Title: cand.Title,
		Summary:        strings.TrimSpace(summary),
		Facts:          facts,
		Interpretation: []string{"Picked by the editor as an alternative to the previous item."},
	}
	d.fillSources(cand.Items)
	return d
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// RunHeadless is the CLI entry: build a runner for the given mode and run it.
// demo=true makes PhaseB use canonical replay input (see Runner.DemoMode).
func RunHeadless(ctx context.Context, db *store.DB, pub *store.Publication, outDir string, fetcher sources.Fetcher, provider llm.Provider, triageModel, draftModel string, maxDeep int, demo bool) (*Result, error) {
	r := &Runner{
		DB:           db,
		LLM:          provider,
		Fetcher:      fetcher,
		TriageModel:  triageModel,
		DraftModel:   draftModel,
		MaxDeepDives: maxDeep,
		DemoMode:     demo,
	}
	return r.Run(ctx, pub, outDir)
}
