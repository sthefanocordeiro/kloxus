package web_test

// Contract tests for the manual-compose flow (KLOX-32): the board's
// lineup becomes empty item slots the editor fills by hand. The whole
// path is model-free — a counting provider proves no LLM call happens
// after Phase A, including dossier creation for manually added stories.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// countingLLM counts Complete calls so the test can prove the manual
// compose flow never reaches the model.
type countingLLM struct {
	inner llm.Provider
	mu    sync.Mutex
	calls int
}

func (c *countingLLM) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.inner.Complete(ctx, req)
}

func (c *countingLLM) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// composeServer is demoServer with a counting LLM provider.
func composeServer(t *testing.T) (*web.Server, *store.DB, *store.Publication, *countingLLM) {
	t.Helper()
	counter := &countingLLM{}
	srv, db, pub, _ := demoServerOpt(t, func(d *web.Deps) {
		counter.inner = d.LLM
		d.LLM = counter
	})
	return srv, db, pub, counter
}

// docItems flattens a document response into its items.
func docItems(doc map[string]any) []map[string]any {
	var out []map[string]any
	for _, s := range doc["sections"].([]any) {
		for _, i := range s.(map[string]any)["items"].([]any) {
			out = append(out, i.(map[string]any))
		}
	}
	return out
}

func readinessCheck(rd map[string]any, code string) map[string]any {
	for _, c := range rd["checks"].([]any) {
		cm := c.(map[string]any)
		if cm["code"] == code {
			return cm
		}
	}
	return nil
}

// supportedSentence builds a claim the citation floor verifies against
// the item's stored source text — content words of the excerpt picked
// at non-consecutive positions, so the sentence can never form a copied
// run either (KLOX-51).
func supportedSentence(excerpt string) string {
	words := strings.FieldsFunc(excerpt, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var pick []string
	for i, w := range words {
		if i%3 == 0 && len([]rune(w)) >= 4 {
			pick = append(pick, w)
		}
	}
	if len(pick) > 6 {
		pick = pick[:6]
	}
	return strings.Join(pick, " ") + "."
}

// verbatimPaste is the owner's "first-sentence paste" case: the
// excerpt's opening words copied one for one — long enough to trip the
// consecutive-word copy rule.
func verbatimPaste(excerpt string) string {
	words := strings.FieldsFunc(excerpt, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) > 12 {
		words = words[:12]
	}
	return strings.Join(words, " ") + "."
}

// citedExcerpt returns the excerpt and ref of an item's first source
// whose stored text is present — the corpus a composed citation checks
// against.
func citedExcerpt(t *testing.T, it map[string]any) (excerpt string, ref int) {
	t.Helper()
	for _, s := range it["sources"].([]any) {
		sv := s.(map[string]any)
		if e, _ := sv["excerpt"].(string); e != "" {
			return e, int(sv["ref"].(float64))
		}
	}
	t.Fatalf("item %v has no source excerpt to cite from", it["ref"])
	return "", 0
}

// TestManualComposeJourney walks the zero-model path: pick candidates,
// compose the empty skeleton, fill every item by hand, approve and dry
// send. The LLM counter must stay flat after Phase A.
func TestManualComposeJourney(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, counter := composeServer(t)
	pubID := fmt.Sprint(pub.ID)

	rr := apiCall(t, srv, "POST", "/api/v1/publications/"+pubID+"/runs", nil)
	if rr.Code != 202 {
		t.Fatalf("start run = %d", rr.Code)
	}
	run := waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))
	if run["status"] != "done" {
		t.Fatalf("run status = %v err=%v", run["status"], run["error"])
	}
	issueID := int64(run["issue_id"].(float64))
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	// pick candidates like the board does: keep the AI lineup and add a
	// bench story (no dossier) into a section with spare capacity
	cands := apiGet(t, srv, issueURL+"/candidates")
	type entry struct {
		StoryID int64  `json:"story_id"`
		Section string `json:"section"`
	}
	var entries []entry
	filled := map[string]int{}
	var benchStory int64
	for _, c := range cands["candidates"].([]any) {
		cv := c.(map[string]any)
		if cv["in_issue"].(bool) {
			entries = append(entries, entry{
				StoryID: int64(cv["story_id"].(float64)),
				Section: cv["section"].(string)})
			filled[cv["section"].(string)]++
		} else if benchStory == 0 && cv["available"].(bool) {
			benchStory = int64(cv["story_id"].(float64))
		}
	}
	benchSection := ""
	for _, s := range cands["sections"].([]any) {
		sv := s.(map[string]any)
		if filled[sv["title"].(string)] < int(sv["capacity"].(float64)) {
			benchSection = sv["title"].(string)
			break
		}
	}
	if benchStory == 0 || benchSection == "" {
		t.Fatalf("no bench story or free section to test the dossierless path (story=%d section=%q)", benchStory, benchSection)
	}
	entries = append(entries, entry{StoryID: benchStory, Section: benchSection})
	rr = apiCall(t, srv, "PUT", issueURL+"/lineup", map[string]any{"entries": entries})
	if rr.Code != 200 {
		t.Fatalf("lineup = %d: %s", rr.Code, shortBody(rr))
	}

	llmAfterBoard := counter.count()
	if llmAfterBoard == 0 {
		t.Fatal("sanity: Phase A should have made replayed model calls")
	}

	// --- compose the empty skeleton ---
	rr = apiCall(t, srv, "POST", issueURL+"/compose", nil)
	if rr.Code != 200 {
		t.Fatalf("compose = %d: %s", rr.Code, shortBody(rr))
	}
	v := apiJSON(t, rr)
	if v["selected"].(float64) != float64(len(entries)) {
		t.Fatalf("compose selected = %v, want %d", v["selected"], len(entries))
	}
	if got := counter.count(); got != llmAfterBoard {
		t.Fatalf("compose called the model: calls %d -> %d", llmAfterBoard, got)
	}

	// the document is all empty slots: every item has sources but no
	// sentences, and the dossierless bench story got its own ref
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	items := docItems(doc)
	if len(items) != len(entries) {
		t.Fatalf("document items = %d, want %d", len(items), len(entries))
	}
	refs := map[int]bool{}
	for _, it := range items {
		ref := int(it["ref"].(float64))
		if refs[ref] {
			t.Fatalf("duplicate item ref %d", ref)
		}
		refs[ref] = true
		if len(it["sentences"].([]any)) != 0 {
			t.Fatalf("item %d should start empty, has %v", ref, it["sentences"])
		}
		if len(it["sources"].([]any)) == 0 {
			t.Fatalf("item %d has no sources attached", ref)
		}
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if iss.Status != "drafted" {
		t.Fatalf("status after compose = %q, want drafted", iss.Status)
	}
	// readiness blocks on empty items
	rd := apiGet(t, srv, issueURL+"/readiness")
	c := readinessCheck(rd, "empty_items")
	if c == nil {
		t.Fatal("readiness has no empty_items check")
	}
	if c["ok"].(bool) || c["count"].(float64) != float64(len(items)) {
		t.Fatalf("empty_items check = %v, want %d empty", c, len(items))
	}
	if rd["ready"].(bool) {
		t.Fatal("issue with all-empty items must not be ready")
	}
	// re-compose now that the skeleton exists is still allowed (all
	// items empty) — but after the first sentence it must refuse
	version := doc["version"].(string)

	// --- fill items by hand ---
	fill := func(ref int, payload map[string]any) map[string]any {
		payload["version"] = version
		rr := apiCall(t, srv, "POST", fmt.Sprintf("%s/items/%d/compose", issueURL, ref), payload)
		if rr.Code != 200 {
			dbg := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
			var have []int
			for _, it := range docItems(dbg) {
				have = append(have, int(it["ref"].(float64)))
			}
			iss, _ := db.GetIssue(ctx, issueID)
			t.Logf("body:\n%s", iss.BodyMD)
			t.Fatalf("compose item %d = %d: %s (refs now: %v)", ref, rr.Code, shortBody(rr), have)
		}
		v := apiJSON(t, rr)
		version = v["version"].(string)
		return v
	}

	// guards: bad citation and stale version are refused
	rr = apiCall(t, srv, "POST", fmt.Sprintf("%s/items/%d/compose", issueURL, int(items[0]["ref"].(float64))),
		map[string]any{"text": "Anything.", "citations": []int{99}, "version": version})
	if c := apiErrCode(t, rr); c != "invalid_citation" {
		t.Fatalf("bad citation: code = %q", c)
	}
	rr = apiCall(t, srv, "POST", fmt.Sprintf("%s/items/%d/compose", issueURL, int(items[0]["ref"].(float64))),
		map[string]any{"text": "Anything.", "citations": []int{1}, "version": "stale"})
	if c := apiErrCode(t, rr); c != "conflict" {
		t.Fatalf("stale version: code = %q", c)
	}

	// an uncited fact lands and citecheck/readiness flags it, exactly as
	// with an AI-drafted sentence
	first := int(items[0]["ref"].(float64))
	fill(first, map[string]any{"text": "A claim nobody can trace back."})
	rd = apiGet(t, srv, issueURL+"/readiness")
	c = readinessCheck(rd, "uncited_sentences")
	if c == nil || c["ok"].(bool) || c["count"].(float64) != 1 {
		t.Fatalf("uncited composed sentence should fail readiness: %v", c)
	}
	// remove it through the normal sentence API
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	version = doc["version"].(string)
	var uncitedSID string
	for _, it := range docItems(doc) {
		for _, sn := range it["sentences"].([]any) {
			s := sn.(map[string]any)
			if s["kind"] == "fact" && len(s["citations"].([]any)) == 0 {
				uncitedSID = s["id"].(string)
			}
		}
	}
	rr = apiCall(t, srv, "DELETE", issueURL+"/sentences/"+uncitedSID, map[string]any{"version": version})
	if rr.Code != 200 {
		t.Fatalf("remove uncited = %d: %s", rr.Code, shortBody(rr))
	}
	version = apiJSON(t, rr)["version"].(string)

	// write every item: a cited multi-sentence block lands one sentence
	// per line, each carrying the markers; an interpretation needs no
	// citation. The text is built from the item's own source excerpt so
	// the citation floor verifies it (KLOX-51). The second sentence must
	// open with a capital — SplitSentences only accepts a ". " boundary
	// before a rune that can start a sentence, so a lowercase continuation
	// would fold both into one line.
	var firstWritten string
	for i, it := range items {
		ref := int(it["ref"].(float64))
		excerpt, cite := citedExcerpt(t, it)
		text := supportedSentence(excerpt)
		if i == 0 {
			firstWritten = text
		}
		second := supportedSentence(excerpt)
		rs := []rune(second)
		rs[0] = unicode.ToUpper(rs[0])
		fill(ref, map[string]any{
			"text":      text + " " + string(rs),
			"citations": []int{cite},
		})
		if i == 0 {
			fill(ref, map[string]any{
				"text": "And this is the editor's own reading of it.",
				"kind": "interpretation",
			})
		}
	}
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	for _, it := range docItems(doc) {
		sents := it["sentences"].([]any)
		if len(sents) < 2 {
			t.Fatalf("item %d has %d sentences, want >= 2", it["ref"], len(sents))
		}
		for _, sn := range sents[:2] {
			s := sn.(map[string]any)
			if len(s["citations"].([]any)) != 1 {
				t.Fatalf("composed sentence %v lacks its citation", s["id"])
			}
		}
	}
	if got := counter.count(); got != llmAfterBoard {
		t.Fatalf("manual compose called the model: calls %d -> %d", llmAfterBoard, got)
	}
	// composed text lands inside the item, before its sources block
	iss, _ = db.GetIssue(ctx, issueID)
	i := strings.Index(iss.BodyMD, strings.TrimSuffix(firstWritten, "."))
	j := strings.Index(iss.BodyMD, "**Sources:**")
	if i < 0 || j < 0 || i > j {
		t.Fatal("composed sentence must land inside the item, before its sources block")
	}
	// composing again is now refused — the issue is no longer empty
	rr = apiCall(t, srv, "POST", issueURL+"/compose", nil)
	if c := apiErrCode(t, rr); c != "not_empty" {
		t.Fatalf("compose over written content: code = %q", c)
	}

	// the manually-added story got a deterministic dossier with sources
	for _, it := range docItems(doc) {
		iid := int64(it["issue_item_id"].(float64))
		d := apiGet(t, srv, "/api/v1/items/"+fmt.Sprint(iid)+"/dossier")
		dd := d["dossier"].(map[string]any)
		if len(dd["sources"].([]any)) == 0 {
			t.Fatalf("item %d dossier has no sources", iid)
		}
	}

	// commentary, readiness, approve, dry send — still zero model calls
	rd = apiGet(t, srv, issueURL+"/readiness")
	if readinessCheck(rd, "empty_items")["ok"].(bool) != true {
		t.Fatalf("empty_items should clear after filling: %v", rd["checks"])
	}
	version = doc["version"].(string)
	rr = apiCall(t, srv, "PUT", issueURL+"/commentary",
		map[string]string{"section": "The big one", "text": "My editorial take.", "version": version})
	if rr.Code != 200 {
		t.Fatalf("commentary = %d: %s", rr.Code, shortBody(rr))
	}
	rd = apiGet(t, srv, issueURL+"/readiness")
	if !rd["ready"].(bool) {
		t.Fatalf("issue not ready after manual fill: %v", rd["checks"])
	}
	rr = apiCall(t, srv, "POST", issueURL+"/approve", nil)
	if rr.Code != 200 {
		t.Fatalf("approve = %d: %s", rr.Code, shortBody(rr))
	}
	rr = apiCall(t, srv, "POST", issueURL+"/send", map[string]string{"audience": "test"})
	if rr.Code != 200 {
		t.Fatalf("test send = %d: %s", rr.Code, shortBody(rr))
	}
	if v := apiJSON(t, rr); v["outbox_email_id"] == nil {
		t.Error("dry send should record an outbox email")
	}
	if got := counter.count(); got != llmAfterBoard {
		t.Fatalf("manual flow called the model after compose: calls %d -> %d", llmAfterBoard, got)
	}
}

func shortBody(rr *httptest.ResponseRecorder) string {
	s := rr.Body.String()
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// TestComposeClaimEnforcement reproduces the owner's v88 audit cases
// (KLOX-51): a fabricated claim carrying an unrelated [n] and a
// verbatim source paste must land flagged on the manual compose path —
// the flag blocks approval until the editor fixes it. On the sentence
// edit path the same writes are refused.
func TestComposeClaimEnforcement(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := composeServer(t)
	pubID := fmt.Sprint(pub.ID)

	rr := apiCall(t, srv, "POST", "/api/v1/publications/"+pubID+"/runs", nil)
	if rr.Code != 202 {
		t.Fatalf("start run = %d", rr.Code)
	}
	run := waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))
	if run["status"] != "done" {
		t.Fatalf("run status = %v err=%v", run["status"], run["error"])
	}
	issueID := int64(run["issue_id"].(float64))
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	rr = apiCall(t, srv, "POST", issueURL+"/compose", nil)
	if rr.Code != 200 {
		t.Fatalf("compose = %d: %s", rr.Code, shortBody(rr))
	}
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	items := docItems(doc)
	var ref, cite int
	var excerpt string
	for _, it := range items {
		ref = int(it["ref"].(float64))
		excerpt, cite = citedExcerpt(t, it)
		break
	}
	version := doc["version"].(string)
	post := func(payload map[string]any) *httptest.ResponseRecorder {
		payload["version"] = version
		rr := apiCall(t, srv, "POST", fmt.Sprintf("%s/items/%d/compose", issueURL, ref), payload)
		if v := apiJSON(t, rr); v["version"] != nil {
			version = v["version"].(string)
		}
		return rr
	}
	flaggedSID := func() (id, note string) {
		doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
		version = doc["version"].(string)
		for _, it := range docItems(doc) {
			for _, sn := range it["sentences"].([]any) {
				s := sn.(map[string]any)
				if f, ok := s["flag"].(map[string]any); ok {
					return s["id"].(string), f["note"].(string)
				}
			}
		}
		return "", ""
	}
	// sentence ids hash the stored text — an edit renames them; fetch the
	// item's current fact-sentence id after each mutation.
	firstFactSID := func() string {
		doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
		version = doc["version"].(string)
		for _, it := range docItems(doc) {
			if int(it["ref"].(float64)) != ref {
				continue
			}
			for _, sn := range it["sentences"].([]any) {
				s := sn.(map[string]any)
				if s["kind"] == "fact" {
					return s["id"].(string)
				}
			}
		}
		return ""
	}

	// --- audit case 1: fabricated claim carrying an unrelated [n] ---
	// lands flagged (not as "1/1 sourced"), and the flag blocks approval.
	rr = post(map[string]any{
		"text":      "O Banco Central vai encerrar o Pix em março de 2027 e cobrar 9 reais por transferência.",
		"citations": []int{cite},
	})
	if rr.Code != 200 {
		t.Fatalf("compose fabricated = %d: %s", rr.Code, shortBody(rr))
	}
	v := apiJSON(t, rr)
	if v["flagged"].(float64) != 1 {
		t.Fatalf("fabricated [n] claim should land flagged: %v", v)
	}
	sid, note := flaggedSID()
	if sid == "" {
		t.Fatal("fabricated sentence carries no flag in the document")
	}
	if !strings.Contains(note, fmt.Sprintf("not supported by [%d]", cite)) {
		t.Fatalf("flag note = %q, want 'not supported by [%d]'", note, cite)
	}
	rd := apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "flags_unresolved"); c == nil || c["ok"].(bool) {
		t.Fatalf("a flagged claim must fail readiness: %v", c)
	}
	if rr = apiCall(t, srv, "POST", issueURL+"/approve", nil); rr.Code != 409 {
		t.Fatalf("approve over a flagged claim = %d, want 409", rr.Code)
	}
	if iss, _ := db.GetIssue(ctx, issueID); iss.Status == "approved" {
		t.Fatal("an unsupported [n] must never become approvable")
	}

	// rewriting the flagged claim into a supported one resolves the flag
	rr = apiCall(t, srv, "PATCH", issueURL+"/sentences/"+sid,
		map[string]any{"text": supportedSentence(excerpt), "citations": []int{cite}, "version": version})
	if rr.Code != 200 {
		t.Fatalf("edit flagged sentence = %d: %s", rr.Code, shortBody(rr))
	}
	version = apiJSON(t, rr)["version"].(string)
	if iss, _ := db.GetIssue(ctx, issueID); len(iss.Flags) != 0 {
		t.Fatalf("fixed claim should resolve the flag, %d remain", len(iss.Flags))
	}

	// --- audit case 2: verbatim first-sentence paste with its citation ---
	rr = post(map[string]any{"text": verbatimPaste(excerpt), "citations": []int{cite}})
	if rr.Code != 200 {
		t.Fatalf("compose verbatim = %d: %s", rr.Code, shortBody(rr))
	}
	v = apiJSON(t, rr)
	if v["flagged"].(float64) != 1 {
		t.Fatalf("verbatim paste should land flagged: %v", v)
	}
	pastedSID, note := flaggedSID()
	if !strings.Contains(note, "too close to the source") {
		t.Fatalf("verbatim flag note = %q, want 'too close to the source'", note)
	}
	// removing the pasted sentence resolves the flag
	rr = apiCall(t, srv, "DELETE", issueURL+"/sentences/"+pastedSID, map[string]any{"version": version})
	if rr.Code != 200 {
		t.Fatalf("remove verbatim = %d: %s", rr.Code, shortBody(rr))
	}
	version = apiJSON(t, rr)["version"].(string)

	// --- the same writes are refused on the sentence edit path ---
	// an unverifiable claim keeps its flag — no [n] can clear it
	sid = firstFactSID()
	if sid == "" {
		t.Fatal("no fact sentence left to edit")
	}
	rr = apiCall(t, srv, "PATCH", issueURL+"/sentences/"+sid,
		map[string]any{"text": "Une affirmation impossible à vérifier contre la source.",
			"citations": []int{cite}, "version": version})
	if c := apiErrCode(t, rr); c != "unsupported_claim" {
		t.Fatalf("unsupported edit: code = %q", c)
	}
	// rewriting a clean sentence into a source copy is refused too
	rr = apiCall(t, srv, "PATCH", issueURL+"/sentences/"+sid,
		map[string]any{"text": verbatimPaste(excerpt), "citations": []int{cite}, "version": version})
	if c := apiErrCode(t, rr); c != "too_close_to_source" {
		t.Fatalf("verbatim edit: code = %q", c)
	}

	// an interpretation cannot smuggle a number-bearing claim past the
	// citation check — same guard as the mark-as-interpretation action
	rr = post(map[string]any{"text": "O Pix custará 9 reais a partir de 2027.", "kind": "interpretation"})
	if c := apiErrCode(t, rr); c != "interpretation_restricted" {
		t.Fatalf("interpretation with numbers: code = %q", c)
	}

	// --- multi-citation flag attachment (CTO blocker on PR #37) ---
	// a flagged claim stored as "claim. [1] [2]" must re-parse with both
	// markers attached, so the flag hangs on the sentence — not in the
	// item's unmatched-flags list — and Remove clears it so approval
	// can unblock.
	multiRef := 0
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	version = doc["version"].(string)
	for _, it := range docItems(doc) {
		if len(it["sources"].([]any)) >= 2 {
			multiRef = int(it["ref"].(float64))
			break
		}
	}
	if multiRef == 0 {
		t.Fatal("no fixture item with >= 2 sources for the multi-citation case")
	}
	rr = apiCall(t, srv, "POST", fmt.Sprintf("%s/items/%d/compose", issueURL, multiRef), map[string]any{
		"text":      "O Banco Central vai encerrar o Pix em março de 2027 e cobrar 9 reais por transferência.",
		"citations": []int{1, 2}, "version": version})
	if rr.Code != 200 {
		t.Fatalf("compose multi-cite = %d: %s", rr.Code, shortBody(rr))
	}
	version = apiJSON(t, rr)["version"].(string)
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	version = doc["version"].(string)
	var multiSID string
	for _, it := range docItems(doc) {
		if int(it["ref"].(float64)) != multiRef {
			continue
		}
		if fl, ok := it["flags"].([]any); ok && len(fl) != 0 {
			t.Fatalf("multi-citation flag orphaned onto the item: %v", fl)
		}
		for _, sn := range it["sentences"].([]any) {
			s := sn.(map[string]any)
			if _, ok := s["flag"].(map[string]any); !ok {
				continue
			}
			multiSID = s["id"].(string)
			if n := len(s["citations"].([]any)); n != 2 {
				t.Fatalf("re-parsed sentence kept %d citation(s), want 2", n)
			}
		}
	}
	if multiSID == "" {
		t.Fatal("multi-citation claim carries no sentence flag")
	}
	rr = apiCall(t, srv, "DELETE", issueURL+"/sentences/"+multiSID, map[string]any{"version": version})
	if rr.Code != 200 {
		t.Fatalf("remove multi-cite = %d: %s", rr.Code, shortBody(rr))
	}
	version = apiJSON(t, rr)["version"].(string)
	if iss, _ := db.GetIssue(ctx, issueID); len(iss.Flags) != 0 {
		t.Fatalf("removing the multi-citation claim should clear the flag, %d remain", len(iss.Flags))
	}
	rd = apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "flags_unresolved"); !c["ok"].(bool) {
		t.Fatalf("flags_unresolved must unblock after removal: %v", c)
	}
}

// TestUncitedSentenceTreatment reproduces the owner's v88 dead-end
// (KLOX-52): an uncited fact that reaches the document without a
// stored flag must surface the same flag treatment as a citecheck flag
// — visible reason and Edit / interpretation / Remove — and resolve
// through the same write paths. A flag keyed to text the body no
// longer carries is stale and must not keep blocking approval.
func TestUncitedSentenceTreatment(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := composeServer(t)
	pubID := fmt.Sprint(pub.ID)

	rr := apiCall(t, srv, "POST", "/api/v1/publications/"+pubID+"/runs", nil)
	if rr.Code != 202 {
		t.Fatalf("start run = %d", rr.Code)
	}
	run := waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))
	if run["status"] != "done" {
		t.Fatalf("run status = %v err=%v", run["status"], run["error"])
	}
	issueID := int64(run["issue_id"].(float64))
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	rr = apiCall(t, srv, "POST", issueURL+"/compose", nil)
	if rr.Code != 200 {
		t.Fatalf("compose = %d: %s", rr.Code, shortBody(rr))
	}
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	items := docItems(doc)
	it0 := items[0]
	ref := int(it0["ref"].(float64))

	// the v88 body: an uncited fact written before flags were persisted
	// on compose — zero flag rows on the issue
	iss, _ := db.GetIssue(ctx, issueID)
	legacy := strings.Replace(iss.BodyMD, "**Sources:**",
		"A claim nobody can trace back.\n\n**Sources:**", 1)
	if legacy == iss.BodyMD {
		t.Fatal("compose skeleton body has no sources block to splice into")
	}
	if err := db.UpdateIssue(ctx, issueID, "drafted", legacy, "[]"); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	// the document surfaces the flag treatment anyway — the sentence
	// carries the same note citecheck writes, so Edit/Remove exist
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	version := doc["version"].(string)
	var sid, note string
	for _, it := range docItems(doc) {
		for _, sn := range it["sentences"].([]any) {
			s := sn.(map[string]any)
			if f, ok := s["flag"].(map[string]any); ok {
				sid, note = s["id"].(string), f["note"].(string)
			}
		}
	}
	if sid == "" || note != "sentence has no citation" {
		t.Fatalf("uncited sentence flag = (%q, %q), want 'sentence has no citation'", sid, note)
	}
	rd := apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "uncited_sentences"); c == nil || c["ok"].(bool) {
		t.Fatalf("uncited fact must still fail readiness: %v", c)
	}
	if rr = apiCall(t, srv, "POST", issueURL+"/approve", nil); rr.Code != 409 {
		t.Fatalf("approve over an uncited fact = %d, want 409", rr.Code)
	}

	// the same edit that resolves a citecheck flag resolves this one —
	// a supported claim carrying its citation
	excerpt, cite := citedExcerpt(t, it0)
	rr = apiCall(t, srv, "PATCH", issueURL+"/sentences/"+sid,
		map[string]any{"text": supportedSentence(excerpt), "citations": []int{cite}, "version": version})
	if rr.Code != 200 {
		t.Fatalf("edit uncited sentence = %d: %s", rr.Code, shortBody(rr))
	}
	version = apiJSON(t, rr)["version"].(string)
	rd = apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "uncited_sentences"); !c["ok"].(bool) {
		t.Fatalf("uncited check must clear after the edit: %v", c)
	}

	// the restore path produces the same treatment: a fact restored
	// without citations lands uncited and unflagged in storage, yet the
	// document must flag it — and Remove clears it, the path the owner
	// could not reach on v88
	rr = apiCall(t, srv, "POST", fmt.Sprintf("%s/items/%d/sentences", issueURL, ref),
		map[string]any{"index": 0, "text": "Another claim nobody can trace.",
			"kind": "fact", "citations": []int{}, "version": version})
	if rr.Code != 200 {
		t.Fatalf("restore uncited = %d: %s", rr.Code, shortBody(rr))
	}
	version = apiJSON(t, rr)["version"].(string)
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	version = doc["version"].(string)
	var restoredSID string
	for _, it := range docItems(doc) {
		for _, sn := range it["sentences"].([]any) {
			s := sn.(map[string]any)
			if s["text"] == "Another claim nobody can trace." {
				if f, ok := s["flag"].(map[string]any); !ok || f["note"] != "sentence has no citation" {
					t.Fatalf("restored uncited sentence flag = %v", s["flag"])
				}
				restoredSID = s["id"].(string)
			}
		}
	}
	if restoredSID == "" {
		t.Fatal("restored sentence not in the document")
	}
	rr = apiCall(t, srv, "DELETE", issueURL+"/sentences/"+restoredSID, map[string]any{"version": version})
	if rr.Code != 200 {
		t.Fatalf("remove restored = %d: %s", rr.Code, shortBody(rr))
	}
	version = apiJSON(t, rr)["version"].(string)
	rd = apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "uncited_sentences"); !c["ok"].(bool) {
		t.Fatalf("uncited check must clear after removal: %v", c)
	}

	// stale flags — keyed to text the body no longer carries — must not
	// block approval: "entry has no citations" holds only while an
	// uncited fact remains; an unmatched sentence flag points at a
	// sentence that is gone (KLOX-52, the dead-end's other half)
	iss, _ = db.GetIssue(ctx, issueID)
	stale, _ := json.Marshal([]store.Flag{
		{Item: int64(ref), Sentence: "Body text the draft once wrote.", Note: "entry has no citations"},
		{Item: int64(ref), Sentence: "A sentence since removed.", Note: "not supported by [1]"},
		{Item: 999, Sentence: "On an item that no longer exists.", Note: "sentence has no citation"},
	})
	if err := db.UpdateIssue(ctx, issueID, "drafted", iss.BodyMD, string(stale)); err != nil {
		t.Fatalf("UpdateIssue stale flags: %v", err)
	}
	rd = apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "flags_unresolved"); c == nil || !c["ok"].(bool) {
		t.Fatalf("stale flags must not block readiness: %v", c)
	}
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	if got := doc["coverage"].(map[string]any)["flagged"].(float64); got != 0 {
		t.Fatalf("coverage.flagged = %v with only stale flags", got)
	}

	// a live "entry has no citations" flag still blocks — while the
	// item carries an uncited fact the flag describes a real problem.
	// Resolving that fact must let the flag go stale: before KLOX-52 it
	// stayed forever, unmatched to any sentence, with no UI path out.
	iss, _ = db.GetIssue(ctx, issueID)
	withUncited := strings.Replace(iss.BodyMD, "**Sources:**",
		"Yet another claim nobody can trace.\n\n**Sources:**", 1)
	entryFlag, _ := json.Marshal([]store.Flag{
		{Item: int64(ref), Sentence: "Two claims nobody could trace, once.", Note: "entry has no citations"},
	})
	if err := db.UpdateIssue(ctx, issueID, "drafted", withUncited, string(entryFlag)); err != nil {
		t.Fatalf("UpdateIssue entry flag: %v", err)
	}
	rd = apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "flags_unresolved"); c == nil || c["ok"].(bool) {
		t.Fatalf("a live entry flag must still block readiness: %v", c)
	}
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	version = doc["version"].(string)
	var lastSID string
	for _, it := range docItems(doc) {
		for _, sn := range it["sentences"].([]any) {
			s := sn.(map[string]any)
			if s["text"] == "Yet another claim nobody can trace." {
				lastSID = s["id"].(string)
			}
		}
	}
	if lastSID == "" {
		t.Fatal("uncited sentence missing after entry-flag setup")
	}
	rr = apiCall(t, srv, "DELETE", issueURL+"/sentences/"+lastSID, map[string]any{"version": version})
	if rr.Code != 200 {
		t.Fatalf("remove last uncited = %d: %s", rr.Code, shortBody(rr))
	}
	rd = apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "flags_unresolved"); c == nil || !c["ok"].(bool) {
		t.Fatalf("entry flag must go stale once the item is fully cited: %v", c)
	}
}
