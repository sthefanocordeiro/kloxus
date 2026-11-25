package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// approveCiteCheck runs the one Haiku inverted-meaning pass at
// approval. It writes a response and returns true only when a verdict
// blocked approval; every failure mode (caps, kill switch, provider
// error) returns false so approval proceeds on deterministic checks.
func (s *Server) approveCiteCheck(w http.ResponseWriter, r *http.Request, iss *store.Issue, pub *store.Publication) bool {
	ctx := r.Context()
	if s.d.Cfg.Mode == "demo" || s.d.Cfg.LLMOff() || s.d.LLM == nil {
		return false // real-mode AI only; demo keeps the deterministic floor
	}
	if s.d.Cfg.CiteChecksPerEdition > 0 {
		if n, err := db(r).PaidActionCount(ctx, store.PaidActionCiteCheck, iss.ID); err == nil && n >= s.d.Cfg.CiteChecksPerEdition {
			return false
		}
	}
	if s.d.Cfg.PaidActionsPerDay > 0 {
		if n, err := db(r).PaidActionsToday(ctx); err == nil && n >= s.d.Cfg.PaidActionsPerDay {
			return false
		}
	}
	doc, err := buildIssueDocument(ctx, db(r), iss, pub)
	if err != nil {
		return false
	}
	type sent struct {
		ID   int    `json:"id"`
		Item int    `json:"item"`
		Refs string `json:"refs"`
		Text string `json:"text"`
	}
	var sents []sent
	var items []map[string]any
	// byID maps the check's sentence id back to its raw stored text —
	// the flag's key in the document.
	rawByID := map[int]string{}
	itemRefByID := map[int]int64{}
	id := 0
	for _, sec := range doc.Sections {
		for _, it := range sec.Items {
			var srcs []map[string]any
			for _, src := range it.Sources {
				srcs = append(srcs, map[string]any{"Ref": src.Ref, "URL": src.URL, "Excerpt": src.Excerpt})
			}
			if len(srcs) > 0 {
				items = append(items, map[string]any{"Ref": it.Ref, "Sources": srcs})
			}
			for _, sn := range it.Sentences {
				if sn.Kind != "fact" || len(sn.Citations) == 0 {
					continue
				}
				id++
				refs := make([]string, len(sn.Citations))
				for i, c := range sn.Citations {
					refs[i] = fmt.Sprint(c)
				}
				sents = append(sents, sent{ID: id, Item: it.Ref,
					Refs: strings.Join(refs, ","), Text: sn.Text})
				rawByID[id] = sn.raw
				itemRefByID[id] = int64(it.Ref)
			}
		}
	}
	if len(sents) == 0 {
		return false
	}
	user, err := pipeline.RenderPrompt("citecheck_approve", map[string]any{
		"Publication": pub.Name, "Sentences": sents, "Items": items,
	})
	if err != nil {
		return false
	}
	resp, err := s.d.LLM.Complete(ctx, llm.Request{
		Model: s.d.Cfg.ModelTriage, Template: "citecheck", User: user, MaxTokens: 4096,
	})
	if err != nil {
		slog.Info("approval citecheck skipped", "err", err)
		return false // blocked/failed → deterministic results stand (§7.5)
	}
	if err := db(r).RecordPaidAction(ctx, store.PaidActionCiteCheck, iss.ID); err != nil {
		slog.Warn("paid-action record failed", "err", err)
	}
	var checks []struct {
		ID      int    `json:"id"`
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Text)), &checks); err != nil {
		// the model answered non-JSON — treat as unavailable
		var anyJSON []struct {
			ID      int    `json:"id"`
			Verdict string `json:"verdict"`
			Note    string `json:"note"`
		}
		start := strings.Index(resp.Text, "[")
		end := strings.LastIndex(resp.Text, "]")
		if start < 0 || end <= start || json.Unmarshal([]byte(resp.Text[start:end+1]), &anyJSON) != nil {
			return false
		}
		checks = anyJSON
	}
	var newFlags []store.Flag
	var reasons []apiReason
	for _, c := range checks {
		if c.Verdict != "unsupported" {
			continue
		}
		raw, ok := rawByID[c.ID]
		if !ok {
			continue
		}
		note := c.Note
		if note == "" {
			note = "citation check: claim not supported by the cited source"
		}
		newFlags = append(newFlags, store.Flag{Item: itemRefByID[c.ID], Sentence: raw, Note: note})
		reasons = append(reasons, apiReason{Code: "unsupported_claim",
			Message: note, Target: &apiTarget{Kind: "sentence", Ref: fmt.Sprintf("%d.%d", itemRefByID[c.ID], c.ID)}})
	}
	if len(newFlags) == 0 {
		return false
	}
	flags := append(append([]store.Flag{}, iss.Flags...), newFlags...)
	if err := db(r).SaveIssueEdit(ctx, iss.ID, store.IssueEdit{
		PubID: iss.PublicationID, Body: iss.BodyMD, Flags: flags,
		Decision: "citecheck", Reason: fmt.Sprintf("approval-time check flagged %d %s", len(newFlags), plural(len(newFlags), "sentence", "sentences")),
	}); err != nil {
		slog.Warn("citecheck flag save failed", "err", err)
		return false
	}
	apiErr(w, 409, "blocked", "the citation check found claims its sources do not support — edit or remove each", reasons...)
	return true
}

// citedCorpus is the stored text the citation floor and the originality
// rule verify against for one dossier source: the catalog item's stored
// body when it exists, else the dossier facts attributed to that ref —
// the same corpus the pipeline's citecheck loads.
func (s *Server) citedCorpus(d *pipeline.Dossier, dc *docContext, src pipeline.DossierSource) string {
	if dc != nil {
		if it, ok := dc.items[src.ItemID]; ok && it.ContentText != "" {
			// the headline/standfirst counts as support (KLOX-100): a
			// sentence echoing the article's own title is grounded.
			return it.Title + "\n" + it.ContentText
		}
	}
	var b strings.Builder
	for _, f := range append(append([]pipeline.DossierFact{}, d.Facts...), d.DeepFacts...) {
		if f.Ref == src.Ref {
			b.WriteString(f.Text)
			b.WriteString(" ")
		}
	}
	return b.String()
}

// composeFlagNotes applies the deterministic half of citecheck to one
// composed (editor-written) line (KLOX-51): an uncited fact, a claim
// the cited source's stored text does not support, or a ≥10-word run
// copied from a source each produce a flag note identical to the ones
// the pipeline writes — the flag then blocks approval.
func (s *Server) composeFlagNotes(item *docItem, dc *docContext, line string) []string {
	var notes []string
	d := item.dossier
	byRef := map[int]pipeline.DossierSource{}
	if d != nil {
		for _, src := range d.Sources {
			byRef[src.Ref] = src
		}
	}
	nums := pipeline.CitationNums(line)
	if len(nums) == 0 {
		notes = append(notes, "sentence has no citation")
	}
	for _, n := range nums {
		src, ok := byRef[n]
		if !ok {
			notes = append(notes, fmt.Sprintf("cites source [%d] that does not exist", n))
			continue
		}
		// when the source carries no stored text at all the dossier
		// summary is the last-resort corpus — same fallback as
		// dossierSupports on the edit path.
		corpus := s.citedCorpus(d, dc, src)
		if corpus == "" {
			corpus = d.Summary
		}
		switch {
		case !pipeline.SupportedBySource(line, corpus):
			notes = append(notes, fmt.Sprintf("not supported by [%d]", n))
		case pipeline.UnsupportedDate(line, corpus):
			notes = append(notes, fmt.Sprintf("date not supported by [%d]", n))
		case pipeline.NegationUnsupported(line, corpus):
			notes = append(notes, fmt.Sprintf("negation not supported by [%d]", n))
		}
	}
	if d != nil {
		for _, src := range d.Sources {
			if pipeline.TooCloseToSource(line, s.citedCorpus(d, dc, src)) {
				notes = append(notes, "too close to the source")
				break
			}
		}
	}
	return notes
}

// tooCloseToSources applies the originality rule to an edited sentence:
// it must not copy a run of source words — the write-time counterpart
// of citecheck's "too close to the source" flag.
func (s *Server) tooCloseToSources(item *docItem, dc *docContext, text string) bool {
	d := item.dossier
	if d == nil {
		return false
	}
	for _, src := range d.Sources {
		if pipeline.TooCloseToSource(text, s.citedCorpus(d, dc, src)) {
			return true
		}
	}
	return false
}

var refRe = regexp.MustCompile(`\[\d+\]`)

// dossierSupports is the citation check for one edited sentence
// (KLOX-48): every [n] must be a source of the item, and the cited
// source's stored text must pass the shared lexical floor — the model
// verdict (real mode) can only tighten that floor, never waive it, so
// an unrelated [n] can never clear a flag.
func (s *Server) dossierSupports(r *http.Request, d *pipeline.Dossier, dc *docContext, itemRef int64, text string) bool {
	byRef := map[int]pipeline.DossierSource{}
	for _, src := range d.Sources {
		byRef[src.Ref] = src
	}
	// Lexical floor against the cited sources' stored text. When an
	// item carries no stored body (a signal row) the dossier facts
	// attributed to that ref — themselves verbatim excerpts — are the
	// only corpus left.
	var corpus []string
	for _, n := range pipeline.CitationNums(text) {
		src, ok := byRef[n]
		if !ok {
			return false // cites a source that does not exist
		}
		if dc != nil {
			if it, ok := dc.items[src.ItemID]; ok && it.ContentText != "" {
				// headline/standfirst count as support (KLOX-100) —
				// same corpus as citedCorpus.
				corpus = append(corpus, it.Title+"\n"+it.ContentText)
				continue
			}
		}
		for _, f := range append(append([]pipeline.DossierFact{}, d.Facts...), d.DeepFacts...) {
			if f.Ref == n {
				corpus = append(corpus, f.Text)
			}
		}
	}
	if len(corpus) == 0 {
		corpus = append(corpus, d.Summary)
	}
	joined := strings.Join(corpus, " ")
	if !pipeline.SupportedBySource(text, joined) ||
		pipeline.UnsupportedDate(text, joined) ||
		pipeline.NegationUnsupported(text, joined) {
		return false
	}
	// model check on top of the floor (real mode); demo misses mean ok.
	// "manual" dossiers (deterministic compose) skip the model entirely —
	// the manual-compose flow must work with zero model calls, and its
	// sentences are editor-written, not model claims to re-verify.
	var checks []struct {
		ID      int    `json:"id"`
		Verdict string `json:"verdict"`
	}
	user, err := editCitecheckPrompt(d, itemRef, text)
	if err == nil && d.Origin != "manual" && !s.d.Cfg.LLMOff() {
		if resp, err := s.d.LLM.Complete(r.Context(), llm.Request{
			Model: s.d.Cfg.ModelTriage, Template: "citecheck", User: user, MaxTokens: 512,
		}); err == nil {
			if json.Unmarshal([]byte(resp.Text), &checks) == nil && len(checks) > 0 {
				return checks[0].Verdict == "ok"
			}
		}
	}
	return true
}

// editCitecheckPrompt renders the citecheck prompt for one edited
// sentence. The template reads capitalized fields (.ID, .Ref, .URL …),
// so the data keys must match its casing — lowercase keys render as
// "<no value>" and the model would judge a blank prompt (KLOX-153).
func editCitecheckPrompt(d *pipeline.Dossier, itemRef int64, text string) (string, error) {
	srcs := []map[string]any{}
	for _, src := range d.Sources {
		srcs = append(srcs, map[string]any{"Ref": src.Ref, "URL": src.URL})
	}
	return pipeline.RenderPrompt("citecheck", map[string]any{
		"Publication": "",
		"Sentences":   []map[string]any{{"ID": 1, "Item": itemRef, "Refs": "", "Text": text}},
		"Items":       []map[string]any{{"Ref": itemRef, "Sources": srcs}},
	})
}
