package web

// Paid-section checkout (KLOX-13, D34/D56): the reader opens a one-time
// PIX checkout at AbacatePay (test-mode key for the hackathon), a
// verified webhook or the throttled status poll confirms it, and the
// reader receives a magic link that unlocks the paid sections for
// payAccessDays. The fake provider keeps the whole grant path working
// when the key is missing or the provider is down — the response's
// provider/simulated fields are what the UI labels.
import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/payments"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const (
	// payAccessDays is the window one PIX checkout buys (D56): PIX has
	// no recurring billing on AbacatePay, so "subscription" is sold as
	// a one-time charge granting a fixed access window.
	payAccessDays = 30
	// payPollInterval throttles provider status calls on the poll
	// endpoint — the page refetches freely, the provider does not.
	payPollInterval = 10 * time.Second
	// abacateSignKey is AbacatePay's documented public HMAC key — one
	// value for every account (docs.abacatepay.com/pages/notificacoes/
	// seguranca). It signs the body; the per-webhook secret proves
	// origin.
	abacateSignKey = "t9dXRhHHo3yDEj5pVDYz0frf7q6bMKyMRmxxCPIPp3RCplBfXRxqlC6ZpiWmOqj4L63qEaeUOtrCI8P0VMUgo6iIga2ri9ogaHFs0WIIywSMg0q7RmBfybe1E5XJcfC4IW3alNqym0tXoAKkzvfEjZxV6bE0oG2zJrNNYmUCKZyV0KZ3JS8Votf9EAWWYdiDkMkpbMdPggfh1EqHlVkMiTady6jOR3hyzGEHrIz2Ret0xHKMbiqkr9HS1JhNHDX9"
)

// payProvider returns the configured checkout provider, or the fake —
// an unset key is the documented fallback, not an error.
func (s *Server) payProvider() payments.Provider {
	if s.d.Pay != nil {
		return s.d.Pay
	}
	return &payments.Fake{}
}

// providerNamed resolves the provider a payment row recorded: the
// configured provider when the name matches, the fake for fake rows,
// and a real client rebuilt from the config key for anything else — a
// checkout opened before the key was removed keeps polling the real
// provider rather than granting through the fake.
func (s *Server) providerNamed(name string) payments.Provider {
	if name == payments.FakeName {
		return &payments.Fake{}
	}
	if s.d.Pay != nil && s.d.Pay.Name() == name {
		return s.d.Pay
	}
	if name == payments.AbacatePayName {
		return payments.AbacatePay{APIKey: s.d.Cfg.AbacatePayKey}
	}
	return &payments.Fake{}
}

// apiPayCheckout opens the paid-section checkout for a reader address:
// one pending payments row per (reader, publication), an AbacatePay
// billing pointed back at the public issue page, and the hosted URL
// the reader is redirected to. The route is public — the sealed
// workspace token in the URL resolves the publication's workspace.
func (s *Server) apiPayCheckout(w http.ResponseWriter, r *http.Request) {
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
	if !pub.Paywall.Enabled {
		apiErr(w, 409, "blocked", "this publication does not sell a paid section")
		return
	}
	var in struct {
		Email string `json:"email"`
		Issue string `json:"issue"` // issue number on the page the reader came from
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
	rid, err := wdb.UpsertReader(ctx, pub.ID, email, "")
	if apiDBErr(w, r, err, "reader") {
		return
	}
	if active, err := wdb.HasActiveSubscription(ctx, rid); err == nil && active {
		apiJSON(w, 200, map[string]any{"already_subscribed": true})
		return
	}
	if p, err := wdb.PendingPaymentForReader(ctx, pub.ID, rid); err == nil && p != nil {
		// A still-open checkout is reused — a second click must not
		// mint a second billing.
		apiJSON(w, 200, s.payResponse(p))
		return
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		apiErr(w, 500, "internal", "could not open the checkout")
		return
	}
	ref := "pay_" + hex.EncodeToString(raw)
	cents := priceToCents(pub.Paywall.Price)
	payID, err := wdb.CreatePayment(ctx, pub.ID, rid, ref, s.payProvider().Name(), cents)
	if apiDBErr(w, r, err, "payment") {
		return
	}
	// The reader returns to the issue they were reading; ?pay=ref is
	// the handle the status poll uses to confirm (or keep waiting).
	page := "/p/" + pub.Slug + "/latest"
	if n, cerr := strconv.Atoi(strings.TrimSpace(in.Issue)); cerr == nil && n > 0 {
		page = fmt.Sprintf("/p/%s/%d", pub.Slug, n)
	}
	base := s.publicBase(r)
	returnTo := base + page
	if q := tokSuffix(r); q != "" {
		// tokSuffix returns "&tok=…"; the query string must start with
		// "?", never "&" (same pattern as claim.go's confirm redirect).
		returnTo += "?" + strings.TrimPrefix(q, "&")
	}
	sep := "?"
	if strings.Contains(returnTo, "?") {
		sep = "&"
	}
	done := returnTo + sep + "pay=" + ref
	desc := "30 days of subscriber access"
	if ptLang(pub.Language) {
		desc = "30 dias de acesso de assinante"
	}
	breq := payments.BillingRequest{
		ExternalID:    ref,
		ProductName:   pub.Name,
		ProductDesc:   desc,
		PriceCents:    cents,
		ReturnURL:     returnTo,
		CompletionURL: done,
		Metadata:      map[string]any{"ref": ref, "publication": pub.Slug},
	}
	prov := s.payProvider()
	billing, err := prov.CreateBilling(ctx, breq)
	if err != nil {
		// Provider down: the fake provider takes over so the reader is
		// never stranded — the row records which provider served.
		slog.Warn("checkout: provider failed, falling back to fake",
			"provider", prov.Name(), "err", sanitizeErr(err), "rid", reqIDOf(r))
		prov = &payments.Fake{}
		billing, err = prov.CreateBilling(ctx, breq)
	}
	if err != nil {
		slog.Error("checkout: fake provider failed", "err", err, "rid", reqIDOf(r))
		apiErr(w, 502, "provider_error", "could not open the checkout")
		return
	}
	if err := wdb.SetPaymentCheckout(ctx, payID, prov.Name(), billing.ID, billing.URL); err != nil {
		slog.Error("checkout: store billing", "err", err, "rid", reqIDOf(r))
		apiErr(w, 500, "internal", "could not open the checkout")
		return
	}
	pay, err := wdb.PaymentByRef(ctx, pub.ID, ref)
	if apiDBErr(w, r, err, "payment") {
		return
	}
	resp := s.payResponse(pay)
	resp["checkout_url"] = billing.URL
	apiJSON(w, 200, resp)
}

// apiPayStatus is the reader return's confirmation path and the
// documented polling fallback for a webhook that never arrives: it
// reports the stored status and, when a provider call is due, asks the
// provider once — a PAID answer runs the same idempotent grant the
// webhook does.
func (s *Server) apiPayStatus(w http.ResponseWriter, r *http.Request) {
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
	pay, err := wdb.PaymentByRef(ctx, pub.ID, r.PathValue("ref"))
	if apiDBErr(w, r, err, "payment") {
		return
	}
	if pay.Status == "pending" && payPollDue(pay) {
		prov := s.providerNamed(pay.Provider)
		st, perr := prov.BillingStatus(ctx, pay.ProviderRef)
		wdb.TouchPaymentPoll(ctx, pay.ID, store.NowUTC())
		switch {
		case perr != nil:
			slog.Warn("pay status poll failed", "ref", pay.Ref, "err", perr, "rid", reqIDOf(r))
		case st == "PAID":
			if _, err := s.grantPaidAccess(ctx, wdb, pay, pub, s.publicBase(r)); err != nil {
				slog.Error("pay grant", "ref", pay.Ref, "err", err, "rid", reqIDOf(r))
			}
		case st == "EXPIRED" || st == "CANCELLED" || st == "REFUNDED":
			wdb.SetPaymentStatus(ctx, pay.ID, strings.ToLower(st))
		}
		pay, err = wdb.PaymentByRef(ctx, pub.ID, pay.Ref)
		if apiDBErr(w, r, err, "payment") {
			return
		}
	}
	resp := s.payResponse(pay)
	if pay.Status == "paid" {
		// Outbox mode has no real inbox — re-serve the access link like
		// the claim and confirm flows do. With a live sender the link
		// only ever travels by email.
		if link := s.accessLinkForPoll(ctx, wdb, pay, pub, s.publicBase(r)); link != "" {
			resp["magic_link"] = link
		}
	}
	apiJSON(w, 200, resp)
}

// payPollDue reports whether the throttled provider poll may fire —
// last_polled_at is the stored 'YYYY-MM-DD HH24:MI:SS' shape, so a
// lexical bound is the chronological one.
func payPollDue(p *store.Payment) bool {
	return p.LastPolledAt == "" || p.LastPolledAt <= store.TSUTC(time.Now().Add(-payPollInterval))
}

// payResponse is the shared checkout/status payload. provider tells the
// UI which provider served (fake → "simulated" label); test_mode is
// the provider's own sandbox flag ("no real charge" label).
func (s *Server) payResponse(p *store.Payment) map[string]any {
	out := map[string]any{
		"ref": p.Ref, "status": p.Status, "provider": p.Provider,
		"test_mode": s.providerNamed(p.Provider).TestMode(),
		"simulated": p.Provider == payments.FakeName,
	}
	if p.CheckoutURL != "" {
		out["checkout_url"] = p.CheckoutURL
	}
	if p.PaidUntil != "" {
		out["paid_until"] = p.PaidUntil
	}
	return out
}

// grantPaidAccess flips a confirmed payment into payAccessDays of
// access: the pending→paid CAS makes the grant single-fire — a webhook
// replay, a raced poll or a retried delivery finds status=paid and
// must not send a second link. The fresh magic token goes to the
// reader by email; the idempotency key dedupes any path that still
// reaches sendMail twice for the same ref.
func (s *Server) grantPaidAccess(ctx context.Context, wdb *store.DB, pay *store.Payment, pub *store.Publication, base string) (bool, error) {
	paidUntil := store.TSUTC(time.Now().Add(payAccessDays * 24 * time.Hour))
	ok, err := wdb.MarkPaymentPaid(ctx, pay.ID, paidUntil)
	if err != nil || !ok {
		return false, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return false, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := wdb.ActivateSubscriptionUntil(ctx, pay.ReaderID, pay.Provider, pay.ProviderRef, token, paidUntil); err != nil {
		return false, err
	}
	rd, err := wdb.ReaderByID(ctx, pay.ReaderID)
	if err != nil {
		return false, err
	}
	link := s.magicLink(ctx, wdb, pub, base, token)
	subject, md := pub.Name+" — your subscriber access",
		fmt.Sprintf("Your %s subscription is active for %d days.\n\n[Read the paid section](%s)\n\nThis link signs you in — keep it private.", pub.Name, payAccessDays, link)
	if ptLang(pub.Language) {
		subject, md = pub.Name+" — seu acesso de assinante",
			fmt.Sprintf("Sua assinatura de %s está ativa por %d dias.\n\n[Ler a seção de assinantes](%s)\n\nEste link abre o seu acesso — guarde-o com cuidado.", pub.Name, payAccessDays, link)
	}
	res, err := s.sendMail(ctx, wdb, &delivery.Message{
		To:      []string{rd.Email},
		Subject: subject,
		Text:    md,
		HTML:    delivery.RenderHTML(subject, md, base),
		Kind:    "reader_access",
		IdemKey: "paid/" + pay.Ref,
	})
	switch {
	case err != nil:
		slog.Error("paid access mail failed", "ref", pay.Ref, "err", err)
	case res.Refused != "":
		slog.Warn("paid access mail refused", "ref", pay.Ref, "reason", res.Refused)
	case res.Fallback:
		slog.Warn("paid access mail fell back to outbox", "ref", pay.Ref)
	}
	return true, nil
}

// magicLink builds the reader's access URL — the latest public issue
// when one exists, the publication page otherwise.
func (s *Server) magicLink(ctx context.Context, wdb *store.DB, pub *store.Publication, base, token string) string {
	page := "/p/" + pub.Slug + "/latest"
	if iss, err := wdb.LatestPublicIssue(ctx, pub.ID); err == nil {
		page = fmt.Sprintf("/p/%s/%d", pub.Slug, iss.Number)
	}
	link := base + page + "?key=" + url.QueryEscape(token)
	if s.d.WS.Hosted() {
		link += "&tok=" + url.QueryEscape(s.d.WS.PublicToken(wdb.Workspace()))
	}
	return link
}

// accessLinkForPoll re-reads the granted token so the return page can
// re-serve the link — but only when there is no real-mail channel to
// have delivered it by.
func (s *Server) accessLinkForPoll(ctx context.Context, wdb *store.DB, pay *store.Payment, pub *store.Publication, base string) string {
	if s.liveMail() {
		return ""
	}
	tok, err := wdb.SubscriptionToken(ctx, pay.ReaderID)
	if err != nil || tok == "" {
		return ""
	}
	return s.magicLink(ctx, wdb, pub, base, tok)
}

// --- AbacatePay webhook ---

// abacateSignatureOK verifies the documented scheme: X-Webhook-Signature
// is base64(HMAC-SHA256(publicKey, rawBody)) — the key is public and
// proves integrity; the per-webhook secret proves origin.
func abacateSignatureOK(body []byte, sig string) bool {
	mac := hmac.New(sha256.New, []byte(abacateSignKey))
	mac.Write(body)
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}

// abacateEvent is the tolerant decode of a billing event: v1 sends
// {event, devMode, data:{billing fields}}; newer shapes may nest
// data.billing. Unknown fields are ignored — the docs warn a rigid
// schema breaks on every field addition.
type abacateEvent struct {
	Event   string          `json:"event"`
	DevMode bool            `json:"devMode"`
	Data    json.RawMessage `json:"data"`
}

// abacateBillRef extracts (billing id, our externalId, status) from the
// event data. The v2 envelope nests it under data.checkout; the legacy
// shape carried it under data.billing or flat — try all three.
func abacateBillRef(data json.RawMessage) (id, externalID, status string) {
	var bill struct {
		ID         string `json:"id"`
		ExternalID string `json:"externalId"`
		Status     string `json:"status"`
	}
	_ = json.Unmarshal(data, &bill)
	if bill.ID == "" && bill.ExternalID == "" {
		var nested struct {
			Checkout json.RawMessage `json:"checkout"`
			Billing  json.RawMessage `json:"billing"`
		}
		if json.Unmarshal(data, &nested) == nil {
			for _, raw := range []json.RawMessage{nested.Checkout, nested.Billing} {
				if len(raw) > 0 {
					_ = json.Unmarshal(raw, &bill)
				}
				if bill.ID != "" || bill.ExternalID != "" {
					break
				}
			}
		}
	}
	return bill.ID, bill.ExternalID, bill.Status
}

// abacatePayWebhook confirms checkouts: secret first (header or the
// ?webhookSecret= query the dashboard appends), then the HMAC
// signature when the sender carries one. A billing.paid event resolves
// the payment across workspaces and runs the idempotent grant; unknown
// payments answer 202 so the provider stops retrying.
func (s *Server) abacatePayWebhook(w http.ResponseWriter, r *http.Request) {
	secret := s.d.Cfg.AbacatePayWebhookSecret
	if secret == "" {
		// fail closed like the other receivers: without the secret a
		// forged event could grant paid access to anyone.
		http.Error(w, "webhook secret not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	got := r.Header.Get("X-Webhook-Secret")
	if got == "" {
		got = r.URL.Query().Get("webhookSecret")
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if sig := r.Header.Get("X-Webhook-Signature"); sig != "" && !abacateSignatureOK(body, sig) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var ev abacateEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if ev.Event != "billing.paid" && ev.Event != "checkout.completed" {
		w.WriteHeader(204)
		return
	}
	billID, extID, status := abacateBillRef(ev.Data)
	if status != "" && status != "PAID" {
		w.WriteHeader(204)
		return
	}
	if billID == "" && extID == "" {
		slog.Warn("abacatepay webhook: no billing reference", "event", ev.Event)
		w.WriteHeader(202)
		return
	}
	ctx := r.Context()
	var pay *store.Payment
	var wdb *store.DB
	if pool := s.d.WS.Pool(); pool != nil {
		var wsID string
		if billID != "" {
			pay, wsID, _ = pool.FindPaymentByProviderRef(ctx, payments.AbacatePayName, billID)
		}
		if pay == nil && extID != "" {
			pay, wsID, _ = pool.FindPaymentByRef(ctx, extID)
		}
		if pay != nil {
			wdb = pool.For(wsID)
		}
	} else if l := s.d.WS.Local(); l != nil {
		if billID != "" {
			pay, _ = l.DB.PaymentByProviderRef(ctx, payments.AbacatePayName, billID)
		}
		if pay == nil && extID != "" {
			pay, _ = l.DB.PaymentByRefUnscoped(ctx, extID)
		}
		wdb = l.DB
	}
	if pay == nil || wdb == nil {
		// Unknown billing — still 202 so AbacatePay stops retrying; it
		// may be a checkout this app never opened.
		slog.Warn("abacatepay webhook: unknown billing", "id", billID, "ref", extID)
		w.WriteHeader(202)
		return
	}
	if pay.Status != "pending" {
		w.WriteHeader(204)
		return
	}
	pub, err := wdb.GetPublication(ctx, fmt.Sprint(pay.PublicationID))
	if err != nil {
		slog.Error("abacatepay webhook: publication", "err", err, "rid", reqIDOf(r))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	base := strings.TrimRight(s.d.Cfg.PublicURL, "/")
	if base == "" {
		base = requestBase(r)
	}
	if _, err := s.grantPaidAccess(ctx, wdb, pay, pub, base); err != nil {
		slog.Error("abacatepay webhook: grant", "err", err, "rid", reqIDOf(r))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("abacatepay billing confirmed", "ref", pay.Ref, "devMode", ev.DevMode)
	w.WriteHeader(204)
}

// priceToCents reads the creator's free-form paywall price ("R$ 12/mês",
// "V$ 12/month", "$9.90") into centavos for the provider. The first
// number wins; a trailing separator followed by 1–2 digits reads as
// decimals. Unparseable prices fall back to 500 — the checkout label
// shows the string, never the guess.
func priceToCents(price string) int {
	i := 0
	for i < len(price) && (price[i] < '0' || price[i] > '9') {
		i++
	}
	j := i
	for j < len(price) && (price[j] >= '0' && price[j] <= '9' || price[j] == ',' || price[j] == '.') {
		j++
	}
	tok := price[i:j]
	if tok == "" {
		return 500
	}
	// decimal separator = the last ',' or '.' followed by 1–2 digits
	whole, frac := tok, ""
	for k := len(tok) - 1; k > 0; k-- {
		if tok[k] == ',' || tok[k] == '.' {
			if n := len(tok) - k - 1; n >= 1 && n <= 2 && allDigits(tok[k+1:]) {
				whole, frac = tok[:k], tok[k+1:]
			}
			break
		}
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, whole)
	if digits == "" {
		return 500
	}
	w, err := strconv.Atoi(digits)
	if err != nil {
		return 500
	}
	f := 0
	if frac != "" {
		f, _ = strconv.Atoi(frac)
		for len(frac) < 2 {
			frac += "0"
			f *= 10
		}
	}
	c := w*100 + f
	if c <= 0 {
		return 500
	}
	return c
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}
