package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

func accountServe(srv interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, method, path, cookie string, body any) *httptest.ResponseRecorder {
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

func accountCookie(rr *httptest.ResponseRecorder) string {
	return strings.SplitN(rr.Header().Get("Set-Cookie"), ";", 2)[0]
}

func cookieWSID(cookie string) string {
	v := strings.SplitN(cookie, "=", 2)[1]
	id, _, _ := strings.Cut(v, ".")
	return id
}

func seedAccountRadar(t *testing.T, db *store.DB, name string) {
	t.Helper()
	data, _ := examples.FS.ReadFile("publications/futuro-proximo.yaml")
	def, err := config.ParsePublication(data)
	if err != nil {
		t.Fatalf("parse publication: %v", err)
	}
	def.Name = name
	if _, err := db.CreatePublication(context.Background(), def); err != nil {
		t.Fatalf("create publication: %v", err)
	}
}

func claimPath(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	res := apiJSON(t, rr)
	raw, _ := res["claim_url"].(string)
	if raw == "" {
		t.Fatalf("claim response without outbox link: %v", res)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("claim url: %v", err)
	}
	return u.RequestURI()
}

func loginPath(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	res := apiJSON(t, rr)
	raw, _ := res["login_url"].(string)
	if raw == "" {
		t.Fatalf("login response without outbox link: %v", res)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("login url: %v", err)
	}
	return u.RequestURI()
}

// KLOX-272: sign-out must not strand a returning owner in a fresh empty
// workspace — the public login link signs the browser back into the
// workspace that carries the existing Radar.
func TestAccountLoginRecoversOwnedWorkspace(t *testing.T) {
	srv, mgr := hostedServer(t)
	email := fmt.Sprintf("return-%d@x.dev", time.Now().UnixNano())

	cookieA := accountCookie(accountServe(srv, "GET", "/api/v1/workspace", "", nil))
	wsA := cookieWSID(cookieA)
	seedAccountRadar(t, mgr.Pool().For(wsA), "Agro no campo")

	rr := accountServe(srv, "POST", "/api/v1/workspace/claim", cookieA, map[string]string{"email": email, "lang": "en"})
	if rr.Code != 200 {
		t.Fatalf("claim start = %d %s", rr.Code, rr.Body.String())
	}
	claim := claimPath(t, rr)
	if rr = accountServe(srv, "GET", claim, "", nil); rr.Code != 200 {
		t.Fatalf("claim page = %d", rr.Code)
	}
	if rr = accountServe(srv, "POST", claim, "", nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("claim confirm = %d, want 303", rr.Code)
	}

	if rr = accountServe(srv, "POST", "/api/v1/logout", cookieA, map[string]any{}); rr.Code != 200 {
		t.Fatalf("logout = %d %s", rr.Code, rr.Body.String())
	}
	rr = accountServe(srv, "POST", "/api/v1/auth/login", "", map[string]string{"email": email, "lang": "en"})
	if rr.Code != 200 {
		t.Fatalf("login start = %d %s", rr.Code, rr.Body.String())
	}
	if sc := rr.Header().Get("Set-Cookie"); strings.Contains(sc, workspace.CookieName) {
		t.Fatalf("login start minted a replacement workspace cookie: %q", sc)
	}
	login := loginPath(t, rr)
	rr = accountServe(srv, "GET", login, "", nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Sign back in to Kloxus") {
		t.Fatalf("login page = %d %q", rr.Code, rr.Body.String()[:160])
	}
	rr = accountServe(srv, "POST", login, "", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login confirm = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/publications?lang=en" {
		t.Fatalf("login redirect = %q, want /publications?lang=en", loc)
	}
	recovered := accountCookie(rr)
	if !strings.Contains(recovered, workspace.CookieName+"="+wsA) {
		t.Fatalf("login signed into %q, want original workspace %s", recovered, wsA)
	}
	rr = accountServe(srv, "GET", "/api/v1/publications", recovered, nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Agro no campo") {
		t.Fatalf("recovered publications = %d %s", rr.Code, rr.Body.String())
	}
}

// KLOX-272: even when the returning owner uses the old "create" form as
// a workaround, the claim link must recover the existing owned workspace
// instead of claiming the fresh empty one.
func TestClaimWithExistingEmailRecoversOwnedWorkspace(t *testing.T) {
	srv, mgr := hostedServer(t)
	email := fmt.Sprintf("workaround-%d@x.dev", time.Now().UnixNano())

	cookieA := accountCookie(accountServe(srv, "GET", "/api/v1/workspace", "", nil))
	wsA := cookieWSID(cookieA)
	seedAccountRadar(t, mgr.Pool().For(wsA), "Agro no campo")
	rr := accountServe(srv, "POST", "/api/v1/workspace/claim", cookieA, map[string]string{"email": email})
	claim := claimPath(t, rr)
	accountServe(srv, "GET", claim, "", nil)
	accountServe(srv, "POST", claim, "", nil)

	cookieB := accountCookie(accountServe(srv, "GET", "/api/v1/workspace", "", nil))
	wsB := cookieWSID(cookieB)
	if wsB == wsA {
		t.Fatal("test did not create a second workspace")
	}
	rr = accountServe(srv, "POST", "/api/v1/workspace/claim", cookieB, map[string]string{"email": email})
	if rr.Code != 200 {
		t.Fatalf("second claim start = %d %s", rr.Code, rr.Body.String())
	}
	claim = claimPath(t, rr)
	rr = accountServe(srv, "GET", claim, "", nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Sign back in to Kloxus") {
		t.Fatalf("existing-account claim copy = %d %q", rr.Code, rr.Body.String()[:180])
	}
	rr = accountServe(srv, "POST", claim, "", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("second claim confirm = %d, want 303", rr.Code)
	}
	if got := accountCookie(rr); !strings.Contains(got, workspace.CookieName+"="+wsA) {
		t.Fatalf("workaround signed into %q, want original workspace %s", got, wsA)
	}
	if owner, _, err := mgr.Pool().WorkspaceOwner(context.Background(), wsB); err != nil || owner != "" {
		t.Fatalf("fresh workaround workspace owner = %q, want unclaimed (err %v)", owner, err)
	}
}

// KLOX-272: if legacy duplicates leave two real Radars on one email, the
// user gets an explicit choice instead of a silent arbitrary workspace.
func TestAccountLoginOffersExplicitChoiceForMultipleRadars(t *testing.T) {
	srv, mgr := hostedServer(t)
	email := fmt.Sprintf("multi-%d@x.dev", time.Now().UnixNano())
	ctx := context.Background()

	cookieA := accountCookie(accountServe(srv, "GET", "/api/v1/workspace", "", nil))
	cookieB := accountCookie(accountServe(srv, "GET", "/api/v1/workspace", "", nil))
	wsA, wsB := cookieWSID(cookieA), cookieWSID(cookieB)
	seedAccountRadar(t, mgr.Pool().For(wsA), "Radar A")
	seedAccountRadar(t, mgr.Pool().For(wsB), "Radar B")
	if err := mgr.Pool().ClaimWorkspace(ctx, wsA, email); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Pool().ClaimWorkspace(ctx, wsB, email); err != nil {
		t.Fatal(err)
	}

	rr := accountServe(srv, "POST", "/api/v1/auth/login", "", map[string]string{"email": email, "lang": "en"})
	login := loginPath(t, rr)
	accountServe(srv, "GET", login, "", nil)
	rr = accountServe(srv, "POST", login, "", nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Choose your Radar") ||
		!strings.Contains(rr.Body.String(), "Radar A") || !strings.Contains(rr.Body.String(), "Radar B") {
		t.Fatalf("choice page = %d %q", rr.Code, rr.Body.String()[:400])
	}
	if sc := rr.Header().Get("Set-Cookie"); strings.Contains(sc, workspace.CookieName) {
		t.Fatalf("choice page already signed in: %q", sc)
	}
	sep := "&"
	if !strings.Contains(login, "?") {
		sep = "?"
	}
	rr = accountServe(srv, "POST", login+sep+"ws="+wsB, "", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("selected login = %d, want 303", rr.Code)
	}
	if got := accountCookie(rr); !strings.Contains(got, workspace.CookieName+"="+wsB) {
		t.Fatalf("selection signed into %q, want %s", got, wsB)
	}
}
