package web

// Deferred work: the jobs table is the shared queue every workspace
// writes to and one dispatcher drains (D28). Claims are atomic via
// FOR UPDATE SKIP LOCKED, so a second dispatcher process would not
// double-send — and a crashed one leaves rows for RequeueStaleJobs.
import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// defaultJobTimeout bounds one scheduled job (e.g. a scheduled send).
const defaultJobTimeout = 5 * time.Minute

func (s *Server) jobTimeout() time.Duration {
	if s.d.JobTimeout > 0 {
		return s.d.JobTimeout
	}
	return defaultJobTimeout
}

// RunScheduler claims and dispatches due jobs on a fixed interval until
// ctx is cancelled. Idle-workspace deletion runs on the workspace
// manager's own ticker.
func (s *Server) RunScheduler(ctx context.Context, pool *store.Pool, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.dispatchDue(ctx, pool)
		}
	}
}

func (s *Server) dispatchDue(ctx context.Context, pool *store.Pool) {
	if err := pool.RequeueStaleJobs(ctx, store.TSUTC(time.Now().Add(-15*time.Minute))); err != nil {
		slog.Warn("requeue stale jobs failed", "err", err)
	}
	jobs, err := pool.ClaimDueJobs(ctx, 20)
	if err != nil {
		slog.Warn("claim jobs failed", "err", err)
		return
	}
	for _, j := range jobs {
		// Bound each job: the scheduler ctx lives for the process, so a
		// send parked on a dead pooled connection would stall the whole
		// dispatch loop. Bookkeeping runs on the parent ctx so failure
		// still records after the job ctx expires.
		jctx, cancel := context.WithTimeout(ctx, s.jobTimeout())
		err := s.runJob(jctx, pool, j)
		cancel()
		if err != nil {
			slog.Warn("job failed", "id", j.ID, "kind", j.Kind, "ws", j.WorkspaceID, "err", err)
			pool.FailJob(ctx, j.ID, err, 3)
			continue
		}
		pool.FinishJob(ctx, j.ID)
	}
}

func (s *Server) runJob(ctx context.Context, pool *store.Pool, j store.Job) error {
	switch j.Kind {
	case store.JobKindSendIssue:
		return s.sendScheduledIssue(ctx, pool, j)
	default:
		return fmt.Errorf("unknown job kind %q", j.Kind)
	}
}

// sendScheduledIssue delivers a scheduled issue to readers and marks it
// sent. A held issue (status moved off "scheduled") or a vanished one
// makes the job a no-op.
func (s *Server) sendScheduledIssue(ctx context.Context, pool *store.Pool, j store.Job) error {
	wdb := pool.For(j.WorkspaceID)
	iss, err := wdb.GetIssue(ctx, j.RefID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if iss.Status != "scheduled" {
		return nil
	}
	pub, err := s.canonicalPub(wdb.GetPublication(ctx, fmt.Sprint(iss.PublicationID)))
	if err != nil {
		return err
	}
	base := strings.TrimRight(s.d.Cfg.PublicURL, "/")
	if base == "" {
		base = fmt.Sprintf("http://localhost:%d", s.d.Cfg.Port)
	}
	if _, err := s.sendIssueMail(ctx, wdb, j.WorkspaceID, iss, pub, []string{"readers@kloxus.demo"}, "issue", base); err != nil {
		return err
	}
	return wdb.SetIssueStatus(ctx, iss.ID, "sent")
}
