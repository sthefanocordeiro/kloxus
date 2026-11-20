// Package dedupe merges exact and near-duplicate items: same canonical
// URL, or SimHash near-identical text. Clustering is deliberately
// bounded — a strict bit threshold, a minimum body length, a short
// recency window, no transitive chaining and a hard size cap — so
// unrelated items can never chain into one giant cluster. Grouping
// *different* articles about the same event is the storylist model's
// job, not this package's.
package dedupe

import (
	"hash/fnv"
	"math/bits"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

// HammingThreshold is the max bit distance for two items' SimHashes to
// merge. 4 is strict on purpose: the previous 16 let chance matches
// (~1 in 20,000 random pairs) chain thousands of unrelated items into
// a single cluster.
const HammingThreshold = 4

// MinContentTokens is the minimum non-stopword token count an item
// needs before SimHash may merge it. Short texts (signal headlines,
// link descriptions) produce unreliable fingerprints, so they only
// merge by exact canonical URL.
const MinContentTokens = 40

// MaxClusterSize caps the members of one cluster; beyond it a new
// cluster starts, so no story can grow without bound.
const MaxClusterSize = 12

// MaxMergeAge is the maximum distance between two items' publication
// times for a SimHash merge — republished copies land within days of
// each other; lookalikes further apart are a different story.
const MaxMergeAge = 72 * time.Hour

// Canonical normalizes a URL for duplicate detection: lowercase host,
// no www., no tracking params, no fragment, no trailing slash.
func Canonical(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	u.Host = strings.ToLower(u.Host)
	u.Host = strings.TrimPrefix(u.Host, "www.")
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "utm_") || lk == "fbclid" || lk == "gclid" || lk == "ref" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

var stopwords = map[string]bool{
	"a": true, "o": true, "e": true, "de": true, "do": true, "da": true,
	"em": true, "um": true, "uma": true, "para": true, "com": true,
	"que": true, "os": true, "as": true, "no": true, "na": true,
	"nos": true, "nas": true, "por": true, "se": true, "ao": true,
	"aos": true, "ou": true, "até": true, "the": true, "of": true,
	"to": true, "and": true, "in": true, "is": true, "on": true,
	"for": true, "an": true, "são": true, "dos": true, "das": true,
}

func tokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func contentTokens(text string) []string {
	var out []string
	for _, t := range tokens(text) {
		if !stopwords[t] {
			out = append(out, t)
		}
	}
	return out
}

// SimHash computes a 64-bit fingerprint over word 2-shingles so that
// near-identical texts land within HammingThreshold bits of each other.
func SimHash(text string) uint64 {
	var v [64]int
	toks := tokens(text)
	add := func(h uint64, w int) {
		for i := 0; i < 64; i++ {
			if h&(1<<i) != 0 {
				v[i] += w
			} else {
				v[i] -= w
			}
		}
	}
	hash := func(s string) uint64 {
		h := fnv.New64a()
		h.Write([]byte(s))
		return h.Sum64()
	}
	for i, t := range toks {
		if stopwords[t] {
			continue
		}
		add(hash(t), 1)
		if i+1 < len(toks) {
			add(hash(t+" "+toks[i+1]), 2)
		}
	}
	var out uint64
	for i := 0; i < 64; i++ {
		if v[i] > 0 {
			out |= 1 << i
		}
	}
	return out
}

func hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// Assign groups items into clusters by canonical URL or near-identical
// body text — pass each item's article body in texts, not its title: a
// republish's new headline must not defeat the match. published
// carries each item's effective publication time
// (published date, or fetched time when the date is unparseable); a
// zero value means the time is unknown and the item cannot SimHash-
// merge — it may still merge by canonical URL. Returns a cluster id
// per item (0..k-1). Clusters never exceed MaxClusterSize members.
func Assign(urls, texts []string, published []time.Time) []int {
	n := len(urls)

	// Exact-match units: items sharing a canonical URL are the same
	// article and stay together; oversized groups split into bounded
	// chunks so even an exact match respects the size cap.
	var units [][]int
	canonUnit := map[string]int{}
	for i, u := range urls {
		c := Canonical(u)
		if c == "" {
			units = append(units, []int{i})
			continue
		}
		if k, ok := canonUnit[c]; ok {
			units[k] = append(units[k], i)
		} else {
			canonUnit[c] = len(units)
			units = append(units, []int{i})
		}
	}
	var chunks [][]int
	for _, u := range units {
		for len(u) > MaxClusterSize {
			chunks = append(chunks, u[:MaxClusterSize])
			u = u[MaxClusterSize:]
		}
		chunks = append(chunks, u)
	}
	sort.Slice(chunks, func(a, b int) bool { return chunks[a][0] < chunks[b][0] })

	sims := make([]uint64, n)
	ntok := make([]int, n)
	for i := 0; i < n && i < len(texts); i++ {
		ntok[i] = len(contentTokens(texts[i]))
		if ntok[i] >= MinContentTokens {
			sims[i] = SimHash(texts[i])
		}
	}
	return assign(chunks, sims, ntok, published, n)
}

// assign clusters pre-chunked units by SimHash similarity of each
// unit's first item. An item joins a cluster only when the unit's
// first item sits within HammingThreshold of the cluster's first
// item — comparisons are never made against later members, so no
// transitive chain can form and no union-find is needed.
func assign(units [][]int, sims []uint64, ntok []int, published []time.Time, n int) []int {
	clusterOf := make([]int, n)
	reps := []int{}        // cluster id → first item index
	clusterSize := []int{} // cluster id → member count
	repCluster := map[int]int{}

	// SimHash LSH: split the 64-bit hash into 5 bands (13,13,13,13,12
	// bits). Two hashes at distance <= 4 differ in at most 4 bands, so
	// they agree on at least one — bucket candidates by band value and
	// the comparison set is never all pairs.
	var buckets [5]map[uint64][]int
	for b := range buckets {
		buckets[b] = map[uint64][]int{}
	}
	band := func(h uint64, b int) uint64 {
		if b < 4 {
			return (h >> (13 * b)) & 0x1FFF
		}
		return h >> 52
	}

	pub := func(i int) time.Time {
		if i < len(published) {
			return published[i]
		}
		return time.Time{}
	}
	withinWindow := func(a, b int) bool {
		ta, tb := pub(a), pub(b)
		if ta.IsZero() || tb.IsZero() {
			return false
		}
		d := ta.Sub(tb)
		if d < 0 {
			d = -d
		}
		return d <= MaxMergeAge
	}

	for _, u := range units {
		rep := u[0]
		cid := -1
		if ntok[rep] >= MinContentTokens {
			var cands []int
			for b := 0; b < 5; b++ {
				cands = append(cands, buckets[b][band(sims[rep], b)]...)
			}
			sort.Ints(cands) // rep index order == cluster creation order
			last := -1
			for _, r := range cands {
				if r == last {
					continue
				}
				last = r
				k := repCluster[r]
				if clusterSize[k]+len(u) > MaxClusterSize ||
					!withinWindow(rep, r) ||
					hamming(sims[rep], sims[r]) > HammingThreshold {
					continue
				}
				cid = k
				break
			}
		}
		if cid < 0 {
			cid = len(reps)
			reps = append(reps, rep)
			clusterSize = append(clusterSize, 0)
			repCluster[rep] = cid
			if ntok[rep] >= MinContentTokens {
				for b := 0; b < 5; b++ {
					key := band(sims[rep], b)
					buckets[b][key] = append(buckets[b][key], rep)
				}
			}
		}
		clusterSize[cid] += len(u)
		for _, i := range u {
			clusterOf[i] = cid
		}
	}
	return clusterOf
}
