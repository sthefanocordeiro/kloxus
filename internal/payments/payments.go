// Package payments abstracts the paid-section checkout provider
// (KLOX-13, D34/D56). AbacatePay is the real provider; the fake stays
// for tests and as the runtime fallback when the key is missing or the
// provider cannot be reached.
//
// Access model (D56): AbacatePay has recurring billing
// (/v2/subscriptions) but it charges cards only — PIX subscriptions
// need "PIX Automático", which is gated per store and not offered in
// test mode. The reader flow is PIX, so a subscription is sold as a
// ONE_TIME PIX checkout granting 30 days of access (subscriptions.
// paid_until). A later PIX Automático integration would swap this for
// a recurring checkout without touching the gating layer.
package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider names recorded on payments and subscriptions rows.
const (
	AbacatePayName = "abacatepay"
	FakeName       = "fake"
)

// Billing is one hosted checkout at the provider.
type Billing struct {
	ID          string // bill_...
	URL         string // hosted checkout page the reader is sent to
	Status      string // PENDING | PAID | EXPIRED | CANCELLED | REFUNDED
	AmountCents int
	DevMode     bool // created under a test-mode key
}

// BillingRequest describes the checkout to create. AccessDays is what
// the purchase grants; the provider sells it as a one-time product.
type BillingRequest struct {
	ExternalID    string // our payment ref (idempotency + lookup)
	ProductName   string
	ProductDesc   string
	PriceCents    int
	ReturnURL     string // reader clicked "back" on the checkout
	CompletionURL string // reader finished paying
	Metadata      map[string]any
}

// Provider creates checkouts and reports their status — the polling
// fallback used when the webhook does not arrive.
type Provider interface {
	Name() string
	// TestMode reports whether the configured credential is the
	// provider's sandbox — the UI labels it "no real charge".
	TestMode() bool
	CreateBilling(ctx context.Context, req BillingRequest) (*Billing, error)
	// BillingStatus returns the provider-side status string
	// (PENDING/PAID/EXPIRED/CANCELLED/REFUNDED) for a billing id.
	BillingStatus(ctx context.Context, billingID string) (string, error)
}

// abacatePayBase is the v1 API root — test and production share it;
// the key prefix (abc_dev_ / abc_prod_) selects the environment.
const abacatePayBase = "https://api.abacatepay.com"

// AbacatePay is the real provider: the Kloxus test-mode key (D34)
// behind a minimal v1 client (billing create + list).
type AbacatePay struct {
	APIKey string
	// BaseURL overrides the API root — tests point it at a fake
	// AbacatePay server. Empty means the real endpoint.
	BaseURL string
	HTTP    *http.Client
}

func (a AbacatePay) Name() string { return AbacatePayName }

// TestMode: the environment comes from the key, not the URL — dev keys
// start abc_dev_ (docs.abacatepay.com/pages/comecar/modo-teste).
func (a AbacatePay) TestMode() bool { return strings.HasPrefix(a.APIKey, "abc_dev_") }

func (a AbacatePay) base() string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	return abacatePayBase
}

func (a AbacatePay) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// apiError surfaces a provider failure: HTTP status plus the envelope's
// own error string, never the request headers (the key stays out of it).
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("abacatepay: HTTP %d: %s", e.status, e.msg) }

// envelope is the {data,error} wrapper every v1 response returns. The
// error field is a string in some responses and an object in others —
// RawMessage keeps the decode tolerant.
type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error json.RawMessage `json:"error"`
}

func (a AbacatePay) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var env envelope
	if jerr := json.Unmarshal(raw, &env); jerr != nil {
		return nil, &apiError{status: res.StatusCode, msg: "invalid JSON response"}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || (len(env.Error) > 0 && string(env.Error) != "null") {
		msg := http.StatusText(res.StatusCode)
		if s, jerr := errorText(env.Error); jerr == nil && s != "" {
			msg = s
		}
		return nil, &apiError{status: res.StatusCode, msg: msg}
	}
	return env.Data, nil
}

// errorText extracts the provider error message whether it arrived as a
// plain string or an {error:{message}} object.
func errorText(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Message != "" {
		return obj.Message, nil
	}
	return "", fmt.Errorf("unrecognized error shape")
}

// CreateBilling opens a one-time PIX checkout (POST /v1/billing/create)
// and returns the hosted payment page URL to send the reader to.
func (a AbacatePay) CreateBilling(ctx context.Context, req BillingRequest) (*Billing, error) {
	payload := map[string]any{
		"frequency": "ONE_TIME",
		"methods":   []string{"PIX"},
		"products": []map[string]any{{
			"externalId":  req.ExternalID,
			"name":        req.ProductName,
			"description": req.ProductDesc,
			"quantity":    1,
			"price":       req.PriceCents,
		}},
		"externalId":    req.ExternalID,
		"returnUrl":     req.ReturnURL,
		"completionUrl": req.CompletionURL,
	}
	if req.Metadata != nil {
		payload["metadata"] = req.Metadata
	}
	data, err := a.do(ctx, http.MethodPost, "/v1/billing/create", payload)
	if err != nil {
		return nil, err
	}
	var b Billing
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("abacatepay: decoding billing: %w", err)
	}
	if b.ID == "" || b.URL == "" {
		return nil, fmt.Errorf("abacatepay: billing response carries no id/url")
	}
	return &b, nil
}

// billingRow is the subset of the Billing schema the status poll reads.
type billingRow struct {
	ID         string `json:"id"`
	ExternalID string `json:"externalId"`
	Status     string `json:"status"`
	DevMode    bool   `json:"devMode"`
}

// BillingStatus polls GET /v1/billing/list and reports the status of
// one billing id — the fallback path for a webhook that never arrived.
// A billing that vanished reports EXPIRED rather than erroring: the
// caller treats it as unpayable either way.
func (a AbacatePay) BillingStatus(ctx context.Context, billingID string) (string, error) {
	data, err := a.do(ctx, http.MethodGet, "/v1/billing/list", nil)
	if err != nil {
		return "", err
	}
	var rows []billingRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return "", fmt.Errorf("abacatepay: decoding billing list: %w", err)
	}
	for _, b := range rows {
		if b.ID == billingID {
			return b.Status, nil
		}
	}
	return "EXPIRED", nil
}

// Fake is the no-key/no-network provider for tests and the documented
// fallback: it creates a checkout that is immediately "paid" and lets
// the caller run the whole grant path without leaving the app.
type Fake struct {
	// Err, when set, makes CreateBilling and BillingStatus fail — the
	// provider-down path the UI must survive.
	Err error
	// Created records every billing the caller opened (assertions).
	Created []BillingRequest
}

func (f *Fake) Name() string  { return FakeName }
func (f *Fake) TestMode() bool { return true }

func (f *Fake) CreateBilling(_ context.Context, req BillingRequest) (*Billing, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.Created = append(f.Created, req)
	return &Billing{
		ID:          "sim-" + req.ExternalID,
		URL:         req.CompletionURL,
		Status:      "PAID",
		AmountCents: req.PriceCents,
		DevMode:     true,
	}, nil
}

func (f *Fake) BillingStatus(_ context.Context, _ string) (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	return "PAID", nil
}
