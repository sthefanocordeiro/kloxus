package web

// The Firecrawl webhook receiver: monitor.page events feed the source's
// item stream in real mode.
import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// firecrawlWebhook receives monitor.page events (real mode).
type fcEvent struct {
	Type string `json:"type"`
	Data struct {
		MonitorID    string `json:"monitor_id"`
		MonitorIDAlt string `json:"monitorId"`
		URL          string `json:"url"`
		Status       string `json:"status"`
		IsMeaningful bool   `json:"isMeaningful"`
		Markdown     string `json:"markdown"`
		Title        string `json:"title"`
	} `json:"data"`
}

func (s *Server) firecrawlWebhook(w http.ResponseWriter, r *http.Request) {
	if s.d.Cfg.Mode == "real" && s.d.Cfg.WebhookSecret == "" {
		http.Error(w, "webhook secret not configured", http.StatusServiceUnavailable)
		return
	}
	if s.d.Cfg.WebhookSecret != "" {
		got := r.Header.Get("Authorization")
		want := "Bearer " + s.d.Cfg.WebhookSecret
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	var ev fcEvent
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&ev); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if ev.Type != "monitor.page" || (ev.Data.Status != "changed" && ev.Data.Status != "new") {
		w.WriteHeader(204)
		return
	}
	mid := ev.Data.MonitorID
	if mid == "" {
		mid = ev.Data.MonitorIDAlt
	}
	// The webhook carries no tenant cookie: the monitor id resolves the
	// workspace in hosted mode (global lookup) and maps to the single
	// local workspace otherwise.
	var wdb *store.DB
	srcID := int64(0)
	if pool := s.d.WS.Pool(); pool != nil {
		if ms, err := pool.FindSourceByMonitor(r.Context(), mid); err == nil {
			wdb, srcID = pool.For(ms.WorkspaceID), ms.ID
		}
	} else if l := s.d.WS.Local(); l != nil {
		if src, err := l.DB.FindSourceByMonitor(r.Context(), mid); err == nil {
			wdb, srcID = l.DB, src.ID
		}
	}
	if srcID == 0 {
		slog.Warn("webhook: unknown monitor", "id", mid)
		w.WriteHeader(202)
		return
	}
	title := ev.Data.Title
	if title == "" {
		title = ev.Data.URL
	}
	if _, err := wdb.SaveItems(r.Context(), srcID, []sources.RawItem{{
		ExternalID:  ev.Data.URL + "#" + ev.Data.Status,
		URL:         ev.Data.URL,
		Title:       title,
		ContentText: ev.Data.Markdown,
	}}, dedupe.Canonical); err != nil {
		slog.Error("webhook: save items", "err", err, "rid", reqIDOf(r))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("firecrawl monitor.page ingested", "status", ev.Data.Status)
	w.WriteHeader(204)
}
