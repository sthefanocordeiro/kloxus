package web

// The structured issue document served by GET /api/v1/issues/{id}/document.
// It is parsed from the stored markdown body (sections → items →
// sentences) so the new interface never has to touch HTML or markdown:
// each sentence carries a stable-per-render id, its kind (fact |
// interpretation), its citation refs and its flag when the citation
// check could not verify it.
import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// docSentence is one node of the issue document.
type docSentence struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"` // display text, citations stripped
	Kind      string   `json:"kind"` // fact | interpretation
	Citations []int    `json:"citations"`
	Flag      *docFlag `json:"flag,omitempty"`
	raw       string   // exact substring of the stored body
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
	Excerpt string `json:"excerpt"` // supporting excerpt for the evidence pane
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

// docCoverage is the always-visible coverage summary.
type docCoverage struct {
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
	Sections []docSection `json:"sections"`
	Coverage docCoverage  `json:"coverage"`
}

var (
	docItemRe    = regexp.MustCompile(`^### (.*?)\s*<!--\s*item\s+(\d+)\s*-->\s*$`)
	docSectionRe = regexp.MustCompile(`^## (.+?)\s*$`)
	docCiteRe    = regexp.MustCompile(`\[(\d+)\]`)
)

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

// findSentence locates a document sentence by its "item.seq" id.
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

// buildIssueDocument parses the stored issue body into the structured
// document. Flags are matched to sentences by (item ref, raw text);
// unmatched flags stay on their item.
func buildIssueDocument(ctx context.Context, d *store.DB, iss *store.Issue, pub *store.Publication) (*issueDocument, error) {
	doc := &issueDocument{
		IssueID: iss.ID, Number: iss.Number, Status: iss.Status, Subject: iss.Subject,
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
	seq := 0
	finishItem := func() {
		if item == nil {
			return
		}
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
		seq++
		text := raw
		if kind == "interpretation" {
			text = interpretationText(raw)
		} else {
			text = displayText(raw)
		}
		sn := docSentence{
			ID: fmt.Sprintf("%d.%d", item.Ref, seq), Text: text, Kind: kind,
			Citations: citationRefs(raw), raw: raw,
		}
		if sn.Citations == nil {
			sn.Citations = []int{}
		}
		if f, ok := flags[int64(item.Ref)][raw]; ok {
			sn.Flag = &docFlag{Note: f.Note}
			delete(flags[int64(item.Ref)], raw)
		}
		item.Sentences = append(item.Sentences, sn)
	}
	for _, line := range strings.Split(iss.BodyMD, "\n") {
		t := strings.TrimSpace(line)
		if m := docSectionRe.FindStringSubmatch(t); m != nil {
			finishItem()
			mode := secMode[m[1]]
			if mode == "" {
				mode = "none"
			}
			doc.Sections = append(doc.Sections, docSection{
				Title: m[1], Commentary: docCommentary{Mode: mode, Text: commentary[m[1]]},
			})
			sec = &doc.Sections[len(doc.Sections)-1]
			continue
		}
		if m := docItemRe.FindStringSubmatch(t); m != nil {
			finishItem()
			if sec == nil {
				doc.Sections = append(doc.Sections, docSection{Title: "", Commentary: docCommentary{Mode: "none"}})
				sec = &doc.Sections[len(doc.Sections)-1]
			}
			ref, _ := strconv.Atoi(m[2])
			it := docItem{Ref: ref, Headline: m[1]}
			if info, ok := byRef[ref]; ok {
				it.IssueItemID = info.issueItemID
				for _, ds := range info.dossier.Sources {
					src := docSource{Ref: ds.Ref, Name: ds.Name, URL: ds.URL, ItemID: ds.ItemID}
					if ds.ItemID != 0 {
						if stored, err := d.GetItem(ctx, ds.ItemID); err == nil {
							src.Excerpt = truncateStr(stored.ContentText, 320)
							src.Date = stored.PublishedAt
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
			seq = 0
			continue
		}
		switch {
		case t == "", strings.HasPrefix(t, "#"), strings.HasPrefix(t, "**"),
			strings.HasPrefix(t, "-"), strings.HasPrefix(t, "_"):
			continue // headings, source lists, emphasis-only lines
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
	}
	finishItem()

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
				}
			}
		}
	}
	doc.Coverage.Flagged = len(iss.Flags)
	if doc.Sections == nil {
		doc.Sections = []docSection{}
	}
	return doc, nil
}

// truncateStr shortens s for excerpts.
func truncateStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
