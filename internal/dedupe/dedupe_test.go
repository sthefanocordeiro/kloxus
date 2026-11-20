package dedupe

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
)

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.example.com/a/?utm_source=feed&utm_medium=rss": "https://example.com/a",
		"https://EXAMPLE.com/A#comments":                            "https://example.com/A",
		"https://example.com/a?fbclid=xyz&id=3":                     "https://example.com/a?id=3",
		"https://example.com/":                                      "https://example.com",
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

var day = 24 * time.Hour

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, "2006-01-02"} {
		if tm, err := time.Parse(layout, s); err == nil {
			return tm
		}
	}
	t.Fatalf("unparseable test date %q", s)
	return time.Time{}
}

// corpusBody is a real-length article body (≥40 non-stopword tokens)
// from the fixture corpus, so SimHash-gated tests exercise the same
// text shape production sees.
const corpusBody = "O regulador do trabalho abriu consulta pública sobre o uso de sistemas automatizados em processos de contratação. A minuta exige que empresas informem candidatos quando currículos forem triados por IA e prevê auditorias anuais para organizações com mais de 100 funcionários. Comentários podem ser enviados até 15 de dezembro pelo portal do órgão. Duas associações empresariais já pediram prazo maior de transição. A norma deve entrar em vigor no segundo semestre de 2027."

const otherBody = "A emenda aprovada em comissão estende regras de trabalho remoto criadas na pandemia a contratos anteriores a 2020, incluindo auxílio para internet e ergonomia e direito a desconexão fora do expediente. Sindicatos comemoram; entidades patronais temem aumento de litígios. O texto ainda precisa passar pelo plenário e pela câmara alta."

func TestAssignMergesIdenticalText(t *testing.T) {
	urls := []string{
		"https://a.example/story?utm_source=x",
		"https://a.example/story",
		"https://b.example/other",
		"https://c.example/same-story-different-outlet",
	}
	texts := []string{
		corpusBody,
		corpusBody,
		otherBody,
		corpusBody + " (Republicação com correção de data.)",
	}
	now := mustTime(t, "2026-11-09T12:00:00Z")
	published := []time.Time{now, now, now, now.Add(2 * time.Hour)}
	got := Assign(urls, texts, published)
	if got[0] != got[1] {
		t.Error("same canonical URL not merged")
	}
	if got[0] != got[3] {
		t.Error("near-identical text not merged")
	}
	if got[2] == got[0] {
		t.Error("different story merged incorrectly")
	}
}

func TestSimHashDistance(t *testing.T) {
	a := SimHash(corpusBody)
	b := SimHash(corpusBody + " (Republicação com correção de data.)")
	c := SimHash(otherBody)
	if hamming(a, b) > HammingThreshold {
		t.Errorf("near-identical texts at distance %d", hamming(a, b))
	}
	if hamming(a, c) <= HammingThreshold {
		t.Error("unrelated texts too close")
	}
}

// TestAssignMinTokens: short items — signal headlines, one-line
// descriptions — must not merge on SimHash alone, no matter how
// identical the text is.
func TestAssignMinTokens(t *testing.T) {
	short := "Startup levanta rodada de investimento para expansão internacional."
	urls := []string{"https://a.example/1", "https://b.example/2", "https://c.example/3"}
	texts := []string{short, short, short}
	now := mustTime(t, "2026-11-09")
	got := Assign(urls, texts, []time.Time{now, now, now})
	if got[0] == got[1] || got[1] == got[2] {
		t.Errorf("short identical texts merged: %v", got)
	}
}

// TestAssignWindow: near-identical texts merge only inside the 72 h
// publication window; further apart they are separate stories.
func TestAssignWindow(t *testing.T) {
	base := mustTime(t, "2026-11-09T12:00:00Z")
	urls := []string{"https://a.example/x", "https://b.example/y", "https://c.example/z"}
	texts := []string{corpusBody, corpusBody, corpusBody}
	got := Assign(urls, texts, []time.Time{base, base.Add(70 * time.Hour), base.Add(4 * day)})
	if got[0] != got[1] {
		t.Error("items 70 h apart should merge")
	}
	if got[0] == got[2] || got[1] == got[2] {
		t.Error("items 4 days apart should not merge")
	}
}

// TestAssignNoTransitiveChaining: an item joins a cluster only if it is
// within the threshold of the cluster's FIRST item. B sits within the
// threshold of A and C sits within the threshold of B but not of A —
// under union-find all three would chain into one cluster.
func TestAssignNoTransitiveChaining(t *testing.T) {
	var a uint64 = 0xAAAAAAAAAAAAAAAA
	b := a ^ 0x3  // distance 2 from A
	c := b ^ 0x3C // distance 4 from B, 6 from A
	if hamming(b, c) > HammingThreshold || hamming(a, c) <= HammingThreshold {
		t.Fatalf("bad chain fixture: d(A,B)=%d d(B,C)=%d d(A,C)=%d",
			hamming(a, b), hamming(b, c), hamming(a, c))
	}
	now := mustTime(t, "2026-11-09")
	published := []time.Time{now, now, now}
	ntok := []int{50, 50, 50}
	sims := []uint64{a, b, c}
	got := assign([][]int{{0}, {1}, {2}}, sims, ntok, published, 3)
	if got[0] != got[1] {
		t.Fatal("B should join A's cluster")
	}
	if got[2] == got[0] {
		t.Fatalf("C chained into A's cluster via B (transitive merge); clusters=%v", got)
	}
}

// TestAssignClusterCap: a cluster never exceeds MaxClusterSize, for
// SimHash matches and exact canonical matches alike.
func TestAssignClusterCap(t *testing.T) {
	now := mustTime(t, "2026-11-09")

	// 15 identical texts on distinct URLs → 12 + 3.
	n := 15
	urls := make([]string, n)
	texts := make([]string, n)
	published := make([]time.Time, n)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://outlet-%d.example/story", i)
		texts[i] = corpusBody
		published[i] = now
	}
	got := Assign(urls, texts, published)
	counts := map[int]int{}
	for _, c := range got {
		counts[c]++
	}
	for c, k := range counts {
		if k > MaxClusterSize {
			t.Fatalf("cluster %d has %d members, cap is %d", c, k, MaxClusterSize)
		}
	}
	if len(counts) < 2 {
		t.Fatalf("15 near-identical items produced %d cluster(s), want at least 2", len(counts))
	}

	// 15 rows on the same canonical URL → 12 + 3 as well.
	for i := range urls {
		urls[i] = "https://same.example/story"
	}
	got = Assign(urls, texts, published)
	counts = map[int]int{}
	for _, c := range got {
		counts[c]++
	}
	for c, k := range counts {
		if k > MaxClusterSize {
			t.Fatalf("canonical cluster %d has %d members, cap is %d", c, k, MaxClusterSize)
		}
	}
	if len(counts) != 2 {
		t.Fatalf("15 same-URL items produced %d clusters, want 2", len(counts))
	}
}

// TestAssignScalesAndStaysSmall is the acceptance guard: 5,000
// synthetic unrelated items must not produce a cluster larger than 2,
// and the whole assignment must finish far under the 5 s the hosted
// machine size allows.
func TestAssignScalesAndStaysSmall(t *testing.T) {
	const n = 5000
	rng := rand.New(rand.NewSource(1))
	vocab := []string{"regulador", "startup", "chip", "satélite", "banco", "emenda", "dobrável", "cripto", "streaming", "drone", "consulta", "modelo", "pesquisa", "mercado", "reembolso", "bateria", "depurador", "assinatura", "fábrica", "licença"}
	urls := make([]string, n)
	texts := make([]string, n)
	published := make([]time.Time, n)
	base := mustTime(t, "2026-11-09")
	for i := 0; i < n; i++ {
		if i%100 == 99 {
			urls[i] = urls[i-1] // exact-duplicate pairs: clusters of 2 are allowed
		} else {
			urls[i] = fmt.Sprintf("https://outlet-%d.example/item-%d", i%97, i)
		}
		// 60-token unrelated word salads: eligible for SimHash, but
		// chance matches must stay singletons.
		words := make([]string, 0, 60)
		for w := 0; w < 60; w++ {
			words = append(words, vocab[rng.Intn(len(vocab))], fmt.Sprintf("w%d", rng.Intn(100000)))
		}
		texts[i] = fmt.Sprintf("Item %d\n", i) + strings.Join(words, " ")
		published[i] = base.Add(time.Duration(rng.Intn(7*24)) * time.Hour)
	}
	start := time.Now()
	got := Assign(urls, texts, published)
	elapsed := time.Since(start)
	counts := map[int]int{}
	for _, c := range got {
		counts[c]++
	}
	maxSize := 0
	for _, k := range counts {
		if k > maxSize {
			maxSize = k
		}
	}
	if maxSize > 2 {
		t.Fatalf("largest cluster has %d members, want <= 2", maxSize)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Assign over %d items took %s, want < 5s", n, elapsed)
	}
	t.Logf("Assign over %d items: %d clusters, max %d, %s", n, len(counts), maxSize, elapsed)
}

// TestCorpusDedupe runs the real fixture corpus: 35 items should collapse
// to 32 stories — the 3-way literal republish (AI-hiring consultation) and
// the remote-work republish merge; same-event rewrites stay separate.
func TestCorpusDedupe(t *testing.T) {
	f := sources.FixtureFetcher{FS: examples.FS}
	var items []struct {
		url, text string
		pub       time.Time
	}
	for _, src := range []struct{ typ, url string }{
		{"rss", "fixtures/feeds/radar-tech.xml"},
		{"rss", "fixtures/feeds/pulso-digital.xml"},
		{"rss", "fixtures/feeds/correio-do-codigo.xml"},
		{"page", "fixtures/pages/observatorio-aberto.html"},
	} {
		raw, err := f.Fetch(context.Background(), src.typ, src.url)
		if err != nil {
			t.Fatalf("fetch %s: %v", src.url, err)
		}
		for _, it := range raw {
			pub, _ := parseFeedDate(it.PublishedAt)
			items = append(items, struct {
				url, text string
				pub       time.Time
			}{it.URL, it.ContentText, pub})
		}
	}
	if len(items) != 35 {
		t.Fatalf("corpus has %d items, want 35", len(items))
	}
	urls := make([]string, len(items))
	texts := make([]string, len(items))
	published := make([]time.Time, len(items))
	for i, it := range items {
		urls[i], texts[i], published[i] = it.url, it.text, it.pub
	}
	clusters := Assign(urls, texts, published)
	seen := map[int]bool{}
	for _, c := range clusters {
		seen[c] = true
	}
	if len(seen) != 32 {
		counts := map[int]int{}
		for _, c := range clusters {
			counts[c]++
		}
		for c, n := range counts {
			if n > 1 {
				t.Logf("cluster %d has %d items", c, n)
			}
		}
		t.Fatalf("corpus produced %d stories, want 32", len(seen))
	}
}

// parseFeedDate reads the fixture item dates (RFC3339 from feeds, a bare
// date from the page fixture) leniently for the corpus test.
func parseFeedDate(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, "2006-01-02"} {
		if tm, err := time.Parse(layout, s); err == nil {
			return tm, true
		}
	}
	return time.Time{}, false
}
