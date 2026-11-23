package web

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

// ownerLoginTTL is how long an owner magic link stays valid.
const ownerLoginTTL = 30 * time.Minute

// validEmail accepts a bare, single address with a dotted domain.
func validEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return "", false
	}
	at := strings.LastIndex(s, "@")
	if at < 1 || !strings.Contains(s[at+1:], ".") {
		return "", false
	}
	return strings.ToLower(s), true
}

// apiOwner reports the workspace owner's confirmation state.
func (s *Server) apiOwner(w http.ResponseWriter, r *http.Request) {
	o, err := db(r).OwnerIdentity(r.Context())
	if apiDBErr(w, r, err, "owner") {
		return
	}
	apiJSON(w, 200, map[string]any{
		"email": o.Email, "confirmed": o.Confirmed, "launch_invite": o.LaunchInvite,
	})
}

// apiOwnerMagic sends the owner a magic link. Creating a Radar requires
// the e-mail to be confirmed through it. Demo mode has no real mailer:
// the message lands in the workspace outbox and the link is returned.
func (s *Server) apiOwnerMagic(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	email, ok := validEmail(in.Email)
	if !ok {
		apiErr(w, 400, "validation", "enter a valid e-mail address")
		return
	}
	if s.d.Cfg.Mode != "demo" {
		apiErr(w, 501, "email_not_configured", "e-mail delivery is not configured")
		return
	}
	ctx := r.Context()
	nonce, err := db(r).StartOwnerLogin(ctx, email, ownerLoginTTL)
	if apiDBErr(w, r, err, "login") {
		return
	}
	q := url.Values{"n": {nonce}}
	if tok := s.readerToken(r); tok != "" {
		q.Set("tok", tok)
	}
	link := requestBase(r) + "/api/v1/auth/verify?" + q.Encode()
	body := fmt.Sprintf(
		`<!DOCTYPE html><html><body style="font-family:system-ui,sans-serif;max-width:38em;margin:2em auto">`+
			`<h2>Kloxus — confirme seu e-mail</h2>`+
			`<p>Clique para confirmar seu e-mail e criar seu Radar (o link vale 30 minutos):</p>`+
			`<p><a href="%s">Confirmar e-mail (magic link)</a></p>`+
			`<p style="color:#777;font-size:.85em">Ao confirmar, você entra na lista para receber o convite do lançamento definitivo. `+
			`Simulado no modo demo — nenhum e-mail real foi enviado.</p></body></html>`,
		html.EscapeString(link))
	emailID, err := db(r).SaveOutboxEmail(ctx, "magic_link", email, "Kloxus — seu link de acesso", "", body)
	if apiDBErr(w, r, err, "email") {
		return
	}
	apiJSON(w, 200, map[string]any{
		"sent": true, "demo": true, "outbox_email_id": emailID, "magic_link": link,
	})
}

// apiOwnerVerify spends a magic link: confirms the owner, registers the
// address for the launch invitation, binds the browser to the workspace
// the link was issued for, and lands on the create screen.
func (s *Server) apiOwnerVerify(w http.ResponseWriter, r *http.Request) {
	r = s.withWS(r)
	if tok := r.URL.Query().Get("tok"); tok != "" && s.d.WS.Hosted() {
		if id := s.d.WS.ResolvePublicToken(tok); id != "" && id != reqWS(r).ID {
			http.Redirect(w, r, "/new?login=invalid", http.StatusSeeOther)
			return
		}
	}
	email, err := db(r).ConfirmOwnerLogin(r.Context(), r.URL.Query().Get("n"))
	if err != nil {
		http.Redirect(w, r, "/new?login=invalid", http.StatusSeeOther)
		return
	}
	if err := s.d.WS.RecordLaunchInvite(email); err != nil {
		slog.Error("record launch invite", "err", err, "rid", reqIDOf(r))
	}
	if s.d.WS.Hosted() {
		s.d.WS.SetCookie(w, r, reqWS(r).ID)
	}
	http.Redirect(w, r, "/new?login=ok", http.StatusSeeOther)
}
