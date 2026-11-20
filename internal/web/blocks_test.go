package web_test

// Contract tests for the Escrever composer block ops (KLOX-66): typed
// blocks in ordered section containers — insert, update, move,
// remove, pauta blocks with a manual dossier, version conflicts,
// approval invalidation — plus the KLOX-66 carry-overs they land
// next to: one newsletter per account, HSTS, outbox scoping and the
// edited-after-approval marker.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// docBlocks returns one section's blocks array from a document
// response — the composer drives mutations by (section, index).
func docBlocks(t *testing.T, doc map[string]any, section string) []map[string]any {
	t.Helper()
	for _, s := range doc["sections"].([]any) {
		sv := s.(map[string]any)
		if sv["title"].(string) == section {
			out := []map[string]any{}
			for _, b := range sv["blocks"].([]any) {
				out = append(out, b.(map[string]any))
			}
			return out
		}
	}
	t.Fatalf("section %q not in document", section)
	return nil
}

func blockKinds(blocks []map[string]any) []string {
	out := []string{}
	for _, b := range blocks {
		out = append(out, b["kind"].(string))
	}
	return out
}

// composedIssue runs Phase A on a counting-LLM server and composes the
// empty skeleton — the cheapest deterministic path to a drafted
// edition the composer can edit.
func composedIssue(t *testing.T) (issueURL string, issueID int64, srv *web.Server, counter *countingLLM) {
	t.Helper()
	s, db, pub, c := composeServer(t)
	issueID = runPhaseA(t, s, db, pub.ID)
	issueURL = "/api/v1/issues/" + fmt.Sprint(issueID)
	rr := apiCall(t, s, "POST", issueURL+"/compose", nil)
	if rr.Code != 200 {
		t.Fatalf("compose = %d: %s", rr.Code, shortBody(rr))
	}
	return issueURL, issueID, s, c
}

// TestBlockOpsJourney inserts every block kind, edits, reorders and
// removes them — the document's blocks stay ordered as stored.
func TestBlockOpsJourney(t *testing.T) {
	issueURL, _, srv, _ := composedIssue(t)
	ver := func() string {
		return apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["version"].(string)
	}
	doc := func() map[string]any {
		return apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	}
	sec := doc()["sections"].([]any)[0].(map[string]any)["title"].(string)

	ins := func(body map[string]any) *httptest.ResponseRecorder {
		body["section"] = sec
		body["version"] = ver()
		return apiCall(t, srv, "POST", issueURL+"/blocks", body)
	}

	// heading + text + divider + link land in insert order
	if rr := ins(map[string]any{"kind": "heading", "text": "Also on my radar"}); rr.Code != 200 {
		t.Fatalf("insert heading = %d: %s", rr.Code, shortBody(rr))
	}
	if rr := ins(map[string]any{"kind": "text", "text": "A note for readers."}); rr.Code != 200 {
		t.Fatalf("insert text = %d: %s", rr.Code, shortBody(rr))
	}
	if rr := ins(map[string]any{"kind": "divider"}); rr.Code != 200 {
		t.Fatalf("insert divider = %d: %s", rr.Code, shortBody(rr))
	}
	if rr := ins(map[string]any{"kind": "link", "text": "Deep dive", "url": "https://example.com/x"}); rr.Code != 200 {
		t.Fatalf("insert link = %d: %s", rr.Code, shortBody(rr))
	}
	blocks := docBlocks(t, doc(), sec)
	kinds := blockKinds(blocks)
	n := len(kinds)
	if kinds[n-4] != "heading" || kinds[n-3] != "text" || kinds[n-2] != "divider" || kinds[n-1] != "link" {
		t.Fatalf("block tail kinds = %v", kinds[n-4:])
	}
	if blocks[n-4]["text"] != "Also on my radar" || blocks[n-1]["url"] != "https://example.com/x" {
		t.Fatalf("block attrs = %v / %v", blocks[n-4], blocks[n-1])
	}

	// update the heading in place
	rr := apiCall(t, srv, "PUT", issueURL+"/blocks", map[string]any{
		"section": sec, "index": n - 4, "text": "On my radar", "version": ver(),
	})
	if rr.Code != 200 {
		t.Fatalf("update heading = %d: %s", rr.Code, shortBody(rr))
	}
	if got := docBlocks(t, doc(), sec)[n-4]["text"]; got != "On my radar" {
		t.Fatalf("heading text = %v", got)
	}

	// a stale version is a conflict, not silent corruption
	rr = apiCall(t, srv, "PUT", issueURL+"/blocks", map[string]any{
		"section": sec, "index": n - 4, "text": "racy", "version": "stale-version",
	})
	if rr.Code != 409 {
		t.Fatalf("stale version update = %d, want 409", rr.Code)
	}

	// move the link above the heading: n-1 -> n-4
	rr = apiCall(t, srv, "POST", issueURL+"/blocks/move", map[string]any{
		"section": sec, "from": n - 1, "to": n - 4, "version": ver(),
	})
	if rr.Code != 200 {
		t.Fatalf("move = %d: %s", rr.Code, shortBody(rr))
	}
	kinds = blockKinds(docBlocks(t, doc(), sec))
	if kinds[n-4] != "link" || kinds[n-3] != "heading" {
		t.Fatalf("after move kinds = %v", kinds[n-4:])
	}

	// remove the divider — after the move the tail is
	// [link, heading, text, divider], so it sits at n-1
	rr = apiCall(t, srv, "DELETE", issueURL+"/blocks", map[string]any{
		"section": sec, "index": n - 1, "version": ver(),
	})
	if rr.Code != 200 {
		t.Fatalf("remove = %d: %s", rr.Code, shortBody(rr))
	}
	kinds = blockKinds(docBlocks(t, doc(), sec))
	if len(kinds) != n-1 || kinds[n-4] != "link" || kinds[n-3] != "heading" || kinds[n-2] != "text" {
		t.Fatalf("tail after removing divider = %v", kinds)
	}

	// validation guards
	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"empty text", map[string]any{"kind": "text", "text": " "}, 400},
		{"link no url", map[string]any{"kind": "link", "text": "x"}, 400},
		{"bad kind", map[string]any{"kind": "video"}, 400},
	} {
		if rr := ins(tc.body); rr.Code != tc.want {
			t.Fatalf("%s insert = %d, want %d", tc.name, rr.Code, tc.want)
		}
	}
}

// TestBlockPautaInsert adds a bench story through the composer: the
// story joins the edition with a manual dossier — no model call —
// and removing its block takes it back out.
func TestBlockPautaInsert(t *testing.T) {
	issueURL, _, srv, counter := composedIssue(t)
	before := counter.count()

	// find a bench candidate (not in the issue)
	cands := apiGet(t, srv, issueURL+"/candidates")
	var storyID int64
	for _, c := range cands["candidates"].([]any) {
		cv := c.(map[string]any)
		if !cv["in_issue"].(bool) && cv["available"].(bool) {
			storyID = int64(cv["story_id"].(float64))
			break
		}
	}
	if storyID == 0 {
		t.Fatal("no bench candidate to insert")
	}
	sec := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["sections"].([]any)[0].(map[string]any)["title"].(string)
	ver := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["version"].(string)

	rr := apiCall(t, srv, "POST", issueURL+"/blocks", map[string]any{
		"section": sec, "kind": "item", "story_id": storyID, "version": ver,
	})
	if rr.Code != 200 {
		t.Fatalf("insert pauta = %d: %s", rr.Code, shortBody(rr))
	}
	if got := counter.count(); got != before {
		t.Fatalf("pauta insert called the model: %d -> %d", before, got)
	}
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	blocks := docBlocks(t, doc, sec)
	last := blocks[len(blocks)-1]
	if last["kind"] != "item" {
		t.Fatalf("last block = %v, want item", last)
	}

	// the new item exists in the store, approved, manual origin
	items := docItems(doc)
	var added map[string]any
	for _, it := range items {
		if int(it["ref"].(float64)) == int(last["ref"].(float64)) {
			added = it
		}
	}
	if added == nil || len(added["sources"].([]any)) == 0 {
		t.Fatalf("inserted item missing or without sources: %v", added)
	}

	// inserting the same story again is refused
	ver = doc["version"].(string)
	rr = apiCall(t, srv, "POST", issueURL+"/blocks", map[string]any{
		"section": sec, "kind": "item", "story_id": storyID, "version": ver,
	})
	if rr.Code != 409 {
		t.Fatalf("duplicate pauta insert = %d, want 409", rr.Code)
	}

	// removing the item block removes the story from the edition
	idx := len(blocks) - 1
	ver = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["version"].(string)
	rr = apiCall(t, srv, "DELETE", issueURL+"/blocks", map[string]any{
		"section": sec, "index": idx, "version": ver,
	})
	if rr.Code != 200 {
		t.Fatalf("remove pauta = %d: %s", rr.Code, shortBody(rr))
	}
	for _, it := range docItems(apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)) {
		if int(it["ref"].(float64)) == int(last["ref"].(float64)) {
			t.Fatal("removed pauta still in the document")
		}
	}
}

// TestOneNewsletterGuard: an account that already has a newsletter
// cannot create another — the API answers 409 one_newsletter and the
// existing publication stays untouched.
func TestOneNewsletterGuard(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	// a fully valid create body — anything less fails validation
	// before the guard runs, which would prove nothing about it
	rr := apiCall(t, srv, "POST", "/api/v1/publications", map[string]any{
		"name": "Second Letter", "frequency": "weekly",
		"sections": []map[string]any{
			{"title": "Top", "format": "news", "items": 3, "commentary": "optional", "access": "all"},
		},
	})
	if rr.Code != 409 || apiErrCode(t, rr) != "one_newsletter" {
		t.Fatalf("second publication = %d code=%q", rr.Code, apiErrCode(t, rr))
	}
	// the original newsletter is still the only one
	pubs := apiGet(t, srv, "/api/v1/publications")
	arr, _ := pubs["publications"].([]any)
	if len(arr) != 1 {
		t.Fatalf("publications after refused create = %d, want 1", len(arr))
	}
	if got := int64(arr[0].(map[string]any)["id"].(float64)); got != pub.ID {
		t.Fatalf("surviving publication id = %d, want %d", got, pub.ID)
	}
}

// TestHSTSHeader: every response carries Strict-Transport-Security.
func TestHSTSHeader(t *testing.T) {
	srv, _, _, _ := demoServer(t)
	rr := apiCall(t, srv, "GET", "/healthz", nil)
	if got := rr.Header().Get("Strict-Transport-Security"); got != "max-age=63072000; includeSubDomains" {
		t.Fatalf("HSTS = %q", got)
	}
}

// TestOutboxScope: the publication outbox lists only its own mail —
// account-level magic_link messages never leak into it.
func TestOutboxScope(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	if _, err := db.SaveOutboxEmail(ctx, "magic_link", "owner@x.test", "Sign in", "link", "<p>link</p>"); err != nil {
		t.Fatalf("seed magic_link: %v", err)
	}
	out := apiGet(t, srv, "/api/v1/publications/"+fmt.Sprint(pub.ID)+"/outbox")
	for _, e := range out["emails"].([]any) {
		if e.(map[string]any)["kind"] == "magic_link" {
			t.Fatal("magic_link leaked into the publication outbox")
		}
	}
}

// TestEditedAfterApproval: an approved edition that is then edited
// (any composer op) returns to drafted and reports
// edited_after_approval until a fresh approval lands.
func TestEditedAfterApproval(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	// lineup decisions like the journey test — the canonical fixture
	// lineup so the recorded draft replay matches: four rejects, the
	// alternative swap, and every remaining pending item approved
	iis, err := db.IssueItems(ctx, issueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	sort.Slice(iis, func(a, b int) bool { return iis[a].ID < iis[b].ID })
	decide := func(iid int64, kind, reason string) {
		body := map[string]string{"kind": kind}
		if reason != "" {
			body["reason"] = reason
		}
		rr := apiCall(t, srv, "POST", issueURL+"/items/"+fmt.Sprint(iid)+"/decisions", body)
		if rr.Code != 200 {
			t.Fatalf("decision %s on %d = %d: %s", kind, iid, rr.Code, shortBody(rr))
		}
	}
	for _, i := range []int{1, 2, 5, 7} {
		decide(iis[i].ID, "reject", "cut")
	}
	decide(iis[6].ID, "alternative", "")
	iis2, err := db.IssueItems(ctx, issueID)
	if err != nil {
		t.Fatalf("IssueItems after alternative: %v", err)
	}
	for _, ii := range iis2 {
		if ii.Status == "pending" {
			decide(ii.ID, "approve", "")
		}
	}
	rr := apiCall(t, srv, "POST", issueURL+"/draft", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("draft = %d", rr.Code)
	}
	waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))

	// clear blockers: required commentary + flagged sentences
	ver := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["version"].(string)
	for _, s := range apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["sections"].([]any) {
		sv := s.(map[string]any)
		if c, _ := sv["commentary"].(map[string]any); c != nil && c["mode"] == "required" {
			rr = apiCall(t, srv, "PUT", issueURL+"/commentary", map[string]string{
				"section": sv["title"].(string), "text": "My take.", "version": ver,
			})
			if rr.Code != 200 {
				t.Fatalf("commentary = %d: %s", rr.Code, shortBody(rr))
			}
			ver = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["version"].(string)
		}
	}
	for {
		doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
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
			break
		}
		rr = apiCall(t, srv, "DELETE", issueURL+"/sentences/"+next, map[string]any{"version": doc["version"]})
		if rr.Code != 200 {
			t.Fatalf("remove flagged %s = %d: %s", next, rr.Code, shortBody(rr))
		}
	}
	if rd := apiGet(t, srv, issueURL+"/readiness"); !rd["ready"].(bool) {
		t.Fatalf("not ready: %v", rd["checks"])
	}
	if rr := apiCall(t, srv, "POST", issueURL+"/approve", nil); rr.Code != 200 {
		t.Fatalf("approve = %d: %s", rr.Code, shortBody(rr))
	}
	// the summary lives under "issue" — status, edited_after_approval
	sum := func() map[string]any {
		return apiGet(t, srv, issueURL)["issue"].(map[string]any)
	}
	if sum()["edited_after_approval"] == true {
		t.Fatal("fresh approval should not flag edited_after_approval")
	}

	// a composer edit voids the approval and flags the summary
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	sec := doc["sections"].([]any)[0].(map[string]any)["title"].(string)
	rr = apiCall(t, srv, "POST", issueURL+"/blocks", map[string]any{
		"section": sec, "kind": "divider", "version": doc["version"],
	})
	if rr.Code != 200 {
		t.Fatalf("post-approval block insert = %d: %s", rr.Code, shortBody(rr))
	}
	if sum()["status"] != "drafted" {
		t.Fatalf("status after edit = %v, want drafted", sum()["status"])
	}
	if sum()["edited_after_approval"] != true {
		t.Fatalf("edited_after_approval = %v", sum()["edited_after_approval"])
	}

	// re-approval clears the marker
	if rd := apiGet(t, srv, issueURL+"/readiness"); !rd["ready"].(bool) {
		t.Fatalf("not ready for re-approval: %v", rd["checks"])
	}
	if rr := apiCall(t, srv, "POST", issueURL+"/approve", nil); rr.Code != 200 {
		t.Fatalf("re-approve = %d: %s", rr.Code, shortBody(rr))
	}
	if sum()["status"] != "approved" || sum()["edited_after_approval"] == true {
		t.Fatalf("after re-approval status=%v marker=%v", sum()["status"], sum()["edited_after_approval"])
	}
}

// TestEmbedFenceBypass: the QA-reproduced SanitizeEmbed bypass (PR #53
// FAIL). Two write paths used to splice a raw <klox-embed> fence into
// the stored body — a newline-bearing heading/block payload and
// newline- or fence-bearing sentence text — and the document/reader
// surfaces served the inner markup verbatim. Now the writes refuse
// document markup, and a body that somehow carries a raw fence is
// re-sanitized at every serve boundary.
func TestEmbedFenceBypass(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := composeServer(t)
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	if rr := apiCall(t, srv, "POST", issueURL+"/compose", nil); rr.Code != 200 {
		t.Fatalf("compose = %d: %s", rr.Code, shortBody(rr))
	}
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	sec := doc["sections"].([]any)[0].(map[string]any)["title"].(string)
	ref := int(docItems(doc)[0]["ref"].(float64))
	ver := doc["version"].(string)
	fence := "x\n\n<klox-embed>\n<img src=\"x\" onerror=\"alert(1)\">\n</klox-embed>"

	// 1. a heading payload carrying a newline + fence is refused
	if rr := apiCall(t, srv, "POST", issueURL+"/blocks", map[string]any{
		"section": sec, "kind": "heading", "text": fence, "version": ver,
	}); rr.Code != 400 {
		t.Fatalf("heading fence payload = %d, want 400: %s", rr.Code, shortBody(rr))
	}
	// link/image urls carrying a newline are refused too
	for _, kind := range []string{"link", "image"} {
		if rr := apiCall(t, srv, "POST", issueURL+"/blocks", map[string]any{
			"section": sec, "kind": kind, "text": "x", "url": "https://example.com\n<klox-embed>", "version": ver,
		}); rr.Code != 400 {
			t.Fatalf("%s newline url = %d, want 400: %s", kind, rr.Code, shortBody(rr))
		}
	}

	// 2. the sentence compose path refuses newline- and fence-bearing
	// text — including a bare single-line fence (no newline needed)
	compose := func(text string) *httptest.ResponseRecorder {
		return apiCall(t, srv, "POST", fmt.Sprintf("%s/items/%d/compose", issueURL, ref),
			map[string]any{"text": text, "version": ver})
	}
	for _, bad := range []string{fence, "<klox-embed>", "note <!-- /item --> tail"} {
		if rr := compose(bad); rr.Code != 400 {
			t.Fatalf("compose %q = %d, want 400: %s", bad, rr.Code, shortBody(rr))
		}
	}
	// an interpretation with a spliced newline is refused the same way
	if rr := apiCall(t, srv, "POST", fmt.Sprintf("%s/items/%d/compose", issueURL, ref),
		map[string]any{"text": "my take " + fence, "kind": "interpretation", "version": ver}); rr.Code != 400 {
		t.Fatalf("interpretation fence payload = %d, want 400: %s", rr.Code, shortBody(rr))
	}

	// 3. sentence edit refuses the same payloads — compose a clean
	// sentence first so the PATCH has a target
	if rr := compose("A claim nobody can trace back."); rr.Code != 200 {
		t.Fatalf("seed sentence = %d: %s", rr.Code, shortBody(rr))
	}
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	ver = doc["version"].(string)
	var sid string
	for _, it := range docItems(doc) {
		for _, sn := range it["sentences"].([]any) {
			sid = sn.(map[string]any)["id"].(string)
		}
	}
	if sid == "" {
		t.Fatal("no composed sentence found")
	}
	if rr := apiCall(t, srv, "PATCH", issueURL+"/sentences/"+sid, map[string]any{
		"text": fence, "citations": []int{1}, "version": ver,
	}); rr.Code != 400 {
		t.Fatalf("sentence edit fence payload = %d, want 400: %s", rr.Code, shortBody(rr))
	}

	// 4. defense in depth: a body that already carries a raw fence
	// (legacy data or a bypass) is still served sanitized — the
	// document's embed block text and the reader preview both strip
	// the active markup
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	raw := "<klox-embed>\n<img src=\"x\" onerror=\"alert(1)\">\n" +
		"<iframe src=\"https://evil.example/x\"></iframe>\n</klox-embed>"
	if err := db.SetIssueBody(ctx, issueID, iss.BodyMD+"\n\n"+raw+"\n"); err != nil {
		t.Fatalf("SetIssueBody: %v", err)
	}
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	var embedText string
	found := false
	for _, s := range doc["sections"].([]any) {
		for _, b := range s.(map[string]any)["blocks"].([]any) {
			bv := b.(map[string]any)
			if bv["kind"] == "embed" {
				embedText, _ = bv["text"].(string)
				found = true
			}
		}
	}
	if !found {
		t.Fatal("planted fence did not parse as an embed block")
	}
	for _, bad := range []string{"onerror", "evil.example", "<script"} {
		if strings.Contains(embedText, bad) {
			t.Fatalf("document embed text leaked %q: %s", bad, embedText)
		}
	}
	pv := apiGet(t, srv, issueURL+"/preview?reader=subscriber")
	md, _ := pv["body_md"].(string)
	for _, bad := range []string{"onerror", "evil.example"} {
		if strings.Contains(md, bad) {
			t.Fatalf("reader preview leaked %q:\n%s", bad, md)
		}
	}
}
