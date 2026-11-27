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
	"github.com/sthefanocordeiro/kloxus/internal/strutil"
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
	// PaceStep, if set, runs at each step boundary before OnStep — demo
	// deployments wire a short sleep (PacedStep) so fixture replays,
	// which finish in milliseconds, stay visible on progress displays.
	// Production leaves it nil: pacing is presentation, not pipeline work.
	PaceStep func(step string)
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

// Prompt-size limits, output budgets and step caps. Each name carries
// the reason for the value so a change is deliberate, not a typo.
const (
	// callMaxTokens is the fallback output ceiling for r.call steps
	// without a stepMaxTokens entry — every prompt asks for a single
	// JSON object, and 8k output tokens stays well above the largest
	// expected reply while capping the cost of a runaway completion.
	callMaxTokens = 8192

	// triageSnippetMaxLen caps each story excerpt in the triage prompt
	// so dozens of stories fit in context without one long article
	// crowding the rest out.
	triageSnippetMaxLen = 400

	// dossierSourceMaxLen caps each source's text sent to the dossier,
	// deepdive and alternative-dossier prompts: enough for lede and
	// body without spending the context on a whole article.
	dossierSourceMaxLen = 1200

	// fetchErrorMaxLen caps the message stored on a source after a
	// failed fetch — it surfaces in the sources list, where a long
	// error would dominate the layout.
	fetchErrorMaxLen = 120

	// flagExcerptMaxLen caps the draft excerpt attached to a
	// no-citations flag so the flagged list stays scannable.
	flagExcerptMaxLen = 120

	// deepDiveCap bounds how many top-scored items get the extra
	// deepdive pass; each dive is a full model call over all of the
	// item's sources, so it stays small regardless of lineup size.
	deepDiveCap = 3
)

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
		maxTok = callMaxTokens
	}
	req := llm.Request{Model: model, Template: name, User: user, MaxTokens: maxTok}
	resp, err := r.complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if err := decodeJSON(resp.Text, out); err != nil {
		if llm.RequestKeyed(r.LLM) {
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
		if ii.Status == store.ItemPending {
			if err := r.DB.SetIssueItemStatus(ctx, ii.ID, store.ItemApproved); err != nil {
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

// PacedStep returns a PaceStep hook that sleeps d at each step boundary.
// Demo deployments wire it so fixture replays stay visible on progress
// displays; production leaves PaceStep nil.
func PacedStep(d time.Duration) func(step string) {
	return func(string) { time.Sleep(d) }
}

func (r *Runner) stepStart(name string) {
	if r.PaceStep != nil {
		r.PaceStep(name)
	}
	if r.OnStep != nil {
		r.OnStep(name)
	}
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
func assembleIssue(pub *store.Publication, number int, entries []entry, drafts map[int]draftEntry, dossiers map[int]*Dossier, sectionOrder []string) string {
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

// AlternativeSubstitution is the prepared content of an alternative
// swap. The dossier (including any model call) is computed before the
// write boundary so the database transaction only covers the write
// statements themselves.
type AlternativeSubstitution struct {
	Candidate   *store.Story
	DossierJSON string
}

// PrepareAlternative picks the best-scored story not already in the
// issue and builds its dossier. It performs no writes; CommitAlternative
// applies the swap atomically.
func (r *Runner) PrepareAlternative(ctx context.Context, pub *store.Publication, iss *store.Issue) (*AlternativeSubstitution, error) {
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
	d := r.alternativeDossier(ctx, pub, cand, newRef)
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	return &AlternativeSubstitution{Candidate: cand, DossierJSON: string(data)}, nil
}

// CommitAlternative writes one prepared swap: the new pending item, its
// dossier and the old item's alternative status either all commit or the
// edition is left exactly as it was. Best-effort feedback stays outside
// this boundary.
func (r *Runner) CommitAlternative(ctx context.Context, iss *store.Issue, old *store.IssueItem, sub *AlternativeSubstitution) (*store.IssueItem, error) {
	var newID int64
	err := r.DB.WithTx(ctx, func(txdb *store.DB) error {
		id, err := txdb.CreateIssueItem(ctx, store.IssueItem{
			IssueID: iss.ID,
			StoryID: sub.Candidate.ID,
			Section: old.Section,
			Score:   sub.Candidate.TriageScore,
			Reason:  sub.Candidate.TriageReason,
			Status:  store.ItemPending,
			Origin:  "alternative",
		})
		if err != nil {
			return err
		}
		newID = id
		if err := txdb.SaveDossier(ctx, id, sub.DossierJSON); err != nil {
			return err
		}
		return txdb.SetIssueItemStatus(ctx, old.ID, store.ItemAlternative)
	})
	if err != nil {
		return nil, err
	}
	return r.DB.GetIssueItem(ctx, newID)
}

// SubstituteAlternative replaces an issue item with the best-scored story
// not already in the issue (triage ranking), generating a dossier for it.
// The new item starts as pending; the old one is marked 'alternative'.
func (r *Runner) SubstituteAlternative(ctx context.Context, pub *store.Publication, iss *store.Issue, old *store.IssueItem) (*store.IssueItem, error) {
	sub, err := r.PrepareAlternative(ctx, pub, iss)
	if err != nil {
		return nil, err
	}
	newII, err := r.CommitAlternative(ctx, iss, old, sub)
	if err != nil {
		return nil, err
	}
	// a swap means the creator declined the old story: it is rejected
	// feedback for the Rocchio profile (KLOX-112).
	RecordFeedback(ctx, r.DB, pub.ID, old.ID, store.FeedbackReject)
	return newII, nil
}

// alternativeDossier builds the dossier for an alternative story. Real
// mode asks the model; fixture replay falls back to a deterministic
// dossier synthesized from the story's items when no fixture exists.
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
		srcs = append(srcs, dSrc{Ref: j + 1, URL: it.URL, Text: strutil.Cut(it.ContentText, dossierSourceMaxLen)})
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
		if ii.Status == store.ItemApproved || ii.Status == store.ItemPending {
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
func (r *Runner) ensureResearch(ctx context.Context, issueID int64, draftByRef map[int]draftEntry) {
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
		if ii.Status == store.ItemApproved || ii.Status == store.ItemPending {
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
			draftByRef[maxRef] = draftEntry{body: res.DraftBody}
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
	draftByRef := map[int]draftEntry{}
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
	if err := r.DB.UpdateIssue(ctx, issueID, store.IssueDrafted, body, "[]"); err != nil {
		return nil, err
	}
	res.Number = number
	return res, nil
}

// RunHeadless is the CLI entry: build a runner and run it. A
// request-keyed provider (fixture replay) automatically gets the
// recorded canonical Phase-B input (see llm.RequestKeyed); det=true
// runs the deterministic no-model pipeline (KLOXUS_LLM_MODE=off).
func RunHeadless(ctx context.Context, db *store.DB, pub *store.Publication, outDir string, fetcher sources.Fetcher, provider llm.Provider, triageModel, draftModel string, maxDeep int, det bool) (*Result, error) {
	r := &Runner{
		DB:            db,
		LLM:           provider,
		Fetcher:       fetcher,
		TriageModel:   triageModel,
		DraftModel:    draftModel,
		MaxDeepDives:  maxDeep,
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

// mergeEventGroups folds clusters whose headlines cover the same event
// from different outlets (KLOX-121) — SimHash only catches republished
// bodies, not five outlets reporting one launch. Merges are bounded by
// the 72 h window and MaxClusterSize, and never chain past the cap.
func mergeEventGroups(groups map[int][]store.Item) map[int][]store.Item {
	ids := make([]int, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	when := func(it store.Item) time.Time {
		if t, ok := parsePublished(it.PublishedAt); ok {
			return t
		}
		t, _ := parsePublished(it.FetchedAt)
		return t
	}
	parent := map[int]int{}
	size := map[int]int{}
	for _, id := range ids {
		size[id] = len(groups[id])
	}
	find := func(x int) int {
		for {
			p, ok := parent[x]
			if !ok || p == x {
				return x
			}
			x = p
		}
	}
	same := func(a, b []store.Item) bool {
		for _, x := range a {
			for _, y := range b {
				tx, ty := when(x), when(y)
				if tx.IsZero() || ty.IsZero() || tx.Sub(ty) > dedupe.MaxMergeAge || ty.Sub(tx) > dedupe.MaxMergeAge {
					continue
				}
				if dedupe.TitleSameEvent(x.Title, y.Title) {
					return true
				}
			}
		}
		return false
	}
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			ra, rb := find(a), find(b)
			if ra == rb || size[ra]+size[rb] > dedupe.MaxClusterSize {
				continue
			}
			if same(groups[a], groups[b]) {
				parent[rb] = ra
				size[ra] += size[rb]
			}
		}
	}
	if len(parent) == 0 {
		return groups
	}
	merged := map[int][]store.Item{}
	for _, id := range ids {
		merged[find(id)] = append(merged[find(id)], groups[id]...)
	}
	return merged
}
