package web

// KLOX-168 — the main flow: tick stories on "Notícias de hoje", write a
// free-text note under each one, go to export. The author's text is
// stored as the issue body (one cited line per text line) so documents,
// renditions and exports keep working on the existing model; no
// checks, no approval gate — the author only gets a heads-up about
// stories left without text.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const (
	pautaMaxStories = 50
	pautaMaxText    = 8000
	// the pauta name doubles as mail subject and public page heading
	pautaMaxTitle = 200
)

type pautaEntryIn struct {
	StoryID int64  `json:"story_id"`
	Text    string `json:"text"`
}

type pautaIn struct {
	Opening string         `json:"opening"`
	Entries []pautaEntryIn `json:"entries"`
	// nil leaves the pauta name alone; "" restores the automatic
	// "first story title" name; anything else is the author's name
	// (KLOX-199)
	Title *string `json:"title"`
}

type pautaEntryView struct {
	StoryID     int64  `json:"story_id"`
	IssueItemID int64  `json:"issue_item_id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Outlet      string `json:"outlet"`
	Date        string `json:"date"`
	Text        string `json:"text"`
}

var pautaCiteRe = regexp.MustCompile(`\[(\d+)\]`)

// pautaLines turns the author's free text into body lines: one per text
// line, with leading markdown markers and citation-looking brackets
// defused so the parser reads plain sentences. Blank lines stay — they
// are the author's paragraph breaks and the document's text field reads
// them back verbatim; only the padding around the text is dropped.
func pautaLines(text string) []string {
	var out []string
	for _, ln := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		ln = strings.ReplaceAll(ln, "<!--", "")
		ln = strings.ReplaceAll(ln, "<klox-embed", "")
		ln = pautaCiteRe.ReplaceAllString(ln, "($1)")
		ln = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(ln), "#>-*+ \t"))
		out = append(out, ln)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// savePauta rewrites the issue to exactly these ordered entries. It
// returns an HTTP status and message, or 0 on success.
func (s *Server) savePauta(ctx context.Context, d *store.DB, iss *store.Issue, pub *store.Publication, in pautaIn) (int, string) {
	if len(in.Entries) > pautaMaxStories {
		return 400, "too many stories"
	}
	if len(in.Opening) > pautaMaxText {
		return 400, "opening is too long"
	}
	if in.Title != nil && len(*in.Title) > pautaMaxTitle {
		return 400, "title is too long"
	}
	secs := issueSections(iss, pub)
	if len(secs) == 0 {
		return 409, "the radar has no section to hold the pauta"
	}
	section := secs[0].Title
	existing, err := d.IssueItems(ctx, iss.ID)
	if err != nil {
		return 500, "could not load the pauta"
	}
	byStory := map[int64]store.IssueItem{}
	for _, ii := range existing {
		byStory[ii.StoryID] = ii
	}
	seen := map[int64]bool{}
	for i, e := range in.Entries {
		if e.StoryID == 0 || seen[e.StoryID] {
			return 400, "each story must appear once"
		}
		if len(e.Text) > pautaMaxText {
			return 400, "text is too long"
		}
		seen[e.StoryID] = true
		if old, ok := byStory[e.StoryID]; ok {
			if err := d.SetIssueItemPlacement(ctx, old.ID, section, i, "approved"); err != nil {
				return 500, "could not reorder the pauta"
			}
			continue
		}
		story, err := d.StoryWithItems(ctx, e.StoryID)
		if err != nil || story.PublicationID != pub.ID {
			return 404, "story not found — reload today's news"
		}
		if _, err := d.CreateIssueItem(ctx, store.IssueItem{
			IssueID: iss.ID, StoryID: e.StoryID, Section: section,
			Score: story.TriageScore, Reason: story.TriageReason,
			Status: "approved", Origin: "manual", Position: i,
		}); err != nil {
			return 500, "could not add the story"
		}
	}
	for _, ii := range existing {
		if !seen[ii.StoryID] && ii.Status != "removed" {
			if err := d.SetIssueItemStatus(ctx, ii.ID, "removed"); err != nil {
				return 500, "could not remove the story"
			}
		}
	}

	comm := iss.Commentary
	if comm == nil {
		comm = map[string]string{}
	}
	comm[section] = strings.TrimSpace(in.Opening)
	if err := d.SaveCommentary(ctx, iss.ID, comm); err != nil {
		return 500, "could not save the opening"
	}
	iss.Commentary = comm

	// KLOX-199: the name lives on the issue subject — an empty title
	// restores the placeholder so the pauta goes back to being named
	// by its first story; the exported text never reads the subject.
	if in.Title != nil {
		subject := strings.TrimSpace(*in.Title)
		if subject == "" {
			subject = fmt.Sprintf("%s — pauta %d", pub.Name, iss.Number)
		}
		if subject != iss.Subject {
			if err := d.SetIssueSubject(ctx, iss.ID, subject); err != nil {
				return 500, "could not save the name"
			}
			iss.Subject = subject
		}
	}

	if len(in.Entries) == 0 {
		if err := d.UpdateIssue(ctx, iss.ID, "drafted", fmt.Sprintf("# %s\n\n", iss.Subject), "[]"); err != nil {
			return 500, "could not save the pauta"
		}
		return 0, ""
	}
	runner := &pipeline.Runner{DB: d}
	if _, err := runner.ComposeSkeleton(ctx, pubWithIssueSections(iss, pub), iss.ID); err != nil {
		return 409, err.Error()
	}
	fresh, err := d.GetIssue(ctx, iss.ID)
	if err != nil {
		return 500, "could not load the pauta"
	}
	doc, err := buildIssueDocument(ctx, d, fresh, pub)
	if err != nil {
		return 500, "could not build the pauta"
	}
	items, _ := d.IssueItems(ctx, iss.ID)
	itemOf := map[int64]store.IssueItem{}
	for _, ii := range items {
		itemOf[ii.StoryID] = ii
	}
	body := fresh.BodyMD
	for _, e := range in.Entries {
		ii := itemOf[e.StoryID]
		var di *docItem
		for si := range doc.Sections {
			for k := range doc.Sections[si].Items {
				if doc.Sections[si].Items[k].IssueItemID == ii.ID {
					di = &doc.Sections[si].Items[k]
				}
			}
		}
		lines := pautaLines(e.Text)
		if di == nil || len(lines) == 0 {
			continue
		}
		cite := ""
		if len(di.Sources) > 0 {
			cite = " [1]"
		}
		for k := range lines {
			if lines[k] != "" {
				lines[k] += cite
			}
		}
		marker := fmt.Sprintf("<!-- item %d -->\n\n", di.Ref)
		body = strings.Replace(body, marker, marker+strings.Join(lines, "\n")+"\n\n", 1)
	}
	if err := d.UpdateIssue(ctx, iss.ID, "drafted", body, "[]"); err != nil {
		return 500, "could not save the pauta"
	}
	return 0, ""
}

// pautaView reads the issue back as ordered entries with free text.
func (s *Server) pautaView(ctx context.Context, d *store.DB, iss *store.Issue, pub *store.Publication) (map[string]any, []pautaEntryView, error) {
	doc, err := buildIssueDocument(ctx, d, iss, pub)
	if err != nil {
		return nil, nil, err
	}
	items, err := d.IssueItems(ctx, iss.ID)
	if err != nil {
		return nil, nil, err
	}
	storyOf := map[int64]int64{}
	for _, ii := range items {
		if ii.Status != "removed" && ii.Status != "rejected" {
			storyOf[ii.ID] = ii.StoryID
		}
	}
	entries := []pautaEntryView{}
	opening := ""
	for _, sec := range doc.Sections {
		if opening == "" {
			opening = sec.Commentary.Text
		}
		for _, b := range sec.Blocks {
			if b.Kind != "item" {
				continue
			}
			it := doc.findItem(b.Ref)
			if it == nil {
				continue
			}
			sid, ok := storyOf[it.IssueItemID]
			if !ok {
				continue
			}
			v := pautaEntryView{StoryID: sid, IssueItemID: it.IssueItemID, Title: it.Headline}
			if len(it.Sources) > 0 {
				v.URL, v.Outlet, v.Date = it.Sources[0].URL, it.Sources[0].Name, it.Sources[0].Date
			}
			v.Text = it.Text
			entries = append(entries, v)
		}
	}
	return map[string]any{
		"issue_id": iss.ID, "number": iss.Number, "status": iss.Status,
		"opening": opening, "entries": entries,
		// KLOX-199: the editor shows the same name the lists do
		"title": s.pautaTitle(ctx, d, iss, pub, items),
	}, entries, nil
}

// apiPautaCreate is "Montar pauta →": a new pauta holding the ticked
// stories in the order they were ticked.
func (s *Server) apiPautaCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pub, err := db(r).GetPublication(ctx, r.PathValue("id"))
	if apiDBErr(w, r, err, "radar") {
		return
	}
	var req struct {
		StoryIDs []int64 `json:"story_ids"`
	}
	if _, err := apiDecode(r, &req); err != nil || len(req.StoryIDs) == 0 {
		apiErr(w, 400, "validation", "story_ids is required")
		return
	}
	number, err := db(r).NextIssueNumber(ctx, pub.ID)
	if apiDBErr(w, r, err, "pauta") {
		return
	}
	id, err := db(r).CreateIssue(ctx, pub.ID, number,
		fmt.Sprintf("%s — pauta %d", pub.Name, number), "", config.PostSections())
	if apiDBErr(w, r, err, "pauta") {
		return
	}
	iss, err := db(r).GetIssue(ctx, id)
	if apiDBErr(w, r, err, "pauta") {
		return
	}
	var in pautaIn
	for _, sid := range req.StoryIDs {
		in.Entries = append(in.Entries, pautaEntryIn{StoryID: sid})
	}
	if code, msg := s.savePauta(ctx, db(r), iss, pub, in); code != 0 {
		apiErr(w, code, "pauta", msg)
		return
	}
	dropPautaDay(r, db(r), pub.ID) // picked stories leave today's list
	apiJSON(w, 201, map[string]any{"issue_id": id})
}

func (s *Server) pautaIssue(w http.ResponseWriter, r *http.Request) (*store.Issue, *store.Publication, bool) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "pauta") {
		return nil, nil, false
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "radar") {
		return nil, nil, false
	}
	return iss, pub, true
}

func (s *Server) apiPautaGet(w http.ResponseWriter, r *http.Request) {
	iss, pub, ok := s.pautaIssue(w, r)
	if !ok {
		return
	}
	v, _, err := s.pautaView(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "pauta") {
		return
	}
	apiJSON(w, 200, v)
}

func (s *Server) apiPautaSave(w http.ResponseWriter, r *http.Request) {
	iss, pub, ok := s.pautaIssue(w, r)
	if !ok {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this pauta is already sent — it is read-only")
		return
	}
	var in pautaIn
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if code, msg := s.savePauta(r.Context(), db(r), iss, pub, in); code != 0 {
		apiErr(w, code, "pauta", msg)
		return
	}
	apiJSON(w, 200, map[string]any{"saved": true})
}

// apiPautaFinish is "Ir para exportar": no checks. The pauta is marked
// ready so the export step opens, and the answer says how many stories
// still have no text so the page can warn — never block.
func (s *Server) apiPautaFinish(w http.ResponseWriter, r *http.Request) {
	iss, pub, ok := s.pautaIssue(w, r)
	if !ok {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiJSON(w, 200, map[string]any{"ready": true, "missing_text": 0})
		return
	}
	_, entries, err := s.pautaView(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "pauta") {
		return
	}
	missing := 0
	for _, e := range entries {
		if strings.TrimSpace(e.Text) == "" {
			missing++
		}
	}
	if iss.Status != "approved" {
		if apiDBErr(w, r, db(r).UpdateIssue(r.Context(), iss.ID, "approved", iss.BodyMD, "[]"), "pauta") {
			return
		}
	}
	apiJSON(w, 200, map[string]any{"ready": true, "missing_text": missing})
}
