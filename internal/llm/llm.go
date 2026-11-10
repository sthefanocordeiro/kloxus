// Package llm defines the model provider interface and its three
// implementations: Anthropic (real), Replay (demo, recorded fixtures) and
// Recorder (wraps a real provider to regenerate fixtures).
package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// Request is one model call.
type Request struct {
	Model     string // e.g. claude-haiku-5-5
	Template  string // prompt template name, e.g. "triage"
	System    string
	User      string
	MaxTokens int
}

// Response is the model's reply with usage for cost tracking.
type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

// Provider is implemented by Anthropic, Replay and Recorder.
type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Func adapts a function to Provider (tests, stubs).
type Func func(ctx context.Context, req Request) (Response, error)

// Complete implements Provider.
func (f Func) Complete(ctx context.Context, req Request) (Response, error) {
	return f(ctx, req)
}

// ErrMissingFixture marks a replay lookup that found no recorded response.
var ErrMissingFixture = errors.New("missing LLM fixture")

// Key returns the stable replay key for a request: hash of model,
// template and whitespace-normalized user content.
func Key(r Request) string {
	norm := strings.Join(strings.Fields(r.User), " ")
	sum := sha256.Sum256([]byte(r.Model + "\x00" + r.Template + "\x00" + norm))
	return hex.EncodeToString(sum[:8])
}

// fixtureFile is the on-disk format of a recorded response.
type fixtureFile struct {
	Model        string `json:"model"`
	Text         string `json:"text"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// PricePerMillion maps model name to (input, output) USD per million
// tokens. Defaults are placeholders to be verified against current
// pricing; unknown models use a conservative mid-range estimate.
func PricePerMillion(model string) (in, out float64) {
	switch {
	case strings.Contains(model, "haiku"):
		return 1.00, 5.00
	case strings.Contains(model, "sonnet"):
		return 3.00, 15.00
	case strings.Contains(model, "opus"):
		return 15.00, 75.00
	default:
		return 3.00, 15.00
	}
}

// CostUSD estimates the dollar cost of a response.
func CostUSD(model string, in, out int) float64 {
	pi, po := PricePerMillion(model)
	return float64(in)/1e6*pi + float64(out)/1e6*po
}
