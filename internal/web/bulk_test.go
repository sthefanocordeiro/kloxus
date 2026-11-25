package web_test

// Contract tests for the bulk remove-flagged action and its batch undo
// (KLOX-47): DELETE /issues/{id}/items/{ref}/sentences drops every
// flagged sentence of one item in a single write, and the same path
// POSTed with {"sentences": [...]} puts them all back — positions,
// citations and flags included.

import (
	"context"
	"fmt"
	"testing"
)

// itemSents flattens one document item's sentence list into the fields
// the bulk contract cares about: text, index order and flag presence.
type sentRow struct {
	text  string
	flag  bool
	cites int
}

func sentsOf(item map[string]any) []sentRow {
	var out []sentRow
	for _, sn := range item["sentences"].([]any) {
		s := sn.(map[string]any)
		out = append(out, sentRow{
			text:  s["text"].(string),
			flag:  s["flag"] != nil,
			cites: len(s["citations"].([]any)),
		})
	}
	return out
}

func findItem(t *testing.T, doc map[string]any, ref int) map[string]any {
	t.Helper()
	for _, s := range doc["sections"].([]any) {
		for _, i := range s.(map[string]any)["items"].([]any) {
			it := i.(map[string]any)
			if int(it["ref"].(float64)) == ref {
				return it
			}
		}
	}
	t.Fatalf("item ref %d not in document", ref)
	return nil
}

func TestBulkRemoveFlagged(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	// approve every pending item, then draft — same shape as the
	// lifecycle test; citecheck leaves flagged sentences in the body
	fresh := apiGet(t, srv, issueURL+"/items")
	for _, it := range fresh["items"].([]any) {
		m := it.(map[string]any)
		if m["status"] == "pending" {
			apiCall(t, srv, "POST",
				issueURL+"/items/"+fmt.Sprint(int64(m["id"].(float64)))+"/decisions",
				map[string]string{"kind": "approve"})
		}
	}
	rr := apiCall(t, srv, "POST", issueURL+"/draft", nil)
	if rr.Code != 202 {
		t.Fatalf("draft = %d: %s", rr.Code, shortBody(rr))
	}
	waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))

	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	ver := doc["version"].(string)

	// locate the item(s) the flags live on and a clean item for the
	// nothing_flagged guard
	flaggedRef, cleanRef := 0, 0
	var orig []sentRow
	for _, s := range doc["sections"].([]any) {
		for _, i := range s.(map[string]any)["items"].([]any) {
			it := i.(map[string]any)
			ref := int(it["ref"].(float64))
			rows := sentsOf(it)
			n := 0
			for _, r := range rows {
				if r.flag {
					n++
				}
			}
			switch {
			case n > 0 && flaggedRef == 0:
				flaggedRef, orig = ref, rows
			case n == 0 && cleanRef == 0:
				cleanRef = ref
			}
		}
	}
	if flaggedRef == 0 {
		t.Fatal("fixture produced no flagged sentence — citecheck replay broken?")
	}
	bulk := issueURL + "/items/" + fmt.Sprint(flaggedRef) + "/sentences"

	// guards: unknown item, stale version, nothing flagged
	rr = apiCall(t, srv, "DELETE", issueURL+"/items/999/sentences", map[string]any{"version": ver})
	if c := apiErrCode(t, rr); c != "not_found" {
		t.Fatalf("unknown item: code = %q", c)
	}
	rr = apiCall(t, srv, "DELETE", bulk, map[string]any{"version": "stale"})
	if c := apiErrCode(t, rr); c != "conflict" {
		t.Fatalf("stale version: code = %q", c)
	}
	if cleanRef != 0 {
		rr = apiCall(t, srv, "DELETE",
			issueURL+"/items/"+fmt.Sprint(cleanRef)+"/sentences",
			map[string]any{"version": ver})
		if c := apiErrCode(t, rr); c != "nothing_flagged" {
			t.Fatalf("clean item: code = %q", c)
		}
	}

	iss, _ := db.GetIssue(ctx, issueID)
	wantFlags := len(iss.Flags)
	wantRemoved := 0
	for _, r := range orig {
		if r.flag {
			wantRemoved++
		}
	}

	// the bulk delete: one call drops every flagged sentence
	rr = apiCall(t, srv, "DELETE", bulk, map[string]any{"version": ver})
	if rr.Code != 200 {
		t.Fatalf("bulk remove = %d: %s", rr.Code, shortBody(rr))
	}
	v := apiJSON(t, rr)
	removed, _ := v["removed"].([]any)
	if len(removed) != wantRemoved {
		t.Fatalf("removed = %d, want %d", len(removed), wantRemoved)
	}
	// undo payload: document order, one entry per removed sentence with
	// the fields the restore needs
	wantIdx := 0
	var restoreIdx []int
	for _, r := range orig {
		if r.flag {
			restoreIdx = append(restoreIdx, wantIdx)
		}
		wantIdx++
	}
	for i, e := range removed {
		m := e.(map[string]any)
		if int(m["ref"].(float64)) != flaggedRef {
			t.Fatalf("removed[%d] ref = %v", i, m["ref"])
		}
		if int(m["index"].(float64)) != restoreIdx[i] {
			t.Fatalf("removed[%d] index = %v, want %d", i, m["index"], restoreIdx[i])
		}
		if m["text"] == "" || m["kind"] == "" || m["flag"] == "" {
			t.Fatalf("removed[%d] payload incomplete: %v", i, m)
		}
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if len(iss.Flags) != wantFlags-wantRemoved {
		t.Fatalf("flags after bulk = %d, want %d", len(iss.Flags), wantFlags-wantRemoved)
	}
	if got := int(v["flags"].(float64)); got != wantFlags-wantRemoved {
		t.Fatalf("response flags = %d, want %d", got, wantFlags-wantRemoved)
	}

	// the item keeps its unflagged sentences, in order, nothing else moves
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	after := sentsOf(findItem(t, doc, flaggedRef))
	if len(after) != len(orig)-wantRemoved {
		t.Fatalf("item sentences after = %d, want %d", len(after), len(orig)-wantRemoved)
	}
	j := 0
	for _, r := range orig {
		if r.flag {
			continue
		}
		if after[j].text != r.text {
			t.Fatalf("surviving sentence order broken at %d: %q", j, after[j].text)
		}
		j++
	}
	for _, r := range after {
		if r.flag {
			t.Fatal("a flagged sentence survived the bulk remove")
		}
	}

	// batch undo: one POST restores the whole removed list at the
	// recorded positions, flags attached
	rr = apiCall(t, srv, "POST", bulk,
		map[string]any{"sentences": removed, "version": v["version"].(string)})
	if rr.Code != 200 {
		t.Fatalf("batch restore = %d: %s", rr.Code, shortBody(rr))
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if len(iss.Flags) != wantFlags {
		t.Fatalf("flags after undo = %d, want %d", len(iss.Flags), wantFlags)
	}
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	restored := sentsOf(findItem(t, doc, flaggedRef))
	if len(restored) != len(orig) {
		t.Fatalf("restored sentences = %d, want %d", len(restored), len(orig))
	}
	for i, r := range orig {
		if restored[i].text != r.text || restored[i].flag != r.flag || restored[i].cites != r.cites {
			t.Fatalf("sentence %d restored differently: %+v want %+v", i, restored[i], r)
		}
	}

	// the item is flagged again — a second bulk pass is legal and
	// removes them once more
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	rr = apiCall(t, srv, "DELETE", bulk,
		map[string]any{"version": doc["version"].(string)})
	if rr.Code != 200 {
		t.Fatalf("second bulk remove = %d: %s", rr.Code, shortBody(rr))
	}
}
