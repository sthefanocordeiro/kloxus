package web

// Renditions API (KLOX-125): after "Concluir revisão" the author picks
// destinations; each chosen format forks a rendition of the approved
// piece through a fixed template (no model) that is then edited on its
// own. Kloxus never publishes — the author copies or downloads.
import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// renditionMax caps one rendition body (HTML with inline links).
const renditionMax = 1 << 20

type renditionView struct {
	store.Rendition
	// Stale is true when the piece's template output changed since the
	// rendition was forked or last refreshed — the UI then asks "atualizar
	// esta versão?" and never overwrites on its own.
	Stale bool `json:"stale"`
}

// rendDoc loads the issue + document and requires an approved piece:
// renditions are exports of reviewed text.
func (s *Server) rendDoc(w http.ResponseWriter, r *http.Request) (*store.Issue, *issueDocument, bool) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return nil, nil, false
	}
	if iss.Status != "approved" && iss.Status != "sent" {
		apiErr(w, 409, "blocked", "renditions ship only reviewed text — finish the review first",
			apiReason{Code: "not_approved", Message: "issue is not approved",
				Target: &apiTarget{Kind: "issue", Ref: strconv.FormatInt(iss.ID, 10)}})
		return nil, nil, false
	}
	return iss, doc, true
}

func renditionID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	if err != nil {
		apiErr(w, 400, "validation", "rendition id must be a number")
		return 0, false
	}
	return id, true
}

func staleView(rs []store.Rendition, doc *issueDocument) []renditionView {
	_, _, _, cur := renditionBodies(doc, FmtMarkdown)
	out := make([]renditionView, 0, len(rs))
	for _, x := range rs {
		out = append(out, renditionView{Rendition: x, Stale: x.BaseHash != cur})
	}
	return out
}

func storyCount(doc *issueDocument) int {
	n := 0
	for _, sec := range doc.Sections {
		n += len(sec.Items)
	}
	return n
}

// apiRenditions lists the piece's renditions.
func (s *Server) apiRenditions(w http.ResponseWriter, r *http.Request) {
	_, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	rs, err := db(r).Renditions(r.Context(), doc.IssueID)
	if apiDBErr(w, r, err, "renditions") {
		return
	}
	apiJSON(w, 200, map[string]any{"renditions": staleView(rs, doc), "stories": storyCount(doc)})
}

// apiRenditionCreate forks a rendition of the chosen format. A repeat
// fork returns the existing rendition untouched.
func (s *Server) apiRenditionCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Format string `json:"format"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !validRenditionFormat(in.Format) {
		apiErr(w, 400, "validation", "format must be linkedin, x, html or markdown")
		return
	}
	iss, doc, ok := s.rendDoc(w, r)
	if !ok {
		return
	}
	t, h, m, hash := renditionBodies(doc, in.Format)
	rd, err := db(r).CreateRendition(r.Context(), iss.ID, in.Format, t, h, m, hash)
	if apiDBErr(w, r, err, "rendition") {
		return
	}
	apiJSON(w, 201, map[string]any{"rendition": staleView([]store.Rendition{*rd}, doc)[0], "stories": storyCount(doc)})
}

// apiRenditionSave stores an author edit. It never reopens the review:
// the piece's status and flags are untouched.
func (s *Server) apiRenditionSave(w http.ResponseWriter, r *http.Request) {
	rid, ok := renditionID(w, r)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if len(in.Body) > renditionMax {
		apiErr(w, 413, "too_large", "rendition is too large")
		return
	}
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	cur, err := db(r).GetRendition(r.Context(), iss.ID, rid)
	if apiDBErr(w, r, err, "rendition") {
		return
	}
	t, h, m := cur.BodyText, cur.BodyHTML, cur.BodyMD
	switch cur.Format {
	case FmtLinkedIn, FmtX:
		t = in.Body
	case FmtHTML:
		h = in.Body
	case FmtMarkdown:
		m = in.Body
	}
	rd, err := db(r).UpdateRenditionBody(r.Context(), iss.ID, rid, t, h, m)
	if apiDBErr(w, r, err, "rendition") {
		return
	}
	apiJSON(w, 200, map[string]any{"rendition": staleView([]store.Rendition{*rd}, doc)[0]})
}

// apiRenditionRefresh re-forks from the piece, discarding the author's
// edits of that rendition — the UI confirms first.
func (s *Server) apiRenditionRefresh(w http.ResponseWriter, r *http.Request) {
	rid, ok := renditionID(w, r)
	if !ok {
		return
	}
	iss, doc, ok := s.rendDoc(w, r)
	if !ok {
		return
	}
	cur, err := db(r).GetRendition(r.Context(), iss.ID, rid)
	if apiDBErr(w, r, err, "rendition") {
		return
	}
	t, h, m, hash := renditionBodies(doc, cur.Format)
	rd, err := db(r).RefreshRendition(r.Context(), iss.ID, rid, t, h, m, hash)
	if apiDBErr(w, r, err, "rendition") {
		return
	}
	apiJSON(w, 200, map[string]any{"rendition": staleView([]store.Rendition{*rd}, doc)[0]})
}

// apiIssueExported records that the author copied or downloaded a
// format (KLOX-184): the piece reads "Exportada" in the lists and the
// formats that left are remembered. Kloxus never sees the paste — the
// mark is the author's own click.
func (s *Server) apiIssueExported(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	var in struct {
		Format string `json:"format"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !validRenditionFormat(in.Format) {
		apiErr(w, 400, "validation", "format must be linkedin, x, html or markdown")
		return
	}
	if iss.Status != "approved" && iss.Status != "sent" && iss.Status != "scheduled" {
		apiErr(w, 409, "blocked", "only a finished pauta can be exported",
			apiReason{Code: "not_approved", Message: "issue is not approved",
				Target: &apiTarget{Kind: "issue", Ref: strconv.FormatInt(iss.ID, 10)}})
		return
	}
	if apiDBErr(w, r, db(r).MarkIssueExport(r.Context(), iss.ID, in.Format), "export") {
		return
	}
	formats, err := db(r).IssueExports(r.Context(), iss.ID)
	if apiDBErr(w, r, err, "export") {
		return
	}
	apiJSON(w, 200, map[string]any{"exported": true, "formats": formats})
}

// apiRenditionDelete drops a rendition (the author unticks a destination).
func (s *Server) apiRenditionDelete(w http.ResponseWriter, r *http.Request) {
	rid, ok := renditionID(w, r)
	if !ok {
		return
	}
	iss, err := s.issue(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	err = db(r).DeleteRendition(r.Context(), iss.ID, rid)
	if err == sql.ErrNoRows {
		apiErr(w, 404, "not_found", "rendition not found")
		return
	}
	if apiDBErr(w, r, err, "rendition") {
		return
	}
	w.WriteHeader(204)
}
