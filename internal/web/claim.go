package web

// Workspace claim by magic link (D33): the creator enters an email, a
// single-use 15-minute link lands in that inbox, and opening it —
// in any browser — signs that browser into the now-owned workspace.
// This file also holds the reader double opt-in and the RFC 8058
// one-click unsubscribe target.
import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
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

// apiClaimStart mints a magic link and mails it to the creator.
func (s *Server) apiClaimStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	wdb := db(r)
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
	hash := hex.EncodeToString(sha256.New().Sum([]byte(token)))
	if err := wdb.SaveMagicLink(ctx, hash, email,
		store.TSUTC(time.Now().Add(magicLinkTTL))); err != nil {
		apiErr(w, 500, "internal", "could not save the claim link")
		return
	}
	link := s.publicBase(r) + "/claim?t=" + url.QueryEscape(token)
	md := fmt.Sprintf("Confirm this email to claim your Kloxus workspace.\n\n[Claim workspace](%s)\n\nThe link works once and expires in 15 minutes.", link)
	res, err := s.sendMail(ctx, wdb, &delivery.Message{
		To:      []string{email},
		Subject: "Claim your Kloxus workspace",
		Text:    md,
		HTML:    delivery.RenderHTML("Claim your Kloxus workspace", md, s.publicBase(r)),
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

// claimConfirm spends a magic link: it must be unexpired and unused,
// then the workspace becomes owned and the cookie signs this browser in.
func (s *Server) claimConfirm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	hash := hex.EncodeToString(sha256.New().Sum([]byte(token)))
	now := store.NowUTC()
	var wsID, email string
	if pool := s.d.WS.Pool(); pool != nil {
		id, em, err := pool.ConsumeMagicLink(r.Context(), hash, now)
		if err != nil {
			claimFail(w)
			return
		}
		wsID, email = id, em
		if err := pool.ClaimWorkspace(r.Context(), wsID, email); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
	} else if l := s.d.WS.Local(); l != nil {
		em, err := l.DB.ConsumeMagicLink(r.Context(), hash, now)
		if err != nil {
			claimFail(w)
			return
		}
		email = em
		if err := l.DB.ClaimWorkspace(r.Context(), email); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
	} else {
		claimFail(w)
		return
	}
	if s.d.WS.Hosted() {
		// Opening the link in any browser signs that browser into the
		// claimed workspace.
		s.d.WS.SetCookie(w, r, wsID)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// claimFail renders the single failure state for a bad, spent or
// expired link.
func claimFail(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprint(w, `<!DOCTYPE html><html><body style="font-family:system-ui,sans-serif;max-width:34em;margin:4em auto">`+
		`<h1>Link expired or already used</h1>`+
		`<p>Claim links work once and expire after 15 minutes. Go back to Kloxus and request a new one.</p></body></html>`)
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
	md := fmt.Sprintf("Confirm you'd like to receive %s by email.\n\n[Confirm subscription](%s)\n\nIf you did not ask for this, ignore the email — nothing else will be sent.", pub.Name, link)
	res, err := s.sendMail(ctx, wdb, &delivery.Message{
		To:      []string{email},
		Subject: pub.Name + " — confirm your subscription",
		Text:    md,
		HTML:    delivery.RenderHTML(pub.Name+" — confirm your subscription", md, s.publicBase(r)),
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
		claimFail(w)
		return
	}
	pub, err := wdb.GetPublication(r.Context(), r.URL.Query().Get("pub"))
	if err != nil {
		claimFail(w)
		return
	}
	rid, _ := strconv.ParseInt(r.URL.Query().Get("r"), 10, 64)
	tok := r.URL.Query().Get("t")
	if rid == 0 || !s.d.WS.ValidMAC("sub", fmt.Sprintf("%d:%d", pub.ID, rid), tok) {
		claimFail(w)
		return
	}
	if err := wdb.MarkReaderConfirmed(r.Context(), rid); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	target := fmt.Sprintf("/p/%s/latest", pub.Slug)
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
