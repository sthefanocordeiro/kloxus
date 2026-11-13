package web_test

// Deterministic mode with a production-shaped guard wired in — the
// shape modeRuntime produces under KLOXUS_LLM_MODE=off (llm.None
// provider, Postgres-backed guard kept for the admin surface). The
// detServer tests leave Deps.Guard nil, so they miss this combination:
// no-AI collect/draft must bypass AI admission and never touch the AI
// run ledger, while real AI runs stay fully guarded.
import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// detGuardServer is guardServer in KLOXUS_LLM_MODE=off with the
// fail-loud provider modeRuntime wires: llm.None errors on any model
// call, so a passing run proves the path made none.
func detGuardServer(t *testing.T, opt func(*web.Deps)) (*web.Server, *store.DB, *store.Publication, *llm.Guard, *store.Pool) {
	t.Helper()
	return guardServer(t, func(d *web.Deps) {
		d.Cfg.LLMMode = "off"
		d.LLM = llm.None{}
		if opt != nil {
			opt(d)
		}
	})
}

// detApproveAll approves every pending item on the issue — the
// deterministic lineup leaves decisions to the creator, same as
// TestDeterministicModeJourney.
func detApproveAll(t *testing.T, srv *web.Server, db *store.DB, issueID int64) {
	t.Helper()
	ctx := context.Background()
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	for pass := 0; pass < 2; pass++ {
		iis, err := db.IssueItems(ctx, issueID)
		if err != nil || len(iis) == 0 {
			t.Fatalf("IssueItems = %d, err %v", len(iis), err)
		}
		for _, ii := range iis {
			if ii.Status != "pending" {
				continue
			}
			rr := apiCall(t, srv, "POST", issueURL+"/items/"+fmt.Sprint(ii.ID)+"/decisions",
				map[string]string{"kind": "approve"})
			if rr.Code != 200 {
				t.Fatalf("decision on %d = %d %s", ii.ID, rr.Code, rr.Body.String()[:300])
			}
		}
	}
}

// TestDetGuardJourneyKillOn: with the persisted kill switch ON and a
// real spend guard wired in, the full deterministic journey — collect,
// decide, draft — completes. The kill switch pauses model calls; a
// deterministic run makes none.
func TestDetGuardJourneyKillOn(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, g, _ := detGuardServer(t, nil)
	if err := g.SetKill(ctx, true); err != nil {
		t.Fatalf("SetKill: %v", err)
	}
	// app_settings is shared — leave the switch off for other tests.
	t.Cleanup(func() { _ = g.SetKill(context.Background(), false) })

	issueID := runPhaseA(t, srv, db, pub.ID)
	detApproveAll(t, srv, db, issueID)

	rr := apiCall(t, srv, "POST", "/api/v1/issues/"+fmt.Sprint(issueID)+"/draft", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST draft = %d %s", rr.Code, rr.Body.String()[:300])
	}
	run := waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))
	if run["status"] != "done" {
		t.Fatalf("draft run under kill+off = %v (%v)", run["status"], run["error"])
	}
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "drafted" {
		t.Fatalf("issue status = %q, want drafted", iss.Status)
	}
}

// TestDetGuardBeyondAllowanceNoCharge: deterministic runs past the AI
// run allowance are neither refused nor charged — llm_ledger records
// real run starts only.
func TestDetGuardBeyondAllowanceNoCharge(t *testing.T) {
	ctx := context.Background()
	srv, _, pub, g, pool := detGuardServer(t, nil)
	g.RunsPerDay = 1 // deterministic runs must neither hit nor consume it

	today := time.Now().UTC().Format("2006-01-02") + " 00:00:00"
	before, err := pool.LLMRunsSince(ctx, "local", today)
	if err != nil {
		t.Fatalf("LLMRunsSince: %v", err)
	}
	for i := 0; i < 3; i++ {
		if run := postRun(t, srv, pub.ID); run["status"] != "done" {
			t.Fatalf("deterministic run %d = %v (%v)", i+1, run["status"], run["error"])
		}
	}
	after, err := pool.LLMRunsSince(ctx, "local", today)
	if err != nil {
		t.Fatalf("LLMRunsSince: %v", err)
	}
	if after != before {
		t.Fatalf("deterministic runs charged the AI run ledger: %d -> %d", before, after)
	}
}

// TestDetGuardAIPathStillBlocked: same persisted kill switch, same
// guard — a deterministic run proceeds while an AI-mode run is refused.
// The bypass is scoped to no-AI work, not the guard.
func TestDetGuardAIPathStillBlocked(t *testing.T) {
	ctx := context.Background()
	srv, _, pub, g, _ := detGuardServer(t, nil)
	aiSrv, _, aiPub, _ := demoServerOpt(t, func(d *web.Deps) { d.Guard = g })

	if err := g.SetKill(ctx, true); err != nil {
		t.Fatalf("SetKill: %v", err)
	}
	t.Cleanup(func() { _ = g.SetKill(context.Background(), false) })

	if run := postRun(t, srv, pub.ID); run["status"] != "done" {
		t.Fatalf("deterministic run under kill = %v (%v)", run["status"], run["error"])
	}
	run := postRun(t, aiSrv, aiPub.ID)
	if run["status"] != "blocked" || run["blocked_reason"] != string(llm.ReasonKillSwitch) {
		t.Fatalf("AI run under kill = %v, want kill_switch block", run)
	}
}
