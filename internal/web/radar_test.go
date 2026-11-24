package web_test

import (
	"fmt"
	"strings"
	"testing"
)

// TestRadarOnboarding walks the Block 2 onboarding contract on a fresh
// account: free text creates the radar with sources pre-selected, the
// calibration deals real stories, answers are accepted, a skip topic
// lands in the reject terms, and a second radar is refused.
func TestRadarOnboarding(t *testing.T) {
	srv, _ := emptyServer(t)

	rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"about": "  "})
	if rr.Code != 400 || apiErrCode(t, rr) != "validation" {
		t.Fatalf("empty about = %d", rr.Code)
	}

	rr = apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{
		"name": "Fintech para bancários", "about": "fintech, Pix e regulação", "niches": []string{"mercado-financeiro", "bogus"},
	})
	if rr.Code != 201 {
		t.Fatalf("create radar = %d %s", rr.Code, rr.Body.String())
	}
	body := apiJSON(t, rr)
	pub := body["publication"].(map[string]any)
	id := fmt.Sprint(int64(pub["id"].(float64)))
	// D4: the radar's name is its own field, not the topics text
	if name, _ := pub["name"].(string); name != "Fintech para bancários" {
		t.Fatalf("name = %q", name)
	}
	if topic, _ := pub["topic"].(string); !strings.Contains(topic, "Pix") {
		t.Fatalf("topic = %q", topic)
	}
	if srcs, _ := body["sources"].([]any); len(srcs) == 0 {
		t.Fatal("no sources pre-selected")
	}

	// one radar per account
	rr = apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"about": "outra coisa"})
	if rr.Code != 409 || apiErrCode(t, rr) != "one_newsletter" {
		t.Fatalf("second radar = %d", rr.Code)
	}

	// calibration deals at most 8 cards, each with the fields the UI needs
	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/calibration", nil)
	if rr.Code != 200 {
		t.Fatalf("calibration = %d %s", rr.Code, rr.Body.String())
	}
	cards, _ := apiJSON(t, rr)["cards"].([]any)
	if len(cards) == 0 || len(cards) > 8 {
		t.Fatalf("cards = %d, want 1..8", len(cards))
	}
	first := cards[0].(map[string]any)
	for _, k := range []string{"story_id", "title", "outlet", "summary"} {
		if first[k] == nil || first[k] == "" {
			t.Fatalf("card missing %s: %v", k, first)
		}
	}
	seen := map[float64]bool{}
	for _, c := range cards {
		sid := c.(map[string]any)["story_id"].(float64)
		if seen[sid] {
			t.Fatalf("story %v dealt twice", sid)
		}
		seen[sid] = true
	}

	// answers: valid ones are accepted, malformed ones refused
	sid := first["story_id"]
	for _, ans := range []string{"yes", "no"} {
		rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/calibration/answer",
			map[string]any{"story_id": sid, "answer": ans})
		if rr.Code != 200 {
			t.Fatalf("answer %s = %d %s", ans, rr.Code, rr.Body.String())
		}
	}
	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/calibration/answer",
		map[string]any{"story_id": sid, "answer": "maybe"})
	if rr.Code != 400 {
		t.Fatalf("bad answer = %d", rr.Code)
	}

	// skip list: appended once, case-insensitively
	for i := 0; i < 2; i++ {
		rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/skip", map[string]any{"topic": "Games"})
		if rr.Code != 200 {
			t.Fatalf("skip = %d %s", rr.Code, rr.Body.String())
		}
	}
	got := apiGet(t, srv, "/api/v1/publications/"+id)["publication"].(map[string]any)
	if rt, _ := got["reject_terms"].(string); rt != "games" {
		t.Fatalf("reject_terms = %q, want games", rt)
	}
}

// TestRadarRequiresClaimedEmail (KLOX-161): both create paths refuse an
// unclaimed workspace with email_required; once the owner confirms the
// magic link the same request succeeds.
func TestRadarRequiresClaimedEmail(t *testing.T) {
	srv, db, _, _ := testServer(t, false, nil)

	for _, path := range []string{"/api/v1/radar", "/api/v1/publications"} {
		rr := apiCall(t, srv, "POST", path, map[string]any{"about": "fintech"})
		if rr.Code != 403 || apiErrCode(t, rr) != "email_required" {
			t.Fatalf("%s unclaimed = %d %s, want 403 email_required", path, rr.Code, shortBody(rr))
		}
	}
	if n := len(apiGet(t, srv, "/api/v1/publications")["publications"].([]any)); n != 0 {
		t.Fatalf("a refused create left %d publications", n)
	}

	claimWorkspace(t, srv, db, "dono@example.com")
	rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"about": "fintech"})
	if rr.Code != 201 {
		t.Fatalf("radar after claim = %d %s", rr.Code, shortBody(rr))
	}
}

func TestRadarNameTooLong(t *testing.T) {
	srv, _ := emptyServer(t)
	rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"name": strings.Repeat("x", 61), "about": "pix"})
	if rr.Code != 400 || apiErrCode(t, rr) != "validation" {
		t.Fatalf("long name = %d", rr.Code)
	}
}

func TestLogoutRequiresClaim(t *testing.T) {
	srv, _ := emptyServer(t)
	rr := apiCall(t, srv, "POST", "/api/v1/logout", map[string]any{})
	if rr.Code == 200 {
		t.Fatalf("unclaimed logout = %d, want refusal", rr.Code)
	}
}
