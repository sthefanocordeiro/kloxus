package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeProvider returns queued (resp, err) pairs in order.
type fakeProvider struct {
	calls int
	queue []struct {
		resp Response
		err  error
	}
}

func (f *fakeProvider) Complete(_ context.Context, _ Request) (Response, error) {
	f.calls++
	if len(f.queue) == 0 {
		return Response{}, errors.New("no queued response")
	}
	q := f.queue[0]
	f.queue = f.queue[1:]
	return q.resp, q.err
}

func noSleep(context.Context, time.Duration) error { return nil }

func okResp() Response {
	return Response{Text: "{}", InputTokens: 10, OutputTokens: 5}
}

func TestRetrierRetries429(t *testing.T) {
	f := &fakeProvider{queue: []struct {
		resp Response
		err  error
	}{
		{err: &StatusError{Provider: "anthropic", Status: 429, Body: "rate limited"}},
		{resp: okResp()},
	}}
	r := Retrier{Inner: f, Attempts: 3, Sleep: noSleep}
	resp, err := r.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if f.calls != 2 || resp.Text != "{}" {
		t.Fatalf("calls=%d resp=%v", f.calls, resp)
	}
}

func TestRetrierRetries5xxAndNetwork(t *testing.T) {
	f := &fakeProvider{queue: []struct {
		resp Response
		err  error
	}{
		{err: &StatusError{Provider: "anthropic", Status: 502, Body: "bad gateway"}},
		{err: &url.Error{Op: "Post", URL: "https://api.anthropic.com", Err: errors.New("timeout")}},
		{resp: okResp()},
	}}
	r := Retrier{Inner: f, Attempts: 4, Sleep: noSleep}
	if _, err := r.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if f.calls != 3 {
		t.Fatalf("calls=%d, want 3 (5xx + network retried)", f.calls)
	}
}

func TestRetrierDoesNotRetry4xx(t *testing.T) {
	f := &fakeProvider{queue: []struct {
		resp Response
		err  error
	}{
		{err: &StatusError{Provider: "anthropic", Status: 400, Body: "bad request"}},
		{resp: okResp()},
	}}
	r := Retrier{Inner: f, Attempts: 4, Sleep: noSleep}
	_, err := r.Complete(context.Background(), Request{})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 400 {
		t.Fatalf("err=%v, want 400 StatusError", err)
	}
	if f.calls != 1 {
		t.Fatalf("calls=%d, want 1 (4xx is not retryable)", f.calls)
	}
}

func TestRetrierExhausts(t *testing.T) {
	f := &fakeProvider{queue: []struct {
		resp Response
		err  error
	}{
		{err: &StatusError{Provider: "anthropic", Status: 503}},
		{err: &StatusError{Provider: "anthropic", Status: 503}},
		{err: &StatusError{Provider: "anthropic", Status: 503}},
	}}
	r := Retrier{Inner: f, Attempts: 3, Sleep: noSleep}
	_, err := r.Complete(context.Background(), Request{})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 503 {
		t.Fatalf("err=%v, want last 503", err)
	}
	if f.calls != 3 {
		t.Fatalf("calls=%d, want all 3 attempts", f.calls)
	}
}

func TestRetrierHonorsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeProvider{queue: []struct {
		resp Response
		err  error
	}{
		{err: &StatusError{Status: 429}},
		{resp: okResp()},
	}}
	r := Retrier{Inner: f, Attempts: 3, Sleep: func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	}}
	_, err := r.Complete(ctx, Request{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}

// TestAnthropicStatusError exercises the typed error a real non-200
// Anthropic reply produces, so the retrier can classify it.
func TestAnthropicStatusError(t *testing.T) {
	var code int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(`{"error":{"message":"overloaded"}}`))
	}))
	defer srv.Close()
	a := Anthropic{APIKey: "k", BaseURL: srv.URL}
	code = 503
	_, err := a.Complete(context.Background(), Request{Model: "m", User: "hi", MaxTokens: 8})
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err=%v (%T), want *StatusError", err, err)
	}
	if se.Status != 503 || !strings.Contains(se.Body, "overloaded") {
		t.Fatalf("StatusError=%+v", se)
	}
	if !retryable(err) {
		t.Fatal("503 StatusError should be retryable")
	}
}
