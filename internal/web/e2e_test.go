package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// TestFixturesAreFictional denies real institutions, companies and people
// from appearing anywhere in the demo fixtures.
func TestFixturesAreFictional(t *testing.T) {
	denied := []string{
		"Banco Central do Brasil", "Banco Central,", "Pix", "Cade", "Anatel", "STF",
		"Google", "Apple", "Meta", "Microsoft", "OpenAI", "Anthropic", "Amazon",
		"Reuters", "Folha", "Estadão", "Globo", "FGV", "USP", "Unicamp",
		"São Paulo", "Brasil", "brasileir", "Lula", "Bolsonaro",
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

// runPhaseA triggers a run and waits for the issue id.
func runPhaseA(t *testing.T, srv *web.Server, db *store.DB, pubID int64) int64 {
	t.Helper()
	if rr := call(t, srv, "POST", "/pub/"+strconv.FormatInt(pubID, 10)+"/run", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST run = %d", rr.Code)
	}
	deadline := 0
	for {
		iss, err := db.LatestIssue(context.Background(), pubID)
		if err == nil {
			iis, _ := db.IssueItems(context.Background(), iss.ID)
			if len(iis) > 0 {
				return iss.ID
			}
		}
		if deadline > 3000 {
			t.Fatal("run did not produce items")
		}
		deadline++
	}
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
