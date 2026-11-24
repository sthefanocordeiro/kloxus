// Package voyage is a plain-HTTP client for Voyage AI's /v1/embeddings
// and /v1/rerank (no SDK). It owns the per-model-group rate limiter,
// the 429 retry policy, the daily USD caps and the cost-ledger writes
// (KLOX-108). The API key is sent as a Bearer token and is never
// logged or put in an error.
package voyage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Group is a rate-limit / cap bucket; it doubles as the ledger kind.
type Group string

const (
	GroupEmbed  Group = "embed"
	GroupRerank Group = "rerank"
)

// PricePerMTok is the list price (USD per 1M tokens) for the lite
// embedding and rerank models (see D60).
// Usage is priced at list even while inside the free allowance so the
// end of the allowance is visible in the ledger.
const PricePerMTok = 0.02

const (
	DefaultBaseURL = "https://api.voyageai.com"
	maxRetries     = 2
	defaultBackoff = 30 * time.Second
)

var (
	// ErrNoKey: VOYAGE_API_SECRET is not configured.
	ErrNoKey = errors.New("voyage: VOYAGE_API_SECRET is not set")
	// ErrCapReached: the group's daily USD cap is spent until 00:00 UTC.
	ErrCapReached = errors.New("voyage: daily cap reached")
)

// APIError is a non-2xx answer. Body is truncated; the key is only
// ever sent in a header, so it cannot appear here.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("voyage: HTTP %d: %s", e.Status, e.Body) }

// Ledger persists usage and answers how much a group spent since a
// moment. Implemented by store.Pool.
type Ledger interface {
	// VoyageSpendSince sums recorded USD for kind since the UTC
	// timestamp in 'YYYY-MM-DD HH24:MI:SS' form.
	VoyageSpendSince(ctx context.Context, kind, since string) (float64, error)
	VoyageUsage(ctx context.Context, kind, model string, tokens int, costUSD float64) error
}

// Client talks to Voyage. Zero-value optional fields get defaults.
type Client struct {
	Key     string
	BaseURL string
	HTTP    *http.Client
	Ledger  Ledger // optional; nil keeps spend in memory only
	Log     *log.Logger

	Limits map[Group]Limits  // per-group RPM/TPM (half the account limits)
	Caps   map[Group]float64 // per-group daily USD cap; <=0 disables

	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error

	mu       sync.Mutex
	limiters map[Group]*Limiter
	spend    map[Group]*daySpend
}

type daySpend struct {
	day    string
	usd    float64
	loaded bool
	logged bool
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	return sleepCtx(ctx, d)
}

func (c *Client) logf(f string, a ...any) {
	if c.Log != nil {
		c.Log.Printf(f, a...)
	}
}

// EstimateTokens is the conservative chars/3 estimate used before a
// request is sent (runes, not bytes, so accents do not inflate it).
func EstimateTokens(s string) int {
	n := len([]rune(s))
	return (n + 2) / 3
}

func (c *Client) limiter(g Group) *Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limiters == nil {
		c.limiters = map[Group]*Limiter{}
	}
	l, ok := c.limiters[g]
	if !ok {
		l = NewLimiter(c.Limits[g], c.now, c.Sleep)
		c.limiters[g] = l
	}
	return l
}

// checkCap refuses when the group already spent its daily cap. The
// running total is seeded from the ledger once per UTC day, so a
// restart never resets it.
func (c *Client) checkCap(ctx context.Context, g Group) error {
	limit := c.Caps[g]
	if limit <= 0 {
		return nil
	}
	now := c.now()
	day := now.Format("2006-01-02")
	c.mu.Lock()
	if c.spend == nil {
		c.spend = map[Group]*daySpend{}
	}
	s := c.spend[g]
	if s == nil || s.day != day {
		s = &daySpend{day: day}
		c.spend[g] = s
	}
	needLoad := !s.loaded && c.Ledger != nil
	c.mu.Unlock()
	if needLoad {
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		usd, err := c.Ledger.VoyageSpendSince(ctx, string(g), start.Format("2006-01-02 15:04:05"))
		if err != nil {
			return fmt.Errorf("voyage: read spend: %w", err)
		}
		c.mu.Lock()
		if !s.loaded {
			s.usd += usd
			s.loaded = true
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.usd >= limit {
		if !s.logged {
			s.logged = true
			c.logf("voyage: %s daily cap $%.4f reached - using fallbacks until 00:00 UTC", g, limit)
		}
		return ErrCapReached
	}
	return nil
}

func (c *Client) record(ctx context.Context, g Group, model string, tokens int) {
	cost := float64(tokens) / 1e6 * PricePerMTok
	c.mu.Lock()
	if s := c.spend[g]; s != nil {
		s.usd += cost
	}
	c.mu.Unlock()
	if c.Ledger != nil {
		if err := c.Ledger.VoyageUsage(ctx, string(g), model, tokens, cost); err != nil {
			c.logf("voyage: ledger write failed: %v", err)
		}
	}
}

// do runs one logical request: cap check, limiter, HTTP, 429 retries.
// 400 and other non-2xx answers are never retried.
func (c *Client) do(ctx context.Context, g Group, path string, est int, body any, out any) (int, error) {
	if strings.TrimSpace(c.Key) == "" {
		return 0, ErrNoKey
	}
	if err := c.checkCap(ctx, g); err != nil {
		return 0, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	for attempt := 0; ; attempt++ {
		if err := c.limiter(g).Wait(ctx, est); err != nil {
			return 0, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(payload))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return 0, fmt.Errorf("voyage: %s: %w", path, err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			if attempt >= maxRetries {
				return 0, &APIError{Status: resp.StatusCode, Body: trunc(raw)}
			}
			wait := defaultBackoff
			if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s >= 0 {
				wait = time.Duration(s) * time.Second
			}
			c.logf("voyage: 429 on %s, retry %d/%d in %s", path, attempt+1, maxRetries, wait)
			if err := c.sleep(ctx, wait); err != nil {
				return 0, err
			}
			continue
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			return 0, &APIError{Status: resp.StatusCode, Body: trunc(raw)}
		}
		var u struct {
			Usage struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &u); err != nil {
			return 0, fmt.Errorf("voyage: decode %s: %w", path, err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return 0, fmt.Errorf("voyage: decode %s: %w", path, err)
		}
		return u.Usage.TotalTokens, nil
	}
}

func trunc(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// EmbedRequest is one /v1/embeddings call.
type EmbedRequest struct {
	Model           string
	Input           []string
	InputType       string // "document" | "query"
	OutputDimension int    // 0 = model default
}

// EmbedResponse carries vectors in input order.
type EmbedResponse struct {
	Vectors     [][]float32
	TotalTokens int
}

// Embed calls /v1/embeddings.
func (c *Client) Embed(ctx context.Context, r EmbedRequest) (*EmbedResponse, error) {
	est := 0
	for _, s := range r.Input {
		est += EstimateTokens(s)
	}
	body := map[string]any{"model": r.Model, "input": r.Input}
	if r.InputType != "" {
		body["input_type"] = r.InputType
	}
	if r.OutputDimension > 0 {
		body["output_dimension"] = r.OutputDimension
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	tokens, err := c.do(ctx, GroupEmbed, "/v1/embeddings", est, body, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Data) != len(r.Input) {
		return nil, fmt.Errorf("voyage: got %d embeddings for %d inputs", len(out.Data), len(r.Input))
	}
	vecs := make([][]float32, len(r.Input))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, errors.New("voyage: embedding index out of range")
		}
		vecs[d.Index] = d.Embedding
	}
	c.record(ctx, GroupEmbed, r.Model, tokens)
	return &EmbedResponse{Vectors: vecs, TotalTokens: tokens}, nil
}

// RerankRequest is one /v1/rerank call.
type RerankRequest struct {
	Model     string
	Query     string
	Documents []string
	TopK      int // 0 = all
}

// RerankResult is one scored document, by original index.
type RerankResult struct {
	Index int
	Score float64
}

// RerankResponse is ordered by descending score.
type RerankResponse struct {
	Results     []RerankResult
	TotalTokens int
}

// Rerank calls /v1/rerank. Voyage counts query_tokens x documents +
// sum(document tokens), which the pre-request estimate mirrors.
func (c *Client) Rerank(ctx context.Context, r RerankRequest) (*RerankResponse, error) {
	est := EstimateTokens(r.Query) * len(r.Documents)
	for _, d := range r.Documents {
		est += EstimateTokens(d)
	}
	body := map[string]any{"model": r.Model, "query": r.Query, "documents": r.Documents}
	if r.TopK > 0 {
		body["top_k"] = r.TopK
	}
	var out struct {
		Data []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"data"`
	}
	tokens, err := c.do(ctx, GroupRerank, "/v1/rerank", est, body, &out)
	if err != nil {
		return nil, err
	}
	res := make([]RerankResult, len(out.Data))
	for i, d := range out.Data {
		res[i] = RerankResult{Index: d.Index, Score: d.RelevanceScore}
	}
	c.record(ctx, GroupRerank, r.Model, tokens)
	return &RerankResponse{Results: res, TotalTokens: tokens}, nil
}
