package web

// The Firecrawl webhook receiver: monitor.page events feed the source's
// item stream in real mode.
import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// --- Resend webhook ---

// resendEvent is the delivery webhook payload: Resend sends Svix-signed
// events for delivered, bounced and complained mail.
type resendEvent struct {
	Type string `json:"type"`
	Data struct {
		EmailID string   `json:"email_id"`
		To      []string `json:"to"`
	} `json:"data"`
}

// svixWindow is the tolerated clock skew on webhook timestamps.
const svixWindow = 5 * time.Minute

// verifySvix checks the Svix signature scheme Resend uses: the secret
// (whsec_…, base64) HMAC-signs "id.timestamp.body", and the
// svix-signature header lists "v1,<b64>" candidates.
func verifySvix(secret string, r *http.Request, body []byte) bool {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return false
	}
	id, ts, sig := r.Header.Get("svix-id"), r.Header.Get("svix-timestamp"), r.Header.Get("svix-signature")
	if id == "" || ts == "" || sig == "" {
		return false
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if skew := time.Since(time.Unix(t, 0)); skew > svixWindow || skew < -svixWindow {
		return false
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte(id + "." + ts + "."))
	h.Write(body)
	want := base64.StdEncoding.EncodeToString(h.Sum(nil))
	for _, cand := range strings.Fields(sig) {
		v, s, ok := strings.Cut(cand, ",")
		if ok && v == "v1" && subtle.ConstantTimeCompare([]byte(s), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

// resendWebhook records delivery events: delivered marks the ledger
// row; a bounce or complaint suppresses the address globally and
// unsubscribes every reader row carrying it in that workspace.
func (s *Server) resendWebhook(w http.ResponseWriter, r *http.Request) {
	secret := s.d.Cfg.ResendWebhookSecret
	if secret == "" {
		// fail closed like the Firecrawl receiver: without the secret a
		// forged bounce could suppress arbitrary addresses.
		http.Error(w, "webhook secret not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if !verifySvix(secret, r, body) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var ev resendEvent
	if err := json.Unmarshal(body, &ev); err != nil || ev.Data.EmailID == "" {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	status := ""
	switch ev.Type {
	case "email.delivered":
		status = "delivered"
	case "email.bounced":
		status = "bounced"
	case "email.complained":
		status = "complained"
	default:
		w.WriteHeader(204)
		return
	}
	ctx := r.Context()
	var target *store.MailTarget
	if pool := s.d.WS.Pool(); pool != nil {
		target, err = pool.FindMailByProviderID(ctx, ev.Data.EmailID)
	} else if l := s.d.WS.Local(); l != nil {
		target, err = l.DB.FindMailByProviderID(ctx, ev.Data.EmailID)
	}
	if err != nil || target == nil {
		// Unknown email id — still 202 so Resend stops retrying; it may
		// be mail this app never logged.
		slog.Warn("resend webhook: unknown email", "id", ev.Data.EmailID, "type", ev.Type)
		w.WriteHeader(202)
		return
	}
	if pool := s.d.WS.Pool(); pool != nil {
		err = pool.SetMailStatusByID(ctx, target.ID, status)
	} else {
		err = s.d.WS.Local().DB.SetMailStatus(ctx, target.ID, status)
	}
	if err != nil {
		slog.Error("resend webhook: status update", "err", err, "rid", reqIDOf(r))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if status == "bounced" || status == "complained" {
		var wdb *store.DB
		if pool := s.d.WS.Pool(); pool != nil {
			wdb = pool.For(target.WorkspaceID)
		} else {
			wdb = s.d.WS.Local().DB
		}
		tos := ev.Data.To
		if len(tos) == 0 {
			tos = []string{target.To}
		}
		for _, addr := range tos {
			addr = strings.ToLower(strings.TrimSpace(addr))
			if addr == "" {
				continue
			}
			if err := s.suppress(ctx, wdb, addr, status); err != nil {
				slog.Warn("resend webhook: suppress", "err", err)
			}
			if err := wdb.UnsubscribeEmail(ctx, addr); err != nil {
				slog.Warn("resend webhook: unsubscribe", "err", err)
			}
		}
		slog.Info("resend webhook: address suppressed", "type", ev.Type)
	}
	w.WriteHeader(204)
}
