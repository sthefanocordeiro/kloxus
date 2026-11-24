// Resend transport behind the Sender seam (D33). Retries what is safe
// to retry — network errors, 5xx and 429 honouring Retry-After — and
// never retries other 4xx, so a rejected message fails fast to the
// outbox fallback.
package delivery

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	resend "github.com/resend/resend-go/v4"

	"github.com/sthefanocordeiro/kloxus/internal/config"
)

// Message is one outgoing email plus its delivery metadata.
type Message struct {
	To      []string
	Subject string
	Text    string // markdown body as the text part
	HTML    string
	Kind    string // issue | test | magic_link | reader_confirm
	// Headers carries RFC headers (List-Unsubscribe etc.) verbatim.
	Headers map[string]string
	// IdemKey dedupes the send at both layers: our ledger and Resend's
	// Idempotency-Key handling.
	IdemKey string
}

// Sender transports a message and returns the provider's id for it.
type Sender interface {
	Send(ctx context.Context, msg *Message) (string, error)
}

// Resend sends through the Resend API using the official Go SDK.
type Resend struct {
	Client *resend.Client
	From   string // validated "Name <local@domain>" at startup
	// MaxAttempts bounds retries; <=1 means a single attempt.
	MaxAttempts int
	// MaxWait caps a Retry-After wait so a 429 never parks a request.
	MaxWait time.Duration
	// Synthetic marks the verification-only transport built by
	// NewLoopbackSender: a loopback fake with a synthetic credential.
	Synthetic bool
}

const resendAttempts = 3

// resendAPI is the one endpoint the production transport may reach —
// pinned so the SDK's own RESEND_BASE_URL environment default can never
// redirect a credential-bearing client.
const resendAPI = "https://api.resend.com/"

// syntheticKey is the fixed credential the loopback transport presents
// to the fake. It is deliberately not a real key — a fake receipt
// proves only that the Resend protocol ran, never that a provider was
// paid.
const syntheticKey = "re_local_verify_synthetic"

// NewResend builds a Resend sender from process config.
func NewResend(apiKey, from string) *Resend {
	c := resend.NewClient(apiKey)
	c.BaseURL, _ = url.Parse(resendAPI)
	return &Resend{Client: c, From: from, MaxWait: 30 * time.Second}
}

// NewLoopbackSender builds the verification-only mail transport: the
// Resend protocol against a validated literal-loopback fake
// (scripts/fake-resend.mjs). The endpoint must pass
// config.ValidateLoopbackEndpoint — anything else is a startup error,
// never a silent fall back to the real API. The client sends the fixed
// synthetic key and refuses redirects, so no real credential or payload
// can leave the machine even if the fake misbehaves.
func NewLoopbackSender(endpoint, from string) (*Resend, error) {
	if err := config.ValidateLoopbackEndpoint(endpoint); err != nil {
		return nil, err
	}
	// The SDK resolves request paths relative to BaseURL — a missing
	// trailing slash would eat the last segment.
	u, _ := url.Parse(strings.TrimSuffix(endpoint, "/") + "/")
	c := resend.NewCustomClient(&http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("loopback mail transport must not redirect")
		},
	}, syntheticKey)
	c.BaseURL = u
	return &Resend{Client: c, From: from, MaxWait: 30 * time.Second, Synthetic: true}, nil
}

// Transport reports the sender's wire identity for server-side
// verification preflights: "synthetic" for the loopback fake, "resend"
// for the real API.
func (r *Resend) Transport() string {
	if r.Synthetic {
		return "synthetic"
	}
	return "resend"
}

func (r *Resend) attempts() int {
	if r.MaxAttempts > 0 {
		return r.MaxAttempts
	}
	return resendAttempts
}

// Send implements Sender with the retry policy: retry on transport
// errors, 5xx and 429 (waiting Retry-After), never on other 4xx.
func (r *Resend) Send(ctx context.Context, msg *Message) (string, error) {
	client := r.Client
	if client == nil {
		client = resend.NewClient("")
		client.BaseURL, _ = url.Parse(resendAPI)
	}
	params := &resend.SendEmailRequest{
		From: r.From, To: msg.To, Subject: msg.Subject,
		Text: msg.Text, Html: msg.HTML, Headers: msg.Headers,
	}
	opts := &resend.SendEmailOptions{IdempotencyKey: msg.IdemKey}

	var last error
	for attempt := 0; attempt < r.attempts(); attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff(attempt, last)); err != nil {
				return "", err
			}
		}
		resp, err := client.Emails.SendWithOptions(ctx, params, opts)
		if err == nil {
			return resp.Id, nil
		}
		last = err
		if !retryable(err) {
			return "", err
		}
	}
	return "", last
}

// retryable reports whether a failed send is worth another attempt:
// transport errors, server errors and rate limits — never a 4xx the
// server meant.
func retryable(err error) bool {
	var rl *resend.RateLimitError
	if errors.As(err, &rl) {
		return true
	}
	var re *resend.ResendError
	if errors.As(err, &re) {
		return re.StatusCode >= 500
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return true
	}
	// Not an SDK-shaped error (dial failure, EOF, ctx-independent I/O):
	// treat as transient unless it is a caller cancellation.
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// backoff picks the wait before the next attempt: Retry-After when the
// server asked for one, otherwise a short exponential wait. Retry-After
// beyond maxRetryWait is not waited out — the send fails to fallback.
const maxRetryWait = 30 * time.Second

func backoff(attempt int, last error) time.Duration {
	var rl *resend.RateLimitError
	if errors.As(last, &rl) {
		if secs, err := strconv.Atoi(rl.RetryAfter); err == nil && secs > 0 {
			if w := time.Duration(secs) * time.Second; w <= maxRetryWait {
				return w
			}
		}
	}
	return time.Duration(attempt) * 500 * time.Millisecond
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
