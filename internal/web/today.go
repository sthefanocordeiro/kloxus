package web

// The Hoje screen (KLOX-123 slice 2, pivot Block 2): today's pauta for
// the radar — stage-1 ranked, MMR-spread, zero model calls — plus the
// pieces in progress and the "Escrever" entry that turns a story into a
// Peça (or adds it to the open one). Training continues here through
// the same Sim/Não answer endpoint the calibration uses.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const (
	todayCards = 12
	// Hoje prefers the last 48 h like the calibration; older stories
	// only fill the list when the radar is quiet.
	todayWindow = pipeline.CalibrationWindow
)

// pieceStatus is the creator-facing state of a Peça. Exports (a copied
// or downloaded rendition, KLOX-184) override it in apiToday; until
// then only rascunho and revisada exist, and a sent legacy issue reads
// as exportada.
func pieceStatus(status string) string {
	switch status {
	case "approved":
		return "reviewed"
	case "sent", "scheduled":
		return "exported"
	default:
		return "draft"
	}
}

type pieceView struct {
	ID     int64  `json:"id"`
	Number int    `json:"number"`
	Status string `json:"status"` // draft | reviewed | exported
	// KLOX-184: the list names the pauta by its content — first story
	// title, opening line or subject — with the day it was built and
	// the formats the author already took out.
	Title           string   `json:"title"`
	CreatedAt       string   `json:"created_at"`
	ExportedFormats []string `json:"exported_formats,omitempty"`
}

// mustItems loads an issue's items for the title lookup — a failed
// read just falls back to the opening/subject, never breaks the list.
func mustItems(r *http.Request, issueID int64) []store.IssueItem {
	items, _ := db(r).IssueItems(r.Context(), issueID)
	return items
}

// openPiece is the newest Peça still being written, nil when none.
func openPiece(issues []*store.Issue) *store.Issue {
	for _, iss := range issues { // newest first
		if pieceStatus(iss.Status) == "draft" {
			return iss
		}
	}
	return nil
}

// apiToday serves the day's pauta and the pieces in progress. When the
// daily sweep stored today's pauta (06:30 America/Sao_Paulo) the cards
// come from that snapshot; otherwise the stories are rebuilt from what
// the radar's sources hold — and that first live build becomes the
// day's snapshot, so repeat GETs are stable and fast and the sweep
// counts the radar as done. Interactions that change the ranking drop
// the row and the next GET snapshots again (the client still refetches
// on a 404 for ids dealt before a rebuild).
func (s *Server) apiToday(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pub, err := db(r).GetPublication(ctx, r.PathValue("id"))
	if apiDBErr(w, r, err, "radar") {
		return
	}
	var views []pautaCardView
	if row, err := db(r).PautaDay(ctx, pub.ID, pautaDayOf(time.Now())); err == nil {
		views = []pautaCardView{}
		if err := json.Unmarshal([]byte(row.CardsJSON), &views); err != nil {
			views = nil // corrupt snapshot — rebuild live
		}
	}
	if views == nil {
		run := s.pautaRunner(r)
		stories, err := run.BuildStories(ctx, pub)
		if apiDBErr(w, r, err, "stories") {
			return
		}
		views = pautaViews(run.Pauta(ctx, pub, stories, todayCards, todayWindow))
		if cards, err := json.Marshal(views); err == nil {
			_ = db(r).SavePautaDay(ctx, pub.ID, pautaDayOf(time.Now()), string(cards))
		}
	}
	issues, err := db(r).ListIssues(r.Context(), pub.ID)
	if apiDBErr(w, r, err, "pieces") {
		return
	}
	ids := make([]int64, 0, len(issues))
	for _, iss := range issues {
		ids = append(ids, iss.ID)
	}
	exports, err := db(r).IssueExportsFor(r.Context(), ids)
	if apiDBErr(w, r, err, "pieces") {
		return
	}
	pieces := []pieceView{}
	for _, iss := range issues {
		formats := exports[iss.ID]
		st := pieceStatus(iss.Status)
		if len(formats) > 0 {
			st = "exported"
		}
		pieces = append(pieces, pieceView{
			ID: iss.ID, Number: iss.Number, Status: st,
			Title:     s.pautaTitle(r, iss, pub, mustItems(r, iss.ID)),
			CreatedAt: iss.CreatedAt, ExportedFormats: formats,
		})
	}
	open := int64(0)
	if o := openPiece(issues); o != nil {
		open = o.ID
	}
	apiJSON(w, 200, map[string]any{
		"cards": views, "pieces": pieces, "open_piece": open,
		"window_hours": int(todayWindow / time.Hour),
	})
}

// apiTodayWrite is "Escrever": the story becomes a Peça. With no open
// Peça a new one is created with the story as its first pauta and
// composed into empty, source-cited slots (deterministic, no model).
// With an open Peça the story joins it — directly while nothing is
// written yet, through the editor's block insert once writing began
// (the response says so with add_block).
func (s *Server) apiTodayWrite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pub, err := db(r).GetPublication(ctx, r.PathValue("id"))
	if apiDBErr(w, r, err, "radar") {
		return
	}
	var in struct {
		StoryID int64 `json:"story_id"`
	}
	if _, err := apiDecode(r, &in); err != nil || in.StoryID == 0 {
		apiErr(w, 400, "validation", "story_id is required")
		return
	}
	story, err := db(r).StoryWithItems(ctx, in.StoryID)
	if err != nil || story.PublicationID != pub.ID {
		// the deck was rebuilt since the cards were served
		apiErr(w, 404, "not_found", "story not found — reload today's pauta")
		return
	}
	dropPautaDay(r, db(r), pub.ID) // a story left today's pauta
	issues, err := db(r).ListIssues(ctx, pub.ID)
	if apiDBErr(w, r, err, "pieces") {
		return
	}
	section := ""
	iss := openPiece(issues)
	created := false
	if iss == nil {
		number, err := db(r).NextIssueNumber(ctx, pub.ID)
		if apiDBErr(w, r, err, "piece") {
			return
		}
		id, err := db(r).CreateIssue(ctx, pub.ID, number,
			fmt.Sprintf("%s — pauta %d", pub.Name, number), "", config.PostSections())
		if apiDBErr(w, r, err, "piece") {
			return
		}
		if iss, err = db(r).GetIssue(ctx, id); apiDBErr(w, r, err, "piece") {
			return
		}
		created = true
	}
	secs := issueSections(iss, pub)
	if len(secs) == 0 {
		apiErr(w, 409, "blocked", "the radar has no section to hold the pauta")
		return
	}
	section = secs[0].Title

	written := 0
	if !created {
		doc, err := buildIssueDocument(ctx, db(r), iss, pub)
		if apiDBErr(w, r, err, "document") {
			return
		}
		for _, sec := range doc.Sections {
			for _, it := range sec.Items {
				written += len(it.Sentences)
			}
		}
	}
	if written > 0 {
		// writing began: the editor's block insert owns the placement
		apiJSON(w, 200, map[string]any{"issue_id": iss.ID, "add_block": true, "section": section})
		return
	}

	if old, err := db(r).IssueItemByStory(ctx, iss.ID, in.StoryID); err == nil {
		if apiDBErr(w, r, db(r).SetIssueItemPlacement(ctx, old.ID, section, old.Position, "approved"), "item") {
			return
		}
	} else if _, err := db(r).CreateIssueItem(ctx, store.IssueItem{
		IssueID: iss.ID, StoryID: in.StoryID, Section: section,
		Score: story.TriageScore, Reason: story.TriageReason,
		Status: "approved", Origin: "manual", Position: nextPosition(r, iss.ID, section),
	}); apiDBErr(w, r, err, "item") {
		return
	}
	runner := &pipeline.Runner{DB: db(r)}
	if _, err := runner.ComposeSkeleton(ctx, pubWithIssueSections(iss, pub), iss.ID); err != nil {
		apiErr(w, 409, "blocked", err.Error())
		return
	}
	db(r).RecordDecision(ctx, pub.ID, 0, "manual_compose", "pauta de hoje became a piece for manual writing")
	apiJSON(w, 200, map[string]any{"issue_id": iss.ID, "created": created})
}
