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

// HardMaxPromptTokens is the absolute ceiling for every request
// (KLOX-67 §7.1): above 100k prompt tokens the whole request bills at
// the provider's higher tier, so the guard refuses outright. Template
// caps below keep prompts at their ≤20k/≤40k targets; this is the
// last-line backstop for a prompt that slips past its template bound.
const HardMaxPromptTokens = 100_000

// MaxInputTokens bounds one call's estimated input tokens per prompt
// template. Triage/storylist carry the whole story set, so they get the
// larger 40k family cap; the evidence-driven steps (dossier, deepdive,
// draft, citecheck) are capped by the ≤3-sources rule and get 30k.
// The shared AI steps (enrich/research/rerank) are the KLOX-67 cheap
// path and target <20k input tokens.
var MaxInputTokens = map[string]int{
	"triage":    40000,
	"storylist": 40000,
	"dossier":   30000,
	"deepdive":  30000,
	"draft":     30000,
	"citecheck": 30000,
	"enrich":    20000,
	"research":  20000,
	"rerank":    20000,
}

// defaultMaxInputTokens bounds templates without a listed cap (guide,
// interactive calls, future steps) at the tighter family cap.
const defaultMaxInputTokens = 30000

// AllowedModel is the single model every AI step runs on (KLOX-67
// §7.1): Claude Haiku 5.5. No Sonnet, no Opus — the check below refuses
// any other id before a request can reach the provider.
const AllowedModel = "claude-haiku-5-5"

// ModelNotAllowedError refuses a request built for a non-Haiku model.
type ModelNotAllowedError struct {
	Model string
}

func (e *ModelNotAllowedError) Error() string {
	return fmt.Sprintf("model %q is not allowed — every AI step must use %s", e.Model, AllowedModel)
}

// AsModelNotAllowed reports whether err is a *ModelNotAllowedError.
func AsModelNotAllowed(err error) (*ModelNotAllowedError, bool) {
	var m *ModelNotAllowedError
	if errors.As(err, &m) {
		return m, true
	}
	return nil, false
}

// CheckModel enforces the Haiku-only rule: any model id other than
// claude-haiku-5-5 is refused before the provider sees the request.
func CheckModel(req Request) error {
	if req.Model != AllowedModel {
		return &ModelNotAllowedError{Model: req.Model}
	}
	return nil
}

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
// with *PromptTooLargeError when it exceeds the template's cap — or the
// absolute 100k backstop when no template cap applies first.
func CheckSize(req Request) error {
	limit := MaxInputTokens[req.Template]
	if limit == 0 {
		limit = defaultMaxInputTokens
	}
	if limit > HardMaxPromptTokens {
		limit = HardMaxPromptTokens
	}
	est := EstimateTokens(req.System) + EstimateTokens(req.User)
	if est > limit {
		return &PromptTooLargeError{Template: req.Template, Estimated: est, Cap: limit}
	}
	return nil
}
