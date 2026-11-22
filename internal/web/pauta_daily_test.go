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
	"net/http/httptest"
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

func spAt(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, spZone)
}

func spDayOf(t time.Time) string { return t.In(spZone).Format("2006-01-02") }

// pautaHosted is a hosted (cookie-per-visitor) demo server on the
// shared pool, with the fixture fetcher so radars hold real stories.
func pautaHosted(t *testing.T, mailer delivery.Sender) (*web.Server, *store.Pool, *workspace.Manager) {
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
	srv := web.New(web.Deps{
		WS: mgr, Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm", DumpDir: llmDumpDir("")},
		Monitor: sources.FakeMonitor{}, Scraper: sources.FakeScraper{},
		Mailer: mailer, Seed: seed, Pool: pool,
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
	if msg.Subject != "Sua pauta de hoje está pronta" || msg.Kind != "pauta" {
		t.Fatalf("mail = %q kind %q", msg.Subject, msg.Kind)
	}
	if !strings.Contains(msg.Text, "/claim?t=") {
		t.Fatalf("mail has no sign-in link: %s", msg.Text)
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
