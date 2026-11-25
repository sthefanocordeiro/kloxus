package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// --- document / sentence mutations / commentary ---

func (s *Server) apiDocument(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return
	}
	apiJSON(w, 200, map[string]any{"document": doc})
}

// apiSentenceCtx loads issue, document and locates a sentence.
func (s *Server) apiSentenceCtx(w http.ResponseWriter, r *http.Request) (*store.Issue, *issueDocument, *locatedSentence, bool) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return nil, nil, nil, false
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return nil, nil, nil, false
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return nil, nil, nil, false
	}
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return nil, nil, nil, false
	}
	loc := doc.findSentence(r.PathValue("sid"))
	if loc == nil {
		apiErr(w, 404, "not_found", "sentence not found")
		return nil, nil, nil, false
	}
	return iss, doc, loc, true
}

// dropFlag removes the flag matching (item ref, raw sentence); reports
// whether one was dropped.
func dropFlag(r *http.Request, iss *store.Issue, itemRef int64, raw string) (bool, error) {
	flags := iss.Flags
	out := flags[:0]
	dropped := false
	for _, f := range flags {
		if f.Item == itemRef && f.Sentence == raw {
			dropped = true
			continue
		}
		out = append(out, f)
	}
	if !dropped {
		return false, nil
	}
	return true, db(r).ResolveFlags(r.Context(), iss.ID, out)
}

// revertApproved returns an approved issue to drafted after an edit —
// which also invalidates the stored approval receipt.
func revertApproved(r *http.Request, iss *store.Issue) {
	if iss.Status == "approved" {
		db(r).RevertIssueToDraft(r.Context(), iss.ID)
		iss.Status = "drafted"
	}
}

// checkDocVersion enforces the document's optimistic-concurrency token:
// every document mutation must send back the version it read. A stale
// or missing version answers 409 conflict with the fresh document.
func checkDocVersion(w http.ResponseWriter, doc *issueDocument, v string) bool {
	if v != "" && v == doc.Version {
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(map[string]any{
		"error": apiError{Code: "conflict",
			Message: "the document changed since this version — reload and retry the edit"},
		"document": doc,
	})
	return false
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// apiSentenceEdit replaces a sentence's text. The client sends the
// display form — {"text": "…", "citations": [1]} — and the server
// composes the stored form (markers appended after the words). Fact
// sentences re-run the citation check: the refs must be sources of that
// item and the claim must stay supported — the flag (or the write) is
// refused otherwise. Interpretation sentences carry no citation
// requirement.
func (s *Server) apiSentenceEdit(w http.ResponseWriter, r *http.Request) {
	iss, doc, loc, ok := s.apiSentenceCtx(w, r)
	if !ok {
		return
	}
	var in struct {
		Text      string `json:"text"`
		Citations []int  `json:"citations"`
		Version   string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	text := displayText(in.Text) // the contract takes marker-free text
	if text == "" {
		apiErr(w, 400, "validation", "edited sentence cannot be empty",
			apiReason{Code: "empty_sentence", Message: "edited sentence cannot be empty",
				Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
		return
	}
	ctx := r.Context()
	var replacement string
	if loc.sentence.Kind == "interpretation" {
		replacement = "> _Interpretação: " + text + "_"
	} else {
		// fact: citations must point at this item's sources and the new
		// claim must stay supported by them
		d, _ := s.itemDossierByID(r, loc.item)
		maxRef := 0
		if d != nil {
			maxRef = len(d.Sources)
		}
		var bad string
		for _, n := range in.Citations {
			if n < 1 || (maxRef > 0 && n > maxRef) {
				bad = fmt.Sprintf("[%d]", n)
				break
			}
		}
		if bad != "" {
			apiErr(w, 409, "invalid_citation",
				fmt.Sprintf("source %s does not exist for this item — cite [1]..[%d] or mark as interpretation", bad, maxRef),
				apiReason{Code: "invalid_citation", Message: "citation ref out of range",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		if len(in.Citations) == 0 {
			apiErr(w, 409, "citation_required",
				"edited sentence needs a citation — or mark it as interpretation",
				apiReason{Code: "citation_required", Message: "factual sentences need a citation",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		if text == loc.sentence.Text && equalInts(in.Citations, loc.sentence.Citations) {
			apiErr(w, 409, "unchanged",
				"the sentence is unchanged — the flag stays until the claim is fixed, marked as interpretation, or removed",
				apiReason{Code: "unchanged", Message: "sentence text is unchanged",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		composed := text
		seen := map[int]bool{}
		for _, n := range in.Citations {
			if seen[n] {
				continue
			}
			seen[n] = true
			composed += fmt.Sprintf(" [%d]", n)
		}
		if !s.editSupportedFor(r, loc.item, int64(loc.item.Ref), composed) {
			apiErr(w, 409, "unsupported_claim",
				"the citation check still cannot verify this sentence against the cited source — the flag stays",
				apiReason{Code: "unsupported_claim", Message: "edit could not be verified against the cited source",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		replacement = composed
	}
	body, ok := replaceInItem(iss.BodyMD, loc.item, loc.sentence.raw, replacement)
	if !ok {
		apiErr(w, 409, "sentence_gone", "the sentence is no longer in the body")
		return
	}
	if apiDBErr(w, r, db(r).SetIssueBody(ctx, iss.ID, body), "issue") {
		return
	}
	dropped, err := dropFlag(r, iss, int64(loc.item.Ref), loc.sentence.raw)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	kind := "sentence_edit"
	if dropped {
		kind = "flag_edit"
	}
	db(r).RecordDecision(ctx, iss.PublicationID, loc.item.IssueItemID, kind,
		loc.sentence.raw+" → "+text)
	revertApproved(r, iss)
	s.respondSentence(w, r, iss, nil)
}

// apiSentenceRemove deletes a sentence (and its flag when present). The
// response carries the removed payload so the client can offer Undo —
// POST /issues/{id}/items/{ref}/sentences restores it verbatim.
func (s *Server) apiSentenceRemove(w http.ResponseWriter, r *http.Request) {
	iss, doc, loc, ok := s.apiSentenceCtx(w, r)
	if !ok {
		return
	}
	var in struct {
		Version string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	// capture the undo payload before the write: position inside the
	// item, display text, citations, kind and the flag it carried
	index := 0
	for i, sn := range loc.item.Sentences {
		if sn.ID == loc.sentence.ID {
			index = i
			break
		}
	}
	removed := map[string]any{
		"ref": loc.item.Ref, "index": index, "text": loc.sentence.Text,
		"kind": loc.sentence.Kind, "citations": orEmpty(loc.sentence.Citations),
	}
	if loc.sentence.Flag != nil {
		removed["flag"] = loc.sentence.Flag.Note
	}
	ctx := r.Context()
	body, ok := removeInItem(iss.BodyMD, loc.item, loc.sentence.raw)
	if !ok {
		apiErr(w, 409, "sentence_gone", "the sentence is no longer in the body")
		return
	}
	if apiDBErr(w, r, db(r).SetIssueBody(ctx, iss.ID, body), "issue") {
		return
	}
	dropped, err := dropFlag(r, iss, int64(loc.item.Ref), loc.sentence.raw)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	kind := "sentence_remove"
	if dropped {
		kind = "flag_remove"
	}
	db(r).RecordDecision(ctx, iss.PublicationID, loc.item.IssueItemID, kind, loc.sentence.raw)
	revertApproved(r, iss)
	s.respondSentence(w, r, iss, map[string]any{"removed": removed})
}

// apiSentenceRestore re-inserts a removed sentence into its item —
// the Undo for apiSentenceRemove. The client sends back the payload
// DELETE returned; the server composes the stored form at the same
// index inside the item's span and re-attaches the flag it carried.
func (s *Server) apiSentenceRestore(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return
	}
	ref, _ := strconv.Atoi(r.PathValue("ref"))
	var item *docItem
	for si := range doc.Sections {
		for ii := range doc.Sections[si].Items {
			if doc.Sections[si].Items[ii].Ref == ref {
				item = &doc.Sections[si].Items[ii]
			}
		}
	}
	if item == nil {
		apiErr(w, 404, "not_found", "item not found in the document")
		return
	}
	var in struct {
		Index     int    `json:"index"`
		Text      string `json:"text"`
		Kind      string `json:"kind"`
		Citations []int  `json:"citations"`
		Flag      string `json:"flag,omitempty"`
		Version   string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	text := displayText(in.Text)
	if text == "" {
		apiErr(w, 400, "validation", "restored sentence cannot be empty")
		return
	}
	var raw string
	if in.Kind == "interpretation" {
		raw = "> _Interpretação: " + text + "_"
	} else {
		d, _ := s.itemDossierByID(r, item)
		maxRef := 0
		if d != nil {
			maxRef = len(d.Sources)
		}
		raw = text
		seen := map[int]bool{}
		for _, n := range in.Citations {
			if n < 1 || (maxRef > 0 && n > maxRef) {
				apiErr(w, 409, "invalid_citation",
					fmt.Sprintf("source [%d] does not exist for this item", n))
				return
			}
			if seen[n] {
				continue
			}
			seen[n] = true
			raw += fmt.Sprintf(" [%d]", n)
		}
	}
	if in.Index < 0 {
		in.Index = 0
	}
	body, ok := insertInItem(iss.BodyMD, item, in.Index, raw)
	if !ok {
		apiErr(w, 409, "conflict", "the item no longer accepts this sentence")
		return
	}
	ctx := r.Context()
	if apiDBErr(w, r, db(r).SetIssueBody(ctx, iss.ID, body), "issue") {
		return
	}
	if in.Flag != "" {
		flags := append(append([]store.Flag{}, iss.Flags...),
			store.Flag{Item: int64(ref), Sentence: raw, Note: in.Flag})
		if apiDBErr(w, r, db(r).ResolveFlags(ctx, iss.ID, flags), "issue") {
			return
		}
	}
	db(r).RecordDecision(ctx, iss.PublicationID, item.IssueItemID, "sentence_restore", raw)
	revertApproved(r, iss)
	s.respondSentence(w, r, iss, nil)
}

// apiSentenceInterpretation marks a sentence as interpretation: it is
// stored as an editor-attributed block, so the citation check no longer
// applies — the flag, when present, is resolved.
func (s *Server) apiSentenceInterpretation(w http.ResponseWriter, r *http.Request) {
	iss, doc, loc, ok := s.apiSentenceCtx(w, r)
	if !ok {
		return
	}
	var in struct {
		Text    string `json:"text"`
		Version string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		text = displayText(loc.sentence.raw)
	}
	if text == "" {
		apiErr(w, 400, "validation", "interpretation text cannot be empty")
		return
	}
	ctx := r.Context()
	replacement := "\n> _Interpretação: " + text + "_\n"
	body, ok := replaceInItem(iss.BodyMD, loc.item, loc.sentence.raw, replacement)
	if !ok {
		apiErr(w, 409, "sentence_gone", "the sentence is no longer in the body")
		return
	}
	if apiDBErr(w, r, db(r).SetIssueBody(ctx, iss.ID, body), "issue") {
		return
	}
	dropped, err := dropFlag(r, iss, int64(loc.item.Ref), loc.sentence.raw)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	kind := "mark_interpretation"
	if dropped {
		kind = "flag_interpretation"
	}
	db(r).RecordDecision(ctx, iss.PublicationID, loc.item.IssueItemID, kind, loc.sentence.raw)
	revertApproved(r, iss)
	s.respondSentence(w, r, iss, nil)
}

// respondSentence re-parses the document after a mutation and answers
// with fresh coverage (and the mutated sentence when it still exists).
// extra keys (e.g. "removed" for undo) are merged into the response.
func (s *Server) respondSentence(w http.ResponseWriter, r *http.Request, iss *store.Issue, extra map[string]any) {
	fresh, err := db(r).GetIssue(r.Context(), iss.ID)
	if err == nil {
		iss = fresh
	}
	pub, _ := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return
	}
	resp := map[string]any{"coverage": doc.Coverage, "flags": len(iss.Flags), "version": doc.Version}
	if loc := doc.findSentence(r.PathValue("sid")); loc != nil {
		resp["sentence"] = loc.sentence
	}
	for k, v := range extra {
		resp[k] = v
	}
	apiJSON(w, 200, resp)
}

// apiCommentary saves the editor's commentary for a section. The AI
// never writes here — this is the only write path.
func (s *Server) apiCommentary(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	var in struct {
		Section string `json:"section"`
		Text    string `json:"text"`
		Version string `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if in.Version != docVersion(iss) {
		pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
		if apiDBErr(w, r, err, "publication") {
			return
		}
		doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
		if apiDBErr(w, r, err, "document") {
			return
		}
		checkDocVersion(w, doc, in.Version)
		return
	}
	if strings.TrimSpace(in.Section) == "" {
		apiErr(w, 400, "validation", "section is required")
		return
	}
	c := iss.Commentary
	if c == nil {
		c = map[string]string{}
	}
	c[in.Section] = in.Text
	if apiDBErr(w, r, db(r).SaveCommentary(r.Context(), iss.ID, c), "issue") {
		return
	}
	revertApproved(r, iss)
	// docVersion below must see the just-written commentary — when
	// iss.Commentary was nil, c is a fresh map that iss does not alias
	iss.Commentary = c
	apiJSON(w, 200, map[string]any{
		"commentary": map[string]string{"section": in.Section, "text": in.Text},
		"version":    docVersion(iss),
	})
}

// --- small helpers ---

// itemDossierByID loads the dossier for a document item.
func (s *Server) itemDossierByID(r *http.Request, item *docItem) (*pipeline.Dossier, error) {
	if item == nil || item.IssueItemID == 0 {
		return nil, sql.ErrNoRows
	}

	dj, err := db(r).Dossier(r.Context(), item.IssueItemID)
	if err != nil {
		return nil, err
	}
	var d pipeline.Dossier
	if err := json.Unmarshal([]byte(dj), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// editSupportedFor re-runs the citation check for an edited sentence
// against a document item's dossier.
func (s *Server) editSupportedFor(r *http.Request, item *docItem, itemRef int64, text string) bool {
	d, err := s.itemDossierByID(r, item)
	if err != nil || d == nil {
		return false
	}
	return s.dossierSupports(r, d, itemRef, text)
}
