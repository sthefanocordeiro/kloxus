package web_test

// Contract tests for the newsletter template endpoints (KLOX-72): the
// publication's default edition layout — GET returns the effective
// template (one section slot per section until the first save), PUT
// validates the composer block vocabulary with optimistic-version
// conflicts, and edition creation materializes the stored list into
// the new issue's body. The publication-scoped media upload covers
// template art attached before any edition exists.

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func templateURL(pubID int64) string {
	return fmt.Sprintf("/api/v1/publications/%d/template", pubID)
}

// TestTemplateGetDefaults: before any save the effective template is
// one section slot per configured section — which materializes exactly
// like the legacy layout.
func TestTemplateGetDefaults(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	res := apiGet(t, srv, templateURL(pub.ID))["template"].(map[string]any)
	if res["version"] == "" {
		t.Fatal("template version empty")
	}
	blocks := res["blocks"].([]any)
	if len(blocks) != 3 {
		t.Fatalf("default blocks = %v, want 3 section slots", blocks)
	}
	for i, want := range []string{"O destaque", "Vale saber", "Links rápidos"} {
		b := blocks[i].(map[string]any)
		if b["kind"] != "section" || b["section"] != want {
			t.Fatalf("default block %d = %v, want section slot %q", i, b, want)
		}
	}
}

// TestTemplateSaveConflict: PUT stores the block list, the version
// rotates, a replayed save with the old token conflicts instead of
// clobbering, and a re-read returns the new list.
func TestTemplateSaveConflict(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	url := templateURL(pub.ID)
	v1 := apiGet(t, srv, url)["template"].(map[string]any)["version"].(string)

	blocks := []map[string]any{
		{"kind": "image", "text": "Banner", "url": "/m/1-ab12"},
		{"kind": "text", "text": "Header note."},
		{"kind": "section", "section": "O destaque"},
		{"kind": "section", "section": "Vale saber"},
		{"kind": "section", "section": "Links rápidos"},
	}
	rr := apiCall(t, srv, "PUT", url, map[string]any{"blocks": blocks, "version": v1})
	if rr.Code != 200 {
		t.Fatalf("PUT template = %d: %s", rr.Code, shortBody(rr))
	}
	saved := apiJSON(t, rr)["template"].(map[string]any)
	v2 := saved["version"].(string)
	if v2 == "" || v2 == v1 {
		t.Fatalf("version did not rotate: %q -> %q", v1, v2)
	}
	if len(saved["blocks"].([]any)) != len(blocks) {
		t.Fatalf("saved blocks = %v", saved["blocks"])
	}

	// stale save conflicts
	rr = apiCall(t, srv, "PUT", url, map[string]any{"blocks": blocks, "version": v1})
	if rr.Code != 409 || apiErrCode(t, rr) != "conflict" {
		t.Fatalf("stale PUT = %d (%s), want 409 conflict", rr.Code, shortBody(rr))
	}

	// the new list survives a re-read
	res := apiGet(t, srv, url)["template"].(map[string]any)
	got := res["blocks"].([]any)
	if len(got) != len(blocks) || got[0].(map[string]any)["kind"] != "image" {
		t.Fatalf("re-read blocks = %v", got)
	}
}

// TestTemplateValidation: unknown kinds, a section slot without a
// section, and payload violations the composer refuses are 400s — the
// template writes through the same blockLine rules.
func TestTemplateValidation(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	url := templateURL(pub.ID)
	v := apiGet(t, srv, url)["template"].(map[string]any)["version"].(string)

	for _, blocks := range []any{
		[]map[string]any{{"kind": "bogus", "text": "x"}},
		[]map[string]any{{"kind": "section"}},
		[]map[string]any{{"kind": "link", "text": "x", "url": "javascript:alert(1)"}},
		[]map[string]any{{"kind": "text", "text": "note\n<klox-embed>\n<p>x</p>\n</klox-embed>"}},
	} {
		rr := apiCall(t, srv, "PUT", url, map[string]any{"blocks": blocks, "version": v})
		if rr.Code != 400 {
			t.Fatalf("PUT %v = %d, want 400", blocks, rr.Code)
		}
	}
}

// TestTemplateMaterializesIntoEdition: the stored template arranges the
// next composed edition — fixed blocks lead in the untitled section,
// pauta slots emit their `##` heading in template order, and edition
// blocks stay ordinary composer blocks the instance can edit.
func TestTemplateMaterializesIntoEdition(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	url := templateURL(pub.ID)
	v := apiGet(t, srv, url)["template"].(map[string]any)["version"].(string)
	blocks := []map[string]any{
		{"kind": "text", "text": "Template header note."},
		{"kind": "section", "section": "Links rápidos"},
		{"kind": "section", "section": "O destaque"},
	}
	rr := apiCall(t, srv, "PUT", url, map[string]any{"blocks": blocks, "version": v})
	if rr.Code != 200 {
		t.Fatalf("PUT template = %d: %s", rr.Code, shortBody(rr))
	}

	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	if rr := apiCall(t, srv, "POST", issueURL+"/compose", nil); rr.Code != 200 {
		t.Fatalf("compose = %d: %s", rr.Code, shortBody(rr))
	}
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	secs := doc["sections"].([]any)
	if len(secs) < 3 {
		t.Fatalf("sections = %v", secs)
	}
	lead := secs[0].(map[string]any)
	if lead["title"] != "" {
		t.Fatalf("leading section title = %v, want untitled", lead["title"])
	}
	lb := lead["blocks"].([]any)
	if len(lb) != 1 || lb[0].(map[string]any)["kind"] != "text" ||
		lb[0].(map[string]any)["text"] != "Template header note." {
		t.Fatalf("leading blocks = %v", lb)
	}
	if secs[1].(map[string]any)["title"] != "Links rápidos" ||
		secs[2].(map[string]any)["title"] != "O destaque" {
		t.Fatalf("section order = %v %v, want template order",
			secs[1].(map[string]any)["title"], secs[2].(map[string]any)["title"])
	}

	// the composer edits the instance only: removing the materialized
	// header block must not write back into the stored template.
	ver := doc["version"].(string)
	rr = apiCall(t, srv, "DELETE", issueURL+"/blocks", map[string]any{
		"section": "", "index": 0, "version": ver,
	})
	if rr.Code != 200 {
		t.Fatalf("remove header block = %d: %s", rr.Code, shortBody(rr))
	}
	tpl := apiGet(t, srv, url)["template"].(map[string]any)
	if tpl["blocks"].([]any)[0].(map[string]any)["kind"] != "text" {
		t.Fatalf("edition edit wrote back to the template: %v", tpl["blocks"])
	}
}

// TestPubMediaUpload: template art uploads against the publication —
// before any edition exists — and lands on the same public /m/ path
// the edition upload uses.
func TestPubMediaUpload(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fakepngdata")...)
	rr := apiCall(t, srv, "POST",
		fmt.Sprintf("/api/v1/publications/%d/media", pub.ID),
		map[string]any{
			"content_type": "image/png",
			"data_b64":     base64.StdEncoding.EncodeToString(png),
			"name":         "banner.png",
		})
	if rr.Code != 201 {
		t.Fatalf("pub media upload = %d: %s", rr.Code, shortBody(rr))
	}
	m := apiJSON(t, rr)["media"].(map[string]any)
	url, _ := m["url"].(string)
	if !strings.HasPrefix(url, "/m/") {
		t.Fatalf("media url = %q, want /m/ path", url)
	}
}

// TestMediaDiesWithPublication: deleting a Radar removes its media rows
// at the origin — a /m/ URL that served bytes before the delete returns
// 404 after it. Caches can still hold a previously fetched copy: shared
// caches are barred by `private` (D69), the viewer's own browser is the
// accepted residual.
func TestMediaDiesWithPublication(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fakepngdata")...)
	rr := apiCall(t, srv, "POST",
		fmt.Sprintf("/api/v1/publications/%d/media", pub.ID),
		map[string]any{
			"content_type": "image/png",
			"data_b64":     base64.StdEncoding.EncodeToString(png),
			"name":         "banner.png",
		})
	if rr.Code != 201 {
		t.Fatalf("pub media upload = %d: %s", rr.Code, shortBody(rr))
	}
	url := apiJSON(t, rr)["media"].(map[string]any)["url"].(string)

	before := apiCall(t, srv, "GET", url, nil)
	if before.Code != 200 || before.Body.Len() == 0 {
		t.Fatalf("GET %s before delete = %d (%d bytes)", url, before.Code, before.Body.Len())
	}
	if cc := before.Header().Get("Cache-Control"); !strings.Contains(cc, "private") {
		t.Fatalf("media Cache-Control = %q, want private so shared caches drop deleted media", cc)
	}

	if rr := apiCall(t, srv, "DELETE",
		fmt.Sprintf("/api/v1/publications/%d", pub.ID),
		map[string]any{"confirm": true}); rr.Code != 204 {
		t.Fatalf("DELETE publication = %d: %s", rr.Code, shortBody(rr))
	}
	if rr := apiCall(t, srv, "GET", url, nil); rr.Code != 404 {
		t.Fatalf("GET %s after delete = %d, want 404", url, rr.Code)
	}
}
