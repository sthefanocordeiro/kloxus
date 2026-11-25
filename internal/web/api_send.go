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

// --- approve / send / export / preview ---

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
	if iss.Status != "approved" {
		if err := db(r).SetIssueStatus(ctx, iss.ID, "approved"); err != nil {
			return nil, err
		}
		iss.Status = "approved" // the receipt attests to the approved document
		db(r).RecordDecision(ctx, iss.PublicationID, 0, "approve", "issue approved")
	}
	rec, err := db(r).ApprovalReceipt(ctx, iss.ID)
	if err != sql.ErrNoRows {
		return rec, err
	}
	if checks == nil {
		checks = s.readinessChecks(r, iss, pub, nil)
	}
	cj, _ := json.Marshal(checks)
	rec = &store.ApprovalReceipt{
		IssueID: iss.ID, Number: iss.Number,
		DocumentVersion: docVersion(iss),
		Approver:        reqWS(r).Label,
		ChecksJSON:      string(cj),
	}
	if err := db(r).SaveApprovalReceipt(ctx, rec); err != nil {
		return nil, err
	}
	return db(r).ApprovalReceipt(ctx, iss.ID)
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
	if iss.Status != "approved" {
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
	emailID, err := s.sendIssueMail(r, iss, pub, to, kind)
	if apiDBErr(w, r, err, "send") {
		return
	}
	if kind == "issue" {
		// a test send only records the outbox email — the issue stays
		// approved so it can still go to readers
		if apiDBErr(w, r, db(r).SetIssueStatus(r.Context(), iss.ID, "sent"), "issue") {
			return
		}
		if fresh, _ := db(r).GetIssue(r.Context(), iss.ID); fresh != nil {
			iss = fresh
		}
	}
	apiJSON(w, 200, map[string]any{
		"issue": s.issueSummary(r, iss), "outbox_email_id": emailID, "audience": in.Audience,
		"to": to, "subject": iss.Subject,
	})
}

// apiHold unschedules a queued issue: the spec's Hold returns a
// scheduled (locked) issue to draft so it can be edited and approved
// again. Hold is a no-op for anything not scheduled.
func (s *Server) apiHold(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status != "scheduled" {
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
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	if iss.Status != "approved" && iss.Status != "sent" {
		apiErr(w, 409, "blocked", "exports ship only approved output — approve the issue first",
			apiReason{Code: "not_approved", Message: "issue is not approved",
				Target: &apiTarget{Kind: "issue", Ref: fmt.Sprint(iss.ID)}})
		return
	}
	md := delivery.ReaderBody(iss, pub, requestBase(r))
	switch r.URL.Query().Get("fmt") {
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=issue-%d.html", iss.Number))
		w.Write([]byte(delivery.RenderHTML(iss.Subject, md, requestBase(r))))
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
	md := delivery.ReaderBody(iss, pub, requestBase(r))
	gated := false
	if reader == "non-subscriber" {
		md = gateSections(md, pub.Paywall.PaidSections, pub.Language)
		gated = true
	}
	md = s.localizeReaderMD(md, rawPub)
	apiJSON(w, 200, map[string]any{
		"body_md": md, "gated": gated, "reader": reader, "surface": surface,
	})
}

// --- outbox ---

func (s *Server) apiOutbox(w http.ResponseWriter, r *http.Request) {
	if _, err := s.apiPub(r); apiDBErr(w, r, err, "publication") {
		return
	}
	emails, _ := db(r).OutboxEmails(r.Context())
	db(r).SetProgress(r.Context(), "outbox_seen")
	out := []map[string]any{}
	for _, e := range emails {
		out = append(out, map[string]any{
			"id": e.ID, "kind": e.Kind, "to": e.To, "subject": e.Subject,
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
		"body_md": e.BodyMD, "body_html": e.BodyHTML, "created_at": e.CreatedAt,
	}})
}
