package web

// The workspace outbox download endpoint: the recorded emails are
// listed and rendered by the React outbox page through /api/v1; the
// .eml export stays a plain file download.
import (
	"database/sql"
	"fmt"
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
