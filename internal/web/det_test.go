package web_test

// KLOXUS_LLM_MODE=off coverage: a full deterministic run over the demo
// fixtures must complete with a provider that fails on any call, and AI
// actions must answer a clear 409 rather than a 500.
import (
	"context"
	"fmt"
	"net/http"
	"strings"
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
			t.Fatalf("decision %s on %d = %d %s", kind, iid, rr.Code, shortBody(rr))
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
		t.Fatalf("POST draft = %d %s", rr.Code, shortBody(rr))
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
	// The deterministic draft is an honest compose skeleton: headline +
	// sources, empty body slots — never source excerpts assembled into
	// prose (KLOX-48).
	for _, chunk := range strings.Split(iss.BodyMD, "### ")[1:] {
		rest := strings.SplitN(chunk, "**Sources:**", 2)[0]
		if i := strings.Index(rest, "\n"); i >= 0 {
			if prose := strings.TrimSpace(rest[i:]); prose != "" {
				t.Errorf("deterministic draft wrote prose: %.80s", prose)
			}
		}
	}
	if len(iss.Flags) != 0 {
		t.Errorf("skeleton carried %d flag(s), want none — nothing to verify", len(iss.Flags))
	}
}

// TestDetRejectionLearnsGuideRule is the KLOX-55 audit repro: under
// llm_mode=off a reasoned rejection must persist a real, versioned
// guide rule — the API response, the guide endpoint and the bench all
// reflect it — with zero provider calls (detServer fails on any).
func TestDetRejectionLearnsGuideRule(t *testing.T) {
	ctx := context.Background()
	srv, db, pub := detServer(t)
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	iis, err := db.IssueItems(ctx, issueID)
	if err != nil || len(iis) == 0 {
		t.Fatalf("IssueItems = %d, err %v", len(iis), err)
	}

	rejectedStory := iis[0].StoryID
	rr := apiCall(t, srv, "POST", issueURL+"/items/"+fmt.Sprint(iis[0].ID)+"/decisions",
		map[string]string{"kind": "reject", "reason": "não é para meu público"})
	if rr.Code != 200 {
		t.Fatalf("reject = %d %s", rr.Code, shortBody(rr))
	}
	resp := apiJSON(t, rr)
	guide, ok := resp["guide"].(map[string]any)
	if !ok {
		t.Fatalf("reject response carries no guide payload: %v", resp)
	}
	if v, _ := guide["version"].(float64); v < 1 {
		t.Fatalf("guide version = %v, want ≥1 — the rule must persist", guide["version"])
	}
	if added, _ := guide["added"].([]any); len(added) == 0 {
		t.Fatalf("guide added = %v, want the recorded rule", guide["added"])
	}

	// the guide screen/API shows what was learned — version and content
	g := apiGet(t, srv, "/api/v1/publications/"+fmt.Sprint(pub.ID)+"/guide")["guide"].(map[string]any)
	if v, _ := g["version"].(float64); v < 1 {
		t.Fatalf("guide API version = %v, want ≥1", g["version"])
	}
	if content, _ := g["content"].(string); !strings.Contains(content, "público") {
		t.Fatalf("guide content lacks the learned rule: %q", content)
	}

	// the rejected story must not resurface as a bench suggestion
	for _, c := range apiGet(t, srv, issueURL+"/candidates")["candidates"].([]any) {
		if int64(c.(map[string]any)["story_id"].(float64)) == rejectedStory {
			t.Fatalf("rejected story %d resurfaced in candidates", rejectedStory)
		}
	}

	// manual rebuild takes the same deterministic path — 200, no model
	if rr := apiCall(t, srv, "POST", "/api/v1/publications/"+fmt.Sprint(pub.ID)+"/guide/rebuild", nil); rr.Code != 200 {
		t.Fatalf("guide/rebuild = %d %s", rr.Code, shortBody(rr))
	}
}

// TestDetAfterLearnsOnlyRecordedRules: the after-sending "learned" block
// may only name rules the guide actually recorded (KLOX-55). With no
// persisted guide — e.g. rejections whose rebuild ran before the fix —
// it must report nothing rather than infer rules from raw reasons.
func TestDetAfterLearnsOnlyRecordedRules(t *testing.T) {
	ctx := context.Background()
	srv, db, pub := detServer(t)
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + fmt.Sprint(issueID)
	iis, err := db.IssueItems(ctx, issueID)
	if err != nil || len(iis) == 0 {
		t.Fatalf("IssueItems = %d, err %v", len(iis), err)
	}
	// simulate the pre-fix state: a recorded rejection reason with no
	// persisted guide (the guide table stays at version 0)
	if err := db.RecordDecision(ctx, pub.ID, iis[0].ID, "reject", "não é para meu público"); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if err := db.SetIssueStatus(ctx, issueID, "sent"); err != nil {
		t.Fatalf("SetIssueStatus: %v", err)
	}
	af := apiGet(t, srv, issueURL+"/after")
	if learned, _ := af["learned"].([]any); len(learned) != 0 {
		t.Fatalf("after.learned = %v, want empty — the guide recorded nothing", learned)
	}
}

// TestAIEndpointsBlockedOffMode: AI-only actions answer 409 with a clear
// code, never a 500.
func TestAIEndpointsBlockedOffMode(t *testing.T) {
	srv, _, pub := detServer(t)

	// no recorded rejections: even the deterministic rebuild cannot
	// fabricate a guide — a clear 409, never a 500, never the model
	rr := apiCall(t, srv, "POST", "/api/v1/publications/"+fmt.Sprint(pub.ID)+"/guide/rebuild", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("guide/rebuild = %d, want 409", rr.Code)
	}
	if code := apiJSON(t, rr)["error"].(map[string]any)["code"]; code != "conflict" {
		t.Fatalf("guide/rebuild error code = %v, want conflict", code)
	}
}
