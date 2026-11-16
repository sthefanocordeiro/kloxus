package web_test

// Contract tests for KLOX-52: an uncited fact sentence must surface the
// same flag treatment as a citecheck-flagged one — Edit, mark as
// interpretation, Remove — no matter which write path produced it.
// Before this, a sentence added (or restored) without a stored flag
// blocked approval while showing nothing in the editor: the owner had
// to resolve it through the API.

import (
	"context"
	"fmt"
	"testing"
)

// seedUncitedBody writes a drafted body with one cited item and one
// uncited fact carrying no stored flag — the shape a flag-less restore
// or a pre-fix compose leaves behind.
func seedUncitedBody(t *testing.T, ctx context.Context, db interface {
	UpdateIssue(context.Context, int64, string, string, string) error
}, issueID int64, flags string) {
	t.Helper()
	body := "## Lead\n\n### Story A <!-- item 1 -->\n\n" +
		"Supported claim. [1] Also supported. [1]\n\n" +
		"**Sources:** [1] X\n\n" +
		"### Story B <!-- item 2 -->\n\n" +
		"An uncited claim with no stored flag.\n\n" +
		"**Sources:** [2] Y"
	if err := db.UpdateIssue(ctx, issueID, "drafted", body, flags); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
}

func docSentenceByID(doc map[string]any, sid string) map[string]any {
	for _, it := range docItems(doc) {
		for _, sn := range it["sentences"].([]any) {
			s := sn.(map[string]any)
			if s["id"] == sid {
				return s
			}
		}
	}
	return nil
}

// TestUncitedSentenceFlagAndResolution walks the audit's dead-end: an
// uncited fact with no stored flag must still show the flag treatment,
// stay removable and restorable-with-flag, and be clearable through the
// bulk action — never an unapprovable dead-end.
func TestUncitedSentenceFlagAndResolution(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	seedUncitedBody(t, ctx, db, issueID, "[]")

	// the document surfaces the uncited fact with the flag treatment —
	// no stored flag needed (KLOX-52)
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	ver := doc["version"].(string)
	var uncitedID string
	for _, it := range docItems(doc) {
		for _, sn := range it["sentences"].([]any) {
			s := sn.(map[string]any)
			if s["kind"] == "fact" && len(s["citations"].([]any)) == 0 {
				uncitedID = s["id"].(string)
				fl, _ := s["flag"].(map[string]any)
				if fl == nil || fl["note"] != "sentence has no citation" {
					t.Fatalf("uncited sentence must carry the flag note, got %v", s["flag"])
				}
			}
		}
	}
	if uncitedID == "" {
		t.Fatal("seeded body produced no uncited sentence")
	}
	rd := apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "uncited_sentences"); c == nil || c["ok"].(bool) {
		t.Fatalf("uncited_sentences check should fail: %v", c)
	}

	// removing it works through the normal sentence API, and the undo
	// payload carries the flag the document showed
	rr := apiCall(t, srv, "DELETE", issueURL+"/sentences/"+uncitedID,
		map[string]any{"version": ver})
	if rr.Code != 200 {
		t.Fatalf("remove uncited = %d: %s", rr.Code, shortBody(rr))
	}
	v := apiJSON(t, rr)
	removed, _ := v["removed"].(map[string]any)
	if removed["flag"] != "sentence has no citation" {
		t.Fatalf("undo payload flag = %v", removed["flag"])
	}
	ver = v["version"].(string)

	// restoring it WITHOUT the flag field — what an old undo payload or
	// a hand-built restore sends — must not resurrect the dead-end: the
	// sentence comes back uncited and the document flags it anyway
	rr = apiCall(t, srv, "POST", issueURL+"/items/2/sentences", map[string]any{
		"index": removed["index"], "text": removed["text"], "kind": "fact",
		"citations": []int{}, "version": ver,
	})
	if rr.Code != 200 {
		t.Fatalf("flag-less restore = %d: %s", rr.Code, shortBody(rr))
	}
	ver = apiJSON(t, rr)["version"].(string)
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	restored := docSentenceByID(doc, uncitedID)
	if restored == nil {
		t.Fatal("restored sentence not in document")
	}
	if fl, _ := restored["flag"].(map[string]any); fl == nil {
		t.Fatal("flag-less restored sentence must still show the flag treatment")
	}

	// the bulk action removes the synthesized-flag sentence too
	rr = apiCall(t, srv, "DELETE", issueURL+"/items/2/sentences",
		map[string]any{"version": ver})
	if rr.Code != 200 {
		t.Fatalf("bulk remove synthesized flag = %d: %s", rr.Code, shortBody(rr))
	}
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	if docSentenceByID(doc, uncitedID) != nil {
		t.Fatal("bulk remove left the uncited sentence behind")
	}
	rd = apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "uncited_sentences"); !c["ok"].(bool) {
		t.Fatalf("uncited_sentences should clear after removal: %v", c)
	}
}

// TestItemLevelFlagClearsThroughBulk: a stored flag that matches no
// sentence ("entry has no citations", or a flag whose sentence was
// edited away) must not leave the issue unapprovable with no UI
// recourse — readiness points at the item and the bulk action clears it.
func TestItemLevelFlagClearsThroughBulk(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	// item 1 carries only cited sentences plus a stored flag that
	// matches none of them — unreachable through any sentence action
	seedUncitedBody(t, ctx, db, issueID,
		`[{"item":1,"sentence":"Story A whole text","note":"entry has no citations"}]`)

	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	ver := doc["version"].(string)
	it := findItem(t, doc, 1)
	if len(it["flags"].([]any)) != 1 {
		t.Fatalf("item-level flags = %v, want 1", it["flags"])
	}
	rd := apiGet(t, srv, issueURL+"/readiness")
	c := readinessCheck(rd, "flags_unresolved")
	if c == nil || c["ok"].(bool) {
		t.Fatalf("flags_unresolved should fail: %v", c)
	}
	tg, _ := c["target"].(map[string]any)
	if tg == nil || tg["kind"] != "item" || tg["ref"] != "1" {
		t.Fatalf("item-level flag should target the item, got %v", tg)
	}

	// the bulk action is the recourse: it clears the stale flag even
	// though the item has no flagged sentences to remove
	rr := apiCall(t, srv, "DELETE", issueURL+"/items/1/sentences",
		map[string]any{"version": ver})
	if rr.Code != 200 {
		t.Fatalf("bulk remove on orphan-flag item = %d: %s", rr.Code, shortBody(rr))
	}
	v := apiJSON(t, rr)
	if int(v["flags"].(float64)) != 0 {
		t.Fatalf("flags after orphan clear = %v", v["flags"])
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if len(iss.Flags) != 0 {
		t.Fatalf("stored flags after orphan clear = %d", len(iss.Flags))
	}
	rd = apiGet(t, srv, issueURL+"/readiness")
	if c := readinessCheck(rd, "flags_unresolved"); !c["ok"].(bool) {
		t.Fatalf("flags_unresolved should clear: %v", c)
	}
	// item 2's uncited fact still blocks on its own check — no silent
	// pass sneaks in through the flag path
	if c := readinessCheck(rd, "uncited_sentences"); c["ok"].(bool) {
		t.Fatal("uncited_sentences should still fail for item 2")
	}
}
