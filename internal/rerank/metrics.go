package rerank

import "math"

// PrecisionAtK is the share of the first k ranked ids that are relevant.
// ranked lists ids best-first; relevant is the labelled set.
func PrecisionAtK(ranked []string, relevant map[string]bool, k int) float64 {
	if k <= 0 {
		return 0
	}
	hit := 0
	for i := 0; i < k && i < len(ranked); i++ {
		if relevant[ranked[i]] {
			hit++
		}
	}
	return float64(hit) / float64(k)
}

// NDCGAtK is binary-gain nDCG@k: DCG of the ranking over the DCG of an
// ideal ranking of the same labels (0 when nothing is relevant).
func NDCGAtK(ranked []string, relevant map[string]bool, k int) float64 {
	var dcg float64
	for i := 0; i < k && i < len(ranked); i++ {
		if relevant[ranked[i]] {
			dcg += 1 / math.Log2(float64(i)+2)
		}
	}
	var ideal float64
	n := len(relevant)
	for i := 0; i < k && i < n; i++ {
		ideal += 1 / math.Log2(float64(i)+2)
	}
	if ideal == 0 {
		return 0
	}
	return dcg / ideal
}
