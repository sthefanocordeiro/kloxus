package web_test

// KLOXUS_LLM_MODE=off coverage: a full deterministic run over the demo
// fixtures must complete with a provider that fails on any call, and AI
// actions must answer a clear 409 rather than a 500.
import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// detServer builds the demo server in KLOXUS_LLM_MODE=off with a
// provider that records — and fails — any attempted model call.
func detServer(t *testing.T) (*web.Server, *store.DB, *store.Publication) {
	t.Helper()
	calls := 0
	srv, db, pub, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Cfg.LLMMode = "off"
		d.LLM = llm.Func(func(context.Context, llm.Request) (llm.Response, error) {
			calls++
			return llm.Response{}, llm.ErrDisabled
		})
	})
	t.Cleanup(func() {
		if calls != 0 {
			t.Errorf("model provider called %d time(s) in deterministic mode", calls)
		}
	})
	return srv, db, pub
}

func TestDeterministicModeJourney(t *testing.T) {
	ctx := context.Background()
	srv, db, pub := detServer(t)

	if ws := apiGet(t, srv, "/api/v1/workspace"); ws["llm_mode"] != "off" {
		t.Fatalf("workspace llm_mode = %v, want off", ws["llm_mode"])
	}

	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)

	cands := apiGet(t, srv, issueURL+"/candidates")
	if cands["llm_mode"] != "off" {
		t.Fatalf("candidates llm_mode = %v, want off", cands["llm_mode"])
	}

	// deterministic lineup picked every capacity slot
	iis, err := db.IssueItems(ctx, issueID)
	if err != nil || len(iis) == 0 {
		t.Fatalf("IssueItems = %d, err %v", len(iis), err)
	}
	decide := func(iid int64, kind string) {
		rr := apiCall(t, srv, "POST", issueURL+"/items/"+fmt.Sprint(iid)+"/decisions",
			map[string]string{"kind": kind})
		if rr.Code != 200 {
			t.Fatalf("decision %s on %d = %d %s", kind, iid, rr.Code, rr.Body.String()[:300])
		}
	}
	decide(iis[0].ID, "alternative") // deterministic substitution
	for _, ii := range iis[1:] {
		decide(ii.ID, "approve")
	}
	iis2, _ := db.IssueItems(ctx, issueID)
	for _, ii := range iis2 {
		if ii.Status == "pending" {
			decide(ii.ID, "approve")
		}
	}

	rr := apiCall(t, srv, "POST", issueURL+"/draft", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST draft = %d %s", rr.Code, rr.Body.String()[:300])
	}
	run := waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))
	if run["status"] != "done" {
		t.Fatalf("draft run failed: %v", run["error"])
	}
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "drafted" {
		t.Fatalf("issue status = %q, want drafted", iss.Status)
	}
}

// TestAIEndpointsBlockedOffMode: AI-only actions answer 409 with a clear
// code, never a 500.
func TestAIEndpointsBlockedOffMode(t *testing.T) {
	srv, _, pub := detServer(t)

	rr := apiCall(t, srv, "POST", "/api/v1/publications/"+fmt.Sprint(pub.ID)+"/guide/rebuild", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("guide/rebuild = %d, want 409", rr.Code)
	}
	if code := apiJSON(t, rr)["error"].(map[string]any)["code"]; code != "ai_disabled" {
		t.Fatalf("guide/rebuild error code = %v, want ai_disabled", code)
	}
}
