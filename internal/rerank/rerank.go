// Package rerank is stage 2 of the ranking engine (KLOX-110, epic
// KLOX-107 section 6): a cross-encoder reranker over the stage-1
// shortlist. It is pure plumbing plus math - the pipeline decides when
// to call it (once per run, never on page load) and treats every
// failure as "keep the stage-1 order".
package rerank

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/voyage"
)

const (
	DefaultModel = "rerank-2.5-lite"
	DefaultK     = 100
	// Timeout bounds the one request a run makes (spec section 6).
	Timeout = 10 * time.Second
	// QueryMaxTokens / DocBodyChars are the spec's size limits.
	QueryMaxTokens = 150
	DocBodyChars   = 600
)

// ErrDisabled is what the "off" provider returns; callers keep the
// stage-1 order and never surface it.
var ErrDisabled = errors.New("rerank: provider is off")

// Scored is one document's relevance, by original index.
type Scored struct {
	Index int
	Score float64
}

// Reranker scores docs against a query. topK<=0 scores all of them.
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []string, topK int) ([]Scored, error)
	Model() string
}

// Config selects and tunes the provider.
type Config struct {
	Provider string // voyage | off
	Model    string // default rerank-2.5-lite
}

// New builds the Reranker for cfg. A nil client with provider voyage is
// an error so the caller can log and run without a reranker.
func New(cfg Config, client *voyage.Client) (Reranker, error) {
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultModel
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "off":
		return offReranker{model: model}, nil
	case "", "voyage":
		if client == nil {
			return nil, errors.New("rerank: voyage provider needs a client")
		}
		return &voyageReranker{c: client, model: model}, nil
	}
	return nil, fmt.Errorf("rerank: unknown provider %q", cfg.Provider)
}

type offReranker struct{ model string }

func (offReranker) Rerank(context.Context, string, []string, int) ([]Scored, error) {
	return nil, ErrDisabled
}
func (o offReranker) Model() string { return o.model }

type voyageReranker struct {
	c     *voyage.Client
	model string
}

func (v *voyageReranker) Model() string { return v.model }

func (v *voyageReranker) Rerank(ctx context.Context, query string, docs []string, topK int) ([]Scored, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	resp, err := v.c.Rerank(ctx, voyage.RerankRequest{Model: v.model, Query: query, Documents: docs, TopK: topK})
	if err != nil {
		return nil, err
	}
	out := make([]Scored, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Index < 0 || r.Index >= len(docs) {
			return nil, errors.New("rerank: result index out of range")
		}
		out = append(out, Scored{Index: r.Index, Score: r.Score})
	}
	return out, nil
}

// Query is the Portuguese query template (spec section 6), capped at
// QueryMaxTokens by trimming the longest free-text part first.
func Query(theme, line, priorities, skip string) string {
	parts := []*string{&theme, &line, &priorities, &skip}
	for i := range parts {
		*parts[i] = strings.Join(strings.Fields(*parts[i]), " ")
	}
	build := func() string {
		var b strings.Builder
		fmt.Fprintf(&b, "Newsletter sobre %s.", theme)
		if line != "" {
			fmt.Fprintf(&b, " Linha editorial: %s.", strings.TrimRight(line, ". "))
		}
		if priorities != "" {
			fmt.Fprintf(&b, " Priorize: %s.", strings.TrimRight(priorities, ". "))
		}
		if skip != "" {
			fmt.Fprintf(&b, " Ignore: %s.", strings.TrimRight(skip, ". "))
		}
		b.WriteString(" Qual notícia é relevante para esta newsletter?")
		return b.String()
	}
	q := build()
	for voyage.EstimateTokens(q) > QueryMaxTokens {
		longest := parts[0]
		for _, p := range parts[1:] {
			if len([]rune(*p)) > len([]rune(*longest)) {
				longest = p
			}
		}
		r := []rune(*longest)
		if len(r) <= 20 {
			break
		}
		*longest = strings.TrimSpace(string(r[:len(r)*3/4]))
		q = build()
	}
	return q
}

// Doc is the document text for one story: title plus the first
// DocBodyChars characters of the earliest item's body.
func Doc(title, body string) string {
	body = strings.Join(strings.Fields(body), " ")
	if r := []rune(body); len(r) > DocBodyChars {
		body = string(r[:DocBodyChars])
	}
	title = strings.TrimSpace(title)
	if body == "" {
		return title
	}
	return title + "\n" + body
}

// Blend is rel_final = 0.7*rerank + 0.3*rel (spec section 6).
const (
	RerankWeight = 0.7
	RelWeight    = 0.3
)

func Blend(rerankScore, rel float64) float64 {
	return RerankWeight*rerankScore + RelWeight*rel
}
