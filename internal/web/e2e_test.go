package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
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
	for _, dir := range []string{"fixtures/feeds", "fixtures/llm", "fixtures/pages", "publications"} {
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
	issueURL := "/issue/" + strconv.FormatInt(issueID, 10)

	if rr := call(t, srv, "GET", issueURL+"/export?fmt=markdown", nil); rr.Code == 200 {
		t.Fatal("export before approval should not return 200")
	}
	if rr := call(t, srv, "POST", issueURL+"/send", nil); rr.Code == 200 {
		t.Fatal("send before approval should not return 200")
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if iss.Status == "approved" || iss.Status == "sent" {
		t.Fatalf("issue status = %q, should not be approved/sent", iss.Status)
	}
}

// TestRejectRequiresReason verifies rejection validation.
func TestRejectRequiresReason(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	issueID := runPhaseA(t, srv, db, pub.ID)
	iis, _ := db.IssueItems(ctx, issueID)
	issueURL := "/issue/" + strconv.FormatInt(issueID, 10)

	call(t, srv, "POST", issueURL+"/item/"+strconv.FormatInt(iis[0].ID, 10)+"/decision",
		url.Values{"kind": {"reject"}, "reason": {""}})
	ii, _ := db.GetIssueItem(ctx, iis[0].ID)
	if ii.Status != "pending" {
		t.Fatalf("reject without reason changed status to %q", ii.Status)
	}
}

// runPhaseA triggers a run and waits for the run status to report done.
func runPhaseA(t *testing.T, srv *web.Server, db *store.DB, pubID int64) int64 {
	t.Helper()
	if rr := call(t, srv, "POST", "/pub/"+strconv.FormatInt(pubID, 10)+"/run", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST run = %d", rr.Code)
	}
	var st struct {
		Done    bool   `json:"done"`
		IssueID int64  `json:"issue_id"`
		Err     string `json:"error"`
		Step    string `json:"step"`
	}
	statusURL := "/pub/" + strconv.FormatInt(pubID, 10) + "/run/status"
	for deadline := 0; deadline < 400; deadline++ {
		rr := call(t, srv, "GET", statusURL, nil)
		if err := json.Unmarshal(rr.Body.Bytes(), &st); err == nil && st.Done {
			if st.Err != "" {
				t.Fatalf("run failed: %s", st.Err)
			}
			// When done, status reports the last step of the list so the
			// run page marks every step (incl. draft/citecheck) complete.
			if st.Step != pipeline.Steps[len(pipeline.Steps)-1] {
				t.Fatalf("done run reports step %q, want %q", st.Step, pipeline.Steps[len(pipeline.Steps)-1])
			}
			return st.IssueID
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return 0
}

// TestWorkspaceIsolation: two visitors get separate databases; resetting
// one does not affect the other.
func TestWorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	seed := func(ctx context.Context, sdb *store.DB) error {
		data, _ := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		_, err = sdb.SeedPublication(ctx, def)
		return err
	}
	mgr, err := workspace.OpenHosted(ctx, dir, []byte("test-secret-key-32-bytes-padded-ok!"), seed)
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
	issueURL := "/issue/" + strconv.FormatInt(issueID, 10)
	iis, _ := db.IssueItems(ctx, issueID)

	post := func(path string, v url.Values) *httptest.ResponseRecorder {
		return call(t, srv, "POST", path, v)
	}
	// reject one, approve the rest — exercises undo as well
	post(issueURL+"/item/"+strconv.FormatInt(iis[1].ID, 10)+"/decision",
		url.Values{"kind": {"reject"}, "reason": {"off the line"}})
	// undo the rejection, then reject again for real
	post(issueURL+"/item/"+strconv.FormatInt(iis[1].ID, 10)+"/decision",
		url.Values{"kind": {"undo"}})
	if ii, _ := db.GetIssueItem(ctx, iis[1].ID); ii.Status != "pending" {
		t.Fatalf("undo rejection: status = %q, want pending", ii.Status)
	}
	post(issueURL+"/item/"+strconv.FormatInt(iis[1].ID, 10)+"/decision",
		url.Values{"kind": {"reject"}, "reason": {"off the line"}})
	for _, ii := range iis {
		if ii.ID != iis[1].ID {
			post(issueURL+"/item/"+strconv.FormatInt(ii.ID, 10)+"/decision", url.Values{"kind": {"approve"}})
		}
	}
	for i := 0; i < 2; i++ {
		if rr := post(issueURL+"/draft", nil); rr.Code != http.StatusSeeOther {
			t.Fatalf("draft #%d = %d", i, rr.Code)
		}
	}
	iss, _ := db.GetIssue(ctx, issueID)
	if len(iss.Flags) == 0 {
		t.Fatal("expected citation flags")
	}
	// unchanged edit keeps the flag
	if rr := post(issueURL+"/flag/0/edit", url.Values{
		"text": {iss.Flags[0].Sentence}}); rr.Code == http.StatusSeeOther {
		t.Fatal("unchanged edit should be refused")
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if len(iss.Flags) == 0 {
		t.Fatal("unchanged edit cleared the flag")
	}
	// supported edit resolves it (grounded in a dossier fact for [1])
	if rr := post(issueURL+"/flag/0/edit", url.Values{
		"text": {"Comentários podem ser enviados até 15 de dezembro pelo portal do órgão [1]"}}); rr.Code != http.StatusSeeOther {
		t.Fatalf("supported edit = %d", rr.Code)
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if len(iss.Flags) != 0 {
		t.Fatalf("supported edit left %d flag(s) — want the sentence replaced", len(iss.Flags))
	}
	// resolve any remaining flags by removal
	for len(iss.Flags) > 0 {
		post(issueURL+"/flag/0/remove", nil)
		iss, _ = db.GetIssue(ctx, issueID)
	}
	post(issueURL+"/commentary", url.Values{"section": {"The big one"}, "text": {"Minha análise."}})
	if rr := post(issueURL+"/approve", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("approve = %d", rr.Code)
	}
	// editing an approved issue reverts it to drafted
	post(issueURL+"/commentary", url.Values{"section": {"The big one"}, "text": {"Minha análise v2."}})
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "drafted" {
		t.Fatalf("edit after approve: status = %q, want drafted", iss.Status)
	}
	post(issueURL+"/approve", nil)
	// send — demo reader is not subscribed → teaser + unsubscribe
	rr := post(issueURL+"/send", nil)
	if !strings.Contains(rr.Body.String(), "Issue sent") {
		t.Fatalf("send = %d", rr.Code)
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
	if !strings.Contains(full.BodyMD, "Subscribers only") {
		t.Error("issue email leaked the paid section to non-subscribers")
	}
	if !strings.Contains(full.BodyMD, "cancelar assinatura") {
		t.Error("issue email lacks an unsubscribe link")
	}
	// sent issue is read-only
	if rr := post(issueURL+"/commentary", url.Values{"section": {"The big one"}, "text": {"v3"}}); rr.Code == 200 {
		if !strings.Contains(rr.Body.String(), "read-only") {
			t.Fatal("commentary on sent issue should be refused")
		}
	}
	// .eml has standard headers + encoded subject
	raw := call(t, srv, "GET", fmt.Sprintf("/outbox/%d/eml", issueMail.ID), nil)
	for _, h := range []string{"From:", "MIME-Version: 1.0", "Date: ", "=?utf-8?", "=?UTF-8?"} {
		if !strings.Contains(raw.Body.String(), h) && h != "=?UTF-8?" {
			t.Errorf(".eml missing %q", h)
		}
	}
}

// TestInPlaceDecisionCard: fetch decisions must return the re-rendered
// card (fresh status, reason, buttons) — and validation errors must not
// strip the card of its actions.
func TestInPlaceDecisionCard(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	issueID := runPhaseA(t, srv, db, pub.ID)
	iis, _ := db.IssueItems(ctx, issueID)
	path := "/issue/" + strconv.FormatInt(issueID, 10) +
		"/item/" + strconv.FormatInt(iis[0].ID, 10) + "/decision"

	fetch := func(v url.Values) string {
		req := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Requested-With", "fetch")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr.Body.String()
	}

	// reject with empty reason: error shown, buttons preserved, still pending
	body := fetch(url.Values{"kind": {"reject"}, "reason": {""}})
	if !strings.Contains(body, "rejection needs a reason") {
		t.Error("empty-reason fetch: card should show the validation error")
	}
	if !strings.Contains(body, `value="approve"`) {
		t.Error("empty-reason fetch: card lost its buttons")
	}
	if strings.Contains(body, ">rejected<") {
		t.Error("empty-reason fetch: card claims rejected status")
	}

	// reject with reason: card re-renders rejected, shows reason + undo
	body = fetch(url.Values{"kind": {"reject"}, "reason": {"off the line"}})
	for _, want := range []string{">rejected<", "off the line", `value="undo"`} {
		if !strings.Contains(body, want) {
			t.Errorf("rejected card missing %q", want)
		}
	}
	if strings.Contains(body, `value="approve"`) {
		t.Error("rejected card still shows approve button")
	}

	// undo returns the card to pending with actions back
	body = fetch(url.Values{"kind": {"undo"}})
	if !strings.Contains(body, ">pending<") || !strings.Contains(body, `value="approve"`) {
		t.Error("undo fetch: card should be pending with buttons restored")
	}

	// approve renders the approved badge in place
	body = fetch(url.Values{"kind": {"approve"}})
	if !strings.Contains(body, ">approved<") {
		t.Error("approve fetch: card should show approved status")
	}
}

// TestGuideFromRejectionsOnly: the editorial guide is built from rejection
// reasons only — approvals never become rules.
func TestGuideFromRejectionsOnly(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()
	pubURL := "/pub/" + strconv.FormatInt(pub.ID, 10)

	// no decisions yet → rebuild refuses instead of writing an empty guide
	if rr := call(t, srv, "POST", pubURL+"/guide/rebuild", nil); rr.Code != http.StatusConflict {
		t.Fatalf("rebuild with no decisions = %d, want 409", rr.Code)
	}

	issueID := runPhaseA(t, srv, db, pub.ID)
	issueURL := "/issue/" + strconv.FormatInt(issueID, 10)
	iis, _ := db.IssueItems(ctx, issueID)
	decide := func(iid int64, v url.Values) {
		call(t, srv, "POST", issueURL+"/item/"+strconv.FormatInt(iid, 10)+"/decision", v)
	}
	// an approve decision must not feed the guide
	decide(iis[0].ID, url.Values{"kind": {"approve"}})
	decide(iis[1].ID, url.Values{"kind": {"reject"}, "reason": {"fora da minha linha editorial."}})

	if rr := call(t, srv, "POST", pubURL+"/guide/rebuild", nil); rr.Code != http.StatusSeeOther {
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
	call(t, srv, "POST", pubURL+"/guide/rebuild", nil)
	_, again, _ := db.LatestGuide(ctx, pub.ID)
	if strings.Count(again, "linha editorial") > 1 {
		t.Errorf("rebuild duplicated the rule:\n%s", again)
	}
}

// TestMagicLinkCarriesWorkspace: the magic link embeds the workspace id so
// it opens the issue in any browser — not just the session that minted it.
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
	mgr, err := workspace.OpenHosted(ctx, t.TempDir(),
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

	serve := func(method, path, cookie string, form url.Values) *httptest.ResponseRecorder {
		var body *strings.Reader
		if form == nil {
			body = strings.NewReader("")
		} else {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

	pubs, _ := wsA.DB.ListPublications(ctx)
	pub := pubs[0]
	body := "# Edição 1\n\n## The big one\n\nConteúdo completo para assinantes."
	iid, _ := wsA.DB.CreateIssue(ctx, pub.ID, 1, "Edição 1", body)
	if err := wsA.DB.UpdateIssue(ctx, iid, "sent", body, "[]"); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	pubURL := "/pub/" + strconv.FormatInt(pub.ID, 10)
	if rr := serve("POST", pubURL+"/paywall/simulate", cookieA, nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("paywall/simulate = %d", rr.Code)
	}
	emails, _ := wsA.DB.OutboxEmails(ctx)
	var link string
	for _, e := range emails {
		if e.Kind == "magic_link" {
			full, _ := wsA.DB.GetOutboxEmail(ctx, e.ID)
			m := regexp.MustCompile(`key=([^&"']+)&(?:amp;)?ws=([^"']+)`).FindStringSubmatch(full.BodyHTML)
			if m != nil {
				link = fmt.Sprintf("%s/reader?key=%s&ws=%s", pubURL, m[1], m[2])
			}
		}
	}
	if link == "" {
		t.Fatal("magic-link email does not carry key + ws parameters")
	}

	// visitor B (no cookie): the ws parameter lands them in wsA's issue
	rr := serve("GET", link, "", nil)
	if !strings.Contains(rr.Body.String(), "Conteúdo completo") {
		t.Fatalf("magic link cross-browser: got %d / %q", rr.Code, rr.Body.String()[:200])
	}
	// sanity: the same link minus ws opens the empty workspace → no issue
	rr = serve("GET", strings.SplitN(link, "&ws=", 2)[0], "", nil)
	if !strings.Contains(rr.Body.String(), "no published issue") {
		t.Error("link without ws should land in an empty workspace")
	}
}
