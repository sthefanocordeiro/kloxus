package web_test

// KLOX-257: the daily pauta mail's own unsubscribe — a "Não quero mais
// este e-mail" link plus RFC 8058 List-Unsubscribe headers in every
// send, a stored-hash token, an endpoint that answers with
// KLOXUS_PUBLISHING=off (the reader /unsub 404s there), and the
// account-menu toggle "Receber as notícias do dia por e-mail".
import (
	"context"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
	"github.com/sthefanocordeiro/kloxus/internal/web"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// dailyMailUnsubRe extracts the token the footer link carries.
var dailyMailUnsubRe = regexp.MustCompile(`/daily-mail/unsub\?t=([A-Za-z0-9_-]+)`)

// dailyMailHosted is pautaHosted with the publishing flag chosen —
// KLOXUS_PUBLISHING=off must not silence the unsubscribe endpoint.
func dailyMailHosted(t *testing.T, mailer delivery.Sender, publishing bool) (*web.Server, *store.Pool, *workspace.Manager) {
	t.Helper()
	ctx := context.Background()
	pool := testdb.Pool(t)
	seed := func(ctx context.Context, sdb *store.DB) error {
		data, err := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		if err != nil {
			return err
		}
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		_, err = sdb.SeedPublication(ctx, def)
		return err
	}
	mgr, err := workspace.OpenHosted(ctx, pool, []byte("test-secret-key-32-bytes-padded-ok!"), seed)
	if err != nil {
		t.Fatalf("OpenHosted: %v", err)
	}
	t.Cleanup(mgr.Close)
	cfg := &config.Config{
		PublishingEnabled: publishing, Mode: "demo", Port: 8080,
		PublicURL:              "https://kloxus.test",
		EmailDailyCap:          80,
		EmailWorkspaceDailyCap: 25,
	}
	srv := web.New(web.Deps{
		WS: mgr, Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm", DumpDir: llmDumpDir("")},
		Monitor: sources.FakeMonitor{}, Scraper: sources.FakeScraper{},
		Mailer: mailer, Seed: seed, Pool: pool,
		DailyPautaMail: true,
	})
	return srv, pool, mgr
}

// dailyMailAnonVisitor mints a fresh workspace without claiming it —
// pautaVisitor always claims, so the unclaimed-account path needs its
// own mint.
func dailyMailAnonVisitor(t *testing.T, mgr *workspace.Manager) (string, *store.DB) {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	ws, err := mgr.Resolve(rr, req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	set := rr.Header().Get("Set-Cookie")
	if set == "" {
		t.Fatal("expected a new workspace cookie")
	}
	t.Cleanup(func() { mgr.CloseWorkspace(ws.ID) })
	return strings.SplitN(set, ";", 2)[0], ws.DB
}

func dailyMailTo(fake *fakeSender, owner string) []*delivery.Message {
	var out []*delivery.Message
	for _, m := range fake.messages() {
		for _, to := range m.To {
			if to == owner {
				out = append(out, m)
			}
		}
	}
	return out
}

// TestDailyPautaMailUnsubLink: each send carries the footer link and
// the RFC 8058 headers pointing at the same endpoint; the stored token
// is a hash — the raw one never reaches the database.
func TestDailyPautaMailUnsubLink(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)

	cookie, db := pautaVisitor(t, mgr)
	pautaRadar(t, srv, cookie)
	owner := fmt.Sprintf("unsub-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}

	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 7, 5))

	mine := dailyMailTo(fake, owner)
	if len(mine) != 1 {
		t.Fatalf("mails to %s = %d, want 1", owner, len(mine))
	}
	msg := mine[0]
	if !strings.Contains(msg.Text, "[Não quero mais este e-mail](") {
		t.Fatalf("mail lacks the unsubscribe link:\n%s", msg.Text)
	}
	mm := dailyMailUnsubRe.FindStringSubmatch(msg.Text)
	if len(mm) == 0 {
		t.Fatalf("no unsubscribe token in the mail:\n%s", msg.Text)
	}
	unsub := "https://kloxus.test/daily-mail/unsub?t=" + mm[1]
	if msg.Headers["List-Unsubscribe"] != "<"+unsub+">" ||
		msg.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("List-Unsubscribe headers = %v", msg.Headers)
	}
	// Only the hash persists: the raw token is not in the workspaces row.
	var raw int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM workspaces WHERE id = $1 AND daily_mail_token_hash = $2`,
		db.Workspace(), mm[1]).Scan(&raw); err != nil {
		t.Fatalf("token query: %v", err)
	}
	if raw != 0 {
		t.Fatal("raw unsubscribe token persisted — only the hash may be stored")
	}
	var hashed int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM workspaces WHERE id = $1 AND daily_mail_token_hash <> ''`,
		db.Workspace()).Scan(&hashed); err != nil {
		t.Fatalf("hash query: %v", err)
	}
	if hashed != 1 {
		t.Fatal("no unsubscribe token hash stored")
	}
}

// TestDailyPautaMailUnsub: one click on the mailed link — no cookie, no
// login — stamps the opt-out, the account menu shows it off and the
// next day's sweep sends nothing. POST answers RFC 8058 one-click.
func TestDailyPautaMailUnsub(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)

	cookie, db := pautaVisitor(t, mgr)
	pautaRadar(t, srv, cookie)
	owner := fmt.Sprintf("unsub-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// The account toggle is ON for a saved account.
	rr := pautaServe(t, srv, "GET", "/api/v1/workspace", cookie, nil)
	if got := apiJSON(t, rr)["daily_mail"]; got != true {
		t.Fatalf("daily_mail for a claimed account = %v, want true", got)
	}

	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 7, 5))
	mine := dailyMailTo(fake, owner)
	if len(mine) != 1 {
		t.Fatalf("mails to %s = %d, want 1", owner, len(mine))
	}
	token := dailyMailUnsubRe.FindStringSubmatch(mine[0].Text)[1]

	// One click, no session cookie: the GET link unsubscribes and the
	// confirmation page answers in the account's language.
	rr = pautaServe(t, srv, "GET", "/daily-mail/unsub?t="+token, "", nil)
	body := rr.Body.String()
	if rr.Code != 200 || (!strings.Contains(body, "desligado") && !strings.Contains(body, "turned off")) {
		t.Fatalf("unsub GET = %d %s", rr.Code, body)
	}
	// The menu's toggle now reads off.
	rr = pautaServe(t, srv, "GET", "/api/v1/workspace", cookie, nil)
	if got := apiJSON(t, rr)["daily_mail"]; got != false {
		t.Fatalf("daily_mail after unsubscribe = %v, want false", got)
	}
	// Idempotent: a second hit (mail client POST, or a repeated click)
	// answers fine.
	rr = pautaServe(t, srv, "POST", "/daily-mail/unsub?t="+token, "", nil)
	if rr.Code != 200 {
		t.Fatalf("unsub POST = %d", rr.Code)
	}
	// An unknown token gets the failure page, not an opt-out.
	rr = pautaServe(t, srv, "GET", "/daily-mail/unsub?t=bogus-token", "", nil)
	if rr.Code != 400 {
		t.Fatalf("unsub bogus token = %d, want 400", rr.Code)
	}

	// The next day's sweep sends nothing to this owner.
	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 25, 7, 5))
	if n := len(dailyMailTo(fake, owner)); n != 1 {
		t.Fatalf("mails after unsubscribe = %d, want still 1", n)
	}
}

// TestDailyPautaMailUnsubPublishingOff: the endpoint belongs to the
// account, not the reader surface — with KLOXUS_PUBLISHING=off the
// reader /unsub 404s while the daily-mail link still works.
func TestDailyPautaMailUnsubPublishingOff(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := dailyMailHosted(t, fake, false)

	cookie, db := pautaVisitor(t, mgr)
	pautaRadar(t, srv, cookie)
	owner := fmt.Sprintf("unsub-off-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}

	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 7, 5))
	mine := dailyMailTo(fake, owner)
	if len(mine) != 1 {
		t.Fatalf("mails to %s = %d, want 1", owner, len(mine))
	}
	token := dailyMailUnsubRe.FindStringSubmatch(mine[0].Text)[1]

	if rr := pautaServe(t, srv, "GET", "/unsub", "", nil); rr.Code != 404 {
		t.Fatalf("reader /unsub with publishing off = %d, want 404", rr.Code)
	}
	if rr := pautaServe(t, srv, "GET", "/daily-mail/unsub?t="+token, "", nil); rr.Code != 200 {
		t.Fatalf("daily-mail unsub with publishing off = %d, want 200", rr.Code)
	}
	if out, err := pool.DailyMailOptedOut(ctx, owner); err != nil || !out {
		t.Fatalf("opted out = %v err %v, want true", out, err)
	}
}

// TestDailyPautaMailToggleOff: the account-menu switch stamps the same
// opt-out — the sweep skips the owner and only claimed accounts may
// touch it.
func TestDailyPautaMailToggleOff(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)

	cookie, db := pautaVisitor(t, mgr)
	pautaRadar(t, srv, cookie)
	owner := fmt.Sprintf("toggle-%d@hackbrazil.dev", time.Now().UnixNano())

	// Unclaimed accounts have no mail setting to flip.
	anon, _ := dailyMailAnonVisitor(t, mgr)
	rr := pautaServe(t, srv, "POST", "/api/v1/workspace/daily-mail", anon,
		map[string]any{"enabled": false})
	if rr.Code != 403 {
		t.Fatalf("daily-mail toggle unclaimed = %d, want 403", rr.Code)
	}
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}
	rr = pautaServe(t, srv, "POST", "/api/v1/workspace/daily-mail", cookie,
		map[string]any{"enabled": false})
	if rr.Code != 200 || apiJSON(t, rr)["daily_mail"] != false {
		t.Fatalf("toggle off = %d %s", rr.Code, rr.Body.String())
	}

	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 7, 5))
	if n := len(dailyMailTo(fake, owner)); n != 0 {
		t.Fatalf("mails with the toggle off = %d, want 0", n)
	}

	// Turning it back on clears the stamp for the whole account.
	rr = pautaServe(t, srv, "POST", "/api/v1/workspace/daily-mail", cookie,
		map[string]any{"enabled": true})
	if rr.Code != 200 || apiJSON(t, rr)["daily_mail"] != true {
		t.Fatalf("toggle on = %d %s", rr.Code, rr.Body.String())
	}
	// KLOX-256: re-enabling only re-arms the mail — it still needs
	// "notícias novas no dia". The fixture replays an identical pauta,
	// so seed the new day with a card the 24th snapshot did not carry
	// (what the overnight build would have stored for a real novelty).
	fresh := "[" + pautaMailCard(9, "Notícia nova", "Fonte Z", "u:toggle-fresh") + "]"
	if err := db.SavePautaDay(ctx, mustPubID(t, db), "2026-11-25", fresh); err != nil {
		t.Fatalf("next-day pauta: %v", err)
	}
	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 25, 7, 5))
	if n := len(dailyMailTo(fake, owner)); n != 1 {
		t.Fatalf("mails after re-enabling = %d, want 1", n)
	}
}

// TestDailyMailUnsubNoWorkspace: a server with no store answers the
// failure page instead of panicking — the route exists before any
// workspace manager is wired.
func TestDailyMailUnsubNoWorkspace(t *testing.T) {
	srv := web.New(web.Deps{Cfg: &config.Config{Mode: "demo"}})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest("GET", "/daily-mail/unsub?t=x", nil))
	if rr.Code != 400 {
		t.Fatalf("unsub without store = %d, want 400", rr.Code)
	}
}

