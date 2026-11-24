package voyage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock advances only when something sleeps — no real waiting.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.t }
func (f *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
	return nil
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
}

func TestLimiterNeverExceedsRPMInWindow(t *testing.T) {
	clk := newClock()
	l := NewLimiter(Limits{RPM: 10}, clk.Now, clk.Sleep)
	var stamps []time.Time
	for i := 0; i < 55; i++ {
		if err := l.Wait(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		stamps = append(stamps, clk.Now())
		clk.Sleep(context.Background(), 100*time.Millisecond)
	}
	for i, s := range stamps {
		n := 0
		for _, o := range stamps[:i+1] {
			if s.Sub(o) < time.Minute {
				n++
			}
		}
		if n > 10 {
			t.Fatalf("%d requests inside one 60s window at i=%d", n, i)
		}
	}
}

func TestLimiterNeverExceedsTPMInWindow(t *testing.T) {
	clk := newClock()
	l := NewLimiter(Limits{TPM: 1000}, clk.Now, clk.Sleep)
	type ev struct {
		at time.Time
		n  int
	}
	var evs []ev
	for i := 0; i < 40; i++ {
		if err := l.Wait(context.Background(), 300); err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev{clk.Now(), 300})
	}
	for i, e := range evs {
		sum := 0
		for _, o := range evs[:i+1] {
			if e.at.Sub(o.at) < time.Minute {
				sum += o.n
			}
		}
		if sum > 1000 {
			t.Fatalf("%d tokens inside one 60s window at i=%d", sum, i)
		}
	}
	if err := l.Wait(context.Background(), 1001); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized request: %v", err)
	}
}

type memLedger struct {
	mu    sync.Mutex
	spent map[string]float64
	rows  int
}

func (m *memLedger) VoyageSpendSince(_ context.Context, kind, _ string) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spent[kind], nil
}
func (m *memLedger) VoyageUsage(_ context.Context, kind, _ string, _ int, cost float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.spent == nil {
		m.spent = map[string]float64{}
	}
	m.spent[kind] += cost
	m.rows++
	return nil
}

func embedServer(t *testing.T, handler func(n int, w http.ResponseWriter, r *http.Request)) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		handler(calls, w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func okEmbed(w http.ResponseWriter, n int, tokens int) {
	data := make([]map[string]any, n)
	for i := range data {
		data[i] = map[string]any{"index": i, "embedding": []float32{1, 0}}
	}
	json.NewEncoder(w).Encode(map[string]any{"data": data, "usage": map[string]any{"total_tokens": tokens}})
}

func TestEmbedSendsBearerAndLogsTokens(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv, _ := embedServer(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		okEmbed(w, 2, 500000)
	})
	led := &memLedger{}
	clk := newClock()
	c := &Client{Key: "sekret", BaseURL: srv.URL, Ledger: led, Now: clk.Now, Sleep: clk.Sleep,
		Limits: map[Group]Limits{GroupEmbed: {RPM: 100, TPM: 1e6}}}
	res, err := c.Embed(context.Background(), EmbedRequest{Model: "voyage-4-lite", Input: []string{"a", "b"}, InputType: "document", OutputDimension: 512})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sekret" || gotBody["input_type"] != "document" || gotBody["output_dimension"] != float64(512) {
		t.Fatalf("auth=%q body=%v", gotAuth, gotBody)
	}
	if res.TotalTokens != 500000 || len(res.Vectors) != 2 {
		t.Fatalf("res=%+v", res)
	}
	if got := led.spent["embed"]; got < 0.00999 || got > 0.01001 {
		t.Fatalf("ledger cost = %v, want 0.01", got)
	}
}

func TestRetry429HonorsRetryAfterThenGivesUp(t *testing.T) {
	srv, calls := embedServer(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
	})
	clk := newClock()
	start := clk.Now()
	c := &Client{Key: "k", BaseURL: srv.URL, Now: clk.Now, Sleep: clk.Sleep}
	_, err := c.Embed(context.Background(), EmbedRequest{Model: "m", Input: []string{"x"}})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 429 {
		t.Fatalf("err = %v", err)
	}
	if *calls != 3 {
		t.Fatalf("calls = %d, want 1 + 2 retries", *calls)
	}
	if got := clk.Now().Sub(start); got != 14*time.Second {
		t.Fatalf("waited %v, want 14s (2 x Retry-After)", got)
	}
}

func TestRetry429DefaultWait30sThenSucceeds(t *testing.T) {
	srv, calls := embedServer(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			w.WriteHeader(429)
			return
		}
		okEmbed(w, 1, 10)
	})
	clk := newClock()
	start := clk.Now()
	c := &Client{Key: "k", BaseURL: srv.URL, Now: clk.Now, Sleep: clk.Sleep}
	if _, err := c.Embed(context.Background(), EmbedRequest{Model: "m", Input: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || clk.Now().Sub(start) != 30*time.Second {
		t.Fatalf("calls=%d waited=%v", *calls, clk.Now().Sub(start))
	}
}

func TestNeverRetries400AndKeyStaysOutOfErrors(t *testing.T) {
	srv, calls := embedServer(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"detail":"bad"}`))
	})
	c := &Client{Key: "sekret-key", BaseURL: srv.URL}
	_, err := c.Embed(context.Background(), EmbedRequest{Model: "m", Input: []string{"x"}})
	if err == nil || *calls != 1 {
		t.Fatalf("err=%v calls=%d", err, *calls)
	}
	if strings.Contains(err.Error(), "sekret-key") {
		t.Fatal("key leaked into error")
	}
	if _, err := (&Client{}).Embed(context.Background(), EmbedRequest{Input: []string{"x"}}); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
}

func TestDailyCapStopsCallsUntilNextUTCDay(t *testing.T) {
	srv, calls := embedServer(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		okEmbed(w, 1, 1_000_000) // $0.02 per call at list price
	})
	var logs strings.Builder
	led := &memLedger{}
	clk := newClock()
	c := &Client{Key: "k", BaseURL: srv.URL, Ledger: led, Now: clk.Now, Sleep: clk.Sleep,
		Caps: map[Group]float64{GroupEmbed: 0.05}, Log: newLogger(&logs)}
	req := EmbedRequest{Model: "m", Input: []string{"x"}}
	for i := 0; i < 3; i++ { // 0.00 -> 0.02 -> 0.04 -> 0.06
		if _, err := c.Embed(context.Background(), req); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Embed(context.Background(), req); !errors.Is(err, ErrCapReached) {
			t.Fatalf("after cap: %v", err)
		}
	}
	if *calls != 3 {
		t.Fatalf("HTTP calls = %d, want 3", *calls)
	}
	if n := strings.Count(logs.String(), "daily cap"); n != 1 {
		t.Fatalf("cap logged %d times, want once", n)
	}
	// The rerank group is independent.
	// Next UTC day: calls resume (ledger is now stubbed to a fresh day).
	led.spent = map[string]float64{}
	clk.mu.Lock()
	clk.t = clk.t.Add(24 * time.Hour)
	clk.mu.Unlock()
	if _, err := c.Embed(context.Background(), req); err != nil {
		t.Fatalf("next day: %v", err)
	}
}

func TestCapSeededFromLedgerSurvivesRestart(t *testing.T) {
	srv, calls := embedServer(t, func(_ int, w http.ResponseWriter, r *http.Request) { okEmbed(w, 1, 1) })
	led := &memLedger{spent: map[string]float64{"rerank": 0.10}}
	c := &Client{Key: "k", BaseURL: srv.URL, Ledger: led, Caps: map[Group]float64{GroupRerank: 0.10}}
	_, err := c.Rerank(context.Background(), RerankRequest{Model: "m", Query: "q", Documents: []string{"d"}})
	if !errors.Is(err, ErrCapReached) || *calls != 0 {
		t.Fatalf("err=%v calls=%d", err, *calls)
	}
}

func TestRerankParsesScores(t *testing.T) {
	srv, _ := embedServer(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"index": 1, "relevance_score": 0.9}, {"index": 0, "relevance_score": 0.2}},
			"usage": map[string]any{"total_tokens": 42},
		})
	})
	c := &Client{Key: "k", BaseURL: srv.URL}
	res, err := c.Rerank(context.Background(), RerankRequest{Model: "rerank-2.5-lite", Query: "q", Documents: []string{"a", "b"}})
	if err != nil || res.TotalTokens != 42 || res.Results[0].Index != 1 || res.Results[0].Score != 0.9 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
