package web_test

// KLOX-102: the public newsletter page (/p/{slug}) and the copyable
// subscribe link on the Leitores screen. The archive lists only the
// issues a public URL may resolve — the same sent/scheduled/test-sent
// rule the issue page enforces — and reading an edition keeps the
// existing per-issue paywall gating.

import (
	"context"
	"fmt"
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
