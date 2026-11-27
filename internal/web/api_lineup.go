package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/strutil"
)

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
	pending := 0
	for _, ii := range items {
		if ii.Status == store.ItemPending {
			pending++
		}
		out = append(out, s.apiCardFor(r, ii))
	}
	apiJSON(w, 200, map[string]any{
		"items": out, store.ItemPending: pending,
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
	// Summary is the one-line AI summary the story card shows (KLOX-67
	// §7.2); empty when the item pool has no enrichment yet.
	Summary string `json:"summary,omitempty"`
	// Kind/Language/Topics/PublishedAt are the enrichment card fields
	// (KLOX-95): article type slug, detected language, topic tags and
	// the newest member's publish time. Empty until enrichment lands —
	// the card falls back to title/outlet.
	Kind        string   `json:"kind,omitempty"`
	Language    string   `json:"language,omitempty"`
	Topics      []string `json:"topics,omitempty"`
	PublishedAt string   `json:"published_at,omitempty"`
	// Foreign and OffTopic mark the suggestions the board hides by
	// default (KLOX-95): a language different from the publication's,
	// or classified topics that never match the declared topic or
	// editorial line. ?all=1 serves them back; hidden_bench keeps the
	// count honest.
	Foreign  bool `json:"foreign,omitempty"`
	OffTopic bool `json:"off_topic,omitempty"`
	// AIDraft marks a lineup card whose pauta body arrived AI-written
	// and has not been edited by the author yet.
	AIDraft bool `json:"ai_draft,omitempty"`
	// RejectedEditorial marks a bench story hitting the newsletter's
	// reject terms: demoted to the bench tail, never auto-slotted and
	// never silently dropped — the card shows the policy (KLOX-94).
	RejectedEditorial bool `json:"rejected_editorial,omitempty"`
}

// demoCandidateCap is how many ranked stories a demo workspace can
// actually draft — replay fixtures only exist for the top of the list.
const demoCandidateCap = 14

// candidateCap bounds the ranked pool the board can see (KLOX-45):
// the bench is paginated through the top candidates only.
const candidateCap = 150

// candidatePageSize is the bench page the board renders — it is also
// the API's default and maximum page size.
const candidatePageSize = 50

func (s *Server) apiCandidates(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	pub, err := s.canonicalPub(db(r).GetPublication(r.Context(), fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if size <= 0 || size > candidatePageSize {
		size = candidatePageSize
	}
	showAll := showFiltered(r)
	cands, err := db(r).RankedCandidates(r.Context(), iss.PublicationID, iss.ID)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	secs := issueSections(iss, pub)
	views, meta, err := s.candidatePage(r, cands, page, size, pub, secs, showAll)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	resp := map[string]any{
		"candidates":       views,
		"sections":         sectionViews(secs),
		"sections_changed": sectionsChanged(iss, pub),
		"demo":             s.d.Cfg.Mode == "demo",
		"llm_mode":         s.d.Cfg.LLMMode,
	}
	maps.Copy(resp, meta)
	apiJSON(w, 200, resp)
}

// showFiltered reports whether the request asked for the full bench —
// ?all=1|true serves the suggestions the default view filters out
// (foreign-language / off-topic, KLOX-95).
func showFiltered(r *http.Request) bool {
	v := r.URL.Query().Get("all")
	return v == "1" || v == "true"
}

// langPrefix reduces a language tag to its two-letter head so "pt-BR"
// and "pt" compare equal.
func langPrefix(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_"); i >= 0 {
		s = s[:i]
	}
	return s
}

// cardFlags derives the two default-hidden traits (KLOX-95) from the
// story's enrichment: a member language different from the
// publication's, and a classified topic set that never intersects the
// declared topic or editorial line. A story without enrichment — or
// without classifiable topics — stays visible: absent data is never a
// reason to hide a suggestion.
//
// KLOX-115: the on-topic predicate is the scorer's own source of truth
// (P1-9) — the tag match reads the editorial line minus its skip
// clauses, and a title satisfying the topic's phrase alternatives
// clears off_topic exactly like the run's "no tema" reason.
func cardFlags(pub *store.Publication, card *store.StoryCard, title string) (foreign, offTopic bool) {
	if pub == nil || card == nil {
		return false, false
	}
	if pl, il := langPrefix(pub.Language), langPrefix(card.Language); pl != "" && il != "" && pl != il {
		foreign = true
	}
	if len(card.Topics) > 0 {
		pos := pipeline.PositiveEditorialText(pub)
		tokens := pipeline.FoldedTokens(pos)
		if len(tokens) > 0 {
			offTopic = true
			for _, tag := range card.Topics {
				if pipeline.TopicMatches(tag, tokens, pos) {
					offTopic = false
					break
				}
			}
			if offTopic && pipeline.ThemeTitleHit(pub, title) {
				offTopic = false
			}
		}
	}
	return foreign, offTopic
}

// cardRejectText is the text the bench's reject check reads — the
// story title plus its enrichment tags, mirroring the scorer's search
// text so a tag-only hit ("GTA 6" → games) marks the same story the
// score demoted (KLOX-115).
func cardRejectText(title string, card *store.StoryCard) string {
	if card == nil || len(card.Topics) == 0 {
		return title
	}
	return title + " " + strings.Join(card.Topics, " ")
}

// candidatePage cuts the ranked pool to the board's window: every
// in-issue story plus the top candidateCap ranked stories, paged by the
// bench only — in-issue rows ride along on every page so the lineup
// column never loses a card. Bench candidates keep their global rank.
// Unless showAll, the bench drops foreign-language and off-topic
// suggestions; hidden_bench reports how many the toggle conceals.
func (s *Server) candidatePage(r *http.Request, cands []store.RankedCandidate, page, size int, pub *store.Publication, secs []config.Section, showAll bool) ([]apiCandidate, map[string]any, error) {
	pool := cands
	if len(pool) > candidateCap {
		pool = pool[:candidateCap]
	}
	inIssue, bench := []store.RankedCandidate{}, []store.RankedCandidate{}
	for _, c := range pool {
		switch {
		case c.Item != nil && (c.Item.Status == store.ItemPending || c.Item.Status == store.ItemApproved):
			inIssue = append(inIssue, c)
		case c.Item != nil && c.Item.Status == store.ItemRejected:
			// A reasoned rejection is an editorial decision, not a
			// bench slot — the story must not resurface as a
			// suggestion (KLOX-55). Undo restores it to the lineup.
		default:
			bench = append(bench, c)
		}
	}
	// An in-issue story ranked beyond the cap must still render on the
	// board — the bench is bounded, the lineup is not.
	if len(cands) > candidateCap {
		for _, c := range cands[candidateCap:] {
			if c.Item != nil && (c.Item.Status == store.ItemPending || c.Item.Status == store.ItemApproved) {
				inIssue = append(inIssue, c)
			}
		}
	}
	// KLOX-95: the hidden set (foreign-language / off-topic) is computed
	// on the full bench, so hidden_bench stays honest in both ?all
	// states — the default view drops those suggestions, ?all=1 serves
	// them back with their markers.
	ids := make([]int64, 0, len(inIssue)+len(bench))
	for _, c := range inIssue {
		ids = append(ids, c.Story.ID)
	}
	for _, c := range bench {
		ids = append(ids, c.Story.ID)
	}
	cards, err := db(r).StoryCards(r.Context(), ids)
	if err != nil {
		return nil, nil, err
	}
	// KLOX-94: reject hits sink to the bench tail — marked, never
	// silently dropped (rejectedEditorial flags them in the views). The
	// partition is stable, so rank order survives inside each half.
	// KLOX-115 widens the list to the editorial line's skip clauses and
	// matches the tags too, so a clean title carrying a banned subject
	// tag ("GTA 6" → games) sinks the same way the score did.
	rejects := func(c store.RankedCandidate) bool {
		return pub != nil && pipeline.RejectMatch(pub, cardRejectText(c.Story.Title, cards[c.Story.ID]))
	}
	if pub != nil {
		sort.SliceStable(bench, func(a, b int) bool {
			return !rejects(bench[a]) && rejects(bench[b])
		})
	}
	hidden := map[int64]bool{}
	for _, c := range bench {
		foreign, offTopic := cardFlags(pub, cards[c.Story.ID], c.Story.Title)
		// KLOX-109: the stage-1 verdict (relevance floor / English in a
		// Portuguese publication) hides through the same filter.
		// KLOX-121: skip-list subjects (games, promoções, …) are "fora
		// do tema" by default — hidden, shown only under ?all=1.
		if c.Hidden != "" || foreign || offTopic || rejects(c) {
			hidden[c.Story.ID] = true
		}
	}
	hiddenN := len(hidden)
	if !showAll {
		kept := bench[:0]
		for _, c := range bench {
			if !hidden[c.Story.ID] {
				kept = append(kept, c)
			}
		}
		bench = kept
	}
	benchTotal := len(bench)
	if from := page * size; from < benchTotal {
		to := from + size
		if to > benchTotal {
			to = benchTotal
		}
		bench = bench[from:to]
	} else {
		bench = []store.RankedCandidate{}
	}
	visible := make([]store.RankedCandidate, 0, len(inIssue)+len(bench))
	visible = append(visible, inIssue...)
	visible = append(visible, bench...)
	sort.Slice(visible, func(a, b int) bool { return visible[a].Rank < visible[b].Rank })
	views, err := s.candidateViews(r, visible, pub, secs, cards)
	if err != nil {
		return nil, nil, err
	}
	return views, map[string]any{
		"page": page, "page_size": size,
		"bench_total": benchTotal, "has_more": (page+1)*size < benchTotal,
		"hidden_bench": hiddenN,
	}, nil
}

func (s *Server) candidateViews(r *http.Request, cands []store.RankedCandidate, pub *store.Publication, secs []config.Section, cards map[int64]*store.StoryCard) ([]apiCandidate, error) {
	demo := s.d.Cfg.Mode == "demo"
	ids := make([]int64, len(cands))
	for i, c := range cands {
		ids[i] = c.Story.ID
	}
	byStory, err := db(r).StoryOutlets(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	out := []apiCandidate{}
	for _, c := range cands {
		outlets := []string{}
		seen := map[string]bool{}
		for _, o := range byStory[c.Story.ID] {
			n := o.Name
			if n == "" {
				n = o.URL
			}
			if !seen[n] {
				seen[n] = true
				outlets = append(outlets, n)
			}
		}
		cv := apiCandidate{
			StoryID: c.Story.ID, Title: c.Story.Title,
			Outlets: outlets, Sources: len(byStory[c.Story.ID]), Duplicates: c.Duplicates,
			Score: c.Story.TriageScore, Reason: c.Story.TriageReason, Rank: c.Rank,
			Dossier: c.Dossier, Available: true,
		}
		if card := cards[c.Story.ID]; card != nil {
			cv.Summary = card.Summary
			cv.Kind = card.Kind
			cv.Language = card.Language
			cv.Topics = card.Topics
			cv.PublishedAt = card.PublishedAt
			cv.Foreign, cv.OffTopic = cardFlags(pub, card, c.Story.Title)
		}
		switch c.Hidden {
		case "off_topic":
			cv.OffTopic = true
		case "english":
			cv.Foreign = true
		}
		if c.Item != nil {
			cv.ItemID = c.Item.ID
			cv.Section = c.Item.Section
			cv.Status = c.Item.Status
			cv.Origin = c.Item.Origin
			cv.Position = c.Item.Position
			cv.AIDraft = c.Item.AIDraft
			cv.InIssue = c.Item.Status == store.ItemPending || c.Item.Status == store.ItemApproved
			// any prior placement is a valid suggestion: a rejected or
			// alternative story re-adds where it once sat
			cv.SuggestedSection = c.Item.Section
		} else {
			// A bench story hitting the reject policy carries the
			// marker and gets no section suggestion — the policy is
			// visible, never silent (KLOX-94; KLOX-115 adds the
			// editorial line's skip clauses and the tag-aware text).
			if pub != nil &&
				pipeline.RejectMatch(pub, cardRejectText(c.Story.Title, cards[c.Story.ID])) {
				cv.RejectedEditorial = true
				if strings.HasPrefix(strings.ToLower(pub.Language), "pt") {
					cv.Reason += " · rejeitada pela linha editorial"
				} else {
					cv.Reason += " · rejected by the editorial line"
				}
			} else if sug := pipeline.SuggestSection(c.Story.Title, secs); sug != "" {
				// a bench story with no prior placement suggests the
				// section whose declared topic it best fits (KLOX-94)
				cv.SuggestedSection = sug
			}
		}
		if demo && cv.Origin != "phaseA" && c.Rank > demoCandidateCap {
			cv.Available = false
		}
		out = append(out, cv)
	}
	return out, nil
}

// rerankCap is the §7.2 bound on one editorial-rerank call — title +
// one-line summary only, at most 120 suggestions.
const rerankCap = 120

// rerankMaxTokens bounds the rerank reply — a JSON array of positions
// for the capped candidate list.
const rerankMaxTokens = 2048

// apiRerank is the user-triggered "Ordenar pela minha linha editorial"
// (KLOX-67 §7.2): one Haiku call re-orders the ranked pool by the
// publication's editorial line, persisted as new triage scores. Capped
// at ReranksPerDay per newsletter and counted against the workspace's
// paid actions; on cap, kill switch or failure the answer is
// {reranked:false} — suggestions stay on the free ranking, never an
// error page.
func (s *Server) apiRerank(w http.ResponseWriter, r *http.Request) {
	iss, err := s.apiIssueID(r)
	if apiDBErr(w, r, err, "issue") {
		return
	}
	ctx := r.Context()
	pub, err := s.canonicalPub(db(r).GetPublication(ctx, fmt.Sprint(iss.PublicationID)))
	if apiDBErr(w, r, err, "publication") {
		return
	}
	reply := func(ok bool) {
		apiJSON(w, 200, map[string]any{"reranked": ok})
	}
	if s.d.Cfg.Mode == "demo" || s.d.Cfg.LLMOff() || s.d.LLM == nil {
		reply(false)
		return
	}
	if s.d.Cfg.ReranksPerDay > 0 {
		if n, err := db(r).PaidActionCount(ctx, store.PaidActionRerank, pub.ID); err == nil && n >= s.d.Cfg.ReranksPerDay {
			reply(false)
			return
		}
	}
	if s.d.Cfg.PaidActionsPerDay > 0 {
		if n, err := db(r).PaidActionsToday(ctx); err == nil && n >= s.d.Cfg.PaidActionsPerDay {
			reply(false)
			return
		}
	}
	cands, err := db(r).RankedCandidates(ctx, pub.ID, iss.ID)
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	if len(cands) > rerankCap {
		cands = cands[:rerankCap]
	}
	if len(cands) == 0 {
		reply(true)
		return
	}
	var ids []int64
	for _, c := range cands {
		ids = append(ids, c.Story.ID)
	}
	summaries, _ := db(r).StorySummaries(ctx, ids)
	type rrItem struct {
		Ref     int
		StoryID int64
		Title   string
		Summary string
		Reason  string
	}
	var list []rrItem
	for i, c := range cands {
		list = append(list, rrItem{Ref: i + 1, StoryID: c.Story.ID,
			Title: c.Story.Title, Summary: summaries[c.Story.ID],
			Reason: c.Story.TriageReason})
	}
	user, err := pipeline.RenderPrompt("rerank", map[string]any{
		"Publication": pub.Name, "Audience": pub.Audience,
		"EditorialLine": pub.EditorialLine, "Items": list,
	})
	if err != nil {
		reply(false)
		return
	}
	resp, err := s.d.LLM.Complete(ctx, llm.Request{
		Model: s.d.Cfg.ModelTriage, Template: "rerank", User: user, MaxTokens: rerankMaxTokens,
	})
	if err != nil {
		reply(false)
		return
	}
	var order []int
	if err := decodeJSONArr(resp.Text, &order); err != nil {
		reply(false)
		return
	}
	if err := db(r).RecordPaidAction(ctx, store.PaidActionRerank, pub.ID); err != nil {
		slog.Warn("paid-action record failed", "err", err)
	}
	// Persist the model's order as descending triage scores: reranked
	// stories float to the top in the returned sequence; anything the
	// answer dropped keeps its score below them.
	var updates []store.StoryTriage
	top := 12.0
	seen := map[int]bool{}
	for pos, ref := range order {
		if ref < 1 || ref > len(list) || seen[ref] {
			continue
		}
		seen[ref] = true
		updates = append(updates, store.StoryTriage{
			StoryID: list[ref-1].StoryID, Score: top - float64(pos)*0.01,
			Reason: list[ref-1].Reason,
		})
	}
	if len(updates) > 0 {
		if err := db(r).SetStoriesTriage(ctx, updates); err != nil {
			apiDBErr(w, r, err, "rerank")
			return
		}
	}
	reply(len(updates) > 0)
}

// decodeJSONArr tolerates a fenced/explained JSON array answer.
func decodeJSONArr(text string, out *[]int) error {
	s := strings.TrimSpace(text)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), out); err == nil {
		return nil
	}
	start, end := strings.Index(s, "["), strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return fmt.Errorf("no JSON array in answer")
	}
	return json.Unmarshal([]byte(s[start:end+1]), out)
}

func sectionViews(secs []config.Section) []map[string]any {
	out := []map[string]any{}
	for _, sec := range secs {
		out = append(out, map[string]any{
			"id": sec.ID, "title": sec.Title, "capacity": sec.Items,
			"commentary": sec.Commentary, "format": sec.Format, "access": sec.Access,
			"topic": sec.Topic,
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
	if iss.Status == store.IssueSent || iss.Status == store.IssueScheduled {
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
	for _, sec := range issueSections(iss, pub) {
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
				fmt.Sprintf("section %q holds %d %s — the lineup asks for %d", e.Section, cap_, plural(cap_, "item", "items"), counts[e.Section]),
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

	// apply: place every entry, then remove what fell out of the lineup.
	// The batch is one logical lineup replacement, so every placement and
	// removal commits together or the prior lineup stays untouched.
	reIncluded := false
	err = db(r).WithTx(ctx, func(txdb *store.DB) error {
		positions := map[string]int{}
		for _, e := range in.Entries {
			positions[e.Section]++
			pos := positions[e.Section] - 1
			if ii, err := txdb.IssueItemByStory(ctx, iss.ID, e.StoryID); err == nil {
				if ii.Status == store.ItemRejected {
					reIncluded = true
				}
				if err := txdb.SetIssueItemPlacement(ctx, ii.ID, e.Section, pos, store.ItemApproved); err != nil {
					return err
				}
			} else {
				c := valid[e.StoryID]
				if _, err := txdb.CreateIssueItem(ctx, store.IssueItem{
					IssueID: iss.ID, StoryID: e.StoryID, Section: e.Section,
					Score: c.Story.TriageScore, Reason: c.Story.TriageReason,
					Status: store.ItemApproved, Origin: "manual", Position: pos,
				}); err != nil {
					return err
				}
			}
		}
		items, err := txdb.IssueItems(ctx, iss.ID)
		if err != nil {
			return err
		}
		for _, ii := range items {
			if (ii.Status == store.ItemPending || ii.Status == store.ItemApproved) && !seen[ii.StoryID] {
				if err := txdb.SetIssueItemStatus(ctx, ii.ID, store.ItemRemoved); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if apiDBErr(w, r, err, "item") {
		return
	}

	// re-including a rejected story undoes the rejection: the guide
	// must forget the rule it learned from it, same as a decision undo
	var guide any
	if reIncluded {
		guide = s.guideAfterRebuild(w, r, pub)
	}
	cands, _ = db(r).RankedCandidates(ctx, iss.PublicationID, iss.ID)
	views, meta, err := s.candidatePage(r, cands, 0, candidatePageSize, pub, issueSections(iss, pub), showFiltered(r))
	if apiDBErr(w, r, err, "candidates") {
		return
	}
	resp := map[string]any{"candidates": views}
	maps.Copy(resp, meta)
	if guide != nil {
		resp["guide"] = guide
	}
	apiJSON(w, 200, resp)
}

// storySnippetMaxLen caps a member article's excerpt in the story
// response — long enough to judge the story, short enough for the
// side sheet's card layout.
const storySnippetMaxLen = 400

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
		row := map[string]any{
			"id": it.ID, "title": it.Title, "url": "/a/" + fmt.Sprint(it.ID),
			"outlet": n, "published_at": it.PublishedAt,
			"snippet": strutil.Truncate(it.ContentText, storySnippetMaxLen),
		}
		if it.Signal() {
			// Link-level signal: shown as "via <source>" with its link —
			// never as citation evidence (D30).
			row["signal"] = true
			row["via"] = n
		}
		items = append(items, row)
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
