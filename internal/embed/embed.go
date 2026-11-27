// Package embed is the provider-neutral embedder behind the ranking
// engine (KLOX-108). One vector per item, computed once at ingestion
// and shared by every workspace. It is distinct from internal/embedding,
// the deterministic feature-hashing layer KLOX-93 ships for the free
// topic classifier: that package stays zero-cost and offline, this one
// calls Voyage.
package embed

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/voyage"
)

// InputKind selects Voyage's input_type.
type InputKind string

const (
	Document InputKind = "document" // items
	Query    InputKind = "query"    // publication / section profiles
)

// Embedder turns texts into unit-length vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string, kind InputKind) ([][]float32, error)
	Model() string
	Dims() int
}

var (
	// ErrDisabled is what the "off" provider returns; callers treat it
	// as "use the FTS fallback", never as a failure to surface.
	ErrDisabled = errors.New("embed: provider is off")
	// ErrNotImplemented marks the future "local" provider.
	ErrNotImplemented = errors.New("embed: provider \"local\" is not built (see D60)")
)

const (
	DefaultModel = "voyage-4-lite"
	DefaultDims  = 512

	MaxBatchTexts  = 128
	MaxBatchTokens = 100_000

	// ItemBodyChars is how much cleaned body goes in with the title
	// (about 400 tokens).
	ItemBodyChars = 1200
)

// Config selects and tunes the provider.
type Config struct {
	Provider string // voyage | off | local
	Model    string // default voyage-4-lite
}

// New builds the Embedder for cfg. voyageClient may be nil only for
// the off provider.
func New(cfg Config, client *voyage.Client) (Embedder, error) {
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "voyage":
		if client == nil {
			return nil, errors.New("embed: voyage provider needs a client")
		}
		return &Voyage{Client: client, model: model, dims: DefaultDims}, nil
	case "off":
		return Off{}, nil
	case "local":
		// Decision (D60): a local ONNX multilingual-e5-small provider via
		// knights-analytics/hugot is deferred - it needs cgo, the
		// onnxruntime shared library in the image and a larger Fly
		// machine. The seam is this switch; nothing else changes.
		return nil, ErrNotImplemented
	default:
		return nil, fmt.Errorf("embed: unknown provider %q (want voyage|off)", cfg.Provider)
	}
}

// Off is the disabled provider.
type Off struct{}

func (Off) Embed(context.Context, []string, InputKind) ([][]float32, error) {
	return nil, ErrDisabled
}
func (Off) Model() string { return "off" }
func (Off) Dims() int     { return 0 }

// Voyage embeds through Voyage's /v1/embeddings.
type Voyage struct {
	Client *voyage.Client
	model  string
	dims   int
}

func (v *Voyage) Model() string { return v.model }
func (v *Voyage) Dims() int     { return v.dims }

// Embed batches texts (<=128 per request, <=100k estimated tokens) and
// returns unit-length vectors in input order.
func (v *Voyage) Embed(ctx context.Context, texts []string, kind InputKind) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, batch := range Batches(texts) {
		res, err := v.Client.Embed(ctx, voyage.EmbedRequest{
			Model:           v.model,
			Input:           batch,
			InputType:       string(kind),
			OutputDimension: v.dims,
		})
		if err != nil {
			return nil, err
		}
		for _, vec := range res.Vectors {
			if len(vec) != v.dims {
				return nil, fmt.Errorf("embed: model %s returned %d dims, want %d", v.model, len(vec), v.dims)
			}
			out = append(out, Normalize(vec))
		}
	}
	return out, nil
}

// Batches splits texts into groups of at most MaxBatchTexts and
// MaxBatchTokens estimated tokens. A single oversized text still gets
// its own batch (truncation is the caller's job: ItemText caps it).
func Batches(texts []string) [][]string {
	var out [][]string
	var cur []string
	tokens := 0
	for _, t := range texts {
		n := voyage.EstimateTokens(t)
		if len(cur) > 0 && (len(cur) >= MaxBatchTexts || tokens+n > MaxBatchTokens) {
			out = append(out, cur)
			cur, tokens = nil, 0
		}
		cur = append(cur, t)
		tokens += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// Normalize scales v to unit length (a zero vector is returned as is).
func Normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

// ItemText is the document input for an item: the title plus the first
// ItemBodyChars characters of the cleaned body.
func ItemText(title, body string) string {
	body = strings.TrimSpace(body)
	if r := []rune(body); len(r) > ItemBodyChars {
		body = string(r[:ItemBodyChars])
	}
	if body == "" {
		return strings.TrimSpace(title)
	}
	return strings.TrimSpace(title) + "\n" + body
}

// Literal formats a vector as the pgvector '[f,f,...]' text literal.
func Literal(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%g", x)
	}
	b.WriteByte(']')
	return b.String()
}
