package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
	"github.com/sthefanocordeiro/kloxus/internal/web"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// TestFixturesAreFictional denies real institutions, companies and people
// from appearing anywhere in the demo fixtures.
func TestFixturesAreFictional(t *testing.T) {
	denied := []string{
		"Banco Central do Brasil", "Banco Central", "Pix", "Cade", "Anatel", "STF",
		"Google", "Apple", "Meta", "Microsoft", "OpenAI", "Anthropic", "Amazon",
		"Reuters", "Folha", "Estadão", "Globo", "FGV", "USP", "Unicamp",
		"São Paulo", "Brasil", "brasileir", "Lula", "Bolsonaro",
		"R$", "pelo câmara",
		"radartech.com", "pulsodigital.com", "correiodocodigo.com", "observatorioaberto.org",
	}
	var files []string
	for _, dir := range []string{"fixtures/feeds", "fixtures/llm", "fixtures/pages", "publications", "landing"} {
		entries, err := examples.FS.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir %s: %v", dir, err)
		}
		for _, e := range entries {
			files = append(files, dir+"/"+e.Name())
		}
	}
	for _, f := range files {
		data, err := examples.FS.ReadFile(f)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", f, err)
		}
		for _, word := range denied {
			if strings.Contains(string(data), word) {
				t.Errorf("fixture %s contains real-entity reference %q", f, word)
			}
		}
	}
}

// TestExportAndSendGatedBeforeApproval verifies drafts cannot be exported
// or sent before approval.
func TestExportAndSendGatedBeforeApproval(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10)

	if rr := apiCall(t, srv, "GET", issueURL+"/export?fmt=markdown", nil); rr.Code != http.StatusConflict {
		t.Fatalf("export before approval = %d, want 409", rr.Code)
	}
	if rr := apiCall(t, srv, "POST", issueURL+"/send", map[string]string{"audience": "readers"}); rr.Code != http.StatusConflict {
		t.Fatalf("send before approval = %d, want 409", rr.Code)
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if iss.Status == "approved" || iss.Status == "sent" {
		t.Fatalf("issue status = %q, should not be approved/sent", iss.Status)
	}
}

// TestRejectRequiresReason verifies rejection validation: a reason-less
// reject is refused and the item stays pending.
func TestRejectRequiresReason(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	issueID := runPhaseA(t, srv, db, pub.ID)
	iis, _ := db.IssueItems(ctx, issueID)
	issueURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10)

	rr := apiCall(t, srv, "POST", issueURL+"/items/"+strconv.FormatInt(iis[0].ID, 10)+"/decisions",
		map[string]string{"kind": "reject"})
	if c := apiErrCode(t, rr); c != "reason_required" {
		t.Fatalf("reject without reason: code = %q, want reason_required", c)
	}
	ii, _ := db.GetIssueItem(ctx, iis[0].ID)
	if ii.Status != "pending" {
		t.Fatalf("reject without reason changed status to %q", ii.Status)
	}
}

// runPhaseA triggers a run and waits for it to report done.
func runPhaseA(t *testing.T, srv *web.Server, db *store.DB, pubID int64) int64 {
	t.Helper()
	rr := apiCall(t, srv, "POST", "/api/v1/publications/"+strconv.FormatInt(pubID, 10)+"/runs", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST run = %d", rr.Code)
	}
	runID := apiJSON(t, rr)["run"].(map[string]any)["id"].(string)
	run := waitRun(t, srv, runID)
	if run["status"] != "done" {
		t.Fatalf("run failed: %v", run["error"])
	}
	// When done, the run reports the last step of the list so the run
	// screen marks every step (incl. draft/citecheck) complete.
	if run["step"] != pipeline.Steps[len(pipeline.Steps)-1] {
		t.Fatalf("done run reports step %q, want %q", run["step"], pipeline.Steps[len(pipeline.Steps)-1])
	}
	return int64(run["issue_id"].(float64))
}

// TestWorkspaceIsolation: two visitors get separate databases; resetting
// one does not affect the other.
func TestWorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	seed := func(ctx context.Context, sdb *store.DB) error {
		data, _ := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		_, err = sdb.SeedPublication(ctx, def)
		return err
	}
	mgr, err := workspace.OpenHosted(ctx, testdb.Pool(t), []byte("test-secret-key-32-bytes-padded-ok!"), seed)
	if err != nil {
		t.Fatalf("OpenHosted: %v", err)
	}
	resolve := func(cookie string) (string, *workspace.Workspace) {
		req := httptest.NewRequest("GET", "/", nil)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		rr := httptest.NewRecorder()
		ws, err := mgr.Resolve(rr, req)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		set := rr.Header().Get("Set-Cookie")
		if cookie == "" && set == "" {
			t.Fatal("expected a new workspace cookie")
		}
		if set != "" {
			cookie = strings.SplitN(set, ";", 2)[0]
		}
		return cookie, ws
	}
	c1, ws1 := resolve("")
	c2, ws2 := resolve("")
	t.Cleanup(func() {
		mgr.CloseWorkspace(ws1.ID)
		mgr.CloseWorkspace(ws2.ID)
	})
	if ws1.ID == ws2.ID {
		t.Fatal("two visitors share a workspace")
	}
	// hosted workspaces start empty — onboarding creates the publication;
	// seed both explicitly so the isolation assertions below stay identical
	if err := seed(ctx, ws1.DB); err != nil {
		t.Fatalf("seed ws1: %v", err)
	}
	if err := seed(ctx, ws2.DB); err != nil {
		t.Fatalf("seed ws2: %v", err)
	}
	// marker write in ws1 only
	if err := ws1.DB.SetProgress(ctx, "marker"); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	if ws1.DB.Progress(ctx, "marker") == "" || ws2.DB.Progress(ctx, "marker") != "" {
		t.Fatal("workspace isolation broken")
	}
	if err := mgr.Reset(ctx, ws1); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if ws1.DB.Progress(ctx, "marker") != "" {
		t.Fatal("reset did not clear ws1 progress")
	}
	// ws2 was reseeded but ws1's marker never leaked into it
	if ws2.DB.Progress(ctx, "marker") != "" {
		t.Fatal("ws2 unexpectedly has marker")
	}
	if _, err := ws2.DB.GetPublication(ctx, "futuro-proximo"); err != nil {
		// ws2 keeps its seeded publication after ws1's reset
		t.Fatalf("ws2 lost its seed data: %v", err)
	}
	// cookies still resolve back to their own workspaces
	_, ws1b := resolve(c1)
	_, ws2b := resolve(c2)
	if ws1b.ID != ws1.ID || ws2b.ID != ws2.ID {
		t.Fatal("cookies did not resolve back to the same workspaces")
	}
}

// TestFlagEditRecheckSentReadOnlyAndEmail covers: flag edits re-run the
// citation check (unchanged/unsupported stays flagged, supported edit
// resolves), sent issues are read-only, issue emails gate paid sections
// and carry an unsubscribe link, and .eml downloads have RFC headers.
func TestFlagEditRecheckSentReadOnlyAndEmail(t *testing.T) {
	ctx := context.Background()
	srv, db, pub, _ := demoServer(t)
	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10)
	iis, _ := db.IssueItems(ctx, issueID)

	decide := func(iid int64, kind, reason string) *httptest.ResponseRecorder {
		body := map[string]string{"kind": kind}
		if reason != "" {
			body["reason"] = reason
		}
		return apiCall(t, srv, "POST",
			issueURL+"/items/"+strconv.FormatInt(iid, 10)+"/decisions", body)
	}
	// reject one, approve the rest — exercises undo as well
	decide(iis[1].ID, "reject", "off the line")
	// undo the rejection, then reject again for real
	decide(iis[1].ID, "undo", "")
	if ii, _ := db.GetIssueItem(ctx, iis[1].ID); ii.Status != "pending" {
		t.Fatalf("undo rejection: status = %q, want pending", ii.Status)
	}
	decide(iis[1].ID, "reject", "off the line")
	for _, ii := range iis {
		if ii.ID != iis[1].ID {
			decide(ii.ID, "approve", "")
		}
	}
	for i := 0; i < 2; i++ {
		rr := apiCall(t, srv, "POST", issueURL+"/draft", nil)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("draft #%d = %d", i, rr.Code)
		}
		waitRun(t, srv, apiJSON(t, rr)["run"].(map[string]any)["id"].(string))
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if len(iss.Flags) == 0 {
		t.Fatal("expected citation flags")
	}
	// the first flagged sentence's document id
	doc := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
	var flaggedID, flaggedText string
	var flaggedCits []any
outer0:
	for _, s := range doc["sections"].([]any) {
		for _, i := range s.(map[string]any)["items"].([]any) {
			for _, sn := range i.(map[string]any)["sentences"].([]any) {
				if sn.(map[string]any)["flag"] != nil {
					flaggedID = sn.(map[string]any)["id"].(string)
					flaggedText = sn.(map[string]any)["text"].(string)
					flaggedCits = sn.(map[string]any)["citations"].([]any)
					break outer0
				}
			}
		}
	}
	if flaggedID == "" {
		t.Fatal("no flagged sentence in the document")
	}
	ver := doc["version"].(string)
	sents := issueURL + "/sentences/"
	// unchanged edit keeps the flag
	if rr := apiCall(t, srv, "PATCH", sents+flaggedID,
		map[string]any{"text": flaggedText, "citations": flaggedCits, "version": ver}); apiErrCode(t, rr) != "unchanged" {
		t.Fatal("unchanged edit should be refused")
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if len(iss.Flags) == 0 {
		t.Fatal("unchanged edit cleared the flag")
	}
	// supported edit resolves it (grounded in a dossier fact for [1])
	if rr := apiCall(t, srv, "PATCH", sents+flaggedID, map[string]any{
		"text":      "Comentários podem ser enviados até 15 de dezembro pelo portal do órgão",
		"citations": []int{1}, "version": ver}); rr.Code != 200 {
		t.Fatalf("supported edit = %d: %s", rr.Code, rr.Body.String()[:300])
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if len(iss.Flags) != 0 {
		t.Fatalf("supported edit left %d flag(s) — want the sentence replaced", len(iss.Flags))
	}
	// resolve any remaining flags by removal
	for len(iss.Flags) > 0 {
		doc = apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)
		next := ""
	outer:
		for _, s := range doc["sections"].([]any) {
			for _, i := range s.(map[string]any)["items"].([]any) {
				for _, sn := range i.(map[string]any)["sentences"].([]any) {
					if sn.(map[string]any)["flag"] != nil {
						next = sn.(map[string]any)["id"].(string)
						break outer
					}
				}
			}
		}
		if next == "" {
			t.Fatalf("flags remain (%d) but no flagged sentence found", len(iss.Flags))
		}
		if rr := apiCall(t, srv, "DELETE", sents+next,
			map[string]any{"version": doc["version"].(string)}); rr.Code != 200 {
			t.Fatalf("remove flag %s = %d", next, rr.Code)
		}
		iss, _ = db.GetIssue(ctx, issueID)
	}
	commentary := func(text string) *httptest.ResponseRecorder {
		ver := apiGet(t, srv, issueURL+"/document")["document"].(map[string]any)["version"].(string)
		return apiCall(t, srv, "PUT", issueURL+"/commentary",
			map[string]string{"section": "The big one", "text": text, "version": ver})
	}
	if rr := commentary("Minha análise."); rr.Code != 200 {
		t.Fatalf("commentary = %d", rr.Code)
	}
	if rr := apiCall(t, srv, "POST", issueURL+"/approve", nil); rr.Code != 200 {
		t.Fatalf("approve = %d %s", rr.Code, rr.Body.String()[:300])
	}
	// editing an approved issue reverts it to drafted
	if rr := commentary("Minha análise v2."); rr.Code != 200 {
		t.Fatalf("commentary after approve = %d", rr.Code)
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "drafted" {
		t.Fatalf("edit after approve: status = %q, want drafted", iss.Status)
	}
	apiCall(t, srv, "POST", issueURL+"/approve", nil)
	// send — demo reader is not subscribed → teaser + unsubscribe
	if rr := apiCall(t, srv, "POST", issueURL+"/send", map[string]string{"audience": "readers"}); rr.Code != 200 {
		t.Fatalf("send = %d %s", rr.Code, rr.Body.String()[:300])
	}
	emails, _ := db.OutboxEmails(ctx)
	var issueMail *store.OutboxEmail
	for i, e := range emails {
		if e.Kind == "issue" {
			issueMail = &emails[i]
		}
	}
	if issueMail == nil {
		t.Fatal("no issue email in outbox")
	}
	full, err := db.GetOutboxEmail(ctx, issueMail.ID)
	if err != nil {
		t.Fatalf("GetOutboxEmail: %v", err)
	}
	if !strings.Contains(full.BodyMD, "Exclusivo para assinantes") {
		t.Error("issue email leaked the paid section to non-subscribers")
	}
	if !strings.Contains(full.BodyMD, "cancelar assinatura") {
		t.Error("issue email lacks an unsubscribe link")
	}
	// the unsubscribe link opens the reader-facing React page, whose
	// /api/v1/unsub call performs the signed one-click opt-out
	m := regexp.MustCompile(`/unsub\?[^\s)]+`).FindString(full.BodyMD)
	if m == "" {
		t.Fatal("issue email has no unsubscribe URL")
	}
	rid, _ := db.UpsertReader(ctx, pub.ID, "demo@reader.test", "Demo Reader")
	apiPath := "/api/v1/unsub?" + strings.SplitN(m, "?", 2)[1]
	// a forged token is refused
	bad := regexp.MustCompile(`t=[^\s)]+`).ReplaceAllString(apiPath, "t=forged")
	if rr := apiCall(t, srv, "GET", bad, nil); rr.Code != http.StatusForbidden {
		t.Errorf("forged unsubscribe token = %d, want 403", rr.Code)
	}
	rr := apiCall(t, srv, "GET", apiPath, nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"unsubscribed":true`) {
		t.Fatalf("unsubscribe = %d / %q", rr.Code, rr.Body.String()[:120])
	}
	if !db.ReaderUnsubscribed(ctx, rid) {
		t.Error("unsubscribe not recorded on the reader")
	}
	// sent issue is read-only
	if rr := apiCall(t, srv, "PUT", issueURL+"/commentary",
		map[string]string{"section": "The big one", "text": "v3"}); apiErrCode(t, rr) != "read_only" {
		t.Fatal("commentary on sent issue should be refused")
	}
	// .eml has standard headers + encoded subject
	raw := apiCall(t, srv, "GET", fmt.Sprintf("/outbox/%d/eml", issueMail.ID), nil)
	for _, h := range []string{"From:", "MIME-Version: 1.0", "Date: ", "=?utf-8?", "=?UTF-8?"} {
		if !strings.Contains(raw.Body.String(), h) && h != "=?UTF-8?" {
			t.Errorf(".eml missing %q", h)
		}
	}
}

// TestGuideFromRejectionsOnly: the editorial guide is built from rejection
// reasons only — approvals never become rules.
func TestGuideFromRejectionsOnly(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	rebuildURL := "/api/v1/publications/" + strconv.FormatInt(pub.ID, 10) + "/guide/rebuild"

	// no decisions yet → rebuild refuses instead of writing an empty guide
	if rr := apiCall(t, srv, "POST", rebuildURL, nil); rr.Code != http.StatusConflict {
		t.Fatalf("rebuild with no decisions = %d, want 409", rr.Code)
	}

	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10)
	iis, _ := db.IssueItems(ctx, issueID)
	decide := func(iid int64, kind, reason string) {
		body := map[string]string{"kind": kind}
		if reason != "" {
			body["reason"] = reason
		}
		apiCall(t, srv, "POST", issueURL+"/items/"+strconv.FormatInt(iid, 10)+"/decisions", body)
	}
	// an approve decision must not feed the guide
	decide(iis[0].ID, "approve", "")
	decide(iis[1].ID, "reject", "fora da minha linha editorial.")

	if rr := apiCall(t, srv, "POST", rebuildURL, nil); rr.Code != 200 {
		t.Fatalf("rebuild = %d", rr.Code)
	}
	_, content, err := db.LatestGuide(ctx, pub.ID)
	if err != nil {
		t.Fatalf("LatestGuide: %v", err)
	}
	if !strings.Contains(content, "- Fora da minha linha editorial") {
		t.Errorf("guide missing condensed rejection rule:\n%s", content)
	}
	if strings.Contains(strings.ToLower(content), "approv") {
		t.Errorf("guide contains approval-derived rule:\n%s", content)
	}
	// rebuild again → reason deduplicated, not appended twice
	apiCall(t, srv, "POST", rebuildURL, nil)
	_, again, _ := db.LatestGuide(ctx, pub.ID)
	if strings.Count(again, "linha editorial") > 1 {
		t.Errorf("rebuild duplicated the rule:\n%s", again)
	}

	// three wordings of the same objection merge into a single rule
	decide(iis[2].ID, "reject", "ainda em comissão")
	decide(iis[3].ID, "reject", "matéria ainda em comissão")
	decide(iis[4].ID, "reject", "ainda em comissão, aguardando plenário")
	// loosely-worded variants of the same theme merge too
	decide(iis[5].ID, "reject", "Projeto ainda em comissão, sem efeito prático para o leitor agora")
	decide(iis[6].ID, "reject", "Ainda em comissão; nada muda para o leitor por enquanto")
	apiCall(t, srv, "POST", rebuildURL, nil)
	_, content, _ = db.LatestGuide(ctx, pub.ID)
	if n := strings.Count(content, "comissão"); n != 1 {
		t.Errorf("near-duplicate reasons not merged — %d comissão lines:\n%s", n, content)
	}
	for _, verbatim := range []string{"efeito prático", "por enquanto", "aguardando plenário"} {
		if strings.Contains(content, verbatim) {
			t.Errorf("verbatim reason fragment kept instead of the general rule:\n%s", content)
		}
	}

	// undoing the "linha editorial" rejection removes its rule
	decide(iis[1].ID, "undo", "")
	apiCall(t, srv, "POST", rebuildURL, nil)
	_, content, _ = db.LatestGuide(ctx, pub.ID)
	if strings.Contains(content, "linha editorial") {
		t.Errorf("undone rejection still in guide:\n%s", content)
	}
	if !strings.Contains(content, "comissão") {
		t.Errorf("unrelated rule lost on undo:\n%s", content)
	}

	// rebuilding with no changes must not mint a new version
	ver, _, _ := db.LatestGuide(ctx, pub.ID)
	apiCall(t, srv, "POST", rebuildURL, nil)
	ver2, _, _ := db.LatestGuide(ctx, pub.ID)
	if ver2 != ver {
		t.Errorf("no-change rebuild minted version %d (was %d)", ver2, ver)
	}
}

// TestMagicLinkCarriesWorkspace: the magic link embeds a sealed workspace
// token so it opens the issue in any browser — not just the session that
// minted it. The link targets the reader page, which calls /api/v1/p.
func TestMagicLinkCarriesWorkspace(t *testing.T) {
	ctx := context.Background()
	seed := func(ctx context.Context, sdb *store.DB) error {
		data, _ := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		_, err = sdb.SeedPublication(ctx, def)
		return err
	}
	mgr, err := workspace.OpenHosted(ctx, testdb.Pool(t),
		[]byte("test-secret-key-32-bytes-padded-ok!"), seed)
	if err != nil {
		t.Fatalf("OpenHosted: %v", err)
	}
	cfg := &config.Config{Mode: "demo", ModelTriage: "claude-haiku-5-5", ModelDraft: "claude-sonnet-5-5"}
	srv := web.New(web.Deps{
		WS: mgr, Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm"},
		Monitor: sources.FakeMonitor{},
		Seed:    seed,
	})

	serve := func(method, path, cookie string, body any) *httptest.ResponseRecorder {
		var reader *strings.Reader
		if body == nil {
			reader = strings.NewReader("")
		} else {
			b, _ := json.Marshal(body)
			reader = strings.NewReader(string(b))
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr
	}

	// visitor A: gets a workspace cookie, a finished issue, a magic link
	rrA := serve("GET", "/", "", nil)
	cookieA := strings.SplitN(rrA.Header().Get("Set-Cookie"), ";", 2)[0]
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Cookie", cookieA)
	wsA, err := mgr.Resolve(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("resolve wsA: %v", err)
	}
	t.Cleanup(mgr.Close)

	// hosted workspaces start empty — onboarding creates the publication
	if err := seed(ctx, wsA.DB); err != nil {
		t.Fatalf("seed wsA: %v", err)
	}
	pubs, _ := wsA.DB.ListPublications(ctx)
	pub := pubs[0]
	body := "# Edição 1\n\n## The big one\n\n### Manchete\n\nFrase de abertura curta. Conteúdo completo para assinantes [1]."
	iid, _ := wsA.DB.CreateIssue(ctx, pub.ID, 1, "Edição 1", body)
	if err := wsA.DB.UpdateIssue(ctx, iid, "sent", body, "[]"); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	pubAPI := "/api/v1/p/" + pub.Slug + "/1"

	// before any subscription: cross-workspace access resolves wsA's
	// issue but a missing or bad key keeps it gated — no paid body
	rr := serve("GET", pubAPI+"?ws="+wsA.ID, "", nil)
	if strings.Contains(rr.Body.String(), "Conteúdo completo") {
		t.Error("ws param without a key opened the paid body")
	}
	rr = serve("GET", pubAPI+"?key=deadbeef&ws="+wsA.ID, "", nil)
	if strings.Contains(rr.Body.String(), "Conteúdo completo") {
		t.Error("ws param with a bad key opened the paid body")
	}

	// simulate the payment: the demo reader gets a key+tok magic link
	rr = serve("POST", "/api/v1/publications/"+strconv.FormatInt(pub.ID, 10)+"/paywall/simulate", cookieA, nil)
	var link string
	if v := apiJSON(t, rr); rr.Code == 200 {
		link, _ = v["magic_link"].(string)
	}
	if link == "" {
		t.Fatalf("paywall simulate returned no magic link (%d)", rr.Code)
	}
	// the magic link targets the reader page; the page calls the API
	apiLink := strings.Replace(link, "/p/", "/api/v1/p/", 1)

	// visitor B (no cookie): the tok parameter lands them in wsA's issue
	rr = serve("GET", apiLink, "", nil)
	if !strings.Contains(rr.Body.String(), "Conteúdo completo") {
		t.Fatalf("magic link cross-browser: got %d / %q", rr.Code, rr.Body.String()[:200])
	}
	// sanity: the same link minus tok opens the visitor's own (empty)
	// workspace — the sent content must not render
	rr = serve("GET", strings.SplitN(apiLink, "&tok=", 2)[0], "", nil)
	if strings.Contains(rr.Body.String(), "Conteúdo completo") {
		t.Error("link without tok leaked content into another workspace")
	}

	// SECURITY: the reader link must never hijack the visitor's editor
	// session — opening it from another browser leaves that browser's
	// workspace cookie (and every editor route) untouched.
	cookieB := strings.SplitN(serve("GET", "/", "", nil).Header().Get("Set-Cookie"), ";", 2)[0]
	rr = serve("GET", apiLink, cookieB, nil)
	if !strings.Contains(rr.Body.String(), "Conteúdo completo") {
		t.Fatalf("magic link with own session: got %d", rr.Code)
	}
	if sc := rr.Header().Get("Set-Cookie"); strings.Contains(sc, wsA.ID) {
		t.Fatalf("reader link set the editor workspace cookie: %q", sc)
	}
	// editor API calls still resolve to visitor B's own (empty) workspace
	if rr := serve("GET", "/api/v1/issues/1", cookieB, nil); rr.Code != 404 {
		t.Errorf("editor route leaked the linked workspace's issue: %d", rr.Code)
	}

	// after the reader unsubscribes the same magic link still opens —
	// in the non-subscriber (teaser) state, not a 404
	key := regexp.MustCompile(`key=([^&]+)`).FindStringSubmatch(link)[1]
	rid, err := wsA.DB.ReaderByToken(ctx, pub.ID, key)
	if err != nil {
		t.Fatalf("ReaderByToken: %v", err)
	}
	if err := wsA.DB.UnsubscribeReader(ctx, rid); err != nil {
		t.Fatalf("UnsubscribeReader: %v", err)
	}
	rr = serve("GET", apiLink, "", nil)
	if rr.Code != 200 {
		t.Fatalf("magic link after unsubscribe = %d, want 200", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "Conteúdo completo") {
		t.Error("unsubscribed reader still sees paid content")
	}
	if !strings.Contains(rr.Body.String(), "Exclusivo para assinantes") {
		t.Error("unsubscribed reader should see the teaser")
	}
}
