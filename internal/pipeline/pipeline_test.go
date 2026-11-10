package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func TestRunHeadlessFake(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	def := &config.Publication{
		Slug: "p",
		Name: "Pub",
		Sections: []config.Section{
			{Title: "Lead", Items: 2, Commentary: "required"},
			{Title: "Links", Items: 3, Commentary: "none"},
		},
	}
	id, err := db.SeedPublication(ctx, def)
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, "p")
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	if pub.ID != id {
		t.Fatalf("pub.ID = %d, want %d", pub.ID, id)
	}

	outDir := t.TempDir()
	path, err := RunHeadlessFake(ctx, db, pub, outDir)
	if err != nil {
		t.Fatalf("RunHeadlessFake: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	for _, want := range []string{"## Lead", "## Links", "commentary", "issue 1"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("output missing %q", want)
		}
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM runs WHERE publication_id = ?`, pub.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(Steps) {
		t.Errorf("runs recorded = %d, want %d", n, len(Steps))
	}
}
