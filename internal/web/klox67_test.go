package web_test

// KLOX-67 handler coverage (QA rework): apiRerank's cap/fallback
// branches, approveCiteCheck's unsupported-flag/fallback paths, and the
// paid-action accounting both endpoints share. Runs Phase A on the
// replay fixture in demo mode, then flips cfg.Mode to "real" so the
// §7.2 endpoints reach the stubbed provider — no paid calls anywhere.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// stubLLM answers armed templates with canned text/errors and delegates
// everything else to the replay fixture, so the demo run still works
// while the handler under test sees the stubbed answer. calls counts
// every request per template, armed or not.
type stubLLM struct {
	inner llm.Provider
	mu    sync.Mutex
	armed map[string]func(llm.Request) (llm.Response, error)
	calls map[string]int
}

func (s *stubLLM) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	s.mu.Lock()
	s.calls[req.Template]++
	fn := s.armed[req.Template]
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return s.inner.Complete(ctx, req)
}

func (s *stubLLM) arm(tmpl, answer string) {
	s.armFunc(tmpl, func(llm.Request) (llm.Response, error) {
		return llm.Response{Text: answer}, nil
	})
}

func (s *stubLLM) armFunc(tmpl string, fn func(llm.Request) (llm.Response, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed[tmpl] = fn
}

func (s *stubLLM) callsFor(tmpl string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[tmpl]
}

// stubbedServer is demoServer with the stub provider swapped in; the
// test flips cfg.Mode to "real" when it needs the §7.2 paid endpoints.
func stubbedServer(t *testing.T) (*web.Server, *store.DB, *store.Publication, *config.Config, *stubLLM) {
	t.Helper()
	stub := &stubLLM{
		armed: map[string]func(llm.Request) (llm.Response, error){},
		calls: map[string]int{},
	}
	srv, db, pub, cfg := demoServerOpt(t, func(d *web.Deps) {
		stub.inner = d.LLM
		d.LLM = stub
	})
	return srv, db, pub, cfg, stub
}

// supportedSentenceAlt is supportedSentence built on a different word
// offset, so an edit can reword a claim and stay supported by the same
// excerpt (used to clear a flag without repeating the stored text).
func supportedSentenceAlt(excerpt string) string {
	words := strings.FieldsFunc(excerpt, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var pick []string
	for i, w := range words {
		if i%3 == 1 && len([]rune(w)) >= 4 {
			pick = append(pick, w)
		}
	}
	if len(pick) < 2 {
		// too sparse — take the default pick in reverse order instead
		for i, w := range words {
			if i%3 == 0 && len([]rune(w)) >= 4 {
				pick = append(pick, w)
			}
		}
		for i, j := 0, len(pick)-1; i < j; i, j = i+1, j-1 {
			pick[i], pick[j] = pick[j], pick[i]
		}
	}
	if len(pick) > 6 {
		pick = pick[:6]
	}
	return strings.Join(pick, " ") + "."
}

// readyIssue drives the manual-compose path to a ready issue: Phase A,
// skeleton compose (pending items approve as-is), one supported cited
// sentence per item and the required commentary. Approval then reaches
// the citecheck gate instead of stopping at readiness.
func readyIssue(t *testing.T, srv *web.Server, db *store.DB, pub *store.Publication) int64 {
	t.Helper()
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	if rr := apiCall(t, srv, "POST", issueURL+"/compose", nil); rr.Code != 200 {
		t.Fatalf("compose = %d: %s", rr.Code, shortBody(rr))
	}
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	version := doc["version"].(string)
	for _, it := range docItems(doc) {
		ref := int(it["ref"].(float64))
		excerpt, cite := citedExcerpt(t, it)
		rr := apiCall(t, srv, "POST", fmt.Sprintf("%s/items/%d/compose", issueURL, ref),
			map[string]any{"text": supportedSentence(excerpt), "citations": []int{cite}, "version": version})
		if rr.Code != 200 {
			t.Fatalf("fill item %d = %d: %s", ref, rr.Code, shortBody(rr))
		}
		version = apiJSON(t, rr)["version"].(string)
	}
	for _, sc := range pub.Sections {
		if sc.Commentary != "required" {
			continue
		}
		rr := apiCall(t, srv, "PUT", issueURL+"/commentary",
			map[string]string{"section": sc.Title, "text": "My editorial take.", "version": version})
		if rr.Code != 200 {
			t.Fatalf("commentary %q = %d: %s", sc.Title, rr.Code, shortBody(rr))
		}
		version = apiJSON(t, rr)["version"].(string)
	}
	if rd := apiGet(t, srv, issueURL+"/readiness"); !rd["ready"].(bool) {
		t.Fatalf("issue %d not ready: %v", issueID, rd["checks"])
	}
	return issueID
}

// TestRerankCapsAndFallback covers the §7.2 "Ordenar pela minha linha
// editorial" branches: demo/no-model refusal, the successful rerank
// persisted as triage scores, the per-newsletter and workspace daily
// caps, and the provider/malformed-answer fallbacks — every refusal
// answers {reranked:false}, never an error.
func TestRerankCapsAndFallback(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, cfg, stub := stubbedServer(t)
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	rerank := func() map[string]any {
		rr := apiCall(t, srv, "POST", issueURL+"/rerank", nil)
		if rr.Code != 200 {
			t.Fatalf("rerank = %d: %s", rr.Code, shortBody(rr))
		}
		return apiJSON(t, rr)
	}

	// demo mode refuses before touching the provider
	if v := rerank(); v["reranked"].(bool) {
		t.Fatal("demo-mode rerank should refuse")
	}
	if n := stub.callsFor("rerank"); n != 0 {
		t.Fatalf("demo rerank made %d model calls", n)
	}

	cfg.Mode = "real"
	before, err := db.RankedCandidates(ctx, pub.ID, issueID)
	if err != nil || len(before) < 2 {
		t.Fatalf("ranked candidates = %d, %v", len(before), err)
	}
	// swap the top two: ref 2 must come out first after the rerank
	stub.arm("rerank", "[2,1]")
	if v := rerank(); !v["reranked"].(bool) {
		t.Fatal("first real-mode rerank should succeed")
	}
	after, err := db.RankedCandidates(ctx, pub.ID, issueID)
	if err != nil || after[0].Story.ID != before[1].Story.ID {
		t.Fatalf("reranked order: first story = %v, want %v", after[0].Story.ID, before[1].Story.ID)
	}
	if n, _ := db.PaidActionCount(ctx, store.PaidActionRerank, pub.ID); n != 1 {
		t.Fatalf("rerank paid actions = %d, want 1", n)
	}

	// per-newsletter cap: one rerank already spent → refused, no call
	cfg.ReranksPerDay = 1
	if v := rerank(); v["reranked"].(bool) {
		t.Fatal("rerank past the newsletter cap should refuse")
	}
	if n := stub.callsFor("rerank"); n != 1 {
		t.Fatalf("capped rerank still called the model: %d", n)
	}

	// workspace daily cap refuses the same way
	cfg.ReranksPerDay, cfg.PaidActionsPerDay = 0, 1
	if v := rerank(); v["reranked"].(bool) {
		t.Fatal("rerank past the workspace cap should refuse")
	}
	cfg.PaidActionsPerDay = 0

	// provider failure and malformed JSON degrade to {reranked:false}
	stub.armFunc("rerank", func(llm.Request) (llm.Response, error) {
		return llm.Response{}, errors.New("anthropic down")
	})
	if v := rerank(); v["reranked"].(bool) {
		t.Fatal("provider failure should refuse")
	}
	stub.arm("rerank", "not json at all")
	if v := rerank(); v["reranked"].(bool) {
		t.Fatal("malformed answer should refuse")
	}
	if n := stub.callsFor("rerank"); n != 3 {
		t.Fatalf("rerank calls = %d, want 3 (success + 2 degraded)", n)
	}
	if n, _ := db.PaidActionCount(ctx, store.PaidActionRerank, pub.ID); n != 1 {
		t.Fatalf("paid actions after degraded calls = %d, want 1", n)
	}
}

// TestApproveCiteCheck covers the §7.5 approval gate: an "unsupported"
// verdict lands as a blocking flag on the sentence; the cap, LLM-off
// and every provider failure fall back to the deterministic floor so
// approval proceeds.
func TestApproveCiteCheck(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, cfg, stub := stubbedServer(t)

	// --- unsupported verdict → 409 + persisted flag ---
	issueID := readyIssue(t, srv, db, pub)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	cfg.Mode = "real"
	stub.arm("citecheck", `[{"id":1,"verdict":"unsupported","note":"inverts the source meaning"}]`)
	rr := apiCall(t, srv, "POST", issueURL+"/approve", nil)
	if rr.Code != 409 {
		t.Fatalf("approve with unsupported verdict = %d: %s", rr.Code, shortBody(rr))
	}
	if c := apiErrCode(t, rr); c != "blocked" {
		t.Fatalf("approve code = %q", c)
	}
	var sawUnsupported bool
	for _, r := range apiReasons(t, rr) {
		if r["code"] == "unsupported_claim" {
			sawUnsupported = true
		}
	}
	if !sawUnsupported {
		t.Fatalf("blocked reasons lack unsupported_claim: %v", apiReasons(t, rr))
	}
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil || len(iss.Flags) == 0 {
		t.Fatalf("unsupported verdict persisted no flag: %+v %v", iss, err)
	}
	if iss.Flags[0].Note != "inverts the source meaning" {
		t.Fatalf("flag note = %q", iss.Flags[0].Note)
	}
	if c := readinessCheck(apiGet(t, srv, issueURL+"/readiness"), "flags_unresolved"); c == nil || c["ok"].(bool) {
		t.Fatalf("a citecheck flag must fail readiness: %v", c)
	}

	// clear the flag by rewriting the flagged sentence into a supported
	// one (deleting it would leave the item empty), then a clean verdict
	// approves and records the second paid call
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	version := doc["version"].(string)
	var flaggedSID, excerpt string
	var cite int
	for _, it := range docItems(doc) {
		for _, sn := range it["sentences"].([]any) {
			if sn.(map[string]any)["flag"] != nil {
				flaggedSID = sn.(map[string]any)["id"].(string)
				excerpt, cite = citedExcerpt(t, it)
			}
		}
	}
	if flaggedSID == "" {
		t.Fatal("no flagged sentence in the document")
	}
	// the sentence-edit path runs its own model verdict on top of the
	// lexical floor — a benign answer lets the rewrite land
	stub.arm("citecheck", "[]")
	rr = apiCall(t, srv, "PATCH", issueURL+"/sentences/"+flaggedSID,
		map[string]any{"text": supportedSentenceAlt(excerpt), "citations": []int{cite}, "version": version})
	if rr.Code != 200 {
		t.Fatalf("rewrite flagged = %d: %s", rr.Code, shortBody(rr))
	}
	stub.arm("citecheck", `[{"id":1,"verdict":"supported"}]`)
	rr = apiCall(t, srv, "POST", issueURL+"/approve", nil)
	if rr.Code != 200 {
		t.Fatalf("approve after flag fix = %d: %s", rr.Code, shortBody(rr))
	}
	if rec := apiJSON(t, rr)["receipt"]; rec == nil {
		t.Fatal("approved issue carries no receipt")
	}
	if n, _ := db.PaidActionCount(ctx, store.PaidActionCiteCheck, issueID); n != 2 {
		t.Fatalf("citecheck paid actions = %d, want 2", n)
	}

	// --- fallbacks: every variant approves, so each needs its own
	// ready issue. The fixture fetcher only yields new items once per
	// publication, so each fallback seeds a sibling publication.
	freshReady := func(slug string) (int64, string) {
		t.Helper()
		data, err := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		if err != nil {
			t.Fatalf("read publication yaml: %v", err)
		}
		def, err := config.ParsePublication(data)
		if err != nil {
			t.Fatalf("parse publication yaml: %v", err)
		}
		// keep the yaml's own fields — publication name/editorial line
		// feed the replay-fixture prompt hash; only the slug must differ
		def.Slug = slug
		pid, err := db.SeedPublication(ctx, def)
		if err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
		p2, err := db.GetPublication(ctx, fmt.Sprint(pid))
		if err != nil {
			t.Fatalf("get %s: %v", slug, err)
		}
		// armed citecheck answers persist across scenarios; the edit
		// path runs its own verdict check, so clear it while composing
		stub.arm("citecheck", "[]")
		cfg.Mode = "demo"
		id := readyIssue(t, srv, db, p2)
		cfg.Mode = "real"
		return id, "/api/v1/issues/" + fmt.Sprint(id)
	}

	// LLM off: citecheck is skipped entirely, deterministic floor stands
	_, issueURL2 := freshReady("citecheck-llmoff")
	cfg.LLMMode = "off"
	before := stub.callsFor("citecheck")
	if rr := apiCall(t, srv, "POST", issueURL2+"/approve", nil); rr.Code != 200 {
		t.Fatalf("approve with LLM off = %d: %s", rr.Code, shortBody(rr))
	}
	if n := stub.callsFor("citecheck"); n != before {
		t.Fatalf("LLM-off approve made %d citecheck calls", n-before)
	}
	cfg.LLMMode = "auto"

	// provider error → approval proceeds on the deterministic floor
	_, issueURL3 := freshReady("citecheck-err")
	stub.armFunc("citecheck", func(llm.Request) (llm.Response, error) {
		return llm.Response{}, errors.New("anthropic down")
	})
	if rr := apiCall(t, srv, "POST", issueURL3+"/approve", nil); rr.Code != 200 {
		t.Fatalf("approve over provider error = %d: %s", rr.Code, shortBody(rr))
	}

	// non-JSON answer counts as a paid call but still falls back
	issueID4, issueURL4 := freshReady("citecheck-badjson")
	stub.arm("citecheck", "not json at all")
	if rr := apiCall(t, srv, "POST", issueURL4+"/approve", nil); rr.Code != 200 {
		t.Fatalf("approve over non-JSON answer = %d: %s", rr.Code, shortBody(rr))
	}
	if n, _ := db.PaidActionCount(ctx, store.PaidActionCiteCheck, issueID4); n != 1 {
		t.Fatalf("non-JSON citecheck paid actions = %d, want 1", n)
	}

	// per-edition cap: the call is skipped, approval still proceeds
	issueID5, issueURL5 := freshReady("citecheck-cap")
	cfg.CiteChecksPerEdition = 3
	for i := 0; i < 3; i++ {
		if err := db.RecordPaidAction(ctx, store.PaidActionCiteCheck, issueID5); err != nil {
			t.Fatalf("seed paid action: %v", err)
		}
	}
	before = stub.callsFor("citecheck")
	if rr := apiCall(t, srv, "POST", issueURL5+"/approve", nil); rr.Code != 200 {
		t.Fatalf("approve at citecheck cap = %d: %s", rr.Code, shortBody(rr))
	}
	if n := stub.callsFor("citecheck"); n != before {
		t.Fatalf("capped approve made %d citecheck calls", n-before)
	}
}
