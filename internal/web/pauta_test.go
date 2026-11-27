package web_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// KLOX-168: tick stories → pauta with free text → finish → export keeps
// the author's text, in the author's order, without any checks.
func TestPautaFlow(t *testing.T) {
	srv, _ := emptyServer(t)
	rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"about": "futuro e tecnologia"})
	if rr.Code != 201 {
		t.Fatalf("radar = %d %s", rr.Code, rr.Body.String())
	}
	id := fmt.Sprint(int64(apiJSON(t, rr)["publication"].(map[string]any)["id"].(float64)))
	cards := apiGet(t, srv, "/api/v1/publications/"+id+"/today")["cards"].([]any)
	if len(cards) < 3 {
		t.Fatalf("cards = %d", len(cards))
	}
	s1 := cards[0].(map[string]any)["story_id"]
	s2 := cards[1].(map[string]any)["story_id"]
	s3 := cards[2].(map[string]any)["story_id"]

	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/pauta", map[string]any{"story_ids": []any{s2, s1}})
	if rr.Code != 201 {
		t.Fatalf("create = %d %s", rr.Code, rr.Body.String())
	}
	iid := fmt.Sprint(int64(apiJSON(t, rr)["issue_id"].(float64)))
	p := apiGet(t, srv, "/api/v1/issues/"+iid+"/pauta")
	es := p["entries"].([]any)
	if len(es) != 2 || es[0].(map[string]any)["story_id"] != s2 || es[1].(map[string]any)["story_id"] != s1 {
		t.Fatalf("order = %v", es)
	}

	// write text, reorder, add a third, opening — the first entry is the
	// KLOX-181 regression case: a '. ' boundary mid-line, a blank-line
	// paragraph break and an emoji must all read back verbatim
	const verbatim = "Alta, mas com ressalvas. Vale ler os detalhes. ✅\nlinha dois\n\noutro parágrafo"
	rr = apiCall(t, srv, "PUT", "/api/v1/issues/"+iid+"/pauta", map[string]any{
		"opening": "Bom dia, leitor",
		"entries": []any{
			map[string]any{"story_id": s1, "text": verbatim + "\nSegunda linha # com hash"},
			map[string]any{"story_id": s3, "text": ""},
			map[string]any{"story_id": s2, "text": "Texto da terceira [2]"},
		},
	})
	if rr.Code != 200 {
		t.Fatalf("save = %d %s", rr.Code, rr.Body.String())
	}
	p = apiGet(t, srv, "/api/v1/issues/"+iid+"/pauta")
	es = p["entries"].([]any)
	if len(es) != 3 || es[0].(map[string]any)["story_id"] != s1 || es[1].(map[string]any)["story_id"] != s3 {
		t.Fatalf("order after save = %v", es)
	}
	if got := es[0].(map[string]any)["text"]; got != verbatim+"\nSegunda linha # com hash" {
		t.Fatalf("text = %q", got)
	}
	if p["opening"] != "Bom dia, leitor" {
		t.Fatalf("opening = %v", p["opening"])
	}
	if es[0].(map[string]any)["url"] == "" {
		t.Fatalf("entry has no link: %v", es[0])
	}

	// remove one
	rr = apiCall(t, srv, "PUT", "/api/v1/issues/"+iid+"/pauta", map[string]any{
		"entries": []any{map[string]any{"story_id": s1, "text": "Só esta."}},
	})
	if rr.Code != 200 {
		t.Fatalf("save2 = %d %s", rr.Code, rr.Body.String())
	}
	if n := len(apiGet(t, srv, "/api/v1/issues/"+iid+"/pauta")["entries"].([]any)); n != 1 {
		t.Fatalf("entries after remove = %d", n)
	}

	// finish: no checks; the missing-text count is only a heads-up
	rr = apiCall(t, srv, "PUT", "/api/v1/issues/"+iid+"/pauta", map[string]any{
		"entries": []any{
			map[string]any{"story_id": s1, "text": "Só esta."},
			map[string]any{"story_id": s2, "text": ""},
		},
	})
	if rr.Code != 200 {
		t.Fatalf("save3 = %d %s", rr.Code, rr.Body.String())
	}
	rr = apiCall(t, srv, "POST", "/api/v1/issues/"+iid+"/pauta/finish", map[string]any{})
	if rr.Code != 200 {
		t.Fatalf("finish = %d %s", rr.Code, rr.Body.String())
	}
	if m := apiJSON(t, rr)["missing_text"]; m != float64(1) {
		t.Fatalf("missing_text = %v", m)
	}

	// export opens and carries the text — verbatim in every format. The
	// verbatim save must land before the forks: a repeat fork returns
	// the existing rendition untouched, and a save re-drafts the issue
	// so it needs a second finish
	rr = apiCall(t, srv, "PUT", "/api/v1/issues/"+iid+"/pauta", map[string]any{
		"entries": []any{map[string]any{"story_id": s1, "text": verbatim}},
	})
	if rr.Code != 200 {
		t.Fatalf("save4 = %d %s", rr.Code, rr.Body.String())
	}
	rr = apiCall(t, srv, "POST", "/api/v1/issues/"+iid+"/pauta/finish", map[string]any{})
	if rr.Code != 200 {
		t.Fatalf("finish2 = %d %s", rr.Code, rr.Body.String())
	}
	for _, format := range []string{"linkedin", "x", "html", "markdown"} {
		rr = apiCall(t, srv, "POST", "/api/v1/issues/"+iid+"/renditions", map[string]any{"format": format})
		if rr.Code != 201 {
			t.Fatalf("rendition %s = %d %s", format, rr.Code, rr.Body.String())
		}
		body := apiJSON(t, rr)["rendition"].(map[string]any)
		got := body["body_text"].(string) + body["body_html"].(string) + body["body_md"].(string)
		if !strings.Contains(got, "com ressalvas. Vale ler os detalhes.") {
			t.Fatalf("%s lost the '. ' boundary: %s", format, got)
		}
	}
}

// KLOX-287: the mailed "write about this story" deep link is idempotent
// while the post is still a draft — a second click opens the same pauta
// instead of stacking an identical one.
func TestPautaCreateReusesOpenDraftWithStory(t *testing.T) {
	srv, db := emptyServer(t)
	rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"about": "futuro e tecnologia"})
	if rr.Code != 201 {
		t.Fatalf("radar = %d %s", rr.Code, rr.Body.String())
	}
	pubID := int64(apiJSON(t, rr)["publication"].(map[string]any)["id"].(float64))
	id := fmt.Sprint(pubID)
	cards := apiGet(t, srv, "/api/v1/publications/"+id+"/today")["cards"].([]any)
	if len(cards) < 2 {
		t.Fatalf("cards = %d", len(cards))
	}
	s1 := cards[0].(map[string]any)["story_id"]
	s2 := cards[1].(map[string]any)["story_id"]

	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/pauta", map[string]any{"story_ids": []any{s1}})
	if rr.Code != 201 {
		t.Fatalf("first create = %d %s", rr.Code, rr.Body.String())
	}
	firstID := int64(apiJSON(t, rr)["issue_id"].(float64))
	before := issueCount(t, db, pubID)

	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/pauta", map[string]any{"story_ids": []any{s1}})
	if rr.Code != 200 {
		t.Fatalf("repeat create = %d %s", rr.Code, rr.Body.String())
	}
	if got := int64(apiJSON(t, rr)["issue_id"].(float64)); got != firstID {
		t.Fatalf("repeat create issue = %d, want existing %d", got, firstID)
	}
	if got := issueCount(t, db, pubID); got != before {
		t.Fatalf("issues after repeat = %d, want %d", got, before)
	}

	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/pauta", map[string]any{"story_ids": []any{s2}})
	if rr.Code != 201 {
		t.Fatalf("different-story create = %d %s", rr.Code, rr.Body.String())
	}
	if got := int64(apiJSON(t, rr)["issue_id"].(float64)); got == firstID {
		t.Fatalf("different story reused issue %d", got)
	}
}

// KLOX-287: a story rejected or replaced as an alternative is editorially
// outside the edition. Clicking the mailed deep link again must start a new
// pauta instead of reopening the one that no longer carries the story.
func TestPautaCreateStartsNewDraftWhenStoryLeftLineup(t *testing.T) {
	for _, status := range []string{store.ItemRejected, store.ItemAlternative} {
		t.Run(status, func(t *testing.T) {
			srv, db := emptyServer(t)
			rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"about": "futuro e tecnologia"})
			if rr.Code != 201 {
				t.Fatalf("radar = %d %s", rr.Code, rr.Body.String())
			}
			pubID := int64(apiJSON(t, rr)["publication"].(map[string]any)["id"].(float64))
			id := fmt.Sprint(pubID)
			cards := apiGet(t, srv, "/api/v1/publications/"+id+"/today")["cards"].([]any)
			if len(cards) == 0 {
				t.Fatal("no cards")
			}
			storyID := cards[0].(map[string]any)["story_id"]

			rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/pauta", map[string]any{"story_ids": []any{storyID}})
			if rr.Code != 201 {
				t.Fatalf("first create = %d %s", rr.Code, rr.Body.String())
			}
			firstID := int64(apiJSON(t, rr)["issue_id"].(float64))
			items, err := db.IssueItems(context.Background(), firstID)
			if err != nil || len(items) != 1 {
				t.Fatalf("items = %v, err %v", items, err)
			}
			if err := db.SetIssueItemStatus(context.Background(), items[0].ID, status); err != nil {
				t.Fatalf("set %s: %v", status, err)
			}
			before := issueCount(t, db, pubID)

			rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/pauta", map[string]any{"story_ids": []any{storyID}})
			if rr.Code != 201 {
				t.Fatalf("repeat create with %s item = %d %s", status, rr.Code, rr.Body.String())
			}
			if got := int64(apiJSON(t, rr)["issue_id"].(float64)); got == firstID {
				t.Fatalf("repeat create reused issue %d with %s item", got, status)
			}
			if got := issueCount(t, db, pubID); got != before+1 {
				t.Fatalf("issues after repeat = %d, want %d", got, before+1)
			}
		})
	}
}

// KLOX-199: the pauta is named by its first story everywhere — editor
// and lists. Clicking the name in the editor saves an author name on
// the issue subject that wins over the derived title; "" restores the
// automatic name. The exported text never gains a title.
func TestPautaName(t *testing.T) {
	srv, _ := emptyServer(t)
	rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{"about": "futuro e tecnologia"})
	if rr.Code != 201 {
		t.Fatalf("radar = %d %s", rr.Code, rr.Body.String())
	}
	id := fmt.Sprint(int64(apiJSON(t, rr)["publication"].(map[string]any)["id"].(float64)))
	cards := apiGet(t, srv, "/api/v1/publications/"+id+"/today")["cards"].([]any)
	if len(cards) < 2 {
		t.Fatalf("cards = %d", len(cards))
	}
	s1 := cards[0].(map[string]any)["story_id"]
	s2 := cards[1].(map[string]any)["story_id"]
	firstTitle := cards[0].(map[string]any)["title"].(string)

	rr = apiCall(t, srv, "POST", "/api/v1/publications/"+id+"/pauta", map[string]any{"story_ids": []any{s1, s2}})
	if rr.Code != 201 {
		t.Fatalf("create = %d %s", rr.Code, rr.Body.String())
	}
	iid := fmt.Sprint(int64(apiJSON(t, rr)["issue_id"].(float64)))

	// the editor reads the same name the lists show — the first story's
	// title, not "pauta N"
	p := apiGet(t, srv, "/api/v1/issues/"+iid+"/pauta")
	if p["title"] != firstTitle {
		t.Fatalf("pauta title = %v, want %q", p["title"], firstTitle)
	}
	issueView := apiGet(t, srv, "/api/v1/issues/"+iid)["issue"].(map[string]any)
	if got := issueView["title"]; got != firstTitle {
		t.Fatalf("issue title = %v, want %q", got, firstTitle)
	}

	entries := []any{
		map[string]any{"story_id": s1, "text": ""},
		map[string]any{"story_id": s2, "text": ""},
	}

	// rename — the author name wins in the editor and the lists
	rr = apiCall(t, srv, "PUT", "/api/v1/issues/"+iid+"/pauta",
		map[string]any{"title": "Meu resumo da semana", "entries": entries})
	if rr.Code != 200 {
		t.Fatalf("rename = %d %s", rr.Code, rr.Body.String())
	}
	if got := apiGet(t, srv, "/api/v1/issues/"+iid+"/pauta")["title"]; got != "Meu resumo da semana" {
		t.Fatalf("pauta title after rename = %v", got)
	}
	iss := apiGet(t, srv, "/api/v1/issues/"+iid)["issue"].(map[string]any)
	if iss["title"] != "Meu resumo da semana" || iss["subject"] != "Meu resumo da semana" {
		t.Fatalf("issue after rename = title %v subject %v", iss["title"], iss["subject"])
	}
	pieces := apiGet(t, srv, "/api/v1/publications/"+id+"/today")["pieces"].([]any)
	found := false
	for _, pc := range pieces {
		if pc.(map[string]any)["title"] == "Meu resumo da semana" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pieces missing the new name: %v", pieces)
	}

	// the exported text carries no automatic title — renaming never
	// leaks the name into the markdown fork
	rr = apiCall(t, srv, "POST", "/api/v1/issues/"+iid+"/pauta/finish", map[string]any{})
	if rr.Code != 200 {
		t.Fatalf("finish = %d %s", rr.Code, rr.Body.String())
	}
	rr = apiCall(t, srv, "POST", "/api/v1/issues/"+iid+"/renditions", map[string]any{"format": "markdown"})
	if rr.Code != 201 {
		t.Fatalf("rendition = %d %s", rr.Code, rr.Body.String())
	}
	md := apiJSON(t, rr)["rendition"].(map[string]any)["body_md"].(string)
	if strings.Contains(md, "Meu resumo da semana") {
		t.Fatalf("export gained the pauta name:\n%s", md)
	}

	// clearing the name restores the automatic one
	rr = apiCall(t, srv, "PUT", "/api/v1/issues/"+iid+"/pauta",
		map[string]any{"title": "", "entries": entries})
	if rr.Code != 200 {
		t.Fatalf("clear = %d %s", rr.Code, rr.Body.String())
	}
	if got := apiGet(t, srv, "/api/v1/issues/"+iid+"/pauta")["title"]; got != firstTitle {
		t.Fatalf("title after clear = %v, want %q", got, firstTitle)
	}
}
