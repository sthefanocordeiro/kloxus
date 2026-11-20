package web_test

// KLOX-95 handler coverage: the suggestion board's enrichment fields
// (summary, article type, language, publish time), the default
// foreign-language/off-topic bench filter with the honest
// hidden_bench count, and the ?all=1 toggle. All deterministic —
// enrichments are seeded rows, no model is called.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestCandidatesFilteredBench seeds four triaged stories — on-topic pt,
// off-topic, English and un-enriched — and checks what the bench serves
// with and without ?all=1.
func TestCandidatesFilteredBench(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)

	issueID, err := db.CreateIssue(ctx, pub.ID, 1, "s", "", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	srcs, err := db.Sources(ctx, pub.ID)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("Sources: %v", err)
	}
	nonce := fmt.Sprintf("cand-%d", time.Now().UnixNano())
	mkStory := func(ext, title, publishedAt string, score float64) (storyID, itemID int64) {
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind, published_at)
			 VALUES ($1,$2,$3,$4,$5,'body','article',$6) RETURNING id`,
			db.Workspace(), srcs[0].ID, ext, "https://x.example/"+ext, title, publishedAt).Scan(&itemID); err != nil {
			t.Fatalf("insert item %s: %v", ext, err)
		}
		var err error
		if storyID, err = db.CreateStory(ctx, pub.ID, title, 1, "https://x.example/"+ext); err != nil {
			t.Fatalf("CreateStory %s: %v", title, err)
		}
		if err := db.AddStoryItem(ctx, storyID, itemID); err != nil {
			t.Fatalf("AddStoryItem %s: %v", title, err)
		}
		if err := db.SetStoryTriage(ctx, storyID, score, "seeded"); err != nil {
			t.Fatalf("SetStoryTriage %s: %v", title, err)
		}
		return storyID, itemID
	}
	enrich := func(itemID int64, kind, summary, lang, topics string) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO item_enrichment (item_id, ai_kind, promotional, topics_json, summary_pt, language, model)
			 VALUES ($1,$2,false,$3,$4,$5,'test')`, itemID, kind, topics, summary, lang); err != nil {
			t.Fatalf("enrich item %d: %v", itemID, err)
		}
	}

	// The fixture publication is pt-BR, topic "Tecnologia e
	// tendências…" — tecnologia tags match, cripto does not.
	_, keepItem := mkStory(nonce+"-a", "Pauta no tema", "2026-11-20T10:00:00Z", 9)
	_, offItem := mkStory(nonce+"-b", "Pauta fora do tema", "2026-11-21T10:00:00Z", 8)
	_, enItem := mkStory(nonce+"-c", "English story", "2026-11-22T10:00:00Z", 7)
	mkStory(nonce+"-d", "Sem classificação", "2026-11-19T10:00:00Z", 6)
	enrich(keepItem, "noticia", "Resumo da pauta.", "pt", `["tecnologia"]`)
	enrich(offItem, "analise", "Off topic.", "pt", `["cripto"]`)
	enrich(enItem, "noticia", "English summary.", "en", `["tecnologia"]`)

	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	get := func(q string) map[string]any {
		rr := apiCall(t, srv, "GET", issueURL+"/candidates"+q, nil)
		if rr.Code != 200 {
			t.Fatalf("candidates %q = %d: %s", q, rr.Code, shortBody(rr))
		}
		return apiJSON(t, rr)
	}
	byTitle := func(resp map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, c := range resp["candidates"].([]any) {
			m := c.(map[string]any)
			out[m["title"].(string)] = m
		}
		return out
	}

	// Default view: the two hidden stories stay out; the count is
	// honest; the visible on-topic card carries every enrichment field.
	def := get("")
	if n := def["hidden_bench"].(float64); n != 2 {
		t.Fatalf("hidden_bench = %v, want 2", n)
	}
	m := byTitle(def)
	if _, ok := m["Pauta fora do tema"]; ok {
		t.Error("off-topic suggestion leaked into the default bench")
	}
	if _, ok := m["English story"]; ok {
		t.Error("foreign-language suggestion leaked into the default bench")
	}
	keep := m["Pauta no tema"]
	if keep == nil {
		t.Fatal("on-topic suggestion missing from the bench")
	}
	if keep["kind"] != "noticia" || keep["language"] != "pt" || keep["summary"] != "Resumo da pauta." {
		t.Errorf("enriched fields = %v %v %v", keep["kind"], keep["language"], keep["summary"])
	}
	if keep["published_at"] != "2026-11-20T10:00:00Z" {
		t.Errorf("published_at = %v", keep["published_at"])
	}
	if topics, ok := keep["topics"].([]any); !ok || len(topics) != 1 || topics[0] != "tecnologia" {
		t.Errorf("topics = %v", keep["topics"])
	}
	bare := m["Sem classificação"]
	if bare == nil {
		t.Fatal("un-enriched suggestion must stay visible")
	}
	// publish time is item metadata, not AI output — present without
	// enrichment; the classified fields stay absent.
	if bare["published_at"] != "2026-11-19T10:00:00Z" {
		t.Errorf("bare published_at = %v", bare["published_at"])
	}
	for _, k := range []string{"summary", "kind", "language", "topics", "foreign", "off_topic"} {
		if _, present := bare[k]; present {
			t.Errorf("un-enriched card carries %s: %v", k, bare[k])
		}
	}

	// ?all=1 serves the hidden suggestions back, flagged with the
	// reason — the count stays the same either way.
	all := get("?all=1")
	if n := all["hidden_bench"].(float64); n != 2 {
		t.Fatalf("hidden_bench with all=1 = %v, want 2", n)
	}
	am := byTitle(all)
	if len(am) != len(m)+2 {
		t.Fatalf("all=1 candidates = %d, want %d", len(am), len(m)+2)
	}
	if !am["Pauta fora do tema"]["off_topic"].(bool) {
		t.Error("off-topic flag missing on the revealed card")
	}
	if am["Pauta fora do tema"]["foreign"] != nil {
		t.Error("pt-language card must not be flagged foreign")
	}
	if !am["English story"]["foreign"].(bool) {
		t.Error("foreign flag missing on the revealed card")
	}
	if am["English story"]["off_topic"] != nil {
		t.Error("on-topic en card must not be flagged off_topic")
	}
	// bench_total counts what the current view serves
	if n := def["bench_total"].(float64); n != 2 {
		t.Errorf("default bench_total = %v, want 2", n)
	}
	if n := all["bench_total"].(float64); n != 4 {
		t.Errorf("all=1 bench_total = %v, want 4", n)
	}
}

// TestCandidatesLineupKeepsHiddenSeats: a hidden-by-default story the
// editor deliberately placed stays in the issue — the filter is a
// bench view, never a lineup eviction.
func TestCandidatesLineupKeepsHiddenSeats(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)

	issueID, err := db.CreateIssue(ctx, pub.ID, 1, "s", "", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	srcs, _ := db.Sources(ctx, pub.ID)
	nonce := fmt.Sprintf("seat-%d", time.Now().UnixNano())
	var itemID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind)
		 VALUES ($1,$2,$3,$4,$5,'body','article') RETURNING id`,
		db.Workspace(), srcs[0].ID, nonce, "https://x.example/"+nonce, "English seat").Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	storyID, err := db.CreateStory(ctx, pub.ID, "English seat", 1, "https://x.example/"+nonce)
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	if err := db.AddStoryItem(ctx, storyID, itemID); err != nil {
		t.Fatalf("AddStoryItem: %v", err)
	}
	if err := db.SetStoryTriage(ctx, storyID, 9, "seeded"); err != nil {
		t.Fatalf("SetStoryTriage: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO item_enrichment (item_id, ai_kind, promotional, topics_json, summary_pt, language, model)
		 VALUES ($1,'noticia',false,'["tecnologia"]','Seat summary.','en','test')`, itemID); err != nil {
		t.Fatalf("enrich: %v", err)
	}

	// Place it through the lineup endpoint — the hidden-by-default
	// story is a valid entry, and afterwards it rides the in-issue
	// payload on every page, filter or not.
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	rr := apiCall(t, srv, "PUT", issueURL+"/lineup",
		map[string]any{"entries": []map[string]any{{"story_id": storyID, "section": "O destaque"}}})
	if rr.Code != 200 {
		t.Fatalf("lineup = %d: %s", rr.Code, shortBody(rr))
	}
	got := apiJSON(t, rr)
	if n := got["hidden_bench"].(float64); n != 0 {
		t.Fatalf("hidden_bench on a seated story = %v, want 0 — the filter is bench-only", n)
	}
	resp := apiGet(t, srv, issueURL+"/candidates")
	var seat map[string]any
	for _, c := range resp["candidates"].([]any) {
		m := c.(map[string]any)
		if m["title"] == "English seat" {
			seat = m
		}
	}
	if seat == nil {
		t.Fatal("seated foreign story missing from the default view")
	}
	if !seat["in_issue"].(bool) || !seat["foreign"].(bool) {
		t.Errorf("seated card = in_issue %v foreign %v", seat["in_issue"], seat["foreign"])
	}
}
