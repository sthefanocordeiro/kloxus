package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WatchResult is what a provider reports for a check.
type WatchResult struct {
	Status   string // same | changed | new | removed | error | monitoring
	Detail   string
	URL      string
	Markdown string // changed page content, when available
}

// MonitorProvider watches a page URL for changes. Demo uses FakeMonitor;
// real mode uses FirecrawlMonitor (Firecrawl Monitoring API v2).
type MonitorProvider interface {
	Watch(ctx context.Context, url string) (monitorID string, err error)
	Check(ctx context.Context, monitorID string) ([]WatchResult, error)
	Unwatch(ctx context.Context, monitorID string) error
	Name() string
}

// FakeMonitor simulates monitoring in demo mode: deterministic, offline.
type FakeMonitor struct{}

// Name implements MonitorProvider.
func (FakeMonitor) Name() string { return "firecrawl (simulated)" }

// Watch implements MonitorProvider.
func (FakeMonitor) Watch(_ context.Context, url string) (string, error) {
	return "fake-" + url, nil
}

// Check implements MonitorProvider.
func (FakeMonitor) Check(_ context.Context, _ string) ([]WatchResult, error) {
	return []WatchResult{{Status: "monitoring", Detail: "simulated watch — no changes since last run"}}, nil
}

// Unwatch implements MonitorProvider.
func (FakeMonitor) Unwatch(_ context.Context, _ string) error { return nil }

// FirecrawlMonitor talks to the Firecrawl Monitoring API v2.
type FirecrawlMonitor struct {
	APIKey     string
	BaseURL    string // defaults to https://api.firecrawl.dev
	WebhookURL string // public URL for monitor.page events; empty = poll only
	Secret     string // sent as webhook Authorization header and verified inbound
	Goal       string // change-judging goal; default set in Watch
	HTTP       *http.Client
}

// Name implements MonitorProvider.
func (FirecrawlMonitor) Name() string { return "firecrawl" }

func (m FirecrawlMonitor) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (m FirecrawlMonitor) base() string {
	if m.BaseURL == "" {
		return "https://api.firecrawl.dev"
	}
	return m.BaseURL
}

func (m FirecrawlMonitor) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	if m.APIKey == "" {
		return nil, fmt.Errorf("FIRECRAWL_API_KEY is not set")
	}
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.base()+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+m.APIKey)
	resp, err := m.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("firecrawl %s %s: %d: %s", method, path, resp.StatusCode, truncateStr(string(raw), 300))
	}
	return raw, nil
}

// Watch creates a page monitor for the URL (30-minute schedule, scrape target).
func (m FirecrawlMonitor) Watch(ctx context.Context, url string) (string, error) {
	goal := m.Goal
	if goal == "" {
		goal = "Notify me when a new article is published or the page content materially changes"
	}
	payload := map[string]any{
		"name":     "kloxus: " + url,
		"schedule": map[string]any{"text": "every 30 minutes", "timezone": "UTC"},
		"goal":     goal,
		"targets":  []map[string]any{{"type": "scrape", "urls": []string{url}}},
	}
	if m.WebhookURL != "" {
		payload["webhook"] = map[string]any{
			"url":     m.WebhookURL,
			"headers": map[string]string{"Authorization": "Bearer " + m.Secret},
			"events":  []string{"monitor.page"},
		}
	}
	raw, err := m.do(ctx, http.MethodPost, "/v2/monitor", payload)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("firecrawl: unexpected monitor response: %s", truncateStr(string(raw), 200))
	}
	return out.ID, nil
}

// Check polls the monitor's latest check results (localhost fallback when
// webhooks cannot reach this instance).
func (m FirecrawlMonitor) Check(ctx context.Context, monitorID string) ([]WatchResult, error) {
	raw, err := m.do(ctx, http.MethodGet, "/v2/monitor/"+monitorID+"/checks?limit=1", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Checks []struct {
			Status string `json:"status"`
			Pages  []struct {
				URL    string `json:"url"`
				Status string `json:"status"`
			} `json:"pages"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("firecrawl: decoding checks: %w", err)
	}
	if len(out.Checks) == 0 {
		return []WatchResult{{Status: "monitoring", Detail: "no checks run yet"}}, nil
	}
	var res []WatchResult
	for _, p := range out.Checks[0].Pages {
		res = append(res, WatchResult{Status: p.Status, URL: p.URL, Detail: "last check: " + out.Checks[0].Status})
	}
	if len(res) == 0 {
		res = append(res, WatchResult{Status: out.Checks[0].Status})
	}
	return res, nil
}

// Unwatch deletes the monitor.
func (m FirecrawlMonitor) Unwatch(ctx context.Context, monitorID string) error {
	_, err := m.do(ctx, http.MethodDelete, "/v2/monitor/"+monitorID, nil)
	return err
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
