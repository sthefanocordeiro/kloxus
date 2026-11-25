package llm

// KLOX-67 tests: Haiku-only enforcement, the replay alias for
// pre-KLOX-67 fixtures, the 100k hard backstop, and the cheap-step
// template caps.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// TestCheckModelHaikuOnly: every AI step must run on Claude Haiku 5.5 —
// any other id is a typed refusal before the provider sees it.
func TestCheckModelHaikuOnly(t *testing.T) {
	if err := CheckModel(Request{Model: AllowedModel}); err != nil {
		t.Fatalf("haiku should pass: %v", err)
	}
	for _, m := range []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-4-5", ""} {
		err := CheckModel(Request{Model: m})
		var mn *ModelNotAllowedError
		if !errors.As(err, &mn) {
			t.Fatalf("model %q: err=%v, want *ModelNotAllowedError", m, err)
		}
	}
}

// TestAnthropicRefusesNonHaiku: the paid edge refuses a non-Haiku
// request without an HTTP call — the transport must never run.
func TestAnthropicRefusesNonHaiku(t *testing.T) {
	a := Anthropic{
		APIKey: "test-key",
		HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("non-Haiku request reached the HTTP transport")
			return nil, nil
		})},
	}
	_, err := a.Complete(context.Background(), Request{Model: "claude-sonnet-5-5"})
	var mn *ModelNotAllowedError
	if !errors.As(err, &mn) {
		t.Fatalf("err=%v, want *ModelNotAllowedError", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestReplayLegacyAlias: a fixture recorded under claude-sonnet-5-5
// (pre-KLOX-67) still serves the same prompt requested under
// claude-haiku-5-5 — the fixture set survives the model switch without
// a paid re-record.
func TestReplayLegacyAlias(t *testing.T) {
	legacy := Request{Model: "claude-sonnet-5-5", Template: "draft", User: "prompt"}
	fsys := fstest.MapFS{
		"llm/" + Key(legacy) + ".json": {Data: []byte(`{"model":"claude-sonnet-5-5","text":"recorded","input_tokens":1,"output_tokens":1}`)},
	}
	rp := Replay{FS: fsys, Dir: "llm"}
	resp, err := rp.Complete(context.Background(), Request{
		Model: "claude-haiku-5-5", Template: "draft", User: "prompt",
	})
	if err != nil {
		t.Fatalf("alias should hit the sonnet-keyed fixture: %v", err)
	}
	if resp.Text != "recorded" {
		t.Fatalf("resp = %+v", resp)
	}
	// The alias only applies to Haiku requests: a different prompt under
	// a non-Haiku model stays a plain miss.
	miss := Request{Model: "claude-sonnet-5-5", Template: "draft", User: "other"}
	if _, err := rp.Complete(context.Background(), miss); !errors.Is(err, ErrMissingFixture) {
		t.Fatalf("non-haiku miss should stay a miss, got %v", err)
	}
}

// TestCheckSizeSharedCaps: the KLOX-67 cheap steps cap at 20k input
// tokens; everything is also under the 100k backstop.
func TestCheckSizeSharedCaps(t *testing.T) {
	for _, tmpl := range []string{"enrich", "research", "rerank"} {
		req := Request{Template: tmpl, User: strings.Repeat("x", 20*3*1000+3)} // ~20.001k
		var p *PromptTooLargeError
		err := CheckSize(req)
		if !errors.As(err, &p) {
			t.Fatalf("%s: err=%v, want over-cap refusal", tmpl, err)
		}
		if p.Cap != 20000 {
			t.Fatalf("%s: cap=%d, want 20000", tmpl, p.Cap)
		}
		if err := CheckSize(Request{Template: tmpl, User: "small"}); err != nil {
			t.Fatalf("%s small: %v", tmpl, err)
		}
	}
}

// TestCheckSizeHardCeiling: a template cap above 100k is clamped to the
// absolute backstop — no request can ever price into the >100k tier.
func TestCheckSizeHardCeiling(t *testing.T) {
	saved := MaxInputTokens["dossier"]
	MaxInputTokens["dossier"] = 150_000
	defer func() { MaxInputTokens["dossier"] = saved }()

	req := Request{Template: "dossier", User: strings.Repeat("x", 110*3*1000)} // ~110k
	var p *PromptTooLargeError
	err := CheckSize(req)
	if !errors.As(err, &p) {
		t.Fatalf("err=%v, want over-cap refusal", err)
	}
	if p.Cap != HardMaxPromptTokens {
		t.Fatalf("cap=%d, want the %d hard ceiling", p.Cap, HardMaxPromptTokens)
	}
	// 90k passes: under the hard ceiling even with a loose template cap.
	if err := CheckSize(Request{Template: "dossier", User: strings.Repeat("x", 90*3*1000)}); err != nil {
		t.Fatalf("90k should pass: %v", err)
	}
}
