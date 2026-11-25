package web

// The workspace outbox download endpoint: the recorded emails are
// listed and rendered by the React outbox page through /api/v1; the
// .eml export stays a plain file download.
import (
	"database/sql"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func (s *Server) outboxEmail(r *http.Request) (*store.OutboxEmail, error) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return db(r).GetOutboxEmail(r.Context(), id)
}

// outboxHTML serves a recorded email's rendered body as a real
// document. The outbox page iframes this URL — a sandboxed srcdoc
// frame painted blank intermittently at narrow widths, and a fetched
// frame has no such quirk (KLOX-100). The response carries the strict
// CSP sandbox itself, so the preview stays inert even if the iframe's
// own sandbox attribute is ever dropped; images and inline styles the
// email body uses still render.
func (s *Server) outboxHTML(w http.ResponseWriter, r *http.Request) {
	e, err := s.outboxEmail(r)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "email not found", http.StatusNotFound)
			return
		}
		slog.Error("outbox html", "err", err, "rid", reqIDOf(r))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy",
		"sandbox; default-src 'none'; img-src 'self' data: https:; "+
			"style-src 'unsafe-inline'; font-src 'self' data:")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if e.BodyHTML == "" {
		w.Write([]byte("<!DOCTYPE html><html><body><pre style=\"white-space:pre-wrap;font-family:monospace\">" +
			html.EscapeString(e.BodyMD) + "</pre></body></html>"))
		return
	}
	w.Write([]byte(e.BodyHTML))
}

func (s *Server) outboxEML(w http.ResponseWriter, r *http.Request) {
	e, err := s.outboxEmail(r)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "email not found", http.StatusNotFound)
			return
		}
		slog.Error("outbox eml", "err", err, "rid", reqIDOf(r))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "message/rfc822")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=email-%d.eml", e.ID))
	w.Write([]byte(delivery.EML(e)))
}
