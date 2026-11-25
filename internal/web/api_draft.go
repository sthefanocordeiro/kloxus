package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// apiCheck is one readiness-check entry.
type apiCheck struct {
	Code    string     `json:"code"`
	OK      bool       `json:"ok"`
	Count   int        `json:"count,omitempty"` // failing items, so clients can localize
	Message string     `json:"message,omitempty"`
	Target  *apiTarget `json:"target,omitempty"`
}

// readinessChecks builds the checklist (KLOX-157): a post is the list of
// stories the author picked, so review only asks that every factual
// sentence is sourced, nothing is flagged and no picked story is still
// empty. Undecided candidates, open slots and commentary never block.
func (s *Server) readinessChecks(r *http.Request, iss *store.Issue, pub *store.Publication, doc *issueDocument) []apiCheck {
	checks := []apiCheck{}
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
		checks = append(checks, apiCheck{Code: "read_only", OK: false,
			Message: "this issue is sent or scheduled — locked issues are read-only"})
	}
	if doc == nil {
		doc, _ = buildIssueDocument(r.Context(), db(r), iss, pub)
	}

	uncited, firstUncited := 0, ""
	for _, sec := range doc.Sections {
		if sec.Format == config.SectionLinks {
			// a links item's source is the link itself — no written
			// sentence, citation or flag gate applies (KLOX-65)
			continue
		}
		for _, it := range sec.Items {
			if it.LinkOnly {
				// same exemption per-item: a link-only pauta ships the
				// story link with zero written text (KLOX-66 D2)
				continue
			}
			for _, sn := range it.Sentences {
				if sn.Kind == "fact" && len(sn.Citations) == 0 {
					uncited++
					if firstUncited == "" {
						firstUncited = sn.ID
					}
				}
			}
		}
	}
	c := apiCheck{Code: "uncited_sentences", OK: uncited == 0, Count: uncited,
		Message: fmt.Sprintf("%d %s without a citation", uncited, plural(uncited, "sentence", "sentences"))}
	if !c.OK {
		c.Target = &apiTarget{Kind: "sentence", Ref: firstUncited}
	}
	checks = append(checks, c)

	// coverage.flagged counts live flags only — a stored flag matching
	// no sentence gives the editor nothing to act on, so it must not
	// keep blocking approval (KLOX-52)
	live := doc.Coverage.Flagged
	c = apiCheck{Code: "flags_unresolved", OK: live == 0, Count: live,
		Message: fmt.Sprintf("%d flagged %s unresolved — edit, mark as interpretation or remove each", live, plural(live, "sentence", "sentences"))}
	if !c.OK {
		// point at the first flagged sentence we can locate
		ref := ""
	search:
		for _, sec := range doc.Sections {
			if sec.Format == config.SectionLinks {
				continue
			}
			for _, it := range sec.Items {
				if it.LinkOnly {
					continue
				}
				for _, sn := range it.Sentences {
					if sn.Flag != nil {
						ref = sn.ID
						break search
					}
				}
			}
		}
		c.Target = &apiTarget{Kind: "sentence", Ref: ref}
	}
	checks = append(checks, c)

	// manual compose leaves item slots empty until the editor writes
	// them — an empty item blocks approval exactly like an uncited fact
	empty, firstEmpty := 0, ""
	for _, sec := range doc.Sections {
		if sec.Format == config.SectionLinks {
			continue
		}
		for _, it := range sec.Items {
			if it.LinkOnly {
				continue
			}
			if len(it.Sentences) == 0 {
				empty++
				if firstEmpty == "" {
					firstEmpty = fmt.Sprint(it.Ref)
				}
			}
		}
	}
	c = apiCheck{Code: "empty_items", OK: empty == 0, Count: empty,
		Message: fmt.Sprintf("%d %s still empty — write each item's text", empty, plural(empty, "item", "items"))}
	if !c.OK {
		c.Target = &apiTarget{Kind: "item", Ref: firstEmpty}
	}
	checks = append(checks, c)

	return checks
}

func (s *Server) apiReadiness(w http.ResponseWriter, r *http.Request) {
	iss, doc, ok := s.issueDoc(w, r)
	if !ok {
		return
	}
	checks := s.readinessChecks(r, iss, doc.pub, doc)
	ready := true
	for _, c := range checks {
		if !c.OK {
			ready = false
		}
	}
	apiJSON(w, 200, map[string]any{
		"ready": ready, "checks": checks, "coverage": doc.Coverage,
	})
}

// apiDraft starts Phase B (draft + citecheck) as a run: GET
// /api/v1/runs/{id}/events streams draft/citecheck steps, then the
// issue document is ready.
func (s *Server) apiDraft(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	// a post is the stories the author picked — no section slots (KLOX-157)
	lineup, _ := db(r).IssueItems(r.Context(), iss.ID)
	picked := 0
	for _, ii := range lineup {
		if ii.Status == store.ItemPending || ii.Status == store.ItemApproved {
			picked++
		}
	}
	if picked == 0 {
		apiErr(w, 409, "blocked", "pick at least one story before drafting",
			apiReason{Code: "no_stories", Message: "the post has no stories"})
		return
	}
	for _, ii := range lineup {
		if ii.Status == store.ItemPending {
			if apiDBErr(w, r, db(r).SetIssueItemStatus(r.Context(), ii.ID, store.ItemApproved), "item") {
				return
			}
		}
	}
	wsp := reqWS(r)
	st := &runProgress{st: runState{RunID: newReqID(), Kind: "draft", Step: "starting", IssueID: iss.ID, PubID: iss.PublicationID}}
	key := "draft-" + fmt.Sprint(iss.ID)
	if prev, running := wsp.Runs.LoadOrStore(key, st); running {
		pst := prev.(*runProgress)
		pst.mu.Lock()
		done := pst.st.Done
		pst.mu.Unlock()
		if !done {
			apiJSON(w, 202, map[string]any{"run": runView(pst)})
			return
		}
		wsp.Runs.Store(key, st)
	}
	// Same spend-guard admission as prepare runs: a refusal comes back
	// as a finished, blocked run with the message and recorded sample.
	if b := s.admitRun(r.Context(), wsp.ID); b != nil {
		st.blockRun(b)
		apiJSON(w, 202, map[string]any{"run": runView(st)})
		return
	}
	go func() {
		wsp.Activity.RLock()
		defer wsp.Activity.RUnlock()
		// Same panic guard as prepare runs (review F5).
		defer func() {
			if p := recover(); p != nil {
				st.mu.Lock()
				defer st.mu.Unlock()
				st.st.Done = true
				st.st.Err = fmt.Sprintf("pipeline panic: %v", p)
				slog.Error("draft run panicked", "issue", iss.ID, "panic", p, "rid", st.st.RunID)
			}
		}()
		runner := &pipeline.Runner{
			DB: wsp.DB, LLM: s.d.LLM, Fetcher: s.d.Fetcher,
			TriageModel: s.d.Cfg.ModelTriage, DraftModel: s.d.Cfg.ModelDraft,
			MaxDeepDives:  s.d.Cfg.MaxDeepDives,
			PaceStep:      s.stepPacer(),
			Deterministic: s.d.Cfg.LLMOff(),
			OnStep: func(step string) {
				st.mu.Lock()
				st.st.Step = step
				st.mu.Unlock()
			},
			OnEvent: func(ev pipeline.StepEvent) {
				st.mu.Lock()
				st.events = append(st.events, ev)
				st.mu.Unlock()
			},
		}
		ctx, cancel := context.WithTimeout(llm.WithRunScope(context.Background(), wsp.ID), s.runTimeout())
		defer cancel()
		res, err := runner.RunPhaseB(ctx, pubWithIssueSections(iss, pub), iss.ID)
		st.mu.Lock()
		defer st.mu.Unlock()
		st.st.Done = true
		if err != nil {
			st.st.Err = err.Error()
			if b, ok := llm.AsBlocked(err); ok {
				st.st.Blocked = true
				st.st.BlockedReason = string(b.Reason)
				st.st.Sample = true
			}
			return
		}
		st.st.IssueID = res.IssueID
	}()
	apiJSON(w, 202, map[string]any{"run": runView(st)})
}

// apiCompose writes the manual-compose skeleton: the board's lineup
// becomes the issue document with empty item slots — headlines and
// source lists only, no model-written text. Pending lineup items are
// approved as-is. The whole path is deterministic: items missing a
// dossier get one built from their collected items, and the endpoint
// never touches the LLM provider.
func (s *Server) apiCompose(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	// compose is for empty issues: it must never overwrite written work
	doc, err := buildIssueDocument(r.Context(), db(r), iss, pub)
	if apiDBErr(w, r, err, "document") {
		return
	}
	written := 0
	for _, sec := range doc.Sections {
		for _, it := range sec.Items {
			written += len(it.Sentences)
		}
	}
	if written > 0 {
		apiErr(w, 409, "not_empty", "this issue already has written content — compose is only for empty issues")
		return
	}
	// same gate as drafting: at least one picked story (KLOX-157)
	lineup, _ := db(r).IssueItems(r.Context(), iss.ID)
	picked := 0
	for _, ii := range lineup {
		if ii.Status == store.ItemPending || ii.Status == store.ItemApproved {
			picked++
		}
	}
	if picked == 0 {
		apiErr(w, 409, "blocked", "pick at least one story before composing",
			apiReason{Code: "no_stories", Message: "the post has no stories"})
		return
	}
	ctx := r.Context()
	for _, ii := range lineup {
		if ii.Status == store.ItemPending {
			if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, ii.ID, store.ItemApproved), "item") {
				return
			}
		}
	}
	runner := &pipeline.Runner{DB: db(r)}
	// AI on (real mode): each pauta arrives prefilled from the shared
	// research cache — one Haiku call per new story, capped and
	// degrading to the manual slot (KLOX-67 §7.3). Demo replay and
	// LLM-off keep the deterministic, model-free skeleton untouched.
	if s.d.Pool != nil && s.d.Cfg.Mode != "demo" && !s.d.Cfg.LLMOff() {
		runner.Research = &pipeline.Researcher{
			Pool: s.d.Pool, LLM: s.d.LLM, Model: s.d.Cfg.ModelTriage,
			PaidActionsPerDay: s.d.Cfg.PaidActionsPerDay,
		}
	}
	res, err := runner.ComposeSkeleton(ctx, pubWithIssueSections(iss, pub), iss.ID)
	if err != nil {
		apiErr(w, 409, "blocked", err.Error())
		return
	}
	db(r).RecordDecision(ctx, iss.PublicationID, 0, "manual_compose",
		fmt.Sprintf("lineup of %d %s became empty slots for manual writing", res.Selected, plural(res.Selected, "item", "items")))
	fresh, err := db(r).GetIssue(ctx, iss.ID)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	apiJSON(w, 200, map[string]any{"issue": s.issueSummary(r, fresh), "selected": res.Selected})
}

// apiComposeSentence adds editor-written text to an item — the manual
// counterpart of a drafted sentence. Multi-sentence input is stored one
// sentence per line; each factual sentence carries every chosen [n]
// marker, so citecheck and readiness treat it exactly like AI-drafted
// content (an uncited addition fails the uncited_sentences check).
// kind "interpretation" stores an editor-attributed block instead.
func (s *Server) apiComposeSentence(w http.ResponseWriter, r *http.Request) {
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
		Text      string `json:"text"`
		Kind      string `json:"kind"`
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
		apiErr(w, 400, "validation", "item text cannot be empty",
			apiReason{Code: "empty_sentence", Message: "item text cannot be empty",
				Target: &apiTarget{Kind: "item", Ref: fmt.Sprint(ref)}})
		return
	}
	if !authorTextOK(text) {
		apiErr(w, 400, "validation", "item text must be a single line with no document markup")
		return
	}
	kind := in.Kind
	if kind == "" {
		kind = "fact"
	}
	if kind != "fact" && kind != "interpretation" {
		apiErr(w, 400, "validation", "kind must be fact or interpretation")
		return
	}
	var raw string
	var newFlags []store.Flag
	if kind == "interpretation" {
		// (KLOX-48/51) an opinion carrying a number, a date or a
		// quotation is a fact, not a take — it may not bypass the
		// citation check, same guard as mark-as-interpretation.
		if pipeline.HasNumberDateOrQuote(text) {
			apiErr(w, 409, "interpretation_restricted",
				"a claim with a number, a date or a quotation cannot be marked as interpretation — cite a source instead",
				apiReason{Code: "interpretation_restricted", Message: "sentence contains a number, a date or a quotation",
					Target: &apiTarget{Kind: "item", Ref: fmt.Sprint(ref)}})
			return
		}
		raw = "> _Interpretação: " + text + "_"
	} else {
		d := item.dossier
		maxRef := 0
		if d != nil {
			maxRef = len(d.Sources)
		}
		seen := map[int]bool{}
		markers := ""
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
			markers += fmt.Sprintf(" [%d]", n)
		}
		// store one sentence per line, each carrying the citations, so
		// the document parser and citecheck see the same shape as a
		// drafted item
		var lines []string
		for _, piece := range pipeline.SplitSentences(text) {
			piece = strings.TrimSpace(piece)
			if !strings.ContainsFunc(piece, unicode.IsLetter) {
				continue
			}
			lines = append(lines, piece+markers)
		}
		if len(lines) == 0 {
			apiErr(w, 400, "validation", "no sentence found in the item text")
			return
		}
		raw = strings.Join(lines, "\n")
		// (KLOX-51) a composed fact must be verifiable exactly like a
		// drafted sentence: every line the citation floor cannot verify —
		// or that copies the source — lands flagged, and the flag blocks
		// approval until the editor fixes or removes the sentence.
		seenFlags := map[string]bool{}
		for _, f := range iss.Flags {
			if f.Item == int64(ref) {
				seenFlags[f.Sentence] = true
			}
		}
		for _, line := range lines {
			notes := s.composeFlagNotes(item, doc.dctx, line)
			if len(notes) == 0 || seenFlags[line] {
				continue
			}
			seenFlags[line] = true
			newFlags = append(newFlags, store.Flag{
				Item: int64(ref), Sentence: line, Note: strings.Join(notes, "; ")})
		}
	}
	// append at the end of the item's body (before its sources block)
	body, ok := insertInItem(iss.BodyMD, item, len(item.Sentences), raw)
	if !ok {
		apiErr(w, 409, "conflict", "the item no longer accepts this text")
		return
	}
	ctx := r.Context()
	flags := iss.Flags
	if len(newFlags) > 0 {
		flags = append(append([]store.Flag{}, iss.Flags...), newFlags...)
	}
	err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, IssueItemID: item.IssueItemID, Body: body,
		Flags: flags, Decision: "manual_compose", Reason: raw, RevertApproved: iss.Status == store.IssueApproved,
	})
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iss.BodyMD = body
	iss.Flags = flags
	if iss.Status == store.IssueApproved {
		iss.Status = store.IssueDrafted
	}
	s.respondSentence(w, r, iss, doc, map[string]any{
		"added":   len(strings.Split(raw, "\n")),
		"flagged": len(newFlags),
	})
}
