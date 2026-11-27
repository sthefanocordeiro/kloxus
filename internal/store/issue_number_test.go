package store

import (
	"context"
	"strconv"
	"sync"
	"testing"
)

// TestCreateNextIssueConcurrent proves issue numbering is race-safe: every
// concurrent creator must receive a distinct, gapless number for the same
// publication. The database UNIQUE(publication_id, number) constraint is the
// backstop; the publication row lock is what makes the allocation itself
// serialize instead of colliding and failing.
func TestCreateNextIssueConcurrent(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}

	const creators = 8
	type result struct {
		id      int64
		number  int
		subject string
		err     error
	}
	results := make(chan result, creators)
	var wg sync.WaitGroup
	for i := 0; i < creators; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, n, err := db.CreateNextIssue(ctx, pubID,
				func(n int) string { return "issue " + strconv.Itoa(n) }, "", nil)
			subject := ""
			if err == nil {
				issue, getErr := db.GetIssue(ctx, id)
				if getErr != nil {
					err = getErr
				} else {
					subject = issue.Subject
				}
			}
			results <- result{id: id, number: n, subject: subject, err: err}
		}()
	}
	wg.Wait()
	close(results)

	ids := map[int64]bool{}
	numbers := map[int]bool{}
	count := 0
	for r := range results {
		if r.err != nil {
			t.Errorf("CreateNextIssue: %v", r.err)
			continue
		}
		count++
		ids[r.id] = true
		numbers[r.number] = true
		if want := "issue " + strconv.Itoa(r.number); r.subject != want {
			t.Errorf("subject = %q, want %q", r.subject, want)
		}
	}
	if count != creators {
		t.Fatalf("%d/%d creations succeeded", count, creators)
	}
	if len(ids) != creators {
		t.Fatalf("got %d distinct issue ids, want %d", len(ids), creators)
	}
	if len(numbers) != creators {
		t.Fatalf("got %d distinct issue numbers, want %d", len(numbers), creators)
	}
	for n := 1; n <= creators; n++ {
		if !numbers[n] {
			t.Fatalf("missing issue number %d in %#v", n, numbers)
		}
	}
}
