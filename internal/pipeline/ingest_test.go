package pipeline

// Signal-item handling in the pipeline: cited-URL merges, the
// outlet_count ranking boost, and the evidence filter that keeps
// link-only signals out of the citation pool.
import (
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func article(id int64, canon string) store.Item {
	return store.Item{ID: id, CanonicalURL: canon, Title: "a", Kind: "article"}
}

func signal(id int64, cited ...string) store.Item {
	return store.Item{ID: id, Title: "s", Kind: "signal",
		Meta: store.ItemMeta{CitedURLs: cited}}
}

// TestMergeCitedGroups: a DJ signal citing an article's canonical URL
// folds into that article's cluster rather than standing alone.
func TestMergeCitedGroups(t *testing.T) {
	groups := map[int][]store.Item{
		1: {article(10, "https://tecnoblog.net/post-a")},
		2: {article(11, "https://meiobit.com/post-b")},
		3: {signal(12, "https://tecnoblog.net/post-a")},
		4: {signal(13)},
	}
	out := mergeCitedGroups(groups)
	if len(out) != 3 {
		t.Fatalf("groups=%d want 3", len(out))
	}
	var merged []store.Item
	for _, g := range out {
		for _, it := range g {
			if it.ID == 12 {
				merged = g
			}
		}
	}
	if len(merged) != 2 {
		t.Fatalf("signal did not merge onto its cited article: %+v", merged)
	}
}

// TestEvidenceItems: signals never reach the LLM citation pool.
func TestEvidenceItems(t *testing.T) {
	in := []store.Item{article(1, "u"), signal(2), article(3, "v")}
	out := evidenceItems(in)
	if len(out) != 2 {
		t.Fatalf("evidence=%d want 2", len(out))
	}
	for _, it := range out {
		if it.Signal() {
			t.Fatal("signal leaked into evidence")
		}
	}
}

// TestSignalBoost: outlet_count adds a bounded score bump.
func TestSignalBoost(t *testing.T) {
	st := store.Story{Items: []store.Item{
		article(1, "u"),
		{ID: 2, Kind: "signal", Meta: store.ItemMeta{OutletCount: 4}},
	}}
	if b := signalBoost(st); b != 1.6 {
		t.Fatalf("boost=%v want 1.6", b)
	}
	st.Items[1].Meta.OutletCount = 99
	if b := signalBoost(st); b != 2.0 {
		t.Fatalf("boost cap=%v want 2.0", b)
	}
	st.Items = []store.Item{article(1, "u")}
	if b := signalBoost(st); b != 0 {
		t.Fatalf("no-signal boost=%v want 0", b)
	}
}
