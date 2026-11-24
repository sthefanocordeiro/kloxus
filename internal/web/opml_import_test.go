package web_test

import (
	"fmt"
	"testing"
)

func TestAPISourceImportOPML(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	base := "/api/v1/publications/" + fmt.Sprint(pub.ID) + "/sources"
	before := len(apiGet(t, srv, base)["sources"].([]any))
	x := `<opml version="1.0"><body><outline text="Tech">
	<outline type="rss" text="A" xmlUrl="http://a.example/feed/"/>
	<outline type="rss" text="A again" xmlUrl="https://www.a.example/feed"/>
	<outline type="rss" text="B" xmlUrl="https://a.example/other"/>
	<outline type="rss" text="Bad" xmlUrl="ftp://x.example/f"/></outline></body></opml>`

	// dry run stores nothing
	rr := apiCall(t, srv, "POST", base+"/import-opml", map[string]any{"opml": x})
	if rr.Code != 200 {
		t.Fatalf("dry run = %d %s", rr.Code, rr.Body.String())
	}
	c := apiJSON(t, rr)["counts"].(map[string]any)
	if c["new"] != 2.0 || c["duplicate"] != 1.0 || c["invalid"] != 1.0 {
		t.Fatalf("counts = %v", c)
	}
	if n := len(apiGet(t, srv, base)["sources"].([]any)); n != before {
		t.Fatalf("dry run stored sources: %d != %d", n, before)
	}

	// apply, then re-apply is idempotent (all duplicates)
	rr = apiCall(t, srv, "POST", base+"/import-opml", map[string]any{"opml": x, "apply": true})
	if rr.Code != 200 {
		t.Fatalf("apply = %d", rr.Code)
	}
	if n := len(apiGet(t, srv, base)["sources"].([]any)); n != before+2 {
		t.Fatalf("after apply %d, want %d", n, before+2)
	}
	c = apiJSON(t, apiCall(t, srv, "POST", base+"/import-opml", map[string]any{"opml": x, "apply": true}))["counts"].(map[string]any)
	if c["new"] != nil || c["duplicate"] != 3.0 {
		t.Fatalf("reapply counts = %v", c)
	}

	rr = apiCall(t, srv, "POST", base+"/import-opml", map[string]any{"opml": "<html/>"})
	if rr.Code != 400 || apiErrCode(t, rr) != "invalid_opml" {
		t.Fatalf("bad opml = %d", rr.Code)
	}
}
