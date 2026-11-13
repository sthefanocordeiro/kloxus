package web_test

// Contract tests for the /api/v1 JSON interface (docs/api.md). They walk
// the whole demo journey — run, triage, draft, fix flags, commentary,
// approve, send, reader — and assert the error envelope on blocked and
// invalid actions.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	// A fixture run issues hundreds of queries; against a remote
	// Postgres each one is a network round-trip, so allow ample time.
	deadline := time.Now().Add(3 * time.Minute)
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
	cov := doc["coverage"].(map[string]any)
	if cov["total"].(float64) == 0 {
		t.Fatal("document has no sentences")
	}
	if _, ok := cov["ok"]; !ok {
		t.Fatal("coverage lacks the ok counter")
	}
	if doc["version"] == "" {
		t.Fatal("document has no version")
	}
	var flaggedID, flaggedText string
	var flaggedCits []any
	sawEvidence := false
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
				cits := snt["citations"].([]any)
				if snt["kind"] == "fact" && len(cits) == 0 {
					uncited++
				}
				if len(cits) > 0 {
					if ev, ok := snt["evidence"].([]any); ok && len(ev) == len(cits) {
						sawEvidence = true
					}
				}
				if snt["flag"] != nil && flaggedID == "" {
					flaggedID = snt["id"].(string)
					flaggedText = snt["text"].(string)
					flaggedCits = cits
				}
			}
		}
	}
	if !sawEvidence {
		t.Error("no cited sentence carries per-citation evidence")
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if len(iss.Flags) == 0 {
		t.Fatal("expected citation flags from the fixture")
	}
	if flaggedID == "" {
		t.Fatal("no flagged sentence in the document")
	}

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
	ver := doc["version"].(string)
	// unchanged edit keeps the flag
	rr = apiCall(t, srv, "PATCH", sents+flaggedID,
		map[string]any{"text": flaggedText, "citations": flaggedCits, "version": ver})
	if c := apiErrCode(t, rr); c != "unchanged" {
		t.Fatalf("unchanged edit: code = %q", c)
	}
	// uncited edit refused
	rr = apiCall(t, srv, "PATCH", sents+flaggedID,
		map[string]any{"text": "Something entirely new.", "version": ver})
	if c := apiErrCode(t, rr); c != "citation_required" {
		t.Fatalf("uncited edit: code = %q", c)
	}
	// supported edit resolves the flag (grounded in a dossier fact)
	rr = apiCall(t, srv, "PATCH", sents+flaggedID,
		map[string]any{"text": "Comentários podem ser enviados até 15 de dezembro pelo portal do órgão",
			"citations": []int{1}, "version": ver})
	if rr.Code != 200 {
		t.Fatalf("supported edit = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	// a stale version is a conflict carrying the fresh document
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	var anySID string
outer0:
	for _, s := range doc["sections"].([]any) {
		for _, i := range s.(map[string]any)["items"].([]any) {
			for _, sn := range i.(map[string]any)["sentences"].([]any) {
				anySID = sn.(map[string]any)["id"].(string)
				break outer0
			}
		}
	}
	rr = apiCall(t, srv, "PATCH", sents+anySID,
		map[string]any{"text": "Comentários podem ser enviados até 15 de dezembro pelo portal do órgão",
			"citations": []int{1}, "version": ver})
	if c := apiErrCode(t, rr); c != "conflict" {
		t.Fatalf("stale version: code = %q", c)
	}
	if v := apiJSON(t, rr); v["document"] == nil {
		t.Error("conflict response must carry the fresh document")
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
		if rr := apiCall(t, srv, "DELETE", sents+next,
			map[string]any{"version": doc["version"].(string)}); rr.Code != 200 {
			t.Fatalf("remove flagged sentence %s = %d: %s", next, rr.Code, rr.Body.String()[:200])
		}
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if len(iss.Flags) != 0 {
		t.Fatalf("flags left: %d", len(iss.Flags))
	}
	_ = uncited

	// --- commentary + approve ---
	ver = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["version"].(string)
	rr = apiCall(t, srv, "PUT", issueURL+"/commentary",
		map[string]string{"section": "The big one", "text": "My editorial take on the regulator consultation.", "version": ver})
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
	rec := apiJSON(t, rr)["receipt"].(map[string]any)
	if rec["approved_at"] == "" || rec["approver"] == "" || rec["document_version"] == "" {
		t.Errorf("approval receipt incomplete: %v", rec)
	}
	// a repeated approve returns the stored receipt, not a new timestamp
	rr = apiCall(t, srv, "POST", issueURL+"/approve", nil)
	if rec2 := apiJSON(t, rr)["receipt"].(map[string]any); rec2["approved_at"] != rec["approved_at"] {
		t.Error("repeated approve minted a new receipt")
	}
	// edits after approval revert to draft and void the receipt
	ver = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["version"].(string)
	apiCall(t, srv, "PUT", issueURL+"/commentary",
		map[string]string{"section": "The big one", "text": "v2.", "version": ver})
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "drafted" {
		t.Fatalf("edit after approve: status = %q", iss.Status)
	}
	if v := apiGet(t, srv, issueURL)["issue"].(map[string]any); v["receipt"] != nil {
		t.Error("post-approval edit should void the receipt")
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
	if !pv["gated"].(bool) || !strings.Contains(pv["body_md"].(string), "Exclusivo para assinantes") {
		t.Error("non-subscriber preview is not gated")
	}
	pv = apiGet(t, srv, issueURL+"/preview?reader=subscriber")
	if strings.Contains(pv["body_md"].(string), "Subscribers only") || strings.Contains(pv["body_md"].(string), "Exclusivo para assinantes") {
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
	// a test send records the outbox email but leaves the issue approved
	rr = apiCall(t, srv, "POST", issueURL+"/send", map[string]string{"audience": "test"})
	if rr.Code != 200 {
		t.Fatalf("send test = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	if v := apiJSON(t, rr); v["outbox_email_id"] == nil {
		t.Error("send response lacks outbox_email_id")
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "approved" {
		t.Fatalf("test send marked issue %q — it must stay approved", iss.Status)
	}

	// --- schedule locks the issue; Hold returns it to draft ---
	// a test send cannot be scheduled
	rr = apiCall(t, srv, "POST", issueURL+"/send",
		map[string]string{"audience": "test", "when": "schedule",
			"at": time.Now().Add(24 * time.Hour).Format(time.RFC3339)})
	if c := apiErrCode(t, rr); c != "validation" {
		t.Fatalf("schedule a test send: code = %q", c)
	}
	// scheduling needs a send time, in the future
	rr = apiCall(t, srv, "POST", issueURL+"/send",
		map[string]string{"audience": "readers", "when": "schedule"})
	if c := apiErrCode(t, rr); c != "bad_time" {
		t.Fatalf("schedule without at: code = %q", c)
	}
	rr = apiCall(t, srv, "POST", issueURL+"/send",
		map[string]string{"audience": "readers", "when": "schedule", "at": "tomorrow-ish"})
	if c := apiErrCode(t, rr); c != "bad_time" {
		t.Fatalf("schedule with malformed at: code = %q", c)
	}
	rr = apiCall(t, srv, "POST", issueURL+"/send",
		map[string]string{"audience": "readers", "when": "schedule",
			"at": time.Now().Add(-time.Hour).Format(time.RFC3339)})
	if c := apiErrCode(t, rr); c != "bad_time" {
		t.Fatalf("schedule in the past: code = %q", c)
	}
	at := time.Now().Add(26 * time.Hour).UTC().Truncate(time.Second)
	rr = apiCall(t, srv, "POST", issueURL+"/send",
		map[string]string{"audience": "readers", "when": "schedule", "at": at.Format(time.RFC3339)})
	if rr.Code != 200 {
		t.Fatalf("schedule = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	if v := apiJSON(t, rr); !v["scheduled"].(bool) || v["scheduled_at"] != at.Format(time.RFC3339) {
		t.Errorf("schedule response = %v", v)
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "scheduled" || iss.ScheduledAt != at.Format(time.RFC3339) {
		t.Fatalf("after schedule: status = %q at = %q", iss.Status, iss.ScheduledAt)
	}
	if v := apiGet(t, srv, issueURL)["issue"].(map[string]any); v["scheduled_at"] != at.Format(time.RFC3339) {
		t.Error("issue summary must carry scheduled_at")
	}
	// a scheduled issue is locked like a sent one
	rr = apiCall(t, srv, "PUT", issueURL+"/commentary",
		map[string]string{"section": "The big one", "text": "v3"})
	if c := apiErrCode(t, rr); c != "read_only" {
		t.Fatalf("commentary on scheduled issue: code = %q", c)
	}
	// hold is a no-op for a non-scheduled issue — here it unlocks
	rr = apiCall(t, srv, "POST", issueURL+"/hold", nil)
	if rr.Code != 200 {
		t.Fatalf("hold = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "drafted" {
		t.Fatalf("status after hold = %q — Hold must return to draft", iss.Status)
	}
	if iss.ScheduledAt != "" {
		t.Error("hold must clear the queued send time")
	}
	if v := apiGet(t, srv, issueURL)["issue"].(map[string]any); v["receipt"] != nil {
		t.Error("hold should drop the approval receipt")
	}
	rr = apiCall(t, srv, "POST", issueURL+"/hold", nil)
	if c := apiErrCode(t, rr); c != "validation" {
		t.Fatalf("hold on unscheduled issue: code = %q", c)
	}
	// re-approve, then the readers send marks it sent
	if rr := apiCall(t, srv, "POST", issueURL+"/approve", nil); rr.Code != 200 {
		t.Fatalf("re-approve after hold = %d", rr.Code)
	}
	rr = apiCall(t, srv, "POST", issueURL+"/send", map[string]string{"audience": "readers"})
	if rr.Code != 200 {
		t.Fatalf("send readers = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "sent" {
		t.Fatalf("status after send = %q", iss.Status)
	}

	// --- after sending: public URL, downloads, learned rules ---
	af := apiGet(t, srv, issueURL+"/after")
	if af["public_url"] == "" || !strings.Contains(af["public_url"].(string), "/p/futuro-proximo/") {
		t.Fatalf("after: public_url = %v", af["public_url"])
	}
	if strings.Contains(af["public_url"].(string), "ws=") {
		t.Error("public_url must never carry the workspace id")
	}
	if af["export_html"] == nil || af["export_md"] == nil {
		t.Error("after: export links missing")
	}
	if _, ok := af["learned"].([]any); !ok {
		t.Error("after: learned must be an array")
	}
	// after is blocked before the issue ships — covered by the 409 above?

	// --- public reader page: gated for non-subscribers ---
	pu := apiGet(t, srv, "/api/v1/p/futuro-proximo/"+fmt.Sprint(iss.Number))
	if pu["gated"] != true {
		t.Error("public page should be gated for the unsubscribed demo reader")
	}
	pubBody := pu["body_md"].(string)
	if !strings.Contains(pubBody, "Exclusivo para assinantes") {
		t.Error("public page leaked the paid section to a non-subscriber")
	}
	if pu["issue"].(map[string]any)["number"].(float64) != float64(iss.Number) {
		t.Error("public page issue number mismatch")
	}
	// forced free view stays gated even with a key
	pu2 := apiGet(t, srv, "/api/v1/p/futuro-proximo/"+fmt.Sprint(iss.Number)+"?view=free&key=whatever")
	if pu2["gated"] != true {
		t.Error("view=free must stay gated")
	}
	rr = apiCall(t, srv, "GET", "/api/v1/p/futuro-proximo/999", nil)
	if rr.Code != 404 {
		t.Fatalf("public page for missing issue = %d", rr.Code)
	}
	// the short public URL serves the reader page at the root now; a
	// legacy /app/* path serves the same SPA response without redirecting —
	// browsers that cached the old "/" → "/app/" 301 must not loop.
	root := apiCall(t, srv, "GET", "/p/futuro-proximo/"+fmt.Sprint(iss.Number), nil)
	rr = apiCall(t, srv, "GET", "/app/p/futuro-proximo/"+fmt.Sprint(iss.Number), nil)
	if loc := rr.Header().Get("Location"); loc != "" || rr.Code != root.Code {
		t.Fatalf("/app/p = %d Location %q (want %d, no redirect)", rr.Code, loc, root.Code)
	}
	// an unsent issue is not publicly visible — the drafted issue number
	// belongs to this same fixture set, so probe a non-existent status:
	// /after on a sent issue works (above); a drafted issue would 409.

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
	// mutating calls must declare a JSON body
	req := httptest.NewRequest("POST", issueURL+"/send", strings.NewReader("{}"))
	raw := httptest.NewRecorder()
	srv.ServeHTTP(raw, req)
	if raw.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("POST without Content-Type = %d, want 415", raw.Code)
	}
	if c := apiErrCode(t, raw); c != "unsupported_media_type" {
		t.Fatalf("415 code = %q", c)
	}
}

// TestAPIDraftLineupGate: the untouched AI suggestion drafts straight
// away (pending items are the lineup), but a lineup that leaves a
// section empty is blocked with per-section reasons.
func TestAPIDraftLineupGate(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10)

	// empty every section via the lineup → draft blocked with reasons
	if rr := apiCall(t, srv, "PUT", issueURL+"/lineup", map[string]any{"entries": []any{}}); rr.Code != 200 {
		t.Fatalf("PUT empty lineup = %d", rr.Code)
	}
	rr := apiCall(t, srv, "POST", issueURL+"/draft", nil)
	if c := apiErrCode(t, rr); c != "blocked" {
		t.Fatalf("draft with empty sections: code = %q", c)
	}
	rs := apiReasons(t, rr)
	if len(rs) == 0 || rs[0]["code"] != "section_empty" {
		t.Fatalf("blocked reasons = %v", rs)
	}
	_ = ctx
}

// TestAPIInterpretation marks a flagged sentence as interpretation:
// the flag resolves and the sentence becomes an editor-attributed block.
// TestAPILineup: the build board contract — candidates expose the
// ranked pool, PUT lineup sets sections/slots, capacity is enforced,
// removal without a reason is not a rejection, and a rejected story
// that comes back rebuilds the guide.
func TestAPILineup(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10)

	get := apiGet(t, srv, issueURL+"/candidates")
	cands, _ := get["candidates"].([]any)
	if len(cands) == 0 {
		t.Fatal("no candidates")
	}
	secs, _ := get["sections"].([]any)
	if len(secs) == 0 {
		t.Fatal("no sections exposed")
	}
	byStory := map[float64]map[string]any{}
	bench := []map[string]any{}
	inIssue := []map[string]any{}
	for _, c := range cands {
		m := c.(map[string]any)
		sid := m["story_id"].(float64)
		byStory[sid] = m
		if m["in_issue"].(bool) {
			inIssue = append(inIssue, m)
		} else {
			bench = append(bench, m)
		}
	}
	if len(inIssue) == 0 {
		t.Fatal("AI suggestion did not pre-fill the lineup")
	}
	first := inIssue[0]
	if first["suggested_section"] == "" {
		t.Fatalf("phaseA item lacks suggested_section: %v", first)
	}

	// overfill a capacity-1 section → 409 section_full
	cap1 := ""
	for _, sc := range secs {
		sm := sc.(map[string]any)
		if sm["capacity"].(float64) == 1 {
			cap1 = sm["title"].(string)
		}
	}
	if cap1 == "" {
		t.Fatal("fixture lacks a capacity-1 section")
	}
	var two []any
	two = append(two, inIssue[0]["story_id"])
	for _, c := range cands {
		m := c.(map[string]any)
		if !m["in_issue"].(bool) {
			two = append(two, m["story_id"])
			break
		}
	}
	rr := apiCall(t, srv, "PUT", issueURL+"/lineup", map[string]any{"entries": []any{
		map[string]any{"story_id": int64(two[0].(float64)), "section": cap1},
		map[string]any{"story_id": int64(two[1].(float64)), "section": cap1},
	}})
	if c := apiErrCode(t, rr); c != "section_full" {
		t.Fatalf("overfill: code = %q, want section_full", c)
	}

	// remove one suggested item without a reason → removed, not rejected
	victim := inIssue[0]
	entries := []any{}
	for _, mm := range inIssue[1:] {
		entries = append(entries, map[string]any{"story_id": int64(mm["story_id"].(float64)), "section": mm["section"]})
	}
	rr = apiCall(t, srv, "PUT", issueURL+"/lineup", map[string]any{"entries": entries})
	if rr.Code != 200 {
		t.Fatalf("PUT lineup = %d: %s", rr.Code, rr.Body.String())
	}
	after := apiGet(t, srv, issueURL+"/candidates")
	var found map[string]any
	for _, c := range after["candidates"].([]any) {
		m := c.(map[string]any)
		if m["story_id"] == victim["story_id"] {
			found = m
		}
	}
	if found == nil {
		t.Fatal("removed story vanished from candidates")
	}
	if found["in_issue"].(bool) || found["status"] != "removed" {
		t.Fatalf("removed story state = %v", found)
	}
	iis, _ := db.IssueItems(ctx, issueID)
	for _, ii := range iis {
		if ii.StoryID == int64(victim["story_id"].(float64)) && ii.Status == "rejected" {
			t.Fatal("reason-free removal became a rejection")
		}
	}

	// add a bench story into a real section, keep the rest
	if len(bench) == 0 {
		t.Skip("no bench candidates in fixture")
	}
	added := bench[0]
	entries = append(entries, map[string]any{"story_id": int64(added["story_id"].(float64)), "section": "Quick links"})
	rr = apiCall(t, srv, "PUT", issueURL+"/lineup", map[string]any{"entries": entries})
	if rr.Code != 200 {
		t.Fatalf("PUT lineup+add = %d: %s", rr.Code, rr.Body.String())
	}
	ii, err := db.IssueItemByStory(ctx, issueID, int64(added["story_id"].(float64)))
	if err != nil || ii.Status != "approved" || ii.Origin != "manual" {
		t.Fatalf("added story item = %+v err=%v", ii, err)
	}
}

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
		map[string]string{"text": "A leitura editorial é que a consulta pública muda o jogo.",
			"version": doc["version"].(string)})
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

// a learned rule carries the story it applies to — a bare chip label
// like "too early" must never reach the guide on its own.
func TestAPILearnedRuleContext(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10)
	iis, _ := db.IssueItems(ctx, issueID)
	var itemID int64
	var title string
	for _, ii := range iis {
		if ii.Status == "pending" || ii.Status == "approved" {
			itemID = ii.ID
			title, _ = db.StoryTitle(ctx, ii.StoryID)
			break
		}
	}
	if itemID == 0 || title == "" {
		t.Skip("no pending item")
	}
	rr := apiCall(t, srv, "POST", issueURL+"/items/"+strconv.FormatInt(itemID, 10)+"/decisions",
		map[string]string{"kind": "reject", "reason": "too early"})
	if rr.Code != 200 {
		t.Fatalf("reject = %d: %s", rr.Code, rr.Body.String())
	}
	g := apiGet(t, srv, "/api/v1/publications/"+strconv.FormatInt(pub.ID, 10)+"/guide")
	content := g["guide"].(map[string]any)["content"].(string)
	// the learned rule generalizes the chip — it names the kind of
	// story to skip, never the specific headline
	if strings.Contains(content, title) {
		t.Fatalf("learned rule leaked a headline: %q", content)
	}
	if !strings.Contains(content, "Skip stories that just broke") {
		t.Fatalf("chip reason was not generalized into a rule: %q", content)
	}
	for line := range strings.Lines(content) {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-")) == "Too early" {
			t.Fatalf("bare chip label stored as a rule: %q", content)
		}
	}
}

// TestAPIOnboarding covers the create-newsletter contract end to end: the
// landing sample, the proposal blocks, publication creation with the
// custom flag, the sources screen catalog/preview/confirm/delete, and —
// the critical invariant — a run on the custom publication still replays
// the demo fixtures via canonical prompt substitution.
func TestAPIOnboarding(t *testing.T) {
	srv, db, _, _ := demoServer(t)
	ctx := context.Background()

	// the landing page's cited sample — workspace-free
	rr := apiCall(t, srv, "GET", "/api/v1/sample", nil)
	if rr.Code != 200 {
		t.Fatalf("GET sample = %d", rr.Code)
	}
	if dossiers, _ := apiJSON(t, rr)["dossiers"].([]any); len(dossiers) == 0 {
		t.Fatal("sample has no dossiers")
	}

	// the proposal carries the editable blocks + offered sources
	prop, _ := apiGet(t, srv, "/api/v1/proposal")["proposal"].(map[string]any)
	for _, k := range []string{"name", "audience", "editorial_line", "tone", "sections", "sources"} {
		if prop[k] == nil {
			t.Fatalf("proposal missing %s", k)
		}
	}
	if secs, _ := prop["sections"].([]any); len(secs) == 0 {
		t.Fatal("proposal has no sections")
	}

	// pasted material is live-only in demo mode
	if code := apiErrCode(t, apiCall(t, srv, "POST", "/api/v1/publications/propose",
		map[string]any{"material": "https://example.com/issue"})); code != "live_only" {
		t.Fatalf("propose = %q, want live_only", code)
	}

	// create — the blocks become a custom publication
	body := map[string]any{
		"name": prop["name"], "topic": prop["topic"], "audience": prop["audience"],
		"editorial_line": prop["editorial_line"], "tone": prop["tone"],
		"language": prop["language"], "frequency": prop["frequency"],
		"ai_disclosure": prop["ai_disclosure"], "sections": prop["sections"],
		"paywall": prop["paywall"],
	}
	rr = apiCall(t, srv, "POST", "/api/v1/publications", body)
	if rr.Code != 201 {
		t.Fatalf("create publication = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	pub, _ := apiJSON(t, rr)["publication"].(map[string]any)
	pubID := fmt.Sprint(int64(pub["id"].(float64)))
	if pub["custom"] != true {
		t.Fatal("created publication is not flagged custom")
	}
	if pub["slug"] == "futuro-proximo" {
		t.Fatal("slug collided with the seeded publication")
	}

	// an empty submission fails validation
	if code := apiErrCode(t, apiCall(t, srv, "POST", "/api/v1/publications", map[string]any{})); code != "validation" {
		t.Fatalf("empty create = %q", code)
	}

	base := "/api/v1/publications/" + pubID

	// the sources screen offers the fixture sources not yet added
	offered, _ := apiGet(t, srv, base+"/sources/catalog")["offered"].([]any)
	if len(offered) == 0 {
		t.Fatal("catalog offers nothing")
	}
	first := offered[0].(map[string]any)

	// preview a fixture source → real items; a pasted URL → simulated
	// note; a malformed one → invalid_source
	pv := apiGet(t, srv, fmt.Sprintf("%s/sources/preview?type=%s&url=%s",
		base, first["type"], url.QueryEscape(first["url"].(string))))
	if n := len(pv["items"].([]any)); n == 0 || n > 3 {
		t.Fatalf("fixture preview returned %d items", n)
	}
	pv = apiGet(t, srv, base+"/sources/preview?type=rss&url="+url.QueryEscape("https://example.com/feed"))
	if pv["note"] == nil {
		t.Fatal("pasted-URL preview has no simulated note")
	}
	if code := apiErrCode(t, apiCall(t, srv, "GET", base+"/sources/preview?type=rss&url=notaurl", nil)); code != "invalid_source" {
		t.Fatalf("bad preview = %q", code)
	}

	// confirm adds everything offered and marks onboarding done
	rr = apiCall(t, srv, "POST", base+"/sources/confirm", nil)
	if rr.Code != 200 {
		t.Fatalf("confirm = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	srcs, _ := apiJSON(t, rr)["sources"].([]any)
	if len(srcs) != len(offered) {
		t.Fatalf("confirm added %d sources, want %d", len(srcs), len(offered))
	}
	if _, ok := srcs[0].(map[string]any)["items_per_week"]; !ok {
		t.Fatal("source row has no items_per_week")
	}
	if left, _ := apiGet(t, srv, base+"/sources/catalog")["offered"].([]any); len(left) != 0 {
		t.Fatal("catalog still offers confirmed sources")
	}

	// the run on the custom publication replays demo fixtures — canonical
	// substitution keeps edited blocks from breaking prompt-hash lookups
	rr = apiCall(t, srv, "POST", base+"/runs", nil)
	if rr.Code != 202 {
		t.Fatalf("start run = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	run, _ := apiJSON(t, rr)["run"].(map[string]any)
	done := waitRun(t, srv, run["id"].(string))
	if done["status"] != "done" {
		t.Fatalf("run on custom publication failed: %v", done["error"])
	}
	if done["issue_id"] == nil {
		t.Fatal("run produced no issue")
	}

	// and a source can be removed afterwards
	sid := fmt.Sprint(int64(srcs[0].(map[string]any)["id"].(float64)))
	if rr := apiCall(t, srv, "DELETE", base+"/sources/"+sid, nil); rr.Code != 200 {
		t.Fatalf("delete source = %d", rr.Code)
	}
	if n := len(apiGet(t, srv, base+"/sources")["sources"].([]any)); n != len(srcs)-1 {
		t.Fatalf("sources after delete = %d", n)
	}

	// --- paywall regression on a custom (JSON-created) publication:
	// both reader outputs gate the paid section for a non-subscriber —
	// teaser only, the wall, nothing below it ---
	issueID := int64(done["issue_id"].(float64))
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	rr = apiCall(t, srv, "POST", issueURL+"/draft", nil)
	if rr.Code != 202 {
		t.Fatalf("draft on custom pub = %d: %s", rr.Code, rr.Body.String()[:200])
	}
	if dr := waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string)); dr["status"] != "done" {
		t.Fatalf("draft run on custom pub failed: %v", dr["error"])
	}
	// approve readiness (flags/commentary) is covered in TestAPIJourney;
	// this test targets the gating on the two reader outputs
	if err := db.SetIssueStatus(ctx, issueID, "approved"); err != nil {
		t.Fatalf("approve on custom pub: %v", err)
	}
	if rr := apiCall(t, srv, "POST", issueURL+"/send", map[string]string{"audience": "readers"}); rr.Code != 200 {
		t.Fatalf("send on custom pub = %d: %s", rr.Code, rr.Body.String()[:200])
	}
	pp := apiGet(t, srv, "/api/v1/p/"+pub["slug"].(string)+"/1")
	if pp["gated"] != true {
		t.Fatalf("custom-pub public page not gated: %v", pp["gated"])
	}
	if md := pp["body_md"].(string); !strings.Contains(md, "Exclusivo para assinantes") ||
		strings.Contains(md, "Worth knowing") {
		t.Errorf("public page leaked the paid section: %.200s", md)
	}
	var issueEmail map[string]any
	for _, e := range apiGet(t, srv, base+"/outbox")["emails"].([]any) {
		if e.(map[string]any)["kind"] == "issue" {
			issueEmail = e.(map[string]any)
		}
	}
	if issueEmail == nil {
		t.Fatal("no readers email in outbox")
	}
	em := apiGet(t, srv, "/api/v1/outbox/"+fmt.Sprint(int64(issueEmail["id"].(float64))))["email"].(map[string]any)
	if md, _ := em["body_md"].(string); !strings.Contains(md, "Exclusivo para assinantes") ||
		strings.Contains(md, "Worth knowing") {
		t.Errorf("readers email leaked the paid section: %.200s", md)
	}
}
