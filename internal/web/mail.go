package web

// Real email delivery (D33): the outbox is the ledger for every
// attempted send — caps survive restarts, idempotency keys dedupe
// retries, and a Resend failure leaves the message in the outbox
// instead of dropping it. claim/send rules are enforced in api.go;
// this file owns the transport mechanics.
import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// liveMail reports whether real delivery is configured — the sender is
// injected (Resend in production, a fake server in tests), so
// outbox-only deployments keep the demo semantics untouched.
func (s *Server) liveMail() bool { return s.d.Mailer != nil }

// mailResult reports how one message left (or didn't).
type mailResult struct {
	Mail     *store.OutboxMail
	Deduped  bool   // an earlier send with this idempotency key was reused
	Refused  string // non-empty when the send was refused (cap, suppression)
	Fallback bool   // Resend failed; the message is an outbox copy
}

// Sent reports whether the message went out for real.
func (r *mailResult) Sent() bool {
	return r != nil && r.Mail != nil && r.Mail.Status == "sent" && r.Mail.Provider == "resend"
}

// today is the UTC day cap window ('YYYY-MM-DD').
func today() string { return time.Now().UTC().Format("2006-01-02") }

// sendMail records and transports one message. Errors only on
// infrastructure failures (the database) — refusals and provider
// failures are reported in the result so callers can surface them.
func (s *Server) sendMail(ctx context.Context, wdb *store.DB, msg *delivery.Message) (*mailResult, error) {
	if len(msg.To) != 1 {
		return nil, fmt.Errorf("sendMail expects exactly one recipient per message, got %d", len(msg.To))
	}
	to := msg.To[0]

	// Suppression is global and absolute — never mail a bounced or
	// complained address again, whatever the rules above it say.
	if s.liveMail() {
		sup, err := s.suppressed(ctx, wdb, []string{to})
		if err != nil {
			return nil, err
		}
		if sup[to] {
			return s.refuseMail(ctx, wdb, msg, "address suppressed")
		}
	}

	if s.liveMail() {
		if reason, err := s.capReason(ctx, wdb); err != nil {
			return nil, err
		} else if reason != "" {
			return s.refuseMail(ctx, wdb, msg, reason)
		}
	}

	provider, status := "outbox", "sent"
	if s.liveMail() {
		provider, status = "resend", "queued"
	}
	id, created, err := wdb.SaveMail(ctx, msg.Kind, to, msg.Subject, msg.Text, msg.HTML, provider, status, msg.IdemKey)
	if err != nil {
		return nil, err
	}
	if !created {
		m, err := wdb.GetMail(ctx, id)
		if err != nil {
			return nil, err
		}
		return &mailResult{Mail: m, Deduped: true}, nil
	}
	if !s.liveMail() {
		m, err := wdb.GetMail(ctx, id)
		return &mailResult{Mail: m}, err
	}

	pid, err := s.d.Mailer.Send(ctx, msg)
	if err != nil {
		cause := sanitizeErr(err)
		if ferr := wdb.MailFallback(ctx, id, cause); ferr != nil {
			return nil, ferr
		}
		m, gerr := wdb.GetMail(ctx, id)
		if gerr != nil {
			return nil, gerr
		}
		slog.Warn("mail fell back to outbox", "id", id, "kind", msg.Kind, "err", cause, "rid", reqIDFromCtx(ctx))
		return &mailResult{Mail: m, Fallback: true}, nil
	}
	if err := wdb.MailSent(ctx, id, pid); err != nil {
		return nil, err
	}
	m, err := wdb.GetMail(ctx, id)
	return &mailResult{Mail: m}, err
}

// refuseMail records a refused send in the ledger (never silently
// dropped) and reports it.
func (s *Server) refuseMail(ctx context.Context, wdb *store.DB, msg *delivery.Message, reason string) (*mailResult, error) {
	id, err := wdb.SaveMailRefused(ctx, msg.Kind, msg.To[0], msg.Subject, reason)
	if err != nil {
		return nil, err
	}
	m, err := wdb.GetMail(ctx, id)
	if err != nil {
		return nil, err
	}
	return &mailResult{Mail: m, Refused: reason}, nil
}

// capReason returns "" or why the send must be refused under the
// persisted daily caps (global and per-workspace).
func (s *Server) capReason(ctx context.Context, wdb *store.DB) (string, error) {
	day := today()
	if pool := s.d.WS.Pool(); pool != nil {
		n, err := pool.SendsTodayAll(ctx, day)
		if err != nil {
			return "", err
		}
		if n >= s.d.Cfg.EmailDailyCap {
			return fmt.Sprintf("global daily email cap reached (%d/day)", s.d.Cfg.EmailDailyCap), nil
		}
	}
	n, err := wdb.SendsToday(ctx, day)
	if err != nil {
		return "", err
	}
	if n >= s.d.Cfg.EmailWorkspaceDailyCap {
		return fmt.Sprintf("workspace daily email cap reached (%d/day)", s.d.Cfg.EmailWorkspaceDailyCap), nil
	}
	return "", nil
}

// suppressed checks the global suppression list, pooled or local.
func (s *Server) suppressed(ctx context.Context, wdb *store.DB, emails []string) (map[string]bool, error) {
	if pool := s.d.WS.Pool(); pool != nil {
		return pool.Suppressed(ctx, emails)
	}
	return wdb.Suppressed(ctx, emails)
}

// suppress adds an address to the global suppression list.
func (s *Server) suppress(ctx context.Context, wdb *store.DB, email, reason string) error {
	if pool := s.d.WS.Pool(); pool != nil {
		return pool.SuppressEmail(ctx, email, reason)
	}
	return wdb.SuppressEmail(ctx, email, reason)
}

// workspaceOwner returns the claiming owner's email ("" when anonymous).
func (s *Server) workspaceOwner(ctx context.Context, wdb *store.DB) string {
	if pool := s.d.WS.Pool(); pool != nil {
		email, _, err := pool.WorkspaceOwner(ctx, wdb.Workspace())
		if err == nil {
			return email
		}
		return ""
	}
	email, _, err := wdb.WorkspaceOwner(ctx)
	if err != nil {
		return ""
	}
	return email
}

// sanitizeErr trims a provider error for the ledger/logs: one line,
// bounded — the API key never reaches error text, but keep the stored
// record tidy anyway.
func sanitizeErr(err error) string {
	m := strings.Join(strings.Fields(err.Error()), " ")
	if len(m) > 400 {
		m = m[:400]
	}
	return m
}

func reqIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(ctxReqID).(string); ok {
		return v
	}
	return ""
}

// validEmail accepts a single RFC 5322 address, no display name.
func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}
