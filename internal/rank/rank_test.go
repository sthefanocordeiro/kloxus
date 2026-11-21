package rank

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// axis builds a unit vector pointing mostly along dimension d with a
// little deterministic noise - a stand-in for "a story about topic d".
func axis(d, dims int, noise float64, rng *rand.Rand) []float32 {
	v := make([]float64, dims)
	v[d] = 1
	for i := range v {
		v[i] += noise * (rng.Float64() - 0.5)
	}
	return normalize(v)
}

func TestCosAndMean(t *testing.T) {
	a := []float32{1, 0}
	if got := Cos(a, a); math.Abs(got-1) > 1e-9 {
		t.Fatalf("cos(a,a)=%v", got)
	}
	if got := Cos(a, []float32{0, 1}); math.Abs(got) > 1e-9 {
		t.Fatalf("orthogonal cos=%v", got)
	}
	if Cos(nil, a) != 0 || Cos(a, []float32{1}) != 0 {
		t.Fatal("empty or mismatched vectors must score 0")
	}
	m := Mean([][]float32{{1, 0}, {0, 1}}, 0)
	if math.Abs(Cos(m, []float32{1, 1})-1) > 1e-6 {
		t.Fatalf("mean not the bisector: %v", m)
	}
	if Mean(nil, 0) != nil {
		t.Fatal("mean of nothing is nil")
	}
	if got := Mean([][]float32{{1, 0}, {0, 1}, {0, 1}}, 1); Cos(got, []float32{1, 0}) < 0.999 {
		t.Fatalf("cap of 1 member ignored: %v", got)
	}
}

func TestRocchioProfile(t *testing.T) {
	c := DefaultConfig()
	theme := []float32{1, 0, 0}
	acc := []float32{0, 1, 0}
	rej := []float32{0, 0, 1}
	if got := c.Profile(theme, nil, nil); Cos(got, theme) < 0.9999 {
		t.Fatalf("theme-only profile drifted: %v", got)
	}
	p := c.Profile(theme, acc, rej)
	if !(p[1] > 0 && p[2] < 0) {
		t.Fatalf("accepted must pull, rejected must push away: %v", p)
	}
	var n float64
	for _, x := range p {
		n += float64(x) * float64(x)
	}
	if math.Abs(n-1) > 1e-5 {
		t.Fatalf("profile not unit length: %v", n)
	}
	if c.Profile(nil, acc, rej) != nil {
		t.Fatal("no theme, no profile")
	}
}

func TestScoreFormula(t *testing.T) {
	c := DefaultConfig()
	prof := Profiles{Rocchio: []float32{1, 0}}
	// rel=1, age 0 => fresh=1, 3 outlets => signal=min(1,log2(4)/2)=1, default source .5
	r := c.Score(prof, Story{Vec: []float32{1, 0}, AgeHours: 0, Outlets: 3})
	want := 0.60*1 + 0.15*1 + 0.15*1 + 0.10*0.5
	if math.Abs(r.Score-want) > 1e-9 || r.Hidden != HiddenNone || r.Fallback {
		t.Fatalf("score %v want %v (%+v)", r.Score, want, r)
	}
	// skip penalty only above the 0.30 offset
	prof.Skip = []float32{1, 0}
	r = c.Score(prof, Story{Vec: []float32{1, 0}, AgeHours: 0, Outlets: 3})
	if math.Abs(r.Score-(want-0.70)) > 1e-9 {
		t.Fatalf("skip penalty wrong: %v", r.Score)
	}
	// age decay
	r = c.Score(Profiles{Rocchio: []float32{1, 0}}, Story{Vec: []float32{1, 0}, AgeHours: 48})
	if math.Abs(r.Fresh-math.Exp(-1)) > 1e-9 {
		t.Fatalf("fresh %v", r.Fresh)
	}
	// undated sits at the midpoint
	r = c.Score(Profiles{Rocchio: []float32{1, 0}}, Story{Vec: []float32{1, 0}, AgeHours: -1})
	if r.Fresh != c.UndatedFresh {
		t.Fatalf("undated fresh %v", r.Fresh)
	}
}

func TestRelevanceFloorAndEnglish(t *testing.T) {
	c := DefaultConfig()
	prof := Profiles{Rocchio: []float32{1, 0}}
	off := c.Score(prof, Story{Vec: []float32{0.1, 1}, AgeHours: 1})
	if off.Hidden != HiddenOffTopic {
		t.Fatalf("rel %.2f should be below the floor: %+v", off.Rel, off)
	}
	on := c.Score(prof, Story{Vec: []float32{1, 0.1}, AgeHours: 1, PTPub: true})
	en := c.Score(prof, Story{Vec: []float32{1, 0.1}, AgeHours: 1, PTPub: true, English: true})
	if en.Hidden != HiddenEnglish || math.Abs((on.Score-en.Score)-c.EnglishPenalty) > 1e-9 {
		t.Fatalf("english penalty: on=%v en=%v hidden=%q", on.Score, en.Score, en.Hidden)
	}
	// English story in an English publication is not penalised.
	if r := c.Score(prof, Story{Vec: []float32{1, 0.1}, AgeHours: 1, English: true}); r.Hidden != "" {
		t.Fatalf("english pub must keep english stories: %+v", r)
	}
}

func TestFTSFallbackNeverFallsToDateOrder(t *testing.T) {
	c := DefaultConfig()
	// No vectors at all: rel comes from the scaled FTS rank, so an
	// on-theme old story must beat an off-theme fresh one.
	scaled := ScaleFTS([]float64{0.9, 0.0})
	onTheme := c.Score(Profiles{}, Story{FTS: scaled[0], AgeHours: 120, Outlets: 1})
	offTheme := c.Score(Profiles{}, Story{FTS: scaled[1], AgeHours: 1, Outlets: 1})
	if !onTheme.Fallback || onTheme.Score <= offTheme.Score {
		t.Fatalf("fallback must rank by theme: on=%+v off=%+v", onTheme, offTheme)
	}
	if offTheme.Hidden != HiddenNone {
		t.Fatalf("FTS rel has no absolute scale, so it never hides: %+v", offTheme)
	}
	if s := ScaleFTS([]float64{0, 0}); s[0] != 0 || s[1] != 0 {
		t.Fatal("all-zero stays zero")
	}
	if s := ScaleFTS([]float64{2, 1}); s[0] != 1 || s[1] != 0.5 {
		t.Fatalf("scaling: %v", s)
	}
}

func TestIsEnglish(t *testing.T) {
	cases := map[string]bool{
		"The company is launching a new product for the market":     true,
		"Banco Central anuncia novas regras para o Pix em 2026":     false,
		"O governo vai ampliar o crédito para as pequenas empresas": false,
		"GTA 6":                          false,
		"Stripe raises $1B":              false, // too little evidence: stays visible
		"What you need to know about it": true,
	}
	for text, want := range cases {
		if got := IsEnglish(text); got != want {
			t.Errorf("IsEnglish(%q)=%v want %v", text, got, want)
		}
	}
}

// TestRankByRealTheme is the stage-1 acceptance on a synthetic fixture:
// dims 0..3 are fintech, banking, games, infra. A fintech publication
// must put fintech/banking stories on top and keep "file server" out
// of the top 10 even though it is the freshest.
func TestRankByRealTheme(t *testing.T) {
	const dims = 16
	rng := rand.New(rand.NewSource(7))
	c := DefaultConfig()
	type story struct {
		title string
		topic int
		age   float64
		out   int
	}
	var stories []story
	for i := 0; i < 12; i++ {
		stories = append(stories, story{"fintech", 0, float64(24 + i*6), 1 + i%3})
	}
	for i := 0; i < 8; i++ {
		stories = append(stories, story{"banking", 1, float64(30 + i*5), 1 + i%2})
	}
	for i := 0; i < 15; i++ {
		stories = append(stories, story{"games", 2, float64(1 + i), 2})
	}
	stories = append(stories, story{"file server", 3, 0.5, 1})
	theme := axis(0, dims, 0.1, rng)
	banking := axis(1, dims, 0.1, rng)
	// the publication's theme blends fintech and banking
	prof := Profiles{Rocchio: c.Profile(Mean([][]float32{theme, banking}, 0), nil, nil)}
	type scored struct {
		title string
		res   Result
	}
	var out []scored
	for _, s := range stories {
		v := axis(s.topic, dims, 0.3, rng)
		out = append(out, scored{s.title, c.Score(prof, Story{Vec: v, AgeHours: s.age, Outlets: s.out})})
	}
	for i := range out { // simple selection sort, high score first
		for j := i + 1; j < len(out); j++ {
			if out[j].res.Score > out[i].res.Score {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	for i := 0; i < 10; i++ {
		if out[i].title != "fintech" && out[i].title != "banking" {
			t.Fatalf("rank %d is %q: off-topic story in the top 10", i+1, out[i].title)
		}
	}
	for _, o := range out {
		if o.title == "file server" && o.res.Hidden != HiddenOffTopic {
			t.Fatalf("file server must sit behind the off-topic filter: %+v", o.res)
		}
	}
}

func TestStage1Under300msFor500Stories(t *testing.T) {
	const dims = 512
	rng := rand.New(rand.NewSource(1))
	c := DefaultConfig()
	prof := Profiles{Rocchio: axis(0, dims, 0.2, rng), Skip: axis(1, dims, 0.2, rng)}
	vecs := make([][]float32, 500)
	for i := range vecs {
		vecs[i] = axis(i%dims, dims, 0.5, rng)
	}
	start := time.Now()
	for i := 0; i < 500; i++ {
		members := [][]float32{vecs[i], vecs[(i+1)%500], vecs[(i+2)%500]}
		c.Score(prof, Story{Vec: Mean(members, c.MaxMembers), AgeHours: float64(i % 168), Outlets: 1 + i%5})
	}
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Fatalf("stage 1 took %v for 500 stories (budget 300ms)", d)
	}
}
