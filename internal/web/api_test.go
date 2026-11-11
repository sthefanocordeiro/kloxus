package web_test

// Contract tests for the /api/v1 JSON interface (docs/api.md). They walk
// the whole demo journey — run, triage, draft, fix flags, commentary,
// approve, send, reader — and assert the error envelope on blocked and
// invalid actions.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// apiCall issues a JSON request and returns the recorder.
func apiCall(t *testing.T, srv *web.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// apiJSON decodes a response body into a generic map.
func apiJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("response is not JSON (%d): %s", rr.Code, rr.Body.String()[:200])
	}
	return v
}

// apiErrCode returns the error.code of an error response, failing the
// test when the envelope is missing.
func apiErrCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	v := apiJSON(t, rr)
	e, ok := v["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error envelope in %d response: %s", rr.Code, rr.Body.String()[:300])
	}
	code, _ := e["code"].(string)
	if code == "" {
		t.Fatalf("error envelope has no code: %s", rr.Body.String()[:300])
	}
	return code
}

// apiReasons returns the error.reasons array.
func apiReasons(t *testing.T, rr *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	v := apiJSON(t, rr)
	e := v["error"].(map[string]any)
	var out []map[string]any
	if rs, ok := e["reasons"].([]any); ok {
		for _, r := range rs {
			out = append(out, r.(map[string]any))
		}
	}
	return out
}

func apiGet(t *testing.T, srv *web.Server, path string) map[string]any {
	t.Helper()
	rr := apiCall(t, srv, "GET", path, nil)
	if rr.Code != 200 {
		t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String()[:300])
	}
	return apiJSON(t, rr)
}

// waitRun polls a run until it is done or failed.
func waitRun(t *testing.T, srv *web.Server, runID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		v := apiGet(t, srv, "/api/v1/runs/"+runID)
		run := v["run"].(map[string]any)
		if run["status"] != "running" {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish", runID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAPIJourney walks the full contract on the demo fixtures.
func TestAPIJourney(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)

	// --- workspace & publications ---
	ws := apiGet(t, srv, "/api/v1/workspace")
	if ws["id"] != "local" || ws["mode"] != "demo" {
		t.Fatalf("workspace = %v", ws)
	}
	pubs := apiGet(t, srv, "/api/v1/publications")
	list := pubs["publications"].([]any)
	if len(list) != 1 {
		t.Fatalf("publications = %d, want 1", len(list))
	}
	pubID := fmt.Sprint(pub.ID)
	detail := apiGet(t, srv, "/api/v1/publications/"+pubID)
	p := detail["publication"].(map[string]any)
	if p["slug"] != "futuro-proximo" {
		t.Fatalf("publication slug = %v", p["slug"])
	}
	secs := p["sections"].([]any)
	if secs[0].(map[string]any)["commentary"] != "required" {
		t.Fatalf("first section commentary = %v", secs[0])
	}

	// --- sources ---
	srcs := apiGet(t, srv, "/api/v1/publications/"+pubID+"/sources")
	if len(srcs["sources"].([]any)) != 4 {
		t.Fatalf("sources = %v", srcs["sources"])
	}
	rr := apiCall(t, srv, "POST", "/api/v1/publications/"+pubID+"/sources",
		map[string]string{"type": "rss", "url": "https://feed.test/rss.xml"})
	if rr.Code != 201 {
		t.Fatalf("add source = %d: %s", rr.Code, rr.Body.String()[:200])
	}
	if v := apiJSON(t, rr); v["note"] == nil {
		t.Error("demo source add should carry the simulated-mode note")
	}
	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+pubID+"/sources",
		map[string]string{"type": "rss", "url": "not-a-url"})
	if c := apiErrCode(t, rr); c != "invalid_source" {
		t.Fatalf("bad source url: code = %q", c)
	}

	// --- run: start, poll, stream events ---
	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+pubID+"/runs", nil)
	if rr.Code != 202 {
		t.Fatalf("start run = %d", rr.Code)
	}
	runID := apiJSON(t, rr)["run"].(map[string]any)["id"].(string)
	run := waitRun(t, srv, runID)
	if run["status"] != "done" {
		t.Fatalf("run status = %v err=%v", run["status"], run["error"])
	}
	events := run["events"].([]any)
	var sawCollect, sawStorylist bool
	for _, e := range events {
		ev := e.(map[string]any)
		switch ev["step"] {
		case "collect":
			sawCollect = true
			if ev["counts"].(map[string]any)["items_read"].(float64) <= 0 {
				t.Error("collect event has no items_read count")
			}
		case "storylist":
			sawStorylist = true
		}
	}
	if !sawCollect || !sawStorylist {
		t.Fatalf("run events missing steps: %v", events)
	}
	// SSE replay: every step event plus the final done event
	rr = apiCall(t, srv, "GET", "/api/v1/runs/"+runID+"/events", nil)
	body := rr.Body.String()
	if !strings.Contains(body, "event: step") || !strings.Contains(body, "event: done") {
		t.Fatalf("SSE stream incomplete: %q", body[:200])
	}
	issueID := int64(run["issue_id"].(float64))
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	// --- issues & items ---
	issues := apiGet(t, srv, "/api/v1/publications/"+pubID+"/issues")
	if len(issues["issues"].([]any)) == 0 {
		t.Fatal("no issues listed")
	}
	cards := apiGet(t, srv, issueURL+"/items")
	items := cards["items"].([]any)
	if len(items) != 8 {
		t.Fatalf("issue items = %d, want 8", len(items))
	}
	card := func(i int) map[string]any { return items[i].(map[string]any) }
	if card(0)["title"] == "" || len(card(0)["outlets"].([]any)) == 0 {
		t.Errorf("card missing title/outlets: %v", card(0))
	}
	iid := func(i int) string { return fmt.Sprint(int64(card(i)["id"].(float64))) }

	// dossier
	d := apiGet(t, srv, "/api/v1/items/"+iid(0)+"/dossier")
	if d["dossier"] == nil || d["item"] == nil {
		t.Fatal("dossier response incomplete")
	}

	// --- decisions ---
	rr = apiCall(t, srv, "POST", issueURL+"/items/"+iid(1)+"/decisions", map[string]string{"kind": "reject"})
	if c := apiErrCode(t, rr); c != "reason_required" {
		t.Fatalf("reject without reason: code = %q", c)
	}
	rr = apiCall(t, srv, "POST", issueURL+"/items/"+iid(1)+"/decisions",
		map[string]string{"kind": "reject", "reason": "off the publication's angle"})
	if rr.Code != 200 {
		t.Fatalf("reject = %d", rr.Code)
	}
	if v := apiJSON(t, rr); v["item"].(map[string]any)["status"] != "rejected" {
		t.Fatalf("rejected item status = %v", v["item"])
	}
	// undo brings it back, then reject again for real
	rr = apiCall(t, srv, "POST", issueURL+"/items/"+iid(1)+"/decisions", map[string]string{"kind": "undo"})
	if v := apiJSON(t, rr); v["item"].(map[string]any)["status"] != "pending" {
		t.Fatalf("undo status = %v", v["item"])
	}
	apiCall(t, srv, "POST", issueURL+"/items/"+iid(1)+"/decisions",
		map[string]string{"kind": "reject", "reason": "off the publication's angle"})
	for _, i := range []int{2, 5, 7} {
		apiCall(t, srv, "POST", issueURL+"/items/"+iid(i)+"/decisions",
			map[string]string{"kind": "reject", "reason": "not for this issue"})
	}
	// alternative replaces the pauta
	rr = apiCall(t, srv, "POST", issueURL+"/items/"+iid(6)+"/decisions", map[string]string{"kind": "alternative"})
	if rr.Code != 200 {
		t.Fatalf("alternative = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	if v := apiJSON(t, rr); v["item"].(map[string]any)["status"] != "pending" {
		t.Fatalf("alternative replacement should be pending: %v", v["item"])
	}
	// approve what is left, including the alternative's replacement
	fresh := apiGet(t, srv, issueURL+"/items")
	for _, it := range fresh["items"].([]any) {
		m := it.(map[string]any)
		if m["status"] == "pending" {
			apiCall(t, srv, "POST", issueURL+"/items/"+fmt.Sprint(int64(m["id"].(float64)))+"/decisions",
				map[string]string{"kind": "approve"})
		}
	}
	if v := apiGet(t, srv, issueURL+"/items"); v["pending"].(float64) != 0 {
		t.Fatalf("pending after decisions = %v", v["pending"])
	}

	// --- draft as a run ---
	rr = apiCall(t, srv, "POST", issueURL+"/draft", nil)
	if rr.Code != 202 {
		t.Fatalf("draft = %d: %s", rr.Code, rr.Body.String()[:200])
	}
	draftRun := waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))
	if draftRun["status"] != "done" || draftRun["kind"] != "draft" {
		t.Fatalf("draft run = %v", draftRun)
	}

	// --- document ---
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	if doc["coverage"].(map[string]any)["total"].(float64) == 0 {
		t.Fatal("document has no sentences")
	}
	var flaggedID string
	var flaggedRaw string
	uncited := 0
	for _, s := range doc["sections"].([]any) {
		sec := s.(map[string]any)
		if sec["commentary"].(map[string]any)["mode"] == "" {
			t.Error("section missing commentary mode")
		}
		for _, i := range sec["items"].([]any) {
			it := i.(map[string]any)
			if len(it["sources"].([]any)) == 0 {
				t.Errorf("item %v has no sources", it["ref"])
			}
			for _, sn := range it["sentences"].([]any) {
				snt := sn.(map[string]any)
				if snt["kind"] == "fact" && len(snt["citations"].([]any)) == 0 {
					uncited++
				}
				if snt["flag"] != nil && flaggedID == "" {
					flaggedID = snt["id"].(string)
				}
			}
		}
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if len(iss.Flags) == 0 {
		t.Fatal("expected citation flags from the fixture")
	}
	if flaggedID == "" {
		t.Fatal("no flagged sentence in the document")
	}
	flaggedRaw = iss.Flags[0].Sentence

	// --- readiness gates approve ---
	rd := apiGet(t, srv, issueURL+"/readiness")
	if rd["ready"].(bool) {
		t.Fatal("issue with flags + missing commentary reports ready")
	}
	rr = apiCall(t, srv, "POST", issueURL+"/approve", nil)
	if c := apiErrCode(t, rr); c != "blocked" {
		t.Fatalf("approve with blockers: code = %q", c)
	}
	rs := apiReasons(t, rr)
	var hasFlagReason, hasCommReason bool
	for _, r := range rs {
		switch r["code"] {
		case "flags_unresolved":
			hasFlagReason = true
			if tgt, ok := r["target"].(map[string]any); !ok || tgt["ref"] == "" {
				t.Errorf("flags reason lacks a sentence pointer: %v", r)
			}
		case "commentary_required":
			hasCommReason = true
		}
	}
	if !hasFlagReason || !hasCommReason {
		t.Fatalf("blocked reasons incomplete: %v", rs)
	}

	// --- sentence mutations ---
	sents := "/api/v1/issues/" + fmt.Sprint(issueID) + "/sentences/"
	// unchanged edit keeps the flag
	rr = apiCall(t, srv, "PATCH", sents+flaggedID, map[string]string{"text": flaggedRaw})
	if c := apiErrCode(t, rr); c != "unchanged" {
		t.Fatalf("unchanged edit: code = %q", c)
	}
	// uncited edit refused
	rr = apiCall(t, srv, "PATCH", sents+flaggedID, map[string]string{"text": "Something entirely new."})
	if c := apiErrCode(t, rr); c != "citation_required" {
		t.Fatalf("uncited edit: code = %q", c)
	}
	// supported edit resolves the flag (grounded in a dossier fact)
	rr = apiCall(t, srv, "PATCH", sents+flaggedID,
		map[string]string{"text": "Comentários podem ser enviados até 15 de dezembro pelo portal do órgão [1]"})
	if rr.Code != 200 {
		t.Fatalf("supported edit = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	// remove the remaining flagged sentences
	for {
		iss, _ = db.GetIssue(ctx, issueID)
		if len(iss.Flags) == 0 {
			break
		}
		doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
		next := ""
	outer:
		for _, s := range doc["sections"].([]any) {
			for _, i := range s.(map[string]any)["items"].([]any) {
				for _, sn := range i.(map[string]any)["sentences"].([]any) {
					if sn.(map[string]any)["flag"] != nil {
						next = sn.(map[string]any)["id"].(string)
						break outer
					}
				}
			}
		}
		if next == "" {
			t.Fatalf("flags remain (%d) but no flagged sentence found", len(iss.Flags))
		}
		if rr := apiCall(t, srv, "DELETE", sents+next, nil); rr.Code != 200 {
			t.Fatalf("remove flagged sentence %s = %d: %s", next, rr.Code, rr.Body.String()[:200])
		}
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if len(iss.Flags) != 0 {
		t.Fatalf("flags left: %d", len(iss.Flags))
	}
	_ = uncited

	// --- commentary + approve ---
	rr = apiCall(t, srv, "PUT", issueURL+"/commentary",
		map[string]string{"section": "The big one", "text": "My editorial take on the regulator consultation."})
	if rr.Code != 200 {
		t.Fatalf("commentary = %d", rr.Code)
	}
	rd = apiGet(t, srv, issueURL+"/readiness")
	if !rd["ready"].(bool) {
		t.Fatalf("issue still not ready: %v", rd["checks"])
	}
	rr = apiCall(t, srv, "POST", issueURL+"/approve", nil)
	if rr.Code != 200 {
		t.Fatalf("approve = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	if v := apiJSON(t, rr); v["receipt"].(map[string]any)["approved_at"] == "" {
		t.Error("approval receipt lacks approved_at")
	}
	// edits after approval revert to draft
	apiCall(t, srv, "PUT", issueURL+"/commentary",
		map[string]string{"section": "The big one", "text": "v2."})
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "drafted" {
		t.Fatalf("edit after approve: status = %q", iss.Status)
	}
	apiCall(t, srv, "POST", issueURL+"/approve", nil)

	// --- export ---
	rr = apiCall(t, srv, "GET", issueURL+"/export?fmt=markdown", nil)
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), "markdown") {
		t.Fatalf("export md = %d %s", rr.Code, rr.Header().Get("Content-Type"))
	}
	rr = apiCall(t, srv, "GET", issueURL+"/export?fmt=html", nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "<h1>") {
		t.Fatalf("export html = %d", rr.Code)
	}

	// --- preview switches ---
	pv := apiGet(t, srv, issueURL+"/preview?reader=non-subscriber&surface=email")
	if !pv["gated"].(bool) || !strings.Contains(pv["body_md"].(string), "Subscribers only") {
		t.Error("non-subscriber preview is not gated")
	}
	pv = apiGet(t, srv, issueURL+"/preview?reader=subscriber")
	if strings.Contains(pv["body_md"].(string), "Subscribers only") {
		t.Error("subscriber preview is gated")
	}
	rr = apiCall(t, srv, "GET", issueURL+"/preview?reader=bogus", nil)
	if c := apiErrCode(t, rr); c != "validation" {
		t.Fatalf("bad preview reader: code = %q", c)
	}

	// --- send needs an explicit audience ---
	rr = apiCall(t, srv, "POST", issueURL+"/send", nil)
	if c := apiErrCode(t, rr); c != "validation" {
		t.Fatalf("send without audience: code = %q", c)
	}
	rr = apiCall(t, srv, "POST", issueURL+"/send", map[string]string{"audience": "test"})
	if rr.Code != 200 {
		t.Fatalf("send test = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	if v := apiJSON(t, rr); v["outbox_email_id"] == nil {
		t.Error("send response lacks outbox_email_id")
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "sent" {
		t.Fatalf("status after send = %q", iss.Status)
	}

	// --- sent issue is read-only ---
	rr = apiCall(t, srv, "PUT", issueURL+"/commentary",
		map[string]string{"section": "The big one", "text": "v3"})
	if c := apiErrCode(t, rr); c != "read_only" {
		t.Fatalf("commentary on sent issue: code = %q", c)
	}
	rr = apiCall(t, srv, "POST", issueURL+"/items/"+iid(0)+"/decisions", map[string]string{"kind": "approve"})
	if c := apiErrCode(t, rr); c != "read_only" {
		t.Fatalf("decision on sent issue: code = %q", c)
	}

	// --- outbox ---
	ob := apiGet(t, srv, "/api/v1/publications/"+pubID+"/outbox")
	emails := ob["emails"].([]any)
	if len(emails) == 0 {
		t.Fatal("outbox empty after send")
	}
	eid := fmt.Sprint(int64(emails[0].(map[string]any)["id"].(float64)))
	em := apiGet(t, srv, "/api/v1/outbox/"+eid)
	if em["email"].(map[string]any)["body_html"] == "" {
		t.Error("outbox email lacks rendered body")
	}

	// --- reader / paywall ---
	rd2 := apiGet(t, srv, "/api/v1/publications/"+pubID+"/reader")
	if rd2["subscribed"].(bool) {
		t.Fatal("demo reader starts unsubscribed")
	}
	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+pubID+"/paywall/simulate", nil)
	if v := apiJSON(t, rr); !v["subscribed"].(bool) || v["magic_link"] == "" {
		t.Fatalf("paywall simulate = %v", v)
	}
	rd2 = apiGet(t, srv, "/api/v1/publications/"+pubID+"/reader")
	if !rd2["subscribed"].(bool) {
		t.Fatal("reader not subscribed after simulate")
	}
	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+pubID+"/paywall/cancel", nil)
	if v := apiJSON(t, rr); v["subscribed"].(bool) {
		t.Fatal("reader still subscribed after cancel")
	}

	// --- guide & costs ---
	g := apiGet(t, srv, "/api/v1/publications/"+pubID+"/guide")
	if g["guide"].(map[string]any)["version"].(float64) < 1 {
		t.Error("rejections should have produced a guide version")
	}
	gv := apiGet(t, srv, "/api/v1/publications/"+pubID+"/guide/versions")
	if len(gv["versions"].([]any)) == 0 {
		t.Error("no guide versions")
	}
	c := apiGet(t, srv, "/api/v1/publications/"+pubID+"/costs")
	if len(c["issues"].([]any)) == 0 {
		t.Error("no cost rows")
	}

	// --- error envelope on unknown routes/ids ---
	rr = apiCall(t, srv, "GET", "/api/v1/issues/9999", nil)
	if c := apiErrCode(t, rr); c != "not_found" {
		t.Fatalf("unknown issue: code = %q", c)
	}
	rr = apiCall(t, srv, "GET", "/api/v1/nonsense", nil)
	if c := apiErrCode(t, rr); c != "not_found" {
		t.Fatalf("unknown route: code = %q", c)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("404 route not JSON: %s", ct)
	}
}

// TestAPIDraftBlocked: drafting with open decisions is a blocked action
// carrying the pending count.
func TestAPIDraftBlocked(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	_ = ctx
	issueID := runPhaseA(t, srv, db, pub.ID)
	rr := apiCall(t, srv, "POST", "/api/v1/issues/"+strconv.FormatInt(issueID, 10)+"/draft", nil)
	if c := apiErrCode(t, rr); c != "blocked" {
		t.Fatalf("draft with pending decisions: code = %q", c)
	}
	rs := apiReasons(t, rr)
	if len(rs) == 0 || rs[0]["code"] != "pending_decisions" {
		t.Fatalf("blocked reasons = %v", rs)
	}
}

// TestAPIInterpretation marks a flagged sentence as interpretation:
// the flag resolves and the sentence becomes an editor-attributed block.
func TestAPIInterpretation(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	issueID := runPhaseA(t, srv, db, pub.ID)
	iis, _ := db.IssueItems(ctx, issueID)
	issueURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10)
	for _, ii := range iis {
		apiCall(t, srv, "POST", issueURL+"/items/"+strconv.FormatInt(ii.ID, 10)+"/decisions",
			map[string]string{"kind": "approve"})
	}
	rr := apiCall(t, srv, "POST", issueURL+"/draft", nil)
	waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	var flaggedID string
outer:
	for _, s := range doc["sections"].([]any) {
		for _, i := range s.(map[string]any)["items"].([]any) {
			for _, sn := range i.(map[string]any)["sentences"].([]any) {
				if sn.(map[string]any)["flag"] != nil {
					flaggedID = sn.(map[string]any)["id"].(string)
					break outer
				}
			}
		}
	}
	if flaggedID == "" {
		t.Fatal("no flagged sentence")
	}
	rr = apiCall(t, srv, "POST", issueURL+"/sentences/"+flaggedID+"/interpretation",
		map[string]string{"text": "A leitura editorial é que a consulta pública muda o jogo."})
	if rr.Code != 200 {
		t.Fatalf("interpretation = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if len(iss.Flags) != 0 {
		t.Fatalf("interpretation left %d flags", len(iss.Flags))
	}
	if !strings.Contains(iss.BodyMD, "> _Interpretação: A leitura editorial") {
		t.Fatal("interpretation not stored as an editor-attributed block")
	}
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	found := false
	for _, s := range doc["sections"].([]any) {
		for _, i := range s.(map[string]any)["items"].([]any) {
			for _, sn := range i.(map[string]any)["sentences"].([]any) {
				if sn.(map[string]any)["kind"] == "interpretation" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("no interpretation sentence in the reparsed document")
	}
}
