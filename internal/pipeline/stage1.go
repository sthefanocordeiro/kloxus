package pipeline

// Stage-1 ranking (KLOX-109, epic KLOX-107 section 5/7): embedding
// cosine against the publication's Rocchio profile, with a Postgres
// full-text fallback when vectors are missing. The math lives in
// internal/rank; this file loads vectors, builds the profile and maps
// the verdicts back onto the run's stories. No paid call happens here
// except the one small profile embedding when the profile changed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/embed"
	"github.com/sthefanocordeiro/kloxus/internal/rank"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// DefaultEmbedder is the process-wide query embedder used for the
// publication profile; main wires it at boot. Nil (or the off
// provider) makes stage 1 rank by FTS. A Runner.Embedder overrides it.
var DefaultEmbedder embed.Embedder

// QueryEmbedder is the embedder seam (alias keeps pipeline.go free of
// the internal/embed import, which would clash with go:embed's name).
type QueryEmbedder = embed.Embedder

// Stage1Config is the one place stage-1 weights live (spec section 5).
var Stage1Config = rank.DefaultConfig()

// stage1Scale maps the 0-1 stage-1 score onto the 0-10 range the board,
// the lineup and the stored triage_score already use.
const stage1Scale = 10.0

// categoryMatchBoost is the same +2 the heuristic gives an on-topic
// enrichment tag. A matching publisher category is explicit item-level
// evidence; absent or generic labels add nothing. Editorial reject and
// non-editorial penalties are an order of magnitude larger and still win.
const categoryMatchBoost = 2.0

// ftsMinToken drops short tokens from the FTS query.
const ftsMinToken = 3

func (r *Runner) embedder() embed.Embedder {
	if r.Embedder != nil {
		return r.Embedder
	}
	return DefaultEmbedder
}

// profileHash is the hash of the theme, editorial line and topics text
// (spec section 2) - the profile is re-embedded only when it changes.
func profileHash(pub *store.Publication, model string) string {
	h := sha256.Sum256([]byte(model + "\x00" + profileText(pub) + "\x00" + skipText(pub)))
	return hex.EncodeToString(h[:])
}

// profileText is the query-side embedding input: name + theme +
// editorial line (skip clauses removed - they are embedded apart as
// skip_vec) + topics.
func profileText(pub *store.Publication) string {
	var b strings.Builder
	for _, s := range []string{pub.Name, pub.Topic, PositiveEditorialText(pub) + " " + pub.Audience} {
		if s = strings.TrimSpace(s); s != "" {
			if b.Len() > 0 {
				b.WriteString(". ")
			}
			b.WriteString(s)
		}
	}
	return b.String()
}

// skipText is the vocabulary the creator wants out: declared reject
// terms plus the editorial line's skip clauses.
func skipText(pub *store.Publication) string {
	_, sk := editorialLineSplit(pub.EditorialLine)
	return strings.TrimSpace(pub.RejectTerms + " " + sk)
}

// ftsTerms builds the sanitized OR-query vocabulary from the theme and
// topics: folded, alphanumeric, no stopwords, no duplicates.
func ftsTerms(pub *store.Publication) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range textToks(pub.Topic + " " + PositiveEditorialText(pub)) {
		if len(t) < ftsMinToken || termStopwords[t] || seen[t] {
			continue
		}
		ok := true
		for _, c := range t {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				ok = false
				break
			}
		}
		if ok {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// loadProfile returns the Rocchio profile and skip vector, embedding
// the publication text (one small query-kind call) only when the stored
// profile is missing or its hash changed. ok=false means no usable
// profile - the caller ranks by FTS.
func (r *Runner) loadProfile(ctx context.Context, pub *store.Publication, emb embed.Embedder) (rank.Profiles, bool) {
	if emb == nil || emb.Model() == "off" || strings.TrimSpace(profileText(pub)) == "" {
		return rank.Profiles{}, false
	}
	hash := profileHash(pub, emb.Model())
	cur, err := r.DB.GetPublicationProfile(ctx, pub.ID)
	if err != nil || cur.ProfileHash != hash || len(cur.ThemeVec) == 0 {
		texts := []string{profileText(pub)}
		if sk := skipText(pub); sk != "" {
			texts = append(texts, sk)
		}
		vecs, eerr := emb.Embed(ctx, texts, embed.Query)
		if eerr != nil || len(vecs) != len(texts) {
			slog.Warn("stage1: profile embedding unavailable, ranking by FTS", "err", eerr)
			return rank.Profiles{}, false
		}
		next := &store.PublicationProfile{ThemeVec: vecs[0]}
		skipLit := ""
		if len(vecs) > 1 {
			next.SkipVec = vecs[1]
			skipLit = embed.Literal(vecs[1])
		}
		if serr := r.DB.SavePublicationProfile(ctx, pub.ID, embed.Literal(vecs[0]), skipLit, hash); serr != nil {
			slog.Warn("stage1: saving profile", "err", serr)
		}
		// keep feedback means when a stored row existed
		if cur != nil {
			next.AcceptedMean, next.RejectedMean = cur.AcceptedMean, cur.RejectedMean
		}
		cur = next
	}
	cfg := Stage1Config
	return rank.Profiles{
		Rocchio: cfg.Profile(cur.ThemeVec, cur.AcceptedMean, cur.RejectedMean),
		Skip:    cur.SkipVec,
	}, true
}

// stage1Result is the per-story verdict the runner applies.
type stage1Result struct {
	Scores  []float64
	Reasons []string
	Rel     []float64
	Hidden  []string
	// StoryVecs and SectionVecs feed the lineup's MMR and section fill
	// (KLOX-112). Nil entries mean "no vector".
	StoryVecs   [][]float32
	SectionVecs map[int][]float32
}

// editorialPenalty keeps the KLOX-55/94/115 policy demotions on top of
// the stage-1 score: reject/skip hits and non-editorial feed noise sink
// below real coverage exactly as before.
func editorialPenalty(s *store.Story, pub *store.Publication, enrich map[int64]*store.ItemEnrichment) float64 {
	pen := 0.0
	if nonEditorial(s) {
		pen -= nonEditorialPenalty
	}
	if pub != nil && (pub.RejectTerms != "" || pub.EditorialLine != "") {
		hay, words := storySearchText(s, enrich)
		if termsAny(parseTerms(pub.RejectTerms), hay, words) || phraseAny(skipTerms(pub.EditorialLine), words) {
			pen -= rejectPenalty
		}
	}
	return pen
}

// rankStage1 scores every story with the section 5 formula. base holds
// the heuristic reasons (date, outlets, policy markers) which stage 1
// extends with the relevance reading. It never returns an error: any
// failure degrades to the FTS rel, never to date order.
func (r *Runner) rankStage1(ctx context.Context, pub *store.Publication, stories []store.Story,
	baseReasons []string, enrich map[int64]*store.ItemEnrichment) stage1Result {

	cfg := Stage1Config
	emb := r.embedder()
	prof, haveProfile := r.loadProfile(ctx, pub, emb)

	var ids []int64
	for i := range stories {
		for j := range stories[i].Items {
			ids = append(ids, stories[i].Items[j].ID)
		}
	}
	vecs := map[int64][]float32{}
	if haveProfile {
		v, err := r.DB.ItemVectors(ctx, emb.Model(), ids)
		if err != nil {
			slog.Warn("stage1: loading vectors", "err", err)
		} else {
			vecs = v
		}
	}

	storyVec := make([][]float32, len(stories))
	needFTS := false
	for i := range stories {
		var vs [][]float32
		for j := range stories[i].Items {
			if v := vecs[stories[i].Items[j].ID]; v != nil {
				vs = append(vs, v)
			}
		}
		storyVec[i] = rank.Mean(vs, cfg.MaxMembers)
		if storyVec[i] == nil {
			needFTS = true
		}
	}

	// FTS rel for stories without a vector (all of them when embeddings
	// are off): max member ts_rank_cd, scaled 0-1 within the run.
	fts := make([]float64, len(stories))
	if needFTS {
		raw, err := r.DB.FTSRank(ctx, ids, ftsTerms(pub))
		if err != nil {
			slog.Warn("stage1: fts ranking", "err", err)
		}
		rawStory := make([]float64, len(stories))
		for i := range stories {
			for j := range stories[i].Items {
				if x := raw[stories[i].Items[j].ID]; x > rawStory[i] {
					rawStory[i] = x
				}
			}
		}
		fts = rank.ScaleFTS(rawStory)
	}

	pt := ptLang(pub.Language)
	now := time.Now()
	out := stage1Result{
		Scores: make([]float64, len(stories)), Reasons: make([]string, len(stories)),
		Rel: make([]float64, len(stories)), Hidden: make([]string, len(stories)),
		StoryVecs: storyVec,
	}
	if haveProfile {
		out.SectionVecs = r.loadSectionVecs(ctx, pub, emb)
	}
	for i := range stories {
		s := &stories[i]
		age := -1.0
		if t, ok := storyNewest(s); ok {
			age = now.Sub(t).Hours()
			if age < 0 {
				age = 0
			}
		}
		res := cfg.Score(prof, rank.Story{
			Vec: storyVec[i], FTS: fts[i], AgeHours: age,
			Outlets: len(s.Outlets()),
			PTPub:   pt,
			English: rank.IsEnglish(langSample(s)),
		})
		catMatch := categoryOnTopic(pub, s)
		out.Scores[i] = res.Score*stage1Scale + editorialPenalty(s, pub, enrich)
		if catMatch {
			out.Scores[i] += categoryMatchBoost
		}
		out.Rel[i], out.Hidden[i] = res.Rel, res.Hidden
		if catMatch && res.Hidden == rank.HiddenOffTopic {
			// A publisher's own editoria label is independent evidence of
			// adherence, so it can lift an embedding-floor false negative.
			// This clears only off_topic: English and every reject/promo/
			// non-editorial guard keep their normal force.
			out.Hidden[i] = rank.HiddenNone
		}
		reason := baseReasons[i]
		if catMatch {
			if pt {
				reason += " · categoria da editoria"
			} else {
				reason += " · matching publisher category"
			}
		}
		src := "emb"
		if res.Fallback {
			src = "fts"
		}
		if pt {
			reason += fmt.Sprintf(" · relevância %.2f (%s)", res.Rel, src)
		} else {
			reason += fmt.Sprintf(" · relevance %.2f (%s)", res.Rel, src)
		}
		out.Reasons[i] = reason
	}
	return out
}

// langSample is the text the language detector reads: the story title
// plus the opening of its first member's body.
func langSample(s *store.Story) string {
	text := s.Title
	if len(s.Items) > 0 {
		body := s.Items[0].ContentText
		if r := []rune(body); len(r) > 400 {
			body = string(r[:400])
		}
		text += " " + body
	}
	return text
}

// sectionHash keys a section vector to its topic text and the model.
func sectionHash(topic, model string) string {
	h := sha256.Sum256([]byte(model + "|section|" + strings.TrimSpace(topic)))
	return hex.EncodeToString(h[:])
}

// loadSectionVecs returns the topic vector of every section that has a
// "que tipo de pauta entra aqui?" text, by section index. Stale or
// missing vectors are re-embedded in one small query-kind call; any
// failure just leaves those sections on the term-matching path.
func (r *Runner) loadSectionVecs(ctx context.Context, pub *store.Publication, emb embed.Embedder) map[int][]float32 {
	var idx []int
	for i, sec := range pub.Sections {
		if strings.TrimSpace(sec.Topic) != "" && sec.ID != "" {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return nil
	}
	cached, err := r.DB.SectionProfiles(ctx, pub.ID)
	if err != nil {
		slog.Warn("stage1: loading section profiles", "err", err)
		return nil
	}
	out := map[int][]float32{}
	var stale []int
	for _, i := range idx {
		sec := pub.Sections[i]
		if c, ok := cached[sec.ID]; ok && c.TopicHash == sectionHash(sec.Topic, emb.Model()) && len(c.Vec) > 0 {
			out[i] = c.Vec
		} else {
			stale = append(stale, i)
		}
	}
	if len(stale) > 0 {
		texts := make([]string, len(stale))
		for k, i := range stale {
			texts[k] = pub.Sections[i].Title + ". " + pub.Sections[i].Topic
		}
		vecs, eerr := emb.Embed(ctx, texts, embed.Query)
		if eerr != nil || len(vecs) != len(texts) {
			slog.Warn("stage1: section embedding unavailable", "err", eerr)
		} else {
			for k, i := range stale {
				out[i] = vecs[k]
				sec := pub.Sections[i]
				if serr := r.DB.SaveSectionProfile(ctx, pub.ID, sec.ID, vecs[k], sectionHash(sec.Topic, emb.Model())); serr != nil {
					slog.Warn("stage1: saving section profile", "err", serr)
				}
			}
		}
	}
	return out
}

// RecordFeedback folds an accept/reject/swap decision on an issue item
// into the publication's Rocchio means (KLOX-112). It is best-effort -
// feedback never fails the decision - and free: it only reads stored
// vectors, no embedding call.
func RecordFeedback(ctx context.Context, db *store.DB, pubID, issueItemID int64, kind string) {
	emb := DefaultEmbedder
	if emb == nil {
		return
	}
	if err := db.FeedbackForItem(ctx, pubID, issueItemID, emb.Model(), kind); err != nil {
		slog.Warn("feedback: record failed", "err", err)
	}
}
