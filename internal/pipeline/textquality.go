// Text-quality rules shared by the pipeline citecheck and the web edit
// path (KLOX-48): a factual sentence is only kept when every [n] marker
// points at an existing source and the cited source's stored text
// supports it lexically — and a drafted sentence that copies ten or
// more consecutive source words is rejected unless it is a short
// attributed quotation.
package pipeline

import (
	"regexp"
	"strings"
	"unicode"
)

// supportThreshold is the fraction of weighted content words that must
// appear in the cited source text for a sentence to count as supported.
const supportThreshold = 0.6

// copyRunLen is the shared-word run that marks a sentence as a copy.
const copyRunLen = 10

// maxQuoteWords bounds the quoted, attributed exception to the copy rule.
const maxQuoteWords = 25

var citeRe = regexp.MustCompile(`\[(\d+)\]`)

// CitationNums parses "[1][3]" or "… [2]." into the cited ref numbers.
func CitationNums(text string) []int {
	var out []int
	for _, m := range citeRe.FindAllStringSubmatch(text, -1) {
		var n int
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out
}

// wordSeq normalizes text into a lowercase word stream (letters and
// digits), the unit both lexical rules compare.
func wordSeq(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// contentWeight doubles tokens that carry verifiable specifics —
// numbers and capitalized (proper-noun-like) words — so a sentence can
// never pass on shared function words alone.
func contentWeight(tok, raw string) int {
	if strings.ContainsFunc(raw, unicode.IsDigit) {
		return 2
	}
	if r := []rune(raw); len(r) > 0 && unicode.IsUpper(r[0]) {
		return 2
	}
	return 1
}

// SupportedBySource is the deterministic citation floor: at least 60%
// of the sentence's content words (≥3 letters, numbers and proper nouns
// weighted double) must appear in the cited source's stored text.
// Citation markers are stripped before the comparison. A sentence with
// no content words has nothing to verify and passes.
func SupportedBySource(sentence, sourceText string) bool {
	plain := citeRe.ReplaceAllString(sentence, " ")
	raw := strings.FieldsFunc(plain, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	corpus := map[string]bool{}
	for _, w := range wordSeq(sourceText) {
		if len([]rune(w)) >= 3 {
			corpus[w] = true
		}
	}
	var total, hit int
	for _, tok := range raw {
		w := strings.ToLower(tok)
		if len([]rune(w)) < 3 {
			continue
		}
		wt := contentWeight(w, tok)
		total += wt
		if corpus[w] {
			hit += wt
		}
	}
	if total == 0 {
		return true
	}
	return float64(hit)/float64(total) >= supportThreshold
}

var quoteRe = regexp.MustCompile(`"[^"\n]{2,}"|«[^»\n]{2,}»|“[^”\n]{2,}”|‘[^’\n]{2,}’`)

// quotedWordMask marks the word positions that sit inside a quoted
// span of at most maxQuoteWords words — the attributed-quotation
// exception to the copy rule.
func quotedWordMask(sentence string, wordIdx [][]int) []bool {
	mask := make([]bool, len(wordIdx))
	for _, q := range quoteRe.FindAllStringIndex(sentence, -1) {
		inside := 0
		for i, w := range wordIdx {
			if w[0] >= q[0] && w[1] <= q[1] {
				inside++
				mask[i] = true
			}
		}
		if inside > maxQuoteWords {
			for i, w := range wordIdx {
				if w[0] >= q[0] && w[1] <= q[1] {
					mask[i] = false
				}
			}
		}
	}
	return mask
}

// TooCloseToSource reports whether the sentence copies a run of ten or
// more consecutive source words. A copied run lying entirely inside a
// quoted span of at most 25 words (a short attributed quotation) is
// allowed; everything else is rejected.
func TooCloseToSource(sentence, sourceText string) bool {
	plain := citeRe.ReplaceAllString(sentence, " ")
	var wordIdx [][]int
	for _, m := range regexp.MustCompile(`[\p{L}\p{N}]+`).FindAllStringIndex(plain, -1) {
		wordIdx = append(wordIdx, m)
	}
	words := make([]string, len(wordIdx))
	for i, m := range wordIdx {
		words[i] = strings.ToLower(plain[m[0]:m[1]])
	}
	if len(words) < copyRunLen {
		return false
	}
	src := wordSeq(sourceText)
	srcRuns := map[string]bool{}
	for i := 0; i+copyRunLen <= len(src); i++ {
		srcRuns[strings.Join(src[i:i+copyRunLen], " ")] = true
	}
	if len(srcRuns) == 0 {
		return false
	}
	mask := quotedWordMask(plain, wordIdx)
	for i := 0; i+copyRunLen <= len(words); i++ {
		if !srcRuns[strings.Join(words[i:i+copyRunLen], " ")] {
			continue
		}
		quoted := true
		for j := i; j < i+copyRunLen; j++ {
			if !mask[j] {
				quoted = false
				break
			}
		}
		if !quoted {
			return true
		}
	}
	return false
}

// dateWordRe covers Portuguese and English date words — month and
// weekday names plus relative-day markers — so a "this Tuesday" claim
// cannot hide as opinion either.
var dateWordRe = regexp.MustCompile(`(?i)\b(janeiro|fevereiro|março|marco|abril|maio|junho|julho|agosto|setembro|outubro|novembro|dezembro|segunda|terça|terca|quarta|quinta|sexta|sábado|sabado|domingo|hoje|amanhã|amanha|ontem|january|february|march|april|may|june|july|august|september|october|november|december|monday|tuesday|wednesday|thursday|friday|saturday|sunday|today|tomorrow|yesterday)\b`)

var (
	numberRe = regexp.MustCompile(`\d`)
	quoteChr = regexp.MustCompile(`["«»“”‘’]`)
)

// HasNumberDateOrQuote reports whether the sentence carries a number,
// a date word or quotation marks — factual claims that may never be
// downgraded to "interpretation" to escape the citation check.
func HasNumberDateOrQuote(s string) bool {
	return numberRe.MatchString(s) || dateWordRe.MatchString(s) || quoteChr.MatchString(s)
}
