package store

import (
	"context"
	"testing"
)

// TestStoryAnswers: a Sim/Não persists on the story's stable keys, the
// latest answer wins, and only "no" answers come back as rejected —
// the set Hoje and the calibration deck filter on (KLOX-183).
func TestStoryAnswers(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	keys := []string{"u:https://example.com/a", "s:12345"}

	if err := db.SaveStoryAnswers(ctx, pubID, keys, "no", "Manchete rejeitada"); err != nil {
		t.Fatalf("save no: %v", err)
	}
	rej, err := db.RejectedStoryFeedback(ctx, pubID)
	if err != nil {
		t.Fatalf("rejected: %v", err)
	}
	if len(rej) != 2 {
		t.Fatalf("rejected keys = %d, want 2 (one per stable key)", len(rej))
	}
	for _, a := range rej {
		if a.Answer != "no" || a.Title != "Manchete rejeitada" {
			t.Fatalf("answer = %+v", a)
		}
	}

	// a later "yes" on the same keys clears the rejection
	if err := db.SaveStoryAnswers(ctx, pubID, keys, "yes", "Manchete rejeitada"); err != nil {
		t.Fatalf("save yes: %v", err)
	}
	if rej, _ := db.RejectedStoryFeedback(ctx, pubID); len(rej) != 0 {
		t.Fatalf("after yes, rejected = %v", rej)
	}

	// a "no" on another story of the same radar lists alongside
	if err := db.SaveStoryAnswers(ctx, pubID, []string{"u:https://example.com/b"}, "no", "Outra manchete"); err != nil {
		t.Fatalf("save second no: %v", err)
	}
	rej, _ = db.RejectedStoryFeedback(ctx, pubID)
	if len(rej) != 1 || rej[0].Key != "u:https://example.com/b" {
		t.Fatalf("rejected = %v", rej)
	}
}
