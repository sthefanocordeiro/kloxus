// Package editorial turns review decisions into the visible, editable
// editorial guide used by every subsequent triage and draft call.
package editorial

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// Rebuild regenerates the guide from the publication's decisions.
// Preferred path: the model condenses decisions into rules. Demo-mode
// fallback (no fixture can cover arbitrary judge input): append the latest
// rejection reason as a guide line. Returns content and new version.
func Rebuild(ctx context.Context, db *store.DB, pub *store.Publication, provider llm.Provider, model string) (string, int, error) {
	decisions, err := db.Decisions(ctx, pub.ID)
	if err != nil {
		return "", 0, err
	}
	if len(decisions) == 0 {
		return "", 0, fmt.Errorf("no decisions recorded yet")
	}
	_, current, _ := db.LatestGuide(ctx, pub.ID)

	type decision struct{ Kind, Reason string }
	ds := make([]decision, len(decisions))
	for i, d := range decisions {
		ds[i] = decision{Kind: d.Kind, Reason: d.Reason}
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
		// deterministic fallback: condense the latest reason into a short
		// general rule and deduplicate against the current guide.
		content = condense(current, decisions[len(decisions)-1].Reason)
	case err != nil:
		return "", 0, err
	default:
		content = strings.TrimSpace(resp.Text)
	}
	version, err := db.SaveGuide(ctx, pub.ID, content)
	if err != nil {
		return "", 0, err
	}
	return content, version, nil
}

// condense turns a rejection reason into a short, general editorial rule
// and appends it to the guide, skipping duplicates. Judges' reasons are
// free text, so the demo fallback normalizes instead of copying verbatim
// fragments (trailing punctuation, hedges, repetition).
func condense(current, reason string) string {
	current = strings.TrimSpace(current)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return current
	}
	rule := reason
	rule = strings.TrimRight(rule, ".! \t")
	if len(rule) > 160 {
		rule = rule[:160] + "…"
	}
	if r := []rune(rule); len(r) > 0 && unicode.IsLower(r[0]) {
		rule = string(unicode.ToUpper(r[0])) + string(r[1:])
	}
	line := "- " + rule
	for _, l := range strings.Split(current, "\n") {
		if strings.EqualFold(strings.TrimSpace(l), line) ||
			strings.EqualFold(strings.TrimSpace(l), "- "+reason) {
			return current
		}
	}
	if current != "" {
		return current + "\n" + line
	}
	return line
}
