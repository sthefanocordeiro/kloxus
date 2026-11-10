package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"
)

// Replay serves recorded responses from a fixture FS (demo mode). A missing
// key fails with ErrMissingFixture and, when DumpDir is set, writes the full
// request to <DumpDir>/<key>.request.json for authoring or `kloxus record`.
type Replay struct {
	FS      fs.FS
	Dir     string // fixture dir inside FS, e.g. "fixtures/llm"
	DumpDir string // optional local dir for missing-request dumps
}

// Complete implements Provider.
func (r Replay) Complete(ctx context.Context, req Request) (Response, error) {
	key := Key(req)
	data, err := fs.ReadFile(r.FS, path.Join(r.Dir, key+".json"))
	if err != nil {
		if r.DumpDir != "" {
			if mkErr := os.MkdirAll(r.DumpDir, 0o755); mkErr == nil {
				dump, _ := json.MarshalIndent(map[string]any{
					"key": key, "model": req.Model, "template": req.Template,
					"system": req.System, "user": req.User, "max_tokens": req.MaxTokens,
				}, "", "  ")
				os.WriteFile(filepath.Join(r.DumpDir, key+".request.json"), dump, 0o644)
			}
		}
		return Response{}, fmt.Errorf("%w %s (template=%s, model=%s): record it with `kloxus record` or author %s/%s.json",
			ErrMissingFixture, key, req.Template, req.Model, r.Dir, key)
	}
	var f fixtureFile
	if err := json.Unmarshal(data, &f); err != nil {
		return Response{}, fmt.Errorf("fixture %s: %w", key, err)
	}
	return Response{Text: f.Text, InputTokens: f.InputTokens, OutputTokens: f.OutputTokens}, nil
}

// Anthropic calls the Anthropic messages API (real mode).
type Anthropic struct {
	APIKey  string
	BaseURL string // defaults to https://api.anthropic.com
	HTTP    *http.Client
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete implements Provider.
func (a Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	if a.APIKey == "" {
		return Response{}, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	base := a.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	body, err := json.Marshal(anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Messages:  []anthropicMessage{{Role: "user", Content: req.User}},
	})
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	var ar anthropicResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return Response{}, fmt.Errorf("anthropic: decoding response: %w", err)
	}
	var text string
	for _, c := range ar.Content {
		if c.Type == "text" {
			text = c.Text
			break
		}
	}
	return Response{Text: text, InputTokens: ar.Usage.InputTokens, OutputTokens: ar.Usage.OutputTokens}, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Recorder wraps a Provider and writes each response as a replay fixture
// under Dir/<key>.json. Used by `kloxus record` (and any run in real mode
// where fixtures should be captured).
type Recorder struct {
	Inner Provider
	Dir   string
}

// Complete implements Provider.
func (r Recorder) Complete(ctx context.Context, req Request) (Response, error) {
	resp, err := r.Inner.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return resp, fmt.Errorf("recorder: %w", err)
	}
	f := fixtureFile{
		Model:        req.Model,
		Text:         resp.Text,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return resp, err
	}
	name := filepath.Join(r.Dir, Key(req)+".json")
	if err := os.WriteFile(name, data, 0o644); err != nil {
		return resp, fmt.Errorf("recorder: writing %s: %w", name, err)
	}
	return resp, nil
}
