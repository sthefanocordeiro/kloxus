// Text-quality rules shared by the pipeline citecheck and the web edit
// path (KLOX-48): a factual sentence is only kept when every [n] marker
// points at an existing source and the cited source's stored text
// supports it lexically — and a drafted sentence that copies ten or
// more consecutive source words is rejected unless it is a short
// attributed quotation.
package pipeline

import (
	"regexp"
	"strconv"
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

// --- date and denial checks (KLOX-100) ---
//
// The lexical floor counts words, not meaning: a sentence could claim
// "dados obtidos em 3 de novembro" while the source says the 16th, or
// write "as empresas negaram qualquer incidente" when the source says
// they CONFIRMED — both pass when the rest of the vocabulary overlaps.
// Two narrow deterministic rules close those gaps: an explicit
// day+month literal must exist in the cited source, and a strong denial
// cue must echo a same-stem word in it.

var monthNum = map[string]int{
	"janeiro": 1, "fevereiro": 2, "março": 3, "marco": 3, "abril": 4,
	"maio": 5, "junho": 6, "julho": 7, "agosto": 8, "setembro": 9,
	"outubro": 10, "novembro": 11, "dezembro": 12,
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5,
	"june": 6, "july": 7, "august": 8, "september": 9, "october": 10,
	"november": 11, "december": 12,
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "jun": 6, "jul": 7, "aug": 8,
	"sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
}

var (
	ptDateRe    = regexp.MustCompile(`(?i)\b(\d{1,2})\s+de\s+([a-zà-ú]+)\b`)
	enDateReA   = regexp.MustCompile(`(?i)\b([a-z]+)\.?\s+(\d{1,2})(?:st|nd|rd|th)?\b`)
	enDateReB   = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)?\s+(?:of\s+)?([a-z]+)\b`)
	slashDateRe = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/\d{2,4})?\b`)
)

// dateClaim is one explicit day+month assertion; slash dates are
// ambiguous (dd/mm vs mm/dd), so either order in the source satisfies
// them.
type dateClaim struct {
	day, month int
	amb        bool
}

func dateClaims(s string) []dateClaim {
	var out []dateClaim
	add := func(d, m int) {
		if d >= 1 && d <= 31 {
			out = append(out, dateClaim{day: d, month: m})
		}
	}
	for _, m := range ptDateRe.FindAllStringSubmatch(s, -1) {
		if mo, ok := monthNum[strings.ToLower(m[2])]; ok {
			if d, err := strconv.Atoi(m[1]); err == nil {
				add(d, mo)
			}
		}
	}
	for _, m := range enDateReA.FindAllStringSubmatch(s, -1) {
		if mo, ok := monthNum[strings.ToLower(m[1])]; ok {
			if d, err := strconv.Atoi(m[2]); err == nil {
				add(d, mo)
			}
		}
	}
	for _, m := range enDateReB.FindAllStringSubmatch(s, -1) {
		if mo, ok := monthNum[strings.ToLower(m[2])]; ok {
			if d, err := strconv.Atoi(m[1]); err == nil {
				add(d, mo)
			}
		}
	}
	for _, m := range slashDateRe.FindAllStringSubmatch(s, -1) {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
			out = append(out, dateClaim{day: d, month: mo, amb: true})
		}
	}
	return out
}

// sourceDates collects every (day,month) a text's date literals assert;
// a dd/mm source date satisfies either interpretation.
func sourceDates(s string) map[[2]int]bool {
	out := map[[2]int]bool{}
	for _, c := range dateClaims(s) {
		out[[2]int{c.day, c.month}] = true
		if c.amb {
			out[[2]int{c.month, c.day}] = true
		}
	}
	return out
}

// UnsupportedDate reports whether the sentence asserts an explicit
// day+month the cited source does not carry — "em 3 de novembro" when
// the source says the 16th (KLOX-100). Month-less numbers are ignored;
// only a full date literal can be wrong this way.
func UnsupportedDate(sentence, sourceText string) bool {
	src := sourceDates(sourceText)
	for _, c := range dateClaims(citeRe.ReplaceAllString(sentence, " ")) {
		if c.amb {
			if !src[[2]int{c.day, c.month}] && !src[[2]int{c.month, c.day}] {
				return true
			}
			continue
		}
		if !src[[2]int{c.day, c.month}] {
			return true
		}
	}
	return false
}

// denialCueRe matches strong negation/denial cues in PT and EN — the
// claims that invert a source's sense when the source never recorded
// the denial (KLOX-100: "negaram qualquer incidente" against a source
// that says "confirmaram"). Bare não/not are too common to check; only
// verbs and absolute quantifiers count.
var denialCueRe = regexp.MustCompile(`(?i)\b(` +
	`negou|negaram|nega|negavam?|negado|negação|negações|` +
	`desmentiu|desmentiram|desmente|desmentem|` +
	`refutou|refutaram|refuta|refutam|` +
	`nunca|jamais|nenhum|nenhuma|` +
	`denied|denies|deny|denial|denials|` +
	`refuted|refutes|refute|refused|refuses|refuse|` +
	`disputed|disputes|dispute|never)\b`)

// negStem reduces a denial cue to the prefix its inflections share, so
// the sentence's "negaram" accepts the source's "negou" and "denied"
// accepts "denial".
func negStem(cue string) string {
	w := strings.ToLower(cue)
	switch {
	case strings.HasPrefix(w, "neg"):
		return "neg"
	case strings.HasPrefix(w, "desment"):
		return "desment"
	case strings.HasPrefix(w, "refut"):
		return "refut"
	case strings.HasPrefix(w, "nenhu"):
		return "nenhu"
	case strings.HasPrefix(w, "den"):
		return "den"
	case strings.HasPrefix(w, "refus"):
		return "refus"
	case strings.HasPrefix(w, "disput"):
		return "disput"
	default:
		return w // nunca, jamais, never are stems already
	}
}

// NegationUnsupported reports whether the sentence asserts a denial the
// cited source does not record: every strong negation cue needs a
// source word sharing its stem — otherwise the sentence claims the
// opposite of what the source says (KLOX-100).
func NegationUnsupported(sentence, sourceText string) bool {
	cues := denialCueRe.FindAllString(citeRe.ReplaceAllString(sentence, " "), -1)
	if len(cues) == 0 {
		return false
	}
	src := wordSeq(sourceText)
	for _, c := range cues {
		stem := negStem(c)
		found := false
		for _, w := range src {
			if strings.HasPrefix(w, stem) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}
