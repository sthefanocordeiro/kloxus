package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// apiDecision applies one triage decision: approve | reject |
// alternative | undo. Same rules as the HTML route.
func (s *Server) apiDecision(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	iid, _ := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	ii, err := db(r).GetIssueItem(r.Context(), iid)
	if apiDBErr(w, r, err, "item") {
		return
	}
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	var in struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	ctx := r.Context()
	pub, _ := s.canonicalPub(db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID)))
	var guide any

	finish := func(iid int64) {
		ii2, err := db(r).GetIssueItem(ctx, iid)
		if err == nil {
			ii = ii2
		}
		resp := map[string]any{
			"item":            s.apiCardFor(r, *ii),
			store.ItemPending: pendingDecisions(r, iss.ID),
		}
		if guide != nil {
			resp["guide"] = guide
		}
		apiJSON(w, 200, resp)
	}

	switch in.Kind {
	case "undo":
		if ii.Status != store.ItemRejected {
			apiErr(w, 409, "not_rejected", "undo applies only to a rejected item")
			return
		}
		if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, iid, store.ItemPending), "item") {
			return
		}
		db(r).RecordDecision(ctx, iss.PublicationID, iid, "undo", "")
		pipeline.RecordFeedback(ctx, db(r), iss.PublicationID, iid, store.FeedbackUndoReject)
		// an undone rejection must leave the editorial guide too
		if pub != nil {
			guide = s.guideAfterRebuild(w, r, pub)
		}
		finish(iid)
		return
	case "approve":
		if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, iid, store.ItemApproved), "item") {
			return
		}
		db(r).RecordDecision(ctx, iss.PublicationID, iid, "approve", in.Reason)
		pipeline.RecordFeedback(ctx, db(r), iss.PublicationID, iid, store.FeedbackAccept)
	case "reject":
		if strings.TrimSpace(in.Reason) == "" {
			apiErr(w, 400, "reason_required",
				"a rejection needs a reason — it feeds the editorial guide",
				apiReason{Code: "reason_required", Message: "provide a rejection reason",
					Target: &apiTarget{Kind: "item", Ref: fmt.Sprint(iid)}})
			return
		}
		if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, iid, store.ItemRejected), "item") {
			return
		}
		db(r).RecordDecision(ctx, iss.PublicationID, iid, "reject", in.Reason)
		pipeline.RecordFeedback(ctx, db(r), iss.PublicationID, iid, store.FeedbackReject)
		if pub != nil {
			guide = s.guideAfterRebuild(w, r, pub)
		}
	case "alternative":
		if pub == nil {
			apiErr(w, 500, "internal", "internal error")
			return
		}
		runner := &pipeline.Runner{
			DB: db(r), LLM: s.d.LLM, Fetcher: s.d.Fetcher,
			TriageModel: s.d.Cfg.ModelTriage, DraftModel: s.d.Cfg.ModelDraft,
			PaceStep: s.stepPacer(), Deterministic: s.d.Cfg.LLMOff(),
		}
		newII, err := runner.SubstituteAlternative(ctx, pub, iss, ii)
		if err == sql.ErrNoRows {
			apiErr(w, 409, "no_alternative", "there is no alternative candidate for this item")
			return
		}
		if apiDBErr(w, r, err, "item") {
			return
		}
		db(r).RecordDecision(ctx, iss.PublicationID, iid, "alternative",
			fmt.Sprintf("replaced by item %d (score %.1f)", newII.ID, newII.Score))
		ii = newII
	default:
		apiErr(w, 400, "validation", "kind must be approve|reject|alternative|undo")
		return
	}
	if iss.Status == store.IssueApproved {
		// a decision after approval returns the issue to draft
		db(r).RevertIssueToDraft(ctx, iss.ID)
	}
	finish(ii.ID)
}
