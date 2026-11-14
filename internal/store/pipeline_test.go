package store

import (
	"context"
	"fmt"
	"testing"
)

// TestRankedCandidatesBatched covers the single-query candidates path:
// ranking, the in-issue join, the dossier EXISTS flag and the duplicate
// count must all come back correct without per-candidate lookups.
// StoryOutlets must report every member outlet in item order.
func TestRankedCandidatesBatched(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, err := db.Sources(ctx, pubID)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("Sources: %v len=%d", err, len(srcs))
	}
	srcA := srcs[0].ID // unnamed fixture source — outlet falls back to URL
	srcB, err := db.AddSource(ctx, pubID, "rss", "https://b.example/feed", "Outlet B")
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	addItem := func(sourceID int64, ext string) int64 {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title)
			 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			db.ws, sourceID, ext, "https://x.example/"+ext, "item "+ext).Scan(&id); err != nil {
			t.Fatalf("insert item %s: %v", ext, err)
		}
		return id
	}
	addStory := func(i int, score float64, itemIDs ...int64) int64 {
		sid, err := db.CreateStory(ctx, pubID, fmt.Sprintf("story %02d", i), int64(i), "")
		if err != nil {
			t.Fatalf("CreateStory: %v", err)
		}
		for _, it := range itemIDs {
			if err := db.AddStoryItem(ctx, sid, it); err != nil {
				t.Fatalf("AddStoryItem: %v", err)
			}
		}
		if err := db.SetStoryTriage(ctx, sid, score, "reason"); err != nil {
			t.Fatalf("SetStoryTriage: %v", err)
		}
		return sid
	}

	issueID, err := db.CreateIssue(ctx, pubID, 1, "s", "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	otherID, err := db.CreateIssue(ctx, pubID, 2, "s2", "")
	if err != nil {
		t.Fatalf("CreateIssue 2: %v", err)
	}

	// 25 ranked stories, descending scores → rank must track score order.
	const N = 25
	storyIDs := make([]int64, N)
	for i := 0; i < N; i++ {
		items := []int64{addItem(srcA, fmt.Sprintf("a-%d", i))}
		if i == 0 {
			// top story covered by a second outlet too
			items = append(items, addItem(srcB, "b-0"))
		}
		storyIDs[i] = addStory(i, float64(N-i), items...)
	}
	// an untriaged story never appears, and a story already placed in
	// another issue is out of the pool
	extra, err := db.CreateStory(ctx, pubID, "untriaged", 999, "")
	if err != nil {
		t.Fatalf("CreateStory untriaged: %v", err)
	}
	_ = extra
	taken := addStory(N, 0.5, addItem(srcA, "a-taken"))
	if _, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: otherID, StoryID: taken, Section: "Lead", Status: "approved",
	}); err != nil {
		t.Fatalf("CreateIssueItem other issue: %v", err)
	}

	// two stories sit in this issue; one carries a dossier
	in1, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: storyIDs[0], Section: "Lead",
		Score: 25, Reason: "r", Status: "approved", Position: 0,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem in1: %v", err)
	}
	in2, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: storyIDs[2], Section: "Lead",
		Score: 23, Reason: "r", Status: "pending", Position: 1,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem in2: %v", err)
	}
	if err := db.SaveDossier(ctx, in1, "{}"); err != nil {
		t.Fatalf("SaveDossier: %v", err)
	}

	cands, err := db.RankedCandidates(ctx, pubID, issueID)
	if err != nil {
		t.Fatalf("RankedCandidates: %v", err)
	}
	if len(cands) != N {
		t.Fatalf("candidates = %d, want %d (taken story in other issue must be excluded)", len(cands), N)
	}
	prev := cands[0].Story.TriageScore
	for i, c := range cands {
		if c.Rank != i+1 {
			t.Fatalf("rank at %d = %d", i, c.Rank)
		}
		if c.Story.TriageScore > prev {
			t.Fatal("candidates not ordered by score desc")
		}
		prev = c.Story.TriageScore
		switch c.Story.ID {
		case storyIDs[0]:
			if c.Item == nil || c.Item.ID != in1 || !c.Dossier {
				t.Fatalf("top candidate = %+v, want issue item %d with dossier", c, in1)
			}
			if c.Item.Section != "Lead" || c.Item.Status != "approved" {
				t.Fatalf("joined item fields wrong: %+v", c.Item)
			}
			if c.Duplicates != 1 {
				t.Fatalf("duplicates = %d, want 1 (two member items)", c.Duplicates)
			}
		case storyIDs[2]:
			if c.Item == nil || c.Item.ID != in2 || c.Dossier {
				t.Fatalf("pending candidate = %+v, want item %d without dossier", c, in2)
			}
		case taken:
			t.Fatal("story placed in another issue leaked into candidates")
		default:
			if c.Item != nil {
				t.Fatalf("bench story %d unexpectedly joined item %+v", c.Story.ID, c.Item)
			}
		}
	}

	ids := make([]int64, N)
	for i, sid := range storyIDs {
		ids[i] = sid
	}
	outlets, err := db.StoryOutlets(ctx, ids)
	if err != nil {
		t.Fatalf("StoryOutlets: %v", err)
	}
	if len(outlets[storyIDs[0]]) != 2 {
		t.Fatalf("top story outlets = %v, want 2 members", outlets[storyIDs[0]])
	}
	if outlets[storyIDs[0]][1].Name != "Outlet B" {
		t.Fatalf("second outlet = %+v, want named Outlet B", outlets[storyIDs[0]][1])
	}
	if got := outlets[storyIDs[1]]; len(got) != 1 || got[0].URL != srcs[0].URL {
		t.Fatalf("single-member outlets = %v, want the unnamed source URL", got)
	}
	if empty, err := db.StoryOutlets(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("StoryOutlets(nil) = %v %v", empty, err)
	}
}
