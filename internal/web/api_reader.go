package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/editorial"
	"github.com/sthefanocordeiro/kloxus/internal/payments"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func (s *Server) apiReader(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	rid, _ := db(r).UpsertReader(r.Context(), pub.ID, demoReaderEmail, demoReaderName)
	active, _ := db(r).HasActiveSubscription(r.Context(), rid)
	subs, _ := db(r).SubscriberCount(r.Context(), pub.ID)
	readers, _ := db(r).ReaderCount(r.Context(), pub.ID)
	confirmed, _ := db(r).ConfirmedReaderCount(r.Context(), pub.ID)
	// the copyable subscribe link (KLOX-102): the stable public
	// newsletter page — ?tok= sealed in hosted mode so it opens for an
	// anonymous visitor, never the editor's workspace id
	publicURL := fmt.Sprintf("%s/p/%s", s.publicBase(r), pub.Slug)
	if tok := s.readerToken(r); tok != "" {
		publicURL += "?tok=" + url.QueryEscape(tok)
	}
	resp := map[string]any{
		"reader":      map[string]string{"email": demoReaderEmail, "name": demoReaderName},
		"subscribed":  active,
		"subscribers": subs,
		"readers":     readers,
		"confirmed":   confirmed,
		"public_url":  publicURL,
	}
	if iss, err := db(r).LatestFinishedIssue(r.Context(), pub.ID); err == nil {
		resp["issue"] = s.issueSummary(r, iss)
	}
	apiJSON(w, 200, resp)
}

func (s *Server) apiPaywallSimulate(w http.ResponseWriter, r *http.Request) {
	r = s.withWS(r)
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	ctx := r.Context()
	rid, _ := db(r).UpsertReader(ctx, pub.ID, demoReaderEmail, demoReaderName)
	tokenBytes := make([]byte, 12)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)
	if apiDBErr(w, r, db(r).ActivateSubscription(ctx, rid, "fake", "sim-"+strconv.FormatInt(rid, 10), token), "subscription") {
		return
	}
	// Demo mode delivers the magic link through the workspace outbox; the
	// sealed token lets it open in any browser. It opens the latest
	// finished issue on the reader-facing page.
	link := s.publicBase(r) + "/p/" + pub.Slug + "/latest?key=" + token + s.tokQ(r)
	if iss, err := db(r).LatestPublicIssue(ctx, pub.ID); err == nil {
		link = fmt.Sprintf("%s/p/%s/%d?key=%s%s",
			s.publicBase(r), pub.Slug, iss.Number, token, s.tokQ(r))
	}
	htmlBody := fmt.Sprintf(
		`<!DOCTYPE html><html><body style="font-family:system-ui,sans-serif;max-width:38em;margin:2em auto">`+
			`<h2>%s — acesso de assinante</h2>`+
			`<p>Olá, %s! Clique para abrir sua edição como assinante:</p>`+
			`<p><a href="%s">Abrir %s (magic link)</a></p>`+
			`<p style="color:#777;font-size:.85em">Simulado no modo demo — nenhum e-mail real foi enviado.</p></body></html>`,
		pub.Name, demoReaderName, link, pub.Name)
	emailID, err := db(r).SaveOutboxEmail(ctx, "magic_link", demoReaderEmail,
		pub.Name+" — seu link de acesso", "", htmlBody)
	if apiDBErr(w, r, err, "email") {
		return
	}
	apiJSON(w, 200, map[string]any{
		"subscribed": true, "outbox_email_id": emailID, "magic_link": link,
	})
}

func (s *Server) apiPaywallCancel(w http.ResponseWriter, r *http.Request) {
	r = s.withWS(r)
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	rid, _ := db(r).UpsertReader(r.Context(), pub.ID, demoReaderEmail, demoReaderName)
	if apiDBErr(w, r, db(r).CancelSubscription(r.Context(), rid), "subscription") {
		return
	}
	apiJSON(w, 200, map[string]any{"subscribed": false})
}

// withWS resolves a cross-workspace override for reader-facing requests.
// New links carry ?tok= — an AES-GCM sealed workspace id that reveals
// nothing in the URL. ?ws= is still honored for links already delivered.
// The visitor's own session workspace is never touched.
func (s *Server) withWS(r *http.Request) *http.Request {
	// A workspace override is honored on read-only requests only — the
	// raw ?ws= id must never mutate another visitor's state (review F2).
	if !s.d.WS.Hosted() || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return r
	}
	wsid := r.URL.Query().Get("ws")
	if wsid == "" {
		if tok := r.URL.Query().Get("tok"); tok != "" {
			wsid = s.d.WS.ResolvePublicToken(tok)
		}
	}
	if wsid == "" || wsid == reqWS(r).ID {
		return r
	}
	if target, err := s.d.WS.Get(r.Context(), wsid); err == nil {
		return r.WithContext(context.WithValue(r.Context(), ctxWS, target))
	}
	return r
}

// readerToken is the opaque workspace reference for public links.
func (s *Server) readerToken(r *http.Request) string {
	if !s.d.WS.Hosted() {
		return ""
	}
	return s.d.WS.PublicToken(reqWS(r).ID)
}

// tokQ is the "&tok=…" query suffix for public links — empty in local
// mode, where the workspace is already the reader's own.
func (s *Server) tokQ(r *http.Request) string {
	if tok := s.readerToken(r); tok != "" {
		return "&tok=" + url.QueryEscape(tok)
	}
	return ""
}

// apiPublicIssue serves the reader-facing issue page data: the gated or
// full body depending on the reader's subscription (magic-link key or a
// forced free preview), with no editor-only fields. The route is public
// — the sealed workspace token resolves the publication's workspace and
// no visitor session is minted (KLOX-193).
func (s *Server) apiPublicIssue(w http.ResponseWriter, r *http.Request) {
	wdb := s.publicWDB(r)
	if wdb == nil {
		apiErr(w, 404, "not_found", "publication not found")
		return
	}
	rawPub, loadErr := wdb.GetPublication(r.Context(), r.PathValue("slug"))
	pub, err := s.canonicalPub(rawPub, loadErr)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var iss *store.Issue
	if r.PathValue("num") == "latest" {
		iss, err = wdb.LatestPublicIssue(r.Context(), pub.ID)
	} else {
		num, nerr := strconv.Atoi(r.PathValue("num"))
		if nerr != nil {
			apiErr(w, 404, "not_found", "issue not found")
			return
		}
		iss, err = wdb.IssueByNumber(r.Context(), pub.ID, num)
	}
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status != store.IssueSent && iss.Status != store.IssueScheduled && iss.TestSentAt == "" {
		apiErr(w, 404, "not_found", "issue not found")
		return
	}
	ctx := r.Context()
	rid, _ := wdb.UpsertReader(ctx, pub.ID, demoReaderEmail, demoReaderName)
	active, _ := wdb.HasActiveSubscription(ctx, rid)
	key := r.URL.Query().Get("key")
	keyValid := false
	if key != "" {
		// The token is the bearer credential a paying reader's magic
		// link carries (KLOX-13) — any active subscriber's token
		// unlocks, not only the demo reader's.
		if _, err := wdb.ReaderByMagicToken(ctx, pub.ID, key); err == nil {
			keyValid = true
		}
	}
	forceFree := r.URL.Query().Get("view") == "free"
	unlocked := (active || keyValid) && !forceFree
	aiUsed, _ := wdb.IssueUsedAI(ctx, iss.ID)
	md := delivery.ReaderBody(iss, pub, s.publicBase(r), aiUsed)
	if !unlocked {
		md = gateSections(md, paidSectionTitles(iss, pub), pub.Language)
	}
	md = s.localizeReaderMD(md, rawPub)
	apiJSON(w, 200, map[string]any{
		"publication": map[string]any{"name": rawPub.Name, "slug": rawPub.Slug, "language": rawPub.Language},
		"issue":       map[string]any{"number": iss.Number, "subject": localizeSubject(iss.Subject, rawPub.Language), "created_at": iss.CreatedAt},
		"body_md":     md, "gated": !unlocked, "subscribed": active || keyValid,
		"reader":        map[string]string{"name": demoReaderName},
		"paid_sections": orEmpty(paidSectionTitles(iss, pub)),
		// price on the reader page (KLOX-66): the gated card sells the
		// subscription with the creator's price and the paid sections
		// test_mode/simulated label the checkout step before the reader
		// pays (KLOX-13): dev-key checkouts carry "no real charge", the
		// fake fallback is labeled simulated.
		"paywall": map[string]any{"enabled": rawPub.Paywall.Enabled, "price": localizePrice(rawPub.Paywall.Price, rawPub.Language),
			"test_mode": s.payProvider().TestMode(), "simulated": s.payProvider().Name() == payments.FakeName},
	})
}

// apiPublicNewsletter serves the /p/{slug} landing page data (KLOX-102):
// the publication's own brand — never the editor chrome — plus its
// editions archive (the same sent/scheduled/test-sent visibility the
// issue page enforces) and the paywall summary the subscribe CTA sells.
// Reading an edition stays on /p/{slug}/{num}, where the existing
// paywall gating applies per access rules. The route is public —
// publicWDB resolves the sealed token's workspace (KLOX-193).
func (s *Server) apiPublicNewsletter(w http.ResponseWriter, r *http.Request) {
	wdb := s.publicWDB(r)
	if wdb == nil {
		apiErr(w, 404, "not_found", "publication not found")
		return
	}
	rawPub, loadErr := wdb.GetPublication(r.Context(), r.PathValue("slug"))
	pub, err := s.canonicalPub(rawPub, loadErr)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	issues, err := wdb.PublicIssues(r.Context(), pub.ID, 50)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	archive := []map[string]any{}
	for _, iss := range issues {
		archive = append(archive, map[string]any{
			"number":     iss.Number,
			"subject":    localizeSubject(iss.Subject, rawPub.Language),
			"created_at": iss.CreatedAt,
		})
	}
	apiJSON(w, 200, map[string]any{
		"publication": map[string]any{"name": rawPub.Name, "slug": rawPub.Slug, "language": rawPub.Language},
		"editions":    archive,
		"paywall": map[string]any{"enabled": rawPub.Paywall.Enabled,
			"price": localizePrice(rawPub.Paywall.Price, rawPub.Language)},
	})
}

// apiAfter is the post-send summary: public URL, downloads, the approval
// receipt and the rules the editorial guide learned from this issue.
func (s *Server) apiAfter(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	if iss.Status != store.IssueSent && iss.Status != store.IssueScheduled && iss.TestSentAt == "" {
		apiErr(w, 409, "blocked", "the public page exists once the issue is sent, scheduled or test-sent",
			apiReason{Code: "not_sent", Message: "issue is not sent",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	// "Learned" rules must be rules the guide actually recorded
	// (KLOX-55): rejection reasons only feed this block when a
	// non-empty guide exists — otherwise the after-sending screen
	// claimed learning while the guide sat at version 0.
	version, guideContent, _ := db(r).LatestGuide(r.Context(), pub.ID)
	var learned []string
	if strings.TrimSpace(guideContent) != "" {
		rawReasons, _ := db(r).IssueRejectionReasons(r.Context(), iss.ID)
		learned = editorial.RulesFrom(rawReasons)
	}
	publicURL := fmt.Sprintf("%s/p/%s/%d", s.publicBase(r), pub.Slug, iss.Number)
	if tok := s.readerToken(r); tok != "" {
		publicURL += "?tok=" + url.QueryEscape(tok)
	}
	apiJSON(w, 200, map[string]any{
		"issue":       s.issueSummary(r, iss),
		"public_url":  publicURL,
		"learned":     orEmpty(learned),
		"guide":       map[string]any{"version": version},
		"export_html": fmt.Sprintf("/api/v1/issues/%d/export?fmt=html", iss.ID),
		"export_md":   fmt.Sprintf("/api/v1/issues/%d/export", iss.ID),
	})
}

// apiUnsub performs the reader-facing one-click unsubscribe behind the
// signed link every issue email carries. The React unsubscribe page calls
// it once with the link's parameters.
func (s *Server) apiUnsub(w http.ResponseWriter, r *http.Request) {
	wdb := s.publicWDB(r)
	if wdb == nil {
		apiErr(w, 403, "validation", "invalid unsubscribe link")
		return
	}
	pub, err := s.canonicalPub(wdb.GetPublication(r.Context(), r.URL.Query().Get("pub")))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	rid, _ := strconv.ParseInt(r.URL.Query().Get("r"), 10, 64)
	tok := r.URL.Query().Get("t")
	if rid == 0 || !s.d.WS.ValidMAC("unsub", fmt.Sprintf("%d:%d", pub.ID, rid), tok) {
		apiErr(w, 403, "validation", "invalid unsubscribe link")
		return
	}
	if apiDBErr(w, r, wdb.UnsubscribeReader(r.Context(), rid), "reader") {
		return
	}
	apiJSON(w, 200, map[string]any{"unsubscribed": true})
}
