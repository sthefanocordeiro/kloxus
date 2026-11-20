package editorial

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

func setup(t *testing.T) (*store.DB, *store.Publication) {
	t.Helper()
	db := testdb.WS(t)
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

// TestRebuildDeterministicNoProvider is the llm_mode=off contract
// (KLOX-55): a nil provider is never invoked — Rebuild condenses the
// recorded reasons locally and persists a real, versioned guide.
func TestRebuildDeterministicNoProvider(t *testing.T) {
	db, pub := setup(t)
	content, version, err := Rebuild(context.Background(), db, pub, nil, "m")
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if version != 1 || content != "- Fora da minha linha editorial" {
		t.Fatalf("deterministic guide content=%q version=%d", content, version)
	}
}

// TestBuildGuideDistinctReasonsSharedContext is the KLOX-100
// regression: rejections from stories sharing kind/outlet produce the
// same rejection-context suffix ("tipo: article · veículo: X"). The
// context must ride alongside the learned rule, not feed the
// near-duplicate check — otherwise every objection collapses into one
// rule and the guide loses the recorded reasons.
func TestBuildGuideDistinctReasonsSharedContext(t *testing.T) {
	reasons := []string{
		"off the publication's angle",
		"too thin for this issue",
		"duplicates a stronger item",
		"prioritize policy with direct Brazil impact",
	}
	ctxs := []string{
		"tipo: article · veículo: Outlet A",
		"tipo: article · veículo: Outlet B",
		"tipo: article · veículo: Outlet A",
		"tipo: article · veículo: Outlet C",
	}
	got := buildGuide(reasons, ctxs)
	for _, want := range []string{
		"Off the publication's angle",
		"Too thin for this issue",
		"Duplicates a stronger item",
		"Prioritize policy with direct Brazil impact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("guide missing %q:\n%s", want, got)
		}
	}
	for _, want := range []string{"Outlet A", "Outlet B", "Outlet C"} {
		if !strings.Contains(got, want) {
			t.Fatalf("guide missing context %q:\n%s", want, got)
		}
	}
}

// TestBuildGuideMergesSameObjection keeps the dedupe contract on the
// objection itself: same reason on different stories still collapses
// to one rule, which then carries each rejection's context.
func TestBuildGuideMergesSameObjection(t *testing.T) {
	got := buildGuide(
		[]string{"not for my audience", "not for my audience"},
		[]string{"tipo: article · veículo: Outlet A", "tipo: video · veículo: Outlet B"},
	)
	if n := strings.Count(got, "\n"); n != 0 {
		t.Fatalf("expected a single merged rule, got:\n%s", got)
	}
	if !strings.Contains(got, "Skip stories that don't serve this newsletter's audience") {
		t.Fatalf("merged rule missing: %s", got)
	}
	if !strings.Contains(got, "Outlet A") || !strings.Contains(got, "Outlet B") {
		t.Fatalf("merged rule should keep both contexts: %s", got)
	}
}
