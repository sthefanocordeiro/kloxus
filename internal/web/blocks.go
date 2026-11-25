package web

// Block ops for the Escrever composer (KLOX-66): each section of the
// edition is an ordered list of blocks — pauta items plus author-made
// heading, divider, link-list and text blocks. Blocks persist inside
// the stored markdown body (`####`, `---`, `- [label](url)`, `> text`)
// so exports, previews and the sent email render them with no schema
// change. Every op carries the document version like a sentence edit
// and reverts an approved issue to drafted.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// parseBlockLine reads one stored line into a layout block — the
// inverse of blockLine. Returns isBlock=false for lines that are not
// blocks (blank, `##`/`#` headings, `**Sources:**`, emphasis marks).
func parseBlockLine(t string) (docBlock, bool) {
	switch {
	case t == "---":
		return docBlock{Kind: "divider"}, true
	case strings.HasPrefix(t, "#### "):
		return docBlock{Kind: "heading", Text: strings.TrimSpace(t[5:])}, true
	case strings.HasPrefix(t, "!["):
		if m := blockImageRe.FindStringSubmatch(t); m != nil {
			return docBlock{Kind: "image", Text: m[1], URL: m[2]}, true
		}
		return docBlock{}, false
	case strings.HasPrefix(t, "- ") && len(t) > 2:
		label, url := splitBlockLink(t[2:])
		return docBlock{Kind: "link", Text: label, URL: url}, true
	case strings.HasPrefix(t, "> "):
		return docBlock{Kind: "text", Text: strings.TrimSpace(t[2:])}, true
	case t == "", strings.HasPrefix(t, "#"), strings.HasPrefix(t, "**"),
		strings.HasPrefix(t, "-"), strings.HasPrefix(t, "_"),
		strings.HasPrefix(t, "<"):
		// `<!-- /item -->` boundaries, embed fence lines and stray
		// markup are structural, never blocks; blank lines, `#`/`##`
		// headings, sources lists, lone dashes, emphasis marks —
		// structural noise, not blocks
		return docBlock{}, false
	default:
		if strings.ContainsFunc(t, unicode.IsLetter) {
			// a plain paragraph at section level is an author text block
			return docBlock{Kind: "text", Text: t}, true
		}
		return docBlock{}, false
	}
}

var (
	blockLinkRe  = regexp.MustCompile(`^\[([^\]]*)\]\(([^)\s]+)\)\s*$`)
	blockImageRe = regexp.MustCompile(`^!\[([^\]]*)\]\(([^)\s]+)\)\s*$`)
)

// oneLine flattens feed-supplied strings (story titles, source names)
// to a single line — a newline inside a stored heading would break its
// `### … <!-- item N -->` marker and splice raw lines into the body.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// splitBlockLink unpacks "[label](url)" or a bare url.
func splitBlockLink(rest string) (label, url string) {
	rest = strings.TrimSpace(rest)
	if m := blockLinkRe.FindStringSubmatch(rest); m != nil {
		return m[1], m[2]
	}
	return rest, rest
}

// authorTextOK reports whether author-supplied text can be written
// into an item's stored body: a newline would splice a new stored
// line, and `<!--`/`<klox-embed` markup breaks out of the item into
// structural document syntax that reaches reader surfaces raw
// (KLOX-66 D3). The check is intentionally anywhere-in-text: every
// stored line is a substring of the submitted text.
func authorTextOK(text string) bool {
	return !strings.ContainsAny(text, "\r\n") &&
		!strings.Contains(text, "<!--") && !strings.Contains(text, "<klox-embed")
}

// blockLine renders a block's stored markdown line — it delegates to
// delivery.BlockLine so the composer ops and the newsletter-template
// materializer validate against the same rules (KLOX-72).
func blockLine(kind, text, url string) (string, bool) {
	return delivery.BlockLine(kind, text, url)
}

// endItemMarker closes an open item in the stored body — written by
// insert/move when a non-item block lands right after an item, since
// `> ` inside an item parses as an interpretation sentence.
const endItemMarker = "<!-- /item -->"

// needsEndItem reports whether a non-item block landing at pos in the
// block list sits right after an item — then it needs the marker to
// avoid folding into that item.
func needsEndItem(sec *docSection, pos int) bool {
	return pos > 0 && sec.Blocks[pos-1].Kind == "item"
}

// findSection returns the parsed section with that title.
func (doc *issueDocument) findSection(title string) *docSection {
	for i := range doc.Sections {
		if doc.Sections[i].Title == title {
			return &doc.Sections[i]
		}
	}
	return nil
}

var blankRun = regexp.MustCompile(`\n{3,}`)

// tidyBody collapses 3+ blank runs left behind by block splices.
func tidyBody(body string) string {
	return blankRun.ReplaceAllString(body, "\n\n")
}

// blockCtx loads the issue + parsed document and enforces the shared
// rules for a block mutation: not locked, section exists, version ok.
func (s *Server) blockCtx(w http.ResponseWriter, r *http.Request, version, section string) (*store.Issue, *issueDocument, *docSection, bool) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return nil, nil, nil, false
	}
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return nil, nil, nil, false
	}
	if !checkDocVersion(w, doc, version) {
		return nil, nil, nil, false
	}
	sec := doc.findSection(section)
	if sec == nil {
		apiErr(w, 404, "not_found", "section not found in this edition")
		return nil, nil, nil, false
	}
	return iss, doc, sec, true
}

// respondBlock answers a block mutation with the re-parsed document.
func (s *Server) respondBlock(w http.ResponseWriter, r *http.Request, iss *store.Issue, doc *issueDocument, extra map[string]any) {
	fresh := parseIssueDocument(iss, doc.pub, doc.dctx)
	resp := map[string]any{"document": fresh}
	for k, v := range extra {
		resp[k] = v
	}
	apiJSON(w, 200, resp)
}

// saveBlockBody persists a rewritten body — same edit path as sentence
// ops, so post-approval edits void the approval (KLOX-66 §8).
func (s *Server) saveBlockBody(w http.ResponseWriter, r *http.Request, iss *store.Issue, body string, flags []store.Flag) bool {
	if flags == nil {
		flags = iss.Flags
	}
	err := db(r).SaveIssueEdit(r.Context(), iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, Body: body, Flags: flags,
		Decision: "block_edit", Reason: "composer block op",
		RevertApproved: iss.Status == store.IssueApproved,
	})
	if apiDBErr(w, r, err, "issue") {
		return false
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == store.IssueApproved {
		iss.Status = store.IssueDrafted
	}
	return true
}

// apiBlockInsert appends a block inside a section: a pauta (story_id —
// the story joins the edition with a manual dossier), or a heading,
// divider, link or author-text line. position defaults to the end of
// the section.
func (s *Server) apiBlockInsert(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Section  string `json:"section"`
		Kind     string `json:"kind"` // item | heading | divider | link | text
		Text     string `json:"text"`
		URL      string `json:"url"`
		StoryID  int64  `json:"story_id"`
		Position *int   `json:"position"`
		Version  string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	iss, doc, sec, ok := s.blockCtx(w, r, in.Version, in.Section)
	if !ok {
		return
	}
	pos := len(sec.Blocks)
	if in.Position != nil {
		pos = *in.Position
	}
	if pos < 0 || pos > len(sec.Blocks) {
		apiErr(w, 400, "validation", "position is out of range for this section")
		return
	}
	var seg string
	if in.Kind == "item" {
		line, msg := s.insertItemBlock(r, iss, in.Section, in.StoryID)
		if msg != "" {
			apiErr(w, 409, "blocked", msg)
			return
		}
		seg = line
	} else {
		line, okv := blockLine(in.Kind, in.Text, in.URL)
		if !okv {
			apiErr(w, 400, "validation",
				"block needs a valid kind (item|heading|divider|link|image|embed|text) and content")
			return
		}
		seg = line
	}
	if in.Kind != "item" && needsEndItem(sec, pos) {
		seg = endItemMarker + "\n\n" + seg
	}
	if in.Kind == "item" && pos < len(sec.Blocks) && sec.Blocks[pos].Kind != "item" {
		// the block that follows the new item needs its own boundary —
		// without it that block folds into the item on re-parse
		seg += "\n\n" + endItemMarker
	}
	body := iss.BodyMD
	if sec.spanStart < 0 {
		// the snapshot section has no `## ` heading in the body yet —
		// append the section and the block at the end.
		body = strings.TrimRight(body, "\n") + "\n\n## " + in.Section + "\n\n" + seg + "\n"
	} else {
		off := sec.spanEnd
		if pos < len(sec.Blocks) {
			off = sec.Blocks[pos].spanStart
		}
		body = body[:off] + seg + "\n\n" + body[off:]
	}
	body = tidyBody(body)
	if !s.saveBlockBody(w, r, iss, body, nil) {
		return
	}
	s.respondBlock(w, r, iss, doc, nil)
}

// insertItemBlock puts a pauta into the edition: the issue item joins
// the lineup as approved/manual, gets a deterministic manual dossier
// (no model call), and returns the stored heading + sources lines.
func (s *Server) insertItemBlock(r *http.Request, iss *store.Issue, section string, storyID int64) (string, string) {
	ctx := r.Context()
	if storyID == 0 {
		return "", "story_id is required for a pauta block"
	}
	story, err := db(r).StoryWithItems(ctx, storyID)
	if err != nil || story.PublicationID != iss.PublicationID {
		return "", "story not found in this newsletter"
	}
	var ii *store.IssueItem
	if old, err := db(r).IssueItemByStory(ctx, iss.ID, storyID); err == nil {
		if old.Status == store.ItemPending || old.Status == store.ItemApproved {
			return "", "this story is already in the edition"
		}
		// a rejected story stays out — the board owns the undo path
		if old.Status == store.ItemRejected {
			return "", "this story was rejected — undo the rejection on the board first"
		}
		if err := db(r).SetIssueItemPlacement(ctx, old.ID, section, nextPosition(r, iss.ID, section), store.ItemApproved); err != nil {
			return "", "could not place the story"
		}
		ii = old
	} else {
		id, err := db(r).CreateIssueItem(ctx, store.IssueItem{
			IssueID: iss.ID, StoryID: storyID, Section: section,
			Status: store.ItemApproved, Origin: "manual",
			Position: nextPosition(r, iss.ID, section),
		})
		if err != nil {
			return "", "could not add the story"
		}
		ii = &store.IssueItem{ID: id, StoryID: storyID, Section: section}
	}
	if err := pipeline.EnsureManualDossiers(ctx, db(r), iss.ID); err != nil {
		return "", "could not prepare the story's sources"
	}
	var d pipeline.Dossier
	if dj, err := db(r).Dossier(ctx, ii.ID); err != nil || json.Unmarshal([]byte(dj), &d) != nil {
		return "", "the story has no reference material"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### %s <!-- item %d -->\n\n", oneLine(story.Title), d.Ref)
	if len(d.Sources) > 0 {
		b.WriteString("**Sources:** ")
		parts := []string{}
		for _, ds := range d.Sources {
			name := oneLine(ds.Name)
			if name == "" {
				name = "source"
			}
			parts = append(parts, fmt.Sprintf("[%d] [%s](/a/%d)", ds.Ref, name, ds.ItemID))
		}
		b.WriteString(strings.Join(parts, " · ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), ""
}

// nextPosition returns the next lineup position inside a section.
func nextPosition(r *http.Request, issueID int64, section string) int {
	max := 0
	if iis, err := db(r).IssueItems(r.Context(), issueID); err == nil {
		for _, ii := range iis {
			if ii.Section == section && ii.Position >= max {
				max = ii.Position + 1
			}
		}
	}
	return max
}

// apiBlockUpdate rewrites a heading, link or text block's content in
// place (pauta blocks have no editable content — their words are
// sentence edits).
func (s *Server) apiBlockUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Section string `json:"section"`
		Index   int    `json:"index"`
		Text    string `json:"text"`
		URL     string `json:"url"`
		Version string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	iss, doc, sec, ok := s.blockCtx(w, r, in.Version, in.Section)
	if !ok {
		return
	}
	if in.Index < 0 || in.Index >= len(sec.Blocks) {
		apiErr(w, 404, "not_found", "block not found in this section")
		return
	}
	blk := sec.Blocks[in.Index]
	switch blk.Kind {
	case "item", "divider":
		apiErr(w, 400, "validation", "this block kind has no editable content")
		return
	}
	line, okv := blockLine(blk.Kind, in.Text, in.URL)
	if !okv {
		apiErr(w, 400, "validation", "the block content is not valid")
		return
	}
	body := iss.BodyMD[:blk.spanStart] + line + iss.BodyMD[blk.spanEnd:]
	if !s.saveBlockBody(w, r, iss, body, nil) {
		return
	}
	s.respondBlock(w, r, iss, doc, nil)
}

// apiBlockMove reorders a block inside its section (from → to). An
// item block moves with its whole story body; the lineup position is
// display order on the board, so the stored order is the source of
// truth here.
func (s *Server) apiBlockMove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Section string `json:"section"`
		From    int    `json:"from"`
		To      int    `json:"to"`
		Version string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	iss, doc, sec, ok := s.blockCtx(w, r, in.Version, in.Section)
	if !ok {
		return
	}
	n := len(sec.Blocks)
	if in.From < 0 || in.From >= n || in.To < 0 || in.To >= n || in.From == in.To {
		apiErr(w, 400, "validation", "from/to must be different block positions in the section")
		return
	}
	blk := sec.Blocks[in.From]
	body := iss.BodyMD
	seg := strings.TrimRight(body[blk.spanStart:blk.spanEnd], "\n")
	if blk.Kind != "item" {
		// landing right after an item needs the boundary marker, or
		// the block folds back into that item on re-parse
		afterItem := (in.To < in.From && needsEndItem(sec, in.To)) ||
			(in.To > in.From && sec.Blocks[in.To].Kind == "item")
		if afterItem {
			seg = endItemMarker + "\n\n" + seg
		}
	} else {
		// a moved item landing right before a non-item block must end
		// with the marker, or that block folds into it on re-parse
		var next *docBlock
		if in.To < in.From {
			next = &sec.Blocks[in.To]
		} else if in.To+1 < n {
			next = &sec.Blocks[in.To+1]
		}
		if next != nil && next.Kind != "item" {
			seg += "\n\n" + endItemMarker
		}
	}
	// remove the block plus one separator it left behind
	cutEnd := blk.spanEnd
	for cutEnd < len(body) && body[cutEnd] == '\n' {
		cutEnd++
	}
	body2 := body[:blk.spanStart] + body[cutEnd:]
	var ins int
	if in.To < in.From {
		ins = sec.Blocks[in.To].spanStart
	} else {
		// after the target block; its span shifted by the cut length
		tgt := sec.Blocks[in.To]
		ins = tgt.spanEnd
		for ins < len(body) && body[ins] == '\n' {
			ins++
		}
		if ins > cutEnd {
			ins -= cutEnd - blk.spanStart
		}
	}
	body2 = tidyBody(body2[:ins] + seg + "\n\n" + body2[ins:])
	if !s.saveBlockBody(w, r, iss, body2, nil) {
		return
	}
	s.respondBlock(w, r, iss, doc, nil)
}

// apiBlockRemove deletes a block. Removing a pauta block also drops
// the story from the edition (status "removed" — it resurfaces in the
// suggestions, matching the board's remove path).
func (s *Server) apiBlockRemove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Section string `json:"section"`
		Index   int    `json:"index"`
		Version string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	iss, doc, sec, ok := s.blockCtx(w, r, in.Version, in.Section)
	if !ok {
		return
	}
	if in.Index < 0 || in.Index >= len(sec.Blocks) {
		apiErr(w, 404, "not_found", "block not found in this section")
		return
	}
	blk := sec.Blocks[in.Index]
	body := iss.BodyMD
	cutEnd := blk.spanEnd
	for cutEnd < len(body) && body[cutEnd] == '\n' {
		cutEnd++
	}
	start := blk.spanStart
	// also swallow a blank line left before the block so seams stay clean
	body = tidyBody(body[:start] + body[cutEnd:])
	var flags []store.Flag
	if blk.Kind == "item" {
		if it := doc.findItem(blk.Ref); it != nil && it.IssueItemID != 0 {
			if err := db(r).SetIssueItemStatus(r.Context(), it.IssueItemID, store.ItemRemoved); err != nil {
				apiDBErr(w, r, err, "issue")
				return
			}
		}
		// flags keyed to the removed item die with it
		flags = iss.Flags[:0]
		for _, f := range iss.Flags {
			if f.Item != int64(blk.Ref) {
				flags = append(flags, f)
			}
		}
	}
	if !s.saveBlockBody(w, r, iss, body, flags) {
		return
	}
	s.respondBlock(w, r, iss, doc, nil)
}

// approveDecisionReason is the marker the approval path stores — the
// issue id keeps "was this edition ever approved" per-issue across a
// later edition's own approvals (KLOX-66).
func approveDecisionReason(issueID int64) string {
	return fmt.Sprintf("issue %d approved", issueID)
}

// itemMarkerIndex returns the offset of an item heading's `<!--` marker
// inside its stored line, or -1 when the line is not an item heading.
// The docItemRe match locates it — a headline may itself carry a
// literal `<!--`, so the first comment in the line is not the marker
// (KLOX-73).
func itemMarkerIndex(head string) int {
	m := docItemRe.FindStringSubmatchIndex(head)
	if m == nil {
		return -1
	}
	// Group 1 ends where the marker's whitespace run starts; the first
	// '<' after it opens the trailing `<!-- item N -->`.
	i := m[3]
	for i < len(head) && head[i] != '<' {
		i++
	}
	return i
}

// apiItemLinkOnly toggles a pauta between written and link-only
// (KLOX-66 D2): the stored heading marker gains/loses its `link`
// flag. A link-only pauta ships the story link with no written text —
// readiness, citations and the writing prompts never pressure the
// author for one.
func (s *Server) apiItemLinkOnly(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LinkOnly bool   `json:"link_only"`
		Version  string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	ref, err := strconv.Atoi(r.PathValue("ref"))
	if err != nil {
		apiErr(w, 400, "validation", "item ref must be a number")
		return
	}
	it := doc.findItem(ref)
	if it == nil {
		apiErr(w, 404, "not_found", "pauta not found in this edition")
		return
	}
	if it.LinkOnly == in.LinkOnly {
		s.respondBlock(w, r, iss, doc, nil)
		return
	}
	body := iss.BodyMD
	lineEnd := strings.IndexByte(body[it.spanStart:], '\n')
	if lineEnd < 0 {
		lineEnd = len(body)
	} else {
		lineEnd += it.spanStart
	}
	head := body[it.spanStart:lineEnd]
	mi := itemMarkerIndex(head)
	if mi < 0 {
		apiErr(w, 409, "blocked", "could not locate the pauta marker")
		return
	}
	marker := fmt.Sprintf("<!-- item %d -->", ref)
	if in.LinkOnly {
		marker = fmt.Sprintf("<!-- item %d link -->", ref)
	}
	head = head[:mi] + marker
	if !s.saveBlockBody(w, r, iss, body[:it.spanStart]+head+body[lineEnd:], nil) {
		return
	}
	s.respondBlock(w, r, iss, doc, nil)
}
