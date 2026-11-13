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

// docSection is one section of the issue document.
type docSection struct {
	Title      string        `json:"title"`
	Commentary docCommentary `json:"commentary"`
	Items      []docItem     `json:"items"`
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
}

var (
	docItemRe    = regexp.MustCompile(`^### (.*?)\s*<!--\s*item\s+(\d+)\s*-->\s*$`)
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

// removeInItem deletes a sentence within its item's span, taking along
// any citation marker that trails it and tidying the leftover
// whitespace — an identical sentence in another item is never touched.
func removeInItem(body string, item *docItem, raw string) (string, bool) {
	if item.spanStart < 0 || item.spanEnd > len(body) || item.spanStart >= item.spanEnd {
		return body, false
	}
	seg := body[item.spanStart:item.spanEnd]
	re := regexp.MustCompile(regexp.QuoteMeta(raw) + `(?:\s*\[\d+\])*\s*[.!?]*`)
	var newSeg string
	if loc := re.FindStringIndex(seg); loc != nil {
		newSeg = seg[:loc[0]] + seg[loc[1]:]
	} else if i := strings.Index(seg, raw); i >= 0 {
		newSeg = seg[:i] + seg[i+len(raw):]
	} else {
		return body, false
	}
	newSeg = strings.ReplaceAll(newSeg, "  ", " ")
	for strings.Contains(newSeg, "\n\n\n") {
		newSeg = strings.ReplaceAll(newSeg, "\n\n\n", "\n\n")
	}
	// a sentence's terminal punctuation can survive as ". ." — collapse
	newSeg = strings.ReplaceAll(newSeg, ". .", ".")
	newSeg = strings.ReplaceAll(newSeg, " . ", " ")
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

// buildIssueDocument parses the stored issue body into the structured
// document. Flags are matched to sentences by (item ref, raw text);
// unmatched flags stay on their item.
func buildIssueDocument(ctx context.Context, d *store.DB, iss *store.Issue, pub *store.Publication) (*issueDocument, error) {
	if pub == nil {
		return nil, fmt.Errorf("publication %d not found", iss.PublicationID)
	}
	doc := &issueDocument{
		IssueID: iss.ID, Number: iss.Number, Status: iss.Status, Subject: iss.Subject,
		Version: docVersion(iss), Sections: []docSection{},
	}

	// item ref → issue item + dossier (the <!-- item N --> markers the
	// assembler writes equal dossier refs).
	type itemInfo struct {
		issueItemID int64
		dossier     *pipeline.Dossier
	}
	byRef := map[int]itemInfo{}
	if iis, err := d.IssueItems(ctx, iss.ID); err == nil {
		for _, ii := range iis {
			dj, err := d.Dossier(ctx, ii.ID)
			if err != nil {
				continue
			}
			var dd pipeline.Dossier
			if json.Unmarshal([]byte(dj), &dd) == nil {
				byRef[dd.Ref] = itemInfo{issueItemID: ii.ID, dossier: &dd}
			}
		}
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
	for _, sc := range pub.Sections {
		secMode[sc.Title] = sc.Commentary
	}
	commentary := iss.Commentary
	if commentary == nil {
		commentary = map[string]string{}
	}

	var sec *docSection
	var item *docItem
	usedIDs := map[string]int{}
	finishItem := func(end int) {
		if item == nil {
			return
		}
		item.spanEnd = end
		// attach flags that matched no sentence to the item itself
		for _, f := range flagOrder[int64(item.Ref)] {
			if _, still := flags[int64(item.Ref)][f.Sentence]; still {
				item.Flags = append(item.Flags, docItemFlag{Sentence: f.Sentence, Note: f.Note})
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
		}
		item.Sentences = append(item.Sentences, sn)
	}
	pos := 0 // byte offset of the current line inside the body
	for _, line := range strings.Split(iss.BodyMD, "\n") {
		t := strings.TrimSpace(line)
		if m := docSectionRe.FindStringSubmatch(t); m != nil {
			finishItem(pos)
			mode := secMode[m[1]]
			if mode == "" {
				mode = "none"
			}
			doc.Sections = append(doc.Sections, docSection{
				Title: m[1], Commentary: docCommentary{Mode: mode, Text: commentary[m[1]]},
				Items: []docItem{},
			})
			sec = &doc.Sections[len(doc.Sections)-1]
			pos += len(line) + 1
			continue
		}
		if m := docItemRe.FindStringSubmatch(t); m != nil {
			finishItem(pos)
			if sec == nil {
				doc.Sections = append(doc.Sections, docSection{Title: "", Commentary: docCommentary{Mode: "none"}, Items: []docItem{}})
				sec = &doc.Sections[len(doc.Sections)-1]
			}
			ref, _ := strconv.Atoi(m[2])
			it := docItem{Ref: ref, Headline: m[1], spanStart: pos, openings: map[int]string{},
				Sentences: []docSentence{}, Sources: []docSource{}}
			if info, ok := byRef[ref]; ok {
				it.IssueItemID = info.issueItemID
				it.dossier = info.dossier
				for _, ds := range info.dossier.Sources {
					src := docSource{Ref: ds.Ref, Name: ds.Name, URL: ds.URL, ItemID: ds.ItemID}
					if ds.ItemID != 0 {
						if stored, err := d.GetItem(ctx, ds.ItemID); err == nil {
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
				Title: sc.Title, Commentary: docCommentary{Mode: sc.Commentary, Text: commentary[sc.Title]},
				Items: []docItem{},
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
	doc.Coverage.Flagged = len(iss.Flags)
	return doc, nil
}

// insertInItem writes composed raw back into the item's span at the
// index-th sentence position (appended before the span end when idx is
// past the current count). Used by the sentence-restore endpoint.
func insertInItem(body string, item *docItem, idx int, raw string) (string, bool) {
	if item.spanStart < 0 || item.spanEnd > len(body) || item.spanStart >= item.spanEnd {
		return body, false
	}
	seg := body[item.spanStart:item.spanEnd]
	// find the byte offset inside seg where the idx-th sentence starts
	pos := 0
	if idx < len(item.Sentences) {
		target := item.Sentences[idx].raw
		for i := 0; i <= idx; i++ {
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
	}
	// raw is a block of whole lines; splice it in on line boundaries
	head := seg[:pos]
	if head != "" && !strings.HasSuffix(head, "\n") {
		raw = "\n" + raw
	}
	if pos < len(seg) && !strings.HasSuffix(raw, "\n") {
		raw += "\n"
	}
	return body[:item.spanStart] + head + raw + seg[pos:], true
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
