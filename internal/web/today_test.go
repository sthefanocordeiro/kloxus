package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
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

// TestTodayNoHidesStory: answering "Não" on a Hoje card keeps that
// story out of the rebuilt pauta — story ids change on every rebuild,
// so the assertion is on the title (KLOX-183).
func TestTodayNoHidesStory(t *testing.T) {
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
	first := cards[0].(map[string]any)
	title := first["title"].(string)

	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/calibration/answer",
		map[string]any{"story_id": first["story_id"], "answer": "no"})
	if rr.Code != 200 {
		t.Fatalf("answer = %d %s", rr.Code, rr.Body.String())
	}

	// the answer drops today's snapshot; the rebuild must not serve
	// the rejected story again under a new id
	today = apiGet(t, srv, "/api/v1/publications/"+id+"/today")
	for _, c := range today["cards"].([]any) {
		if c.(map[string]any)["title"] == title {
			t.Fatalf("rejected story %q is back on Hoje", title)
		}
	}

	// the re-dealt calibration deck must not serve it either
	cal := apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/calibration", nil)
	if cal.Code != 200 {
		t.Fatalf("calibration = %d %s", cal.Code, cal.Body.String())
	}
	for _, c := range apiJSON(t, cal)["cards"].([]any) {
		if c.(map[string]any)["title"] == title {
			t.Fatalf("rejected story %q is back in the calibration deck", title)
		}
	}
}

// TestTodayNoOnStaleStoryID: a "Não" on a card whose story id died in
// a rebuild still lands on the card's feedback keys (KLOX-236) — the
// story stays out of Hoje and the re-dealt deck.
func TestTodayNoOnStaleStoryID(t *testing.T) {
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
	first := cards[0].(map[string]any)
	title := first["title"].(string)
	keys, _ := first["feedback_keys"].([]any)
	if len(keys) == 0 {
		t.Fatal("today card carries no feedback_keys")
	}

	// the deck is re-dealt between the card and the answer: every story
	// id the first list carried is dead now
	cal := apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/calibration", nil)
	if cal.Code != 200 {
		t.Fatalf("calibration = %d %s", cal.Code, cal.Body.String())
	}

	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/calibration/answer",
		map[string]any{"story_id": first["story_id"], "answer": "no",
			"feedback_keys": keys, "title": title})
	if rr.Code != 200 {
		t.Fatalf("answer = %d %s", rr.Code, rr.Body.String())
	}

	cal = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/calibration", nil)
	for _, c := range apiJSON(t, cal)["cards"].([]any) {
		if c.(map[string]any)["title"] == title {
			t.Fatalf("rejected story %q is back in the calibration deck", title)
		}
	}
	today = apiGet(t, srv, "/api/v1/publications/"+id+"/today")
	for _, c := range today["cards"].([]any) {
		if c.(map[string]any)["title"] == title {
			t.Fatalf("rejected story %q is back on Hoje", title)
		}
	}
}

// TestTodaySnapshotSkipsAnswered: a stored pauta written before the
// "Não" landed must still hide the story — the rejection filter runs
// on read, not only at build time (KLOX-236).
func TestTodaySnapshotSkipsAnswered(t *testing.T) {
	srv, db := emptyServer(t)
	ctx := context.Background()
	rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"about": "futuro e tecnologia"})
	if rr.Code != 201 {
		t.Fatalf("radar = %d %s", rr.Code, rr.Body.String())
	}
	pubID := int64(apiJSON(t, rr)["publication"].(map[string]any)["id"].(float64))
	id := fmt.Sprint(pubID)

	today := apiGet(t, srv, "/api/v1/publications/"+id+"/today")
	cards, _ := today["cards"].([]any)
	if len(cards) < 2 {
		t.Fatalf("cards = %d, want at least 2", len(cards))
	}
	first := cards[0].(map[string]any)
	title := first["title"].(string)
	sid := int64(first["story_id"].(float64))
	st, err := db.StoryWithItems(ctx, sid)
	if err != nil {
		t.Fatalf("story %d: %v", sid, err)
	}
	keys := st.FeedbackKeys()
	if len(keys) == 0 {
		t.Fatal("story has no feedback keys")
	}
	if err := db.SaveStoryAnswers(ctx, pubID, keys, "no", title); err != nil {
		t.Fatalf("SaveStoryAnswers: %v", err)
	}

	// a snapshot written before that answer: one card resolves its keys
	// through the live story row, one carries them, one keeps nothing
	snap := []map[string]any{
		{"story_id": sid, "title": title, "outlet": "x", "outlets": 1},
		{"story_id": sid, "title": "BY KEYS", "outlet": "x", "outlets": 1, "feedback_keys": keys[:1]},
		{"story_id": 999999, "title": "KEPT", "outlet": "x", "outlets": 1},
	}
	raw, _ := json.Marshal(snap)
	if err := db.SavePautaDay(ctx, pubID, spDayOf(time.Now()), string(raw)); err != nil {
		t.Fatalf("SavePautaDay: %v", err)
	}
	today = apiGet(t, srv, "/api/v1/publications/"+id+"/today")
	got := today["cards"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["title"] != "KEPT" {
		t.Fatalf("stored pauta served rejected cards: %v", got)
	}
}
