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
	"unicode/utf8"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
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
	Ref         int           `json:"ref"`
	IssueItemID int64         `json:"issue_item_id"`
	Headline    string        `json:"headline"`
	Sentences   []docSentence `json:"sentences"`
	Sources     []docSource   `json:"sources"`
	Flags       []docItemFlag `json:"flags,omitempty"` // flags not matched to a sentence
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
	return truncateStr(best, 320)
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
		if p.Item.Status == "pending" {
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

	// flags by (item ref, raw sentence text), preserving flag order
	flags := map[int64]map[string]store.Flag{}
	flagOrder := map[int64][]store.Flag{}
	for _, f := range iss.Flags {
		if flags[f.Item] == nil {
			flags[f.Item] = map[string]store.Flag{}
		}
		flags[f.Item][f.Sentence] = f
		flagOrder[f.Item] = append(flagOrder[f.Item], f)
	}

	secMode := map[string]string{}
	secFormat := map[string]string{}
	for _, sc := range pub.Sections {
		secMode[sc.Title] = sc.Commentary
		secFormat[sc.Title] = sc.Format
	}
	commentary := iss.Commentary
	if commentary == nil {
		commentary = map[string]string{}
	}

	var sec *docSection
	var item *docItem
	usedIDs := map[string]int{}
	// liveFlags counts the stored flags that still describe the
	// document: a flag keyed to a sentence is live while that sentence
	// exists; an "entry has no citations" flag is live while the item
	// still has an uncited fact. A flag matching nothing would block
	// approval with no UI path to clear it — it is stale and must not
	// count (KLOX-52).
	liveFlags := 0
	finishItem := func(end int) {
		if item == nil {
			return
		}
		item.spanEnd = end
		uncited := 0
		for _, sn := range item.Sentences {
			if sn.Kind == "fact" && len(sn.Citations) == 0 {
				uncited++
			}
		}
		// attach flags that matched no sentence to the item itself —
		// only while they still hold (see liveFlags above)
		for _, f := range flagOrder[int64(item.Ref)] {
			if _, still := flags[int64(item.Ref)][f.Sentence]; !still {
				continue
			}
			if f.Note == "entry has no citations" && uncited > 0 {
				item.Flags = append(item.Flags, docItemFlag{Sentence: f.Sentence, Note: f.Note})
				liveFlags++
			}
		}
		item = nil
	}
	addSentence := func(raw, kind string) {
		if item == nil {
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
		base := sentenceID(item.Ref, raw)
		occ := usedIDs[base]
		usedIDs[base]++
		sn.ID = base
		if occ > 0 {
			sn.ID = fmt.Sprintf("%s.%d", base, occ+1)
		}
		if kind == "fact" {
			for _, ref := range sn.Citations {
				sn.Evidence = append(sn.Evidence, docEvidence{
					Ref:     ref,
					Excerpt: bestEvidence(item.dossier, ref, text, item.openings[ref]),
				})
			}
		}
		if f, ok := flags[int64(item.Ref)][raw]; ok {
			sn.Flag = &docFlag{Note: f.Note}
			delete(flags[int64(item.Ref)], raw)
			liveFlags++
		} else if kind == "fact" && len(sn.Citations) == 0 {
			// an uncited fact blocks approval whether or not a stored
			// flag reached it (legacy bodies, restores without
			// citations, drafts written before the flag rule) — give it
			// the same flag treatment so the editor always has
			// Edit / interpretation / Remove to clear it (KLOX-52)
			sn.Flag = &docFlag{Note: "sentence has no citation"}
		}
		item.Sentences = append(item.Sentences, sn)
	}
	pos := 0         // byte offset of the current line inside the body
	textRun := false // previous line extended a section text block
	lines := strings.Split(iss.BodyMD, "\n")
	for li := 0; li < len(lines); li++ {
		line := lines[li]
		t := strings.TrimSpace(line)
		wasText := textRun
		textRun = false
		if m := docSectionRe.FindStringSubmatch(t); m != nil {
			finishItem(pos)
			if sec != nil {
				sec.spanEnd = pos
			}
			mode := secMode[m[1]]
			if mode == "" {
				mode = "none"
			}
			doc.Sections = append(doc.Sections, docSection{
				Title: m[1], Format: secFormat[m[1]],
				Commentary: docCommentary{Mode: mode, Text: commentary[m[1]]},
				Items:      []docItem{}, Blocks: []docBlock{},
				spanStart: pos + len(line) + 1, spanEnd: -1,
			})
			sec = &doc.Sections[len(doc.Sections)-1]
			pos += len(line) + 1
			continue
		}
		if m := docItemRe.FindStringSubmatch(t); m != nil {
			finishItem(pos)
			if sec == nil {
				doc.Sections = append(doc.Sections, docSection{Title: "", Commentary: docCommentary{Mode: "none"},
					Items: []docItem{}, Blocks: []docBlock{}, spanStart: 0, spanEnd: -1})
				sec = &doc.Sections[len(doc.Sections)-1]
			}
			ref, _ := strconv.Atoi(m[2])
			it := docItem{Ref: ref, Headline: m[1], spanStart: pos, openings: map[int]string{},
				LinkOnly: m[3] == "link", Sentences: []docSentence{}, Sources: []docSource{}}
			if info, ok := dc.byRef[ref]; ok {
				it.IssueItemID = info.issueItemID
				it.dossier = info.dossier
				it.AIDraft = info.aiDraft
				for _, ds := range info.dossier.Sources {
					src := docSource{Ref: ds.Ref, Name: ds.Name, URL: ds.URL, ItemID: ds.ItemID}
					if ds.ItemID != 0 {
						if stored, ok := dc.items[ds.ItemID]; ok {
							src.Excerpt = truncateStr(stored.ContentText, 320)
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
			sec.Items = append(sec.Items, it)
			item = &sec.Items[len(sec.Items)-1]
			sec.Blocks = append(sec.Blocks, docBlock{Kind: "item", Ref: ref,
				spanStart: pos, spanEnd: pos})
			pos += len(line) + 1
			continue
		}
		// A `###`/`####` line without an item marker is an author
		// heading block — it always closes the running item (KLOX-66).
		if sec != nil && (strings.HasPrefix(t, "#### ") ||
			(strings.HasPrefix(t, "### ") && docItemRe.FindStringSubmatch(t) == nil)) {
			finishItem(pos)
			hd := strings.TrimSpace(t[strings.Index(t, " ")+1:])
			sec.Blocks = append(sec.Blocks, docBlock{Kind: "heading", Text: hd,
				spanStart: pos, spanEnd: pos + len(line)})
			pos += len(line) + 1
			continue
		}
		// An `<!-- /item -->` marker closes the running item (KLOX-66):
		// `> ` inside an item is an interpretation sentence, so an
		// author text block that follows an item can only be told from
		// one by an explicit boundary — the block ops write it. An
		// embed fence opens a section-level block too — it closes a
		// running item the same way, but the line itself still parses
		// as a block below.
		if sec != nil && item != nil && strings.HasPrefix(t, "<!--") {
			finishItem(pos)
			pos += len(line) + 1
			continue
		}
		if sec != nil && item != nil && t == delivery.EmbedFenceOpen {
			finishItem(pos)
		}
		// Section-level layout blocks (KLOX-66): divider, link-list,
		// image, embed and author text live between items. Inside an
		// item nothing changes — `> ` stays an interpretation sentence
		// and other marks are folded into the item's span as before.
		// Before the first `##` they land in the untitled leading
		// section (KLOX-72) — template header blocks like the banner
		// stay ordinary composer blocks addressed as (section="", i).
		if item == nil {
			// The untitled leading section (KLOX-72) is created lazily —
			// only when the line actually opens a fence or parses as a
			// block, so the `#` title line and structural noise before
			// the first `##` never leave an empty section behind.
			leadSection := func() {
				if sec == nil {
					doc.Sections = append(doc.Sections, docSection{Title: "", Commentary: docCommentary{Mode: "none"},
						Items: []docItem{}, Blocks: []docBlock{}, spanStart: 0, spanEnd: -1})
					sec = &doc.Sections[len(doc.Sections)-1]
				}
			}
			if t == delivery.EmbedFenceOpen {
				// multi-line embed block: sanitized HTML lives between
				// the fence lines. An unclosed fence is stray markup —
				// skip only its opening line.
				liStart := li
				cur := pos + len(line) + 1
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
				if closed {
					// re-sanitize on read: a fence that reached the
					// body past the write path (legacy bodies, a
					// bypassed guard) is served sanitized too, so raw
					// markup never reaches the composer canvas
					leadSection()
					sec.Blocks = append(sec.Blocks, docBlock{Kind: "embed",
						Text:      delivery.SanitizeEmbed(strings.Join(inner, "\n")),
						spanStart: pos, spanEnd: cur + len(lines[li])})
					pos = cur + len(lines[li]) + 1
				} else {
					li = liStart
					pos += len(line) + 1
				}
				continue
			}
			blk, isBlock := parseBlockLine(t)
			if isBlock {
				leadSection()
				if blk.Kind == "text" && wasText && len(sec.Blocks) > 0 &&
					sec.Blocks[len(sec.Blocks)-1].Kind == "text" {
					// contiguous `> ` lines are one multi-paragraph
					// text block — a blank line splits them
					last := &sec.Blocks[len(sec.Blocks)-1]
					last.Text += "\n" + blk.Text
					last.spanEnd = pos + len(line)
					textRun = true
					pos += len(line) + 1
					continue
				}
				blk.spanStart, blk.spanEnd = pos, pos+len(line)
				sec.Blocks = append(sec.Blocks, blk)
				textRun = blk.Kind == "text"
			}
			pos += len(line) + 1
			continue
		}
		switch {
		case t == "", strings.HasPrefix(t, "#"), strings.HasPrefix(t, "**"),
			strings.HasPrefix(t, "-"), strings.HasPrefix(t, "_"):
			// headings, source lists, emphasis-only lines
		case strings.HasPrefix(t, "> "):
			addSentence(t, "interpretation")
		default:
			for _, piece := range pipeline.SplitSentences(t) {
				piece = strings.TrimSpace(piece)
				if !strings.ContainsFunc(piece, unicode.IsLetter) {
					continue
				}
				addSentence(piece, "fact")
			}
		}
		pos += len(line) + 1
	}
	finishItem(len(iss.BodyMD))
	if sec != nil {
		sec.spanEnd = len(iss.BodyMD)
	}
	// align each item block's span with its item — the item's end is
	// only known once finishItem ran for it.
	for si := range doc.Sections {
		sec := &doc.Sections[si]
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

	// sections with required commentary that carry no items still matter
	// for the readiness checklist — include them even when the body has
	// no section heading yet.
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

	for _, s := range doc.Sections {
		for _, it := range s.Items {
			for _, sn := range it.Sentences {
				if sn.Kind != "fact" {
					continue
				}
				doc.Coverage.Total++
				if len(sn.Citations) > 0 {
					doc.Coverage.Cited++
					if sn.Flag == nil {
						doc.Coverage.Ok++
					}
				}
			}
		}
	}
	doc.Coverage.Flagged = liveFlags
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

// truncateStr shortens s to n runes for excerpts (rune-safe: never
// splits a UTF-8 sequence).
func truncateStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
