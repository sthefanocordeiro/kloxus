package web_test

import (
	"fmt"
	"testing"
)

// TestTodayAndWrite: Hoje serves the ranked pauta and the pieces in
// progress; "Escrever" creates a Peça with the story, and a second story
// joins the same open Peça instead of opening another.
func TestTodayAndWrite(t *testing.T) {
	srv, _ := emptyServer(t)
	rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"about": "futuro e tecnologia"})
	if rr.Code != 201 {
		t.Fatalf("radar = %d %s", rr.Code, rr.Body.String())
	}
	id := fmt.Sprint(int64(apiJSON(t, rr)["publication"].(map[string]any)["id"].(float64)))

	today := apiGet(t, srv, "/api/v1/publications/"+id+"/today")
	cards, _ := today["cards"].([]any)
	if len(cards) < 2 {
		t.Fatalf("cards = %d, want at least 2", len(cards))
	}
	if pcs, _ := today["pieces"].([]any); len(pcs) != 0 {
		t.Fatalf("pieces before writing = %d", len(pcs))
	}

	first := cards[0].(map[string]any)["story_id"]
	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/today/write", map[string]any{"story_id": first})
	if rr.Code != 200 {
		t.Fatalf("write = %d %s", rr.Code, rr.Body.String())
	}
	w1 := apiJSON(t, rr)
	if w1["created"] != true || w1["issue_id"] == nil {
		t.Fatalf("first write = %v", w1)
	}

	// the used story leaves Hoje; the piece is listed as a draft
	today = apiGet(t, srv, "/api/v1/publications/"+id+"/today")
	pcs, _ := today["pieces"].([]any)
	if len(pcs) != 1 || pcs[0].(map[string]any)["status"] != "draft" {
		t.Fatalf("pieces = %v", pcs)
	}
	next := today["cards"].([]any)[0].(map[string]any)["story_id"]
	if next == first {
		t.Fatal("a used story came back on Hoje")
	}

	// a second story joins the same open piece
	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/today/write", map[string]any{"story_id": next})
	if rr.Code != 200 {
		t.Fatalf("second write = %d %s", rr.Code, rr.Body.String())
	}
	w2 := apiJSON(t, rr)
	if w2["issue_id"] != w1["issue_id"] || w2["created"] == true {
		t.Fatalf("second write opened another piece: %v vs %v", w2, w1)
	}

	// KLOX-157: a post is the picked stories — one internal section, and
	// review never asks for decisions or required commentary
	issueURL := "/api/v1/issues/" + fmt.Sprint(int64(w1["issue_id"].(float64)))
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	if secs, _ := doc["sections"].([]any); len(secs) != 1 || secs[0].(map[string]any)["title"] != "Notícias" {
		t.Fatalf("post sections = %v", doc["sections"])
	}
	rd := apiGet(t, srv, issueURL+"/readiness")
	for _, c := range rd["checks"].([]any) {
		switch c.(map[string]any)["code"] {
		case "pending_decisions", "commentary_required":
			t.Fatalf("legacy check on a post: %v", c)
		}
	}

	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/today/write", map[string]any{"story_id": 999999})
	if rr.Code != 404 {
		t.Fatalf("unknown story = %d", rr.Code)
	}
}
