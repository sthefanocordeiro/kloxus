package embed

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/voyage"
)

func TestBatchesRespectTextAndTokenCaps(t *testing.T) {
	var texts []string
	for i := 0; i < 300; i++ {
		texts = append(texts, "x")
	}
	for _, b := range Batches(texts) {
		if len(b) > MaxBatchTexts {
			t.Fatalf("batch of %d texts", len(b))
		}
	}
	if got := len(Batches(texts)); got != 3 {
		t.Fatalf("batches = %d, want 3", got)
	}
	big := strings.Repeat("a", 3*60_000) // ~60k tokens each
	bs := Batches([]string{big, big, big})
	if len(bs) != 3 {
		t.Fatalf("token cap: %d batches, want 3", len(bs))
	}
}

func TestNormalizeUnitLength(t *testing.T) {
	v := Normalize([]float32{3, 4})
	var sum float64
	for _, x := range v {
		sum += float64(x * x)
	}
	if math.Abs(sum-1) > 1e-6 {
		t.Fatalf("norm^2 = %v", sum)
	}
}

func TestItemTextCapsBody(t *testing.T) {
	got := ItemText("Título", strings.Repeat("é", 5000))
	if n := len([]rune(got)); n != len([]rune("Título\n"))+ItemBodyChars {
		t.Fatalf("len = %d", n)
	}
}

func TestOffAndLocalProviders(t *testing.T) {
	e, err := New(Config{Provider: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), []string{"x"}, Document); !errors.Is(err, ErrDisabled) {
		t.Fatalf("off: %v", err)
	}
	if _, err := New(Config{Provider: "local"}, nil); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("local: %v", err)
	}
	if _, err := New(Config{Provider: "nope"}, nil); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestVoyageEmbedderSendsContractAndNormalizes(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		vec := make([]float32, 512)
		vec[0], vec[1] = 3, 4
		json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"index": 0, "embedding": vec}},
			"usage": map[string]any{"total_tokens": 5},
		})
	}))
	defer srv.Close()
	e, err := New(Config{}, &voyage.Client{Key: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if e.Model() != "voyage-4-lite" || e.Dims() != 512 {
		t.Fatalf("%s %d", e.Model(), e.Dims())
	}
	out, err := e.Embed(context.Background(), []string{"oi"}, Query)
	if err != nil {
		t.Fatal(err)
	}
	if body["input_type"] != "query" || body["output_dimension"] != float64(512) || body["model"] != "voyage-4-lite" {
		t.Fatalf("body = %v", body)
	}
	if math.Abs(float64(out[0][0])-0.6) > 1e-6 || math.Abs(float64(out[0][1])-0.8) > 1e-6 {
		t.Fatalf("not normalized: %v", out[0][:2])
	}
}

// ---- worker / backfill on fakes (zero paid calls) ----

type fakeStore struct {
	pending []Pending // newest first
	saved   []int64
	chars   int64
}

func (f *fakeStore) PendingEmbeddings(_ context.Context, _ string, _ time.Time, limit int) ([]Pending, error) {
	n := len(f.pending)
	if n > limit {
		n = limit
	}
	return append([]Pending(nil), f.pending[:n]...), nil
}
func (f *fakeStore) PendingStats(context.Context, string, time.Time) (int, int64, error) {
	return len(f.pending), f.chars, nil
}
func (f *fakeStore) SaveItemEmbeddings(_ context.Context, _ string, _ int, rows []Vector) error {
	for _, r := range rows {
		f.saved = append(f.saved, r.ItemID)
		for i, p := range f.pending {
			if p.ID == r.ItemID {
				f.pending = append(f.pending[:i], f.pending[i+1:]...)
				break
			}
		}
	}
	return nil
}

type fakeEmbedder struct {
	calls int
	failAt int // fail the Nth call (1-based), 0 = never
}

func (f *fakeEmbedder) Model() string { return "fake" }
func (f *fakeEmbedder) Dims() int     { return 2 }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string, _ InputKind) ([][]float32, error) {
	f.calls++
	if f.failAt == f.calls {
		return nil, voyage.ErrCapReached
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}

func pend(n int) []Pending {
	var p []Pending
	for i := n; i >= 1; i-- { // newest = highest id first
		p = append(p, Pending{ID: int64(i), Title: "t", Body: "b"})
	}
	return p
}

func TestBackfillNewestFirstAndResumable(t *testing.T) {
	st := &fakeStore{pending: pend(300), chars: 300 * 100}
	emb := &fakeEmbedder{failAt: 2}
	w := &Worker{Store: st, Embedder: emb}
	var lines []string
	n, err := w.Backfill(context.Background(), 7, func(s string) { lines = append(lines, s) })
	if !errors.Is(err, voyage.ErrCapReached) || n != 128 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if st.saved[0] != 300 {
		t.Fatalf("not newest-first: first saved %d", st.saved[0])
	}
	// Resume: the rest is still pending and gets finished.
	emb.failAt = 0
	n2, err := w.Backfill(context.Background(), 7, func(string) {})
	if err != nil || n+n2 != 300 || len(st.pending) != 0 {
		t.Fatalf("resume n2=%d err=%v left=%d", n2, err, len(st.pending))
	}
	if !strings.Contains(lines[0], "projected list cost") {
		t.Fatalf("no projection line: %v", lines)
	}
}

func TestBackfillRefusesAboveTenCents(t *testing.T) {
	// 6M tokens at $0.02/M = $0.12 > $0.10  (18M chars / 3)
	st := &fakeStore{pending: pend(10), chars: 18_000_000}
	emb := &fakeEmbedder{}
	w := &Worker{Store: st, Embedder: emb}
	_, err := w.Backfill(context.Background(), 7, func(string) {})
	if !errors.Is(err, ErrOverBudget) || emb.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, emb.calls)
	}
}

func TestOffProviderStopsDrainWithoutStoring(t *testing.T) {
	st := &fakeStore{pending: pend(5)}
	w := &Worker{Store: st, Embedder: Off{}}
	if _, err := w.Drain(context.Background(), time.Now(), nil); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v", err)
	}
	if len(st.saved) != 0 {
		t.Fatal("stored vectors with provider off")
	}
}

func TestKickNeverBlocks(t *testing.T) {
	w := &Worker{}
	w.Start()
	for i := 0; i < 10; i++ {
		w.Kick() // channel cap 1: extra kicks coalesce, none block
	}
	var nilWorker *Worker
	nilWorker.Kick()
}
