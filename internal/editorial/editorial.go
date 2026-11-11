// Package editorial turns review decisions into the visible, editable
// editorial guide used by every subsequent triage and draft call.
package editorial

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// Rebuild regenerates the guide from the publication's decisions.
// Preferred path: the model condenses decisions into rules. Demo-mode
// fallback (no fixture can cover arbitrary judge input): normalize and
// merge rejection reasons into short general rules. Only effective
// rejections feed the guide — approvals, alternatives and undone
// rejections carry no editorial signal. Identical output is not
// re-versioned (SaveGuide dedupes). Returns content and version.
func Rebuild(ctx context.Context, db *store.DB, pub *store.Publication, provider llm.Provider, model string) (string, int, error) {
	decisions, err := db.Decisions(ctx, pub.ID)
	if err != nil {
		return "", 0, err
	}
	version, current, _ := db.LatestGuide(ctx, pub.ID)

	// Effective rejections: the latest decision per item. An undone (or
	// later reverted) rejection drops out of the guide entirely.
	lastIdx := map[int64]int{}
	for i, d := range decisions {
		lastIdx[d.ItemID] = i
	}
	var ds []store.Decision
	var reasons []string
	for i, d := range decisions {
		if i == lastIdx[d.ItemID] && d.Kind == "reject" && strings.TrimSpace(d.Reason) != "" {
			ds = append(ds, d)
			reasons = append(reasons, strings.TrimSpace(d.Reason))
		}
	}
	if len(reasons) == 0 {
		if strings.TrimSpace(current) != "" {
			// every recorded rejection was undone — clear derived rules
			v, err := db.SaveGuide(ctx, pub.ID, "")
			return "", v, err
		}
		return current, version, fmt.Errorf("no rejection reasons recorded yet")
	}

	user, err := pipeline.RenderPrompt("guide", map[string]any{
		"Publication":   pub.Name,
		"EditorialLine": pub.EditorialLine,
		"Decisions":     ds,
		"Current":       current,
	})
	if err != nil {
		return "", 0, err
	}

	resp, err := provider.Complete(ctx, llm.Request{
		Model: model, Template: "guide", User: user, MaxTokens: 2048,
	})
	var content string
	switch {
	case errors.Is(err, llm.ErrMissingFixture):
		content = buildGuide(reasons)
	case err != nil:
		return "", 0, err
	default:
		content = strings.TrimSpace(resp.Text)
	}
	v, err := db.SaveGuide(ctx, pub.ID, content)
	if err != nil {
		return "", 0, err
	}
	return content, v, nil
}

// guideSubject strips leading item references ("esta matéria", "this
// item") so reasons read as general rules rather than per-item notes.
var guideSubject = regexp.MustCompile(`(?i)^(?:esta|esse|essa|este|a|o|this|the)\s+(?:matéria|materia|história|historia|notícia|noticia|item|story|texto|artigo)\s*[,:—-]?\s*`)

var punctRe = regexp.MustCompile(`[.,;:!?()\[\]"'“”‘’]+`)

// buildGuide condenses rejection reasons into short general rules and
// merges near-duplicates (same objection, different wording → one rule).
func buildGuide(reasons []string) string {
	var rules []string
	var toks []map[string]bool
	for _, reason := range reasons {
		rule := ruleFor(reason)
		if rule == "" {
			continue
		}
		t := tokens(rule)
		dup := -1
		for i, rt := range toks {
			// near-duplicate: high token overlap, or one rule is a
			// stricter superset of the other (same objection, extra
			// detail — the shorter, more general phrasing wins).
			if jaccard(t, rt) >= 0.6 || subset(t, rt) || subset(rt, t) {
				dup = i
				break
			}
		}
		if dup >= 0 {
			// keep the shorter (more general) phrasing
			if len(rule) < len(rules[dup]) {
				rules[dup], toks[dup] = rule, t
			}
			continue
		}
		rules = append(rules, rule)
		toks = append(toks, t)
	}
	var b strings.Builder
	for _, r := range rules {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	return strings.TrimSpace(b.String())
}

// ruleFor normalizes a free-text reason into one short rule.
func ruleFor(reason string) string {
	rule := strings.TrimSpace(reason)
	rule = guideSubject.ReplaceAllString(rule, "")
	rule = strings.TrimSpace(rule)
	rule = strings.TrimRight(rule, ".! \t")
	if len(rule) > 120 {
		rule = rule[:120] + "…"
	}
	if r := []rune(rule); len(r) > 0 && unicode.IsLower(r[0]) {
		rule = string(unicode.ToUpper(r[0])) + string(r[1:])
	}
	return rule
}

// tokens normalizes a rule to a word set for similarity checks.
func tokens(rule string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(punctRe.ReplaceAllString(rule, " "))) {
		if !stopword[w] {
			set[w] = true
		}
	}
	return set
}

var stopword = map[string]bool{
	"a": true, "o": true, "e": true, "de": true, "da": true, "do": true,
	"em": true, "no": true, "na": true, "um": true, "uma": true, "para": true,
	"que": true, "com": true, "por": true, "os": true, "as": true, "das": true,
	"dos": true, "the": true, "of": true, "in": true, "and": true, "is": true,
	"to": true, "for": true, "on": true, "at": true, "an": true, "it": true,
}

// subset reports whether every token of a is also in b.
func subset(a, b map[string]bool) bool {
	if len(a) == 0 {
		return false
	}
	for t := range a {
		if !b[t] {
			return false
		}
	}
	return true
}

// jaccard returns |A∩B| / |A∪B| over token sets.
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}
