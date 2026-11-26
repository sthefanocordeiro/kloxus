package web_test

// Contract test for the stale_exports flag (KLOX-235): an export
// followed by an edit leaves the forked versions behind — the issues
// list and Today flag "edited since export" until "Update this
// version?" refreshes them.

import (
	"context"
	"fmt"
	"testing"
)

func TestStaleExportsFlag(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	iid := approvedIssue(t, db, pub.ID, 93, "Pix e cripto")
	base := fmt.Sprintf("/api/v1/issues/%d", iid)
	pubID := fmt.Sprint(pub.ID)

	// fork a version and mark the export
	if rr := apiCall(t, srv, "POST", base+"/renditions", map[string]any{"format": "markdown"}); rr.Code != 201 {
		t.Fatalf("fork = %d: %s", rr.Code, shortBody(rr))
	}
	if rr := apiCall(t, srv, "POST", base+"/exported", map[string]any{"format": "markdown"}); rr.Code != 200 {
		t.Fatalf("export = %d: %s", rr.Code, shortBody(rr))
	}

	// a fresh export flags nothing on either list
	issues := apiGet(t, srv, "/api/v1/publications/"+pubID+"/issues")["issues"].([]any)
	row := issueByID(t, issues, iid)
	if row["exported"] != true {
		t.Fatalf("exported = %v", row["exported"])
	}
	if _, ok := row["stale_exports"]; ok {
		t.Fatalf("fresh export must not flag stale_exports: %v", row)
	}
	pieces := apiGet(t, srv, "/api/v1/publications/"+pubID+"/today")["pieces"].([]any)
	if pieceByID(t, pieces, iid)["stale_exports"] == true {
		t.Fatal("fresh export must not flag stale_exports on /today")
	}

	// edit the piece — the exported version is now behind
	body := "# Pix e cripto\n\n## Section\n\n### Item\n\nA different sourced sentence [1]."
	if err := db.UpdateIssue(ctx, iid, "approved", body, "[]"); err != nil {
		t.Fatal(err)
	}
	issues = apiGet(t, srv, "/api/v1/publications/"+pubID+"/issues")["issues"].([]any)
	if issueByID(t, issues, iid)["stale_exports"] != true {
		t.Fatalf("edited pauta must flag stale_exports: %v", issueByID(t, issues, iid))
	}
	pieces = apiGet(t, srv, "/api/v1/publications/"+pubID+"/today")["pieces"].([]any)
	if pieceByID(t, pieces, iid)["stale_exports"] != true {
		t.Fatalf("/today piece must flag stale_exports: %v", pieceByID(t, pieces, iid))
	}

	// "Update this version?" re-forks from the piece and clears the flag
	rs := apiGet(t, srv, base+"/renditions")["renditions"].([]any)
	rid := fmt.Sprint(int64(rs[0].(map[string]any)["id"].(float64)))
	if rr := apiCall(t, srv, "POST", base+"/renditions/"+rid+"/refresh", nil); rr.Code != 200 {
		t.Fatalf("refresh = %d: %s", rr.Code, shortBody(rr))
	}
	issues = apiGet(t, srv, "/api/v1/publications/"+pubID+"/issues")["issues"].([]any)
	if _, ok := issueByID(t, issues, iid)["stale_exports"]; ok {
		t.Fatal("stale_exports must clear once every version is refreshed")
	}
	pieces = apiGet(t, srv, "/api/v1/publications/"+pubID+"/today")["pieces"].([]any)
	if pieceByID(t, pieces, iid)["stale_exports"] == true {
		t.Fatal("stale_exports must clear on /today once refreshed")
	}
}
