package web_test

// KLOX-13 acceptance tests: the paid-section checkout end to end —
// fake-provider fallback, webhook confirm + replay, polling fallback,
// and the paid_until expiry.
import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/payments"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
)

// abacatePubKey mirrors the documented public HMAC key the webhook
// checks against — it is public, so the test hardcodes it like the
// receiver does.
const abacatePubKey = "t9dXRhHHo3yDEj5pVDYz0frf7q6bMKyMRmxxCPIPp3RCplBfXRxqlC6ZpiWmOqj4L63qEaeUOtrCI8P0VMUgo6iIga2ri9ogaHFs0WIIywSMg0q7RmBfybe1E5XJcfC4IW3alNqym0tXoAKkzvfEjZxV6bE0oG2zJrNNYmUCKZyV0KZ3JS8Votf9EAWWYdiDkMkpbMdPggfh1EqHlVkMiTady6jOR3hyzGEHrIz2Ret0xHKMbiqkr9HS1JhNHDX9"

// fakeAbacate is an in-process AbacatePay v1 stub: billing create
// records the checkout, billing list reports the scripted status.
type fakeAbacate struct {
	srv    *httptest.Server
	status string
	last   map[string]any
}

func newFakeAbacate(t *testing.T, status string) *fakeAbacate {
	t.Helper()
	f := &fakeAbacate{status: status}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/billing/create":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			f.last = in
			ext, _ := in["externalId"].(string)
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id": "bill_42", "externalId": ext, "url": f.srv.URL + "/pay/bill_42",
					"status": "PENDING", "devMode": true,
				},
				"error": nil,
			})
		case r.Method == "GET" && r.URL.Path == "/v1/billing/list":
			ext := ""
			if f.last != nil {
				ext, _ = f.last["externalId"].(string)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"id": "bill_42", "externalId": ext, "status": f.status, "devMode": true,
				}},
				"error": nil,
			})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// webhookCall posts a signed (or deliberately mis-signed) billing event.
func webhookCall(t *testing.T, srv *web.Server, secret, body string, sign bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/webhooks/abacatepay", strings.NewReader(body))
	if secret != "" {
		req.Header.Set("X-Webhook-Secret", secret)
	}
	if sign {
		mac := hmac.New(sha256.New, []byte(abacatePubKey))
		mac.Write([]byte(body))
		req.Header.Set("X-Webhook-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	}
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

func paidEvent(ref string) string {
	return fmt.Sprintf(`{"event":"billing.paid","devMode":true,"data":{"id":"bill_42","externalId":%q,"status":"PAID"}}`, ref)
}

// openCheckout posts the reader checkout and returns the parsed payload.
func openCheckout(t *testing.T, srv *web.Server, slug, email string) map[string]any {
	t.Helper()
	rr := apiCall(t, srv, "POST", "/api/v1/p/"+slug+"/checkout", map[string]string{"email": email})
	if rr.Code != 200 {
		t.Fatalf("checkout = %d: %s", rr.Code, shortBody(rr))
	}
	return apiJSON(t, rr)
}

func readerAccessMails(t *testing.T, db *store.DB) int {
	t.Helper()
	n := 0
	for _, e := range mustOutbox(t, db) {
		if e.Kind == "reader_access" {
			n++
		}
	}
	return n
}

func mustOutbox(t *testing.T, db *store.DB) []store.OutboxEmail {
	t.Helper()
	emails, err := db.OutboxEmails(t.Context())
	if err != nil {
		t.Fatalf("OutboxEmails: %v", err)
	}
	return emails
}

// TestPayWebhookFlow: checkout → webhook confirm → magic link granted;
// a forged event is rejected, a replay grants exactly once, and the
// access ends at paid_until.
func TestPayWebhookFlow(t *testing.T) {
	fake := newFakeAbacate(t, "PAID")
	srv, db, pub, cfg := demoServerOpt(t, func(d *web.Deps) {
		d.Pay = payments.AbacatePay{APIKey: "abc_dev_test", BaseURL: fake.srv.URL}
	})
	cfg.AbacatePayWebhookSecret = "whsec_test_32chars_min_length_secret"

	res := openCheckout(t, srv, pub.Slug, "reader@example.com")
	if res["provider"] != "abacatepay" || res["test_mode"] != true {
		t.Fatalf("checkout response = %v", res)
	}
	ref, _ := res["ref"].(string)
	url, _ := res["checkout_url"].(string)
	if ref == "" || url == "" || !strings.HasPrefix(ref, "pay_") {
		t.Fatalf("checkout missing ref/url: %v", res)
	}
	pay, err := db.PaymentByRef(t.Context(), pub.ID, ref)
	if err != nil || pay.Status != "pending" || pay.ProviderRef != "bill_42" {
		t.Fatalf("payment row = %+v err=%v", pay, err)
	}

	// forged deliveries never reach the grant path: wrong secret → 401,
	// and a signature computed over different bytes → 401 (the body was
	// signed, then tampered).
	if rr := webhookCall(t, srv, "wrong-secret", paidEvent(ref), true); rr.Code != 401 {
		t.Fatalf("bad secret = %d, want 401", rr.Code)
	}
	mac := hmac.New(sha256.New, []byte(abacatePubKey))
	mac.Write([]byte(paidEvent(ref)))
	tampered := httptest.NewRequest("POST", "/webhooks/abacatepay", strings.NewReader(paidEvent(ref)+" "))
	tampered.Header.Set("X-Webhook-Secret", cfg.AbacatePayWebhookSecret)
	tampered.Header.Set("X-Webhook-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	badSig := httptest.NewRecorder()
	srv.ServeHTTP(badSig, tampered)
	if badSig.Code != 401 {
		t.Fatalf("tampered body = %d, want 401", badSig.Code)
	}

	// the real event grants access once
	if rr := webhookCall(t, srv, cfg.AbacatePayWebhookSecret, paidEvent(ref), true); rr.Code != 204 {
		t.Fatalf("webhook = %d", rr.Code)
	}
	pay, err = db.PaymentByRef(t.Context(), pub.ID, ref)
	if err != nil || pay.Status != "paid" || pay.PaidUntil == "" {
		t.Fatalf("payment after webhook = %+v err=%v", pay, err)
	}
	rid, _ := db.ReaderByEmail(t.Context(), pub.ID, "reader@example.com")
	if active, _ := db.HasActiveSubscription(t.Context(), rid.ID); !active {
		t.Fatal("webhook grant did not activate the subscription")
	}
	if n := readerAccessMails(t, db); n != 1 {
		t.Fatalf("access mails = %d, want 1", n)
	}

	// a replayed delivery grants nothing twice
	if rr := webhookCall(t, srv, cfg.AbacatePayWebhookSecret, paidEvent(ref), true); rr.Code != 204 {
		t.Fatalf("replay = %d", rr.Code)
	}
	if n := readerAccessMails(t, db); n != 1 {
		t.Fatalf("access mails after replay = %d, want 1", n)
	}

	// outbox mode: the status endpoint re-serves the link, and it opens
	// the paid sections
	st := apiGet(t, srv, "/api/v1/p/"+pub.Slug+"/pay/"+ref)
	if st["status"] != "paid" {
		t.Fatalf("status = %v", st)
	}
	link, _ := st["magic_link"].(string)
	key := ""
	if i := strings.Index(link, "key="); i >= 0 {
		key = link[i+4:]
		if j := strings.Index(key, "&"); j >= 0 {
			key = key[:j]
		}
	}
	if key == "" {
		t.Fatalf("status did not re-serve the magic link: %v", st)
	}
	if tok, err := db.SubscriptionToken(t.Context(), rid.ID); err != nil || tok != key {
		t.Fatalf("link token %q vs stored %q (err=%v)", key, tok, err)
	}
	if _, err := db.ReaderByMagicToken(t.Context(), pub.ID, key); err != nil {
		t.Fatalf("magic token does not unlock: %v", err)
	}

	// access ends at paid_until
	if _, err := db.ExecContext(t.Context(),
		`UPDATE subscriptions SET paid_until = '2000-01-01 00:00:00' WHERE reader_id = $1`, rid.ID); err != nil {
		t.Fatalf("expire subscription: %v", err)
	}
	if active, _ := db.HasActiveSubscription(t.Context(), rid.ID); active {
		t.Fatal("subscription stayed active past paid_until")
	}
	if _, err := db.ReaderByMagicToken(t.Context(), pub.ID, key); err != sql.ErrNoRows {
		t.Fatalf("expired token still unlocks (err=%v)", err)
	}
}

// TestPayPollFallback: the webhook never arrives — the return page's
// poll asks the provider, the PAID answer runs the same grant, and a
// second poll does not re-send the link.
func TestPayPollFallback(t *testing.T) {
	fake := newFakeAbacate(t, "PAID")
	srv, db, pub, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Pay = payments.AbacatePay{APIKey: "abc_dev_test", BaseURL: fake.srv.URL}
	})
	res := openCheckout(t, srv, pub.Slug, "poller@example.com")
	ref := res["ref"].(string)

	st := apiGet(t, srv, "/api/v1/p/"+pub.Slug+"/pay/"+ref)
	if st["status"] != "paid" {
		t.Fatalf("poll did not confirm: %v", st)
	}
	if st["magic_link"] == "" {
		t.Fatal("outbox mode should re-serve the magic link")
	}
	if n := readerAccessMails(t, db); n != 1 {
		t.Fatalf("access mails = %d, want 1", n)
	}
	// a second poll finds status=paid and grants nothing again
	st = apiGet(t, srv, "/api/v1/p/"+pub.Slug+"/pay/"+ref)
	if n := readerAccessMails(t, db); n != 1 {
		t.Fatalf("access mails after re-poll = %d, want 1", n)
	}
}

// TestPayFakeFallback: with no configured provider — and with the real
// one unreachable — checkout still completes through the fake provider,
// the response says so, and the grant path runs identically.
func TestPayFakeFallback(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	res := openCheckout(t, srv, pub.Slug, "demo@example.com")
	if res["provider"] != "fake" || res["simulated"] != true {
		t.Fatalf("expected fake/simulated checkout, got %v", res)
	}
	done, _ := res["checkout_url"].(string)
	if !strings.Contains(done, "pay=") {
		t.Fatalf("fake checkout should return straight to the page: %v", res)
	}
	ref := res["ref"].(string)
	st := apiGet(t, srv, "/api/v1/p/"+pub.Slug+"/pay/"+ref)
	if st["status"] != "paid" || st["magic_link"] == "" {
		t.Fatalf("fake grant did not complete: %v", st)
	}
	rid, _ := db.ReaderByEmail(t.Context(), pub.ID, "demo@example.com")
	if active, _ := db.HasActiveSubscription(t.Context(), rid.ID); !active {
		t.Fatal("fake grant did not activate the subscription")
	}

	// provider down: the AbacatePay client errors and the fake serves
	srv2, _, pub2, _ := demoServerOpt(t, func(d *web.Deps) {
		d.Pay = payments.AbacatePay{APIKey: "abc_dev_test", BaseURL: "http://127.0.0.1:1"}
	})
	res = openCheckout(t, srv2, pub2.Slug, "down@example.com")
	if res["provider"] != "fake" || res["simulated"] != true {
		t.Fatalf("provider-down should fall back to fake: %v", res)
	}
}

// TestPayAlreadySubscribed: an active subscriber is never charged again.
func TestPayAlreadySubscribed(t *testing.T) {
	srv, db, pub, _ := demoServer(t)
	rid, err := db.UpsertReader(t.Context(), pub.ID, "sub@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ActivateSubscription(t.Context(), rid, "fake", "x", "tok"); err != nil {
		t.Fatal(err)
	}
	res := openCheckout(t, srv, pub.Slug, "sub@example.com")
	if res["already_subscribed"] != true {
		t.Fatalf("active subscriber was offered a checkout: %v", res)
	}
}
