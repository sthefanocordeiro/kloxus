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

// Runner executes one pipeline run for a publication.
type Runner struct {
	DB           *store.DB
	LLM          llm.Provider
	Fetcher      sources.Fetcher
	TriageModel  string
	DraftModel   string
	MaxDeepDives int
}

// Result summarizes a completed run.
type Result struct {
	IssueID  int64
	Number   int
	Path     string
	Items    int
	Stories  int
	Selected int
	Flagged  []Flagged
	CostUSD  float64
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
func (r *Runner) Run(ctx context.Context, pub *store.Publication, outDir string) (*Result, error) {
	res := &Result{}

	// --- collect ---
	start := time.Now()
	srcs, err := r.DB.Sources(ctx, pub.ID)
	if err != nil {
		return nil, err
	}
	for _, s := range srcs {
		raw, err := r.Fetcher.Fetch(ctx, s.Type, s.URL)
		if err != nil {
			r.DB.MarkSourceFetched(ctx, s.ID, "error: "+truncate(err.Error(), 120))
			return nil, fmt.Errorf("source %s: %w", s.URL, err)
		}
		n, err := r.DB.SaveItems(ctx, s.ID, raw, dedupe.Canonical)
		if err != nil {
			return nil, err
		}
		r.DB.MarkSourceFetched(ctx, s.ID, fmt.Sprintf("ok: %d items, %d new", len(raw), n))
		res.Items += len(raw)
	}
	r.record(ctx, pub.ID, 0, "collect", "", start, llm.Response{})

	// --- dedupe ---
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

	// --- triage ---
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
	for _, t := range triage {
		scoreByRef[t.Ref] = struct {
			score  float64
			reason string
		}{t.Score, t.Reason}
	}

	// --- storylist ---
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
		d.fillSources(e.sources)
		data, _ := json.Marshal(d)
		if err := r.DB.SaveDossier(ctx, e.iiID, string(data)); err != nil {
			return nil, err
		}
		entries[i].sources = e.sources // keep for later steps
	}

	// --- deepdive (top N by score) ---
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
		d := dossierByRef[e.ref]
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

	// --- draft ---
	start = time.Now()
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
	drItems := make([]drItem, len(entries))
	for i, e := range entries {
		d := dossierByRef[e.ref]
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
	resp, err = r.call(ctx, r.DraftModel, "draft", map[string]any{
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

	// --- assemble issue markdown ---
	sectionOrder := []string{}
	for _, s := range pub.Sections {
		sectionOrder = append(sectionOrder, s.Title)
	}
	body := assembleIssue(pub, number, entries, draftByRef, dossierByRef, sectionOrder)

	// --- citecheck ---
	start = time.Now()
	sents := extractSentences(body)
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
	for _, e := range entries {
		d := dossierByRef[e.ref]
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
	for _, c := range checks {
		if c.Verdict == "unsupported" {
			s := sentByID[c.ID]
			res.Flagged = append(res.Flagged, Flagged{
				Item:     int64(s.itemRef),
				Sentence: s.text,
				Note:     c.Note,
			})
		}
	}
	// deterministic rules: a body sentence with no citation, or an entry
	// with no citation at all, is flagged without needing the model.
	for _, s := range sents {
		if s.refs == "" {
			res.Flagged = append(res.Flagged, Flagged{
				Item:     int64(s.itemRef),
				Sentence: s.text,
				Note:     "sentence has no citation",
			})
		}
	}
	for _, e := range entries {
		de, ok := draftByRef[e.ref]
		if ok && !refRe.MatchString(de.body) {
			res.Flagged = append(res.Flagged, Flagged{
				Item:     int64(e.ref),
				Sentence: truncate(de.body, 120),
				Note:     "entry has no citations",
			})
		}
	}
	flagsJSON, _ := json.Marshal(res.Flagged)
	if res.Flagged == nil {
		flagsJSON = []byte("[]")
	}
	status := "reviewing"
	if err := r.DB.UpdateIssue(ctx, issueID, status, body, string(flagsJSON)); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	res.Path = filepath.Join(outDir, fmt.Sprintf("issue-%d.md", number))
	if err := os.WriteFile(res.Path, []byte(body), 0o644); err != nil {
		return nil, err
	}
	return res, nil
}

// Dossier is the per-item research record stored as JSON.
type Dossier struct {
	Ref            int             `json:"ref"`
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

// DossierSource maps a [n] ref to a URL.
type DossierSource struct {
	Ref int    `json:"ref"`
	URL string `json:"url"`
}

// fillSources rebuilds the source list from member items so [n] refs in
// facts and the stored list stay consistent.
func (d *Dossier) fillSources(items []store.Item) {
	d.Sources = d.Sources[:0]
	for i, it := range items {
		d.Sources = append(d.Sources, DossierSource{Ref: i + 1, URL: it.URL})
	}
}

type sentRec struct {
	id      int
	itemRef int
	refs    string
	text    string
}

var sentEndRe = regexp.MustCompile(`[.!?]\s+`)
var refRe = regexp.MustCompile(`\[\d+\]`)

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
		for _, s := range sentEndRe.Split(trimmed, -1) {
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

func assembleIssue(pub *store.Publication, number int, entries []entry, drafts map[int]struct {
	headline, body string
}, dossiers map[int]*Dossier, sectionOrder []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — issue %d\n\n", pub.Name, number)
	if pub.AIDisclosure == "short" {
		b.WriteString("_Prepared with AI assistance and reviewed by the editor._\n\n")
	} else if pub.AIDisclosure == "detailed" {
		b.WriteString("_This issue was prepared by an AI editorial assistant from the sources listed below; every fact links to its source. All commentary and final review are the editor's._\n\n")
	}
	commentaryOf := map[string]string{}
	for _, s := range pub.Sections {
		commentaryOf[s.Title] = s.Commentary
	}
	bySection := map[string][]entry{}
	seen := map[string]bool{}
	for _, e := range entries {
		bySection[e.section] = append(bySection[e.section], e)
		seen[e.section] = true
	}
	writeItem := func(e entry) {
		de := drafts[e.ref]
		d := dossiers[e.ref]
		headline := de.headline
		if headline == "" {
			headline = e.title
		}
		fmt.Fprintf(&b, "### %s <!-- item %d -->\n\n", headline, e.ref)
		fmt.Fprintf(&b, "_%.1f · %s_\n\n", e.score, e.why)
		b.WriteString(de.body + "\n\n")
		if len(d.Unknowns) > 0 {
			b.WriteString("**Still unknown:** " + strings.Join(d.Unknowns, "; ") + "\n\n")
		}
		if len(d.Sources) > 0 {
			b.WriteString("**Sources:** ")
			parts := []string{}
			for _, s := range d.Sources {
				parts = append(parts, fmt.Sprintf("[%d] %s", s.Ref, s.URL))
			}
			b.WriteString(strings.Join(parts, " · ") + "\n\n")
		}
		switch commentaryOf[e.section] {
		case "required":
			b.WriteString("> Your commentary goes here (required — sending is blocked while this slot is empty)\n\n")
		case "optional":
			b.WriteString("> Optional commentary slot\n\n")
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// RunHeadless is the CLI entry: build a runner for the given mode and run it.
func RunHeadless(ctx context.Context, db *store.DB, pub *store.Publication, outDir string, fetcher sources.Fetcher, provider llm.Provider, triageModel, draftModel string, maxDeep int) (*Result, error) {
	r := &Runner{
		DB:           db,
		LLM:          provider,
		Fetcher:      fetcher,
		TriageModel:  triageModel,
		DraftModel:   draftModel,
		MaxDeepDives: maxDeep,
	}
	return r.Run(ctx, pub, outDir)
}
