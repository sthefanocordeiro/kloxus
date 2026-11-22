package web_test

// KLOX-102: the public newsletter page (/p/{slug}) and the copyable
// subscribe link on the Leitores screen. The archive lists only the
// issues a public URL may resolve — the same sent/scheduled/test-sent
// rule the issue page enforces — and reading an edition keeps the
// existing per-issue paywall gating.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// sentIssue writes one sent issue straight to the store — the archive
// rules being tested don't need the full draft pipeline.
func sentIssue(t *testing.T, db *store.DB, pubID int64, num int, subject string) int64 {
	t.Helper()
	body := "# " + subject + "\n\n## Section\n\n### Item\n\nA short sourced sentence [1]."
	iid, err := db.CreateIssue(context.Background(), pubID, num, subject, body, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := db.UpdateIssue(context.Background(), iid, "sent", body, "[]"); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	return iid
}

func TestPublicNewsletterArchive(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	ctx := context.Background()

	sentIssue(t, db, pub.ID, 2, "Edição dois")
	sentIssue(t, db, pub.ID, 1, "Edição um")
	// drafted and approved-but-unsent issues stay private
	drafted, err := db.CreateIssue(ctx, pub.ID, 3, "Rascunho privado", "# r", nil)
	if err != nil {
		t.Fatalf("CreateIssue drafted: %v", err)
	}
	if err := db.UpdateIssue(ctx, drafted, "drafted", "# r", "[]"); err != nil {
		t.Fatalf("UpdateIssue drafted: %v", err)
	}

	v := apiGet(t, srv, "/api/v1/p/futuro-proximo")
	p := v["publication"].(map[string]any)
	if p["name"] != "Futuro Próximo" || p["slug"] != "futuro-proximo" {
		t.Errorf("publication brand = %v", p)
	}
	eds := v["editions"].([]any)
	if len(eds) != 2 {
		t.Fatalf("editions = %d, want 2 (drafted must stay private)", len(eds))
	}
	first := eds[0].(map[string]any)
	if first["number"].(float64) != 2 || first["subject"] != "Edição dois" {
		t.Errorf("newest edition first = %v", first)
	}
	if eds[1].(map[string]any)["number"].(float64) != 1 {
		t.Errorf("archive order = %v", eds)
	}
	if _, ok := v["paywall"].(map[string]any); !ok {
		t.Error("paywall summary missing")
	}

	// unknown slug 404s like the issue page
	if rr := apiCall(t, srv, "GET", "/api/v1/p/no-such-newsletter", nil); rr.Code != 404 {
		t.Fatalf("unknown slug = %d, want 404", rr.Code)
	}

	// each archive edition opens through the existing issue page, whose
	// paywall gating is unchanged (covered there) — here just the link
	// contract: /p/{slug}/{num} still serves it
	if rr := apiCall(t, srv, "GET", "/api/v1/p/futuro-proximo/2", nil); rr.Code != 200 {
		t.Fatalf("archive edition page = %d", rr.Code)
	}
}

func TestReaderPublicURL(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	sentIssue(t, db, pub.ID, 1, "Edição um")

	v := apiGet(t, srv, "/api/v1/publications/"+fmt.Sprint(pub.ID)+"/reader")
	u, ok := v["public_url"].(string)
	if !ok || u == "" {
		t.Fatalf("public_url missing: %v", v["public_url"])
	}
	if want := "/p/futuro-proximo"; !strings.Contains(u, want) {
		t.Errorf("public_url = %q, want it to contain %q", u, want)
	}
	if strings.Contains(u, "ws=") {
		t.Error("public_url must never carry the workspace id")
	}
	// the counts stay honest — the demo reader is not confirmed, so the
	// Leitores screen must not count it
	if v["confirmed"].(float64) != 0 {
		t.Errorf("confirmed = %v, want 0", v["confirmed"])
	}
}

// Subscribe → confirm on a newsletter with no public issue yet must not
// land on /latest (404): it goes to the newsletter page instead.
func TestConfirmSubEmptyArchiveRedirect(t *testing.T) {
	srv, _, pub, _ := demoServer(t)
	base := "/api/v1/p/" + pub.Slug
	if rr := apiCall(t, srv, "GET", base+"/latest", nil); rr.Code != 404 {
		t.Fatalf("precondition: /latest = %d, want 404 on an empty archive", rr.Code)
	}
	rr := apiCall(t, srv, "POST", base+"/subscribe", map[string]string{"email": "novo@example.com"})
	if rr.Code != 200 {
		t.Fatalf("subscribe = %d: %s", rr.Code, rr.Body.String())
	}
	var res struct {
		ConfirmURL string `json:"confirm_url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil || res.ConfirmURL == "" {
		t.Fatalf("no confirm_url in %s", rr.Body.String())
	}
	u := res.ConfirmURL[strings.Index(res.ConfirmURL, "/confirm-sub"):]
	rr = apiCall(t, srv, "GET", u, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("confirm-sub = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/p/"+pub.Slug) || strings.Contains(loc, "/latest") {
		t.Fatalf("Location = %q, want the newsletter page, not /latest", loc)
	}

	// once an edition is out, the confirmation lands on it
	srv2, db2, pub2, _ := demoServer(t)
	sentIssue(t, db2, pub2.ID, 1, "Edição um")
	rr = apiCall(t, srv2, "POST", "/api/v1/p/"+pub2.Slug+"/subscribe", map[string]string{"email": "novo@example.com"})
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	u = res.ConfirmURL[strings.Index(res.ConfirmURL, "/confirm-sub"):]
	rr = apiCall(t, srv2, "GET", u, nil)
	if loc := rr.Header().Get("Location"); !strings.Contains(loc, "/latest") {
		t.Fatalf("Location = %q, want /latest once an issue is public", loc)
	}
}
