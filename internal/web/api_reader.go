package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/editorial"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// --- paywall / reader state ---

func (s *Server) apiReader(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	rid, _ := db(r).UpsertReader(r.Context(), pub.ID, demoReaderEmail, demoReaderName)
	active, _ := db(r).HasActiveSubscription(r.Context(), rid)
	subs, _ := db(r).SubscriberCount(r.Context(), pub.ID)
	readers, _ := db(r).ReaderCount(r.Context(), pub.ID)
	resp := map[string]any{
		"reader":      map[string]string{"email": demoReaderEmail, "name": demoReaderName},
		"subscribed":  active,
		"subscribers": subs,
		"readers":     readers,
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
	link := requestBase(r) + "/p/" + pub.Slug + "/latest?key=" + token + s.tokQ(r)
	if iss, err := db(r).LatestPublicIssue(ctx, pub.ID); err == nil {
		link = fmt.Sprintf("%s/p/%s/%d?key=%s%s",
			requestBase(r), pub.Slug, iss.Number, token, s.tokQ(r))
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
	if !s.d.WS.Hosted() {
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
// forced free preview), with no editor-only fields.
func (s *Server) apiPublicIssue(w http.ResponseWriter, r *http.Request) {
	r = s.withWS(r)
	rawPub, loadErr := db(r).GetPublication(r.Context(), r.PathValue("slug"))
	pub, err := s.canonicalPub(rawPub, loadErr)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var iss *store.Issue
	if r.PathValue("num") == "latest" {
		iss, err = db(r).LatestPublicIssue(r.Context(), pub.ID)
	} else {
		num, nerr := strconv.Atoi(r.PathValue("num"))
		if nerr != nil {
			apiErr(w, 404, "not_found", "issue not found")
			return
		}
		iss, err = db(r).IssueByNumber(r.Context(), pub.ID, num)
	}
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status != "sent" && iss.Status != "scheduled" {
		apiErr(w, 404, "not_found", "issue not found")
		return
	}
	ctx := r.Context()
	rid, _ := db(r).UpsertReader(ctx, pub.ID, demoReaderEmail, demoReaderName)
	active, _ := db(r).HasActiveSubscription(ctx, rid)
	key := r.URL.Query().Get("key")
	if key != "" {
		if mrid, err := db(r).ReaderByMagicToken(ctx, pub.ID, key); err == nil && mrid == rid {
			active = true
		}
	}
	forceFree := r.URL.Query().Get("view") == "free"
	unlocked := active && !forceFree
	md := delivery.ReaderBody(iss, pub, requestBase(r))
	if !unlocked {
		md = gateSections(md, pub.Paywall.PaidSections, pub.Language)
	}
	md = s.localizeReaderMD(md, rawPub)
	apiJSON(w, 200, map[string]any{
		"publication": map[string]any{"name": rawPub.Name, "slug": rawPub.Slug, "language": rawPub.Language},
		"issue":       map[string]any{"number": iss.Number, "subject": iss.Subject, "created_at": iss.CreatedAt},
		"body_md":     md, "gated": !unlocked, "subscribed": active,
		"reader":        map[string]string{"name": demoReaderName},
		"paid_sections": pub.Paywall.PaidSections,
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
	if iss.Status != "sent" && iss.Status != "scheduled" {
		apiErr(w, 409, "blocked", "the public page exists once the issue is sent or scheduled",
			apiReason{Code: "not_sent", Message: "issue is not sent",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	rawReasons, _ := db(r).IssueRejectionReasons(r.Context(), iss.ID)
	learned := editorial.RulesFrom(rawReasons)
	version, _, _ := db(r).LatestGuide(r.Context(), pub.ID)
	publicURL := fmt.Sprintf("%s/p/%s/%d", requestBase(r), pub.Slug, iss.Number)
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
	r = s.withWS(r)
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), r.URL.Query().Get("pub")))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	rid, _ := strconv.ParseInt(r.URL.Query().Get("r"), 10, 64)
	tok := r.URL.Query().Get("t")
	if rid == 0 || !s.d.WS.ValidMAC("unsub", fmt.Sprintf("%d:%d", pub.ID, rid), tok) {
		apiErr(w, 403, "validation", "invalid unsubscribe link")
		return
	}
	if apiDBErr(w, r, db(r).UnsubscribeReader(r.Context(), rid), "reader") {
		return
	}
	apiJSON(w, 200, map[string]any{"unsubscribed": true})
}
