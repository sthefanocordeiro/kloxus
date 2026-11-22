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
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/rerank"
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
	// Deterministic runs the no-model pipeline (KLOXUS_LLM_MODE=off):
	// triage, storylist, dossier, deepdive, draft and citecheck use the
	// deterministic implementations in rank.go and no model call is
	// ever made — call() refuses outright.
	Deterministic bool
	// OnStep, if set, is called as each step starts (progress reporting).
	OnStep func(step string)
	// OnEvent, if set, is called as each step finishes with the step's
	// real counts (the /api/v1 run-events stream).
	OnEvent func(StepEvent)
	// OnIssue, if set, is called the moment Phase A creates the issue
	// row — lets run tracking expose issue_id before the run finishes.
	OnIssue func(issueID int64)
	// Research, when set, lets ComposeSkeleton prefill each pauta from
	// the shared cached research (KLOX-67 §7.2): a story with research
	// arrives with an AI-written, author-editable draft body; the
	// degraded path keeps the manual slot. Nil means the deterministic
	// skeleton exactly as before.
	Research *Researcher
	// Embedder embeds the publication profile for stage-1 ranking
	// (KLOX-109); nil uses DefaultEmbedder, and no embedder at all ranks
	// by the FTS fallback.
	Embedder QueryEmbedder
	// Reranker scores the stage-1 shortlist once per run (KLOX-110); nil
	// uses DefaultReranker, and none keeps the stage-1 order.
	Reranker rerank.Reranker
}

// StepEvent reports a finished pipeline step with its real counts — never
// percentages.
type StepEvent struct {
	Step   string         `json:"step"`
	Counts map[string]int `json:"counts,omitempty"`
}

// emit reports a step's completion to OnEvent (when set).
func (r *Runner) emit(step string, counts map[string]int) {
	if r.OnEvent != nil {
		r.OnEvent(StepEvent{Step: step, Counts: counts})
	}
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
	iiID     int64
	storyID  int64
	ref      int
	title    string
	section  string
	why      string
	score    float64
	position int
	sources  []store.Item
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

// complete invokes the provider behind a panic guard: a provider that
// panics fails this step (and the run) instead of taking the process
// down. Every model call in a pipeline step goes through here (D32),
// and every one passes the prompt-size cap first — an over-cap request
// is refused before the provider sees it (KLOX-44).
func (r *Runner) complete(ctx context.Context, req llm.Request) (resp llm.Response, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("template %s: model call panicked: %v", req.Template, p)
		}
	}()
	if err := llm.CheckSize(req); err != nil {
		return llm.Response{}, err
	}
	return r.LLM.Complete(ctx, req)
}

// stepMaxTokens sets the output ceiling each prompt template needs —
// every model call carries max_tokens (KLOX-44). Long-form steps keep
// the 32k ceiling real-feed runs proved they need: an 8k cap truncated
// a dossier on ~20 real stories mid-JSON, and truncation lands as
// invalid JSON the repair retry cannot fix.
var stepMaxTokens = map[string]int{
	"triage":    8192,
	"storylist": 8192,
	"dossier":   32768,
	"deepdive":  32768,
	"draft":     32768,
	"citecheck": 8192,
}

// call renders a prompt, calls the model and decodes JSON output,
// retrying once on invalid JSON.
func (r *Runner) call(ctx context.Context, model, name string, data any, out any) (llm.Response, error) {
	if r.Deterministic {
		// Defense in depth: deterministic mode must never reach a model
		// call — fail loudly even if a code path misses its branch.
		return llm.Response{}, fmt.Errorf("template %s: %w", name, llm.ErrDisabled)
	}
	user, err := RenderPrompt(name, data)
	if err != nil {
		return llm.Response{}, err
	}
	maxTok := stepMaxTokens[name]
	if maxTok == 0 {
		maxTok = 8192
	}
	req := llm.Request{Model: model, Template: name, User: user, MaxTokens: maxTok}
	resp, err := r.complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if err := decodeJSON(resp.Text, out); err != nil {
		if r.DemoMode {
			// A retry would change the request and miss the fixture.
			return resp, fmt.Errorf("template %s: %w", name, err)
		}
		req.User = user + "\n\nYour previous reply was not valid JSON (" + err.Error() + "). Reply with ONLY the JSON."
		resp2, err2 := r.complete(ctx, req)
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
	if r.DemoMode {
		// Demo replays finish in milliseconds; a short pause per step
		// makes the live progress display on the run page visible.
		time.Sleep(500 * time.Millisecond)
	}
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
	hasCatalog := false
	for _, s := range srcs {
		if s.CatalogID != 0 {
			// Catalog sources are ingested globally by the scheduler —
			// a run never fetches them itself (D29: one request per
			// interval no matter how many workspaces run).
			hasCatalog = true
			r.DB.MarkSourceFetched(ctx, s.ID, s.Name, "ok: shared catalog")
			continue
		}
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
	if hasCatalog {
		// What's "new" for a shared source is what the scheduler stored
		// after this publication's last issue, not what this run fetched.
		total, fresh, err := r.DB.CatalogItemsStats(ctx, pub.ID)
		if err != nil {
			return nil, err
		}
		res.Items += total
		res.New += fresh
	}
	if res.New == 0 {
		poolAvailable := false
		if _, err := r.DB.LatestIssue(ctx, pub.ID); err == nil {
			// "Nothing new" means nothing to DRAFT, not nothing fetched:
			// a fetch that turns up zero new items must not block a next
			// edition while the bench still holds unused suggestions
			// (KLOX-100). Only when the unused pool is empty is the run
			// genuinely idle. A count error plays it safe and proceeds —
			// an empty board is recoverable, a false "nothing new" is not.
			unused, uerr := r.DB.UnusedCandidateCount(ctx, pub.ID)
			if uerr == nil && unused == 0 {
				r.emit("collect", map[string]int{"items_read": res.Items, "new_items": res.New})
				res.NoNewItems = true
				return res, nil
			}
			poolAvailable = true
		}
		if res.Items == 0 && !poolAvailable {
			return nil, fmt.Errorf("no items collected — check the publication's sources")
		}
	}
	r.emit("collect", map[string]int{"items_read": res.Items, "new_items": res.New})
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
	published := make([]time.Time, len(items))
	for i, it := range items {
		urls[i] = it.URL
		// SimHash runs over body text only (KLOX-46): a republish's new
		// headline must not push the pair past the 4-bit threshold.
		texts[i] = it.ContentText
		// The 72 h merge window uses the published date first and the
		// fetched time when it is unparseable — the same fallback the
		// candidate query applies (safe_ts).
		if t, ok := parsePublished(it.PublishedAt); ok {
			published[i] = t
		} else if t, ok := parsePublished(it.FetchedAt); ok {
			published[i] = t
		}
	}
	clusters := dedupe.Assign(urls, texts, published)
	if err := r.DB.ClearStories(ctx, pub.ID); err != nil {
		return nil, err
	}
	groups := map[int][]store.Item{}
	for i, it := range items {
		groups[clusters[i]] = append(groups[clusters[i]], it)
	}
	groups = mergeCitedGroups(groups)
	ordered := make([][]store.Item, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a][0].ID < ordered[b][0].ID })
	drafts := make([]store.StoryDraft, len(ordered))
	for i, g := range ordered {
		itemIDs := make([]int64, len(g))
		for j, it := range g {
			itemIDs[j] = it.ID
		}
		drafts[i] = store.StoryDraft{
			Title:     earliestTitle(g),
			Simhash:   int64(dedupe.SimHash(g[0].Title + "\n" + g[0].ContentText)),
			DedupeKey: dedupeKey(g),
			ItemIDs:   itemIDs,
		}
	}
	if err := r.DB.CreateStories(ctx, pub.ID, drafts); err != nil {
		return nil, err
	}
	stories, err := r.DB.StoriesWithItems(ctx, pub.ID)
	if err != nil {
		return nil, err
	}
	res.Stories = len(stories)
	r.emit("dedupe", map[string]int{"stories": res.Stories, "duplicates_merged": len(items) - res.Stories})
	r.record(ctx, pub.ID, 0, "dedupe", "", start, llm.Response{})

	// create the issue now so all model steps attach to it
	number, err := r.DB.NextIssueNumber(ctx, pub.ID)
	if err != nil {
		return nil, err
	}
	res.Number = number
	// the stored subject mirrors assembleIssue's body heading — the
	// issue chrome, mail subject, and export title all render it as-is
	// (KLOX-56).
	word := "issue"
	if ptLang(pub.Language) {
		word = "edição"
	}
	// The issue snapshots the publication's section definitions: builder
	// edits land on the next edition, not the one already in progress
	// (KLOX-65).
	issueID, err := r.DB.CreateIssue(ctx, pub.ID, number,
		fmt.Sprintf("%s — %s %d", pub.Name, word, number), "", pub.Sections)
	if err != nil {
		return nil, err
	}
	res.IssueID = issueID
	r.DB.AttachRuns(ctx, pub.ID, issueID)
	if r.OnIssue != nil {
		r.OnIssue(issueID)
	}

	// --- triage ---
	r.stepStart("triage")
	start = time.Now()
	type triStory struct {
		Ref     int
		Title   string
		Outlets string
		Snippet string
	}
	// Real catalogs yield hundreds of stories and triage answers one row
	// per story — an unbounded response truncates mid-JSON, a scale the
	// fixture never reaches. Cap the prompt by a cheap pre-rank when the
	// batch overflows; leftovers keep score 0 and sit on the bench.
	// Under the cap the original order stands, so replay prompts are
	// unchanged.
	triIdx := make([]int, len(stories))
	for i := range triIdx {
		triIdx[i] = i
	}
	if len(triIdx) > maxTriageStories {
		triIdx = triageOrder(stories)[:maxTriageStories]
	}
	ts := make([]triStory, len(triIdx))
	for i, si := range triIdx {
		s := stories[si]
		snip := ""
		if len(s.Items) > 0 {
			snip = truncate(s.Items[0].ContentText, 400)
		}
		ts[i] = triStory{Ref: i + 1, Title: s.Title, Outlets: strings.Join(s.Outlets(), ", "), Snippet: snip}
	}
	scoreByRef := map[int]struct {
		score  float64
		reason string
	}{}
	var detScores []float64
	var detReasons []string
	var hiddenStory []bool
	var lineupVec *lineupVectors
	var enrich map[int64]*store.ItemEnrichment
	if r.Deterministic {
		// The free ranking reads the shared enrichment when it exists
		// (KLOX-67 §7.2): topic tags matching the publication's topic or
		// editorial line and the story's kind weight adjust the
		// recency + diversity score. No paid call happens here — the
		// enrichment fills asynchronously after ingest.
		var eids []int64
		for i := range stories {
			for j := range stories[i].Items {
				eids = append(eids, stories[i].Items[j].ID)
			}
		}
		enrich, _ = r.DB.EnrichmentsForItems(ctx, eids)
		detScores, detReasons = scoreStoriesEnriched(stories, pub.Language, pub, enrich)
		// Stage 1 (KLOX-109): embedding cosine against the publication
		// profile (FTS when vectors are missing) replaces the recency +
		// diversity order; the editorial-line demotions stay on top.
		s1 := r.rankStage1(ctx, pub, stories, detReasons, enrich)
		r.rerankStage(ctx, pub, stories, &s1) // stage 2 (KLOX-110); silent on failure
		detScores, detReasons = s1.Scores, s1.Reasons
		lineupVec = &lineupVectors{Story: s1.StoryVecs, Section: s1.SectionVecs}
		hiddenStory = make([]bool, len(stories))
		for i, h := range s1.Hidden {
			hiddenStory[i] = h != ""
		}
		updates := make([]store.StoryTriage, len(stories))
		for i := range stories {
			scoreByRef[i+1] = struct {
				score  float64
				reason string
			}{detScores[i], detReasons[i]}
			updates[i] = store.StoryTriage{StoryID: stories[i].ID, Score: detScores[i], Reason: detReasons[i],
				Rel: s1.Rel[i], Hidden: s1.Hidden[i]}
		}
		// One UPDATE for the whole run: deterministic triage scores
		// every story, and at shared-catalog scale a call per story
		// is thousands of remote round-trips (KLOX-41).
		if err := r.DB.SetStoriesTriage(ctx, updates); err != nil {
			return nil, err
		}
		r.emit("triage", map[string]int{"ranked": len(stories)})
		r.record(ctx, pub.ID, issueID, "triage", "deterministic", start, llm.Response{})
	} else {
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
			"RejectTerms":   pub.RejectTerms,
			"Guide":         r.guide(ctx, pub.ID),
			"Stories":       ts,
		}, &triage)
		if err != nil {
			return nil, fmt.Errorf("triage: %w", err)
		}
		res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
		r.emit("triage", map[string]int{"ranked": len(triage)})
		r.record(ctx, pub.ID, issueID, "triage", r.TriageModel, start, resp)
		for _, t := range triage {
			if t.Ref-1 >= 0 && t.Ref-1 < len(triIdx) {
				si := triIdx[t.Ref-1]
				scoreByRef[si+1] = struct {
					score  float64
					reason string
				}{t.Score, t.Reason}
				r.DB.SetStoryTriage(ctx, stories[si].ID, t.Score, t.Reason)
			}
		}
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
	var picks []lineupPick
	if r.Deterministic {
		picks = pickLineupVec(stories, detScores, detReasons, pub, enrich, hiddenStory, lineupVec)
		r.record(ctx, pub.ID, issueID, "storylist", "deterministic", start, llm.Response{})
	} else {
		var picked struct {
			Items []struct {
				Refs    []int  `json:"refs"`
				Title   string `json:"title"`
				Theme   string `json:"theme"`
				Why     string `json:"why"`
				Section string `json:"section"`
			} `json:"items"`
		}
		resp, err := r.call(ctx, r.TriageModel, "storylist", map[string]any{
			"Publication":   pub.Name,
			"Audience":      pub.Audience,
			"Sections":      pub.Sections,
			"EditorialLine": pub.EditorialLine,
			"RejectTerms":   pub.RejectTerms,
			"Guide":         r.guide(ctx, pub.ID),
			"Stories":       ss,
		}, &picked)
		if err != nil {
			return nil, fmt.Errorf("storylist: %w", err)
		}
		res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
		r.record(ctx, pub.ID, issueID, "storylist", r.TriageModel, start, resp)
		for _, p := range picked.Items {
			picks = append(picks, lineupPick{Refs: p.Refs, Title: p.Title, Why: p.Why, Section: p.Section})
		}
	}

	// --- create issue items, merging same-event stories ---

	entries := []entry{}
	used := make([]bool, len(stories))
	for idx, p := range picks {
		if len(p.Refs) == 0 {
			continue
		}
		primaryIdx := p.Refs[0] - 1
		if primaryIdx < 0 || primaryIdx >= len(stories) {
			return nil, fmt.Errorf("storylist: ref %d out of range", p.Refs[0])
		}
		used[primaryIdx] = true
		primary := stories[primaryIdx]
		var merges []int64
		memberItems := append([]store.Item{}, primary.Items...)
		for _, ref := range p.Refs[1:] {
			if ref-1 < 0 || ref-1 >= len(stories) {
				continue
			}
			used[ref-1] = true
			sec := stories[ref-1]
			merges = append(merges, sec.ID)
			memberItems = append(memberItems, sec.Items...)
		}
		if len(merges) > 0 {
			if err := r.DB.MergeStories(ctx, primary.ID, merges); err != nil {
				return nil, err
			}
		}
		memberItems = capEntrySources(evidenceItems(memberItems))
		if len(memberItems) == 0 {
			// A story with only signal members (a bare HN/DJ link) has
			// no evidence body — it can't be drafted or cited. Its
			// section is re-slotted below rather than left stranded.
			continue
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
	// Re-slot any section a skipped pick left empty: the best still-
	// unused story that carries evidence takes the slot, so a
	// collection can't strand a required section while draftable
	// stories remain in the pool (KLOX-60 — the deterministic picker
	// already skips evidence-less stories; this covers a model
	// storylist that doesn't). A section only stays empty when the
	// pool truly has no story that can back an item.
	sectionScores := make([]float64, len(stories))
	for i := range stories {
		sectionScores[i] = scoreByRef[i+1].score
	}
	filled := map[string]int{}
	for _, e := range entries {
		filled[e.section]++
	}
	nextRef := len(picks)
	reslotted := 0
	for _, sec := range pub.Sections {
		if filled[sec.Title] > 0 {
			continue
		}
		for _, idx := range storyOrder(stories, sectionScores) {
			if used[idx] || !hasEvidence(&stories[idx]) {
				continue
			}
			used[idx] = true
			reslotted++
			sc := scoreByRef[idx+1]
			why := sc.reason
			if why != "" {
				why += " · "
			}
			if ptLang(pub.Language) {
				why += "realocada para preencher a seção vazia"
			} else {
				why += "re-slotted to fill an empty section"
			}
			iiID, err := r.DB.CreateIssueItem(ctx, store.IssueItem{
				IssueID: issueID,
				StoryID: stories[idx].ID,
				Section: sec.Title,
				Score:   sc.score,
				Reason:  why,
				Status:  "pending",
			})
			if err != nil {
				return nil, err
			}
			nextRef++
			entries = append(entries, entry{
				iiID: iiID, storyID: stories[idx].ID, ref: nextRef,
				title: stories[idx].Title, section: sec.Title, why: why,
				score:   sc.score,
				sources: capEntrySources(evidenceItems(stories[idx].Items)),
			})
			break
		}
	}

	// The "selected" count the header shows is the lineup the issue
	// actually renders: picks with no evidence body (signal-only
	// stories) create no issue item and don't count (KLOX-55).
	res.Selected = len(entries)
	r.emit("storylist", map[string]int{"selected": res.Selected, "reslotted": reslotted})

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
	dossierByRef := map[int]*Dossier{}
	if r.Deterministic {
		// the rank note is reader-facing — it speaks the publication's
		// language like every other deterministic string (KLOX-56).
		rankNote := "Relevance score: %.1f/10."
		if ptLang(pub.Language) {
			rankNote = "Nota de relevância: %.1f/10."
		}
		for _, e := range entries {
			dossierByRef[e.ref] = synthDossier(e.ref, e.title,
				fmt.Sprintf(rankNote, e.score), e.sources)
		}
		r.emit("dossier", map[string]int{"dossiers": len(entries)})
		r.record(ctx, pub.ID, issueID, "dossier", "deterministic", start, llm.Response{})
	} else {
		var dossiers []Dossier
		resp, err := r.call(ctx, r.DraftModel, "dossier", map[string]any{
			"Publication": pub.Name,
			"Language":    pub.Language,
			"Items":       dItems,
		}, &dossiers)
		if err != nil {
			return nil, fmt.Errorf("dossier: %w", err)
		}
		res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
		r.emit("dossier", map[string]int{"dossiers": len(dossiers)})
		r.record(ctx, pub.ID, issueID, "dossier", r.DraftModel, start, resp)
		for i := range dossiers {
			dossierByRef[dossiers[i].Ref] = &dossiers[i]
		}
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
		dj := d.promptJSON()
		di := ddItem{Ref: e.ref, Title: e.title, Dossier: string(dj)}
		for j, it := range e.sources {
			di.Sources = append(di.Sources, dSrc{Ref: j + 1, URL: it.URL, Text: truncate(it.ContentText, 1200)})
		}
		ddItems = append(ddItems, di)
	}
	if r.Deterministic {
		// No model call: dossiers keep their synthesized facts only.
		r.emit("deepdive", map[string]int{"items": len(ddItems)})
		r.record(ctx, pub.ID, issueID, "deepdive", "deterministic", start, llm.Response{})
	} else {
		var deep []struct {
			Ref       int `json:"ref"`
			DeepFacts []struct {
				Text string `json:"text"`
				Ref  int    `json:"ref"`
			} `json:"deep_facts"`
			ExtraUnknowns []string `json:"extra_unknowns"`
		}
		resp, err := r.call(ctx, r.DraftModel, "deepdive", map[string]any{
			"Publication": pub.Name,
			"Language":    pub.Language,
			"Items":       ddItems,
		}, &deep)
		if err != nil {
			return nil, fmt.Errorf("deepdive: %w", err)
		}
		res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
		r.emit("deepdive", map[string]int{"items": len(ddItems)})
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
			// Only the AI-picked items belong to the recorded
			// canonical input — alternatives and manual adds would
			// change the replay keys.
			if ii.Origin != "phaseA" {
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
		section := ii.Section
		if canonical && ii.OrigSection != "" {
			// replay input must look exactly like the recorded Phase A
			// output — board moves mutate Section, OrigSection holds the
			// pick
			section = ii.OrigSection
		}
		entries = append(entries, entry{
			iiID: ii.ID, storyID: ii.StoryID, ref: d.Ref,
			title: title, section: section, why: ii.Reason, score: ii.Score,
			position: ii.Position,
		})
		dd := d
		dossierByRef[d.Ref] = &dd
	}
	return entries, dossierByRef, nil
}

// ensureDossiers generates dossiers for lineup items that lack one —
// stories the creator added on the board get theirs at draft time,
// so the draft run can pick up any board composition.
func (r *Runner) ensureDossiers(ctx context.Context, pub *store.Publication, issueID int64) error {
	iis, err := r.DB.IssueItems(ctx, issueID)
	if err != nil {
		return err
	}
	maxRef := 0
	var missing []store.IssueItem
	for _, ii := range iis {
		if dj, err := r.DB.Dossier(ctx, ii.ID); err == nil {
			var d Dossier
			if json.Unmarshal([]byte(dj), &d) == nil && d.Ref > maxRef {
				maxRef = d.Ref
			}
			continue
		}
		if ii.Status == "approved" || ii.Status == "pending" {
			missing = append(missing, ii)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	r.stepStart("dossier")
	for _, ii := range missing {
		cand, err := r.DB.StoryWithItems(ctx, ii.StoryID)
		if err != nil {
			return err
		}
		maxRef++
		d := r.alternativeDossier(ctx, pub, cand, maxRef)
		data, _ := json.Marshal(d)
		if err := r.DB.SaveDossier(ctx, ii.ID, string(data)); err != nil {
			return err
		}
	}
	r.emit("dossier", map[string]int{"dossiers": len(missing)})
	return nil
}

// RunPhaseB runs draft → assemble → citecheck on an issue produced by
// RunPhaseA (and reviewed): the stored body carries approved items only.
func (r *Runner) RunPhaseB(ctx context.Context, pub *store.Publication, issueID int64) (*Result, error) {
	res := &Result{IssueID: issueID}
	if err := r.ensureDossiers(ctx, pub, issueID); err != nil {
		return nil, err
	}
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
	draftByRef := map[int]struct {
		headline, body string
	}{}
	aiDraftRefs := map[int]bool{}
	if r.Deterministic {
		// No-model draft = honest compose skeleton: headline + sources,
		// empty body. Assembling source excerpts into prose would put
		// words in the draft nobody wrote (KLOX-48).
		for _, e := range entries {
			draftByRef[e.ref] = struct {
				headline, body string
			}{e.title, ""}
		}
		r.emit("draft", map[string]int{"entries": len(entries)})
		r.record(ctx, pub.ID, issueID, "draft", "deterministic", start, llm.Response{})
	} else {
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
			if _, ok := llm.AsBlocked(err); ok {
				// Capped model = same honest fallback as model-off:
				// store the compose skeleton, never excerpts (KLOX-48).
				return r.ComposeSkeleton(ctx, pub, issueID)
			}
			return nil, fmt.Errorf("draft: %w", err)
		}
		res.CostUSD += llm.CostUSD(r.DraftModel, resp.InputTokens, resp.OutputTokens)
		r.emit("draft", map[string]int{"entries": len(drafted.Entries)})
		r.record(ctx, pub.ID, issueID, "draft", r.DraftModel, start, resp)
		for _, de := range drafted.Entries {
			draftByRef[de.Ref] = struct {
				headline, body string
			}{de.Headline, de.BodyMD}
			// The pauta body arrived AI-written — mark it "Gerado por IA"
			// until the author edits (KLOX-67). Synthesized alternative
			// bodies below are deterministic assembly, not AI text.
			if strings.TrimSpace(de.BodyMD) != "" {
				aiDraftRefs[de.Ref] = true
			}
		}
		// Alternative items are never part of the canonical draft input, so
		// they have no model-drafted body: synthesize one from the dossier
		// (summary carries [n] citations, so it passes the citation rules).
		for _, e := range entries {
			if _, ok := draftByRef[e.ref]; !ok {
				draftByRef[e.ref] = struct {
					headline, body string
				}{e.title, synthBody(dossierByRef[e.ref])}
			}
		}
	}

	// --- assemble issue markdown ---
	// honor the board's slot order inside each section
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].position < entries[b].position })
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
	if r.Deterministic {
		// Deterministic citation checks only (no model call): the rules
		// below flag uncited sentences and citation-free entries.
		r.record(ctx, pub.ID, issueID, "citecheck", "deterministic", start, llm.Response{})
	} else {
		resp, err := r.call(ctx, r.TriageModel, "citecheck", map[string]any{
			"Publication": pub.Name,
			"Sentences":   cs,
			"Items":       ci,
		}, &checks)
		if err != nil {
			if _, blocked := llm.AsBlocked(err); !blocked {
				return nil, fmt.Errorf("citecheck: %w", err)
			}
			// Capped citecheck still lands the deterministic rules —
			// the draft stays, flagged by the lexical floor (KLOX-48).
		} else {
			res.CostUSD += llm.CostUSD(r.TriageModel, resp.InputTokens, resp.OutputTokens)
			r.record(ctx, pub.ID, issueID, "citecheck", r.TriageModel, start, resp)
		}
	}

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
	// deterministic rules (KLOX-48): these apply under the model too —
	// the lexical test is the floor a model verdict cannot waive.
	//   - a body sentence with no citation is flagged;
	//   - every [n] must be a source of its item, and the cited source's
	//     stored text must support the sentence ("not supported by [n]");
	//   - a sentence copying ≥10 consecutive source words is rejected
	//     ("too close to the source") unless it is a short attributed
	//     quotation;
	//   - an entry whose body carries no citation at all is flagged.
	textByItemID, err := r.sourceTexts(ctx, dossierByRef)
	if err != nil {
		return nil, err
	}
	for _, s := range extractSentences(body) {
		d := dossierByRef[s.itemRef]
		if s.refs == "" {
			addFlag(int64(s.itemRef), s.text, "sentence has no citation")
		} else if d != nil {
			byRef := map[int]DossierSource{}
			for _, src := range d.Sources {
				byRef[src.Ref] = src
			}
			for _, n := range CitationNums(s.refs) {
				src, ok := byRef[n]
				if !ok {
					addFlag(int64(s.itemRef), s.text,
						fmt.Sprintf("cites source [%d] that does not exist", n))
					continue
				}
				corpus := textByItemID[src.ItemID]
				switch {
				case !SupportedBySource(s.text, corpus):
					addFlag(int64(s.itemRef), s.text,
						fmt.Sprintf("not supported by [%d]", n))
				case UnsupportedDate(s.text, corpus):
					addFlag(int64(s.itemRef), s.text,
						fmt.Sprintf("date not supported by [%d]", n))
				case NegationUnsupported(s.text, corpus):
					addFlag(int64(s.itemRef), s.text,
						fmt.Sprintf("negation not supported by [%d]", n))
				}
			}
		}
		if d != nil {
			for _, src := range d.Sources {
				if TooCloseToSource(s.text, textByItemID[src.ItemID]) {
					addFlag(int64(s.itemRef), s.text, "too close to the source")
					break
				}
			}
		}
	}
	for _, e := range entries {
		de, ok := draftByRef[e.ref]
		if ok && de.body != "" && !refRe.MatchString(de.body) {
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
	for _, e := range entries {
		if aiDraftRefs[e.ref] {
			if err := r.DB.SetIssueItemAIDraft(ctx, e.iiID, true); err != nil {
				return nil, err
			}
		}
	}
	r.emit("citecheck", map[string]int{"checked": len(sents), "flagged": len(res.Flagged)})
	res.Number = number
	return res, nil
}

// sourceTexts batch-loads the stored body of every item a dossier
// cites, keyed by item id — the corpus the citation floor and the
// originality rule check against.
func (r *Runner) sourceTexts(ctx context.Context, dossiers map[int]*Dossier) (map[int64]string, error) {
	var ids []int64
	seen := map[int64]bool{}
	for _, d := range dossiers {
		for _, s := range d.Sources {
			if s.ItemID != 0 && !seen[s.ItemID] {
				seen[s.ItemID] = true
				ids = append(ids, s.ItemID)
			}
		}
	}
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	items, err := r.DB.GetItems(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, it := range items {
		// The headline counts as support (KLOX-100): a sentence echoing
		// the article's own title or standfirst is grounded in the
		// source, not a fabrication.
		out[id] = it.Title + "\n" + it.ContentText
	}
	return out, nil
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
	Title          string          `json:"title,omitempty"`  // storylist-picked title, set at save time
	Origin         string          `json:"origin,omitempty"` // "manual" when built without a model
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

// promptJSON marshals the dossier for embedding in a prompt: the same
// shape as the stored JSON minus database-local item ids, so replay keys
// stay stable when items land on different rows (reused databases,
// second publications, shared Postgres ids).
func (d *Dossier) promptJSON() []byte {
	c := *d
	c.Sources = make([]DossierSource, len(d.Sources))
	for i, s := range d.Sources {
		s.ItemID = 0
		c.Sources[i] = s
	}
	data, _ := json.Marshal(c)
	return data
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

// SplitSentences splits a paragraph into sentences. A boundary is only
// accepted at terminal punctuation followed by whitespace when the next
// rune can start a sentence and the previous token is not a common
// abbreviation (avoids splitting on "Dr.", "e.g.", decimals, etc.).
// citeLeadRe matches the citation markers at the start of a text run —
// the "[n] [m]" left dangling after a sentence-final period all belong
// to the sentence that precedes them, not to whatever comes next. Every
// consecutive marker must be consumed: dropping a trailing one would
// orphan it (and any flag keyed on the full stored line).
var citeLeadRe = regexp.MustCompile(`^(\[\d+\]\s*)+`)

func SplitSentences(line string) []string {
	var out []string
	start := 0
	// a quotation stays in one piece: splitting at a period inside
	// "…" broke an inserted quote into unquoted fragments that then
	// failed the copy rule's short-quote exception (KLOX-124)
	quotes := quoteRe.FindAllStringIndex(line, -1)
	for _, m := range sentEndRe.FindAllStringIndex(line, -1) {
		inQuote := false
		for _, q := range quotes {
			// a period right before the closing mark still ends the quote
			if m[0] > q[0] && m[0] < q[1]-1 && m[1] < q[1] {
				inQuote = true
				break
			}
		}
		if inQuote {
			continue
		}
		if m[0] <= start {
			// Boundary already inside the emitted span (e.g. the ". "
			// right after an attached citation): drop it.
			if m[1] > start {
				start = m[1]
			}
			continue
		}
		if cm := citeLeadRe.FindString(line[m[1]:]); cm != "" {
			// "urbana. [1] next." — the citation terminates the first
			// sentence; split after the marker so it stays attached.
			out = append(out, line[start:m[1]+len(cm)])
			start = m[1] + len(cm)
			continue
		}
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
		for _, s := range SplitSentences(trimmed) {
			s = strings.TrimSpace(s)
			// skip empty or punctuation-only remnants ("." left over
			// after an attached citation marker)
			if !strings.ContainsFunc(s, unicode.IsLetter) {
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

// docMarkupRe removes the tokens that would read as document structure
// inside a stored line: closed comments (`<!-- /item -->`), an unclosed
// comment opener, and the embed-fence tags.
var docMarkupRe = regexp.MustCompile(`<!--.*?-->|<!--|</?klox-embed>?`)

// oneLine flattens feed-supplied strings (story titles, dossier source
// names, picked section titles) to a single line and strips document
// markup: a newline, a `<!--` or a `<klox-embed` inside a stored
// heading would splice raw structural lines into the generated body —
// the same guard the composer's insertItemBlock applies (KLOX-73).
func oneLine(s string) string {
	s = docMarkupRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
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
	word := "issue"
	if ptLang(pub.Language) {
		word = "edição"
	}
	fmt.Fprintf(&b, "# %s — %s %d\n\n", pub.Name, word, number)
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
		fmt.Fprintf(&b, "### %s <!-- item %d -->\n\n", oneLine(headline), e.ref)
		b.WriteString(de.body + "\n\n")
		if len(d.Sources) > 0 {
			b.WriteString("**Sources:** ")
			parts := []string{}
			for _, s := range d.Sources {
				name := oneLine(s.Name)
				if name == "" {
					name = "source"
				}
				parts = append(parts, fmt.Sprintf("[%d] [%s](/a/%d)", s.Ref, name, s.ItemID))
			}
			b.WriteString(strings.Join(parts, " · ") + "\n\n")
		}
	}
	// The newsletter's stored template (KLOX-72) arranges the edition:
	// fixed blocks are copied in verbatim, "section" slots emit their
	// `##` heading plus this week's pautas — a slot keeps its place
	// even when the section has no items yet, so the edition opens
	// already shaped like the template. Sections the template does
	// not name fall back to the legacy tail order.
	emitted := map[string]bool{}
	writeSection := func(sec string, forceHeading bool) {
		list := bySection[sec]
		if emitted[sec] || (!forceHeading && len(list) == 0) {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n", oneLine(sec))
		for _, e := range list {
			writeItem(e)
		}
		emitted[sec] = true
	}
	inSnapshot := map[string]bool{}
	for _, sec := range sectionOrder {
		inSnapshot[sec] = true
	}
	// needsEndItem mirrors the composer's boundary rule: a divider,
	// link, image or text block landing right after an item would fold
	// into that item's span without the `<!-- /item -->` marker —
	// heading and embed blocks close an item on their own.
	needsEndItem := map[string]bool{"divider": true, "link": true, "image": true, "text": true}
	fixedAfterItem := false
	writeFixed := func(kind, line string) {
		if fixedAfterItem && needsEndItem[kind] {
			b.WriteString("<!-- /item -->\n\n")
		}
		b.WriteString(line + "\n\n")
		fixedAfterItem = false
	}
	for _, tb := range pub.Template {
		switch tb.Kind {
		case "section":
			if !inSnapshot[tb.Section] || emitted[tb.Section] {
				continue
			}
			writeSection(tb.Section, true)
			fixedAfterItem = len(bySection[tb.Section]) > 0
		default:
			if line, ok := delivery.BlockLine(tb.Kind, tb.Text, tb.URL); ok {
				writeFixed(tb.Kind, line)
			}
		}
	}
	for _, sec := range sectionOrder {
		writeSection(sec, false)
	}
	for sec, list := range bySection {
		if !emitted[sec] && len(list) > 0 {
			writeSection(sec, false)
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
	// a swap means the creator declined the old story: it is rejected
	// feedback for the Rocchio profile (KLOX-112).
	RecordFeedback(ctx, r.DB, pub.ID, old.ID, store.FeedbackReject)
	return r.DB.GetIssueItem(ctx, newID)
}

// alternativeDossier builds the dossier for an alternative story. Real
// mode asks the model; demo mode falls back to a deterministic dossier
// synthesized from the story's items when no replay fixture exists.
func (r *Runner) alternativeDossier(ctx context.Context, pub *store.Publication, cand *store.Story, ref int) *Dossier {
	interpretation := "Picked by the editor as an alternative to the previous item."
	if ptLang(pub.Language) {
		interpretation = "Escolhida pelo editor como alternativa à pauta anterior."
	}
	items := capEntrySources(cand.Items)
	if r.Deterministic {
		return synthDossier(ref, cand.Title, interpretation, items)
	}
	type dSrc struct {
		Ref  int
		URL  string
		Text string
	}
	var srcs []dSrc
	for j, it := range items {
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
				d.fillSources(items)
				return d
			}
		}
	}
	_ = resp
	// Synthetic dossier: every claim comes from a member item, so citations
	// stay valid without a model call.
	return synthDossier(ref, cand.Title, interpretation, items)
}

// ensureManualDossiers creates deterministic dossiers for lineup items
// that lack one — the compose flow's version of ensureDossiers, except
// it never calls the model.
func (r *Runner) ensureManualDossiers(ctx context.Context, issueID int64) error {
	return EnsureManualDossiers(ctx, r.DB, issueID)
}

// EnsureManualDossiers is the package-level form of
// ensureManualDossiers — the block-insert path uses it to give a pauta
// block dropped into a drafted edition its reference dossier without
// any model call (KLOX-66).
func EnsureManualDossiers(ctx context.Context, db *store.DB, issueID int64) error {
	iis, err := db.IssueItems(ctx, issueID)
	if err != nil {
		return err
	}
	maxRef := 0
	var missing []store.IssueItem
	for _, ii := range iis {
		if dj, err := db.Dossier(ctx, ii.ID); err == nil {
			var d Dossier
			if json.Unmarshal([]byte(dj), &d) == nil && d.Ref > maxRef {
				maxRef = d.Ref
			}
			continue
		}
		if ii.Status == "approved" || ii.Status == "pending" {
			missing = append(missing, ii)
		}
	}
	for _, ii := range missing {
		cand, err := db.StoryWithItems(ctx, ii.StoryID)
		if err != nil {
			return err
		}
		maxRef++
		// Origin "manual" marks the dossier as editor-facing reference
		// material: its sentences were never model-written, so edits on
		// them use the deterministic citation check, not a model call.
		d := synthDossier(maxRef, cand.Title, "", cand.Items)
		d.Interpretation = nil
		d.Origin = "manual"
		data, _ := json.Marshal(d)
		if err := db.SaveDossier(ctx, ii.ID, string(data)); err != nil {
			return err
		}
	}
	return nil
}

// ensureResearch fills the missing dossiers from the shared research
// cache — generating one Haiku call per new story (a §7.3 paid action)
// and marking each AI-filled pauta ai_draft. A story the research
// cannot serve (cap, kill switch, provider failure) gets no research
// dossier at all — owner D4 hides the unavailable element instead of
// labeling it: ensureManualDossiers then fills the slot with the same
// deterministic dossier compose uses with the model off, so the pauta
// keeps real sourced facts and stays a manual slot — empty body, no
// ai_draft marker, edits checked by the deterministic rule only.
// draftByRef receives the item bodies for assembleIssue.
func (r *Runner) ensureResearch(ctx context.Context, issueID int64, draftByRef map[int]struct {
	headline, body string
}) {
	iis, err := r.DB.IssueItems(ctx, issueID)
	if err != nil {
		return
	}
	maxRef := 0
	var missing []store.IssueItem
	for _, ii := range iis {
		if dj, err := r.DB.Dossier(ctx, ii.ID); err == nil {
			var d Dossier
			if json.Unmarshal([]byte(dj), &d) == nil && d.Ref > maxRef {
				maxRef = d.Ref
			}
			continue
		}
		if ii.Status == "approved" || ii.Status == "pending" {
			missing = append(missing, ii)
		}
	}
	for _, ii := range missing {
		story, err := r.DB.StoryWithItems(ctx, ii.StoryID)
		if err != nil {
			return
		}
		res, outcome, err := r.Research.ForStory(ctx, r.DB, story)
		if err != nil {
			return // store failure — let ensureManualDossiers take over
		}
		if outcome == researchDegraded {
			continue // no research dossier — the deterministic fill owns this slot
		}
		maxRef++
		d := res.ToDossier(maxRef)
		data, _ := json.Marshal(d)
		if err := r.DB.SaveDossier(ctx, ii.ID, string(data)); err != nil {
			return
		}
		if strings.TrimSpace(res.DraftBody) != "" {
			draftByRef[maxRef] = struct{ headline, body string }{body: res.DraftBody}
			if err := r.DB.SetIssueItemAIDraft(ctx, ii.ID, true); err != nil {
				return
			}
		}
	}
}

// ComposeSkeleton writes the issue body for the board's lineup. With a
// Researcher wired (AI on) each pauta arrives prefilled with the cached
// AI draft — claims carrying [n] citations, marked ai_draft until the
// author edits — while a story the research could not serve keeps the
// same deterministic dossier and empty slot it would have with the
// model off (owner D4: nothing renders an "unavailable" placeholder).
// With no Researcher the manual skeleton is exactly what it was:
// headlines + source lists only, no AI text.
func (r *Runner) ComposeSkeleton(ctx context.Context, pub *store.Publication, issueID int64) (*Result, error) {
	res := &Result{IssueID: issueID}
	draftByRef := map[int]struct {
		headline, body string
	}{}
	if r.Research != nil {
		r.ensureResearch(ctx, issueID, draftByRef)
	}
	if err := r.ensureManualDossiers(ctx, issueID); err != nil {
		return nil, err
	}
	entries, dossierByRef, err := r.loadEntries(ctx, issueID, false)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no approved items — approve at least one story before composing")
	}
	res.Selected = len(entries)
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].position < entries[b].position })
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
	if err := r.DB.UpdateIssue(ctx, issueID, "drafted", body, "[]"); err != nil {
		return nil, err
	}
	res.Number = number
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// RunHeadless is the CLI entry: build a runner for the given mode and run it.
// demo=true makes PhaseB use canonical replay input (see Runner.DemoMode);
// det=true runs the deterministic no-model pipeline (KLOXUS_LLM_MODE=off).
func RunHeadless(ctx context.Context, db *store.DB, pub *store.Publication, outDir string, fetcher sources.Fetcher, provider llm.Provider, triageModel, draftModel string, maxDeep int, demo, det bool) (*Result, error) {
	r := &Runner{
		DB:            db,
		LLM:           provider,
		Fetcher:       fetcher,
		TriageModel:   triageModel,
		DraftModel:    draftModel,
		MaxDeepDives:  maxDeep,
		DemoMode:      demo,
		Deterministic: det,
	}
	return r.Run(ctx, pub, outDir)
}

// maxTriageStories bounds the one prompt whose answer size scales with
// the catalog — everything else is fixed-shape or section-bounded.
const maxTriageStories = 80

// triageOrder ranks story indexes for the bounded triage prompt:
// article-bearing clusters first (signal-only groups can't be drafted),
// then corroborated ones (more member items), then freshest.
func triageOrder(stories []store.Story) []int {
	idx := make([]int, len(stories))
	for i := range idx {
		idx[i] = i
	}
	newest := make([]string, len(stories))
	hasArticle := make([]bool, len(stories))
	for i, s := range stories {
		for _, it := range s.Items {
			if !it.Signal() {
				hasArticle[i] = true
			}
			if it.PublishedAt > newest[i] {
				newest[i] = it.PublishedAt
			}
		}
	}
	sort.Slice(idx, func(a, b int) bool {
		i, j := idx[a], idx[b]
		if hasArticle[i] != hasArticle[j] {
			return hasArticle[i]
		}
		if len(stories[i].Items) != len(stories[j].Items) {
			return len(stories[i].Items) > len(stories[j].Items)
		}
		if newest[i] != newest[j] {
			return newest[i] > newest[j]
		}
		return stories[i].ID < stories[j].ID
	})
	return idx
}

// evidenceItems drops signal rows (links-only HN entries) from a
// story's member list for LLM and citation use: signals rank and
// attribute but are never evidence (D30). Story membership in the DB
// still includes them so the UI can show "via <source>".
func evidenceItems(items []store.Item) []store.Item {
	out := items[:0:0]
	for _, it := range items {
		if !it.Signal() {
			out = append(out, it)
		}
	}
	return out
}

// maxEntrySources bounds the evidence items one issue entry may cite —
// at 3 sources × 1,200 chars each, a giant cluster can no longer turn
// one dossier/deepdive call into an oversized prompt (KLOX-44).
const maxEntrySources = 3

// capEntrySources picks up to maxEntrySources evidence items: the
// newest item of each unseen outlet first, then the remaining slots by
// recency. The picks keep their original member order so entries with
// few sources see no change at all.
func capEntrySources(items []store.Item) []store.Item {
	if len(items) <= maxEntrySources {
		return items
	}
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ta, oka := parsePublished(items[idx[a]].PublishedAt)
		tb, okb := parsePublished(items[idx[b]].PublishedAt)
		if oka != okb {
			return oka // dated items rank above undated ones
		}
		if oka && !ta.Equal(tb) {
			return ta.After(tb) // newest first
		}
		return items[idx[a]].ID < items[idx[b]].ID
	})
	outlet := func(i int) string {
		if items[i].SourceName != "" {
			return items[i].SourceName
		}
		return outletLabel(items[i].URL)
	}
	seen := map[string]bool{}
	picked := map[int]bool{}
	var pick []int
	for _, i := range idx {
		if seen[outlet(i)] {
			continue
		}
		seen[outlet(i)] = true
		picked[i] = true
		pick = append(pick, i)
		if len(pick) == maxEntrySources {
			break
		}
	}
	for _, i := range idx { // fill leftover slots by recency
		if len(pick) == maxEntrySources {
			break
		}
		if !picked[i] {
			pick = append(pick, i)
		}
	}
	sort.Ints(pick)
	out := make([]store.Item, len(pick))
	for j, i := range pick {
		out[j] = items[i]
	}
	return out
}

// mergeCitedGroups folds a signal's cluster into the cluster holding an
// article the signal cites — a signal item's cited links point at the
// same reporting our launch feeds carried, so the signal attaches to
// that story instead of clustering alone. Merges respect the same
// MaxClusterSize cap as SimHash clusters: a merge that would overflow
// the cap is skipped and the signal keeps its own story. Cluster ids
// are processed in sorted order so cap decisions are deterministic.
func mergeCitedGroups(groups map[int][]store.Item) map[int][]store.Item {
	ids := make([]int, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	owner := map[string]int{}
	for _, id := range ids {
		for _, it := range groups[id] {
			if !it.Signal() && it.CanonicalURL != "" {
				if _, ok := owner[it.CanonicalURL]; !ok {
					owner[it.CanonicalURL] = id
				}
			}
		}
	}
	// union-find over cluster ids, with a per-root member count so a
	// merge that would push a story past MaxClusterSize never happens.
	parent := map[int]int{}
	size := map[int]int{}
	for _, id := range ids {
		size[id] = len(groups[id])
	}
	var find func(int) int
	find = func(x int) int {
		p, ok := parent[x]
		if !ok || p == x {
			return x
		}
		parent[x] = find(p)
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb && size[ra]+size[rb] <= dedupe.MaxClusterSize {
			parent[rb] = ra
			size[ra] += size[rb]
		}
	}
	for _, id := range ids {
		for _, it := range groups[id] {
			if !it.Signal() {
				continue
			}
			for _, u := range it.Meta.CitedURLs {
				if tgt, ok := owner[u]; ok {
					union(id, tgt)
				}
			}
		}
	}
	if len(parent) == 0 {
		return groups
	}
	merged := map[int][]store.Item{}
	for id, g := range groups {
		merged[find(id)] = append(merged[find(id)], g...)
	}
	return merged
}
