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
