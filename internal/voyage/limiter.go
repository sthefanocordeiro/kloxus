package voyage

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrTooLarge means a single request exceeds the group's whole TPM
// budget and could never be admitted.
var ErrTooLarge = errors.New("voyage: request exceeds the tokens-per-minute budget")

// Limits is one model group's request and token budget per minute.
type Limits struct {
	RPM int
	TPM int
}

type usage struct {
	at     time.Time
	tokens int
}

// Limiter is a sliding 60-second window over requests and estimated
// tokens. In-process only: correct while the app runs on a single Fly
// machine (recorded in docs/decisions.md D60).
type Limiter struct {
	limits Limits
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error

	mu     sync.Mutex
	events []usage
}

// NewLimiter builds a limiter. now and sleep are injectable so tests
// run on a fake clock; nil falls back to the wall clock.
func NewLimiter(l Limits, now func() time.Time, sleep func(context.Context, time.Duration) error) *Limiter {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepCtx
	}
	return &Limiter{limits: l, now: now, sleep: sleep}
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

const window = time.Minute

// Wait blocks until a request of the given estimated tokens fits in
// the window, then reserves it. A zero limit disables that dimension.
func (l *Limiter) Wait(ctx context.Context, tokens int) error {
	if l.limits.TPM > 0 && tokens > l.limits.TPM {
		return ErrTooLarge
	}
	for {
		l.mu.Lock()
		now := l.now()
		cut := 0
		for cut < len(l.events) && !l.events[cut].at.After(now.Add(-window)) {
			cut++
		}
		l.events = l.events[cut:]
		reqs, toks := len(l.events), 0
		for _, e := range l.events {
			toks += e.tokens
		}
		okReq := l.limits.RPM <= 0 || reqs+1 <= l.limits.RPM
		okTok := l.limits.TPM <= 0 || toks+tokens <= l.limits.TPM
		if okReq && okTok {
			l.events = append(l.events, usage{at: now, tokens: tokens})
			l.mu.Unlock()
			return nil
		}
		// Wait for the oldest event to leave the window, then re-check.
		wait := l.events[0].at.Add(window).Sub(now)
		l.mu.Unlock()
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if err := l.sleep(ctx, wait); err != nil {
			return err
		}
	}
}
