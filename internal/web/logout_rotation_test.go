package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// KLOX-279: a copied pre-logout credential must stop opening the old
// workspace even through another Manager with the entry already hot.
// Account recovery then signs the owner back into the same workspace
// with a credential at the current epoch — never the copied one.
func TestLogoutRotatesWorkspaceCredentialAcrossManagers(t *testing.T) {
	srv, mgr := hostedServer(t)
	email := fmt.Sprintf("rotate-%d@x.dev", time.Now().UnixNano())
	const radarName = "Credencial rotacionada"

	cookieA := accountCookie(accountServe(srv, "GET", "/api/v1/workspace", "", nil))
	wsA := cookieWSID(cookieA)
	seedAccountRadar(t, mgr.Pool().For(wsA), radarName)
	rr := accountServe(srv, "POST", "/api/v1/workspace/claim", cookieA, map[string]string{"email": email, "lang": "en"})
	if rr.Code != 200 {
		t.Fatalf("claim start = %d", rr.Code)
	}
	claim := claimPath(t, rr)
	accountServe(srv, "GET", claim, "", nil)
	if rr = accountServe(srv, "POST", claim, "", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("claim confirm = %d, want 303", rr.Code)
	}
	if rr = accountServe(srv, "GET", "/api/v1/publications", cookieA, nil); rr.Code != 200 || !strings.Contains(rr.Body.String(), radarName) {
		t.Fatalf("pre-logout publications = %d %s", rr.Code, rr.Body.String())
	}

	// A second manager over the same pool models another machine/process.
	// Priming it before logout also fills its in-memory workspace entry,
	// so only a persisted epoch check can observe the revocation.
	mgr2, err := workspace.OpenHosted(context.Background(), mgr.Pool(), hostedTestSecret, nil)
	if err != nil {
		t.Fatalf("second OpenHosted: %v", err)
	}
	t.Cleanup(mgr2.Close)
	primeReq := httptest.NewRequest("GET", "/", nil)
	primeReq.Header.Set("Cookie", cookieA)
	primed, err := mgr2.Resolve(httptest.NewRecorder(), primeReq)
	if err != nil || primed.ID != wsA {
		t.Fatalf("second manager did not resolve the pre-logout workspace: %v", err)
	}

	copied := cookieA
	if rr = accountServe(srv, "POST", "/api/v1/logout", cookieA, map[string]any{}); rr.Code != 200 {
		t.Fatalf("logout = %d %s", rr.Code, rr.Body.String())
	}

	afterReq := httptest.NewRequest("GET", "/", nil)
	afterReq.Header.Set("Cookie", copied)
	after, err := mgr2.Resolve(httptest.NewRecorder(), afterReq)
	if err != nil {
		t.Fatalf("resolve copied credential after logout: %v", err)
	}
	if after.ID == wsA {
		t.Fatal("copied pre-logout credential still opened the original workspace")
	}
	if pubs, err := after.DB.ListPublications(context.Background()); err != nil || len(pubs) != 0 {
		t.Fatalf("replacement workspace publications = %d, want empty (err %v)", len(pubs), err)
	}
	rr = accountServe(srv, "GET", "/api/v1/publications", copied, nil)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), radarName) {
		t.Fatalf("copied credential exposed old publications: status %d", rr.Code)
	}

	rr = accountServe(srv, "POST", "/api/v1/auth/login", "", map[string]string{"email": email, "lang": "en"})
	if rr.Code != 200 {
		t.Fatalf("login start = %d", rr.Code)
	}
	login := loginPath(t, rr)
	accountServe(srv, "GET", login, "", nil)
	rr = accountServe(srv, "POST", login, "", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login confirm = %d, want 303", rr.Code)
	}
	recovered := accountCookie(rr)
	if cookieWSID(recovered) != wsA {
		t.Fatal("account recovery did not return to the original workspace")
	}
	if recovered == copied {
		t.Fatal("account recovery reissued the pre-logout credential")
	}
	rr = accountServe(srv, "GET", "/api/v1/publications", recovered, nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), radarName) {
		t.Fatalf("recovered publications = %d %s", rr.Code, rr.Body.String())
	}
}

// KLOX-279 compatibility: a cookie minted before the epoch existed is
// read as epoch 0 and keeps working until the first logout; that same
// logout increments the persisted epoch and retires it too.
func TestLegacyWorkspaceCookieValidUntilFirstLogout(t *testing.T) {
	srv, mgr := hostedServer(t)
	email := fmt.Sprintf("legacy-%d@x.dev", time.Now().UnixNano())
	const radarName = "Radar legado"

	cookieA := accountCookie(accountServe(srv, "GET", "/api/v1/workspace", "", nil))
	wsA := cookieWSID(cookieA)
	seedAccountRadar(t, mgr.Pool().For(wsA), radarName)
	rr := accountServe(srv, "POST", "/api/v1/workspace/claim", cookieA, map[string]string{"email": email})
	if rr.Code != 200 {
		t.Fatalf("claim start = %d", rr.Code)
	}
	claim := claimPath(t, rr)
	accountServe(srv, "GET", claim, "", nil)
	if rr = accountServe(srv, "POST", claim, "", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("claim confirm = %d, want 303", rr.Code)
	}

	legacy := legacyWorkspaceCookie(wsA)
	rr = accountServe(srv, "GET", "/api/v1/publications", legacy, nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), radarName) {
		t.Fatalf("legacy cookie before logout = %d %s", rr.Code, rr.Body.String())
	}
	if rr = accountServe(srv, "POST", "/api/v1/logout", legacy, map[string]any{}); rr.Code != 200 {
		t.Fatalf("legacy logout = %d %s", rr.Code, rr.Body.String())
	}
	rr = accountServe(srv, "GET", "/api/v1/publications", legacy, nil)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), radarName) {
		t.Fatalf("legacy cookie after logout exposed the old workspace: status %d", rr.Code)
	}
}
