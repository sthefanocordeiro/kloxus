package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func (s *Server) apiApprove(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	checks := s.readinessChecks(r, iss, pub, nil)
	var reasons []apiReason
	for _, c := range checks {
		if !c.OK {
			reasons = append(reasons, apiReason{Code: c.Code, Message: c.Message, Target: c.Target})
		}
	}
	if len(reasons) > 0 {
		apiErr(w, 409, "blocked", "the issue is not ready to approve — fix each reason first", reasons...)
		return
	}
	// Approval-time AI citecheck (KLOX-67 §7.5): the deterministic
	// floor passed, so exactly one Haiku call checks for inverted
	// meaning across every cited fact — author-edited AI claims are
	// re-checked here like any other. Capped per edition per day and
	// counted against the workspace's paid actions; on cap, kill
	// switch or API failure the deterministic results stand alone.
	if s.approveCiteCheck(w, r, iss, pub) {
		return
	}
	rec, err := s.recordApproval(r, iss, pub, checks)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	fresh, _ := db(r).GetIssue(r.Context(), iss.ID)
	apiJSON(w, 200, map[string]any{"issue": s.issueSummary(r, fresh), "receipt": receiptView(rec)})
}

// recordApproval marks the issue approved (when it is not already) and
// returns the stored receipt. The first receipt wins — a repeated
// approve returns the same receipt, never a new timestamp.
func (s *Server) recordApproval(r *http.Request, iss *store.Issue, pub *store.Publication, checks []apiCheck) (*store.ApprovalReceipt, error) {
	ctx := r.Context()
	if checks == nil {
		checks = s.readinessChecks(r, iss, pub, nil)
	}
	cj, _ := json.Marshal(checks)
	approver := s.workspaceOwner(ctx, db(r))
	approvedDoc := *iss
	approvedDoc.Status = store.IssueApproved // the receipt attests to the approved document

	var out *store.ApprovalReceipt
	err := db(r).WithTx(ctx, func(txdb *store.DB) error {
		if iss.Status != store.IssueApproved {
			if err := txdb.SetIssueStatus(ctx, iss.ID, store.IssueApproved); err != nil {
				return err
			}
			if err := txdb.RecordDecision(ctx, iss.PublicationID, 0, "approve", approveDecisionReason(iss.ID)); err != nil {
				return err
			}
		}
		rec, err := txdb.ApprovalReceipt(ctx, iss.ID)
		if err != sql.ErrNoRows {
			if err != nil {
				return err
			}
			out = rec
			return nil
		}
		rec = &store.ApprovalReceipt{
			IssueID: iss.ID, Number: iss.Number,
			DocumentVersion: docVersion(&approvedDoc),
			// the approver is the claimed owner — an anonymous session has
			// no name to show, so it stays empty rather than leaking the
			// workspace label (§2 never-show, KLOX-64)
			Approver:   approver,
			ChecksJSON: string(cj),
		}
		if err := txdb.SaveApprovalReceipt(ctx, rec); err != nil {
			return err
		}
		rec, err = txdb.ApprovalReceipt(ctx, iss.ID)
		if err != nil {
			return err
		}
		out = rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	iss.Status = store.IssueApproved
	return out, nil
}

func (s *Server) apiSend(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Audience string `json:"audience"` // test | readers (safe default: explicit choice required)
		To       string `json:"to"`       // optional test address
		When     string `json:"when"`     // now | schedule — scheduling locks the issue until Hold
		At       string `json:"at"`       // RFC3339 send time, required when when=schedule
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if in.Audience != "test" && in.Audience != "readers" {
		apiErr(w, 400, "validation", "audience is required: test | readers — sending needs an explicit choice")
		return
	}
	if in.When == "" {
		in.When = "now"
	}
	if in.When != "now" && in.When != "schedule" {
		apiErr(w, 400, "validation", "when must be now | schedule")
		return
	}
	if in.When == "schedule" && in.Audience != "readers" {
		apiErr(w, 400, "validation", "scheduling applies to a reader send — a test send goes out now")
		return
	}
	if iss.Status != store.IssueApproved {
		apiErr(w, 409, "blocked", "the issue must be approved before sending",
			apiReason{Code: "not_approved", Message: "issue is not approved",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	if in.When == "schedule" {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(in.At))
		if err != nil {
			apiErr(w, 400, "bad_time", "scheduling needs a send time: at must be RFC3339 (e.g. 2026-01-04T08:00:00Z)")
			return
		}
		if !at.After(time.Now()) {
			apiErr(w, 400, "bad_time", "the scheduled time must be in the future")
			return
		}
		atStr := at.UTC().Format(time.RFC3339)
		// demo mode has no dispatch clock: a scheduled issue is locked
		// and stays queued until Hold returns it to draft.
		if apiDBErr(w, r, db(r).ScheduleIssue(r.Context(), iss.ID, atStr), "issue") {
			return
		}
		db(r).RecordDecision(r.Context(), iss.PublicationID, 0, "schedule", "issue scheduled for "+atStr)
		fresh, _ := db(r).GetIssue(r.Context(), iss.ID)
		apiJSON(w, 200, map[string]any{
			"issue": s.issueSummary(r, fresh), "scheduled": true, "scheduled_at": atStr,
		})
		return
	}
	if !s.liveMail() {
		// Outbox/demo mode: one recorded message, no claim gate.
		to := []string{"readers@kloxus.demo"}
		kind := "issue"
		if in.Audience == "test" {
			addr := strings.TrimSpace(in.To)
			if addr == "" {
				addr = demoReaderEmail
			}
			to = []string{addr}
			kind = "test"
		}
		emailID, err := s.sendIssueMail(r.Context(), db(r), reqWS(r).ID, iss, pub, to, kind, s.publicBase(r))
		if apiDBErr(w, r, err, "send") {
			return
		}
		if kind == "issue" {
			if apiDBErr(w, r, db(r).SetIssueStatus(r.Context(), iss.ID, store.IssueSent), "issue") {
				return
			}
		} else {
			// a test send stamps the marker but never enters the
			// lifecycle — the issue stays approved and editable (KLOX-53)
			if apiDBErr(w, r, db(r).MarkIssueTestSent(r.Context(), iss.ID), "issue") {
				return
			}
		}
		if fresh, _ := db(r).GetIssue(r.Context(), iss.ID); fresh != nil {
			iss = fresh
		}
		apiJSON(w, 200, map[string]any{
			"issue": s.issueSummary(r, iss), "outbox_email_id": emailID, "audience": in.Audience,
			"to": to, "subject": localizeSubject(iss.Subject, pub.Language), "delivery": "outbox",
		})
		return
	}

	// Live delivery (D33): reader sends go to confirmed readers of a
	// claimed workspace; test sends go to the claimed address only. An
	// unclaimed workspace may still "send a test" — it writes to the
	// outbox and completes the journey: the test_sent_at marker unlocks
	// the after-sending screen and the public page while the issue stays
	// approved and editable (KLOX-49, KLOX-53). Real email still
	// requires a claimed address.
	ctx := r.Context()
	wdb := db(r)
	owner := s.workspaceOwner(ctx, wdb)
	if owner == "" {
		if in.Audience != "test" {
			apiErr(w, 409, "unclaimed_workspace",
				"claim the workspace before sending — verify the creator email in workspace settings")
			return
		}
		to := []string{demoReaderEmail}
		if addr := strings.TrimSpace(in.To); addr != "" {
			to = []string{addr}
		}
		emailID, err := s.sendIssueMail(ctx, wdb, reqWS(r).ID, iss, pub, to, "test", s.publicBase(r))
		if apiDBErr(w, r, err, "send") {
			return
		}
		if apiDBErr(w, r, wdb.MarkIssueTestSent(ctx, iss.ID), "issue") {
			return
		}
		if fresh, _ := wdb.GetIssue(ctx, iss.ID); fresh != nil {
			iss = fresh
		}
		apiJSON(w, 200, map[string]any{
			"issue": s.issueSummary(r, iss), "outbox_email_id": emailID, "audience": in.Audience,
			"to": to, "subject": localizeSubject(iss.Subject, pub.Language), "delivery": "outbox",
		})
		return
	}
	kind := "issue"
	var readers []store.Reader
	if in.Audience == "test" {
		kind = "test"
		if addr := strings.TrimSpace(in.To); addr != "" && !strings.EqualFold(addr, owner) {
			apiErr(w, 400, "validation", "test sends go to the claimed address only")
			return
		}
	} else {
		var err error
		readers, err = wdb.ConfirmedReaders(ctx, iss.PublicationID)
		if apiDBErr(w, r, err, "readers") {
			return
		}
	}
	// An explicit Idempotency-Key namespaces the per-recipient keys, so a
	// retried request can't mail the same reader twice.
	idemNS := ""
	if k := strings.TrimSpace(r.Header.Get("Idempotency-Key")); k != "" {
		idemNS = "h/" + k
	}
	sum, err := s.sendIssueLive(ctx, wdb, reqWS(r).ID, iss, pub, kind, owner, s.publicBase(r), idemNS, readers)
	if apiDBErr(w, r, err, "send") {
		return
	}
	if kind == "test" && len(sum.Refused) > 0 {
		apiErr(w, 429, "send_refused", "test send refused: "+strings.Join(sum.Refused, "; "))
		return
	}
	// A test send stamps the test_sent_at marker but never enters the
	// lifecycle — the issue stays approved so it can still go to
	// readers (KLOX-53). A readers send marks it sent only when
	// something left the app: an all-refused, all-fallback or empty
	// send keeps it approved so it can go out once deliverable.
	if kind == "test" {
		if apiDBErr(w, r, wdb.MarkIssueTestSent(ctx, iss.ID), "issue") {
			return
		}
		if fresh, _ := wdb.GetIssue(ctx, iss.ID); fresh != nil {
			iss = fresh
		}
	}
	if kind == "issue" && sum.Sent+sum.Deduped > 0 {
		if apiDBErr(w, r, db(r).SetIssueStatus(r.Context(), iss.ID, store.IssueSent), "issue") {
			return
		}
		if fresh, _ := db(r).GetIssue(r.Context(), iss.ID); fresh != nil {
			iss = fresh
		}
	}
	deliv := "none"
	switch {
	case sum.Sent > 0 && sum.Fallback > 0:
		deliv = "mixed"
	case sum.Sent > 0:
		deliv = "resend"
	case sum.Fallback > 0:
		deliv = "outbox_fallback"
	}
	resp := map[string]any{
		"issue": s.issueSummary(r, iss), "outbox_email_id": sum.FirstID, "audience": in.Audience,
		"to": sum.To, "subject": localizeSubject(iss.Subject, pub.Language), "delivery": deliv,
		"sent": sum.Sent, "refused": sum.Refused, "deduped": sum.Deduped,
	}
	if sum.Fallback > 0 {
		resp["fallback"] = true
	}
	if kind == "issue" && sum.Sent+sum.Deduped == 0 {
		resp["warnings"] = []string{"nothing was delivered — the issue stayed approved and can be sent again"}
	}
	apiJSON(w, 200, resp)
}

// apiHold unschedules a queued issue: the spec's Hold returns a
// scheduled (locked) issue to draft so it can be edited and approved
// again. Hold is a no-op for anything not scheduled.
func (s *Server) apiHold(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status != store.IssueScheduled {
		apiErr(w, 409, "validation", "only a scheduled issue can be held")
		return
	}
	if apiDBErr(w, r, db(r).RevertIssueToDraft(r.Context(), iss.ID), "issue") {
		return
	}
	db(r).RecordDecision(r.Context(), iss.PublicationID, 0, "hold", "scheduled issue held")
	fresh, _ := db(r).GetIssue(r.Context(), iss.ID)
	apiJSON(w, 200, map[string]any{"issue": s.issueSummary(r, fresh)})
}

func (s *Server) apiExport(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	rawPub, loadErr := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	pub, err := s.canonicalPub(rawPub, loadErr)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	if iss.Status != store.IssueApproved && iss.Status != store.IssueSent {
		apiErr(w, 409, "blocked", "exports ship only approved output — approve the issue first",
			apiReason{Code: "not_approved", Message: "issue is not approved",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	aiUsed, _ := db(r).IssueUsedAI(r.Context(), iss.ID)
	md := delivery.ReaderBody(iss, pub, s.publicBase(r), aiUsed)
	md = s.localizeReaderMD(md, rawPub)
	// both spellings ship the same file — ?format= is the spelling the
	// audit reached for; ?fmt= is the documented one (KLOX-56).
	f := r.URL.Query().Get("fmt")
	if f == "" {
		f = r.URL.Query().Get("format")
	}
	switch f {
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=issue-%d.html", iss.Number))
		w.Write([]byte(delivery.RenderHTML(localizeSubject(iss.Subject, rawPub.Language), md, s.publicBase(r))))
	default:
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=issue-%d.md", iss.Number))
		w.Write([]byte(md))
	}
}

// apiPreview renders the reader-facing body with the two preview
// switches: reader (subscriber | non-subscriber) gates paid sections;
// surface (email | web | phone) is presentation-only and echoed back.
func (s *Server) apiPreview(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	rawPub, loadErr := db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID))
	pub, err := s.canonicalPub(rawPub, loadErr)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	reader := r.URL.Query().Get("reader")
	if reader == "" {
		reader = "subscriber"
	}
	surface := r.URL.Query().Get("surface")
	if surface == "" {
		surface = "email"
	}
	if reader != "subscriber" && reader != "non-subscriber" {
		apiErr(w, 400, "validation", "reader must be subscriber | non-subscriber")
		return
	}
	if surface != "email" && surface != "web" && surface != "phone" {
		apiErr(w, 400, "validation", "surface must be email | web | phone")
		return
	}
	aiUsed, _ := db(r).IssueUsedAI(r.Context(), iss.ID)
	md := delivery.ReaderBody(iss, pub, s.publicBase(r), aiUsed)
	gated := false
	if reader == "non-subscriber" {
		md = gateSections(md, paidSectionTitles(iss, pub), pub.Language)
		gated = true
	}
	md = s.localizeReaderMD(md, rawPub)
	apiJSON(w, 200, map[string]any{
		"body_md": md, "gated": gated, "reader": reader, "surface": surface,
	})
}

func (s *Server) apiOutbox(w http.ResponseWriter, r *http.Request) {
	if _, err := s.apiPub(r); apiDBErr(w, r, err, "publication") {
		return
	}
	emails, _ := db(r).OutboxEmails(r.Context())
	db(r).SetProgress(r.Context(), "outbox_seen")
	out := []map[string]any{}
	for _, e := range emails {
		// The publication outbox shows only the newsletter's own mail
		// (KLOX-66 §8): account-level magic-link mail belongs to the
		// workspace claim, never to an edition. Under the
		// one-newsletter rule every remaining kind — issue, test,
		// reader_confirm — is this newsletter's.
		if e.Kind == "magic_link" {
			continue
		}
		out = append(out, map[string]any{
			"id": e.ID, "kind": e.Kind, "to": e.To, "subject": e.Subject,
			"provider": e.Provider, "status": e.Status,
			"created_at": e.CreatedAt,
		})
	}
	apiJSON(w, 200, map[string]any{"emails": out})
}

func (s *Server) apiOutboxEmail(w http.ResponseWriter, r *http.Request) {
	e, err := s.outboxEmail(r)
	if apiDBErr(w, r, err, "email") {
		return
	}
	apiJSON(w, 200, map[string]any{"email": map[string]any{
		"id": e.ID, "kind": e.Kind, "to": e.To, "subject": e.Subject,
		"body_md": e.BodyMD, "body_html": e.BodyHTML,
		"provider": e.Provider, "provider_id": e.ProviderID, "status": e.Status,
		"error":      e.Error,
		"created_at": e.CreatedAt,
	}})
}
