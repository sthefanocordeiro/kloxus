package web_test

// Contract tests for the renditions API (KLOX-125): forking an approved
// piece per format, independent edits that never reopen the review, the
// manual "base changed" refresh, and the approved-only gate.

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRenditionsLifecycle(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	iid := approvedIssue(t, db, pub.ID, 91, "Pix e cripto")
	base := fmt.Sprintf("/api/v1/issues/%d/renditions", iid)

	// fork three formats
	ids := map[string]string{}
	for _, f := range []string{"linkedin", "html", "markdown"} {
		rr := apiCall(t, srv, "POST", base, map[string]any{"format": f})
		if rr.Code != 201 {
			t.Fatalf("fork %s = %d: %s", f, rr.Code, shortBody(rr))
		}
		r := apiJSON(t, rr)["rendition"].(map[string]any)
		ids[f] = fmt.Sprint(int64(r["id"].(float64)))
		if r["stale"] != false {
			t.Fatalf("fresh %s fork must not be stale", f)
		}
	}
	// repeat fork returns the existing rendition
	rr := apiCall(t, srv, "POST", base, map[string]any{"format": "linkedin"})
	if got := fmt.Sprint(int64(apiJSON(t, rr)["rendition"].(map[string]any)["id"].(float64))); got != ids["linkedin"] {
		t.Fatalf("repeat fork id = %s, want %s", got, ids["linkedin"])
	}

	// renditions edit independently, and an edit never reopens the review
	rr = apiCall(t, srv, "PUT", base+"/"+ids["linkedin"], map[string]any{"body": "só o LinkedIn"})
	if rr.Code != 200 {
		t.Fatalf("PUT linkedin = %d: %s", rr.Code, shortBody(rr))
	}
	list := apiGet(t, srv, base)["renditions"].([]any)
	if len(list) != 3 {
		t.Fatalf("renditions = %d, want 3", len(list))
	}
	for _, x := range list {
		r := x.(map[string]any)
		switch r["format"] {
		case "linkedin":
			if r["body_text"] != "só o LinkedIn" {
				t.Errorf("linkedin body_text = %q", r["body_text"])
			}
		case "html":
			if !strings.Contains(r["body_html"].(string), "<h3>Item</h3>") {
				t.Errorf("html rendition was touched: %q", r["body_html"])
			}
		case "markdown":
			if !strings.Contains(r["body_md"].(string), "### Item") {
				t.Errorf("markdown rendition was touched: %q", r["body_md"])
			}
		}
	}
	if iss, err := db.GetIssue(ctx, iid); err != nil || iss.Status != "approved" {
		t.Fatalf("issue after rendition edit: %v %+v", err, iss)
	}

	// the piece changes (the text template omits section headings by
	// spec; the HTML/MD templates carry them, which moves the shared hash)
	// → renditions go stale, nothing is overwritten
	body := "# Pix e cripto\n\n## Nova seção\n\n### Item\n\nA short sourced sentence [1]."
	if err := db.UpdateIssue(ctx, iid, "approved", body, "[]"); err != nil {
		t.Fatal(err)
	}
	for _, x := range apiGet(t, srv, base)["renditions"].([]any) {
		if x.(map[string]any)["stale"] != true {
			t.Fatalf("rendition not stale after base change: %v", x)
		}
	}
	rr = apiCall(t, srv, "POST", base+"/"+ids["linkedin"]+"/refresh", nil)
	if rr.Code != 200 {
		t.Fatalf("refresh = %d: %s", rr.Code, shortBody(rr))
	}
	r := apiJSON(t, rr)["rendition"].(map[string]any)
	if r["stale"] != false || !strings.HasPrefix(r["body_text"].(string), "Item") {
		t.Fatalf("refreshed rendition = %v", r)
	}

	// untick a destination
	if rr := apiCall(t, srv, "DELETE", base+"/"+ids["html"], nil); rr.Code != 204 {
		t.Fatalf("DELETE = %d", rr.Code)
	}
	if n := len(apiGet(t, srv, base)["renditions"].([]any)); n != 2 {
		t.Fatalf("renditions after delete = %d, want 2", n)
	}
}

func TestRenditionsRequireApprovedPiece(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	iid, err := db.CreateIssue(context.Background(), pub.ID, 92, "Rascunho", "# Rascunho\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/issues/%d/renditions", iid)
	rr := apiCall(t, srv, "POST", base, map[string]any{"format": "markdown"})
	if rr.Code != 409 || apiErrCode(t, rr) != "blocked" {
		t.Fatalf("fork of unapproved piece = %d: %s", rr.Code, shortBody(rr))
	}
	if rr := apiCall(t, srv, "POST", base, map[string]any{"format": "pdf"}); rr.Code != 400 {
		t.Fatalf("bad format = %d", rr.Code)
	}
}
