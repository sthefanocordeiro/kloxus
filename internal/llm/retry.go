package llm

// Retry with exponential backoff around a provider (D32): 429, 5xx and
// network/timeout errors are retried; 4xx client errors, decode
// failures and caller-side cancellations are not.
import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"time"
)

// StatusError is a non-OK HTTP response from a provider.
type StatusError struct {
	Provider string
	Status   int
	Body     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: status %d: %s", e.Provider, e.Status, e.Body)
}

// retryable reports whether err is worth retrying: rate limits, server
// errors and transport failures (timeouts, resets, DNS).
func retryable(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status == 429 || se.Status >= 500
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return true
	}
	return false
}

// Retrier wraps a Provider with bounded exponential backoff.
type Retrier struct {
	Inner     Provider
	Attempts  int           // total tries; <=0 means 4
	BaseDelay time.Duration // first wait; <=0 means 500ms
	// Sleep waits between tries; nil uses a jittered timer honoring ctx.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Complete implements Provider.
func (r Retrier) Complete(ctx context.Context, req Request) (Response, error) {
	attempts := r.Attempts
	if attempts <= 0 {
		attempts = 4
	}
	base := r.BaseDelay
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	var resp Response
	var err error
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return resp, ctx.Err()
		}
		resp, err = r.Inner.Complete(ctx, req)
		if err == nil || !retryable(err) || i == attempts-1 {
			return resp, err
		}
		if err := sleep(ctx, base<<i+time.Duration(rand.Int63n(int64(base)))); err != nil {
			return resp, err
		}
	}
	return resp, err
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
