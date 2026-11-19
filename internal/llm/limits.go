package llm

// Prompt-size caps (KLOX-44): every model call's input is estimated
// before the provider sees it and refused when it exceeds the
// template's cap. A refusal is a run error — never a retry and never a
// fallback to a bigger call.
import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// EstimateTokens approximates input tokens as characters/3 — the bound
// the owner accepted for prompt-size caps.
func EstimateTokens(s string) int { return utf8.RuneCountInString(s) / 3 }

// MaxInputTokens bounds one call's estimated input tokens per prompt
// template. Triage/storylist carry the whole story set, so they get the
// larger 40k family cap; the evidence-driven steps (dossier, deepdive,
// draft, citecheck) are capped by the ≤3-sources rule and get 30k.
var MaxInputTokens = map[string]int{
	"triage":    40000,
	"storylist": 40000,
	"dossier":   30000,
	"deepdive":  30000,
	"draft":     30000,
	"citecheck": 30000,
}

// defaultMaxInputTokens bounds templates without a listed cap (guide,
// interactive calls, future steps) at the tighter family cap.
const defaultMaxInputTokens = 30000

// PromptTooLargeError refuses an over-cap request before any provider
// call. It carries the estimate and the cap so the run error names the
// step that must shrink its input.
type PromptTooLargeError struct {
	Template  string
	Estimated int
	Cap       int
}

func (e *PromptTooLargeError) Error() string {
	return fmt.Sprintf("prompt %q is ~%d input tokens, over the %d cap — the call was refused before reaching the provider",
		e.Template, e.Estimated, e.Cap)
}

// AsTooLarge reports whether err is a *PromptTooLargeError.
func AsTooLarge(err error) (*PromptTooLargeError, bool) {
	var p *PromptTooLargeError
	if errors.As(err, &p) {
		return p, true
	}
	return nil, false
}

// CheckSize estimates a request's input (system + user) and refuses
// with *PromptTooLargeError when it exceeds the template's cap.
func CheckSize(req Request) error {
	limit := MaxInputTokens[req.Template]
	if limit == 0 {
		limit = defaultMaxInputTokens
	}
	est := EstimateTokens(req.System) + EstimateTokens(req.User)
	if est > limit {
		return &PromptTooLargeError{Template: req.Template, Estimated: est, Cap: limit}
	}
	return nil
}
