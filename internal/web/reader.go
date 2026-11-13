package web

// Reader-facing output: the issue email/outbox copy, paywall gating and
// the localized subscribers-only note. The reader page itself is React;
// these helpers shape the Markdown the API returns and the mailer sends.
import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const demoReaderEmail = "demo@reader.test"
const demoReaderName = "Demo Reader"

// sendIssueMail is the outbox-mode send: one recorded message to the
// given addresses (demo semantics — the demo reader's subscription
// decides the gating). Live sends go through issueMessage + sendMail.
func (s *Server) sendIssueMail(ctx context.Context, wdb *store.DB, wsID string, iss *store.Issue, pub *store.Publication, to []string, kind, base string) (string, error) {
	cpub, _ := s.canonicalPub(pub, nil)
	md := delivery.ReaderBody(iss, cpub, base)
	rid, _ := wdb.UpsertReader(ctx, pub.ID, demoReaderEmail, demoReaderName)
	active, _ := wdb.HasActiveSubscription(ctx, rid)
	msg := s.issueMessage(ctx, wdb, wsID, iss, pub, cpub, rid, demoReaderEmail, active, kind, base, md, "")
	mailer := delivery.Outbox{DB: wdb, Kind: kind, Ctx: ctx}
	return mailer.Send(msg.Subject, msg.Text, msg.HTML, to)
}

// issueMessage renders one recipient's issue email: gated or full body
// by subscription, their signed unsubscribe link in the footer and in
// the RFC 8058 headers, and the idempotency key that makes resends
// safe. tok carries the caller-supplied idempotency namespace.
func (s *Server) issueMessage(ctx context.Context, wdb *store.DB, wsID string, iss *store.Issue, pub, cpub *store.Publication, rid int64, email string, active bool, kind, base, md, idemNS string) *delivery.Message {
	tokQ := ""
	if s.d.WS.Hosted() {
		tokQ = "&tok=" + url.QueryEscape(s.d.WS.PublicToken(wsID))
	}
	unsub := fmt.Sprintf("%s/unsub?pub=%d%s&r=%d&t=%s",
		base, pub.ID, tokQ, rid, s.d.WS.MAC("unsub", fmt.Sprintf("%d:%d", pub.ID, rid)))
	publicPage := fmt.Sprintf("%s/p/%s/%d", base, pub.Slug, iss.Number)
	if s.d.WS.Hosted() {
		publicPage += "?tok=" + url.QueryEscape(s.d.WS.PublicToken(wsID))
	}
	if !active {
		md = gateSections(md, cpub.Paywall.PaidSections, cpub.Language)
	}
	md = s.localizeReaderMD(md, pub)
	if !active {
		md += fmt.Sprintf("\n\n---\n*[Assinar %s](%s) · [cancelar assinatura](%s)*",
			pub.Name, publicPage, unsub)
	} else {
		md += fmt.Sprintf("\n\n---\n*[cancelar assinatura](%s)*", unsub)
	}
	idem := fmt.Sprintf("%s/%d/r%d", kind, iss.ID, rid)
	if idemNS != "" {
		idem = idemNS + "/" + idem
	}
	return &delivery.Message{
		To:      []string{email},
		Subject: iss.Subject,
		Text:    md,
		HTML:    delivery.RenderHTML(iss.Subject, md, base),
		Kind:    kind,
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + unsub + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
		IdemKey: idem,
	}
}

// mailResult aggregate for one issue send across its recipients.
type sendSummary struct {
	To       []string
	Sent     int
	Refused  []string
	Fallback int
	Deduped  int
	FirstID  int64
}

// sendIssueLive delivers the issue to real recipients under the D33
// rules: claimed workspace, confirmed addresses only, persisted caps,
// idempotent retries, outbox fallback on provider failure.
func (s *Server) sendIssueLive(ctx context.Context, wdb *store.DB, wsID string, iss *store.Issue, pub *store.Publication, kind, owner, base, idemNS string, readers []store.Reader) (*sendSummary, error) {
	cpub, _ := s.canonicalPub(pub, nil)
	md := delivery.ReaderBody(iss, cpub, base)
	sum := &sendSummary{}
	send := func(rid int64, email string, active bool) error {
		msg := s.issueMessage(ctx, wdb, wsID, iss, pub, cpub, rid, email, active, kind, base, md, idemNS)
		res, err := s.sendMail(ctx, wdb, msg)
		if err != nil {
			return err
		}
		sum.To = append(sum.To, email)
		if res.Mail != nil && sum.FirstID == 0 {
			sum.FirstID = res.Mail.ID
		}
		switch {
		case res.Refused != "":
			sum.Refused = append(sum.Refused, email+": "+res.Refused)
		case res.Deduped:
			sum.Deduped++
		case res.Fallback:
			sum.Fallback++
		case res.Sent():
			sum.Sent++
		}
		return nil
	}
	if kind == "test" {
		// Test sends go to the claimed creator address only — the owner
		// is a verified recipient and sees the full issue.
		rid, err := wdb.UpsertReader(ctx, pub.ID, owner, "")
		if err != nil {
			return nil, err
		}
		_ = wdb.MarkReaderConfirmed(ctx, rid)
		return sum, send(rid, owner, true)
	}
	for _, rd := range readers {
		active, _ := wdb.HasActiveSubscription(ctx, rd.ID)
		if err := send(rd.ID, rd.Email, active); err != nil {
			return sum, err
		}
	}
	return sum, nil
}

// gateSections gates a paid section like a real paywall: the section
// and first item's headings plus the opening lines, then one
// subscribers-only note. Everything after the note — the rest of that
// item and every following item in the section — is removed, and the
// note says how many items were hidden.
func gateSections(body string, paid []string, lang string) string {
	paidSet := map[string]bool{}
	for _, p := range paid {
		paidSet[p] = true
	}
	var out []string
	gated := false
	teased := false
	noteIdx := -1
	hidden := 0
	seal := func() {
		if hidden > 0 && noteIdx >= 0 {
			out[noteIdx] = "> **" + paywallNote(lang, hidden) + "**"
		}
		hidden = 0
		noteIdx = -1
	}
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			if teased {
				// the wall ends the issue — nothing below it ships
				continue
			}
			seal()
			gated = paidSet[strings.TrimSpace(strings.TrimPrefix(t, "## "))]
			out = append(out, line)
			continue
		}
		if !gated {
			out = append(out, line)
			continue
		}
		if teased {
			if strings.HasPrefix(t, "### ") {
				hidden++
			}
			continue
		}
		if strings.HasPrefix(t, "### ") {
			out = append(out, line)
			continue
		}
		if t == "" {
			continue
		}
		out = append(out, firstSentences(t, 1))
		out = append(out, "", "> **"+paywallNote(lang, 0)+"**", "")
		noteIdx = len(out) - 2
		teased = true
	}
	seal()
	return strings.Join(out, "\n")
}

// paywallNote is the subscribers-only marker inserted after a paid
// section's teaser, in the publication's own language. When the wall
// also hides further items, "more" names how many.
func paywallNote(lang string, more int) string {
	pt := strings.HasPrefix(strings.ToLower(lang), "pt")
	if more > 0 {
		if pt {
			return fmt.Sprintf("Exclusivo para assinantes — assine para ler esta e mais %d matérias.", more)
		}
		return fmt.Sprintf("Subscribers only — subscribe to read this and %d more stories.", more)
	}
	if pt {
		return "Exclusivo para assinantes — assine para continuar a leitura."
	}
	return "Subscribers only — subscribe to keep reading."
}

// firstSentences returns text up to the nth sentence end (or the whole
// line when shorter).
func firstSentences(t string, n int) string {
	pos := 0
	for i := 0; i < n; i++ {
		j := strings.Index(t[pos:], ". ")
		if j < 0 {
			return t
		}
		pos += j + 1
	}
	return t[:pos]
}
