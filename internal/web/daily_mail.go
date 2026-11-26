package web

// Daily pauta mail opt-out (KLOX-257). Every 07:00 "suas notícias de
// hoje estão prontas" mail carries a "Não quero mais este e-mail" link
// and RFC 8058 List-Unsubscribe/List-Unsubscribe-Post headers pointing
// at /daily-mail/unsub. This is not the reader /unsub: the account's
// own notice must keep answering with KLOXUS_PUBLISHING=off, where the
// whole reader surface 404s — so the route lives outside the
// publishing gate, carries its own stored-hash token and never asks
// for a session.
import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// dailyMailUnsub is the one-click target the daily pauta mail carries:
// the GET link itself stamps the opt-out (one click, no login — the
// acceptance asks for exactly that) and answers with the confirmation
// page; POST answers the mail clients' RFC 8058 List-Unsubscribe-Post
// call. The mailed token resolves the account by its stored hash — a
// wrong or stale token gets the friendly failure page, never a clue
// about which tokens exist.
func (s *Server) dailyMailUnsub(w http.ResponseWriter, r *http.Request) {
	lang, ok, err := s.unsubDailyMail(r.Context(), r.URL.Query().Get("t"))
	if err != nil {
		slog.Error("daily mail unsubscribe failed", "err", err, "rid", reqIDOf(r))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if lang == "" {
		lang = r.Header.Get("Accept-Language")
	}
	if !ok {
		dailyMailUnsubFail(w, lang)
		return
	}
	if r.Method == http.MethodPost {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// the URL carries a credential — never let a cache replay it
	w.Header().Set("Cache-Control", "no-store")
	title, body := "Daily email turned off",
		"You won't get the morning stories email anymore. You can turn it back on in the app, under the account menu."
	if ptLang(lang) {
		title, body = "E-mail diário desligado",
			"Você não vai mais receber o e-mail com as notícias do dia. Para reativar, abra o menu da conta no app."
	}
	fmt.Fprintf(w, claimShell(lang, `<h1 style="font-size:1.5rem;line-height:1.25;margin:0 0 .75rem">%s</h1>`+
		`<p style="margin:0;color:#5b6660">%s</p>`), html.EscapeString(title), html.EscapeString(body))
}

// dailyMailUnsubFail renders the single failure state for an unknown or
// stale unsubscribe token — the same shell as the claim pages.
func dailyMailUnsubFail(w http.ResponseWriter, lang string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	title, body := "Invalid unsubscribe link",
		"This link doesn't match any account. Open the account menu in Kloxus to turn the daily email off."
	if ptLang(lang) {
		title, body = "Link de descadastro inválido",
			"Este link não corresponde a nenhuma conta. Abra o menu da conta no Kloxus para desligar o e-mail diário."
	}
	fmt.Fprintf(w, claimShell(lang, `<h1 style="font-size:1.5rem;margin:0 0 .75rem">%s</h1>`+
		`<p style="margin:0;color:#5b6660">%s</p>`), html.EscapeString(title), html.EscapeString(body))
}

// unsubDailyMail spends the mailed token against the shared workspace
// table — pooled in hosted mode, the local workspace store otherwise.
func (s *Server) unsubDailyMail(ctx context.Context, tok string) (lang string, ok bool, err error) {
	if tok == "" || s.d.WS == nil {
		return "", false, nil
	}
	hash := hashToken(tok)
	if pool := s.d.WS.Pool(); pool != nil {
		return pool.UnsubscribeDailyMail(ctx, hash)
	}
	if l := s.d.WS.Local(); l != nil {
		return l.DB.UnsubscribeDailyMail(ctx, hash)
	}
	return "", false, nil
}

// dailyMailOptedOut reports whether this owner already unsubscribed —
// owner-keyed because the daily mail claim is (KLOX-154).
func (s *Server) dailyMailOptedOut(ctx context.Context, pool *store.Pool, wdb *store.DB, owner string) (bool, error) {
	if pool != nil {
		return pool.DailyMailOptedOut(ctx, owner)
	}
	return wdb.DailyMailOptedOut(ctx, owner)
}

// apiWorkspaceDailyMail is the account-menu toggle "Receber as
// notícias do dia por e-mail": claimed accounts only, and it writes
// the same opt-out column the mailed unsubscribe link stamps.
func (s *Server) apiWorkspaceDailyMail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	owner := s.workspaceOwner(r.Context(), db(r))
	if owner == "" {
		apiErr(w, http.StatusForbidden, "email_required",
			"confirm your email with the magic link before changing mail settings")
		return
	}
	if err := db(r).SetDailyMail(r.Context(), owner, in.Enabled); err != nil {
		apiErr(w, 500, "internal", "could not save the setting")
		return
	}
	apiJSON(w, 200, map[string]any{"daily_mail": in.Enabled})
}
