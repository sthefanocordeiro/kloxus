// Package dedupe merges exact and near-duplicate items: same canonical
// URL, or SimHash near-identical text. Grouping *different* articles about
// the same event is the storylist model's job, not this package's.
package dedupe

import (
	"hash/fnv"
	"math/bits"
	"net/url"
	"strings"
	"unicode"
)

// HammingThreshold is the max bit distance for two items to be merged.
// 16 tolerates republished items that gain an outlet-specific footer
// while still separating unrelated short news items.
const HammingThreshold = 16

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
// text. Returns a cluster id per item (0..k-1).
func Assign(urls, texts []string) []int {
	n := len(urls)
	cluster := make([]int, n)
	for i := range cluster {
		cluster[i] = -1
	}
	byCanonical := map[string]int{}
	next := 0
	var sims []uint64
	for i := range texts {
		sims = append(sims, SimHash(texts[i]))
	}
	var parent []int
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	parent = make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for i, u := range urls {
		c := Canonical(u)
		if c == "" {
			continue
		}
		if j, ok := byCanonical[c]; ok {
			union(i, j)
		} else {
			byCanonical[c] = i
		}
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if hamming(sims[i], sims[j]) <= HammingThreshold {
				union(i, j)
			}
		}
	}
	roots := map[int]int{}
	for i := 0; i < n; i++ {
		r := find(i)
		if id, ok := roots[r]; ok {
			cluster[i] = id
		} else {
			roots[r] = next
			cluster[i] = next
			next++
		}
	}
	return cluster
}
