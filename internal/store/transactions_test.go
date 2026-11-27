package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// installJobInsertFailure creates a trigger that fails only the job insert
// for one issue id. It lets the test prove ScheduleIssue rolls its status
// flip back when the queue half of the write fails.
func installJobInsertFailure(t *testing.T, db *DB, issueID int64) func() {
	t.Helper()
	fn := fmt.Sprintf("klox263_fail_job_insert_%d", issueID)
	trg := fmt.Sprintf("klox263_fail_job_insert_%d", issueID)
	if _, err := db.db.Exec(fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.workspace_id = '%s' AND NEW.ref_id = %d THEN
				RAISE EXCEPTION 'forced job insert failure';
			END IF;
			RETURN NEW;
		END;
		$$`, fn, db.ws, issueID)); err != nil {
		t.Fatalf("create job failure function: %v", err)
	}
	if _, err := db.db.Exec(fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT ON jobs
		FOR EACH ROW EXECUTE FUNCTION %s()`, trg, fn)); err != nil {
		t.Fatalf("create job failure trigger: %v", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if _, err := db.db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON jobs`, trg)); err != nil {
				t.Errorf("drop job failure trigger: %v", err)
			}
			if _, err := db.db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn)); err != nil {
				t.Errorf("drop job failure function: %v", err)
			}
		})
	}
}

func TestScheduleIssueTransactional(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	issueID, _, err := db.CreateNextIssue(ctx, pubID, nil, "", nil)
	if err != nil {
		t.Fatalf("CreateNextIssue: %v", err)
	}
	if err := db.SetIssueStatus(ctx, issueID, IssueApproved); err != nil {
		t.Fatalf("approve issue: %v", err)
	}

	drop := installJobInsertFailure(t, db, issueID)
	t.Cleanup(drop)
	at := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	err = db.ScheduleIssue(ctx, issueID, at)
	if err == nil || !strings.Contains(err.Error(), "forced job insert failure") {
		t.Fatalf("ScheduleIssue error = %v, want forced insert failure", err)
	}
	issue, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue after forced failure: %v", err)
	}
	if issue.Status != IssueApproved || issue.ScheduledAt != "" {
		t.Fatalf("failed schedule left partial issue state: status=%q scheduled_at=%q", issue.Status, issue.ScheduledAt)
	}
	var jobs int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM jobs WHERE workspace_id = $1 AND kind = $2 AND ref_id = $3`,
		db.ws, JobKindSendIssue, issueID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("failed schedule left %d jobs", jobs)
	}

	drop()
	if err := db.ScheduleIssue(ctx, issueID, at); err != nil {
		t.Fatalf("ScheduleIssue success path: %v", err)
	}
	issue, err = db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != IssueScheduled || issue.ScheduledAt != at {
		t.Fatalf("scheduled issue = status %q at %q, want %q/%q", issue.Status, issue.ScheduledAt, IssueScheduled, at)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM jobs WHERE workspace_id = $1 AND kind = $2 AND ref_id = $3 AND status = 'pending'`,
		db.ws, JobKindSendIssue, issueID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("pending send jobs = %d, want 1", jobs)
	}
}

// installStoryDeleteFailure fails only deletion of stories belonging to one
// publication. ClearStories deletes member links first, so this proves the
// link delete is rolled back when the stories delete fails.
func installStoryDeleteFailure(t *testing.T, db *DB, pubID int64) func() {
	t.Helper()
	fn := fmt.Sprintf("klox263_fail_story_delete_%d", pubID)
	trg := fmt.Sprintf("klox263_fail_story_delete_%d", pubID)
	if _, err := db.db.Exec(fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF OLD.workspace_id = '%s' AND OLD.publication_id = %d THEN
				RAISE EXCEPTION 'forced story delete failure';
			END IF;
			RETURN OLD;
		END;
		$$`, fn, db.ws, pubID)); err != nil {
		t.Fatalf("create story failure function: %v", err)
	}
	if _, err := db.db.Exec(fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE DELETE ON stories
		FOR EACH ROW EXECUTE FUNCTION %s()`, trg, fn)); err != nil {
		t.Fatalf("create story failure trigger: %v", err)
	}
	return func() {
		if _, err := db.db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON stories`, trg)); err != nil {
			t.Errorf("drop story failure trigger: %v", err)
		}
		if _, err := db.db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn)); err != nil {
			t.Errorf("drop story failure function: %v", err)
		}
	}
}

func TestClearStoriesTransactional(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	sourceID, err := db.AddSource(ctx, pubID, "rss", "https://example.com/feed", "Example")
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	var itemID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO items (workspace_id, source_id, external_id, title) VALUES ($1,$2,$3,$4) RETURNING id`,
		db.ws, sourceID, "item-1", "Item").Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	storyID, err := db.CreateStory(ctx, pubID, "Story", 1, "https://example.com/story")
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	if err := db.AddStoryItem(ctx, storyID, itemID); err != nil {
		t.Fatalf("AddStoryItem: %v", err)
	}

	drop := installStoryDeleteFailure(t, db, pubID)
	t.Cleanup(drop)
	err = db.ClearStories(ctx, pubID)
	if err == nil || !strings.Contains(err.Error(), "forced story delete failure") {
		t.Fatalf("ClearStories error = %v, want forced delete failure", err)
	}
	var stories, links int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM stories WHERE workspace_id = $1 AND publication_id = $2`, db.ws, pubID).Scan(&stories); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM story_items WHERE workspace_id = $1 AND story_id = $2`, db.ws, storyID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if stories != 1 || links != 1 {
		t.Fatalf("failed clear left partial state: stories=%d links=%d, want 1/1", stories, links)
	}

	drop()
	if err := db.ClearStories(ctx, pubID); err != nil {
		t.Fatalf("ClearStories success path: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM stories WHERE workspace_id = $1 AND publication_id = $2`, db.ws, pubID).Scan(&stories); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM story_items WHERE workspace_id = $1 AND story_id = $2`, db.ws, storyID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if stories != 0 || links != 0 {
		t.Fatalf("clear success left rows: stories=%d links=%d", stories, links)
	}
}
