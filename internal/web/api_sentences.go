package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func (s *Server) apiDocument(w http.ResponseWriter, r *http.Request) {
	_, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	apiJSON(w, 200, map[string]any{"document": doc})
}

// issueDoc loads the issue, its canonical publication and the parsed
// document in a single Postgres round-trip. Every remote-DB statement
// costs ~150 ms on the hosted app, so the issue→publication→dossiers→
// items chain this replaced could not meet the KLOX-47 latency targets.
func (s *Server) issueDoc(w http.ResponseWriter, r *http.Request) (*store.Issue, *issueDocument, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		apiDBErr(w, r, err, "issue")
		return nil, nil, false
	}
	b, err := db(r).IssueDocBundle(r.Context(), id)
	if apiDBErr(w, r, err, "issue") {
		return nil, nil, false
	}
	pub, err := s.canonicalPub(b.Pub, nil)
	if apiDBErr(w, r, err, "publication") {
		return nil, nil, false
	}
	return b.Issue, parseIssueDocument(b.Issue, pub, dcFromPairs(b.Pairs, b.Items)), true
}

// apiSentenceCtx loads issue, document and locates a sentence.
func (s *Server) apiSentenceCtx(w http.ResponseWriter, r *http.Request) (*store.Issue, *issueDocument, *locatedSentence, bool) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return nil, nil, nil, false
	}
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return nil, nil, nil, false
	}
	loc := doc.findSentence(r.PathValue("sid"))
	if loc == nil {
		apiErr(w, 404, "not_found", "sentence not found")
		return nil, nil, nil, false
	}
	return iss, doc, loc, true
}

// dropFlagRaw removes the flag matching (item ref, raw sentence) and
// returns the list the issue should carry after the write — identical
// to the current flags when nothing matched.
func dropFlagRaw(iss *store.Issue, itemRef int64, raw string) ([]store.Flag, bool) {
	out := iss.Flags[:0]
	dropped := false
	for _, f := range iss.Flags {
		if f.Item == itemRef && f.Sentence == raw {
			dropped = true
			continue
		}
		out = append(out, f)
	}
	return out, dropped
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
	if !authorTextOK(text) {
		apiErr(w, 400, "validation", "edited sentence must be a single line with no document markup")
		return
	}
	ctx := r.Context()
	var replacement string
	if loc.sentence.Kind == "interpretation" {
		// (KLOX-48/51) same guard as compose and mark-as-interpretation:
		// a claim carrying a number, a date or a quotation is a fact —
		// editing its text may not sneak it past the citation check.
		if pipeline.HasNumberDateOrQuote(text) {
			apiErr(w, 409, "interpretation_restricted",
				"a claim with a number, a date or a quotation cannot be marked as interpretation — cite a source instead",
				apiReason{Code: "interpretation_restricted", Message: "sentence contains a number, a date or a quotation",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		replacement = "> _Interpretação: " + text + "_"
	} else {
		// fact: citations must point at this item's sources and the new
		// claim must stay supported by them
		d := loc.item.dossier
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
		if !s.editSupportedFor(r, loc.item, doc.dctx, int64(loc.item.Ref), composed) {
			apiErr(w, 409, "unsupported_claim",
				"the citation check still cannot verify this sentence against the cited source — the flag stays",
				apiReason{Code: "unsupported_claim", Message: "edit could not be verified against the cited source",
					Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
			return
		}
		if s.tooCloseToSources(loc.item, doc.dctx, composed) {
			apiErr(w, 409, "too_close_to_source",
				"the sentence copies the source almost word for word — rewrite it in the editor's own words",
				apiReason{Code: "too_close_to_source", Message: "sentence copies a run of source words",
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
	flags, dropped := dropFlagRaw(iss, int64(loc.item.Ref), loc.sentence.raw)
	kind := "sentence_edit"
	if dropped {
		kind = "flag_edit"
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: loc.item.IssueItemID, Body: body,
		Flags: flags, Decision: kind, Reason: loc.sentence.raw + " → " + text,
		RevertApproved: iss.Status == store.IssueApproved,
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == store.IssueApproved {
		iss.Status = store.IssueDrafted
	}
	s.respondSentence(w, r, iss, doc, nil)
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
	flags, dropped := dropFlagRaw(iss, int64(loc.item.Ref), loc.sentence.raw)
	kind := "sentence_remove"
	if dropped {
		kind = "flag_remove"
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: loc.item.IssueItemID, Body: body,
		Flags: flags, Decision: kind, Reason: loc.sentence.raw,
		RevertApproved: iss.Status == store.IssueApproved,
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == store.IssueApproved {
		iss.Status = store.IssueDrafted
	}
	s.respondSentence(w, r, iss, doc, map[string]any{"removed": removed})
}

// restoreEntry is one removed sentence the client asks to put back:
// the undo payload DELETE (or the bulk flagged-removal) returned.
type restoreEntry struct {
	Index     int    `json:"index"`
	Text      string `json:"text"`
	Kind      string `json:"kind"`
	Citations []int  `json:"citations"`
	Flag      string `json:"flag,omitempty"`
}

// apiSentenceRestore re-inserts removed sentences into their item —
// the Undo for apiSentenceRemove and apiItemRemoveFlagged. The client
// sends back the payload(s) DELETE returned — one flat object, or
// {"sentences": [...]} for a batch; the server composes the stored
// form at each recorded index inside the item's span and re-attaches
// the flags they carried, all in one write.
func (s *Server) apiSentenceRestore(w http.ResponseWriter, r *http.Request) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	ref, _ := strconv.Atoi(r.PathValue("ref"))
	item := doc.findItem(ref)
	if item == nil {
		apiErr(w, 404, "not_found", "item not found in the document")
		return
	}
	var in struct {
		restoreEntry
		Sentences []restoreEntry `json:"sentences"`
		Version   string         `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if !checkDocVersion(w, doc, in.Version) {
		return
	}
	entries := in.Sentences
	if len(entries) == 0 {
		entries = []restoreEntry{in.restoreEntry}
	}
	// compose every entry's stored form first — a bad citation or an
	// empty text refuses the whole restore, never half of it
	maxRef := 0
	if item.dossier != nil {
		maxRef = len(item.dossier.Sources)
	}
	raws := make([]string, len(entries))
	for i, e := range entries {
		text := displayText(e.Text)
		if text == "" {
			apiErr(w, 400, "validation", "restored sentence cannot be empty")
			return
		}
		if !authorTextOK(text) {
			apiErr(w, 400, "validation", "restored sentence must be a single line with no document markup")
			return
		}
		if e.Kind == "interpretation" {
			raws[i] = "> _Interpretação: " + text + "_"
			continue
		}
		raw := text
		seen := map[int]bool{}
		for _, n := range e.Citations {
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
		raws[i] = raw
	}
	if item.spanStart < 0 || item.spanEnd > len(iss.BodyMD) || item.spanStart >= item.spanEnd {
		apiErr(w, 409, "conflict", "the item no longer accepts this sentence")
		return
	}
	// splice in ascending recorded index — each entry lands back at the
	// position it was removed from, in one body write
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return entries[order[a]].Index < entries[order[b]].Index })
	seg := iss.BodyMD[item.spanStart:item.spanEnd]
	cur := make([]string, len(item.Sentences))
	for i, sn := range item.Sentences {
		cur[i] = sn.raw
	}
	for _, oi := range order {
		idx := entries[oi].Index
		if idx < 0 {
			idx = 0
		}
		if idx > len(cur) {
			idx = len(cur)
		}
		seg = insertRaw(seg, cur, idx, raws[oi])
		cur = slices.Insert(cur, idx, raws[oi])
	}
	body := iss.BodyMD[:item.spanStart] + seg + iss.BodyMD[item.spanEnd:]
	ctx := r.Context()
	flags := append([]store.Flag{}, iss.Flags...)
	restored := 0
	for _, oi := range order {
		if entries[oi].Flag != "" {
			flags = append(flags, store.Flag{Item: int64(ref), Sentence: raws[oi], Note: entries[oi].Flag})
			restored++
		}
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: item.IssueItemID, Body: body, Flags: flags,
		Decision:       "sentence_restore",
		Reason:         fmt.Sprintf("%d %s restored to item %d", len(entries), plural(len(entries), "sentence", "sentences"), ref),
		RevertApproved: iss.Status == store.IssueApproved,
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == store.IssueApproved {
		iss.Status = store.IssueDrafted
	}
	s.respondSentence(w, r, iss, doc, nil)
}

// apiItemRemoveFlagged deletes every flagged sentence of one item in a
// single write — the editor's bulk action for a flagged-heavy item.
// The response carries the removed payload list so the client can offer
// Undo: POST /issues/{id}/items/{ref}/sentences restores them.
func (s *Server) apiItemRemoveFlagged(w http.ResponseWriter, r *http.Request) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	ref, _ := strconv.Atoi(r.PathValue("ref"))
	item := doc.findItem(ref)
	if item == nil {
		apiErr(w, 404, "not_found", "item not found in the document")
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
	if item.spanStart < 0 || item.spanEnd > len(iss.BodyMD) || item.spanStart >= item.spanEnd {
		apiErr(w, 409, "conflict", "the item is no longer in the body")
		return
	}
	// remove every flagged sentence inside the item's span — one write
	// for the whole batch, undo payload in document order
	seg := iss.BodyMD[item.spanStart:item.spanEnd]
	removedRaws := map[string]bool{}
	var removed []map[string]any
	for i := range item.Sentences {
		sn := &item.Sentences[i]
		if sn.Flag == nil {
			continue
		}
		ns, ok := removeRaw(seg, sn.raw)
		if !ok {
			continue
		}
		seg = ns
		removedRaws[sn.raw] = true
		entry := map[string]any{
			"ref": item.Ref, "index": i, "text": sn.Text,
			"kind": sn.Kind, "citations": orEmpty(sn.Citations),
			"flag": sn.Flag.Note,
		}
		removed = append(removed, entry)
	}
	if len(removed) == 0 {
		apiErr(w, 409, "nothing_flagged", "this item has no flagged sentences")
		return
	}
	body := iss.BodyMD[:item.spanStart] + seg + iss.BodyMD[item.spanEnd:]
	ctx := r.Context()
	kept := make([]store.Flag, 0, len(iss.Flags))
	for _, f := range iss.Flags {
		if f.Item == int64(item.Ref) && removedRaws[f.Sentence] {
			continue
		}
		kept = append(kept, f)
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: item.IssueItemID, Body: body, Flags: kept,
		Decision:       "flagged_bulk_remove",
		Reason:         fmt.Sprintf("%d flagged %s removed from item %d", len(removed), plural(len(removed), "sentence", "sentences"), item.Ref),
		RevertApproved: iss.Status == store.IssueApproved,
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = kept
	if iss.Status == store.IssueApproved {
		iss.Status = store.IssueDrafted
	}
	s.respondSentence(w, r, iss, doc, map[string]any{"removed": removed})
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
	if !authorTextOK(text) {
		apiErr(w, 400, "validation", "interpretation text must be a single line with no document markup")
		return
	}
	// (KLOX-48) a claim carrying a number, a date or a quotation is a
	// fact, not an opinion — it may not escape the citation check by
	// being relabeled; fix the claim or remove it.
	if pipeline.HasNumberDateOrQuote(loc.sentence.Text) || pipeline.HasNumberDateOrQuote(text) {
		apiErr(w, 409, "interpretation_restricted",
			"a claim with a number, a date or a quotation cannot be marked as interpretation — fix it or remove it",
			apiReason{Code: "interpretation_restricted", Message: "sentence contains a number, a date or a quotation",
				Target: &apiTarget{Kind: "sentence", Ref: loc.sentence.ID}})
		return
	}
	ctx := r.Context()
	replacement := "\n> _Interpretação: " + text + "_\n"
	body, ok := replaceInItem(iss.BodyMD, loc.item, loc.sentence.raw, replacement)
	if !ok {
		apiErr(w, 409, "sentence_gone", "the sentence is no longer in the body")
		return
	}
	flags, dropped := dropFlagRaw(iss, int64(loc.item.Ref), loc.sentence.raw)
	kind := "mark_interpretation"
	if dropped {
		kind = "flag_interpretation"
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: loc.item.IssueItemID, Body: body,
		Flags: flags, Decision: kind, Reason: loc.sentence.raw,
		RevertApproved: iss.Status == store.IssueApproved,
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == store.IssueApproved {
		iss.Status = store.IssueDrafted
	}
	s.respondSentence(w, r, iss, doc, nil)
}

// respondSentence answers a mutation with the post-write document
// state: fresh coverage and version, plus the mutated sentence when it
// still exists. The document re-parses in memory over the request's
// already-loaded context — the dossier/item queries happen once per
// request, not once per build (KLOX-47). extra keys (e.g. "removed"
// for undo) are merged into the response.
func (s *Server) respondSentence(w http.ResponseWriter, r *http.Request, iss *store.Issue, doc *issueDocument, extra map[string]any) {
	// no reload: callers update iss in memory after SaveIssueEdit, so the
	// re-parse sees the post-write state without a second round-trip
	doc = parseIssueDocument(iss, doc.pub, doc.dctx)
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
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
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
	c := map[string]string{}
	for section, text := range iss.Commentary {
		c[section] = text
	}
	c[in.Section] = in.Text
	err = db(r).WithTx(r.Context(), func(txdb *store.DB) error {
		if err := txdb.SaveCommentary(r.Context(), iss.ID, c); err != nil {
			return err
		}
		if iss.Status == store.IssueApproved {
			return txdb.RevertIssueToDraft(r.Context(), iss.ID)
		}
		return nil
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == store.IssueApproved {
		iss.Status = store.IssueDrafted
	}
	// docVersion below must see the just-written commentary.
	iss.Commentary = c
	apiJSON(w, 200, map[string]any{
		"commentary": map[string]string{"section": in.Section, "text": in.Text},
		"version":    docVersion(iss),
	})
}

// editSupportedFor re-runs the citation check for an edited sentence
// against a document item's dossier — already parsed in the document
// context, no second store lookup.
func (s *Server) editSupportedFor(r *http.Request, item *docItem, dc *docContext, itemRef int64, text string) bool {
	d := item.dossier
	if d == nil {
		return false
	}
	return s.dossierSupports(r, d, dc, itemRef, text)
}
