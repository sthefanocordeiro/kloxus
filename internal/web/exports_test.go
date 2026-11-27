package web_test

// Contract tests for the export mark (KLOX-184): copying or downloading
// a rendition records the format, the piece reads "Exportada" in the
// lists, and the list rows carry a content title plus the formats that
// left — never the bare "pauta N".

import (
	"context"
	"fmt"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func pieceByID(t *testing.T, pieces []any, id int64) map[string]any {
	t.Helper()
	for _, p := range pieces {
		m := p.(map[string]any)
		if int64(m["id"].(float64)) == id {
			return m
		}
	}
	t.Fatalf("piece %d not in %v", id, pieces)
	return nil
}

func issueByID(t *testing.T, issues []any, id int64) map[string]any {
	t.Helper()
	for _, p := range issues {
		m := p.(map[string]any)
		if int64(m["id"].(float64)) == id {
			return m
		}
	}
	t.Fatalf("issue %d not in %v", id, issues)
	return nil
}

func TestIssueExportedMark(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	iid := approvedIssue(t, db, pub.ID, 91, "Futuro Próximo — pauta 91")

	// a story on the pauta names it in the lists
	storyID, err := db.CreateStory(ctx, pub.ID, "Fim da escala 6×1 no Brasil", 1, "klox184-story")
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	if _, err := db.CreateIssueItem(ctx, store.IssueItem{
		IssueID: iid, StoryID: storyID, Section: "Section",
		Status: "approved", Origin: "manual", Position: 0,
	}); err != nil {
		t.Fatalf("CreateIssueItem: %v", err)
	}

	// an unfinished pauta cannot be marked
	draftID, err := db.CreateIssue(ctx, pub.ID, 92, "Rascunho", "# R\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := func(id int64) string { return fmt.Sprintf("/api/v1/issues/%d/exported", id) }
	if rr := apiCall(t, srv, "POST", base(draftID), map[string]any{"format": "linkedin"}); rr.Code != 409 {
		t.Fatalf("export of draft = %d: %s", rr.Code, shortBody(rr))
	}
	if rr := apiCall(t, srv, "POST", base(iid), map[string]any{"format": "pdf"}); rr.Code != 400 {
		t.Fatalf("bad format = %d", rr.Code)
	}

	// copy LinkedIn, then download Markdown — both are remembered once
	for _, f := range []string{"linkedin", "markdown", "linkedin"} {
		rr := apiCall(t, srv, "POST", base(iid), map[string]any{"format": f})
		if rr.Code != 200 {
			t.Fatalf("export %s = %d: %s", f, rr.Code, shortBody(rr))
		}
		if apiJSON(t, rr)["exported"] != true {
			t.Fatalf("export %s answer = %s", f, rr.Body.String())
		}
	}

	// the Pautas list: content title, export mark and formats
	pubID := fmt.Sprint(pub.ID)
	issues := apiGet(t, srv, "/api/v1/publications/"+pubID+"/issues")["issues"].([]any)
	row := issueByID(t, issues, iid)
	if row["title"] != "Fim da escala 6×1 no Brasil" {
		t.Fatalf("title = %v", row["title"])
	}
	if row["exported"] != true {
		t.Fatalf("exported = %v", row["exported"])
	}
	formats := row["exported_formats"].([]any)
	if len(formats) != 2 {
		t.Fatalf("exported_formats = %v", formats)
	}

	// Hoje's pieces read the same: exported + formats, content title
	today := apiGet(t, srv, "/api/v1/publications/"+pubID+"/today")
	piece := pieceByID(t, today["pieces"].([]any), iid)
	if piece["status"] != "exported" {
		t.Fatalf("piece status = %v", piece["status"])
	}
	if piece["title"] != "Fim da escala 6×1 no Brasil" {
		t.Fatalf("piece title = %v", piece["title"])
	}
	if n := len(piece["exported_formats"].([]any)); n != 2 {
		t.Fatalf("piece exported_formats = %v", piece["exported_formats"])
	}
	if piece["created_at"] == "" {
		t.Fatal("piece has no created_at")
	}
}
