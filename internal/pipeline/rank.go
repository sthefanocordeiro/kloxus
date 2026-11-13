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

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// detMaxPerOutlet caps how many issue slots one primary outlet may
// occupy (first pass; relaxed when capacity would otherwise go unused).
const detMaxPerOutlet = 2

// lineupPick is the deterministic equivalent of a storylist pick: the
// story refs to merge (1-based positions into the run's stories), a
// working title, a provenance reason and the target section.
type lineupPick struct {
	Refs    []int
	Title   string
	Why     string
	Section string
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

// scoreStories is the deterministic replacement for the triage model:
// (score 0–10, reason) per story, stable for identical input.
func scoreStories(stories []store.Story) (scores []float64, reasons []string) {
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
		if t, ok := storyNewest(&stories[i]); ok {
			date = t.Format("2006-01-02")
		}
		reasons[i] = fmt.Sprintf("deterministic rank: %s, %d source(s)", date, len(stories[i].Outlets()))
	}
	return scores, reasons
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

// pickLineup slots the best-scored stories into the publication's
// section capacities. Round-robin across sections preserves section
// order; the per-outlet cap applies on the first pass, and a second
// pass fills any slots left empty by it.
func pickLineup(stories []store.Story, scores []float64, reasons []string, sections []config.Section) []lineupPick {
	order := storyOrder(stories, scores)
	slots := make([]int, len(sections))
	for i, sec := range sections {
		slots[i] = sec.Items
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
				for _, idx := range order {
					if placed[idx] {
						continue
					}
					outlet := primaryOutlet(&stories[idx])
					if pass == 0 && outlet != "" && outlets[outlet] >= detMaxPerOutlet {
						continue
					}
					placed[idx] = true
					slots[si]--
					outlets[outlet]++
					picks = append(picks, lineupPick{
						Refs:    []int{idx + 1},
						Title:   stories[idx].Title,
						Why:     reasons[idx],
						Section: sec.Title,
					})
					progress = true
					break
				}
			}
		}
	}
	return picks
}
