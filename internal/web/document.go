package web

// The structured issue document served by GET /api/v1/issues/{id}/document.
// It is parsed from the stored markdown body (sections → items →
// sentences) so the new interface never has to touch HTML or markdown:
// each sentence carries an id anchored to its item and a hash of its
// stored text, its kind (fact | interpretation), its citation refs and
// its flag when the citation check could not verify it. The document
// version changes with any mutation and guards concurrent edits.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/strutil"
)

// docEvidence is the supporting passage for one citation of a sentence:
// the dossier fact with that ref when one is known, else the article's
// opening.
type docEvidence struct {
	Ref     int    `json:"ref"`
	Excerpt string `json:"excerpt"`
}

// docSentence is one node of the issue document.
type docSentence struct {
	ID        string        `json:"id"`
	Text      string        `json:"text"` // display text, citations stripped
	Kind      string        `json:"kind"` // fact | interpretation
	Citations []int         `json:"citations"`
	Evidence  []docEvidence `json:"evidence,omitempty"` // one entry per citation
	Flag      *docFlag      `json:"flag,omitempty"`
	raw       string        // exact substring of the stored body
}

// docFlag is a citation-check flag attached to a sentence.
type docFlag struct {
	Note string `json:"note"`
}

// docItemFlag is a flag that could not be matched to a single sentence
// (e.g. "entry has no citations" covers the whole body of an item).
type docItemFlag struct {
	Sentence string `json:"sentence"`
	Note     string `json:"note"`
}

// docSource is one [n] source of an item.
type docSource struct {
	Ref     int    `json:"ref"`
	Name    string `json:"name"`
	URL     string `json:"url"`     // canonical article URL
	ItemID  int64  `json:"item_id"` // /a/{item_id} article page
	Excerpt string `json:"excerpt"` // the article's opening passage
	Date    string `json:"date"`    // published_at, when known
}

// docItem is one story inside a section.
type docItem struct {
	Ref         int    `json:"ref"`
	IssueItemID int64  `json:"issue_item_id"`
	Headline    string `json:"headline"`
	// Text is the item's written text as a single field: the stored
	// fact lines verbatim — punctuation, line breaks and emojis kept —
	// with only the [n] citation markers removed (KLOX-181). The
	// sentence-split model loses the period at every '. ' boundary;
	// pauta entries and exports read Text instead.
	Text      string        `json:"text"`
	Sentences []docSentence `json:"sentences"`
	Sources   []docSource   `json:"sources"`
	Flags     []docItemFlag `json:"flags,omitempty"` // flags not matched to a sentence
	// AIDraft marks the pauta whose body arrived AI-written (KLOX-67);
	// the composer shows the AI marker until the author's first edit.
	AIDraft bool `json:"ai_draft,omitempty"`
	// LinkOnly marks a pauta the editor sends as a bare link — no
	// written text required and no citation prompt (KLOX-66 D2). The
	// whole section may also imply it via the "links" format.
	LinkOnly bool `json:"link_only,omitempty"`

	// spanStart/spanEnd bound the item's block inside the stored body,
	// so edits replace only inside their own item — an identical
	// sentence elsewhere is never touched.
	spanStart, spanEnd int
	dossier            *pipeline.Dossier
	openings           map[int]string // source ref → article opening
}

// docCommentary is the per-section commentary state.
type docCommentary struct {
	Mode string `json:"mode"` // required | optional | none
	Text string `json:"text"`
}

// docBlock is one ordered unit inside a section (KLOX-66): a pauta
// (kind "item") or an author-inserted heading / divider / link-list /
// text block. The composer renders a section's blocks in order; block
// ops address one by its index inside the section.
type docBlock struct {
	Kind string `json:"kind"`          // item | heading | divider | link | text
	Ref  int    `json:"ref,omitempty"` // item → dossier ref
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"` // link → href

	// spanStart/spanEnd bound the block inside the stored body —
	// insert/move/remove/edit ops rewrite only their own block.
	spanStart, spanEnd int
}

// docSection is one section of the issue document. Format comes from
// the edition's section snapshot — "links" sections carry the link as
// their source and skip the written-text checks (KLOX-65).
type docSection struct {
	Title      string        `json:"title"`
	Format     string        `json:"format,omitempty"`
	Commentary docCommentary `json:"commentary"`
	Items      []docItem     `json:"items"`
	Blocks     []docBlock    `json:"blocks"`

	// spanStart/spanEnd bound the section's body region (between its
	// `## ` heading and the next one); -1 when the section only exists
	// in the snapshot and the body has no heading for it yet.
	spanStart, spanEnd int
}

// docCoverage is the always-visible coverage summary: ok counts fact
// sentences that are cited and unflagged — the counter shows ok/total.
type docCoverage struct {
	Ok      int `json:"ok"`
	Cited   int `json:"cited"`
	Total   int `json:"total"`
	Flagged int `json:"flagged"`
}

// issueDocument is the structured representation of an issue.
type issueDocument struct {
	IssueID  int64        `json:"issue_id"`
	Number   int          `json:"number"`
	Status   string       `json:"status"`
	Subject  string       `json:"subject"`
	Version  string       `json:"version"`
	Sections []docSection `json:"sections"`
	Coverage docCoverage  `json:"coverage"`

	// dctx and pub let a mutation handler re-render the response's
	// fresh document without paying the dossier/item queries again —
	// one document context load per request (KLOX-47).
	dctx *docContext
	pub  *store.Publication
}

var (
	docItemRe    = regexp.MustCompile(`^### (.*?)\s*<!--\s*item\s+(\d+)(?:\s+(link))?\s*-->\s*$`)
	docSectionRe = regexp.MustCompile(`^## (.+?)\s*$`)
	docCiteRe    = regexp.MustCompile(`\[(\d+)\]`)
)

// excerptMaxLen caps excerpts in the document model (best-evidence
// passage, source excerpt) — enough to orient the reader, short enough
// for the card layout.
const excerptMaxLen = 320

// docVersion is the document's optimistic-concurrency token: a hash of
// everything a mutation may touch (body, flags, commentary, status).
// Every document mutation must send it back; a stale value → conflict.
func docVersion(iss *store.Issue) string {
	flags, _ := json.Marshal(iss.Flags)
	commentary, _ := json.Marshal(iss.Commentary)
	sum := sha256.Sum256([]byte(iss.Status + "\x00" + iss.BodyMD + "\x00" +
		string(flags) + "\x00" + string(commentary)))
	return hex.EncodeToString(sum[:8])
}

// sentenceID anchors a sentence to its item and stored text: removing a
// sentence above never reassigns ids, and a stale id cannot silently
// resolve to different words. Identical sentences inside one item get a
// ".{occ}" suffix (second occurrence onwards).
func sentenceID(itemRef int, raw string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", itemRef, raw)))
	return fmt.Sprintf("%d.%s", itemRef, hex.EncodeToString(sum[:4]))
}

// displayText strips citation markers from a stored sentence and tidies
// the whitespace they leave behind.
func displayText(raw string) string {
	s := docCiteRe.ReplaceAllString(raw, "")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = strings.ReplaceAll(s, " .", ".")
	s = strings.ReplaceAll(s, " ,", ",")
	return strings.TrimSpace(s)
}

// citationRefs extracts the [n] refs carried by a stored sentence.
func citationRefs(raw string) []int {
	var out []int
	for _, m := range docCiteRe.FindAllStringSubmatch(raw, -1) {
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}

// docTextCiteRe removes the citation markers a stored fact line
// carries, including the space they were attached with.
var docTextCiteRe = regexp.MustCompile(`\s*\[\d+\]`)

// joinItemText builds the item's single text field from its stored
// fact lines: [n] markers out, the author's line breaks (including
// blank-line paragraph breaks) preserved, only the padding around the
// block trimmed.
func joinItemText(lines []string) string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimSpace(docTextCiteRe.ReplaceAllString(l, "")))
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// interpretationText unwraps a stored interpretation block
// ("> _Interpretação: text_") to the editor's words.
func interpretationText(line string) string {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ">"))
	if strings.HasPrefix(t, "_") && strings.HasSuffix(t, "_") && len(t) > 1 {
		t = strings.TrimSpace(t[1 : len(t)-1])
	}
	t = strings.TrimPrefix(t, "Interpretação: ")
	t = strings.TrimPrefix(t, "Interpretation: ")
	return strings.TrimSpace(t)
}

// locatedSentence is a parsed sentence with its owning item.
type locatedSentence struct {
	sentence *docSentence
	item     *docItem
}

// findItem locates a document item by its dossier ref.
func (doc *issueDocument) findItem(ref int) *docItem {
	for si := range doc.Sections {
		for ii := range doc.Sections[si].Items {
			if doc.Sections[si].Items[ii].Ref == ref {
				return &doc.Sections[si].Items[ii]
			}
		}
	}
	return nil
}

// findSentence locates a document sentence by its "item.hash" id.
func (doc *issueDocument) findSentence(id string) *locatedSentence {
	for si := range doc.Sections {
		for ii := range doc.Sections[si].Items {
			item := &doc.Sections[si].Items[ii]
			for ki := range item.Sentences {
				if item.Sentences[ki].ID == id {
					return &locatedSentence{sentence: &item.Sentences[ki], item: item}
				}
			}
		}
	}
	return nil
}

// replaceInItem swaps raw for repl inside the item's span only.
func replaceInItem(body string, item *docItem, raw, repl string) (string, bool) {
	if item.spanStart < 0 || item.spanEnd > len(body) || item.spanStart >= item.spanEnd {
		return body, false
	}
	seg := body[item.spanStart:item.spanEnd]
	if !strings.Contains(seg, raw) {
		return body, false
	}
	return body[:item.spanStart] + strings.Replace(seg, raw, repl, 1) + body[item.spanEnd:], true
}

// removeRaw deletes the first occurrence of raw inside a segment,
// taking along any citation marker that trails it and tidying the
// leftover whitespace — the segment-level core of removeInItem, shared
// with the bulk remove-flagged path.
func removeRaw(seg, raw string) (string, bool) {
	re := regexp.MustCompile(regexp.QuoteMeta(raw) + `(?:\s*\[\d+\])*\s*[.!?]*`)
	var newSeg string
	if loc := re.FindStringIndex(seg); loc != nil {
		newSeg = seg[:loc[0]] + seg[loc[1]:]
	} else if i := strings.Index(seg, raw); i >= 0 {
		newSeg = seg[:i] + seg[i+len(raw):]
	} else {
		return seg, false
	}
	newSeg = strings.ReplaceAll(newSeg, "  ", " ")
	for strings.Contains(newSeg, "\n\n\n") {
		newSeg = strings.ReplaceAll(newSeg, "\n\n\n", "\n\n")
	}
	// a sentence's terminal punctuation can survive as ". ." — collapse
	newSeg = strings.ReplaceAll(newSeg, ". .", ".")
	newSeg = strings.ReplaceAll(newSeg, " . ", " ")
	return newSeg, true
}

// removeInItem deletes a sentence within its item's span, taking along
// any citation marker that trails it and tidying the leftover
// whitespace — an identical sentence in another item is never touched.
func removeInItem(body string, item *docItem, raw string) (string, bool) {
	if item.spanStart < 0 || item.spanEnd > len(body) || item.spanStart >= item.spanEnd {
		return body, false
	}
	newSeg, ok := removeRaw(body[item.spanStart:item.spanEnd], raw)
	if !ok {
		return body, false
	}
	return body[:item.spanStart] + newSeg + body[item.spanEnd:], true
}

// bestEvidence returns the dossier fact carrying ref that best matches
// the sentence's wording (the passage that supports it); the article's
// opening when no fact matches.
func bestEvidence(d *pipeline.Dossier, ref int, sentence, fallback string) string {
	if d == nil {
		return fallback
	}
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(sentence), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(w) >= 3 {
			words[w] = true
		}
	}
	best, bestScore := "", 0
	for _, f := range append(append([]pipeline.DossierFact{}, d.Facts...), d.DeepFacts...) {
		if f.Ref != ref {
			continue
		}
		low := strings.ToLower(f.Text)
		score := 0
		for w := range words {
			if strings.Contains(low, w) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = f.Text, score
		}
	}
	if best == "" {
		return fallback
	}
	return strutil.Truncate(best, excerptMaxLen)
}

// docContext is everything the document parser needs from the store,
// loaded in two queries for the whole issue: one join for every lineup
// item and its dossier JSON, one batch load for every cited item —
// replacing the per-item Dossier call and the per-source GetItem call
// that made each document build N+1 against remote Postgres (KLOX-47).
// docItemInfo links one dossier ref to its issue item and parsed dossier.
type docItemInfo struct {
	issueItemID int64
	dossier     *pipeline.Dossier
	aiDraft     bool
}

type docContext struct {
	// item ref → issue item + dossier (the <!-- item N --> markers the
	// assembler writes equal dossier refs).
	byRef map[int]docItemInfo
	// cited item id → stored item (source excerpt, date, outlet name)
	items map[int64]*store.Item
	// issue items whose board decision is still open — counted from the
	// pairs the document already loaded, not a separate query
	pending int
}

// dcFromPairs builds the document context from already-loaded
// item/dossier pairs and cited items.
func dcFromPairs(pairs []store.IssueItemDossier, items map[int64]*store.Item) *docContext {
	dc := &docContext{byRef: map[int]docItemInfo{}, items: items}
	if dc.items == nil {
		dc.items = map[int64]*store.Item{}
	}
	for _, p := range pairs {
		if p.Item.Status == store.ItemPending {
			dc.pending++
		}
		var dd pipeline.Dossier
		if p.Dossier == "" || json.Unmarshal([]byte(p.Dossier), &dd) != nil {
			continue
		}
		dc.byRef[dd.Ref] = docItemInfo{issueItemID: p.Item.ID, dossier: &dd, aiDraft: p.Item.AIDraft}
	}
	return dc
}

// loadDocContext runs the document's only two store queries.
func loadDocContext(ctx context.Context, d *store.DB, iss *store.Issue) (*docContext, error) {
	pairs, err := d.IssueItemDossiers(ctx, iss.ID)
	if err != nil {
		return nil, err
	}
	var cited []int64
	seen := map[int64]bool{}
	for _, p := range pairs {
		var dd pipeline.Dossier
		if p.Dossier == "" || json.Unmarshal([]byte(p.Dossier), &dd) != nil {
			continue
		}
		for _, ds := range dd.Sources {
			if ds.ItemID != 0 && !seen[ds.ItemID] {
				seen[ds.ItemID] = true
				cited = append(cited, ds.ItemID)
			}
		}
	}
	items, err := d.GetItems(ctx, cited)
	if err != nil {
		return nil, err
	}
	return dcFromPairs(pairs, items), nil
}

// buildIssueDocument parses the stored issue body into the structured
// document. Flags are matched to sentences by (item ref, raw text);
// unmatched flags stay on their item.
func buildIssueDocument(ctx context.Context, d *store.DB, iss *store.Issue, pub *store.Publication) (*issueDocument, error) {
	if pub == nil {
		return nil, fmt.Errorf("publication %d not found", iss.PublicationID)
	}
	dc, err := loadDocContext(ctx, d, iss)
	if err != nil {
		return nil, err
	}
	return parseIssueDocument(iss, pub, dc), nil
}

// docFlagIndex groups flags by (item ref, raw sentence text),
// preserving flag order: each parsed sentence claims its flag, and the
// ones no sentence claims may attach to their item in stored order.
type docFlagIndex struct {
	bySentence map[int64]map[string]store.Flag
	order      map[int64][]store.Flag
}

func indexDocFlags(fl []store.Flag) docFlagIndex {
	idx := docFlagIndex{
		bySentence: map[int64]map[string]store.Flag{},
		order:      map[int64][]store.Flag{},
	}
	for _, f := range fl {
		if idx.bySentence[f.Item] == nil {
			idx.bySentence[f.Item] = map[string]store.Flag{}
		}
		idx.bySentence[f.Item][f.Sentence] = f
		idx.order[f.Item] = append(idx.order[f.Item], f)
	}
	return idx
}

// pubSectionIndex maps section title → section config from the
// edition's section snapshot, so the body parser resolves a `## `
// heading's commentary mode and format in one lookup.
func pubSectionIndex(pub *store.Publication) map[string]config.Section {
	idx := make(map[string]config.Section, len(pub.Sections))
	for _, sc := range pub.Sections {
		idx[sc.Title] = sc
	}
	return idx
}

// docBodyParser walks the stored markdown body line by line, growing
// the document's sections, items, blocks and sentences. sec and item
// point at the section/item currently being filled; textLines keeps
// the running item's stored fact lines for its single Text field;
// usedIDs disambiguates identical sentences inside one item;
// liveFlags counts the stored flags that still describe the document:
// a flag keyed to a sentence is live while that sentence exists; an
// "entry has no citations" flag is live while the item still has an
// uncited fact. A flag matching nothing would block approval with no
// UI path to clear it — it is stale and must not count (KLOX-52).
type docBodyParser struct {
	doc        *issueDocument
	dc         *docContext
	flags      docFlagIndex
	sections   map[string]config.Section
	commentary map[string]string

	sec       *docSection
	item      *docItem
	textLines []string // stored fact lines of the running item
	usedIDs   map[string]int
	liveFlags int
	textRun   bool // previous line extended a section text block
}

func (p *docBodyParser) parse(body string) {
	pos := 0 // byte offset of the current line inside the body
	lines := strings.Split(body, "\n")
	for li := 0; li < len(lines); li++ {
		line := lines[li]
		t := strings.TrimSpace(line)
		wasText := p.textRun
		p.textRun = false
		if m := docSectionRe.FindStringSubmatch(t); m != nil {
			p.finishItem(pos)
			p.startSection(m[1], pos, line)
		} else if m := docItemRe.FindStringSubmatch(t); m != nil {
			p.finishItem(pos)
			p.startItem(m[1], m[2], m[3], pos)
		} else if p.sec != nil && headingBlockLine(t) {
			p.finishItem(pos)
			p.addHeadingBlock(t, pos, line)
		} else if p.sec != nil && p.item != nil && strings.HasPrefix(t, "<!--") {
			// An `<!-- /item -->` marker closes the running item
			// (KLOX-66): `> ` inside an item is an interpretation
			// sentence, so an author text block that follows an item
			// can only be told from one by an explicit boundary — the
			// block ops write it.
			p.finishItem(pos)
		} else {
			// An embed fence opens a section-level block too — it
			// closes a running item the same way, but the line itself
			// still parses as a block below.
			if p.sec != nil && p.item != nil && t == delivery.EmbedFenceOpen {
				p.finishItem(pos)
			}
			if p.item == nil {
				li, pos = p.addSectionBlock(lines, li, pos, wasText)
				continue
			}
			p.addContent(t)
		}
		pos += len(line) + 1
	}
	p.finishItem(len(body))
	if p.sec != nil {
		p.sec.spanEnd = len(body)
	}
	p.alignBlockSpans()
}

// headingBlockLine reports whether a `###`/`####` line carries no item
// marker — it is an author heading block, which always closes the
// running item (KLOX-66).
func headingBlockLine(t string) bool {
	return strings.HasPrefix(t, "#### ") ||
		(strings.HasPrefix(t, "### ") && docItemRe.FindStringSubmatch(t) == nil)
}

// leadSection returns the section being filled, creating the untitled
// leading section (KLOX-72) lazily — only when a line actually opens a
// fence, parses as a block or starts an item, so the `#` title line
// and structural noise before the first `##` never leave an empty
// section behind.
func (p *docBodyParser) leadSection() *docSection {
	if p.sec == nil {
		p.doc.Sections = append(p.doc.Sections, docSection{
			Title: "", Commentary: docCommentary{Mode: "none"},
			Items: []docItem{}, Blocks: []docBlock{}, spanStart: 0, spanEnd: -1,
		})
		p.sec = &p.doc.Sections[len(p.doc.Sections)-1]
	}
	return p.sec
}

// startSection opens a "## title" section with the commentary mode and
// format its title carries in the edition's section snapshot.
func (p *docBodyParser) startSection(title string, pos int, line string) {
	if p.sec != nil {
		p.sec.spanEnd = pos
	}
	sc := p.sections[title]
	mode := sc.Commentary
	if mode == "" {
		mode = "none"
	}
	p.doc.Sections = append(p.doc.Sections, docSection{
		Title: title, Format: sc.Format,
		Commentary: docCommentary{Mode: mode, Text: p.commentary[title]},
		Items:      []docItem{}, Blocks: []docBlock{},
		spanStart: pos + len(line) + 1, spanEnd: -1,
	})
	p.sec = &p.doc.Sections[len(p.doc.Sections)-1]
}

// startItem opens a "### headline <!-- item N -->" item: its span
// starts at the heading, its sources come from the dossier and it
// appends the matching item block. An item before any "## " lands in
// the untitled leading section.
func (p *docBodyParser) startItem(headline, refText, marker string, pos int) {
	p.leadSection()
	ref, _ := strconv.Atoi(refText)
	it := docItem{Ref: ref, Headline: headline, spanStart: pos, openings: map[int]string{},
		LinkOnly: marker == "link", Sentences: []docSentence{}, Sources: []docSource{}}
	if info, ok := p.dc.byRef[ref]; ok {
		it.IssueItemID = info.issueItemID
		it.dossier = info.dossier
		it.AIDraft = info.aiDraft
		p.loadItemSources(&it, info.dossier)
	}
	p.sec.Items = append(p.sec.Items, it)
	p.item = &p.sec.Items[len(p.sec.Items)-1]
	p.sec.Blocks = append(p.sec.Blocks, docBlock{Kind: "item", Ref: ref,
		spanStart: pos, spanEnd: pos})
}

// loadItemSources fills the item's source list and article openings
// from its dossier: each source carries the stored article's opening
// excerpt and date so citation evidence has a fallback passage.
func (p *docBodyParser) loadItemSources(it *docItem, dossier *pipeline.Dossier) {
	for _, ds := range dossier.Sources {
		src := docSource{Ref: ds.Ref, Name: ds.Name, URL: ds.URL, ItemID: ds.ItemID}
		if ds.ItemID != 0 {
			if stored, ok := p.dc.items[ds.ItemID]; ok {
				src.Excerpt = strutil.Truncate(stored.ContentText, excerptMaxLen)
				src.Date = stored.PublishedAt
				it.openings[ds.Ref] = src.Excerpt
				if src.Name == "" {
					src.Name = stored.SourceName
				}
			}
		}
		it.Sources = append(it.Sources, src)
	}
}

// finishItem closes the current item's span at end, joins its stored
// fact lines into the single Text field, and attaches the flags that
// matched no sentence to the item itself — only while they still hold
// (see liveFlags on docBodyParser).
func (p *docBodyParser) finishItem(end int) {
	if p.item == nil {
		return
	}
	p.item.spanEnd = end
	p.item.Text = joinItemText(p.textLines)
	p.textLines = nil
	uncited := 0
	for _, sn := range p.item.Sentences {
		if sn.Kind == "fact" && len(sn.Citations) == 0 {
			uncited++
		}
	}
	for _, f := range p.flags.order[int64(p.item.Ref)] {
		if _, still := p.flags.bySentence[int64(p.item.Ref)][f.Sentence]; !still {
			continue
		}
		if f.Note == "entry has no citations" && uncited > 0 {
			p.item.Flags = append(p.item.Flags, docItemFlag{Sentence: f.Sentence, Note: f.Note})
			p.liveFlags++
		}
	}
	p.item = nil
}

// addSentence parses one raw stored sentence into the current item:
// display text, citations with their evidence, deduplicated id, and
// the flag matching its raw text.
func (p *docBodyParser) addSentence(raw, kind string) {
	if p.item == nil {
		return
	}
	var text string
	if kind == "interpretation" {
		text = interpretationText(raw)
	} else {
		text = displayText(raw)
	}
	sn := docSentence{
		Text: text, Kind: kind,
		Citations: citationRefs(raw), raw: raw,
	}
	if sn.Citations == nil {
		sn.Citations = []int{}
	}
	base := sentenceID(p.item.Ref, raw)
	occ := p.usedIDs[base]
	p.usedIDs[base]++
	sn.ID = base
	if occ > 0 {
		sn.ID = fmt.Sprintf("%s.%d", base, occ+1)
	}
	if kind == "fact" {
		for _, ref := range sn.Citations {
			sn.Evidence = append(sn.Evidence, docEvidence{
				Ref:     ref,
				Excerpt: bestEvidence(p.item.dossier, ref, text, p.item.openings[ref]),
			})
		}
	}
	if f, ok := p.flags.bySentence[int64(p.item.Ref)][raw]; ok {
		sn.Flag = &docFlag{Note: f.Note}
		delete(p.flags.bySentence[int64(p.item.Ref)], raw)
		p.liveFlags++
	} else if kind == "fact" && len(sn.Citations) == 0 {
		// an uncited fact blocks approval whether or not a stored
		// flag reached it (legacy bodies, restores without
		// citations, drafts written before the flag rule) — give it
		// the same flag treatment so the editor always has
		// Edit / interpretation / Remove to clear it (KLOX-52)
		sn.Flag = &docFlag{Note: "sentence has no citation"}
	}
	p.item.Sentences = append(p.item.Sentences, sn)
}

// addContent routes one body line inside an item: blank lines keep the
// author's own line breaks (invisible to the sentence parser,
// preserved in the item's text), `> ` blocks parse as interpretation
// sentences, and remaining text splits into fact sentences — headings,
// source lists and emphasis-only lines are structural noise.
func (p *docBodyParser) addContent(t string) {
	switch {
	case t == "":
		p.textLines = append(p.textLines, "")
	case strings.HasPrefix(t, "#"), strings.HasPrefix(t, "**"),
		strings.HasPrefix(t, "-"), strings.HasPrefix(t, "_"):
		// headings, source lists, emphasis-only lines
	case strings.HasPrefix(t, "> "):
		p.addSentence(t, "interpretation")
	default:
		p.textLines = append(p.textLines, t)
		for _, piece := range pipeline.SplitSentences(t) {
			piece = strings.TrimSpace(piece)
			if !strings.ContainsFunc(piece, unicode.IsLetter) {
				continue
			}
			p.addSentence(piece, "fact")
		}
	}
}

// addHeadingBlock appends an author heading block for a `###`/`####`
// line with no item marker.
func (p *docBodyParser) addHeadingBlock(t string, pos int, line string) {
	hd := strings.TrimSpace(t[strings.Index(t, " ")+1:])
	p.sec.Blocks = append(p.sec.Blocks, docBlock{Kind: "heading", Text: hd,
		spanStart: pos, spanEnd: pos + len(line)})
}

// addSectionBlock parses one line outside any item as a section-level
// layout block (KLOX-66): divider, link-list, image, embed and author
// text live between items. Returns the line index and byte offset to
// continue from — a closed embed fence consumes several lines.
func (p *docBodyParser) addSectionBlock(lines []string, li, pos int, wasText bool) (int, int) {
	line := lines[li]
	if strings.TrimSpace(line) == delivery.EmbedFenceOpen {
		return p.addEmbedBlock(lines, li, pos)
	}
	blk, isBlock := parseBlockLine(strings.TrimSpace(line))
	if !isBlock {
		return li, pos + len(line) + 1
	}
	p.leadSection()
	if blk.Kind == "text" && wasText && len(p.sec.Blocks) > 0 &&
		p.sec.Blocks[len(p.sec.Blocks)-1].Kind == "text" {
		// contiguous `> ` lines are one multi-paragraph text block —
		// a blank line splits them
		last := &p.sec.Blocks[len(p.sec.Blocks)-1]
		last.Text += "\n" + blk.Text
		last.spanEnd = pos + len(line)
		p.textRun = true
		return li, pos + len(line) + 1
	}
	blk.spanStart, blk.spanEnd = pos, pos+len(line)
	p.sec.Blocks = append(p.sec.Blocks, blk)
	p.textRun = blk.Kind == "text"
	return li, pos + len(line) + 1
}

// addEmbedBlock consumes a multi-line embed block: sanitized HTML
// lives between the fence lines. An unclosed fence is stray markup —
// only its opening line is skipped. Returns the line index and byte
// offset to continue from.
func (p *docBodyParser) addEmbedBlock(lines []string, li, pos int) (int, int) {
	liStart := li
	cur := pos + len(lines[li]) + 1
	var inner []string
	closed := false
	for li+1 < len(lines) {
		li++
		nl := lines[li]
		if strings.TrimSpace(nl) == delivery.EmbedFenceClose {
			closed = true
			break
		}
		inner = append(inner, nl)
		cur += len(nl) + 1
	}
	if !closed {
		return liStart, pos + len(lines[liStart]) + 1
	}
	// re-sanitize on read: a fence that reached the body past the
	// write path (legacy bodies, a bypassed guard) is served
	// sanitized too, so raw markup never reaches the composer canvas
	p.leadSection()
	p.sec.Blocks = append(p.sec.Blocks, docBlock{Kind: "embed",
		Text:      delivery.SanitizeEmbed(strings.Join(inner, "\n")),
		spanStart: pos, spanEnd: cur + len(lines[li])})
	return li, cur + len(lines[li]) + 1
}

// alignBlockSpans aligns each item block's span with its item — the
// item's end is only known once finishItem ran for it.
func (p *docBodyParser) alignBlockSpans() {
	for si := range p.doc.Sections {
		sec := &p.doc.Sections[si]
		bi := 0
		for i := range sec.Items {
			for bi < len(sec.Blocks) && sec.Blocks[bi].Kind != "item" {
				bi++
			}
			if bi >= len(sec.Blocks) {
				break
			}
			sec.Blocks[bi].spanStart = sec.Items[i].spanStart
			sec.Blocks[bi].spanEnd = sec.Items[i].spanEnd
			bi++
		}
	}
}

// appendCommentarySections adds publication sections the body has no
// heading for: sections with required commentary that carry no items
// still matter for the readiness checklist.
func appendCommentarySections(doc *issueDocument, pub *store.Publication, commentary map[string]string) {
	for _, sc := range pub.Sections {
		found := false
		for _, ds := range doc.Sections {
			if ds.Title == sc.Title {
				found = true
			}
		}
		if !found {
			doc.Sections = append(doc.Sections, docSection{
				Title: sc.Title, Format: sc.Format,
				Commentary: docCommentary{Mode: sc.Commentary, Text: commentary[sc.Title]},
				Items:      []docItem{}, Blocks: []docBlock{},
				spanStart: -1, spanEnd: -1,
			})
		}
	}
}

// coverageOf counts the always-visible coverage summary: ok counts
// fact sentences that are cited and unflagged — the counter shows
// ok/total.
func coverageOf(doc *issueDocument, liveFlags int) docCoverage {
	var cov docCoverage
	for _, s := range doc.Sections {
		for _, it := range s.Items {
			for _, sn := range it.Sentences {
				if sn.Kind != "fact" {
					continue
				}
				cov.Total++
				if len(sn.Citations) > 0 {
					cov.Cited++
					if sn.Flag == nil {
						cov.Ok++
					}
				}
			}
		}
	}
	cov.Flagged = liveFlags
	return cov
}

// parseIssueDocument renders the structured document from the stored
// body — a pure transform over the issue and a preloaded context, so a
// mutation handler can answer with the post-write document without a
// second round of dossier/item queries.
func parseIssueDocument(iss *store.Issue, pub *store.Publication, dc *docContext) *issueDocument {
	// The document — and every consumer holding doc.pub — works from
	// the edition's own section snapshot, so builder edits made after
	// the run never rewrite an in-progress issue (KLOX-65).
	pub = pubWithIssueSections(iss, pub)
	doc := &issueDocument{
		IssueID: iss.ID, Number: iss.Number, Status: iss.Status, Subject: iss.Subject,
		Version: docVersion(iss), Sections: []docSection{},
		dctx: dc, pub: pub,
	}
	commentary := iss.Commentary
	if commentary == nil {
		commentary = map[string]string{}
	}
	p := &docBodyParser{
		doc: doc, dc: dc,
		flags:      indexDocFlags(iss.Flags),
		sections:   pubSectionIndex(pub),
		commentary: commentary,
		usedIDs:    map[string]int{},
	}
	p.parse(iss.BodyMD)
	appendCommentarySections(doc, pub, commentary)
	doc.Coverage = coverageOf(doc, p.liveFlags)
	return doc
}

// insertRaw splices raw into a segment at the idx-th sentence position
// (before raws[idx]; appended before the sources block when idx is past
// the current count). raws is the segment's sentence sequence — callers
// that splice several entries keep it in sync between inserts.
func insertRaw(seg string, raws []string, idx int, raw string) string {
	// find the byte offset inside seg where the idx-th sentence starts —
	// walk each sentence in order so a duplicate raw earlier in the item
	// cannot land the splice on the wrong occurrence
	pos := 0
	if idx < len(raws) {
		for i := 0; i <= idx; i++ {
			target := raws[i]
			j := strings.Index(seg[pos:], target)
			if j < 0 {
				pos = len(seg)
				break
			}
			pos += j
			if i == idx {
				break
			}
			pos += len(target)
		}
	} else {
		pos = len(seg)
		// appending past the last sentence still belongs to the item's
		// body — land before the sources block, not after it
		if i := strings.LastIndex(seg, "**Sources:**"); i >= 0 {
			pos = i
		}
	}
	// raw is a block of whole lines; splice it in on sentence boundaries.
	// The stored body keeps several sentences per line separated by
	// '. ' — mid-line, that separator belongs to no raw. A '\n' between
	// the separator and the next piece would leave 'X [1].' at line
	// end, where SplitSentences re-attaches the '.' to the raw ('X [1]'
	// becomes 'X [1].'), changing its text, id and flag match.
	head := seg[:pos]
	switch {
	case pos == len(seg):
		// append at the segment end: a fact rides the last line so no
		// separator can re-attach; a '>' block still needs its own line
		if head != "" && !strings.HasSuffix(head, "\n") {
			if strings.HasPrefix(raw, ">") {
				raw = "\n" + raw
			} else if !strings.HasSuffix(head, " ") {
				raw = " " + raw
			}
		}
		return head + raw
	case strings.HasPrefix(raw, ">") || punctTerminated(raw):
		// interpretation blocks parse only at line start, and a raw that
		// already ends '.', '!' or '?' must stay at line end to survive
		// the round-trip — both splice on their own line
		if head != "" && !strings.HasSuffix(head, "\n") {
			raw = "\n" + raw
		}
		if !strings.HasSuffix(raw, "\n") {
			raw += "\n"
		}
		return head + raw + seg[pos:]
	case head == "" || strings.HasSuffix(head, "\n"):
		// line boundary — the entry gets its own line
		return head + raw + "\n" + seg[pos:]
	case sentenceSepTail(head):
		// the previous piece ended with a '. '/'! '/'? ' separator:
		// splice inline so it keeps bounding the previous piece and a
		// fresh '. ' bounds the entry — byte-identical to a segment
		// where the sentence was never removed
		return head + raw + ". " + seg[pos:]
	default:
		// a bare-space separator (post-citation split 'urbana. [1] next')
		// leaves the '.' inside the previous raw — a line splice keeps
		// every piece identical
		raw = "\n" + raw + "\n"
		return head + raw + seg[pos:]
	}
}

// punctTerminated reports whether a sentence raw ends in terminal
// punctuation — such raws only parse identically at end of line.
func punctTerminated(raw string) bool {
	return strings.HasSuffix(raw, ".") || strings.HasSuffix(raw, "!") || strings.HasSuffix(raw, "?")
}

// sentenceSepTail reports whether head ends with a '. '/'! '/'? '
// separator — the split point SplitSentences consumed between pieces.
func sentenceSepTail(head string) bool {
	return len(head) >= 2 && strings.ContainsRune(".!?", rune(head[len(head)-2])) &&
		(head[len(head)-1] == ' ' || head[len(head)-1] == '\t')
}

// insertInItem writes composed raw back into the item's span at the
// index-th sentence position (appended before the span end when idx is
// past the current count). Used by the sentence-restore endpoint.
func insertInItem(body string, item *docItem, idx int, raw string) (string, bool) {
	if item.spanStart < 0 || item.spanEnd > len(body) || item.spanStart >= item.spanEnd {
		return body, false
	}
	seg := body[item.spanStart:item.spanEnd]
	raws := make([]string, len(item.Sentences))
	for i, sn := range item.Sentences {
		raws[i] = sn.raw
	}
	// body[spanEnd:] carries the rest of the issue — next items and
	// sections; dropping it here would silently truncate the document
	return body[:item.spanStart] + insertRaw(seg, raws, idx, raw) + body[item.spanEnd:], true
}
