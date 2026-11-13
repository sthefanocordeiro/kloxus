package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestJobClaimExclusive: N concurrent claims on the same due job must
// hand it to exactly one worker (FOR UPDATE SKIP LOCKED).
func TestJobClaimExclusive(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()

	if _, err := db.EnqueueJob(ctx, JobKindSendIssue, 1, nil, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	const workers = 8
	claimed := make(chan int64, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs, err := pool.ClaimDueJobs(ctx, 1)
			if err != nil {
				t.Errorf("ClaimDueJobs: %v", err)
				return
			}
			if len(jobs) == 1 {
				claimed <- jobs[0].ID
			}
		}()
	}
	wg.Wait()
	close(claimed)
	var ids []int64
	for id := range claimed {
		ids = append(ids, id)
	}
	if len(ids) != 1 {
		t.Fatalf("job claimed by %d workers, want 1", len(ids))
	}
	if err := pool.FinishJob(ctx, ids[0]); err != nil {
		t.Fatalf("FinishJob: %v", err)
	}
	// done jobs are never re-claimed
	if jobs, _ := pool.ClaimDueJobs(ctx, 10); len(jobs) != 0 {
		t.Fatalf("finished job reclaimed: %+v", jobs)
	}
}

// TestJobFailRetry: a failed job under max attempts returns to pending;
// at max attempts it dies.
func TestJobFailRetry(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()

	id, err := db.EnqueueJob(ctx, JobKindSendIssue, 7, nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	jobs, _ := pool.ClaimDueJobs(ctx, 10)
	if len(jobs) != 1 {
		t.Fatalf("claim = %d jobs", len(jobs))
	}
	if err := pool.FailJob(ctx, id, fmt.Errorf("boom"), 3); err != nil {
		t.Fatalf("FailJob: %v", err)
	}
	var status string
	db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = $1`, id).Scan(&status)
	if status != "pending" {
		t.Fatalf("status after fail = %q, want pending retry", status)
	}
	// exhaust attempts → dead
	pool.ClaimDueJobs(ctx, 10)
	pool.FailJob(ctx, id, fmt.Errorf("boom"), 3)
	pool.ClaimDueJobs(ctx, 10)
	pool.FailJob(ctx, id, fmt.Errorf("boom"), 3)
	db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = $1`, id).Scan(&status)
	if status != "dead" {
		t.Fatalf("status after max attempts = %q, want dead", status)
	}
}

// TestDeleteIdleWorkspaces: rows past the cutoff go, fresh ones stay.
func TestDeleteIdleWorkspaces(t *testing.T) {
	pool := openPool(t)
	old := openOn(t, pool)
	fresh := openOn(t, pool)
	ctx := context.Background()

	oldTS := TSUTC(time.Now().Add(-30 * 24 * time.Hour))
	if _, err := pool.db.ExecContext(ctx,
		`UPDATE workspaces SET last_seen_at = $1 WHERE id = $2`, oldTS, old.ws); err != nil {
		t.Fatalf("age workspace: %v", err)
	}
	cutoff := TSUTC(time.Now().Add(-21 * 24 * time.Hour))
	n, err := pool.DeleteIdleWorkspaces(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteIdleWorkspaces: %v", err)
	}
	if n < 1 {
		t.Fatal("idle workspace not deleted")
	}
	if ok, _ := pool.WorkspaceExists(ctx, old.ws); ok {
		t.Fatal("old workspace survives")
	}
	if ok, _ := pool.WorkspaceExists(ctx, fresh.ws); !ok {
		t.Fatal("fresh workspace deleted")
	}
}
