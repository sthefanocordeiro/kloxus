package web_test

// R4c acceptance tests (D33): claim by magic link, confirmed-only
// sends, persisted caps, idempotency, outbox fallback, the Resend
// webhook and one-click unsubscribe — all against a fake sender (and a
// fake Resend HTTP server where the SDK path matters).
import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

// --- fakes & helpers ---

// fakeSender is a delivery.Sender that records messages (and can fail).
type fakeSender struct {
	mu   sync.Mutex
	msgs []*delivery.Message
	err  error
	n    int
}

func (f *fakeSender) Send(_ context.Context, m *delivery.Message) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.msgs = append(f.msgs, m)
	f.n++
	return fmt.Sprintf("re_%d", f.n), nil
}

func (f *fakeSender) messages() []*delivery.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*delivery.Message(nil), f.msgs...)
}

// liveDeps wires a local-mode server whose mail is "live" — a fake
// sender stands in for Resend. Local mode keeps every rule (claim,
// caps, idempotency) without the cookie dance.
func liveDeps(t *testing.T, mailer delivery.Sender, tweak func(*config.Config)) (web.Deps, *store.DB, *store.Publication, *config.Config) {
	t.Helper()
	ctx := context.Background()
	base := t.TempDir()
	cfg := &config.Config{
		Mode: "demo", Port: 8080,
		OutDir:      base + "/out",
		ModelTriage: "claude-haiku-5-5", ModelDraft: "claude-sonnet-5-5", MaxDeepDives: 3,
		EmailDailyCap:          80,
		EmailWorkspaceDailyCap: 25,
		ResendWebhookSecret:    "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-webhook-secret-32-bytes!!")),
	}
	if tweak != nil {
		tweak(cfg)
	}
	db := testdb.WS(t)
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
	if err := seed(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pubs, err := db.ListPublications(ctx)
	if err != nil || len(pubs) == 0 {
		t.Fatalf("ListPublications: %v", err)
	}
	deps := web.Deps{
		WS: workspace.OpenLocal(db, seed), Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm"},
		Monitor: sources.FakeMonitor{},
		Mailer:  mailer, Seed: seed,
	}
	return deps, db, pubs[0], cfg
}

// approvedIssue writes one approved issue straight to the store — the
// send rules being tested don't need the full draft pipeline.
func approvedIssue(t *testing.T, db *store.DB, pubID int64, num int, subject string) int64 {
	t.Helper()
	body := "# " + subject + "\n\n## Section\n\n### Item\n\nA short sourced sentence [1]."
	iid, err := db.CreateIssue(context.Background(), pubID, num, subject, body)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := db.UpdateIssue(context.Background(), iid, "approved", body, "[]"); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	return iid
}

var claimTokenRe = regexp.MustCompile(`/claim\?t=([A-Za-z0-9_-]+)`)
var confirmLinkRe = regexp.MustCompile(`/confirm-sub\?[^\s"'\])]+`)

// mailToken digs a magic-link token out of the ledger — the email body
// is the only place the plaintext token exists.
func claimToken(t *testing.T, db *store.DB) string {
	t.Helper()
	mails, err := db.OutboxEmails(context.Background())
	if err != nil {
		t.Fatalf("OutboxEmails: %v", err)
	}
	for _, m := range mails {
		if m.Kind != "magic_link" {
			continue
		}
		full, err := db.GetMail(context.Background(), m.ID)
		if err != nil {
			t.Fatalf("GetMail: %v", err)
		}
		if mm := claimTokenRe.FindStringSubmatch(full.BodyMD); mm != nil {
			return mm[1]
		}
	}
	t.Fatal("no magic-link email in the ledger")
	return ""
}

// claimWorkspace drives the whole claim in local mode and returns the
// owner email — subsequent calls build on it.
func claimWorkspace(t *testing.T, srv *web.Server, db *store.DB, email string) {
	t.Helper()
	rr := apiCall(t, srv, "POST", "/api/v1/workspace/claim", map[string]string{"email": email})
	if rr.Code != 200 {
		t.Fatalf("claim = %d %s", rr.Code, rr.Body.String()[:300])
	}
	tok := claimToken(t, db)
	rr = apiCall(t, srv, "GET", "/claim?t="+tok, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("GET /claim = %d, want 303", rr.Code)
	}
}

// apiCallHdr is apiCall with extra request headers (Idempotency-Key).
func apiCallHdr(t *testing.T, srv *web.Server, method, path string, body any, hdr http.Header) *httptest.ResponseRecorder {
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
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// svixRequest builds a signed webhook request the way Resend/Svix sends it.
func svixRequest(t *testing.T, srv *web.Server, secret string, body []byte, badSig bool) *httptest.ResponseRecorder {
	t.Helper()
	id := "msg_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		t.Fatalf("decode webhook secret: %v", err)
	}
	h := hmac.New(sha256.New, key)
	fmt.Fprintf(h, "%s.%s.", id, ts)
	h.Write(body)
	sig := base64.StdEncoding.EncodeToString(h.Sum(nil))
	if badSig {
		sig = base64.StdEncoding.EncodeToString([]byte("forged"))
	}
	req := httptest.NewRequest("POST", "/webhooks/resend", strings.NewReader(string(body)))
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", "v1,"+sig)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// --- tests ---

// TestLiveSendNeedsClaimAndTestGoesToOwner covers the gate: sends fail
// while the workspace is anonymous; once claimed the test send reaches
// the owner only — and a reused claim link is rejected.
func TestLiveSendNeedsClaimAndTestGoesToOwner(t *testing.T) {
	fake := &fakeSender{}
	deps, db, pub, _ := liveDeps(t, fake, nil)
	srv := web.New(deps)
	owner := "criador@hackbrazil.dev"
	issueID := approvedIssue(t, db, pub.ID, 1, "Edição 1")
	sendURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10) + "/send"

	for _, aud := range []string{"test", "readers"} {
		rr := apiCall(t, srv, "POST", sendURL, map[string]string{"audience": aud})
		if rr.Code != 409 || apiErrCode(t, rr) != "unclaimed_workspace" {
			t.Fatalf("send %s unclaimed = %d %s, want 409 unclaimed_workspace", aud, rr.Code, rr.Body.String()[:200])
		}
	}

	claimWorkspace(t, srv, db, owner)
	ws := apiJSON(t, apiCall(t, srv, "GET", "/api/v1/workspace", nil))
	if ws["claimed"] != true || ws["owner_email"] != owner {
		t.Fatalf("workspace after claim = %v", ws)
	}
	// the claim itself went out by email — never to anyone but the owner
	msgs := fake.messages()
	if len(msgs) != 1 || msgs[0].To[0] != owner || msgs[0].Kind != "magic_link" {
		t.Fatalf("claim email = %+v", msgs)
	}

	// a test send goes to the claimed address only
	rr := apiCall(t, srv, "POST", sendURL, map[string]string{"audience": "test"})
	if rr.Code != 200 {
		t.Fatalf("test send = %d %s", rr.Code, rr.Body.String()[:300])
	}
	msgs = fake.messages()
	last := msgs[len(msgs)-1]
	if last.To[0] != owner {
		t.Fatalf("test send went to %q, want owner %q", last.To[0], owner)
	}
	// an explicit different address is rejected
	rr = apiCall(t, srv, "POST", sendURL, map[string]string{"audience": "test", "to": "other@x.dev"})
	if rr.Code != 400 {
		t.Fatalf("test send to other = %d, want 400", rr.Code)
	}

	// the same claim link cannot be spent twice
	tok := claimToken(t, db)
	if rr := apiCall(t, srv, "GET", "/claim?t="+tok, nil); rr.Code != 400 {
		t.Fatalf("reused claim link = %d, want 400", rr.Code)
	}
}

// TestClaimLinkCanonicalAndSlashTolerant reproduces KLOX-27: an
// emailed link must point at the configured public origin — not at
// whatever host the request happened to arrive on — and a stray
// trailing slash on it must reach the handler instead of falling
// through to the SPA's 404.
func TestClaimLinkCanonicalAndSlashTolerant(t *testing.T) {
	fake := &fakeSender{}
	deps, db, pub, _ := liveDeps(t, fake, func(c *config.Config) {
		c.PublicURL = "https://kloxus.fly.dev/"
	})
	srv := web.New(deps)
	ctx := context.Background()

	rr := apiCall(t, srv, "POST", "/api/v1/workspace/claim", map[string]string{"email": "link@x.dev"})
	if rr.Code != 200 {
		t.Fatalf("claim = %d %s", rr.Code, rr.Body.String()[:300])
	}
	msgs := fake.messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "https://kloxus.fly.dev/claim?t=") {
		t.Fatalf("claim link did not use the public URL: %+v", msgs)
	}

	// a stray trailing slash on the emailed link spends the token
	// exactly like the bare path — previously this 404'd in the SPA
	tok := claimToken(t, db)
	if rr := apiCall(t, srv, "GET", "/claim/?t="+tok, nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("GET /claim/?t= = %d, want 303", rr.Code)
	}
	if ws := apiJSON(t, apiCall(t, srv, "GET", "/api/v1/workspace", nil)); ws["claimed"] != true {
		t.Fatalf("workspace not claimed through the slash link: %v", ws)
	}
	// single-use still holds on the slash variant
	if rr := apiCall(t, srv, "GET", "/claim/?t="+tok, nil); rr.Code != 400 {
		t.Fatalf("reused slash claim link = %d, want 400", rr.Code)
	}

	// the reader confirm link is canonical too, and slash-tolerant
	rr = apiCall(t, srv, "POST", "/api/v1/p/"+pub.Slug+"/subscribe", map[string]string{"email": "r@x.dev"})
	if rr.Code != 200 {
		t.Fatalf("subscribe = %d %s", rr.Code, rr.Body.String()[:300])
	}
	mails, _ := db.OutboxEmails(ctx)
	var confirmURL string
	for _, m := range mails {
		if m.Kind != "reader_confirm" {
			continue
		}
		full, _ := db.GetMail(ctx, m.ID)
		if !strings.Contains(full.BodyMD, "https://kloxus.fly.dev/confirm-sub?") {
			t.Fatalf("confirm link did not use the public URL: %s", full.BodyMD)
		}
		if mm := confirmLinkRe.FindString(full.BodyMD); mm != "" {
			confirmURL = mm
		}
	}
	if confirmURL == "" {
		t.Fatal("no confirm link found")
	}
	slashURL := strings.Replace(confirmURL, "/confirm-sub", "/confirm-sub/", 1)
	if rr := apiCall(t, srv, "GET", slashURL, nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("GET /confirm-sub/ = %d, want 303", rr.Code)
	}
	if rd, _ := db.ReaderByEmail(ctx, pub.ID, "r@x.dev"); rd == nil || !rd.Confirmed {
		t.Fatal("reader not confirmed through the slash link")
	}

	// RFC 8058 one-click unsubscribe survives a stray slash as well
	rid, err := db.UpsertReader(ctx, pub.ID, "oneclick@x.dev", "")
	if err != nil {
		t.Fatalf("UpsertReader: %v", err)
	}
	unsubPath := fmt.Sprintf("/unsub/?pub=%d&r=%d&t=%s",
		pub.ID, rid, deps.WS.MAC("unsub", fmt.Sprintf("%d:%d", pub.ID, rid)))
	req := httptest.NewRequest("POST", unsubPath, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req)
	if rr2.Code != 200 {
		t.Fatalf("POST /unsub/ = %d %s", rr2.Code, rr2.Body.String()[:200])
	}
	if rd, _ := db.ReaderByEmail(ctx, pub.ID, "oneclick@x.dev"); rd == nil || !rd.Unsubscribed {
		t.Fatal("reader not unsubscribed through the slash link")
	}
}

// TestClaimLinkExpiryAndClaimRateLimit: an expired token is rejected,
// and minting stays bounded per workspace per hour.
func TestClaimLinkExpiryAndClaimRateLimit(t *testing.T) {
	fake := &fakeSender{}
	deps, db, _, _ := liveDeps(t, fake, nil)
	srv := web.New(deps)
	ctx := context.Background()

	if err := db.SaveMagicLink(ctx, "deadbeef", "a@b.dev",
		store.TSUTC(time.Now().Add(-time.Minute))); err != nil {
		t.Fatalf("SaveMagicLink: %v", err)
	}
	if rr := apiCall(t, srv, "GET", "/claim?t=deadbeef", nil); rr.Code != 400 {
		t.Fatalf("expired claim = %d, want 400", rr.Code)
	}

	// five links an hour — the expired one above already counts, so four
	// more fit and the fifth mint is rate-limited.
	for i := 0; i < 4; i++ {
		rr := apiCall(t, srv, "POST", "/api/v1/workspace/claim",
			map[string]string{"email": fmt.Sprintf("c%d@x.dev", i)})
		if rr.Code != 200 {
			t.Fatalf("claim %d = %d", i, rr.Code)
		}
	}
	rr := apiCall(t, srv, "POST", "/api/v1/workspace/claim", map[string]string{"email": "c6@x.dev"})
	if rr.Code != 429 {
		t.Fatalf("5th claim = %d, want 429", rr.Code)
	}
}

// TestReadersDoubleOptInAndSend covers the reader rules: nothing is
// mailed until confirm; after confirm the issue mails; after
// unsubscribe it stops.
func TestReadersDoubleOptInAndSend(t *testing.T) {
	fake := &fakeSender{}
	deps, db, pub, _ := liveDeps(t, fake, nil)
	srv := web.New(deps)
	owner := "owner@hackbrazil.dev"
	claimWorkspace(t, srv, db, owner)
	ctx := context.Background()

	// two readers subscribe — each gets a confirm email, nothing else
	for _, em := range []string{"reader1@x.dev", "reader2@x.dev"} {
		rr := apiCall(t, srv, "POST", "/api/v1/p/"+pub.Slug+"/subscribe", map[string]string{"email": em})
		if rr.Code != 200 {
			t.Fatalf("subscribe %s = %d %s", em, rr.Code, rr.Body.String()[:300])
		}
	}
	if got := len(fake.messages()); got != 3 { // claim + 2 confirms
		t.Fatalf("mails after subscribe = %d, want 3", got)
	}

	// an unconfirmed reader gets no issue
	issueID := approvedIssue(t, db, pub.ID, 1, "Edição 1")
	rr := apiCall(t, srv, "POST", "/api/v1/issues/"+strconv.FormatInt(issueID, 10)+"/send",
		map[string]string{"audience": "readers"})
	if rr.Code != 200 {
		t.Fatalf("send readers = %d", rr.Code)
	}
	res := apiJSON(t, rr)
	if res["sent"].(float64) != 0 {
		t.Fatalf("sent = %v, want 0 (no confirmed readers)", res["sent"])
	}

	// confirm reader1 via the link in their confirm email
	mails, _ := db.OutboxEmails(ctx)
	var confirmURL string
	for _, m := range mails {
		if m.Kind != "reader_confirm" || m.To != "reader1@x.dev" {
			continue
		}
		full, _ := db.GetMail(ctx, m.ID)
		if mm := confirmLinkRe.FindString(full.BodyMD); mm != "" {
			confirmURL = mm
		}
	}
	if confirmURL == "" {
		t.Fatal("no confirm link found for reader1")
	}
	if rr := apiCall(t, srv, "GET", confirmURL, nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("confirm-sub = %d, want 303", rr.Code)
	}
	if rd, _ := db.ReaderByEmail(ctx, pub.ID, "reader1@x.dev"); rd == nil || !rd.Confirmed {
		t.Fatal("reader1 not confirmed after the link")
	}

	// issue 2 mails exactly the confirmed reader (+ owner if confirmed —
	// claim didn't confirm them here)
	issueID2 := approvedIssue(t, db, pub.ID, 2, "Edição 2")
	before := len(fake.messages())
	rr = apiCall(t, srv, "POST", "/api/v1/issues/"+strconv.FormatInt(issueID2, 10)+"/send",
		map[string]string{"audience": "readers"})
	if rr.Code != 200 {
		t.Fatalf("send readers = %d %s", rr.Code, rr.Body.String()[:300])
	}
	newMsgs := fake.messages()[before:]
	if len(newMsgs) != 1 || newMsgs[0].To[0] != "reader1@x.dev" {
		t.Fatalf("issue mails = %v, want one to reader1", tos(newMsgs))
	}
	if newMsgs[0].Headers["List-Unsubscribe"] == "" || newMsgs[0].Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("unsubscribe headers missing: %v", newMsgs[0].Headers)
	}

	// one-click unsubscribe: POST to the List-Unsubscribe URL
	unsubFull := regexp.MustCompile(`<(.*)>`).FindStringSubmatch(newMsgs[0].Headers["List-Unsubscribe"])[1]
	unsubPath := unsubFull[strings.Index(unsubFull, "/unsub"):]
	req := httptest.NewRequest("POST", unsubPath, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req)
	if rr2.Code != 200 {
		t.Fatalf("one-click unsub = %d %s", rr2.Code, rr2.Body.String()[:200])
	}
	if rd, _ := db.ReaderByEmail(ctx, pub.ID, "reader1@x.dev"); rd == nil || !rd.Unsubscribed {
		t.Fatal("reader1 not unsubscribed after one-click")
	}

	// a third issue mails nobody (reader2 still unconfirmed, reader1 out)
	issueID3 := approvedIssue(t, db, pub.ID, 3, "Edição 3")
	before = len(fake.messages())
	apiCall(t, srv, "POST", "/api/v1/issues/"+strconv.FormatInt(issueID3, 10)+"/send",
		map[string]string{"audience": "readers"})
	if n := len(fake.messages()) - before; n != 0 {
		t.Fatalf("post-unsub send mailed %d messages, want 0", n)
	}
}

func tos(msgs []*delivery.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.To...)
	}
	return out
}

// TestIdempotentSend: one idempotency key, one email — across retries.
func TestIdempotentSend(t *testing.T) {
	fake := &fakeSender{}
	deps, db, pub, _ := liveDeps(t, fake, nil)
	srv := web.New(deps)
	owner := "idem@hackbrazil.dev"
	claimWorkspace(t, srv, db, owner)
	issueID := approvedIssue(t, db, pub.ID, 1, "Edição 1")
	sendURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10) + "/send"

	before := len(fake.messages())
	hdr := http.Header{"Idempotency-Key": {"try-123"}}
	for i := 0; i < 2; i++ {
		rr := apiCallHdr(t, srv, "POST", sendURL, map[string]string{"audience": "test"}, hdr)
		if rr.Code != 200 {
			t.Fatalf("send %d = %d %s", i, rr.Code, rr.Body.String()[:300])
		}
	}
	if n := len(fake.messages()) - before; n != 1 {
		t.Fatalf("same Idempotency-Key sent %d emails, want 1", n)
	}
	mails, _ := db.OutboxEmails(context.Background())
	sends := 0
	for _, m := range mails {
		if m.Kind == "test" {
			sends++
		}
	}
	if sends != 1 {
		t.Fatalf("ledger holds %d test mails, want 1", sends)
	}
}

// TestFallbackToOutbox: a dead provider keeps the message as an outbox
// copy and the response tells the creator.
func TestFallbackToOutbox(t *testing.T) {
	fake := &fakeSender{}
	deps, db, pub, _ := liveDeps(t, fake, nil)
	srv := web.New(deps)
	owner := "fallback@hackbrazil.dev"
	// claim while the provider is up
	claimWorkspace(t, srv, db, owner)
	issueID := approvedIssue(t, db, pub.ID, 1, "Edição 1")

	fake.err = errors.New("resend api: 503 backend down")
	rr := apiCall(t, srv, "POST", "/api/v1/issues/"+strconv.FormatInt(issueID, 10)+"/send",
		map[string]string{"audience": "test"})
	if rr.Code != 200 {
		t.Fatalf("send = %d %s", rr.Code, rr.Body.String()[:300])
	}
	res := apiJSON(t, rr)
	if res["fallback"] != true || res["delivery"] != "outbox_fallback" {
		t.Fatalf("response = %v, want fallback delivery", res)
	}
	id := int64(res["outbox_email_id"].(float64))
	mail, err := db.GetMail(context.Background(), id)
	if err != nil {
		t.Fatalf("GetMail: %v", err)
	}
	if mail.Status != "fallback" || mail.Provider != "resend" {
		t.Fatalf("ledger = %s/%s, want fallback/resend", mail.Status, mail.Provider)
	}
	if strings.Contains(mail.Error, "re_test") || strings.Contains(mail.Error, "Bearer") {
		t.Fatalf("stored error leaks the api key: %q", mail.Error)
	}
}

// TestFallbackRetrySends: a fallback row never left the app, so it must
// not satisfy idempotency — the retry requeues the same ledger row and
// really reaches the provider. A delivered send still dedupes.
func TestFallbackRetrySends(t *testing.T) {
	fake := &fakeSender{}
	deps, db, pub, _ := liveDeps(t, fake, nil)
	srv := web.New(deps)
	claimWorkspace(t, srv, db, "retry@hackbrazil.dev")
	issueID := approvedIssue(t, db, pub.ID, 1, "Edição 1")
	sendURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10) + "/send"

	fake.err = errors.New("resend api: 503 backend down")
	rr := apiCall(t, srv, "POST", sendURL, map[string]string{"audience": "test"})
	if rr.Code != 200 {
		t.Fatalf("send = %d %s", rr.Code, rr.Body.String()[:300])
	}
	res := apiJSON(t, rr)
	if res["delivery"] != "outbox_fallback" {
		t.Fatalf("failed send delivery = %v, want outbox_fallback", res["delivery"])
	}
	id := int64(res["outbox_email_id"].(float64))
	fake.err = nil

	before := len(fake.messages())
	rr = apiCall(t, srv, "POST", sendURL, map[string]string{"audience": "test"})
	if rr.Code != 200 {
		t.Fatalf("retry = %d %s", rr.Code, rr.Body.String()[:300])
	}
	res = apiJSON(t, rr)
	if res["delivery"] != "resend" || res["sent"].(float64) != 1 {
		t.Fatalf("retry = %v, want a real resend", res)
	}
	if int64(res["outbox_email_id"].(float64)) != id {
		t.Fatalf("retry wrote a new ledger row, want the fallback row %d requeued", id)
	}
	if n := len(fake.messages()) - before; n != 1 {
		t.Fatalf("provider got %d messages on retry, want 1", n)
	}
	mail, err := db.GetMail(context.Background(), id)
	if err != nil {
		t.Fatalf("GetMail: %v", err)
	}
	if mail.Status != "sent" || mail.Provider != "resend" || mail.ProviderID == "" || mail.Error != "" {
		t.Fatalf("ledger = %s/%s err=%q, want a clean sent resend row", mail.Status, mail.Provider, mail.Error)
	}

	// a delivered send still dedupes — idempotency itself is intact
	before = len(fake.messages())
	rr = apiCall(t, srv, "POST", sendURL, map[string]string{"audience": "test"})
	res = apiJSON(t, rr)
	if res["delivery"] != "none" || res["deduped"].(float64) != 1 {
		t.Fatalf("re-send after a success = %v, want deduped", res)
	}
	if n := len(fake.messages()) - before; n != 0 {
		t.Fatalf("provider got %d messages on a deduped retry, want 0", n)
	}
}

// TestFallbackIssueSendStaysApproved: a readers send where every
// message fell back delivered nothing, so the issue stays approved
// and the retry — requeuing the same ledger rows — can still go out.
func TestFallbackIssueSendStaysApproved(t *testing.T) {
	fake := &fakeSender{}
	deps, db, pub, _ := liveDeps(t, fake, nil)
	srv := web.New(deps)
	owner := fmt.Sprintf("fb-%d@hackbrazil.dev", time.Now().UnixNano())
	reader := fmt.Sprintf("rd-%d@x.dev", time.Now().UnixNano())
	claimWorkspace(t, srv, db, owner)
	subscribeAndConfirm(t, srv, db, pub, reader)
	issueID := approvedIssue(t, db, pub.ID, 1, "Edição 1")
	sendURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10) + "/send"

	fake.err = errors.New("resend api: 503 backend down")
	rr := apiCall(t, srv, "POST", sendURL, map[string]string{"audience": "readers"})
	if rr.Code != 200 {
		t.Fatalf("send = %d %s", rr.Code, rr.Body.String()[:300])
	}
	res := apiJSON(t, rr)
	if res["delivery"] != "outbox_fallback" || res["warnings"] == nil {
		t.Fatalf("all-fallback send = %v, want outbox_fallback + warning", res)
	}
	iss, err := db.GetIssue(context.Background(), issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "approved" {
		t.Fatalf("issue status = %q, want approved — nothing left the app", iss.Status)
	}
	fake.err = nil

	before := len(fake.messages())
	rr = apiCall(t, srv, "POST", sendURL, map[string]string{"audience": "readers"})
	if rr.Code != 200 {
		t.Fatalf("retry = %d %s", rr.Code, rr.Body.String()[:300])
	}
	res = apiJSON(t, rr)
	if res["delivery"] != "resend" || res["sent"].(float64) != 1 {
		t.Fatalf("retry = %v, want a real resend", res)
	}
	if n := len(fake.messages()) - before; n != 1 {
		t.Fatalf("provider got %d messages on retry, want 1", n)
	}
	iss, err = db.GetIssue(context.Background(), issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "sent" {
		t.Fatalf("issue status = %q after a real resend, want sent", iss.Status)
	}
}

// TestAllRefusedSendStaysApproved: a readers send where every
// recipient is refused delivers nothing, so the issue stays approved
// and can be sent again once deliverable.
func TestAllRefusedSendStaysApproved(t *testing.T) {
	fake := &fakeSender{}
	deps, db, pub, _ := liveDeps(t, fake, nil)
	srv := web.New(deps)
	// run-unique addresses: suppressions are global and persist across
	// test runs on a shared database.
	owner := fmt.Sprintf("refused-%d@hackbrazil.dev", time.Now().UnixNano())
	reader := fmt.Sprintf("sup-%d@x.dev", time.Now().UnixNano())
	claimWorkspace(t, srv, db, owner)
	subscribeAndConfirm(t, srv, db, pub, reader)
	ctx := context.Background()
	if err := db.SuppressEmail(ctx, reader, "test"); err != nil {
		t.Fatalf("SuppressEmail: %v", err)
	}

	issueID := approvedIssue(t, db, pub.ID, 1, "Edição 1")
	rr := apiCall(t, srv, "POST", "/api/v1/issues/"+strconv.FormatInt(issueID, 10)+"/send",
		map[string]string{"audience": "readers"})
	if rr.Code != 200 {
		t.Fatalf("send = %d %s", rr.Code, rr.Body.String()[:300])
	}
	res := apiJSON(t, rr)
	if res["sent"].(float64) != 0 || res["refused"] == nil || res["delivery"] != "none" {
		t.Fatalf("all-refused send = %v, want sent:0 + refused + delivery:none", res)
	}
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Status != "approved" {
		t.Fatalf("issue status = %q, want approved — nothing left the app", iss.Status)
	}
	// and it can be sent again: the gate accepts approved
	rr = apiCall(t, srv, "POST", "/api/v1/issues/"+strconv.FormatInt(issueID, 10)+"/send",
		map[string]string{"audience": "test"})
	if rr.Code != 200 {
		t.Fatalf("resend = %d, want 200 — issue must still be sendable", rr.Code)
	}
}

// TestCapsPersistAcrossRestart: the workspace cap is ledger-derived, so
// a fresh Server over the same store enforces it identically.
func TestCapsPersistAcrossRestart(t *testing.T) {
	fake := &fakeSender{}
	deps, db, pub, _ := liveDeps(t, fake, func(c *config.Config) {
		c.EmailWorkspaceDailyCap = 2 // claim + one send
	})
	srv := web.New(deps)
	claimWorkspace(t, srv, db, "caps@hackbrazil.dev")
	issueID := approvedIssue(t, db, pub.ID, 1, "Edição 1")
	sendURL := "/api/v1/issues/" + strconv.FormatInt(issueID, 10) + "/send"

	rr := apiCall(t, srv, "POST", sendURL, map[string]string{"audience": "test"})
	if rr.Code != 200 {
		t.Fatalf("first send = %d %s", rr.Code, rr.Body.String()[:300])
	}
	// cap now spent — refuse loudly, never silently drop
	rr = apiCall(t, srv, "POST", sendURL, map[string]string{"audience": "test"})
	if rr.Code != 429 {
		t.Fatalf("over-cap send = %d %s, want 429", rr.Code, rr.Body.String()[:300])
	}
	mails, _ := db.OutboxEmails(context.Background())
	refused := 0
	for _, m := range mails {
		if m.Kind == "test" {
			full, _ := db.GetMail(context.Background(), m.ID)
			if full.Status == "refused" {
				refused++
			}
		}
	}
	if refused == 0 {
		t.Fatal("refused send was not recorded in the ledger")
	}

	// "restart": a new Server over the same pool enforces the same cap
	srv2 := web.New(deps)
	rr = apiCall(t, srv2, "POST", sendURL, map[string]string{"audience": "test"})
	if rr.Code != 429 {
		t.Fatalf("send after restart = %d, want 429 — cap must persist", rr.Code)
	}
}

// TestGlobalCapAcrossWorkspaces: the daily cap is shared across the
// hosted deployment; one workspace's sends shrink everyone's budget.
func TestGlobalCapAcrossWorkspaces(t *testing.T) {
	fake := &fakeSender{}
	seed := func(ctx context.Context, sdb *store.DB) error {
		data, _ := examples.FS.ReadFile("publications/futuro-proximo.yaml")
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		_, err = sdb.SeedPublication(ctx, def)
		return err
	}
	mgr, err := workspace.OpenHosted(context.Background(), testdb.Pool(t),
		[]byte("test-secret-key-32-bytes-padded-ok!"), seed)
	if err != nil {
		t.Fatalf("OpenHosted: %v", err)
	}
	t.Cleanup(mgr.Close)
	cfg := &config.Config{Mode: "demo", EmailDailyCap: 1, EmailWorkspaceDailyCap: 25}
	srv := web.New(web.Deps{
		WS: mgr, Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm"},
		Monitor: sources.FakeMonitor{}, Mailer: fake, Seed: seed,
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

	// workspace A's claim mail spends the single global slot
	cookieA := strings.SplitN(serve("GET", "/", "", nil).Header().Get("Set-Cookie"), ";", 2)[0]
	if rr := serve("POST", "/api/v1/workspace/claim", cookieA, map[string]string{"email": "a@x.dev"}); rr.Code != 200 {
		t.Fatalf("claim A = %d", rr.Code)
	}

	// workspace B's claim send is refused — loudly, and recorded.
	cookieB := strings.SplitN(serve("GET", "/", "", nil).Header().Get("Set-Cookie"), ";", 2)[0]
	rr := serve("POST", "/api/v1/workspace/claim", cookieB, map[string]string{"email": "b@x.dev"})
	if rr.Code != 200 {
		t.Fatalf("claim B = %d %s", rr.Code, rr.Body.String()[:300])
	}
	res := apiJSON(t, rr)
	if res["sent"] == true || res["refused"] == nil {
		t.Fatalf("over-cap claim = %v, want sent:false + refused reason", res)
	}
}

// TestResendWebhookFlow: delivered marks the row; bounced suppresses the
// address globally and unsubscribes it; forged signatures are rejected.
func TestResendWebhookFlow(t *testing.T) {
	fake := &fakeSender{}
	var cfg *config.Config
	deps, db, pub, c := liveDeps(t, fake, nil)
	cfg = c
	srv := web.New(deps)
	// run-unique addresses: suppressions are global and persist across
	// test runs on a shared database.
	owner := fmt.Sprintf("hook-%d@hackbrazil.dev", time.Now().UnixNano())
	bounced := fmt.Sprintf("bounce-%d@x.dev", time.Now().UnixNano())
	claimWorkspace(t, srv, db, owner)
	ctx := context.Background()

	// the claim mail's provider id is the fake's re_N
	var mail *store.OutboxMail
	mails, _ := db.OutboxEmails(ctx)
	for _, m := range mails {
		if m.Kind == "magic_link" {
			mail, _ = db.GetMail(ctx, m.ID)
		}
	}
	if mail == nil || mail.ProviderID == "" {
		t.Fatal("claim mail has no provider id")
	}
	pid := mail.ProviderID

	// forged signature → 401
	if rr := svixRequest(t, srv, cfg.ResendWebhookSecret,
		[]byte(`{"type":"email.delivered","data":{"email_id":"`+pid+`","to":["`+owner+`"]}}`), true); rr.Code != 401 {
		t.Fatalf("bad signature = %d, want 401", rr.Code)
	}
	// real signature → 204, status delivered
	if rr := svixRequest(t, srv, cfg.ResendWebhookSecret,
		[]byte(`{"type":"email.delivered","data":{"email_id":"`+pid+`","to":["`+owner+`"]}}`), false); rr.Code != 204 {
		t.Fatalf("delivered webhook = %d, want 204", rr.Code)
	}
	if got, _ := db.GetMail(ctx, mail.ID); got.Status != "delivered" {
		t.Fatalf("status = %q, want delivered", got.Status)
	}

	// a subscribed+confirmed reader who bounces is suppressed globally
	subscribeAndConfirm(t, srv, db, pub, bounced)
	issueID := approvedIssue(t, db, pub.ID, 1, "Edição 1")
	apiCall(t, srv, "POST", "/api/v1/issues/"+strconv.FormatInt(issueID, 10)+"/send",
		map[string]string{"audience": "readers"})
	// the webhook addresses the message by the provider id the sender
	// recorded in the ledger.
	bouncePid := ""
	mails, _ = db.OutboxEmails(ctx)
	for _, m := range mails {
		if m.Kind == "issue" && m.To == bounced {
			if full, _ := db.GetMail(ctx, m.ID); full != nil {
				bouncePid = full.ProviderID
			}
		}
	}
	if bouncePid == "" {
		t.Fatalf("issue mail to %s has no provider id", bounced)
	}
	if rr := svixRequest(t, srv, cfg.ResendWebhookSecret,
		[]byte(`{"type":"email.bounced","data":{"email_id":"`+bouncePid+`","to":["`+bounced+`"]}}`), false); rr.Code != 204 {
		t.Fatalf("bounced webhook = %d, want 204", rr.Code)
	}
	sup, err := db.Suppressed(ctx, []string{bounced, "fine@x.dev"})
	if err != nil || !sup[bounced] || sup["fine@x.dev"] {
		t.Fatalf("suppressions = %v, err %v", sup, err)
	}
	if rd, _ := db.ReaderByEmail(ctx, pub.ID, bounced); rd == nil || !rd.Unsubscribed {
		t.Fatal("bounced reader still subscribed")
	}

	// the unsubscribed reader is no longer a recipient at all — and a
	// suppressed address is refused inside sendMail even when a send is
	// attempted. Bounce the owner's own mail to see it: their next test
	// send must be refused, loudly, with a ledger row — never dropped.
	if rr := svixRequest(t, srv, cfg.ResendWebhookSecret,
		[]byte(`{"type":"email.bounced","data":{"email_id":"`+pid+`","to":["`+owner+`"]}}`), false); rr.Code != 204 {
		t.Fatalf("owner bounce webhook = %d, want 204", rr.Code)
	}
	before := len(fake.messages())
	issueID2 := approvedIssue(t, db, pub.ID, 2, "Edição 2")
	rr := apiCall(t, srv, "POST", "/api/v1/issues/"+strconv.FormatInt(issueID2, 10)+"/send",
		map[string]string{"audience": "test"})
	if rr.Code != 429 {
		t.Fatalf("test send to suppressed owner = %d, want 429", rr.Code)
	}
	if n := len(fake.messages()) - before; n != 0 {
		t.Fatalf("suppressed owner got %d mails", n)
	}
	refused := 0
	mails, _ = db.OutboxEmails(ctx)
	for _, m := range mails {
		if m.Kind != "test" || m.To != owner {
			continue
		}
		if full, _ := db.GetMail(ctx, m.ID); full != nil && full.Status == "refused" {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("no refused ledger row for the suppressed send")
	}
}

// subscribeAndConfirm runs a reader through the whole double opt-in.
func subscribeAndConfirm(t *testing.T, srv *web.Server, db *store.DB, pub *store.Publication, email string) {
	t.Helper()
	rr := apiCall(t, srv, "POST", "/api/v1/p/"+pub.Slug+"/subscribe", map[string]string{"email": email})
	if rr.Code != 200 {
		t.Fatalf("subscribe = %d", rr.Code)
	}
	mails, _ := db.OutboxEmails(context.Background())
	for _, m := range mails {
		if m.Kind != "reader_confirm" || m.To != email {
			continue
		}
		full, _ := db.GetMail(context.Background(), m.ID)
		if link := confirmLinkRe.FindString(full.BodyMD); link != "" {
			if rr := apiCall(t, srv, "GET", link, nil); rr.Code != http.StatusSeeOther {
				t.Fatalf("confirm = %d link=%q", rr.Code, link)
			}
			return
		}
	}
	t.Fatalf("no confirm email for %s", email)
}

// TestHostedClaimSignsInBrowser: in hosted mode the claim link carries
// the workspace and signing in happens in whichever browser opens it.
func TestHostedClaimSignsInBrowser(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSender{}
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
	t.Cleanup(mgr.Close)
	cfg := &config.Config{
		Mode: "demo", EmailDailyCap: 80, EmailWorkspaceDailyCap: 25,
		ResendWebhookSecret: "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-webhook-secret-32-bytes!!")),
	}
	srv := web.New(web.Deps{
		WS: mgr, Cfg: cfg,
		Fetcher: sources.FixtureFetcher{FS: examples.FS},
		LLM:     llm.Replay{FS: examples.FS, Dir: "fixtures/llm"},
		Monitor: sources.FakeMonitor{}, Mailer: fake, Seed: seed,
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

	// visitor A claims their workspace by email
	cookieA := strings.SplitN(serve("GET", "/", "", nil).Header().Get("Set-Cookie"), ";", 2)[0]
	rr := serve("POST", "/api/v1/workspace/claim", cookieA, map[string]string{"email": "host@hackbrazil.dev"})
	if rr.Code != 200 {
		t.Fatalf("claim = %d %s", rr.Code, rr.Body.String()[:300])
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Cookie", cookieA)
	wsA, err := mgr.Resolve(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("resolve A: %v", err)
	}
	msgs := fake.messages()
	var tok string
	for _, m := range msgs {
		if m.Kind == "magic_link" {
			if mm := claimTokenRe.FindStringSubmatch(m.Text); mm != nil {
				tok = mm[1]
			}
		}
	}
	if tok == "" {
		t.Fatal("claim email carried no token")
	}

	// visitor B's browser opens the link — no cookie at all: the claim
	// signs THAT browser into A's workspace.
	rr = serve("GET", "/claim?t="+tok, "", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("claim confirm = %d, want 303", rr.Code)
	}
	set := rr.Header().Get("Set-Cookie")
	if !strings.Contains(set, workspace.CookieName+"="+wsA.ID) {
		t.Fatalf("claim did not set the workspace cookie: %q", set)
	}
	// and the workspace is now owned
	if email, _, err := mgr.Pool().WorkspaceOwner(ctx, wsA.ID); err != nil || email != "host@hackbrazil.dev" {
		t.Fatalf("owner = %q %v", email, err)
	}
}
