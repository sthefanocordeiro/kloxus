package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// --- issues / items / dossier ---

func (s *Server) apiIssues(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	issues, err := db(r).ListIssues(r.Context(), pub.ID)
	if apiDBErr(w, r, err, "issues") {
		return
	}
	out := []map[string]any{}
	for _, iss := range issues {
		out = append(out, s.issueSummary(r, iss))
	}
	apiJSON(w, 200, map[string]any{"issues": out})
}

func (s *Server) apiIssue(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	apiJSON(w, 200, map[string]any{"issue": s.issueSummary(r, iss)})
}

func (s *Server) apiIssueItems(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	items, _ := db(r).IssueItems(r.Context(), iss.ID)
	out := []apiItem{}
	for _, ii := range items {
		out = append(out, s.apiCardFor(r, ii))
	}
	apiJSON(w, 200, map[string]any{
		"items": out, "pending": pendingDecisions(r, iss.ID),
	})
}

// apiCandidate is one ranked story on the build board: in the issue or
// still on the bench.
type apiCandidate struct {
	StoryID          int64    `json:"story_id"`
	ItemID           int64    `json:"item_id,omitempty"`
	Title            string   `json:"title"`
	Outlets          []string `json:"outlets"`
	Sources          int      `json:"sources"`
	Duplicates       int      `json:"duplicates"`
	Score            float64  `json:"score"`
	Reason           string   `json:"reason"`
	Rank             int      `json:"rank"`
	InIssue          bool     `json:"in_issue"`
	Section          string   `json:"section,omitempty"`
	SuggestedSection string   `json:"suggested_section,omitempty"`
	Status           string   `json:"status,omitempty"`
	Origin           string   `json:"origin,omitempty"`
	Position         int      `json:"position"`
	Dossier          bool     `json:"dossier"`
	Available        bool     `json:"available"`
}

// demoCandidateCap is how many ranked stories a demo workspace can
// actually draft — replay fixtures only exist for the top of the list.
const demoCandidateCap = 14

func (s *Server) apiCandidates(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	cands, err := db(r).RankedCandidates(r.Context(), iss.PublicationID, iss.ID)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	out := s.candidateViews(r, cands)
	apiJSON(w, 200, map[string]any{
		"candidates": out,
		"sections":   sectionViews(pub),
		"demo":       s.d.Cfg.Mode == "demo",
	})
}

func (s *Server) candidateViews(r *http.Request, cands []store.RankedCandidate) []apiCandidate {
	demo := s.d.Cfg.Mode == "demo"
	out := []apiCandidate{}
	for _, c := range cands {
		story, err := db(r).StoryWithItems(r.Context(), c.Story.ID)
		outlets := []string{}
		sources := 0
		if err == nil {
			seen := map[string]bool{}
			for _, it := range story.Items {
				n := it.SourceName
				if n == "" {
					n = it.SourceURL
				}
				if !seen[n] {
					seen[n] = true
					outlets = append(outlets, n)
				}
			}
			sources = len(story.Items)
		}
		cv := apiCandidate{
			StoryID: c.Story.ID, Title: c.Story.Title,
			Outlets: outlets, Sources: sources, Duplicates: c.Duplicates,
			Score: c.Story.TriageScore, Reason: c.Story.TriageReason, Rank: c.Rank,
			Dossier: c.Dossier, Available: true,
		}
		if c.Item != nil {
			cv.ItemID = c.Item.ID
			cv.Section = c.Item.Section
			cv.Status = c.Item.Status
			cv.Origin = c.Item.Origin
			cv.Position = c.Item.Position
			cv.InIssue = c.Item.Status == "pending" || c.Item.Status == "approved"
			// any prior placement is a valid suggestion: a rejected or
			// alternative story re-adds where it once sat
			cv.SuggestedSection = c.Item.Section
		}
		if demo && cv.Origin != "phaseA" && c.Rank > demoCandidateCap {
			cv.Available = false
		}
		out = append(out, cv)
	}
	return out
}

func sectionViews(pub *store.Publication) []map[string]any {
	out := []map[string]any{}
	for _, sec := range pub.Sections {
		out = append(out, map[string]any{
			"title": sec.Title, "capacity": sec.Items, "commentary": sec.Commentary,
		})
	}
	return out
}

// apiLineup replaces the issue lineup: which stories sit in which
// section, in which slot order. Items dropped from the lineup are
// 'removed' — not rejected — unless the caller uses the reject
// decision with a reason.
func (s *Server) apiLineup(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	if iss.Status == "sent" || iss.Status == "scheduled" {
		apiErr(w, 409, "read_only", "this issue is sent or scheduled — locked issues are read-only")
		return
	}
	var in struct {
		Entries []struct {
			StoryID int64  `json:"story_id"`
			Section string `json:"section"`
		} `json:"entries"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	ctx := r.Context()
	pub, err := s.canonicalPub(db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	caps := map[string]int{}
	for _, sec := range pub.Sections {
		caps[sec.Title] = sec.Items
	}
	cands, err := db(r).RankedCandidates(ctx, iss.PublicationID, iss.ID)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	valid := map[int64]*store.RankedCandidate{}
	for i := range cands {
		valid[cands[i].Story.ID] = &cands[i]
	}
	counts := map[string]int{}
	demo := s.d.Cfg.Mode == "demo"
	seen := map[int64]bool{}
	for _, e := range in.Entries {
		c, ok := valid[e.StoryID]
		if !ok {
			apiErr(w, 400, "unknown_story", fmt.Sprintf("story %d is not a ranked candidate of this run", e.StoryID))
			return
		}
		if seen[e.StoryID] {
			apiErr(w, 400, "duplicate_story", fmt.Sprintf("story %d appears twice in the lineup", e.StoryID))
			return
		}
		seen[e.StoryID] = true
		cap_, ok := caps[e.Section]
		if !ok {
			apiErr(w, 400, "unknown_section", fmt.Sprintf("section %q is not configured for this publication", e.Section))
			return
		}
		counts[e.Section]++
		if counts[e.Section] > cap_ {
			apiErr(w, 409, "section_full",
				fmt.Sprintf("section %q holds %d item(s) — the lineup asks for %d", e.Section, cap_, counts[e.Section]),
				apiReason{Code: "section_full",
					Message: fmt.Sprintf("%s is full (%d/%d)", e.Section, cap_, cap_),
					Target:  &apiTarget{Kind: "section", Ref: e.Section}})
			return
		}
		if demo && c.Rank > demoCandidateCap && (c.Item == nil || c.Item.Origin != "phaseA") {
			apiErr(w, 409, "demo_unavailable",
				"this story is not available in the demo — only the top-ranked stories can be added",
				apiReason{Code: "demo_unavailable",
					Message: "not available in the demo",
					Target:  &apiTarget{Kind: "story", Ref: fmt.Sprint(e.StoryID)}})
			return
		}
	}

	// apply: place every entry, then remove what fell out of the lineup
	reIncluded := false
	positions := map[string]int{}
	for _, e := range in.Entries {
		positions[e.Section]++
		pos := positions[e.Section] - 1
		if ii, err := db(r).IssueItemByStory(ctx, iss.ID, e.StoryID); err == nil {
			if ii.Status == "rejected" {
				reIncluded = true
			}
			if apiDBErr(w, r, db(r).SetIssueItemPlacement(ctx, ii.ID, e.Section, pos, "approved"), "item") {
				return
			}
		} else {
			c := valid[e.StoryID]
			if _, err := db(r).CreateIssueItem(ctx, store.IssueItem{
				IssueID: iss.ID, StoryID: e.StoryID, Section: e.Section,
				Score: c.Story.TriageScore, Reason: c.Story.TriageReason,
				Status: "approved", Origin: "manual", Position: pos,
			}); apiDBErr(w, r, err, "item") {
				return
			}
		}
	}
	items, _ := db(r).IssueItems(ctx, iss.ID)
	for _, ii := range items {
		if (ii.Status == "pending" || ii.Status == "approved") && !seen[ii.StoryID] {
			if apiDBErr(w, r, db(r).SetIssueItemStatus(ctx, ii.ID, "removed"), "item") {
				return
			}
		}
	}

	// re-including a rejected story undoes the rejection: the guide
	// must forget the rule it learned from it, same as a decision undo
	var guide any
	if reIncluded {
		guide = s.guideAfterRebuild(w, r, pub)
	}
	cands, _ = db(r).RankedCandidates(ctx, iss.PublicationID, iss.ID)
	resp := map[string]any{"candidates": s.candidateViews(r, cands)}
	if guide != nil {
		resp["guide"] = guide
	}
	apiJSON(w, 200, resp)
}

// apiStory serves a ranked story's member articles for the board's
// side sheet when no dossier exists yet.
func (s *Server) apiStory(w http.ResponseWriter, r *http.Request) {
	sid, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	story, err := db(r).StoryWithItems(r.Context(), sid)
	if apiDBErr(w, r, err, "story") {
		return
	}
	outlets := []string{}
	seen := map[string]bool{}
	items := []map[string]any{}
	for _, it := range story.Items {
		n := it.SourceName
		if n == "" {
			n = it.SourceURL
		}
		if !seen[n] {
			seen[n] = true
			outlets = append(outlets, n)
		}
		items = append(items, map[string]any{
			"id": it.ID, "title": it.Title, "url": "/a/" + fmt.Sprint(it.ID),
			"outlet": n, "published_at": it.PublishedAt,
			"snippet": truncateStr(it.ContentText, 400),
		})
	}
	apiJSON(w, 200, map[string]any{
		"story": map[string]any{
			"id": story.ID, "title": story.Title,
			"score": story.TriageScore, "reason": story.TriageReason,
			"outlets": outlets, "items": items,
		},
	})
}

func (s *Server) apiDossier(w http.ResponseWriter, r *http.Request) {
	iid, _ := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	ii, err := db(r).GetIssueItem(r.Context(), iid)
	if apiDBErr(w, r, err, "item") {
		return
	}
	dj, err := db(r).Dossier(r.Context(), iid)
	if apiDBErr(w, r, err, "dossier") {
		return
	}
	var dd pipeline.Dossier
	if err := json.Unmarshal([]byte(dj), &dd); err != nil {
		apiErr(w, 500, "internal", "dossier is unreadable")
		return
	}
	dd.Facts = orEmpty(dd.Facts)
	dd.DeepFacts = orEmpty(dd.DeepFacts)
	dd.Unknowns = orEmpty(dd.Unknowns)
	dd.Interpretation = orEmpty(dd.Interpretation)
	dd.Sources = orEmpty(dd.Sources)
	db(r).SetProgress(r.Context(), "dossier_seen")
	apiJSON(w, 200, map[string]any{
		"item":    s.apiCardFor(r, *ii),
		"dossier": dd,
	})
}

// apiDecision applies one triage decision: approve | reject |
// alternative | undo. Same rules as the HTML route.
