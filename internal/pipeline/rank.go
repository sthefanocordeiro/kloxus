package pipeline

// Deterministic triage + lineup for KLOXUS_LLM_MODE=off.
//
// The heuristic replaces the triage and storylist model calls:
//
//	score = recency + diversity, both computed across the run's stories
//
//	recency   0–6: the newest member item's published date mapped
//	             linearly onto the run's [oldest, newest] span. A story
//	             with no parseable date scores the midpoint (3) — it
//	             neither leads nor trails on freshness.
//	diversity 0–4: min(4, distinct member sources), so a story confirmed
//	             by several outlets outranks a single-source one.
//
// Ties break on the story's lowest member item id, so identical input
// ranks identically. Stories are then slotted round-robin into the
// publication's sections — each section takes the best remaining story
// until its capacity is met — with at most detMaxPerOutlet picks whose
// primary outlet repeats across the issue (relaxed in a fill pass when
// slots would otherwise stay empty).

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/internal/rank"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// detMaxPerOutlet caps how many issue slots one primary outlet may
// occupy (first pass; relaxed when capacity would otherwise go unused).
const detMaxPerOutlet = 2

// nonEditorialPenalty sinks obvious non-editorial feed content —
// release lists, where-to-watch guides, the outlet's own supporter
// campaigns (KLOX-55) — below real coverage. It exceeds the recency +
// diversity maximum (10), so a noisy title never outranks a story;
// demoted rows still fill slots when nothing else remains.
const nonEditorialPenalty = 12.0

// rejectPenalty sinks a story that hits a creator-declared reject term
// below every other candidate — deeper than feed noise, because the
// newsletter's editorial line explicitly excludes the theme (KLOX-94).
// Demoted, never silently dropped: the story stays on the bench with a
// marked reason, and the auto-lineup never slots it. A manual board
// move still overrides — the creator's call wins over the keyword.
const rejectPenalty = 100.0

// lineupPick is the deterministic equivalent of a storylist pick: the
// story refs to merge (1-based positions into the run's stories), a
// working title, a provenance reason and the target section.
type lineupPick struct {
	Refs    []int
	Title   string
	Why     string
	Section string
}

// earliestTitle names a story after its earliest-published member
// article (owner decision, KLOX-46): the previous longest-title rule
// let a giant cluster take an unrelated member's name. Signal items
// (link headlines) are not articles and can't supply the title; with
// no parseable date the first member's title stands.
func earliestTitle(g []store.Item) string {
	title := g[0].Title
	var best time.Time
	for _, it := range g {
		if it.Signal() {
			continue
		}
		if t, ok := parsePublished(it.PublishedAt); ok && (best.IsZero() || t.Before(best)) {
			best, title = t, it.Title
		}
	}
	return title
}

// dedupeKey returns the canonical URL shared by every member item —
// the story's dedupe key — or "" when the cluster merged on
// near-identical text alone.
func dedupeKey(items []store.Item) string {
	key := ""
	for i, it := range items {
		c := it.CanonicalURL
		if c == "" {
			c = dedupe.Canonical(it.URL)
		}
		if i == 0 {
			key = c
		} else if c != key {
			return ""
		}
	}
	return key
}

// parsePublished reads an RSS/Atom published date leniently.
func parsePublished(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339, time.RFC1123Z, time.RFC1123,
		"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// storyNewest returns the newest parseable member published date.
func storyNewest(s *store.Story) (time.Time, bool) {
	var newest time.Time
	ok := false
	for i := range s.Items {
		if t, parsed := parsePublished(s.Items[i].PublishedAt); parsed && (!ok || t.After(newest)) {
			newest, ok = t, true
		}
	}
	return newest, ok
}

// ptLang reports whether the publication speaks Portuguese — reader-
// facing deterministic strings localize on it (KLOX-56).
func ptLang(lang string) bool {
	return strings.HasPrefix(strings.ToLower(lang), "pt")
}

// scoreStories is the deterministic replacement for the triage model:
// (score 0–10, reason) per story, stable for identical input.
// scoreStories ranks deterministically. The reason strings are
// reader-facing — they speak the publication's language (KLOX-56).
func scoreStories(stories []store.Story, lang string) (scores []float64, reasons []string) {
	pt := ptLang(lang)
	scores = make([]float64, len(stories))
	reasons = make([]string, len(stories))
	var lo, hi time.Time
	any := false
	for i := range stories {
		if t, ok := storyNewest(&stories[i]); ok {
			if !any || t.Before(lo) {
				lo = t
			}
			if !any || t.After(hi) {
				hi = t
			}
			any = true
		}
	}
	span := hi.Sub(lo).Seconds()
	for i := range stories {
		recency := 3.0 // undated, or the only date in the run
		if t, ok := storyNewest(&stories[i]); ok && span > 0 {
			recency = 6.0 * t.Sub(lo).Seconds() / span
		}
		div := float64(len(stories[i].Outlets()))
		if div > 4 {
			div = 4
		}
		scores[i] = recency + div
		date := "undated"
		if pt {
			date = "sem data"
		}
		if t, ok := storyNewest(&stories[i]); ok {
			date = agoLabel(time.Since(t), t, pt)
		}
		reasons[i] = date + " · " + outletsLabel(len(stories[i].Outlets()), pt)
		if nonEditorial(&stories[i]) {
			scores[i] -= nonEditorialPenalty
			if pt {
				reasons[i] += " · fora do editorial"
			} else {
				reasons[i] += " · off the editorial line"
			}
		}
	}
	return scores, reasons
}

// enrichmentKindWeight is the §7.2 kind weight the free ranking applies
// on top of recency + diversity: news and analysis outrank product
// launches; excluded kinds never reach the ranker (the candidate
// queries already filter them).
var enrichmentKindWeight = map[string]float64{
	"noticia":    1.0,
	"analise":    1.0,
	"lancamento": 0.5,
	"outro":      -0.5,
}

// scoreStoriesEnriched extends scoreStories with the shared AI
// enrichment signals (KLOX-67 §7.2): a topic match between the story's
// enrichment tags and the publication's topic/editorial line adds 2,
// and the story's best member kind applies its weight. Stories with no
// enrichment rows score exactly as before — the ranker degrades to the
// deterministic heuristic, never to an error.
func scoreStoriesEnriched(stories []store.Story, lang string, pub *store.Publication, enrich map[int64]*store.ItemEnrichment) (scores []float64, reasons []string) {
	scores, reasons = scoreStories(stories, lang)
	if pub == nil {
		return scores, reasons
	}
	pt := ptLang(lang)
	// KLOX-94: the newsletter's reject terms demote matching stories
	// to the bench's tail. KLOX-115 adds the editorial line's own skip
	// clauses to that policy — "Pule games" sinks games-tagged stories
	// the same −100 — and the declared topic lifts phrase hits, not
	// loose words: "banco central" needs banco AND central, so "banco
	// de dados" stops reading as on-topic.
	reject := parseTerms(pub.RejectTerms)
	skip := skipTerms(pub.EditorialLine)
	editorias, subjects := splitTopicRoles(pub.Topic)
	positiveTopic := pub.Topic
	if editorias != "" && subjects != "" {
		// With both roles present, editoria vocabulary is not subject
		// evidence. The typed subjects alone drive the ordinary on-topic
		// reading; a compatible publisher category is scored separately
		// through editorialCategoryBoost.
		positiveTopic = subjects
	}
	theme := phraseTerms(positiveTopic)
	needRejectText := len(reject)+len(skip) > 0
	needPositiveText := len(theme) > 0
	// The folded keyword set the tags match against - the positive topic
	// vocabulary plus the editorial line minus its skip clauses, so "pule
	// games" never counts "games" as a theme word.
	posLine, _ := editorialLineSplit(pub.EditorialLine)
	posText := strings.TrimSpace(positiveTopic + " " + posLine)
	tokens := FoldedTokens(posText)
	for i := range stories {
		var hay string
		var words map[string]bool
		if needRejectText {
			hay, words = storySearchText(&stories[i], enrich)
		}
		var positiveWords map[string]bool
		if needPositiveText {
			_, positiveWords = storyPositiveSearchText(&stories[i], enrich)
		}
		if (len(reject) > 0 && termsAny(reject, hay, words)) || phraseAny(skip, words) {
			scores[i] -= rejectPenalty
			if pt {
				reasons[i] += " · rejeitada pela linha editorial"
			} else {
				reasons[i] += " · rejected by the editorial line"
			}
		}
		bestW := 0.0
		haveKind := false
		onTopic := false
		for j := range stories[i].Items {
			e := enrich[stories[i].Items[j].ID]
			if e == nil {
				continue
			}
			w := enrichmentKindWeight[e.Kind] // 0 for unlisted kinds
			if !haveKind || w > bestW {
				bestW, haveKind = w, true
			}
			for _, t := range e.Topics {
				if TopicMatches(t, tokens, posText) {
					onTopic = true
					break
				}
			}
		}
		if !onTopic {
			onTopic = phraseAny(theme, positiveWords)
		}
		if haveKind {
			scores[i] += bestW
		}
		if onTopic {
			scores[i] += 2
			if pt {
				reasons[i] += " · no tema"
			} else {
				reasons[i] += " · on topic"
			}
		}
		if editorialCategoryBoost(pub, &stories[i], enrich) {
			scores[i] += categoryMatchBoost
			if pt {
				reasons[i] += " · categoria da editoria"
			} else {
				reasons[i] += " · matching publisher category"
			}
		}
	}
	return scores, reasons
}

// FoldedTokens lowercases, strips accents and splits on word
// boundaries — the same fold the deals list uses.
func FoldedTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(foldAccents.Replace(strings.ToLower(s)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out[w] = true
	}
	return out
}

// foldText is the phrase-level version of FoldedTokens: lowercase,
// accents stripped, whitespace collapsed — the same normalization
// promo and non-editorial matching use on titles.
func foldText(s string) string {
	return strings.Join(strings.Fields(foldAccents.Replace(strings.ToLower(s))), " ")
}

// termSet is a parsed topics/keywords field: each comma-, semicolon-
// or pipe-separated entry yields one single-word term per content word
// ("celular e games" → celular, games) plus, when the entry holds more
// than one content word, the whole folded phrase ("banco central" →
// banco, central, "banco central") — so a full-phrase hit outranks a
// partial one while keywords still match independently.
type termSet [][]string

// parseTerms splits a free-text topic or reject list into a termSet.
// Creators write these fields as prose or keyword lists — commas and
// semicolons are the phrase separators, stopwords never count alone.
func parseTerms(s string) termSet {
	var out termSet
	for _, raw := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		toks := strings.Fields(foldText(raw))
		content := 0
		for _, w := range toks {
			if len(w) >= 3 && !termStopwords[w] {
				out = append(out, []string{w})
				content++
			}
		}
		if content > 1 {
			out = append(out, toks)
		}
	}
	return out
}

// termMatch reports whether one parsed term hits the story's folded
// text. A single-token term must appear as a whole word — "ia" cannot
// match inside "dia", "games" inside "gamestay" — while a multi-word
// term matches as a folded phrase.
func termMatch(term []string, hay string, words map[string]bool) bool {
	if len(term) == 1 {
		return wordHit(term[0], words)
	}
	return strings.Contains(" "+hay+" ", " "+strings.Join(term, " ")+" ")
}

// wordHit tests one content word against the folded word set, tolerant
// of the common plural endings — "fintech" hits "fintechs", "game"
// hits "games", and a creator's plural term hits the singular headline
// word. Prefix/substring tricks stay out so boundaries hold.
func wordHit(w string, words map[string]bool) bool {
	if words[w] || words[w+"s"] || words[w+"es"] {
		return true
	}
	if strings.HasSuffix(w, "s") && len(w) > 3 && words[w[:len(w)-1]] {
		return true
	}
	if strings.HasSuffix(w, "es") && len(w) > 4 && words[w[:len(w)-2]] {
		return true
	}
	return false
}

// termsAny reports whether any term in the set hits the text.
func termsAny(terms termSet, hay string, words map[string]bool) bool {
	for _, t := range terms {
		if termMatch(t, hay, words) {
			return true
		}
	}
	return false
}

// TermsMatch reports whether a free-text term list hits one text —
// the shared reject/topic predicate for surfaces that can't hoist a
// parsed set, like a per-title API check (KLOX-94).
func TermsMatch(list, text string) bool {
	hay := foldText(text)
	return termsAny(parseTerms(list), hay, FoldedTokens(hay))
}

// termsCount returns how many distinct terms hit the text — the
// aderência strength a section's topic scores a story with.
func termsCount(terms termSet, hay string, words map[string]bool) int {
	n := 0
	for _, t := range terms {
		if termMatch(t, hay, words) {
			n++
		}
	}
	return n
}

// storySearchText folds the story's guardrail text once - its own
// title, member headlines, publisher-supplied categories and any
// enrichment topic tags - into a phrase haystack plus the whole-word set
// single-token terms test against (KLOX-94). Categories count as evidence
// for reject/non-editorial guardrails; they never weaken those guardrails
// (KLOX-278).
func storySearchText(s *store.Story, enrich map[int64]*store.ItemEnrichment) (string, map[string]bool) {
	return storySearchTextMode(s, enrich, true)
}

// storyPositiveSearchText is the positive topic/subject matching view.
// It deliberately excludes publisher-supplied categories: a category is
// evidence of the editoria, never evidence that one of the creator's
// typed subjects is present (KLOX-278).
func storyPositiveSearchText(s *store.Story, enrich map[int64]*store.ItemEnrichment) (string, map[string]bool) {
	return storySearchTextMode(s, enrich, false)
}

func storySearchTextMode(s *store.Story, enrich map[int64]*store.ItemEnrichment, withCategories bool) (string, map[string]bool) {
	var b strings.Builder
	b.WriteString(s.Title)
	for i := range s.Items {
		b.WriteString(" ")
		b.WriteString(s.Items[i].Title)
		if withCategories {
			for _, cat := range s.Items[i].Categories {
				b.WriteString(" ")
				b.WriteString(cat)
			}
		}
		if e := enrich[s.Items[i].ID]; e != nil {
			for _, tg := range e.Topics {
				b.WriteString(" ")
				b.WriteString(tg)
			}
		}
	}
	hay := foldText(b.String())
	return hay, FoldedTokens(hay)
}

// categoryOnTopic reports whether any publisher-supplied category matches
// an editoria recovered from the Radar's stored topic. Comparison is
// case- and accent-insensitive through foldText/FoldedTokens, while
// storage keeps the publisher's original spelling. An item with no
// categories simply returns false - absence is no signal, never a
// penalty. Matching is per publisher label: two separate categories must
// not combine into a multi-word editoria phrase neither one actually says.
func categoryOnTopic(pub *store.Publication, s *store.Story) bool {
	if pub == nil {
		return false
	}
	editorias, _ := splitTopicRoles(pub.Topic)
	return categoryMatchesEditoria(editorias, s)
}

func categoryMatchesEditoria(editorias string, s *store.Story) bool {
	topic := phraseTerms(editorias)
	if len(topic) == 0 {
		return false
	}
	for i := range s.Items {
		for _, cat := range s.Items[i].Categories {
			if phraseAny(topic, FoldedTokens(foldText(cat))) {
				return true
			}
		}
	}
	return false
}

// subjectEvidence reports whether the story has evidence of at least one
// typed subject, independent of publisher categories. The evidence is the
// same deterministic vocabulary the free ranking already uses: story and
// member headlines plus enrichment topic tags. Publisher categories are
// intentionally absent from this view.
func subjectEvidence(subjects string, s *store.Story, enrich map[int64]*store.ItemEnrichment) bool {
	if strings.TrimSpace(subjects) == "" {
		return false
	}
	_, words := storyPositiveSearchText(s, enrich)
	if phraseAny(phraseTerms(subjects), words) {
		return true
	}
	tokens := FoldedTokens(subjects)
	for i := range s.Items {
		e := enrich[s.Items[i].ID]
		if e == nil {
			continue
		}
		for _, tag := range e.Topics {
			if TopicMatches(tag, tokens, subjects) {
				return true
			}
		}
	}
	return false
}

// editorialCategoryBoost is the conjunction required before a publisher
// category may reinforce relevance or lift the embedding floor:
//
//   - no editoria recovered: no editorial category boost at all;
//   - editoria without typed subjects: a compatible category is enough;
//   - editoria plus typed subjects: both the compatible category and
//     independent evidence of one subject are required.
func editorialCategoryBoost(pub *store.Publication, s *store.Story, enrich map[int64]*store.ItemEnrichment) bool {
	if pub == nil {
		return false
	}
	editorias, subjects := splitTopicRoles(pub.Topic)
	if editorias == "" || !categoryMatchesEditoria(editorias, s) {
		return false
	}
	return subjects == "" || subjectEvidence(subjects, s, enrich)
}

// termStopwords are the function words a topic phrase contributes that
// must never count as a theme signal on their own — "e", "de", "the".
var termStopwords = map[string]bool{
	"a": true, "as": true, "o": true, "os": true, "um": true, "uma": true,
	"de": true, "da": true, "do": true, "das": true, "dos": true,
	"e": true, "em": true, "no": true, "na": true, "nos": true, "nas": true,
	"que": true, "para": true, "com": true, "por": true, "ao": true,
	"the": true, "an": true, "and": true, "of": true, "to": true,
	"in": true, "on": true, "for": true, "at": true, "by": true,
}

// textToks splits folded text into letter/digit tokens — punctuation
// never rides a word the way strings.Fields lets it.
func textToks(s string) []string {
	return strings.FieldsFunc(foldText(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// skipVerbs open an exclusion clause in the editorial line ("Pule
// games", "Skip deals"): the subjects they govern join the reject
// policy and never count as on-topic vocabulary (KLOX-115).
var skipVerbs = map[string]bool{
	"pule": true, "pular": true, "ignore": true, "ignorar": true,
	"evite": true, "evitar": true, "skip": true, "avoid": true,
	"exclude": true, "exclua": true, "excluir": true,
}

// weakSkipWords open an exclusion clause only at a clause boundary —
// mid-clause they are ordinary function words ("impacto sem hype" is
// a preference, not a ban; "no Brasil" is a place, not a "no").
var weakSkipWords = map[string]bool{
	"sem": true, "nada": true, "nao": true, "nunca": true,
	"nenhum": true, "nenhuma": true, "without": true, "never": true,
}

// includeVerbs flip a clause back to positive coverage ("Prefira
// fontes primárias", "cover fintech").
var includeVerbs = map[string]bool{
	"prefira": true, "preferir": true, "prefer": true,
	"cubra": true, "cobrir": true, "cover": true,
	"priorize": true, "priorizar": true, "prioritize": true,
	"foque": true, "focar": true, "focus": true,
	"destaque": true, "highlight": true, "inclua": true, "include": true,
}

// skipPhraseMarkers are multi-word negative pivots safe to honor
// mid-clause — "Priorize impacto real em vez de anúncios e hype".
var skipPhraseMarkers = []string{
	"em vez de", "ao inves de", "instead of", "rather than",
}

// skipExceptionClauses end a skip run without feeding either list: a
// qualification like "a menos que mudem um mercado" is fine print on
// the ban, not more ban-list and not coverage vocabulary.
var skipExceptionClauses = []string{
	"a menos que", "a nao ser", "salvo", "exceto", "mas",
	"unless", "except", "but",
}

// skipFragmentStops cut a skip fragment where its qualifier begins —
// "Pule lançamentos sem disponibilidade no Brasil" bans "lançamentos",
// never "brasil" or "disponibilidade" (KLOX-115).
var skipFragmentStops = map[string]bool{
	"sem": true, "que": true, "quando": true, "onde": true,
	"no": true, "na": true, "nos": true, "nas": true,
	"a": true, "as": true, "salvo": true, "exceto": true,
	"porque": true, "pois": true, "enquanto": true, "vez": true,
	"inves": true, "than": true,
	"unless": true, "except": true, "but": true,
	"since": true, "because": true, "while": true,
}

// editorialLineSplit separates an editorial line's positive coverage
// text from its skip clauses. Sentences split on .!?; and newlines,
// clauses on commas. A clause opening with a skip verb — or carrying
// a phrase marker like "em vez de" — turns its subject list into
// reject vocabulary; an include verb flips the run back to positive,
// and an exception clause ("a menos que …") is neither. Positive
// clauses keep their original case so the literal "IA"/"AI" tag check
// still works; skip fragments return folded tokens.
func editorialLineSplit(line string) (positive string, skip string) {
	var pos, sk strings.Builder
	for _, sent := range strings.FieldsFunc(line, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == ';' || r == '\n'
	}) {
		skipping := false
		for _, clause := range strings.Split(sent, ",") {
			toks := textToks(clause)
			if len(toks) == 0 {
				continue
			}
			joined := strings.Join(toks, " ")
			except := false
			for _, e := range skipExceptionClauses {
				if strings.HasPrefix(joined, e) {
					except = true
					break
				}
			}
			if except {
				skipping = false
				continue
			}
			// A mid-clause phrase marker pivots the clause: the part
			// before it keeps the current polarity, the part after it
			// is always ban-list.
			if before, after, ok := phraseMarkerCut(toks); ok {
				if !skipping {
					pos.WriteString(strings.Join(before, " ") + " ")
				}
				sk.WriteString(skipFragment(after))
				skipping = true
				continue
			}
			switch first := toks[0]; {
			case skipVerbs[first], weakSkipWords[first]:
				skipping = true
				sk.WriteString(skipFragment(toks[1:]))
			case includeVerbs[first]:
				skipping = false
				pos.WriteString(strings.TrimSpace(clause) + " ")
			default:
				if skipping {
					sk.WriteString(skipFragment(toks))
				} else {
					pos.WriteString(strings.TrimSpace(clause) + " ")
				}
			}
		}
	}
	return strings.TrimSpace(pos.String()), strings.TrimSpace(sk.String())
}

// phraseMarkerCut splits a clause's tokens around the first phrase
// marker ("em vez de", "instead of"); ok is false when none appears.
func phraseMarkerCut(toks []string) (before, after []string, ok bool) {
	for i := 0; i < len(toks); i++ {
		for _, m := range skipPhraseMarkers {
			mt := strings.Fields(m)
			if i+len(mt) > len(toks) {
				continue
			}
			match := true
			for j, w := range mt {
				if toks[i+j] != w {
					match = false
					break
				}
			}
			if match {
				return toks[:i], toks[i+len(mt):], true
			}
		}
	}
	return toks, nil, false
}

// skipFragment keeps the ban's subject list and drops its qualifier:
// tokens up to the first stop word ("sem", "que", "unless"), joined
// with the "; " separator phraseTerms reads as the entry boundary.
func skipFragment(toks []string) string {
	var out []string
	for _, w := range toks {
		if skipFragmentStops[w] {
			break
		}
		out = append(out, w)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " ") + "; "
}

// skipTerms parses the editorial line's skip clauses into
// conjunctions — the same shape phraseTerms gives the topic — so a
// skip entry only counts when every content word hits ("rodadas de
// investimento" bans funding rounds, not every "investimento" story).
func skipTerms(line string) [][]string {
	_, skip := editorialLineSplit(line)
	if skip == "" {
		return nil
	}
	return phraseTerms(skip)
}

// phraseTerms parses a theme or skip list into conjunctions
// (KLOX-115): entries split on , ; | and a bare "e"/"and", and every
// content word inside an entry must hit for it to count. "banco
// central" needs banco AND central — a loose "banco" in "banco de
// dados" stops qualifying — while "fintech e regulação" still rewards
// either subject.
func phraseTerms(s string) [][]string {
	var out [][]string
	for _, entry := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		var cur []string
		flush := func() {
			var words []string
			for _, w := range cur {
				if len(w) >= 3 && !termStopwords[w] {
					words = append(words, w)
				}
			}
			if len(words) > 0 {
				out = append(out, words)
			}
			cur = nil
		}
		for _, w := range textToks(entry) {
			if w == "e" || w == "and" {
				flush()
			} else {
				cur = append(cur, w)
			}
		}
		flush()
	}
	return out
}

// phraseHit reports whether one conjunction is fully present — every
// content word must hit the folded word set (plural-tolerant).
func phraseHit(alt []string, words map[string]bool) bool {
	for _, w := range alt {
		if !wordHit(w, words) {
			return false
		}
	}
	return len(alt) > 0
}

// phraseAny reports whether any conjunction is fully present.
func phraseAny(alts [][]string, words map[string]bool) bool {
	for _, a := range alts {
		if phraseHit(a, words) {
			return true
		}
	}
	return false
}

// PositiveEditorialText is the publication's on-topic source text —
// the declared topic plus the editorial line minus its skip clauses
// (KLOX-115). "Pule games" stops counting "games" as a theme word
// while "Prefira fontes primárias" keeps its vocabulary.
func PositiveEditorialText(pub *store.Publication) string {
	if pub == nil {
		return ""
	}
	pos, _ := editorialLineSplit(pub.EditorialLine)
	return strings.TrimSpace(pub.Topic + " " + pos)
}

// RejectMatch reports whether a text hits the publication's effective
// reject list (KLOX-115): the declared reject_terms plus the subjects
// the editorial line tells the desk to skip. It is the request-time
// mirror of the run-time scorer's demotion — callers pass the story
// title plus its enrichment tags so a tag-only hit ("GTA 6" carrying
// the "games" tag) marks the same story the score sank.
func RejectMatch(pub *store.Publication, text string) bool {
	if pub == nil {
		return false
	}
	hay := foldText(text)
	words := FoldedTokens(hay)
	return termsAny(parseTerms(pub.RejectTerms), hay, words) ||
		phraseAny(skipTerms(pub.EditorialLine), words)
}

// ThemeTitleHit reports whether a title satisfies one of the
// publication topic's conjunction alternatives (KLOX-115) — the
// request-time mirror of the scorer's theme match, so the card's
// off_topic flag and the stored "no tema" reason read the same source
// of truth (P1-9).
func ThemeTitleHit(pub *store.Publication, title string) bool {
	if pub == nil {
		return false
	}
	return phraseAny(phraseTerms(pub.Topic), FoldedTokens(foldText(title)))
}

// SuggestSection names the section a bench candidate best fits: the
// declared topic matching the most terms wins, ties settle on the
// earlier section. "" means no section claims the story — the board
// falls back to its first open slot (KLOX-94).
func SuggestSection(title string, secs []config.Section) string {
	hay := foldText(title)
	words := FoldedTokens(hay)
	best, hits := "", 0
	for _, sec := range secs {
		if h := termsCount(parseTerms(sec.Topic), hay, words); h > hits {
			best, hits = sec.Title, h
		}
	}
	return best
}

// TopicMatches reports whether an enrichment tag hits the publication's
// on-topic tokens. "IA" is special-cased: the folded token "ia" is a
// Portuguese verb form, so it only counts as a literal uppercase "IA"
// in the original source text or as "inteligência artificial". posSource
// is PositiveEditorialText — skip clauses stay out of the IA check too.
func TopicMatches(tag string, tokens map[string]bool, posSource string) bool {
	if tag == "IA" {
		if tokens["inteligencia"] && tokens["artificial"] {
			return true
		}
		for _, w := range strings.Fields(posSource) {
			if w == "IA" || w == "AI" {
				return true
			}
		}
		return false
	}
	return tokens[tag]
}

var foldAccents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// agoLabel renders story freshness for the reader-facing reason:
// relative under a week ("há 3 h" / "3 h ago"), the date beyond it.
func agoLabel(d time.Duration, t time.Time, pt bool) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		m := int(d.Minutes()) + 1
		if pt {
			return fmt.Sprintf("há %d min", m)
		}
		return fmt.Sprintf("%d min ago", m)
	case d < 24*time.Hour:
		if pt {
			return fmt.Sprintf("há %d h", int(d.Hours()))
		}
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		if pt {
			return fmt.Sprintf("há %d d", int(d.Hours()/24))
		}
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// outletsLabel pluralizes the outlet count — never "fonte(s)".
func outletsLabel(n int, pt bool) string {
	if pt {
		if n == 1 {
			return "1 veículo"
		}
		return fmt.Sprintf("%d veículos", n)
	}
	if n == 1 {
		return "1 outlet"
	}
	return fmt.Sprintf("%d outlets", n)
}

// nonEditorial reports whether the story title or any member headline
// is obvious non-editorial feed noise: a release list, a
// where-to-watch guide or the source's own supporter campaign.
func nonEditorial(s *store.Story) bool {
	if store.IsNonEditorialTitle(s.Title, primaryOutlet(s)) {
		return true
	}
	for i := range s.Items {
		if store.IsNonEditorialTitle(s.Items[i].Title, s.Items[i].SourceName) {
			return true
		}
	}
	return false
}

// storyOrder sorts story indices by (score desc, story id asc) — the
// stable candidate order served to the board.
func storyOrder(stories []store.Story, scores []float64) []int {
	order := make([]int, len(stories))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if scores[ia] != scores[ib] {
			return scores[ia] > scores[ib]
		}
		return stories[ia].ID < stories[ib].ID
	})
	return order
}

// primaryOutlet names the feed that produced the story's first item —
// the outlet counted against detMaxPerOutlet.
func primaryOutlet(s *store.Story) string {
	if len(s.Items) == 0 {
		return ""
	}
	if s.Items[0].SourceName != "" {
		return s.Items[0].SourceName
	}
	if u, err := url.Parse(s.Items[0].SourceURL); err == nil {
		return u.Hostname()
	}
	return s.Items[0].SourceURL
}

// synthDossier builds a dossier deterministically from a story's member
// items: the first sentence of each item becomes a summary sentence
// cited to that source, later sentences become facts. Every claim comes
// from a member item, so citations stay valid without a model call.
func synthDossier(ref int, title, interpretation string, items []store.Item) *Dossier {
	items = capEntrySources(items)
	var facts []DossierFact
	var summary string
	for i, it := range items {
		sents := SplitSentences(it.ContentText)
		if len(sents) > 0 {
			first := strings.TrimRight(strings.TrimSpace(sents[0]), ". ")
			summary += first + fmt.Sprintf(" [%d]. ", i+1)
		}
		for _, s := range sents[1:] {
			facts = append(facts, DossierFact{Text: strings.TrimSpace(s), Ref: i + 1})
			if len(facts) >= 4 {
				break
			}
		}
	}
	d := &Dossier{
		Ref: ref, Title: title,
		Summary:        strings.TrimSpace(summary),
		Facts:          facts,
		Interpretation: []string{interpretation},
	}
	d.fillSources(items)
	return d
}

// synthBody composes an item body from its dossier: the summary plus
// any facts not already stated, each claim carrying a [n] citation.
func synthBody(d *Dossier) string {
	body := strings.TrimSpace(d.Summary)
	for _, f := range d.Facts {
		if s := strings.TrimSpace(f.Text); s != "" && !strings.Contains(body, s) {
			body += " " + ensureRefSuffix(s, f.Ref)
		}
	}
	return body
}

// hasEvidence reports whether the story can back an issue item: at
// least one member must carry a body. An all-signal cluster (a bare
// HN/DJ link) ranks and attributes but yields no issue item, so it
// must never occupy a section slot (KLOX-60).
func hasEvidence(s *store.Story) bool {
	for i := range s.Items {
		if !s.Items[i].Signal() {
			return true
		}
	}
	return false
}

// pickLineup slots the best-scored stories into the publication's
// section capacities. Round-robin across sections preserves section
// order; the per-outlet cap applies on the first pass, and a second
// pass fills any slots left empty by it. Evidence-less stories are
// skipped outright — they produce no issue item, so slotting one
// would strand the section.
//
// Aderência (KLOX-94): a section with a declared topic prefers the
// story matching the most topic terms — strongest first, score
// breaking ties — and only relaxes to the global best when nothing
// matches. A story hitting the newsletter's reject terms is never
// auto-slotted; it stays a marked bench candidate the creator can
// still place by hand.
func pickLineup(stories []store.Story, scores []float64, reasons []string, pub *store.Publication, enrich map[int64]*store.ItemEnrichment) []lineupPick {
	return pickLineupSkipping(stories, scores, reasons, pub, enrich, nil)
}

// pickLineupSkipping is pickLineup with a stage-1 exclusion list
// (KLOX-109): stories flagged hidden (off-topic / English) are never
// auto-slotted - a thin lineup beats an off-theme one. They stay on
// the bench behind the board's filter.
func pickLineupSkipping(stories []store.Story, scores []float64, reasons []string, pub *store.Publication, enrich map[int64]*store.ItemEnrichment, skip []bool) []lineupPick {
	return pickLineupVec(stories, scores, reasons, pub, enrich, skip, nil)
}

// lineupVectors carries the stage-1 vectors the lineup reads (KLOX-112):
// per-story vectors for MMR and per-section topic vectors (by section
// index). Nil keeps the pure score/topic-term lineup.
type lineupVectors struct {
	Story   [][]float32
	Section map[int][]float32
}

// sectionMinSim is the floor a story's cosine to a section vector must
// clear to fill the slot: a section with a vector would rather stay
// short than take an off-topic story (Regulação must not fill with
// Final Cut Pro). Same scale as the stage-1 relevance floor.
const sectionMinSim = 0.25

// pickLineupVec is the lineup with MMR variety and section vectors
// (spec section 8): global picks maximize 0.7*score - 0.3*max cos to
// the stories already picked; a section with a vector takes the best
// unpicked stories by section similarity.
func pickLineupVec(stories []store.Story, scores []float64, reasons []string, pub *store.Publication, enrich map[int64]*store.ItemEnrichment, skip []bool, lv *lineupVectors) []lineupPick {
	var sections []config.Section
	var rejectTerms termSet
	var skipAlts [][]string
	pt := false
	if pub != nil {
		sections = pub.Sections
		rejectTerms = parseTerms(pub.RejectTerms)
		skipAlts = skipTerms(pub.EditorialLine)
		pt = ptLang(pub.Language)
	}
	order := storyOrder(stories, scores)
	slots := make([]int, len(sections))
	secTopics := make([]termSet, len(sections))
	needRejectText := len(rejectTerms)+len(skipAlts) > 0
	needPositiveText := false
	for i, sec := range sections {
		slots[i] = sec.Items
		secTopics[i] = parseTerms(sec.Topic)
		if len(secTopics[i]) > 0 {
			needPositiveText = true
		}
	}
	// Search text is built once per story - and only when some rule
	// actually reads it - since it walks every member headline. Positive
	// section matching excludes publisher categories; reject matching keeps
	// them because editorial guards may use every publisher-supplied label.
	rejectHay := make([]string, len(stories))
	rejectWords := make([]map[string]bool, len(stories))
	if needRejectText {
		for i := range stories {
			rejectHay[i], rejectWords[i] = storySearchText(&stories[i], enrich)
		}
	}
	positiveHay := make([]string, len(stories))
	positiveWords := make([]map[string]bool, len(stories))
	if needPositiveText {
		for i := range stories {
			positiveHay[i], positiveWords[i] = storyPositiveSearchText(&stories[i], enrich)
		}
	}
	rejected := func(idx int) bool {
		return (len(rejectTerms) > 0 && termsAny(rejectTerms, rejectHay[idx], rejectWords[idx])) ||
			phraseAny(skipAlts, rejectWords[idx])
	}
	placed := make([]bool, len(stories))
	outlets := map[string]int{}
	var picks []lineupPick
	var pickedVecs [][]float32
	// redundancy is the max cosine of a story to the lineup so far.
	redundancy := func(idx int) float64 {
		if lv == nil || idx >= len(lv.Story) || len(lv.Story[idx]) == 0 {
			return 0
		}
		m := 0.0
		for _, v := range pickedVecs {
			if c := rank.Cos(lv.Story[idx], v); c > m {
				m = c
			}
		}
		return m
	}
	for pass := 0; pass < 2; pass++ {
		for progress := true; progress; {
			progress = false
			for si, sec := range sections {
				if slots[si] <= 0 {
					continue
				}
				eligible := func(idx int) bool {
					if placed[idx] || !hasEvidence(&stories[idx]) || rejected(idx) || (skip != nil && skip[idx]) {
						return false
					}
					outlet := primaryOutlet(&stories[idx])
					return pass > 0 || outlet == "" || outlets[outlet] < detMaxPerOutlet
				}
				pick := -1
				topicHits := 0
				var secVec []float32
				if lv != nil {
					secVec = lv.Section[si]
				}
				if len(secVec) > 0 {
					// section vector: best unpicked by section
					// similarity (MMR-diversified), floor-gated.
					bestV := 0.0
					for _, idx := range order {
						if !eligible(idx) || idx >= len(lv.Story) || len(lv.Story[idx]) == 0 {
							continue
						}
						sim := rank.Cos(lv.Story[idx], secVec)
						if sim < sectionMinSim {
							continue
						}
						if v := rank.MMRLambda*sim - (1-rank.MMRLambda)*redundancy(idx); pick < 0 || v > bestV {
							pick, bestV = idx, v
						}
					}
					if pick >= 0 {
						topicHits = 1
					}
				}
				if pick < 0 && len(secTopics[si]) > 0 {
					// aderência first: the story matching the most of
					// this section's topic terms wins the slot; order
					// (score desc, id asc) breaks the tie.
					for _, idx := range order {
						if !eligible(idx) {
							continue
						}
						if h := termsCount(secTopics[si], positiveHay[idx], positiveWords[idx]); h > topicHits {
							topicHits, pick = h, idx
						}
					}
				}
				if pick < 0 && len(secVec) == 0 {
					bestV := 0.0
					for _, idx := range order {
						if !eligible(idx) {
							continue
						}
						if v := rank.MMRLambda*scores[idx]/stage1Scale - (1-rank.MMRLambda)*redundancy(idx); pick < 0 || v > bestV {
							pick, bestV = idx, v
						}
					}
				}
				if pick < 0 {
					continue
				}
				if lv != nil && pick < len(lv.Story) && len(lv.Story[pick]) > 0 {
					pickedVecs = append(pickedVecs, lv.Story[pick])
				}
				placed[pick] = true
				slots[si]--
				outlets[primaryOutlet(&stories[pick])]++
				why := reasons[pick]
				if topicHits > 0 {
					if pt {
						why += " · no tema da seção"
					} else {
						why += " · on the section topic"
					}
				}
				picks = append(picks, lineupPick{
					Refs:    []int{pick + 1},
					Title:   stories[pick].Title,
					Why:     why,
					Section: sec.Title,
				})
				progress = true
			}
		}
	}
	return picks
}
