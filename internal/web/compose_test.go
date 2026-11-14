package web_test

// Contract tests for the manual-compose flow (KLOX-32): the board's
// lineup becomes empty item slots the editor fills by hand. The whole
// path is model-free — a counting provider proves no LLM call happens
// after Phase A, including dossier creation for manually added stories.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
	// citation
	for i, it := range items {
		ref := int(it["ref"].(float64))
		fill(ref, map[string]any{
			"text":      fmt.Sprintf("Manual paragraph %d says the first thing. It also adds a second.", i+1),
			"citations": []int{1},
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
	i := strings.Index(iss.BodyMD, "Manual paragraph 1")
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
