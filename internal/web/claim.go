package web

// Workspace claim by magic link (D33): the creator enters an email, a
// single-use 15-minute link lands in that inbox, and opening it —
// in any browser — signs that browser into the now-owned workspace.
// This file also holds the reader double opt-in and the RFC 8058
// one-click unsubscribe target.
import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const (
	magicLinkTTL    = 15 * time.Minute
	claimPerHour    = 5 // magic links a workspace may mint per hour
	loginPerHour    = 5 // account-recovery links an email may mint per hour
	subscribePerDay = 1 // confirm emails per reader per day (idem key)
)

// ptLang reports whether a language tag is Portuguese — claim pages
// and lifecycle emails localize on it (KLOX-64).
func ptLang(lang string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "pt")
}

// plural picks the singular or plural English noun for API messages —
// never "item(s)" on screen (KLOX-64).
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// publicWDB resolves the workspace a sealed public link points at — the
// ?tok (or legacy ?ws) parameter — without touching the visitor's own
// session. Local mode returns the fixed workspace; anything else is nil
// and the caller fails the request.
func (s *Server) publicWDB(r *http.Request) *store.DB {
	if l := s.d.WS.Local(); l != nil {
		return l.DB
	}
	pool := s.d.WS.Pool()
	if pool == nil {
		return nil
	}
	id := ""
	if tok := r.URL.Query().Get("tok"); tok != "" {
		id = s.d.WS.ResolvePublicToken(tok)
	}
	if id == "" {
		id = r.URL.Query().Get("ws")
	}
	if id == "" {
		return nil
	}
	if ok, err := pool.WorkspaceExists(r.Context(), id); err != nil || !ok {
		return nil
	}
	return pool.For(id)
}

// tokSuffix re-appends the workspace token a public link arrived with,
// so follow-up links resolve the same workspace.
func tokSuffix(r *http.Request) string {
	if tok := r.URL.Query().Get("tok"); tok != "" {
		return "&tok=" + url.QueryEscape(tok)
	}
	if ws := r.URL.Query().Get("ws"); ws != "" {
		return "&ws=" + url.QueryEscape(ws)
	}
	return ""
}

// --- claim ---

// hashToken digests a magic-link token for storage: the live token
// travels only in the email, the DB keeps its SHA-256 hex.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// apiClaimStart mints a magic link and mails it to the creator.
func (s *Server) apiClaimStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	wdb := db(r)
	var in struct {
		Email string `json:"email"`
		Lang  string `json:"lang"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(email) {
		apiErr(w, 400, "validation", "a plain email address is required")
		return
	}
	if n, err := wdb.RecentMagicLinks(ctx, store.TSUTC(time.Now().Add(-time.Hour))); err == nil && n >= claimPerHour {
		apiErr(w, 429, "rate_limited", "too many claim links — try again later")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		apiErr(w, 500, "internal", "could not mint a claim link")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := hashToken(token)
	if err := wdb.SaveMagicLink(ctx, hash, email,
		store.TSUTC(time.Now().Add(magicLinkTTL))); err != nil {
		apiErr(w, 500, "internal", "could not save the claim link")
		return
	}
	// the link always carries the interface language it was asked from
	// (KLOX-232): /claim renders in it and the confirmed session lands
	// on /start?lang= — without it an EN sender on a PT browser got the
	// app back in Portuguese.
	lang := "en"
	if ptLang(in.Lang) {
		lang = "pt"
	}
	link := s.publicBase(r) + "/claim?t=" + url.QueryEscape(token) + "&lang=" + lang
	stamp := claimStamp(time.Now(), lang == "pt")
	recovering := false
	if pool := s.d.WS.Pool(); pool != nil {
		if owned, err := pool.WorkspacesByOwner(ctx, email); err == nil && len(owned) > 0 {
			recovering = true
		}
	}
	subject, md := "Your Kloxus link — "+stamp,
		fmt.Sprintf("Confirm this email to create your Radar.\n\n[Create my Radar](%s)\n\nThe link works once and expires in 15 minutes.\n\nIf you didn't ask for this, ignore this email.", link)
	if recovering {
		subject, md = "Your Kloxus sign-in link — "+stamp,
			fmt.Sprintf("Use this link to sign back into your Kloxus account.\n\n[Sign in](%s)\n\nThe link works once and expires in 15 minutes.\n\nIf you didn't ask for this, ignore this email.", link)
	}
	if lang == "pt" {
		subject, md = "Seu link do Kloxus — "+stamp,
			fmt.Sprintf("Confirme este e-mail para criar seu Radar.\n\n[Criar meu Radar](%s)\n\nO link funciona uma vez e expira em 15 minutos.\n\nSe você não pediu, ignore este e-mail.", link)
		if recovering {
			subject, md = "Seu link de acesso ao Kloxus — "+stamp,
				fmt.Sprintf("Use este link para entrar de novo na sua conta do Kloxus.\n\n[Entrar](%s)\n\nO link funciona uma vez e expira em 15 minutos.\n\nSe você não pediu, ignore este e-mail.", link)
		}
	}
	res, err := s.sendMail(ctx, wdb, &delivery.Message{
		To:      []string{email},
		Subject: subject,
		Text:    md,
		HTML:    delivery.RenderHTML(subject, md, s.publicBase(r)),
		Kind:    "magic_link",
		IdemKey: "claim/" + hash,
	})
	if err != nil {
		apiErr(w, 500, "internal", "could not send the claim email")
		return
	}
	resp := map[string]any{"sent": res.Sent() || res.Fallback, "email": email}
	if res.Mail != nil {
		resp["outbox_email_id"] = res.Mail.ID
	}
	if res.Refused != "" {
		resp["refused"] = res.Refused
	}
	if res.Fallback {
		resp["delivery"] = "outbox_fallback"
	} else if res.Sent() {
		resp["delivery"] = "resend"
	} else {
		resp["delivery"] = "outbox"
		// Outbox mode sends nothing real — surface the link so the local
		// demo and tests can complete the claim without an inbox.
		resp["claim_url"] = link
	}
	apiJSON(w, 200, resp)
}

// --- account recovery login (KLOX-272) ---

// apiLoginStart mints an account-recovery magic link without resolving
// or creating a visitor workspace. The answer is deliberately uniform
// when the email has no account: account existence is never exposed
// through this public endpoint.
func (s *Server) apiLoginStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool := s.d.WS.Pool()
	if pool == nil {
		pool = s.d.Pool
	}
	if pool == nil {
		apiErr(w, 500, "internal", "account login is unavailable")
		return
	}
	var in struct {
		Email string `json:"email"`
		Lang  string `json:"lang"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(email) {
		apiErr(w, 400, "validation", "a plain email address is required")
		return
	}
	if n, err := pool.RecentLoginLinks(ctx, email, store.TSUTC(time.Now().Add(-time.Hour))); err == nil && n >= loginPerHour {
		apiErr(w, 429, "rate_limited", "too many sign-in links — try again later")
		return
	}
	owned, err := pool.WorkspacesByOwner(ctx, email)
	if err != nil {
		apiErr(w, 500, "internal", "could not look up the account")
		return
	}
	if len(owned) == 0 {
		// Uniform response for an unknown address, with no token minted: a
		// pre-account probe must never become a valid sign-in link if the
		// address creates an account before the TTL would have expired.
		apiJSON(w, 200, map[string]any{"sent": true, "email": email})
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		apiErr(w, 500, "internal", "could not mint a sign-in link")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := hashToken(token)
	if err := pool.SaveLoginLink(ctx, hash, email, store.TSUTC(time.Now().Add(magicLinkTTL))); err != nil {
		apiErr(w, 500, "internal", "could not save the sign-in link")
		return
	}

	lang := "en"
	if ptLang(in.Lang) {
		lang = "pt"
	}
	link := s.publicBase(r) + "/login?t=" + url.QueryEscape(token) + "&lang=" + lang
	stamp := claimStamp(time.Now(), lang == "pt")
	subject, md := "Your Kloxus sign-in link — "+stamp,
		fmt.Sprintf("Use this link to sign back into your Kloxus account.\n\n[Sign in](%s)\n\nThe link works once and expires in 15 minutes.\n\nIf you didn't ask for this, ignore this email.", link)
	if lang == "pt" {
		subject, md = "Seu link de acesso ao Kloxus — "+stamp,
			fmt.Sprintf("Use este link para entrar de novo na sua conta do Kloxus.\n\n[Entrar](%s)\n\nO link funciona uma vez e expira em 15 minutos.\n\nSe você não pediu, ignore este e-mail.", link)
	}
	// The send ledger is workspace-scoped; account mail is recorded on the
	// account's preferred workspace, but account-level magic_link rows are
	// already excluded from publication outboxes.
	wdb := pool.For(owned[0].ID)
	if _, err := s.sendMail(ctx, wdb, &delivery.Message{
		To:      []string{email},
		Subject: subject,
		Text:    md,
		HTML:    delivery.RenderHTML(subject, md, s.publicBase(r)),
		Kind:    "magic_link",
		IdemKey: "login/" + hash,
	}); err != nil {
		apiErr(w, 500, "internal", "could not send the sign-in email")
		return
	}
	// Keep the live response identical to the unknown-address answer:
	// delivery metadata would leak whether the email owns a workspace.
	// Outbox-only mode is the local/hosted verification transport, so it
	// exposes the test hook needed to open the mailed link without an inbox.
	resp := map[string]any{"sent": true, "email": email}
	if !s.liveMail() {
		resp["delivery"] = "outbox"
		resp["login_url"] = link
	}
	apiJSON(w, 200, resp)
}

// claimNextTarget accepts only internal app paths a mailed sign-in link
// is allowed to deep-link into. Anything absolute, protocol-relative or
// outside the small app whitelist falls back to /start — a claim URL is
// a credential and must never become an open redirect.
func claimNextTarget(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/start"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" {
		return "/start"
	}
	ok := u.Path == "/start" || u.Path == "/compor" || strings.HasPrefix(u.Path, "/publications/")
	if !ok {
		return "/start"
	}
	return u.RequestURI()
}

func claimNextQuery(r *http.Request) string {
	next := r.URL.Query().Get("next")
	if next == "" {
		return ""
	}
	return "&amp;next=" + url.QueryEscape(next)
}

// claimPage lands the emailed magic link without spending it. Mail
// scanners prefetch every link in a message, so a GET that consumed the
// single-use token left the user's real click "already expired" — the
// claim only spends on this page's explicit POST (KLOX-27 follow-on).
// An invalid, spent or expired token renders the one failure state.
func (s *Server) claimPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	lang := r.URL.Query().Get("lang")
	hash := hashToken(token)
	now := store.NowUTC()
	var email string
	recovering := false
	if pool := s.d.WS.Pool(); pool != nil {
		_, em, err := pool.PeekMagicLink(r.Context(), hash, now)
		if err != nil {
			claimFail(w, lang)
			return
		}
		email = em
		if owned, err := pool.WorkspacesByOwner(r.Context(), email); err == nil && len(owned) > 0 {
			recovering = true
		}
	} else if l := s.d.WS.Local(); l != nil {
		em, err := l.DB.PeekMagicLink(r.Context(), hash, now)
		if err != nil {
			claimFail(w, lang)
			return
		}
		email = em
	} else {
		claimFail(w, lang)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// the URL carries a credential — don't let a cache keep or replay it
	w.Header().Set("Cache-Control", "no-store")
	title, blurb, button, note := "Confirm your email to create your Radar",
		"Confirming signs this browser in as the owner (%s).",
		"Confirm and continue",
		"The link works once and expires after the period stated in the email."
	if recovering {
		title, blurb = "Sign back in to Kloxus",
			"Confirming signs this browser back into the account owned by %s."
	}
	if ptLang(lang) {
		title, blurb, button, note = "Confirme seu e-mail para criar seu Radar",
			"Ao confirmar, este navegador fica associado ao e-mail %s.",
			"Confirmar e continuar",
			"O link funciona uma vez e expira depois do prazo informado no e-mail."
		if recovering {
			title, blurb = "Entrar de novo no Kloxus",
				"Ao confirmar, este navegador entra de novo na conta de %s."
		}
	}
	fmt.Fprintf(w, claimShell(lang, `<h1 style="font-size:1.5rem;line-height:1.25;margin:0 0 .75rem">%s</h1>`+
		`<p style="margin:0 0 1.5rem;color:#111814">`+blurb+`</p>`+
		`<form method="POST" action="/claim?t=%s&amp;lang=%s%s"><button type="submit" `+
		`style="font:inherit;font-weight:600;padding:.7em 1.5em;border:0;border-radius:.6rem;`+
		`background:#0e3b2e;color:#fff;cursor:pointer">%s</button></form>`+
		`<p style="margin:1.5rem 0 0;color:#5b6660;font-size:.85rem">%s</p>`),
		html.EscapeString(title), html.EscapeString(email),
		url.QueryEscape(token), url.QueryEscape(lang), claimNextQuery(r),
		html.EscapeString(button), html.EscapeString(note))
}

// claimShell wraps the claim pages in the app's look (surface
// background, white card, brand teal) — the page is server-rendered, so
// it carries its own inline styles instead of the SPA stylesheet.
// body is a Printf template; callers escape their own arguments.
func claimShell(lang, body string) string {
	htmlLang := "en"
	if ptLang(lang) {
		htmlLang = "pt-BR"
	}
	return `<!DOCTYPE html><html lang="` + htmlLang + `"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"><title>Kloxus</title></head>` +
		`<body style="margin:0;background:#f4f6f3;color:#111814;font-family:system-ui,-apple-system,'Segoe UI',sans-serif">` +
		`<main style="max-width:30rem;margin:0 auto;padding:3rem 1rem">` +
		`<p style="margin:0 0 1.25rem;font-weight:700;font-size:1.25rem;color:#0e3b2e">Kloxus</p>` +
		`<div style="background:#fff;border:1px solid #dde4dc;border-radius:1rem;padding:2rem">` +
		body + `</div></main></body></html>`
}

// chooseAccountWorkspace picks the workspace a recovered account should
// open. Empty duplicate sessions never win over a workspace carrying a
// Radar; multiple workspaces with Radars are ambiguous and require the
// explicit choice rendered by renderAccountChoice.
func chooseAccountWorkspace(cands []store.OwnedWorkspace, requested string) (id string, ambiguous bool) {
	var withPubs []store.OwnedWorkspace
	for _, c := range cands {
		if c.Publications > 0 {
			withPubs = append(withPubs, c)
		}
	}
	if len(withPubs) == 1 {
		return withPubs[0].ID, false
	}
	if len(withPubs) > 1 {
		for _, c := range withPubs {
			if c.ID == requested {
				return requested, false
			}
		}
		return "", true
	}
	for _, c := range cands {
		if c.ID == requested {
			return requested, false
		}
	}
	if len(cands) > 0 {
		return cands[0].ID, false
	}
	return "", false
}

// renderAccountChoice is the explicit disambiguation step for an email
// that owns more than one workspace carrying a Radar. The magic-link
// token is not consumed until one of these forms posts back, so every
// button carries the same still-valid credential plus its workspace
// choice.
func (s *Server) renderAccountChoice(w http.ResponseWriter, r *http.Request, endpoint, token, lang, email, next string, cands []store.OwnedWorkspace) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	title, blurb, choose := "Choose your Radar",
		"This email is connected to more than one Kloxus Radar. Choose the one you want to open.",
		"Open"
	if ptLang(lang) {
		title, blurb, choose = "Escolha seu Radar",
			"Este e-mail está conectado a mais de um Radar no Kloxus. Escolha qual você quer abrir.",
			"Abrir"
	}
	var body strings.Builder
	fmt.Fprintf(&body, `<h1 style="font-size:1.5rem;line-height:1.25;margin:0 0 .75rem">%s</h1>`, html.EscapeString(title))
	fmt.Fprintf(&body, `<p style="margin:0 0 1rem;color:#111814">%s</p>`, html.EscapeString(blurb))
	fmt.Fprintf(&body, `<p style="margin:0 0 1.5rem;color:#5b6660;font-size:.9rem">%s</p>`, html.EscapeString(email))
	for _, c := range cands {
		if c.Publications == 0 {
			continue
		}
		names := []string{}
		if pool := s.d.WS.Pool(); pool != nil {
			if pubs, err := pool.For(c.ID).ListPublications(r.Context()); err == nil {
				for _, p := range pubs {
					names = append(names, p.Name)
				}
			}
		}
		label := strings.Join(names, ", ")
		if label == "" {
			label = "Radar " + c.ID[:6]
		}
		q := url.Values{}
		q.Set("t", token)
		q.Set("lang", lang)
		q.Set("ws", c.ID)
		if next != "" {
			q.Set("next", next)
		}
		action := endpoint + "?" + q.Encode()
		fmt.Fprintf(&body, `<form method="POST" action="%s" style="margin:.6rem 0"><button type="submit" `+
			`style="display:block;width:100%%;text-align:left;font:inherit;font-weight:600;padding:.8em 1em;border:1px solid #dde4dc;border-radius:.6rem;`+
			`background:#fff;color:#0e3b2e;cursor:pointer">%s · %s</button></form>`,
			html.EscapeString(action), html.EscapeString(choose), html.EscapeString(label))
	}
	fmt.Fprint(w, claimShell(lang, body.String()))
}

// loginPage lands an account-recovery link without spending it, mirroring
// the claim link's scanner-safe two-step confirmation.
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	lang := r.URL.Query().Get("lang")
	hash := hashToken(token)
	now := store.NowUTC()
	pool := s.d.WS.Pool()
	if pool == nil {
		pool = s.d.Pool
	}
	if pool == nil {
		claimFail(w, lang)
		return
	}
	email, err := pool.PeekLoginLink(r.Context(), hash, now)
	if err != nil {
		claimFail(w, lang)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	title, blurb, button, note := "Sign back in to Kloxus",
		"Confirming signs this browser in as the account owner (%s).",
		"Sign in and continue",
		"The link works once and expires after the period stated in the email."
	if ptLang(lang) {
		title, blurb, button, note = "Entrar de novo no Kloxus",
			"Ao confirmar, este navegador entra na conta de %s.",
			"Entrar e continuar",
			"O link funciona uma vez e expira depois do prazo informado no e-mail."
	}
	fmt.Fprintf(w, claimShell(lang, `<h1 style="font-size:1.5rem;line-height:1.25;margin:0 0 .75rem">%s</h1>`+
		`<p style="margin:0 0 1.5rem;color:#111814">`+blurb+`</p>`+
		`<form method="POST" action="/login?t=%s&amp;lang=%s"><button type="submit" `+
		`style="font:inherit;font-weight:600;padding:.7em 1.5em;border:0;border-radius:.6rem;`+
		`background:#0e3b2e;color:#fff;cursor:pointer">%s</button></form>`+
		`<p style="margin:1.5rem 0 0;color:#5b6660;font-size:.85rem">%s</p>`),
		html.EscapeString(title), html.EscapeString(email),
		url.QueryEscape(token), url.QueryEscape(lang),
		html.EscapeString(button), html.EscapeString(note))
}

// loginConfirm spends an account-recovery token and signs the browser
// into the workspace that actually carries the account's Radar. It never
// mints or claims an empty replacement workspace.
func (s *Server) loginConfirm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	lang := r.URL.Query().Get("lang")
	hash := hashToken(token)
	now := store.NowUTC()
	pool := s.d.WS.Pool()
	if pool == nil {
		pool = s.d.Pool
	}
	if pool == nil {
		claimFail(w, lang)
		return
	}
	email, err := pool.PeekLoginLink(r.Context(), hash, now)
	if err != nil {
		claimFail(w, lang)
		return
	}
	owned, err := pool.WorkspacesByOwner(r.Context(), email)
	if err != nil || len(owned) == 0 {
		claimFail(w, lang)
		return
	}
	wsID, ambiguous := chooseAccountWorkspace(owned, r.FormValue("ws"))
	if ambiguous {
		s.renderAccountChoice(w, r, "/login", token, lang, email, "", owned)
		return
	}
	if _, err := pool.ConsumeLoginLink(r.Context(), hash, now); err != nil {
		claimFail(w, lang)
		return
	}
	if s.d.WS.Hosted() {
		if err := s.d.WS.SetCookie(w, r, wsID); err != nil {
			slog.Error("workspace cookie mint failed", "err", err, "rid", reqIDFromCtx(r.Context()))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	target := "/publications"
	if lang == "en" || lang == "pt" {
		if db := pool.For(wsID); db != nil {
			if err := db.SetUILang(r.Context(), lang); err != nil {
				slog.Warn("ui_lang save failed", "err", err, "rid", reqIDFromCtx(r.Context()))
			}
		}
		target += "?lang=" + lang
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// claimConfirm spends a magic link: it must be unexpired and unused,
// then the workspace becomes owned and the cookie signs this browser in.
// When the email already owns workspaces, confirmation instead recovers
// the real account workspace — never the empty session that minted the
// workaround link (KLOX-272).
func (s *Server) claimConfirm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	lang := r.URL.Query().Get("lang")
	hash := hashToken(token)
	now := store.NowUTC()
	var wsID, email string
	var claimed *store.DB
	recoveredWithRadar := false
	if pool := s.d.WS.Pool(); pool != nil {
		minted, em, err := pool.PeekMagicLink(r.Context(), hash, now)
		if err != nil {
			claimFail(w, lang)
			return
		}
		wsID, email = minted, em
		owned, err := pool.WorkspacesByOwner(r.Context(), email)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		if len(owned) > 0 {
			choice, ambiguous := chooseAccountWorkspace(owned, r.FormValue("ws"))
			if ambiguous {
				s.renderAccountChoice(w, r, "/claim", token, lang, email, r.URL.Query().Get("next"), owned)
				return
			}
			wsID = choice
			for _, c := range owned {
				if c.ID == wsID && c.Publications > 0 {
					recoveredWithRadar = true
					break
				}
			}
		}
		if _, _, err := pool.ConsumeMagicLink(r.Context(), hash, now); err != nil {
			claimFail(w, lang)
			return
		}
		// A first claim stamps ownership; account recovery only signs into an
		// already-owned workspace and must not rewrite its original claimed_at.
		if len(owned) == 0 {
			if err := pool.ClaimWorkspace(r.Context(), wsID, email); err != nil {
				http.Error(w, "internal error", 500)
				return
			}
		}
		// A confirmed owner is someone who really entered the product:
		// register them for the launch invitation (KLOX-161). Idempotent;
		// a failure here must not undo the claim.
		if err := pool.AddWaitlist(r.Context(), email, "claim"); err != nil {
			slog.Warn("launch invite registration failed", "err", err, "rid", reqIDFromCtx(r.Context()))
		}
		claimed = pool.For(wsID)
	} else if l := s.d.WS.Local(); l != nil {
		em, err := l.DB.ConsumeMagicLink(r.Context(), hash, now)
		if err != nil {
			claimFail(w, lang)
			return
		}
		email = em
		if err := l.DB.ClaimWorkspace(r.Context(), email); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		claimed = l.DB
	} else {
		claimFail(w, lang)
		return
	}
	if s.d.WS.Hosted() {
		// Opening the link in any browser signs that browser into the
		// claimed workspace.
		if err := s.d.WS.SetCookie(w, r, wsID); err != nil {
			slog.Error("workspace cookie mint failed", "err", err, "rid", reqIDFromCtx(r.Context()))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	target := claimNextTarget(r.URL.Query().Get("next"))
	// A recovered account with a Radar lands on its dashboard; a genuinely
	// new claim still lands on /start to create the first Radar.
	if r.URL.Query().Get("next") == "" && recoveredWithRadar {
		target = "/publications"
	}
	// the language the link carried follows the confirmed session into
	// the app — the account keeps it and the SPA picks it off the URL
	// (KLOX-232). /claim never resolves a request workspace, so the
	// save goes through the workspace the token claimed, not db(r).
	if lang == "en" || lang == "pt" {
		if err := claimed.SetUILang(r.Context(), lang); err != nil {
			slog.Warn("ui_lang save failed", "err", err, "rid", reqIDFromCtx(r.Context()))
		}
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + "lang=" + lang
	}
	// Without next this is /start ("Criar seu Radar") for a first claim,
	// or /publications for a recovered account; the daily mail deep-links
	// next to the briefing or composer (KLOX-267).
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// claimFail renders the single failure state for a bad, spent or
// expired link, in the language the link carried.
func claimFail(w http.ResponseWriter, lang string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	title, body := "Link expired or already used",
		"Links work once and expire after the period stated in the email. Go back to Kloxus and request a new one."
	if ptLang(lang) {
		title, body = "Link expirado ou já usado",
			"O link funciona uma vez e expira depois do prazo informado no e-mail. Volte ao Kloxus e peça um novo."
	}
	fmt.Fprintf(w, claimShell(lang, `<h1 style="font-size:1.5rem;margin:0 0 .75rem">%s</h1>`+
		`<p style="margin:0;color:#5b6660">%s</p>`), html.EscapeString(title), html.EscapeString(body))
}

// --- reader double opt-in ---

// apiSubscribe starts a reader's double opt-in: a confirmation email
// with a signed link; nothing is mailed to them until they confirm.
// The route is public — the sealed workspace token resolves the
// publication's workspace, never the visitor's.
func (s *Server) apiSubscribe(w http.ResponseWriter, r *http.Request) {
	wdb := s.publicWDB(r)
	if wdb == nil {
		apiErr(w, 400, "validation", "this publication link is missing its workspace token")
		return
	}
	ctx := r.Context()
	pub, err := wdb.GetPublication(ctx, r.PathValue("slug"))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(email) {
		apiErr(w, 400, "validation", "a plain email address is required")
		return
	}
	if existing, err := wdb.ReaderByEmail(ctx, pub.ID, email); err == nil && existing.Confirmed {
		apiJSON(w, 200, map[string]any{"sent": false, "already_confirmed": true})
		return
	}
	rid, err := wdb.UpsertReader(ctx, pub.ID, email, "")
	if apiDBErr(w, r, err, "reader") {
		return
	}
	link := fmt.Sprintf("%s/confirm-sub?pub=%s&r=%d&t=%s%s",
		s.publicBase(r), url.QueryEscape(pub.Slug), rid,
		s.d.WS.MAC("sub", fmt.Sprintf("%d:%d", pub.ID, rid)), tokSuffix(r))
	subject, md := pub.Name+" — confirm your subscription",
		fmt.Sprintf("Confirm you'd like to receive %s by email.\n\n[Confirm subscription](%s)\n\nIf you did not ask for this, ignore the email — nothing else will be sent.", pub.Name, link)
	if ptLang(pub.Language) {
		subject, md = pub.Name+" — confirme sua inscrição",
			fmt.Sprintf("Confirme que você quer receber %s por e-mail.\n\n[Confirmar inscrição](%s)\n\nSe você não pediu, ignore este e-mail — nada mais será enviado.", pub.Name, link)
	}
	res, err := s.sendMail(ctx, wdb, &delivery.Message{
		To:      []string{email},
		Subject: subject,
		Text:    md,
		HTML:    delivery.RenderHTML(subject, md, s.publicBase(r)),
		Kind:    "reader_confirm",
		IdemKey: fmt.Sprintf("confirm/%d/%s", rid, today()),
	})
	if err != nil {
		apiErr(w, 500, "internal", "could not send the confirmation email")
		return
	}
	resp := map[string]any{"sent": res.Sent() || res.Fallback}
	if res.Refused != "" {
		resp["refused"] = res.Refused
	}
	if res.Fallback {
		resp["delivery"] = "outbox_fallback"
	} else if res.Sent() {
		resp["delivery"] = "resend"
	} else {
		resp["delivery"] = "outbox"
		resp["confirm_url"] = link
	}
	apiJSON(w, 200, resp)
}

// confirmSub lands the double opt-in link: the signed MAC proves the
// inbox was reached; the reader is stamped confirmed and sent back to
// the publication page.
func (s *Server) confirmSub(w http.ResponseWriter, r *http.Request) {
	wdb := s.publicWDB(r)
	if wdb == nil {
		claimFail(w, "")
		return
	}
	pub, err := wdb.GetPublication(r.Context(), r.URL.Query().Get("pub"))
	if err != nil {
		claimFail(w, "")
		return
	}
	rid, _ := strconv.ParseInt(r.URL.Query().Get("r"), 10, 64)
	tok := r.URL.Query().Get("t")
	if rid == 0 || !s.d.WS.ValidMAC("sub", fmt.Sprintf("%d:%d", pub.ID, rid), tok) {
		claimFail(w, pub.Language)
		return
	}
	if err := wdb.MarkReaderConfirmed(r.Context(), rid); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	// With no public issue yet /latest would 404 — land on the
	// newsletter page instead, which is built for the empty archive.
	target := fmt.Sprintf("/p/%s/latest", pub.Slug)
	if _, err := wdb.LatestPublicIssue(r.Context(), pub.ID); errors.Is(err, sql.ErrNoRows) {
		target = fmt.Sprintf("/p/%s", pub.Slug)
	}
	if q := tokSuffix(r); q != "" {
		target += "?" + strings.TrimPrefix(q, "&")
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// unsubOneClick is the RFC 8058 POST target carried by
// List-Unsubscribe-Post: mail clients unsubscribe without a page load.
// The signed parameters (pub, r, t) plus the sealed workspace token are
// the whole credential — no cookie.
func (s *Server) unsubOneClick(w http.ResponseWriter, r *http.Request) {
	wdb := s.publicWDB(r)
	if wdb == nil {
		http.Error(w, "invalid unsubscribe link", 403)
		return
	}
	pub, err := wdb.GetPublication(r.Context(), r.URL.Query().Get("pub"))
	if err != nil {
		http.Error(w, "invalid unsubscribe link", 403)
		return
	}
	rid, _ := strconv.ParseInt(r.URL.Query().Get("r"), 10, 64)
	tok := r.URL.Query().Get("t")
	if rid == 0 || !s.d.WS.ValidMAC("unsub", fmt.Sprintf("%d:%d", pub.ID, rid), tok) {
		http.Error(w, "invalid unsubscribe link", 403)
		return
	}
	if err := wdb.UnsubscribeReader(r.Context(), rid); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// requireClaimed gates Radar creation on a confirmed owner email
// (KLOX-161): the address is the account's identity and the launch
// invitation's recipient. It writes the refusal and reports false when
// the workspace has not been claimed through a magic link.
func (s *Server) requireClaimed(w http.ResponseWriter, r *http.Request) bool {
	if s.workspaceOwner(r.Context(), db(r)) != "" {
		return true
	}
	apiErr(w, http.StatusForbidden, "email_required",
		"confirm your email with the magic link before creating a Radar")
	return false
}

// apiWorkspaceLang saves the interface language on the account
// (KLOX-232): the toggle writes it so a fresh browser — or the next
// magic link — lands in the same language. Only "en"/"pt" are saved.
func (s *Server) apiWorkspaceLang(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Lang string `json:"lang"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if in.Lang != "en" && in.Lang != "pt" {
		apiErr(w, 400, "validation", "lang must be en or pt")
		return
	}
	if err := db(r).SetUILang(r.Context(), in.Lang); err != nil {
		apiErr(w, 500, "internal", "could not save the language")
		return
	}
	apiJSON(w, 200, map[string]any{"ok": true})
}

// apiLogout signs the browser out and retires the workspace credential:
// every copy of the cookie, not just this browser's, loses access. A
// claimed workspace comes back with a new magic link at the new epoch; an
// unclaimed one has no way back, so it is refused rather than orphaned.
func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	if !s.requireClaimed(w, r) {
		return
	}
	if s.d.Pool == nil {
		apiErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := s.d.Pool.RotateWorkspaceAuthEpoch(r.Context(), db(r).Workspace()); err != nil {
		slog.Error("workspace logout rotation failed", "err", err, "rid", reqIDFromCtx(r.Context()))
		apiErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	s.d.WS.ClearCookie(w, r)
	apiJSON(w, 200, map[string]any{"ok": true})
}

// claimStamp renders the send time in BRT ("19h18" / "7:18 PM") so each
// magic-link email has a unique subject and Gmail does not thread old,
// already-used links. The English mail gets the 12 h clock (KLOX-232).
func claimStamp(t time.Time, pt bool) string {
	loc := time.FixedZone("BRT", -3*3600)
	if pt {
		return t.In(loc).Format("15h04")
	}
	return t.In(loc).Format("3:04 PM")
}
