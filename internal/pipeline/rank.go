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
	// KLOX-94: the editorial line's reject terms demote matching
	// stories to the bench's tail, and the publication's declared topic
	// lifts direct title/headline hits — the free stand-ins for the
	// pgvector classification until it lands (sibling issue).
	reject := parseTerms(pub.RejectTerms)
	theme := topicTokens(pub.Topic)
	needText := len(reject) > 0 || len(theme) > 0
	// The folded keyword set the tags match against — the publication's
	// declared topic plus its editorial line.
	tokens := FoldedTokens(pub.Topic + " " + pub.EditorialLine)
	for i := range stories {
		var hay string
		var words map[string]bool
		if needText {
			hay, words = storySearchText(&stories[i], enrich)
		}
		if len(reject) > 0 && termsAny(reject, hay, words) {
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
				if TopicMatches(t, tokens, pub) {
					onTopic = true
					break
				}
			}
		}
		if !onTopic {
			for w := range theme {
				if wordHit(w, words) {
					onTopic = true
					break
				}
			}
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

// storySearchText folds the story's matchable text once — its own
// title, member headlines and any enrichment topic tags — into a
// phrase haystack plus the whole-word set single-token terms test
// against (KLOX-94).
func storySearchText(s *store.Story, enrich map[int64]*store.ItemEnrichment) (string, map[string]bool) {
	var b strings.Builder
	b.WriteString(s.Title)
	for i := range s.Items {
		b.WriteString(" ")
		b.WriteString(s.Items[i].Title)
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

// topicTokens is the publication topic's discriminating vocabulary:
// folded words of at least three letters minus the stopwords — short
// tokens like "ia"/"ai" stay out because they are common word pieces.
func topicTokens(s string) map[string]bool {
	out := map[string]bool{}
	for w := range FoldedTokens(s) {
		if len(w) >= 3 && !termStopwords[w] {
			out[w] = true
		}
	}
	return out
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
// topic tokens. "IA" is special-cased: the folded token "ia" is a
// Portuguese verb form, so it only counts as a literal uppercase "IA"
// in the original text or as "inteligência artificial".
func TopicMatches(tag string, tokens map[string]bool, pub *store.Publication) bool {
	if tag == "IA" {
		if tokens["inteligencia"] && tokens["artificial"] {
			return true
		}
		for _, w := range strings.Fields(pub.Topic + " " + pub.EditorialLine) {
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
	var sections []config.Section
	var rejectTerms termSet
	pt := false
	if pub != nil {
		sections = pub.Sections
		rejectTerms = parseTerms(pub.RejectTerms)
		pt = ptLang(pub.Language)
	}
	order := storyOrder(stories, scores)
	slots := make([]int, len(sections))
	secTopics := make([]termSet, len(sections))
	needText := len(rejectTerms) > 0
	for i, sec := range sections {
		slots[i] = sec.Items
		secTopics[i] = parseTerms(sec.Topic)
		if len(secTopics[i]) > 0 {
			needText = true
		}
	}
	// Search text is built once per story — and only when some rule
	// actually reads it — since it walks every member headline.
	hay := make([]string, len(stories))
	words := make([]map[string]bool, len(stories))
	if needText {
		for i := range stories {
			hay[i], words[i] = storySearchText(&stories[i], enrich)
		}
	}
	rejected := func(idx int) bool {
		return len(rejectTerms) > 0 && termsAny(rejectTerms, hay[idx], words[idx])
	}
	placed := make([]bool, len(stories))
	outlets := map[string]int{}
	var picks []lineupPick
	for pass := 0; pass < 2; pass++ {
		for progress := true; progress; {
			progress = false
			for si, sec := range sections {
				if slots[si] <= 0 {
					continue
				}
				eligible := func(idx int) bool {
					if placed[idx] || !hasEvidence(&stories[idx]) || rejected(idx) {
						return false
					}
					outlet := primaryOutlet(&stories[idx])
					return pass > 0 || outlet == "" || outlets[outlet] < detMaxPerOutlet
				}
				pick := -1
				topicHits := 0
				if len(secTopics[si]) > 0 {
					// aderência first: the story matching the most of
					// this section's topic terms wins the slot; order
					// (score desc, id asc) breaks the tie.
					for _, idx := range order {
						if !eligible(idx) {
							continue
						}
						if h := termsCount(secTopics[si], hay[idx], words[idx]); h > topicHits {
							topicHits, pick = h, idx
						}
					}
				}
				if pick < 0 {
					for _, idx := range order {
						if eligible(idx) {
							pick = idx
							break
						}
					}
				}
				if pick < 0 {
					continue
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
