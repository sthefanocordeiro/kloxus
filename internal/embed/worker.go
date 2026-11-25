package embed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/voyage"
)

// Pending is an item awaiting its vector.
type Pending struct {
	ID    int64
	Title string
	Body  string
}

// Vector is one computed row, ready to store.
type Vector struct {
	ItemID  int64
	Literal string // '[f,f,...]'
}

// Store is the persistence the worker and backfill need.
// Implemented by store.Pool.
type Store interface {
	// PendingEmbeddings lists non-promo items without a vector for
	// model whose effective date is >= since, newest first.
	PendingEmbeddings(ctx context.Context, model string, since time.Time, limit int) ([]Pending, error)
	// PendingStats counts them and sums the characters ItemText would
	// send (title + capped body), for the backfill projection.
	PendingStats(ctx context.Context, model string, since time.Time) (items int, chars int64, err error)
	SaveItemEmbeddings(ctx context.Context, model string, dims int, rows []Vector) error
}

// DefaultDays is the candidate window (D41): older items are never
// ranked, so they are never embedded by the queue either.
const DefaultDays = 7

// Worker is the background embedding queue. Ingestion calls Kick and
// returns immediately - it never waits on Voyage; an item without a
// vector yet is ranked by the FTS fallback.
type Worker struct {
	Store    Store
	Embedder Embedder
	Log      *slog.Logger
	Days     int

	kick chan struct{}
}

func (w *Worker) logInfo(msg string, args ...any) {
	if w.Log != nil {
		w.Log.Info(msg, args...)
	}
}

func (w *Worker) logWarn(msg string, args ...any) {
	if w.Log != nil {
		w.Log.Warn(msg, args...)
	}
}

func (w *Worker) since() time.Time {
	d := w.Days
	if d <= 0 {
		d = DefaultDays
	}
	return time.Now().UTC().Add(-time.Duration(d) * 24 * time.Hour)
}

// Kick asks the loop to drain the queue. Non-blocking and coalescing:
// any number of kicks while a drain runs cause at most one more drain.
func (w *Worker) Kick() {
	if w == nil || w.kick == nil {
		return
	}
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Start prepares the kick channel; call before Run and before any Kick.
func (w *Worker) Start() {
	if w.kick == nil {
		w.kick = make(chan struct{}, 1)
	}
}

// Run drains whenever kicked until ctx ends. A drain stopped by the
// off provider or the daily cap is not an error worth shouting about.
func (w *Worker) Run(ctx context.Context) {
	w.Start()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.kick:
		}
		n, err := w.Drain(ctx, w.since(), nil)
		switch {
		case err == nil:
			if n > 0 {
				w.logInfo("embed queue: items embedded", "count", n)
			}
		case errors.Is(err, ErrDisabled), errors.Is(err, voyage.ErrCapReached):
			// fallback ranking takes over; the client logged the cap once
		default:
			w.logWarn("embed queue: stopped", "count", n, "err", err)
		}
	}
}

// Drain embeds pending items newest-first in batches until none are
// left or an error stops it. Resumable by construction: stored items
// leave the pending set, so a rerun continues where it stopped.
// progress, when set, is called after every stored batch with the
// running total.
func (w *Worker) Drain(ctx context.Context, since time.Time, progress func(done int)) (int, error) {
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		items, err := w.Store.PendingEmbeddings(ctx, w.Embedder.Model(), since, MaxBatchTexts)
		if err != nil {
			return total, err
		}
		if len(items) == 0 {
			return total, nil
		}
		texts := make([]string, len(items))
		for i, it := range items {
			texts[i] = ItemText(it.Title, it.Body)
		}
		vecs, err := w.Embedder.Embed(ctx, texts, Document)
		if err != nil {
			return total, err
		}
		rows := make([]Vector, len(items))
		for i, it := range items {
			rows[i] = Vector{ItemID: it.ID, Literal: Literal(vecs[i])}
		}
		if err := w.Store.SaveItemEmbeddings(ctx, w.Embedder.Model(), w.Embedder.Dims(), rows); err != nil {
			return total, err
		}
		total += len(items)
		if progress != nil {
			progress(total)
		}
	}
}

// BackfillLimitUSD is the most a backfill may project before it
// refuses to run (owner limit, KLOX-107 §4).
const BackfillLimitUSD = 0.10

// Projection is what a backfill would cost, at list price.
type Projection struct {
	Items  int
	Tokens int64
	USD    float64
}

// ErrOverBudget refuses a backfill whose projected list cost exceeds
// BackfillLimitUSD.
var ErrOverBudget = errors.New("embed: projected backfill cost exceeds the USD 0.10 limit")

// Project estimates the backfill for the last `days` days.
func (w *Worker) Project(ctx context.Context, days int) (Projection, error) {
	n, chars, err := w.Store.PendingStats(ctx, w.Embedder.Model(), time.Now().UTC().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		return Projection{}, err
	}
	tokens := (chars + 2) / 3
	return Projection{Items: n, Tokens: tokens, USD: float64(tokens) / 1e6 * voyage.PricePerMTok}, nil
}

// Backfill prints the projection, refuses above BackfillLimitUSD, then
// drains newest-first logging progress through out.
func (w *Worker) Backfill(ctx context.Context, days int, out func(string)) (int, error) {
	p, err := w.Project(ctx, days)
	if err != nil {
		return 0, err
	}
	out(fmt.Sprintf("embed-backfill: %d items, ~%d tokens, projected list cost $%.4f (limit $%.2f)",
		p.Items, p.Tokens, p.USD, BackfillLimitUSD))
	if p.USD > BackfillLimitUSD {
		return 0, ErrOverBudget
	}
	if p.Items == 0 {
		out("embed-backfill: nothing to do")
		return 0, nil
	}
	n, err := w.Drain(ctx, time.Now().UTC().Add(-time.Duration(days)*24*time.Hour), func(done int) {
		out(fmt.Sprintf("embed-backfill: %d/%d", done, p.Items))
	})
	if err != nil {
		return n, fmt.Errorf("%w (embedded %d, rerun to resume)", err, n)
	}
	out(fmt.Sprintf("embed-backfill: done, %d items embedded", n))
	return n, nil
}
