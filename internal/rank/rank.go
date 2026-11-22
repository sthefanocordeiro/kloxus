// Package rank is stage 1 of the ranking engine (KLOX-109, epic
// KLOX-107 section 5): pure math over unit vectors. The pipeline loads
// vectors, this package scores - no I/O, no clock reads, no paid calls,
// so it is unit-testable and fast (well under 300 ms for 500 stories).
package rank

import (
	"math"
	"strings"
	"unicode"
)

// Config holds every stage-1 weight and threshold in one place - the
// pipeline carries no magic numbers (spec section 5).
type Config struct {
	// Rocchio profile: theme + accepted - rejected.
	ThemeWeight    float64
	AcceptedWeight float64
	RejectedWeight float64

	// score = Rel*rel + Fresh*fresh + Signal*signal + Source*source - Skip*skip
	Rel, Fresh, Signal, Source, Skip float64

	// SkipOffset is subtracted from cos(story, skip_vec) before the
	// penalty applies: only strong similarity to the skip list counts.
	SkipOffset float64
	// FreshTauHours is the exp(-age/tau) decay constant.
	FreshTauHours float64
	// UndatedFresh is the freshness of a story with no parseable date.
	UndatedFresh float64
	// DefaultSourceWeight is used until sources carry their own weight.
	DefaultSourceWeight float64

	// RelFloor: stories with rel below it sit behind the off-topic
	// filter (calibration recorded in docs/decisions.md D61). It applies
	// to embedding cosines only: FTS rel is scaled within the run, so it
	// ranks but has no absolute scale to cut on.
	RelFloor float64
	// EnglishPenalty is subtracted from the score of English stories in
	// a Portuguese publication, which are also hidden by default.
	EnglishPenalty float64

	// MaxMembers caps the member vectors averaged into a story vector.
	MaxMembers int
}

// DefaultConfig returns the spec's defaults.
func DefaultConfig() Config {
	return Config{
		ThemeWeight: 1.0, AcceptedWeight: 0.75, RejectedWeight: 0.25,
		Rel: 0.60, Fresh: 0.15, Signal: 0.15, Source: 0.10, Skip: 1.0,
		SkipOffset: 0.30, FreshTauHours: 48, UndatedFresh: 0.5,
		DefaultSourceWeight: 0.5,
		RelFloor:            0.25, EnglishPenalty: 0.10,
		MaxMembers: 12,
	}
}

// Cos is the cosine similarity of a and b (0 for empty, mismatched or
// zero vectors). Inputs need not be normalized.
func Cos(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func normalize(v []float64) []float32 {
	var s float64
	for _, x := range v {
		s += x * x
	}
	out := make([]float32, len(v))
	if s == 0 {
		return out
	}
	inv := 1 / math.Sqrt(s)
	for i, x := range v {
		out[i] = float32(x * inv)
	}
	return out
}

// Mean returns the normalized mean of vs, ignoring empty vectors and
// stopping at max members (max <= 0 means no cap). nil when none count.
func Mean(vs [][]float32, max int) []float32 {
	var sum []float64
	n := 0
	for _, v := range vs {
		if len(v) == 0 || (len(sum) > 0 && len(v) != len(sum)) {
			continue
		}
		if max > 0 && n >= max {
			break
		}
		if sum == nil {
			sum = make([]float64, len(v))
		}
		for i, x := range v {
			sum[i] += float64(x)
		}
		n++
	}
	if n == 0 {
		return nil
	}
	return normalize(sum)
}

// Profile is the Rocchio publication profile:
// normalize(theme*1.0 + accepted*0.75 - rejected*0.25). Absent terms
// (nil) drop out; with no theme the result is nil.
func (c Config) Profile(theme, accepted, rejected []float32) []float32 {
	if len(theme) == 0 {
		return nil
	}
	sum := make([]float64, len(theme))
	add := func(v []float32, w float64) {
		if len(v) != len(sum) {
			return
		}
		for i, x := range v {
			sum[i] += w * float64(x)
		}
	}
	add(theme, c.ThemeWeight)
	add(accepted, c.AcceptedWeight)
	add(rejected, -c.RejectedWeight)
	return normalize(sum)
}

// Story is one candidate's stage-1 input.
type Story struct {
	Vec        []float32 // story vector (nil: no embedding yet, rel falls back to FTS)
	FTS        float64   // ts_rank_cd scaled 0-1 within the run (fallback rel)
	AgeHours   float64   // < 0 means undated
	Outlets    int       // distinct outlets covering the story
	SourceW    float64   // 0-1; <= 0 uses the default
	English    bool      // detected English text
	PTPub      bool      // publication language is Portuguese
	SectionVec []float32 // optional section vector
}

// Profiles are the per-publication vectors stage 1 scores against.
type Profiles struct {
	Rocchio []float32 // output of Config.Profile
	Skip    []float32 // optional skip_vec
}

// Hidden reasons carried on a Result and persisted on the story.
const (
	HiddenNone     = ""
	HiddenOffTopic = "off_topic"
	HiddenEnglish  = "english"
)

// Result is the stage-1 verdict for one story.
type Result struct {
	Rel      float64
	Section  float64
	Fresh    float64
	Signal   float64
	Skip     float64
	Score    float64
	Fallback bool   // rel came from FTS, not embeddings
	Hidden   string // HiddenNone, HiddenOffTopic or HiddenEnglish
}

// Score applies the section 5 formula to one story.
func (c Config) Score(p Profiles, s Story) Result {
	var r Result
	if len(s.Vec) > 0 && len(p.Rocchio) > 0 {
		r.Rel = Cos(s.Vec, p.Rocchio)
	} else {
		r.Rel, r.Fallback = clamp01(s.FTS), true
	}
	r.Section = r.Rel
	if len(s.SectionVec) > 0 && len(s.Vec) > 0 {
		r.Section = Cos(s.Vec, s.SectionVec)
	}
	if len(p.Skip) > 0 && len(s.Vec) > 0 {
		r.Skip = math.Max(0, Cos(s.Vec, p.Skip)-c.SkipOffset)
	}
	r.Fresh = c.UndatedFresh
	if s.AgeHours >= 0 {
		r.Fresh = math.Exp(-s.AgeHours / c.FreshTauHours)
	}
	if s.Outlets > 0 {
		r.Signal = math.Min(1, math.Log2(1+float64(s.Outlets))/2)
	}
	sw := s.SourceW
	if sw <= 0 {
		sw = c.DefaultSourceWeight
	}
	r.Score = c.Rel*r.Rel + c.Fresh*r.Fresh + c.Signal*r.Signal + c.Source*sw - c.Skip*r.Skip
	switch {
	case s.PTPub && s.English:
		r.Score -= c.EnglishPenalty
		r.Hidden = HiddenEnglish
	case !r.Fallback && r.Rel < c.RelFloor:
		r.Hidden = HiddenOffTopic
	}
	return r
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// ScaleFTS maps raw ts_rank_cd values to 0-1 within the run (divide by
// the maximum). All-zero input stays zero.
func ScaleFTS(raw []float64) []float64 {
	var max float64
	for _, x := range raw {
		if x > max {
			max = x
		}
	}
	out := make([]float64, len(raw))
	if max <= 0 {
		return out
	}
	for i, x := range raw {
		out[i] = x / max
	}
	return out
}

var enStop = wordSet("the", "and", "of", "to", "in", "is", "for", "on", "with", "that", "this",
	"are", "was", "it", "as", "at", "by", "from", "an", "be", "has", "have", "its", "will",
	"how", "why", "what", "your", "you", "new", "not", "but", "about", "after", "can", "more")

var ptStop = wordSet("o", "a", "os", "as", "de", "da", "do", "das", "dos", "e", "em", "no", "na",
	"nos", "nas", "um", "uma", "para", "por", "com", "que", "se", "mais", "como", "sobre",
	"foi", "são", "ao", "pelo", "pela", "seu", "sua", "não", "é", "ser", "tem", "após", "entre")

func wordSet(ws ...string) map[string]bool {
	m := make(map[string]bool, len(ws))
	for _, w := range ws {
		m[w] = true
	}
	return m
}

// IsEnglish is the stopword-ratio language detector: English when
// English function words clearly outnumber Portuguese ones. Text with
// too few tokens (or no stopwords either way) is "unknown" and returns
// false, so absent evidence never hides a story.
func IsEnglish(text string) bool {
	toks := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	if len(toks) < 4 {
		return false
	}
	en, pt := 0, 0
	for _, t := range toks {
		if enStop[t] {
			en++
		}
		if ptStop[t] {
			pt++
		}
	}
	return en >= 2 && float64(en) >= 1.5*float64(pt) && float64(en)/float64(len(toks)) >= 0.08
}
