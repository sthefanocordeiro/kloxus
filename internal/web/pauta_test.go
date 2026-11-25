package web_test

import (
	"fmt"
	"strings"
	"testing"
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
