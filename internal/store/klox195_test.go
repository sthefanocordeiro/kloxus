package store

import (
	"context"
	"testing"
	"time"
)

// TestRequeueStaleJobsUsesClaimTime: a job enqueued long ago but claimed
// just now must not be requeued (KLOX-195/A4); one claimed before the
// cutoff must.
func TestRequeueStaleJobsUsesClaimTime(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()

	id, err := db.EnqueueJob(ctx, JobKindSendIssue, 1, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := pool.db.ExecContext(ctx,
		`UPDATE jobs SET created_at = $2 WHERE id = $1`, id, TSUTC(time.Now().Add(-24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	jobs, err := pool.ClaimDueJobs(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	claimed := false
	for _, j := range jobs {
		claimed = claimed || j.ID == id
	}
	if !claimed {
		t.Fatal("job not claimed")
	}
	status := func() string {
		var s string
		if err := pool.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	cutoff := TSUTC(time.Now().Add(-15 * time.Minute))
	if err := pool.RequeueStaleJobs(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	if s := status(); s != "running" {
		t.Fatalf("freshly claimed job requeued: status=%s", s)
	}
	if _, err := pool.db.ExecContext(ctx,
		`UPDATE jobs SET claimed_at = $2 WHERE id = $1`, id, TSUTC(time.Now().Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := pool.RequeueStaleJobs(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	if s := status(); s != "pending" {
		t.Fatalf("stale claim not requeued: status=%s", s)
	}
}

// TestDeletePublicationWithPautaAndRenditions: pauta_days, renditions and
// issue_exports must not block or survive a publication delete (KLOX-195/A1).
func TestDeletePublicationWithPautaAndRenditions(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.CreatePublication(ctx, testPub())
	if err != nil {
		t.Fatalf("CreatePublication: %v", err)
	}
	issueID, err := db.CreateIssue(ctx, pubID, 1, "s", "b", testPub().Sections)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := db.SavePautaDay(ctx, pubID, "2026-11-24", "[]"); err != nil {
		t.Fatalf("SavePautaDay: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO renditions (workspace_id, issue_id, format) VALUES ($1, $2, 'x')`, db.ws, issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO issue_exports (workspace_id, issue_id, format) VALUES ($1, $2, 'x')`, db.ws, issueID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeletePublication(ctx, pubID); err != nil {
		t.Fatalf("DeletePublication: %v", err)
	}
	for _, tbl := range []string{"pauta_days", "renditions", "issue_exports"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+tbl+` WHERE workspace_id = $1`, db.ws).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s: %d orphan rows after delete", tbl, n)
		}
	}
}
