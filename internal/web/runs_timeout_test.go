package web_test

// Regression test for the QA-reproduced wedge: a pipeline run parked on
// a dead pooled connection must be released by the worker deadline —
// the run is marked failed and the publication can start another run.

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
	"github.com/sthefanocordeiro/kloxus/internal/web"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// wedgeFetcher parks inside Fetch until the run's context ends — the
// fetch-side equivalent of a pgx read on a socket the pooler silently
// dropped — while wedge is set, then delegates to the fixture fetcher.
type wedgeFetcher struct {
	inner sources.Fetcher
	wedge atomic.Bool
}

func (f *wedgeFetcher) Fetch(ctx context.Context, typ, url string) ([]sources.RawItem, error) {
	if f.wedge.Load() {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.inner.Fetch(ctx, typ, url)
}

// TestRunTimeoutReleasesPublication starts a collect run that wedges
// mid-flight, asserts the worker deadline fails it, then runs a second
// run on the same workspace to completion — the slot is released, not
// held forever. The second server shares the workspace manager (where
// the run slot lives) but gets a production-scale deadline, so the
// recovery run isn't bounded by the test's 1.5s wedge deadline.
func TestRunTimeoutReleasesPublication(t *testing.T) {
	ctx := context.Background()
	db := testdb.WS(t)
	seed := func(ctx context.Context, sdb *store.DB) error {
		data, err := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		if err != nil {
			return err
		}
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		_, err = sdb.SeedPublication(ctx, def)
		return err
	}
	if err := seed(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pubs, err := db.ListPublications(ctx)
	if err != nil || len(pubs) == 0 {
		t.Fatalf("ListPublications: %v", err)
	}
	pub := pubs[0]

	mgr := workspace.OpenLocal(db, seed)
	cfg := &config.Config{
		Mode: "demo", Port: 8080,
		OutDir:      t.TempDir(),
		ModelTriage: "claude-haiku-5-5", ModelDraft: "claude-sonnet-5-5", MaxDeepDives: 3,
	}
	fetcher := &wedgeFetcher{inner: sources.FixtureFetcher{FS: examples.FS}}
	fetcher.wedge.Store(true)
	deps := func(timeout time.Duration) web.Deps {
		return web.Deps{
			WS: mgr, Cfg: cfg,
			Fetcher: fetcher,
			LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm", DumpDir: filepath.Join(cfg.OutDir, "llm-dump")},
			Monitor: sources.FakeMonitor{},
			Seed:    seed,
			// RunTimeout bounds the worker — short for the wedged run,
			// production-scale for the recovery run.
			RunTimeout: timeout,
		}
	}
	runsURL := "/api/v1/publications/" + fmt.Sprint(pub.ID) + "/runs"

	srv := web.New(deps(1500 * time.Millisecond))
	rr := apiCall(t, srv, "POST", runsURL, nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("start wedged run = %d: %s", rr.Code, rr.Body.String()[:200])
	}
	run1ID := apiJSON(t, rr)["run"].(map[string]any)["id"].(string)
	run1 := waitRun(t, srv, run1ID)
	if run1["status"] != "failed" {
		t.Fatalf("wedged run status = %v, want failed (err %v)", run1["status"], run1["error"])
	}

	// The publication is unlocked: a second run gets a fresh run id and,
	// with the wedge cleared, finishes end to end.
	fetcher.wedge.Store(false)
	srv2 := web.New(deps(3 * time.Minute))
	rr = apiCall(t, srv2, "POST", runsURL, nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("start recovery run = %d: %s", rr.Code, rr.Body.String()[:200])
	}
	run2ID := apiJSON(t, rr)["run"].(map[string]any)["id"].(string)
	if run2ID == run1ID {
		t.Fatal("recovery run reuses the failed run's id — slot was not released")
	}
	if run2 := waitRun(t, srv2, run2ID); run2["status"] != "done" {
		t.Fatalf("recovery run status = %v err=%v, want done", run2["status"], run2["error"])
	}
}
