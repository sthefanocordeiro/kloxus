package store

import (
	"context"
	"sync"
	"testing"
)

// TestRotateWorkspaceAuthEpochAtomic: concurrent logout rotations must
// serialize on the workspace row and each return a distinct next epoch.
// A lost increment would leave a pre-rotation credential valid.
func TestRotateWorkspaceAuthEpochAtomic(t *testing.T) {
	pool := openPool(t)
	db := openOn(t, pool)
	ctx := context.Background()

	const workers = 8
	start := make(chan struct{})
	epochs := make(chan int64, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			epoch, err := pool.RotateWorkspaceAuthEpoch(ctx, db.Workspace())
			if err != nil {
				errs <- err
				return
			}
			epochs <- epoch
		}()
	}
	close(start)
	wg.Wait()
	close(epochs)
	close(errs)
	for err := range errs {
		t.Fatalf("RotateWorkspaceAuthEpoch: %v", err)
	}
	seen := map[int64]bool{}
	for epoch := range epochs {
		if seen[epoch] {
			t.Fatalf("epoch %d returned twice — increment was not atomic", epoch)
		}
		seen[epoch] = true
	}
	if len(seen) != workers {
		t.Fatalf("rotations = %d, want %d", len(seen), workers)
	}
	epoch, ok, err := pool.WorkspaceAuthEpoch(ctx, db.Workspace())
	if err != nil || !ok {
		t.Fatalf("WorkspaceAuthEpoch = ok %v, err %v", ok, err)
	}
	if epoch != workers {
		t.Fatalf("final epoch = %d, want %d", epoch, workers)
	}
}
