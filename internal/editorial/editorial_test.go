package editorial

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func setup(t *testing.T) (*store.DB, *store.Publication) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	def := &config.Publication{
		Slug: "p", Name: "Pub", EditorialLine: "regra editorial",
		Sections: []config.Section{{Title: "S", Items: 1, Commentary: "none"}},
	}
	db.SeedPublication(ctx, def)
	pub, _ := db.GetPublication(ctx, "p")
	db.RecordDecision(ctx, pub.ID, 0, "reject", "fora da minha linha editorial")
	return db, pub
}

func TestRebuildWithModel(t *testing.T) {
	db, pub := setup(t)
	provider := llm.Func(func(_ context.Context, req llm.Request) (llm.Response, error) {
		if req.Template != "guide" {
			t.Fatalf("unexpected template %q", req.Template)
		}
		if !strings.Contains(req.User, "fora da minha linha editorial") {
			t.Fatal("prompt should include the decision reason")
		}
		return llm.Response{Text: "- Priorizar histórias com fonte primária\n- Evitar pauta só de lançamento\n"}, nil
	})
	content, version, err := Rebuild(context.Background(), db, pub, provider, "m")
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if version != 1 || !strings.Contains(content, "fonte primária") {
		t.Fatalf("content=%q version=%d", content, version)
	}
}

func TestRebuildFallbackOnMissingFixture(t *testing.T) {
	db, pub := setup(t)
	provider := llm.Replay{FS: fstest.MapFS{}, Dir: "llm"}
	content, version, err := Rebuild(context.Background(), db, pub, provider, "m")
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if version != 1 || content != "- Fora da minha linha editorial" {
		t.Fatalf("fallback content=%q version=%d", content, version)
	}
}
