package delivery

// Resend transport tests run against a fake Resend API server — they
// cover the request shape (auth, idempotency, headers) and the retry
// policy: network errors, 5xx and 429 retry; other 4xx fail fast.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// resendCall records one HTTP request the fake API received.
type resendCall struct {
	auth string
	idem string
	body map[string]any
}

// fakeResend mounts a fake Resend API: handler decides the response per
// attempt (1-based) and every request is recorded.
func fakeResend(t *testing.T, handler func(attempt int, w http.ResponseWriter, c *resendCall)) (*Resend, func() []resendCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []resendCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c resendCall
		c.auth = r.Header.Get("Authorization")
		c.idem = r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&c.body)
		mu.Lock()
		calls = append(calls, c)
		attempt := len(calls)
		mu.Unlock()
		handler(attempt, w, &c)
	}))
	t.Cleanup(srv.Close)
	rs := NewResend("re_test_secret_key", "Kloxus <news@kloxus-ai.trendfi.com.br>")
	rs.BaseURL = srv.URL
	return rs, func() []resendCall { mu.Lock(); defer mu.Unlock(); return append([]resendCall(nil), calls...) }
}

func testMsg() *Message {
	return &Message{
		To:      []string{"reader@example.com"},
		Subject: "Edição 1",
		Text:    "hello",
		HTML:    "<p>hello</p>",
		Kind:    "issue",
		Headers: map[string]string{
			"List-Unsubscribe":      "<https://x/unsub?r=1>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
		IdemKey: "issue/1/r1",
	}
}

func resendOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"id":"re_msg_1"}`))
}

func TestResendSendRequestShape(t *testing.T) {
	rs, calls := fakeResend(t, func(_ int, w http.ResponseWriter, _ *resendCall) { resendOK(w) })
	id, err := rs.Send(context.Background(), testMsg())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "re_msg_1" {
		t.Fatalf("provider id = %q", id)
	}
	got := calls()
	if len(got) != 1 {
		t.Fatalf("calls = %d, want 1", len(got))
	}
	c := got[0]
	if c.auth != "Bearer re_test_secret_key" {
		t.Fatalf("Authorization = %q", c.auth)
	}
	if c.idem != "issue/1/r1" {
		t.Fatalf("Idempotency-Key = %q", c.idem)
	}
	if c.body["from"] != "Kloxus <news@kloxus-ai.trendfi.com.br>" {
		t.Fatalf("from = %v", c.body["from"])
	}
	headers, _ := c.body["headers"].(map[string]any)
	if headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("List-Unsubscribe-Post missing: %v", headers)
	}
}

func TestResendRetries5xxThenSucceeds(t *testing.T) {
	rs, calls := fakeResend(t, func(attempt int, w http.ResponseWriter, _ *resendCall) {
		if attempt < 3 {
			w.WriteHeader(500)
			return
		}
		resendOK(w)
	})
	id, err := rs.Send(context.Background(), testMsg())
	if err != nil || id != "re_msg_1" {
		t.Fatalf("Send = %q, %v", id, err)
	}
	if n := len(calls()); n != 3 {
		t.Fatalf("calls = %d, want 3", n)
	}
}

func TestResendRetries429WithRetryAfter(t *testing.T) {
	rs, calls := fakeResend(t, func(attempt int, w http.ResponseWriter, _ *resendCall) {
		if attempt == 1 {
			w.Header().Set("retry-after", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			w.Write([]byte(`{"message":"rate limited"}`))
			return
		}
		resendOK(w)
	})
	id, err := rs.Send(context.Background(), testMsg())
	if err != nil || id != "re_msg_1" {
		t.Fatalf("Send = %q, %v", id, err)
	}
	if n := len(calls()); n != 2 {
		t.Fatalf("calls = %d, want 2", n)
	}
}

func TestResendNeverRetries4xx(t *testing.T) {
	rs, calls := fakeResend(t, func(_ int, w http.ResponseWriter, _ *resendCall) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(422)
		w.Write([]byte(`{"statusCode":422,"name":"validation_error","message":"invalid from address"}`))
	})
	if _, err := rs.Send(context.Background(), testMsg()); err == nil {
		t.Fatal("expected an error for 422")
	}
	if n := len(calls()); n != 1 {
		t.Fatalf("calls = %d, want 1 — 4xx must not retry", n)
	}
}

func TestResendExhaustsRetries(t *testing.T) {
	rs, calls := fakeResend(t, func(_ int, w http.ResponseWriter, _ *resendCall) {
		w.WriteHeader(500)
	})
	if _, err := rs.Send(context.Background(), testMsg()); err == nil {
		t.Fatal("expected an error after attempts run out")
	}
	if n := len(calls()); n != resendAttempts {
		t.Fatalf("calls = %d, want %d", n, resendAttempts)
	}
}

func TestResendRetriesNetworkError(t *testing.T) {
	// A dead server produces a transport error, which must be retried —
	// the second fake (a live one) answers on a restarted endpoint.
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		resendOK(w)
	}))
	url := srv.URL
	srv.Close() // down: dials fail
	rs := NewResend("re_test_secret_key", "Kloxus <n@x.com>")
	rs.BaseURL = url
	rs.MaxAttempts = 2
	if _, err := rs.Send(context.Background(), testMsg()); err == nil {
		t.Fatal("expected a transport error")
	}
	if attempts != 0 {
		t.Fatalf("server saw %d calls — it was down", attempts)
	}
}

func TestRetryableClassifies(t *testing.T) {
	if !retryable(errors.New("dial tcp: connection refused")) {
		t.Error("transport error should retry")
	}
	if retryable(context.Canceled) {
		t.Error("caller cancellation should not retry")
	}
}
