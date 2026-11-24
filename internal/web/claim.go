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
	link := s.publicBase(r) + "/claim?t=" + url.QueryEscape(token)
	subject, md := "Confirm your email to create your Radar",
		fmt.Sprintf("Confirm this email to create your Radar.\n\n[Create my Radar](%s)\n\nThe link works once and expires in 15 minutes.", link)
	if ptLang(in.Lang) {
		link += "&lang=pt"
		subject, md = "Confirme seu e-mail para criar seu Radar",
			fmt.Sprintf("Confirme este e-mail para criar seu Radar.\n\n[Criar meu Radar](%s)\n\nO link funciona uma vez e expira em 15 minutos.", link)
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
	if pool := s.d.WS.Pool(); pool != nil {
		_, em, err := pool.PeekMagicLink(r.Context(), hash, now)
		if err != nil {
			claimFail(w, lang)
			return
		}
		email = em
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
		"The link works once and expires 15 minutes after it was sent."
	if ptLang(lang) {
		title, blurb, button, note = "Confirme seu e-mail para criar seu Radar",
			"Ao confirmar, este navegador fica associado ao e-mail %s.",
			"Confirmar e continuar",
			"O link funciona uma vez e expira 15 minutos depois de enviado."
	}
	fmt.Fprintf(w, claimShell(lang, `<h1 style="font-size:1.5rem;line-height:1.25;margin:0 0 .75rem">%s</h1>`+
		`<p style="margin:0 0 1.5rem;color:#111814">`+blurb+`</p>`+
		`<form method="POST" action="/claim?t=%s&amp;lang=%s"><button type="submit" `+
		`style="font:inherit;font-weight:600;padding:.7em 1.5em;border:0;border-radius:.6rem;`+
		`background:#0e3b2e;color:#fff;cursor:pointer">%s</button></form>`+
		`<p style="margin:1.5rem 0 0;color:#5b6660;font-size:.85rem">%s</p>`),
		html.EscapeString(title), html.EscapeString(email),
		url.QueryEscape(token), url.QueryEscape(lang),
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

// claimConfirm spends a magic link: it must be unexpired and unused,
// then the workspace becomes owned and the cookie signs this browser in.
func (s *Server) claimConfirm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	lang := r.URL.Query().Get("lang")
	hash := hashToken(token)
	now := store.NowUTC()
	var wsID, email string
	if pool := s.d.WS.Pool(); pool != nil {
		id, em, err := pool.ConsumeMagicLink(r.Context(), hash, now)
		if err != nil {
			claimFail(w, lang)
			return
		}
		wsID, email = id, em
		if err := pool.ClaimWorkspace(r.Context(), wsID, email); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		// A confirmed owner is someone who really entered the product:
		// register them for the launch invitation (KLOX-161). Idempotent;
		// a failure here must not undo the claim.
		if err := pool.AddWaitlist(r.Context(), email, "claim"); err != nil {
			slog.Warn("launch invite registration failed", "err", err, "rid", reqIDFromCtx(r.Context()))
		}
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
	} else {
		claimFail(w, lang)
		return
	}
	if s.d.WS.Hosted() {
		// Opening the link in any browser signs that browser into the
		// claimed workspace.
		s.d.WS.SetCookie(w, r, wsID)
	}
	// straight on to "Criar seu Radar" (/start) — the screen forwards to
	// the existing Radar when the owner already has one
	http.Redirect(w, r, "/start", http.StatusSeeOther)
}

// claimFail renders the single failure state for a bad, spent or
// expired link, in the language the link carried.
func claimFail(w http.ResponseWriter, lang string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	title, body := "Link expired or already used",
		"Links work once and expire after 15 minutes. Go back to Kloxus and request a new one."
	if ptLang(lang) {
		title, body = "Link expirado ou já usado",
			"O link funciona uma vez e expira em 15 minutos. Volte ao Kloxus e peça um novo."
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
