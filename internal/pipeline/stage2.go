package pipeline

// Stage-2 ranking (KLOX-110, epic KLOX-107 section 6): the reranker
// scores the stage-1 shortlist once per run. It never runs on page load
// and never fails a run: any error (off, cap, 429, timeout) leaves the
// stage-1 order in place and logs.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/rank"
	"github.com/sthefanocordeiro/kloxus/internal/rerank"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// DefaultReranker is the process-wide stage-2 reranker; main wires it
// at boot. Nil (or the off provider) keeps the stage-1 order. A
// Runner.Reranker overrides it.
var DefaultReranker rerank.Reranker

// Stage2Config tunes stage 2. Floor 0 disables the rerank floor cut
// (not calibrated yet - see D63).
type Stage2Config struct {
	K     int
	Floor float64
}

var Stage2 = Stage2Config{K: rerank.DefaultK}

func (r *Runner) reranker() rerank.Reranker {
	if r.Reranker != nil {
		return r.Reranker
	}
	return DefaultReranker
}

// rerankQuery builds the Portuguese query from the publication.
func rerankQuery(pub *store.Publication) string {
	_, skip := editorialLineSplit(pub.EditorialLine)
	if rt := strings.TrimSpace(pub.RejectTerms); rt != "" {
		skip = strings.TrimSpace(skip + " " + rt)
	}
	pos, _ := editorialLineSplit(pub.EditorialLine)
	var secs []string
	for _, s := range pub.Sections {
		if t := strings.TrimSpace(s.Title); t != "" {
			secs = append(secs, t)
		}
	}
	return rerank.Query(pub.Topic, pos, strings.Join(secs, ", "), skip)
}

// earliestItem is the story's oldest member (by published date; the
// first member when none is dated).
func earliestItem(s *store.Story) *store.Item {
	if len(s.Items) == 0 {
		return nil
	}
	best := &s.Items[0]
	var bt time.Time
	have := false
	for i := range s.Items {
		if t, ok := parsePublished(s.Items[i].PublishedAt); ok && (!have || t.Before(bt)) {
			bt, have, best = t, true, &s.Items[i]
		}
	}
	return best
}

// applyRerank folds rerank scores (story index -> score) into the
// stage-1 verdict: rel_final = 0.7*rerank + 0.3*rel, score recomputed
// with rel_final, reranked stories lifted above every visible
// non-reranked one, and stories under floor joining the off-topic
// filter.
func applyRerank(cfg rank.Config, s1 *stage1Result, scores map[int]float64, floor float64) {
	if len(scores) == 0 {
		return
	}
	for i, rs := range scores {
		rel := s1.Rel[i]
		fin := rerank.Blend(rs, rel)
		s1.Scores[i] += stage1Scale * cfg.Rel * (fin - rel)
		s1.Rel[i] = fin
		if floor > 0 && rs < floor && s1.Hidden[i] == rank.HiddenNone {
			s1.Hidden[i] = rank.HiddenOffTopic
		}
		s1.Reasons[i] += fmt.Sprintf(" · reranker %.2f", rs)
	}
	// reranked always above non-reranked: lift only as far as needed.
	maxOther, minRe := -1e18, 1e18
	haveOther := false
	for i := range s1.Scores {
		if _, ok := scores[i]; ok {
			if s1.Scores[i] < minRe {
				minRe = s1.Scores[i]
			}
		} else if s1.Hidden[i] == rank.HiddenNone {
			haveOther = true
			if s1.Scores[i] > maxOther {
				maxOther = s1.Scores[i]
			}
		}
	}
	if haveOther && minRe <= maxOther {
		lift := maxOther - minRe + 0.01
		for i := range scores {
			s1.Scores[i] += lift
		}
	}
}

// rerankStage runs stage 2 over s1 in place. It is a no-op without a
// reranker and silent to the creator on every failure.
func (r *Runner) rerankStage(ctx context.Context, pub *store.Publication, stories []store.Story, s1 *stage1Result) {
	rr := r.reranker()
	if rr == nil {
		return
	}
	k := Stage2.K
	if k <= 0 {
		k = rerank.DefaultK
	}
	var order []int
	for i := range stories {
		if s1.Hidden[i] == rank.HiddenNone && earliestItem(&stories[i]) != nil {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return s1.Scores[order[a]] > s1.Scores[order[b]] })
	if len(order) > k {
		order = order[:k]
	}
	if len(order) == 0 {
		return
	}

	query := rerankQuery(pub)
	sum := sha256.Sum256([]byte(rr.Model() + "\x00" + query))
	hash := hex.EncodeToString(sum[:])
	now := time.Now()

	ids := make([]int64, len(order))
	for n, i := range order {
		ids[n] = earliestItem(&stories[i]).ID
	}
	cached, err := r.DB.RerankScores(ctx, pub.ID, hash, rr.Model(), ids, now)
	if err != nil {
		slog.Warn("stage2: reading rerank cache", "err", err)
		cached = map[int64]float64{}
	}
	scores := map[int]float64{}
	var missIdx []int
	var docs []string
	for n, i := range order {
		if s, ok := cached[ids[n]]; ok {
			scores[i] = s
			continue
		}
		it := earliestItem(&stories[i])
		missIdx = append(missIdx, i)
		docs = append(docs, rerank.Doc(stories[i].Title, it.ContentText))
	}
	if len(docs) > 0 {
		res, err := rr.Rerank(ctx, query, docs, 0)
		if err != nil {
			// silent to the creator: off, cap, 429, timeout.
			slog.Warn("stage2: reranker unavailable, keeping stage-1 order", "err", err)
			if len(scores) == 0 {
				return
			}
		} else {
			fresh := map[int64]float64{}
			for _, sc := range res {
				if sc.Index < 0 || sc.Index >= len(missIdx) {
					continue
				}
				i := missIdx[sc.Index]
				scores[i] = sc.Score
				fresh[earliestItem(&stories[i]).ID] = sc.Score
			}
			if serr := r.DB.SaveRerankScores(ctx, pub.ID, hash, rr.Model(), fresh, now); serr != nil {
				slog.Warn("stage2: saving rerank scores", "err", serr)
			}
		}
	}
	applyRerank(Stage1Config, s1, scores, Stage2.Floor)
}
