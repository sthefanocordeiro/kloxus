package web_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func installDossierInsertFailure(t *testing.T, db *store.DB) func() {
	t.Helper()
	ws := strings.ReplaceAll(db.Workspace(), "-", "_")
	rawWS := db.Workspace()
	fn := "klox263_fail_dossier_" + ws
	trg := fn
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.workspace_id = '%s' THEN
				RAISE EXCEPTION 'forced dossier insert failure';
			END IF;
			RETURN NEW;
		END;
		$$`, fn, rawWS)); err != nil {
		t.Fatalf("create dossier failure function: %v", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT ON dossiers
		FOR EACH ROW EXECUTE FUNCTION %s()`, trg, fn)); err != nil {
		t.Fatalf("create dossier failure trigger: %v", err)
	}
	var once sync.Once
	drop := func() {
		once.Do(func() {
			if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON dossiers`, trg)); err != nil {
				t.Errorf("drop dossier failure trigger: %v", err)
			}
			if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn)); err != nil {
				t.Errorf("drop dossier failure function: %v", err)
			}
		})
	}
	t.Cleanup(drop)
	return drop
}

func installReceiptInsertFailure(t *testing.T, db *store.DB, issueID int64) func() {
	t.Helper()
	fn := fmt.Sprintf("klox263_fail_receipt_%d", issueID)
	trg := fn
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.issue_id = %d THEN
				RAISE EXCEPTION 'forced approval receipt failure';
			END IF;
			RETURN NEW;
		END;
		$$`, fn, issueID)); err != nil {
		t.Fatalf("create receipt failure function: %v", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT ON approval_receipts
		FOR EACH ROW EXECUTE FUNCTION %s()`, trg, fn)); err != nil {
		t.Fatalf("create receipt failure trigger: %v", err)
	}
	var once sync.Once
	drop := func() {
		once.Do(func() {
			if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON approval_receipts`, trg)); err != nil {
				t.Errorf("drop receipt failure trigger: %v", err)
			}
			if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn)); err != nil {
				t.Errorf("drop receipt failure function: %v", err)
			}
		})
	}
	t.Cleanup(drop)
	return drop
}

func installIssueUpdateFailure(t *testing.T, db *store.DB, issueID int64) func() {
	t.Helper()
	fn := fmt.Sprintf("klox263_fail_issue_update_%d", issueID)
	trg := fn
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.workspace_id = '%s' AND NEW.id = %d THEN
				RAISE EXCEPTION 'forced issue body save failure';
			END IF;
			RETURN NEW;
		END;
		$$`, fn, db.Workspace(), issueID)); err != nil {
		t.Fatalf("create issue failure function: %v", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE UPDATE ON issues
		FOR EACH ROW EXECUTE FUNCTION %s()`, trg, fn)); err != nil {
		t.Fatalf("create issue failure trigger: %v", err)
	}
	var once sync.Once
	drop := func() {
		once.Do(func() {
			if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON issues`, trg)); err != nil {
				t.Errorf("drop issue failure trigger: %v", err)
			}
			if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn)); err != nil {
				t.Errorf("drop issue failure function: %v", err)
			}
		})
	}
	t.Cleanup(drop)
	return drop
}

func installPlacementFailure(t *testing.T, db *store.DB, issueItemID int64, position int) func() {
	t.Helper()
	fn := fmt.Sprintf("klox263_fail_placement_%d_%d", issueItemID, position)
	trg := fn
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.workspace_id = '%s' AND NEW.id = %d AND NEW.position = %d THEN
				RAISE EXCEPTION 'forced lineup write failure';
			END IF;
			RETURN NEW;
		END;
		$$`, fn, db.Workspace(), issueItemID, position)); err != nil {
		t.Fatalf("create placement failure function: %v", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE UPDATE ON issue_items
		FOR EACH ROW EXECUTE FUNCTION %s()`, trg, fn)); err != nil {
		t.Fatalf("create placement failure trigger: %v", err)
	}
	var once sync.Once
	drop := func() {
		once.Do(func() {
			if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON issue_items`, trg)); err != nil {
				t.Errorf("drop placement failure trigger: %v", err)
			}
			if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn)); err != nil {
				t.Errorf("drop placement failure function: %v", err)
			}
		})
	}
	t.Cleanup(drop)
	return drop
}

func manualStory(t *testing.T, db *store.DB, pubID int64, label string) int64 {
	t.Helper()
	ctx := context.Background()
	var itemID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO items
		(workspace_id, external_id, url, canonical_url, title, content_text, kind)
		VALUES ($1,$2,$3,$3,$4,$5,'article') RETURNING id`,
		db.Workspace(), label, "https://example.com/"+label, "Story "+label, "A sourced body for "+label).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	storyID, err := db.CreateStory(ctx, pubID, "Story "+label, 1, "https://example.com/"+label)
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	if err := db.AddStoryItem(ctx, storyID, itemID); err != nil {
		t.Fatalf("AddStoryItem: %v", err)
	}
	return storyID
}

func issueCount(t *testing.T, db *store.DB, pubID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM issues WHERE workspace_id = $1 AND publication_id = $2`,
		db.Workspace(), pubID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func decisionCount(t *testing.T, db *store.DB, pubID int64, kind string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM decisions WHERE workspace_id = $1 AND publication_id = $2 AND kind = $3`,
		db.Workspace(), pubID, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApproveTransactionalRollback(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	issueID, err := db.CreateIssue(ctx, pub.ID, 1, "Approval rollback", "# Approval rollback\n\n", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	drop := installReceiptInsertFailure(t, db, issueID)

	rr := apiCall(t, srv, "POST", fmt.Sprintf("/api/v1/issues/%d/approve", issueID), nil)
	if rr.Code == 200 {
		t.Fatalf("approve unexpectedly succeeded despite forced receipt failure: %s", shortBody(rr))
	}
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status == store.IssueApproved {
		t.Fatalf("failed receipt write left issue approved: %+v", iss)
	}
	if _, err := db.ApprovalReceipt(ctx, issueID); err != sql.ErrNoRows {
		t.Fatalf("receipt after rollback: err=%v, want sql.ErrNoRows", err)
	}
	var approvals int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM decisions WHERE workspace_id = $1 AND publication_id = $2 AND kind = 'approve' AND reason = $3`,
		db.Workspace(), pub.ID, fmt.Sprintf("issue %d approved", issueID)).Scan(&approvals); err != nil {
		t.Fatal(err)
	}
	if approvals != 0 {
		t.Fatalf("failed receipt write left %d approve decisions", approvals)
	}

	drop()
	rr = apiCall(t, srv, "POST", fmt.Sprintf("/api/v1/issues/%d/approve", issueID), nil)
	if rr.Code != 200 {
		t.Fatalf("approve success path = %d: %s", rr.Code, shortBody(rr))
	}
	iss, err = db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if iss.Status != store.IssueApproved {
		t.Fatalf("status after successful approve = %q", iss.Status)
	}
	if _, err := db.ApprovalReceipt(ctx, issueID); err != nil {
		t.Fatalf("receipt after successful approve: %v", err)
	}
}

func TestPautaCreateTransactionalRollback(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	storyID := manualStory(t, db, pub.ID, "pauta-create-rollback")
	before := issueCount(t, db, pub.ID)
	drop := installDossierInsertFailure(t, db)

	rr := apiCall(t, srv, "POST", fmt.Sprintf("/api/v1/publications/%d/pauta", pub.ID),
		map[string]any{"story_ids": []int64{storyID}})
	if rr.Code == 201 {
		t.Fatalf("pauta create unexpectedly succeeded despite forced compose failure: %s", shortBody(rr))
	}
	if got := issueCount(t, db, pub.ID); got != before {
		t.Fatalf("failed pauta create left issues: got %d want %d", got, before)
	}
	var items int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM issue_items WHERE workspace_id = $1 AND story_id = $2`,
		db.Workspace(), storyID).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 0 {
		t.Fatalf("failed pauta create left %d issue items", items)
	}

	drop()
	rr = apiCall(t, srv, "POST", fmt.Sprintf("/api/v1/publications/%d/pauta", pub.ID),
		map[string]any{"story_ids": []int64{storyID}})
	if rr.Code != 201 {
		t.Fatalf("pauta create success path = %d: %s", rr.Code, shortBody(rr))
	}
	if got := issueCount(t, db, pub.ID); got != before+1 {
		t.Fatalf("issues after success = %d, want %d", got, before+1)
	}
}

func TestTodayWriteTransactionalRollback(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	storyID := manualStory(t, db, pub.ID, "today-write-rollback")
	before := issueCount(t, db, pub.ID)
	beforeDecisions := decisionCount(t, db, pub.ID, "manual_compose")
	drop := installDossierInsertFailure(t, db)

	rr := apiCall(t, srv, "POST", fmt.Sprintf("/api/v1/publications/%d/today/write", pub.ID),
		map[string]any{"story_id": storyID})
	if rr.Code == 200 {
		t.Fatalf("today write unexpectedly succeeded despite forced compose failure: %s", shortBody(rr))
	}
	if got := issueCount(t, db, pub.ID); got != before {
		t.Fatalf("failed today write left issues: got %d want %d", got, before)
	}
	if got := decisionCount(t, db, pub.ID, "manual_compose"); got != beforeDecisions {
		t.Fatalf("failed today write left decisions: got %d want %d", got, beforeDecisions)
	}
	var items int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM issue_items WHERE workspace_id = $1 AND story_id = $2`,
		db.Workspace(), storyID).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 0 {
		t.Fatalf("failed today write left %d issue items", items)
	}

	drop()
	rr = apiCall(t, srv, "POST", fmt.Sprintf("/api/v1/publications/%d/today/write", pub.ID),
		map[string]any{"story_id": storyID})
	if rr.Code != 200 {
		t.Fatalf("today write success path = %d: %s", rr.Code, shortBody(rr))
	}
	if got := issueCount(t, db, pub.ID); got != before+1 {
		t.Fatalf("issues after success = %d, want %d", got, before+1)
	}
	if !strings.Contains(rr.Body.String(), `"created":true`) {
		t.Fatalf("success response does not report created piece: %s", shortBody(rr))
	}
}

func countIssueItems(t *testing.T, db *store.DB, issueID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM issue_items WHERE workspace_id = $1 AND issue_id = $2`,
		db.Workspace(), issueID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countIssueDossiers(t *testing.T, db *store.DB, issueID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM dossiers d
		 JOIN issue_items ii ON ii.id = d.issue_item_id
		 WHERE d.workspace_id = $1 AND ii.issue_id = $2`,
		db.Workspace(), issueID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func markStoryTriaged(t *testing.T, db *store.DB, storyID int64, score float64) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`UPDATE stories SET triage_score = $1, triage_reason = 'rollback test' WHERE workspace_id = $2 AND id = $3`,
		score, db.Workspace(), storyID); err != nil {
		t.Fatalf("mark story triaged: %v", err)
	}
}

func issueItemPositions(t *testing.T, db *store.DB, issueID int64) map[int64]int {
	t.Helper()
	items, err := db.IssueItems(context.Background(), issueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	out := map[int64]int{}
	for _, ii := range items {
		out[ii.ID] = ii.Position
	}
	return out
}

func issueItemStatuses(t *testing.T, db *store.DB, issueID int64) map[int64]string {
	t.Helper()
	items, err := db.IssueItems(context.Background(), issueID)
	if err != nil {
		t.Fatalf("IssueItems: %v", err)
	}
	out := map[int64]string{}
	for _, ii := range items {
		out[ii.ID] = ii.Status
	}
	return out
}

func TestLineupTransactionalRollback(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	storyA := manualStory(t, db, pub.ID, "lineup-rollback-a")
	storyB := manualStory(t, db, pub.ID, "lineup-rollback-b")
	storyC := manualStory(t, db, pub.ID, "lineup-rollback-c")
	markStoryTriaged(t, db, storyA, 9)
	markStoryTriaged(t, db, storyB, 8)
	markStoryTriaged(t, db, storyC, 7)
	issueID, err := db.CreateIssue(ctx, pub.ID, 1, "Lineup rollback", "# Lineup rollback\n\n", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if len(pub.Sections) < 2 {
		t.Fatalf("demo publication has no second section: %+v", pub.Sections)
	}
	section := pub.Sections[1].Title
	itemA, err := db.CreateIssueItem(ctx, store.IssueItem{
		IssueID: issueID, StoryID: storyA, Section: section,
		Status: store.ItemApproved, Origin: "manual", Position: 0,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem A: %v", err)
	}
	itemB, err := db.CreateIssueItem(ctx, store.IssueItem{
		IssueID: issueID, StoryID: storyB, Section: section,
		Status: store.ItemApproved, Origin: "manual", Position: 1,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem B: %v", err)
	}
	itemC, err := db.CreateIssueItem(ctx, store.IssueItem{
		IssueID: issueID, StoryID: storyC, Section: section,
		Status: store.ItemApproved, Origin: "manual", Position: 2,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem C: %v", err)
	}
	entries := []map[string]any{
		{"story_id": storyB, "section": section},
		{"story_id": storyA, "section": section},
	}
	drop := installPlacementFailure(t, db, itemC, 2)

	rr := apiCall(t, srv, "PUT", fmt.Sprintf("/api/v1/issues/%d/lineup", issueID),
		map[string]any{"entries": entries})
	if rr.Code == 200 {
		t.Fatalf("lineup unexpectedly succeeded despite forced lineup write failure: %s", shortBody(rr))
	}
	positions := issueItemPositions(t, db, issueID)
	if positions[itemA] != 0 || positions[itemB] != 1 || positions[itemC] != 2 {
		t.Fatalf("failed lineup changed prior positions: A=%d want 0, B=%d want 1, C=%d want 2",
			positions[itemA], positions[itemB], positions[itemC])
	}
	statuses := issueItemStatuses(t, db, issueID)
	if statuses[itemC] != store.ItemApproved {
		t.Fatalf("failed lineup changed omitted item status: C=%q want %q", statuses[itemC], store.ItemApproved)
	}

	drop()
	rr = apiCall(t, srv, "PUT", fmt.Sprintf("/api/v1/issues/%d/lineup", issueID),
		map[string]any{"entries": entries})
	if rr.Code != 200 {
		t.Fatalf("lineup success path = %d: %s", rr.Code, shortBody(rr))
	}
	positions = issueItemPositions(t, db, issueID)
	if positions[itemA] != 1 || positions[itemB] != 0 {
		t.Fatalf("successful lineup positions: A=%d want 1, B=%d want 0", positions[itemA], positions[itemB])
	}
	statuses = issueItemStatuses(t, db, issueID)
	if statuses[itemC] != store.ItemRemoved {
		t.Fatalf("omitted item status after successful lineup: C=%q want %q", statuses[itemC], store.ItemRemoved)
	}
}

func TestBlockInsertTransactionalRollback(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	storyID := manualStory(t, db, pub.ID, "block-insert-rollback")
	initialBody := "# Block insert rollback\n\n"
	issueID, err := db.CreateIssue(ctx, pub.ID, 1, "Block insert rollback", initialBody, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issueURL := fmt.Sprintf("/api/v1/issues/%d", issueID)
	section := pub.Sections[1].Title
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	drop := installIssueUpdateFailure(t, db, issueID)

	rr := apiCall(t, srv, "POST", issueURL+"/blocks", map[string]any{
		"section": section, "kind": "item", "story_id": storyID, "version": doc["version"],
	})
	if rr.Code == 200 {
		t.Fatalf("block insert unexpectedly succeeded despite forced body failure: %s", shortBody(rr))
	}
	if got := countIssueItems(t, db, issueID); got != 0 {
		t.Fatalf("failed block insert left %d issue items", got)
	}
	if got := countIssueDossiers(t, db, issueID); got != 0 {
		t.Fatalf("failed block insert left %d dossiers", got)
	}
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.BodyMD != initialBody {
		t.Fatalf("failed block insert changed body: %q", iss.BodyMD)
	}

	drop()
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	rr = apiCall(t, srv, "POST", issueURL+"/blocks", map[string]any{
		"section": section, "kind": "item", "story_id": storyID, "version": doc["version"],
	})
	if rr.Code != 200 {
		t.Fatalf("block insert success path = %d: %s", rr.Code, shortBody(rr))
	}
	if got := countIssueItems(t, db, issueID); got != 1 {
		t.Fatalf("issue items after successful insert = %d, want 1", got)
	}
	if got := countIssueDossiers(t, db, issueID); got != 1 {
		t.Fatalf("dossiers after successful insert = %d, want 1", got)
	}
}

func TestBlockRemoveTransactionalRollback(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	storyID := manualStory(t, db, pub.ID, "block-remove-rollback")
	issueID, err := db.CreateIssue(ctx, pub.ID, 1, "Block remove rollback", "# Block remove rollback\n\n", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issueURL := fmt.Sprintf("/api/v1/issues/%d", issueID)
	section := pub.Sections[1].Title
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	rr := apiCall(t, srv, "POST", issueURL+"/blocks", map[string]any{
		"section": section, "kind": "item", "story_id": storyID, "version": doc["version"],
	})
	if rr.Code != 200 {
		t.Fatalf("seed block insert = %d: %s", rr.Code, shortBody(rr))
	}

	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	blocks := docBlocks(t, doc, section)
	itemIndex, ref := -1, 0
	for i, b := range blocks {
		if b["kind"] == "item" {
			itemIndex, ref = i, int(b["ref"].(float64))
		}
	}
	if itemIndex < 0 {
		t.Fatalf("inserted item block missing: %+v", blocks)
	}
	issBefore, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue before remove: %v", err)
	}
	marker := fmt.Sprintf("<!-- item %d -->", ref)
	if !strings.Contains(issBefore.BodyMD, marker) {
		t.Fatalf("seeded body does not contain %q: %q", marker, issBefore.BodyMD)
	}
	drop := installIssueUpdateFailure(t, db, issueID)

	rr = apiCall(t, srv, "DELETE", issueURL+"/blocks", map[string]any{
		"section": section, "index": itemIndex, "version": doc["version"],
	})
	if rr.Code == 200 {
		t.Fatalf("block remove unexpectedly succeeded despite forced body failure: %s", shortBody(rr))
	}
	items, err := db.IssueItems(ctx, issueID)
	if err != nil {
		t.Fatalf("IssueItems after failed remove: %v", err)
	}
	if len(items) != 1 || items[0].Status != store.ItemApproved {
		t.Fatalf("failed block remove changed lineup status: %+v", items)
	}
	issAfter, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue after failed remove: %v", err)
	}
	if issAfter.BodyMD != issBefore.BodyMD {
		t.Fatalf("failed block remove changed body: %q", issAfter.BodyMD)
	}

	drop()
	doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	rr = apiCall(t, srv, "DELETE", issueURL+"/blocks", map[string]any{
		"section": section, "index": itemIndex, "version": doc["version"],
	})
	if rr.Code != 200 {
		t.Fatalf("block remove success path = %d: %s", rr.Code, shortBody(rr))
	}
	items, err = db.IssueItems(ctx, issueID)
	if err != nil {
		t.Fatalf("IssueItems after successful remove: %v", err)
	}
	if len(items) != 1 || items[0].Status != store.ItemRemoved {
		t.Fatalf("lineup status after successful remove: %+v", items)
	}
	issAfter, err = db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue after successful remove: %v", err)
	}
	if strings.Contains(issAfter.BodyMD, marker) {
		t.Fatalf("successful remove left item marker in body: %q", issAfter.BodyMD)
	}
}
