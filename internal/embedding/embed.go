// Package embedding implements the deterministic local semantic layer
// (KLOX-93): a feature-hashing text embedder and a lexicon classifier,
// both free of any model call. Embeddings are 256-dim float32 vectors
// stored in item_embedding (pgvector when the extension is present,
// TEXT otherwise); cosine similarity runs in Go or via pgvector's <=>
// when available.
package embedding

import (
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Dim is the embedding width. It is part of the stored format — a
// dimension change needs a new model tag so stale rows re-embed.
const Dim = 256

// Model is the version tag written on item_embedding and on the local
// classification rows in item_enrichment. Bumping it makes the sweep
// recompute every stored vector/label.
const Model = "local-hash-v1"

// ClassifyModel marks deterministic classification rows in
// item_enrichment so the AI sweep can tell them apart from its own
// Haiku rows and upgrade them later.
const ClassifyModel = "local-classify-v1"

var foldAccents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// Tokenize folds text to lowercase, strips accents and splits on
// non-letter/non-digit boundaries — the same fold the deals list and
// the topic matcher use, so embeddings and labels see the same words.
func Tokenize(s string) []string {
	return strings.FieldsFunc(foldAccents.Replace(strings.ToLower(s)),
		func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// Embed returns the L2-normalized hashing embedding of a text:
// word unigrams weighted 1.0 and adjacent bigrams 0.6, hashed into
// Dim signed buckets. Deterministic — identical input always produces
// the identical vector, which is what makes the stored rows idempotent
// and the coverage test replayable with zero paid calls.
func Embed(text string) []float32 {
	var v [Dim]float32
	toks := Tokenize(text)
	for i, t := range toks {
		add(&v, "u "+t, 1.0)
		if i+1 < len(toks) {
			add(&v, "b "+t+" "+toks[i+1], 0.6)
		}
	}
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return v[:]
	}
	out := make([]float32, Dim)
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out
}

func add(v *[Dim]float32, feature string, w float32) {
	h := fnv.New64a()
	h.Write([]byte(feature))
	sum := h.Sum64()
	sign := float32(1.0)
	if sum&(1<<63) != 0 {
		sign = -1.0
	}
	v[sum%Dim] += w * sign
}

// Cosine is the cosine similarity of two equal-length vectors; the
// embedder's output is unit-normalized so this is a dot product.
// Mismatched or empty inputs return 0.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

// Format renders a vector in the '[f,f,...]' literal both the
// pgvector type and the TEXT fallback column store.
func Format(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', 6, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// Parse reads a '[f,f,...]' literal back — the inverse of Format.
func Parse(s string) ([]float32, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil, fmt.Errorf("empty vector literal")
	}
	parts := strings.Split(s, ",")
	out := make([]float32, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil, fmt.Errorf("vector literal: %w", err)
		}
		out[i] = float32(f)
	}
	return out, nil
}
