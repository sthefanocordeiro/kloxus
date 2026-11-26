package web_test

// The daily pauta sweep (KLOX-123 Block 2): the 06:30 America/Sao_Paulo
// build stores each active radar's "pauta de hoje" and Hoje serves it;
// the 07:00 mail reaches claimed radars once a day. Ticks run on the
// shared pool with an explicit clock — no waiting for wall time.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"net/http/httptest"
	"net/url"
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

// spZone is the sweep's timezone for tests — a fixed UTC-3, the exact
// America/Sao_Paulo offset since Brazil dropped DST in 2019.
var spZone = time.FixedZone("America/Sao_Paulo", -3*3600)

// pautaMailOn is the DailyPautaMail setting pautaHosted builds with.
var pautaMailOn = true

// pautaMailTopForTest mirrors the server constant (unexported).
const pautaMailTopForTest = 3

func spAt(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, spZone)
}

func spDayOf(t time.Time) string { return t.In(spZone).Format("2006-01-02") }

// pautaHosted is a hosted (cookie-per-visitor) demo server on the
// shared pool, with the fixture fetcher so radars hold real stories.
func pautaHosted(t *testing.T, mailer delivery.Sender) (*web.Server, *store.Pool, *workspace.Manager) {
	return pautaHostedCfg(t, mailer, nil)
}

// pautaHostedCfg is pautaHosted with the test tweaking the config
// (mail caps) before the server is built.
func pautaHostedCfg(t *testing.T, mailer delivery.Sender, tweak func(*config.Config)) (*web.Server, *store.Pool, *workspace.Manager) {
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
		PublishingEnabled: true, Mode: "demo", Port: 8080,
		PublicURL:              "https://kloxus.test",
		EmailDailyCap:          80,
		EmailWorkspaceDailyCap: 25,
	}
	if tweak != nil {
		tweak(cfg)
	}
	srv := web.New(web.Deps{
		WS: mgr, Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm", DumpDir: llmDumpDir("")},
		Monitor: sources.FakeMonitor{}, Scraper: sources.FakeScraper{},
		Mailer: mailer, Seed: seed, Pool: pool,
		DailyPautaMail: pautaMailOn,
	})
	return srv, pool, mgr
}

// pautaServe calls srv with the visitor's workspace cookie.
func pautaServe(t *testing.T, srv *web.Server, method, path, cookie string, body any) *httptest.ResponseRecorder {
	t.Helper()
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

// pautaVisitor mints a fresh workspace through the manager and returns
// its cookie and scoped store.
func pautaVisitor(t *testing.T, mgr *workspace.Manager) (string, *store.DB) {
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
	if err := ws.DB.ClaimWorkspace(req.Context(), "owner@example.com"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	return strings.SplitN(set, ";", 2)[0], ws.DB
}

// pautaRadar creates the visitor's radar through the onboarding API and
// returns the publication id.
func pautaRadar(t *testing.T, srv *web.Server, cookie string) int64 {
	t.Helper()
	rr := pautaServe(t, srv, "POST", "/api/v1/radar", cookie,
		map[string]any{"about": "futuro e tecnologia"})
	if rr.Code != 201 {
		t.Fatalf("radar = %d %s", rr.Code, rr.Body.String())
	}
	pub := apiJSON(t, rr)["publication"].(map[string]any)
	return int64(pub["id"].(float64))
}

// TestDailyPautaBuild: at 06:30 SP the sweep stores the day's pauta for
// radars visited in the last 7 days — not before, not for idle ones —
// and the stored row drives Hoje until an interaction invalidates it.
func TestDailyPautaBuild(t *testing.T) {
	ctx := context.Background()
	srv, pool, mgr := pautaHosted(t, nil)
	now := spAt(2026, 11, 23, 6, 45)
	day := spDayOf(now)

	cookieA, dbA := pautaVisitor(t, mgr)
	pubA := pautaRadar(t, srv, cookieA)
	cookieB, dbB := pautaVisitor(t, mgr)
	pubB := pautaRadar(t, srv, cookieB)

	// ws B is stale: last seen eight days ago — outside the 7-day window.
	stale := store.TSUTC(now.Add(-8 * 24 * time.Hour))
	if _, err := dbB.ExecContext(ctx,
		`UPDATE workspaces SET last_seen_at = $1 WHERE id = $2`, stale, dbB.Workspace()); err != nil {
		t.Fatalf("age workspace: %v", err)
	}

	// Before 06:30 SP nothing is built.
	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 23, 6, 29))
	if _, err := dbA.PautaDay(ctx, pubA, day); err != sql.ErrNoRows {
		t.Fatalf("pauta before 06:30 = %v, want no row", err)
	}

	srv.DailyPautaTick(ctx, pool, now)

	row, err := dbA.PautaDay(ctx, pubA, day)
	if err != nil {
		t.Fatalf("pauta after 06:30: %v", err)
	}
	var snap []map[string]any
	if err := json.Unmarshal([]byte(row.CardsJSON), &snap); err != nil || len(snap) == 0 {
		t.Fatalf("stored pauta = %v cards (err %v)", len(snap), err)
	}
	// The idle radar gets no build.
	if _, err := dbB.PautaDay(ctx, pubB, day); err != sql.ErrNoRows {
		t.Fatalf("stale workspace pauta = %v, want no row", err)
	}
	// A second tick does not rebuild the same day.
	row.BuiltAt = "2000-01-01 00:00:00"
	if _, err := dbA.ExecContext(ctx,
		`UPDATE pauta_days SET built_at = $4 WHERE workspace_id=$1 AND publication_id=$2 AND day=$3`,
		dbA.Workspace(), pubA, day, row.BuiltAt); err != nil {
		t.Fatalf("stamp built_at: %v", err)
	}
	srv.DailyPautaTick(ctx, pool, now)
	if again, _ := dbA.PautaDay(ctx, pubA, day); again.BuiltAt != row.BuiltAt {
		t.Fatal("second tick rebuilt the same day")
	}

	// Hoje serves the stored pauta verbatim.
	if err := dbA.SavePautaDay(ctx, pubA, spDayOf(time.Now()),
		`[{"story_id":1,"title":"PAUTA DO DIA","outlet":"Fonte X","outlets":1,"summary":"s","topics":[],"url":"https://x.dev/a"}]`); err != nil {
		t.Fatalf("save today pauta: %v", err)
	}
	rr := pautaServe(t, srv, "GET", "/api/v1/publications/"+fmt.Sprint(pubA)+"/today", cookieA, nil)
	if rr.Code != 200 {
		t.Fatalf("today = %d", rr.Code)
	}
	cards := apiJSON(t, rr)["cards"].([]any)
	if len(cards) != 1 || cards[0].(map[string]any)["title"] != "PAUTA DO DIA" {
		t.Fatalf("today did not serve the stored pauta: %v", cards)
	}

	// A Sim/Não answer invalidates the snapshot — the next Hoje rebuilds.
	rr = pautaServe(t, srv, "POST", "/api/v1/publications/"+fmt.Sprint(pubA)+"/calibration/answer", cookieA,
		map[string]any{"story_id": 1, "answer": "no"})
	if rr.Code != 200 {
		t.Fatalf("answer = %d", rr.Code)
	}
	if _, err := dbA.PautaDay(ctx, pubA, spDayOf(time.Now())); err != sql.ErrNoRows {
		t.Fatalf("pauta survived an answer: %v", err)
	}
	rr = pautaServe(t, srv, "GET", "/api/v1/publications/"+fmt.Sprint(pubA)+"/today", cookieA, nil)
	live := apiJSON(t, rr)["cards"].([]any)
	if len(live) == 1 && live[0].(map[string]any)["title"] == "PAUTA DO DIA" {
		t.Fatal("today kept serving the invalidated pauta")
	}
}

// TestDailyPautaMailOffByDefault (KLOX-166 / D2): with the mail flag off
// the sweep still builds the pauta but never mails a claimed radar.
func TestDailyPautaMailOff(t *testing.T) {
	ctx := context.Background()
	pautaMailOn = false
	t.Cleanup(func() { pautaMailOn = true })
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)
	now := spAt(2026, 11, 24, 7, 5)
	cookie, db := pautaVisitor(t, mgr)
	pub := pautaRadar(t, srv, cookie)
	owner := fmt.Sprintf("pauta-off-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}
	srv.DailyPautaTick(ctx, pool, now)
	if row, _ := db.PautaDay(ctx, pub, spDayOf(now)); row == nil {
		t.Fatal("pauta not built")
	}
	for _, m := range fake.messages() {
		t.Fatalf("pauta mail sent while disabled: %v", m.Subject)
	}
}

// TestDailyPautaMail: at 07:00 SP claimed radars get the one daily
// "sua pauta de hoje está pronta" mail with a working sign-in link;
// anonymous radars are skipped, and a second tick never re-sends.
func TestDailyPautaMail(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)
	now := spAt(2026, 11, 24, 7, 5)
	day := spDayOf(now)

	cookieA, dbA := pautaVisitor(t, mgr)
	pubA := pautaRadar(t, srv, cookieA)
	cookieB, dbB := pautaVisitor(t, mgr)
	pautaRadar(t, srv, cookieB)
	owner := fmt.Sprintf("pauta-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, dbA.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// The build alone (06:45) must not mail anyone.
	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 6, 45))
	for _, m := range fake.messages() {
		t.Fatalf("mail before 07:00: %v", m.Subject)
	}
	if row, _ := dbA.PautaDay(ctx, pubA, day); row == nil {
		t.Fatal("06:45 tick did not store the pauta")
	}

	srv.DailyPautaTick(ctx, pool, now)

	var mine []*delivery.Message
	for _, m := range fake.messages() {
		for _, to := range m.To {
			if to == owner {
				mine = append(mine, m)
			}
		}
	}
	if len(mine) != 1 {
		t.Fatalf("mails to %s = %d, want 1", owner, len(mine))
	}
	msg := mine[0]
	if msg.Subject != "Suas notícias de hoje estão prontas" || msg.Kind != "pauta" {
		t.Fatalf("mail = %q kind %q", msg.Subject, msg.Kind)
	}
	if !strings.Contains(msg.Text, "/claim?t=") {
		t.Fatalf("mail has no sign-in link: %s", msg.Text)
	}
	// KLOX-267: the HTML part is the KLOX-251 email template — no
	// author-commentary block, one teal create-post CTA per top story,
	// and the KLOX-257 unsubscribe in the footer.
	if !strings.Contains(msg.HTML, `lang="pt-BR"`) || !strings.Contains(msg.HTML, "Seu Radar de hoje") {
		t.Fatalf("HTML is not the PT Radar briefing:\n%s", msg.HTML)
	}
	if n := strings.Count(msg.HTML, "Criar post com essa notícia →"); n != pautaMailTopForTest {
		t.Fatalf("story CTAs = %d, want %d", n, pautaMailTopForTest)
	}
	for _, want := range []string{
		"background-color:#0f766e",
		"next=%2Fcompor%3Fpub%3D",
		"Descadastrar deste Radar",
		"https://kloxus.test/daily-mail/unsub?t=",
	} {
		if !strings.Contains(msg.HTML, want) {
			t.Fatalf("HTML lacks %q", want)
		}
	}
	for _, banned := range []string{"Seu comentário", "commentary", "<script", "<link"} {
		if strings.Contains(msg.HTML, banned) {
			t.Fatalf("HTML contains banned %q", banned)
		}
	}
	if _, err := dbA.MailByIdemKey(ctx, "pauta/"+dbA.Workspace()+"/"+day); err != nil {
		t.Fatalf("ledger row: %v", err)
	}
	if row, _ := dbA.PautaDay(ctx, pubA, day); row == nil || row.EmailedAt == "" {
		t.Fatal("pauta not stamped emailed")
	}
	// The anonymous radar was handled without a mail.
	rowB, err := dbB.PautaDay(ctx, mustPubID(t, dbB), day)
	if err == nil && rowB.EmailedAt == "" {
		t.Fatal("anonymous radar never stamped")
	}

	// The emailed sign-in link really signs a browser in (claim page +
	// confirm, exactly like the claim mail).
	mm := claimTokenRe.FindStringSubmatch(msg.Text)
	if len(mm) == 0 {
		t.Fatal("no claim token in the pauta mail")
	}
	if rr := pautaServe(t, srv, "GET", "/claim?t="+mm[1], "", nil); rr.Code != 200 {
		t.Fatalf("claim page = %d", rr.Code)
	}

	// The footer "Abrir no Kloxus" is the same kind of 72h claim as the
	// text part, aimed at today's briefing — without it the HTML-only
	// recipient lands signed out on the bare origin.
	openRe := regexp.MustCompile(`href="([^"]*next=%2Fpublications[^"]*)"[^>]*>Abrir no Kloxus`)
	om := openRe.FindStringSubmatch(msg.HTML)
	if len(om) == 0 {
		t.Fatal("HTML has no magic link to open today's briefing")
	}
	openURL, err := url.Parse(html.UnescapeString(om[1]))
	if err != nil {
		t.Fatalf("parse open link: %v", err)
	}
	if openURL.Path != "/claim" || openURL.Query().Get("t") == "" ||
		openURL.Query().Get("lang") != "pt" ||
		openURL.Query().Get("next") != fmt.Sprintf("/publications/%d/today", pubA) {
		t.Fatalf("open link = %q, want claim for /publications/%d/today", openURL.String(), pubA)
	}

	// The first story CTA signs the browser in and lands on the real
	// composer deep link with the story pre-linked (KLOX-267).
	ctaRe := regexp.MustCompile(`href="([^"]*next=%2Fcompor[^"]*)"`)
	cm := ctaRe.FindStringSubmatch(msg.HTML)
	if len(cm) == 0 {
		t.Fatal("no composer CTA link in the HTML mail")
	}
	cta, err := url.Parse(html.UnescapeString(cm[1]))
	if err != nil {
		t.Fatalf("parse CTA: %v", err)
	}
	claimPath := cta.RequestURI()
	if rr := pautaServe(t, srv, "GET", claimPath, "", nil); rr.Code != 200 {
		t.Fatalf("story claim page = %d", rr.Code)
	} else if !strings.Contains(rr.Body.String(), "next=") {
		t.Fatal("claim confirm form dropped the composer next path")
	}
	rr := pautaServe(t, srv, "POST", claimPath, "", nil)
	loc := rr.Header().Get("Location")
	if rr.Code != 303 || !strings.HasPrefix(loc, "/compor?pub=") ||
		!strings.Contains(loc, "&noticia=") || !strings.Contains(loc, "&lang=pt") {
		t.Fatalf("story claim = %d → %q, want composer deep link", rr.Code, loc)
	}

	// A second tick sends nothing new to the owner.
	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 7, 40))
	n := 0
	for _, m := range fake.messages() {
		for _, to := range m.To {
			if to == owner {
				n++
			}
		}
	}
	if n != 1 {
		t.Fatalf("mails after second tick = %d, want 1", n)
	}
}

func mustPubID(t *testing.T, db *store.DB) int64 {
	t.Helper()
	pubs, err := db.ListPublications(context.Background())
	if err != nil || len(pubs) == 0 {
		t.Fatalf("publications: %v", err)
	}
	return pubs[0].ID
}

// TestDailyPautaMailOncePerRecipient (KLOX-154): one owner with several
// claimed radars gets a single mail per day, not one per workspace.
func TestDailyPautaMailOncePerRecipient(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)
	owner := fmt.Sprintf("dup-%d@hackbrazil.dev", time.Now().UnixNano())
	for i := 0; i < 3; i++ {
		cookie, db := pautaVisitor(t, mgr)
		pautaRadar(t, srv, cookie)
		if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
			t.Fatalf("claim: %v", err)
		}
	}
	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 7, 5))
	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 7, 6))
	n := 0
	for _, m := range fake.messages() {
		for _, to := range m.To {
			if to == owner {
				n++
			}
		}
	}
	if n != 1 {
		t.Fatalf("mails to one owner across 3 radars = %d, want 1", n)
	}
}

// pautaMailCard is one stored-pauta card JSON carrying the stable
// feedback keys the no-new-stories diff reads.
func pautaMailCard(storyID int64, title, outlet, key string) string {
	return fmt.Sprintf(`{"story_id":%d,"title":%q,"outlet":%q,"outlets":1,"summary":"","topics":[],"url":"https://x.dev/a","feedback_keys":[%q]}`,
		storyID, title, outlet, key)
}

// TestDailyPautaMailSkipsStalePauta (KLOX-256): a pauta that only
// repeats the previous snapshot is not "notícias novas" — the 07:00
// mail stays unsent and the day is stamped; a genuinely new card lets
// the mail through again.
func TestDailyPautaMailSkipsStalePauta(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)
	now := spAt(2026, 12, 2, 7, 5)
	day := spDayOf(now)
	prevDay := spDayOf(now.Add(-24 * time.Hour))

	cookie, db := pautaVisitor(t, mgr)
	pautaRadar(t, srv, cookie)
	pub := mustPubID(t, db)
	owner := fmt.Sprintf("stale-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE workspaces SET last_seen_at = $1 WHERE id = $2`,
		store.TSUTC(now.Add(-time.Hour)), db.Workspace()); err != nil {
		t.Fatalf("touch workspace: %v", err)
	}

	same := "[" + pautaMailCard(1, "A", "X", "u:a") + "," + pautaMailCard(2, "B", "Y", "u:b") + "]"
	if err := db.SavePautaDay(ctx, pub, prevDay, same); err != nil {
		t.Fatalf("prev pauta: %v", err)
	}
	if err := db.SavePautaDay(ctx, pub, day, same); err != nil {
		t.Fatalf("today pauta: %v", err)
	}
	srv.DailyPautaTick(ctx, pool, now)
	// The sweep walks every active workspace on the shared pool, so
	// only this radar's owner may be checked — other workspaces' mails
	// are their own tests' business.
	for _, m := range dailyMailTo(fake, owner) {
		t.Fatalf("repeat pauta mailed: %v", m.Subject)
	}
	if row, _ := db.PautaDay(ctx, pub, day); row == nil || row.EmailedAt == "" {
		t.Fatal("skipped day was not stamped")
	}

	// A new story in the next snapshot makes the mail go out again.
	fresh := "[" + pautaMailCard(1, "A", "X", "u:a") + "," + pautaMailCard(3, "C", "Z", "u:c") + "]"
	if err := db.SavePautaDay(ctx, pub, day, fresh); err != nil {
		t.Fatalf("fresh pauta: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE pauta_days SET emailed_at='' WHERE workspace_id=$1 AND publication_id=$2 AND day=$3`,
		db.Workspace(), pub, day); err != nil {
		t.Fatalf("unstamp: %v", err)
	}
	srv.DailyPautaTick(ctx, pool, now)
	n := 0
	for _, m := range fake.messages() {
		for _, to := range m.To {
			if to == owner {
				n++
			}
		}
	}
	if n != 1 {
		t.Fatalf("mails after new story = %d, want 1", n)
	}
}

// TestDailyPautaMailLoginPriority (KLOX-256 / D33): magic links keep
// the full daily cap — the morning mail yields the last quarter as
// sign-in headroom, so a saturated mail day refuses the alert but
// never the sign-in link. The shared test pool accumulates sends, so
// the cap is set relative to today's count: with three more sends,
// the day must be over the bulk-mail ceiling yet still inside the
// login one.
func TestDailyPautaMailLoginPriority(t *testing.T) {
	ctx := context.Background()
	probe := testdb.Pool(t)
	already, err := probe.SendsTodayAll(ctx, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		t.Fatalf("SendsTodayAll: %v", err)
	}
	fake := &fakeSender{}
	srv, pool, mgr := pautaHostedCfg(t, fake, func(c *config.Config) {
		c.EmailDailyCap = already + 4
	})
	now := spAt(2026, 12, 4, 7, 5)
	day := spDayOf(now)

	cookie, db := pautaVisitor(t, mgr)
	pautaRadar(t, srv, cookie)
	pub := mustPubID(t, db)
	owner := fmt.Sprintf("prio-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE workspaces SET last_seen_at = $1 WHERE id = $2`,
		store.TSUTC(now.Add(-time.Hour)), db.Workspace()); err != nil {
		t.Fatalf("touch workspace: %v", err)
	}
	// Three real sends: past the bulk ceiling (cap minus its quarter,
	// here already+3) yet under the login cap itself (already+4).
	for i := 0; i < 3; i++ {
		if _, _, err := db.SaveMail(ctx, "issue", "r@x.dev", "s", "", "", "resend", "sent",
			fmt.Sprintf("prio-seed-%s-%d", owner, i)); err != nil {
			t.Fatalf("seed send %d: %v", i, err)
		}
	}
	if err := db.SavePautaDay(ctx, pub, day,
		"["+pautaMailCard(1, "A", "X", "u:a")+"]"); err != nil {
		t.Fatalf("today pauta: %v", err)
	}
	srv.DailyPautaTick(ctx, pool, now)
	for _, m := range fake.messages() {
		t.Fatalf("over-cap pauta mail left the app: %v", m.Subject)
	}
	refused := false
	for _, m := range mustOutbox(t, db) {
		if m.Kind == "pauta" && m.Status == "refused" {
			refused = true
		}
	}
	if !refused {
		t.Fatal("pauta mail was not refused under the bulk ceiling")
	}
	if row, _ := db.PautaDay(ctx, pub, day); row == nil || row.EmailedAt == "" {
		t.Fatal("refused day was not stamped")
	}

	// The same saturated day still delivers a magic link — login wins.
	cookieB, _ := pautaVisitor(t, mgr)
	email := fmt.Sprintf("login-%d@hackbrazil.dev", time.Now().UnixNano())
	rr := pautaServe(t, srv, "POST", "/api/v1/workspace/claim", cookieB, map[string]string{"email": email})
	if rr.Code != 200 {
		t.Fatalf("claim = %d %s", rr.Code, rr.Body.String())
	}
	res := apiJSON(t, rr)
	if res["sent"] != true {
		t.Fatalf("magic link refused under the login cap: %v", res)
	}
	sent := false
	for _, m := range fake.messages() {
		if m.Kind == "magic_link" {
			sent = true
		}
	}
	if !sent {
		t.Fatal("magic link did not reach the sender")
	}
}

// TestDailyPautaMailLang (KLOX-258): the daily mail speaks the
// account's language — an EN account gets the English subject,
// greeting, button and link notice with "&lang=en" on the sign-in
// link; a PT account keeps the Portuguese copy.
func TestDailyPautaMailLang(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)

	ownerEN := fmt.Sprintf("en-%d@hackbrazil.dev", time.Now().UnixNano())
	cookieEN, dbEN := pautaVisitor(t, mgr)
	pautaRadar(t, srv, cookieEN)
	if err := dbEN.SetUILang(ctx, "en"); err != nil {
		t.Fatalf("set lang: %v", err)
	}
	if err := pool.ClaimWorkspace(ctx, dbEN.Workspace(), ownerEN); err != nil {
		t.Fatalf("claim en: %v", err)
	}

	ownerPT := fmt.Sprintf("pt-%d@hackbrazil.dev", time.Now().UnixNano())
	cookiePT, dbPT := pautaVisitor(t, mgr)
	pautaRadar(t, srv, cookiePT)
	if err := dbPT.SetUILang(ctx, "pt"); err != nil {
		t.Fatalf("set lang: %v", err)
	}
	if err := pool.ClaimWorkspace(ctx, dbPT.Workspace(), ownerPT); err != nil {
		t.Fatalf("claim pt: %v", err)
	}

	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 7, 5))

	mailTo := func(owner string) *delivery.Message {
		for _, m := range fake.messages() {
			for _, to := range m.To {
				if to == owner {
					return m
				}
			}
		}
		return nil
	}
	en := mailTo(ownerEN)
	if en == nil || en.Subject != "Your stories for today are ready" {
		t.Fatalf("EN mail = %+v", en)
	}
	for _, want := range []string{
		"Good morning! Today's stories for your Radar",
		"[Open my stories](",
		"is valid for 72 hours",
		"&lang=en",
	} {
		if !strings.Contains(en.Text, want) {
			t.Fatalf("EN body lacks %q:\n%s", want, en.Text)
		}
	}
	pt := mailTo(ownerPT)
	if pt == nil || pt.Subject != "Suas notícias de hoje estão prontas" {
		t.Fatalf("PT mail = %+v", pt)
	}
	for _, want := range []string{
		"Bom dia! As notícias do seu radar",
		"[Abrir minhas notícias](",
		"vale por 72 horas",
		"&lang=pt",
	} {
		if !strings.Contains(pt.Text, want) {
			t.Fatalf("PT body lacks %q:\n%s", want, pt.Text)
		}
	}
	if !strings.Contains(en.HTML, "Create a post with this story →") ||
		!strings.Contains(en.HTML, "Unsubscribe from this Radar") ||
		!strings.Contains(en.HTML, `lang="en"`) {
		t.Fatal("EN HTML is not the English Radar template")
	}
	if !strings.Contains(pt.HTML, "Criar post com essa notícia →") ||
		!strings.Contains(pt.HTML, "Descadastrar deste Radar") ||
		!strings.Contains(pt.HTML, `lang="pt-BR"`) {
		t.Fatal("PT HTML is not the Portuguese Radar template")
	}
}

// TestDailyPautaMailNoNews (KLOX-256/258): a radar whose built pauta
// has no stories sends nothing — the day is stamped handled and the
// owner's daily claim stays free for another of their radars.
func TestDailyPautaMailNoNews(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)
	now := spAt(2026, 11, 24, 7, 5)
	day := spDayOf(now)

	cookie, db := pautaVisitor(t, mgr)
	pub := pautaRadar(t, srv, cookie)
	owner := fmt.Sprintf("nonews-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// The 06:30 build ran and ranked nothing — an empty snapshot.
	if err := db.SavePautaDay(ctx, pub, day, `[]`); err != nil {
		t.Fatalf("save empty pauta: %v", err)
	}

	srv.DailyPautaTick(ctx, pool, now)

	for _, m := range dailyMailTo(fake, owner) {
		t.Fatalf("mail sent with no stories: %v", m.Subject)
	}
	if row, _ := db.PautaDay(ctx, pub, day); row == nil || row.EmailedAt == "" {
		t.Fatal("empty pauta never stamped handled")
	}
	// The daily claim was not spent: the next day the same owner can
	// still be mailed once real stories land.
	if ok, err := pool.ClaimPautaMail(ctx, owner, day); err != nil || !ok {
		t.Fatalf("empty pauta spent the owner's daily claim (ok=%v, err=%v)", ok, err)
	}
}

// TestDailyPautaMailUnsubscribed (KLOX-258): an address that opted out
// is never mailed — the send is refused into the ledger and the day is
// still stamped handled, today and tomorrow. The one-click endpoint
// and account toggle land with KLOX-257; the suppression list is the
// opt-out seam the sweep already honors.
func TestDailyPautaMailUnsubscribed(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)
	day := spDayOf(spAt(2026, 11, 24, 7, 5))

	cookie, db := pautaVisitor(t, mgr)
	pub := pautaRadar(t, srv, cookie)
	owner := fmt.Sprintf("unsub-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := pool.SuppressEmail(ctx, owner, "unsubscribed"); err != nil {
		t.Fatalf("suppress: %v", err)
	}

	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 24, 7, 5))
	srv.DailyPautaTick(ctx, pool, spAt(2026, 11, 25, 7, 5))

	for _, m := range fake.messages() {
		for _, to := range m.To {
			if to == owner {
				t.Fatalf("suppressed owner mailed: %v", m.Subject)
			}
		}
	}
	if row, _ := db.PautaDay(ctx, pub, day); row == nil || row.EmailedAt == "" {
		t.Fatal("suppressed radar's day never stamped handled")
	}
	mails, err := db.OutboxEmails(ctx)
	if err != nil {
		t.Fatalf("OutboxEmails: %v", err)
	}
	refused := 0
	for _, m := range mails {
		if m.Kind == "pauta" && m.To == owner && m.Status == "refused" {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("no refused pauta mail in the ledger for the suppressed owner")
	}
}

// TestDailyPautaMailSaoPauloSchedule (KLOX-268): the schedule is read
// in America/Sao_Paulo, not UTC — 09:59 UTC is still before the mail
// window and 10:00 UTC is exactly 07:00 in São Paulo.
func TestDailyPautaMailSaoPauloSchedule(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)
	day := "2026-11-24"

	cookie, db := pautaVisitor(t, mgr)
	pub := pautaRadar(t, srv, cookie)
	owner := fmt.Sprintf("schedule-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := db.SavePautaDay(ctx, pub, day,
		"["+pautaMailCard(1, "A", "X", "u:a")+"]"); err != nil {
		t.Fatalf("today pauta: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE workspaces SET last_seen_at = $1 WHERE id = $2`,
		"2026-11-24 09:30:00", db.Workspace()); err != nil {
		t.Fatalf("touch workspace: %v", err)
	}

	srv.DailyPautaTick(ctx, pool, time.Date(2026, 11, 24, 9, 59, 0, 0, time.UTC)) // 06:59 SP
	if n := len(dailyMailTo(fake, owner)); n != 0 {
		t.Fatalf("mails before 07:00 SP = %d, want 0", n)
	}
	srv.DailyPautaTick(ctx, pool, time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)) // 07:00 SP
	if n := len(dailyMailTo(fake, owner)); n != 1 {
		t.Fatalf("mails at 07:00 SP = %d, want 1", n)
	}
}

// TestDailyPautaMailNoEveningCatchup (KLOX-268): a deploy or restart
// hours after the window must not turn the first tick into a 22:15
// "morning" mail. The day is left unstamped, not marked as handled.
func TestDailyPautaMailNoEveningCatchup(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)
	day := "2026-11-24"

	cookie, db := pautaVisitor(t, mgr)
	pub := pautaRadar(t, srv, cookie)
	owner := fmt.Sprintf("catchup-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, db.Workspace(), owner); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := db.SavePautaDay(ctx, pub, day,
		"["+pautaMailCard(1, "A", "X", "u:a")+"]"); err != nil {
		t.Fatalf("today pauta: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE workspaces SET last_seen_at = $1 WHERE id = $2`,
		"2026-11-25 00:30:00", db.Workspace()); err != nil {
		t.Fatalf("touch workspace: %v", err)
	}

	// 01:15 UTC on the 25th is 22:15 in São Paulo on the 24th — the
	// exact shape of the reported deploy-time catch-up.
	srv.DailyPautaTick(ctx, pool, time.Date(2026, 11, 25, 1, 15, 0, 0, time.UTC))
	if n := len(dailyMailTo(fake, owner)); n != 0 {
		t.Fatalf("evening catch-up mails = %d, want 0", n)
	}
	row, err := db.PautaDay(ctx, pub, day)
	if err != nil {
		t.Fatalf("pauta day: %v", err)
	}
	if row.EmailedAt != "" {
		t.Fatal("evening tick stamped the mail as handled")
	}
}

// TestDailyPautaMailScopeActiveWorkspaces (KLOX-268): the mail pass is
// bounded to workspaces visited inside the 7-day window even when a
// stale workspace already has a stored pauta waiting.
func TestDailyPautaMailScopeActiveWorkspaces(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
	srv, pool, mgr := pautaHosted(t, fake)
	now := spAt(2026, 11, 24, 7, 5)
	day := spDayOf(now)
	cards := "[" + pautaMailCard(1, "A", "X", "u:a") + "]"

	activeCookie, activeDB := pautaVisitor(t, mgr)
	activePub := pautaRadar(t, srv, activeCookie)
	activeOwner := fmt.Sprintf("active-scope-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, activeDB.Workspace(), activeOwner); err != nil {
		t.Fatalf("claim active: %v", err)
	}
	if err := activeDB.SavePautaDay(ctx, activePub, day, cards); err != nil {
		t.Fatalf("active pauta: %v", err)
	}
	if _, err := activeDB.ExecContext(ctx,
		`UPDATE workspaces SET last_seen_at = $1 WHERE id = $2`,
		store.TSUTC(now.Add(-time.Hour)), activeDB.Workspace()); err != nil {
		t.Fatalf("touch active workspace: %v", err)
	}

	staleCookie, staleDB := pautaVisitor(t, mgr)
	stalePub := pautaRadar(t, srv, staleCookie)
	staleOwner := fmt.Sprintf("stale-scope-%d@hackbrazil.dev", time.Now().UnixNano())
	if err := pool.ClaimWorkspace(ctx, staleDB.Workspace(), staleOwner); err != nil {
		t.Fatalf("claim stale: %v", err)
	}
	if err := staleDB.SavePautaDay(ctx, stalePub, day, cards); err != nil {
		t.Fatalf("stale pauta: %v", err)
	}
	if _, err := staleDB.ExecContext(ctx,
		`UPDATE workspaces SET last_seen_at = $1 WHERE id = $2`,
		store.TSUTC(now.Add(-8*24*time.Hour)), staleDB.Workspace()); err != nil {
		t.Fatalf("age stale workspace: %v", err)
	}

	srv.DailyPautaTick(ctx, pool, now)
	if n := len(dailyMailTo(fake, activeOwner)); n != 1 {
		t.Fatalf("active workspace mails = %d, want 1", n)
	}
	if n := len(dailyMailTo(fake, staleOwner)); n != 0 {
		t.Fatalf("stale workspace mails = %d, want 0", n)
	}
}
