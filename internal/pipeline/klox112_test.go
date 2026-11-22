package pipeline

// KLOX-112 (ranking G5): MMR variety and section-vector lineup fill.

import (
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/rank"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// A "Regulação" section with a vector must not complete with software
// stories (Final Cut Pro / Pages): it takes the regulation stories and
// stays short rather than fill with off-topic ones.
func TestSectionVectorDoesNotFillWithOffTopic(t *testing.T) {
	stories := []store.Story{
		detStory(1, "Final Cut Pro ganha novos recursos", "2025-01-05", 1),
		detStory(2, "Pages atualiza modelos de documento", "2025-01-05", 1),
		detStory(3, "BC publica nova regra para o Pix", "2025-01-01", 1),
	}
	scores := []float64{9, 8.5, 5}
	reasons := []string{"", "", ""}
	reg := []float32{1, 0, 0}
	lv := &lineupVectors{
		Story:   [][]float32{{0, 1, 0}, {0, 0.9, 0.1}, {0.95, 0, 0.1}},
		Section: map[int][]float32{0: reg},
	}
	pub := &store.Publication{Sections: []config.Section{
		{ID: "reg", Title: "Regulação", Items: 2, Topic: "regulação bancária"},
	}}
	picks := pickLineupVec(stories, scores, reasons, pub, nil, nil, lv)
	if len(picks) != 1 || picks[0].Refs[0] != 3 {
		t.Fatalf("picks = %+v, want only the regulation story (section stays short)", picks)
	}
}

// MMR: with two near-duplicate top stories, the second slot goes to a
// different story.
func TestLineupMMRSpreadsNearDuplicates(t *testing.T) {
	stories := []store.Story{
		detStory(1, "Pix muda regras", "2025-01-05", 1),
		detStory(2, "Pix muda regras (2)", "2025-01-05", 1),
		detStory(3, "Fintech levanta rodada", "2025-01-05", 1),
	}
	stories[1].Items[0].SourceName, stories[2].Items[0].SourceName = "b", "c"
	scores := []float64{9.0, 8.9, 8.0}
	reasons := []string{"", "", ""}
	lv := &lineupVectors{Story: [][]float32{{1, 0}, {0.99, 0.14}, {0, 1}}}
	pub := &store.Publication{Sections: []config.Section{{ID: "a", Title: "Lead", Items: 2}}}
	picks := pickLineupVec(stories, scores, reasons, pub, nil, nil, lv)
	if len(picks) != 2 || picks[0].Refs[0] != 1 || picks[1].Refs[0] != 3 {
		t.Fatalf("picks = %+v, want stories 1 then 3", picks)
	}
	for i := range picks {
		for j := i + 1; j < len(picks); j++ {
			a, b := lv.Story[picks[i].Refs[0]-1], lv.Story[picks[j].Refs[0]-1]
			if rank.Cos(a, b) > 0.85 {
				t.Fatalf("lineup holds a pair with cosine > 0.85")
			}
		}
	}
}
