package web

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// TestIndexDocFlags: flags group by (item, raw sentence) and each
// item's list keeps the stored order for the ones no sentence claims.
func TestIndexDocFlags(t *testing.T) {
	idx := indexDocFlags([]store.Flag{
		{Item: 2, Sentence: "B sent.", Note: "n2"},
		{Item: 1, Sentence: "A sent.", Note: "n1"},
		{Item: 1, Sentence: "A other.", Note: "n3"},
	})
	if got := idx.bySentence[1]["A sent."].Note; got != "n1" {
		t.Fatalf("bySentence lookup = %q", got)
	}
	if len(idx.order[1]) != 2 || idx.order[1][0].Note != "n1" || idx.order[1][1].Note != "n3" {
		t.Fatalf("order not preserved: %+v", idx.order[1])
	}
	if _, ok := idx.bySentence[9]; ok {
		t.Fatal("unexpected item bucket")
	}
}

// TestDocBodyParserParse: the body walk splits sections, items and
// sentences, attaches a matching flag to its sentence, and leaves an
// unmatched flag on its item.
func TestDocBodyParserParse(t *testing.T) {
	body := strings.Join([]string{
		"# Issue title",
		"",
		"## Radar",
		"### First story <!-- item 1 -->",
		"Custo segue três vezes o da banda larga. [1] Outra afirmação sem fonte.",
		"> _Interpretação: tom do editor._",
		"**Sources:** [1] [X](http://x)",
		"## Cena",
		"### Second story <!-- item 2 -->",
		"Fato final da edição.",
	}, "\n")

	doc := &issueDocument{Sections: []docSection{}}
	p := &docBodyParser{
		doc:   doc,
		items: map[int]docItemInfo{},
		flags: indexDocFlags([]store.Flag{
			{Item: 1, Sentence: "Custo segue três vezes o da banda larga. [1]", Note: "unverifiable"},
			{Item: 2, Sentence: "no matching sentence", Note: "loose flag"},
		}),
		secMode:    map[string]string{"Radar": "required"},
		commentary: map[string]string{"Radar": "editorial note"},
		usedIDs:    map[string]int{},
	}
	p.parse(body)

	if len(doc.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(doc.Sections))
	}
	radar := doc.Sections[0]
	if radar.Title != "Radar" || radar.Commentary.Mode != "required" || radar.Commentary.Text != "editorial note" {
		t.Fatalf("radar section = %+v", radar)
	}
	if len(radar.Items) != 1 {
		t.Fatalf("radar items = %d, want 1", len(radar.Items))
	}
	item := radar.Items[0]
	if item.Ref != 1 || item.Headline != "First story" {
		t.Fatalf("item head = %+v", item)
	}
	if len(item.Sentences) != 3 {
		t.Fatalf("sentences = %d, want 3 (2 facts + 1 interpretation)", len(item.Sentences))
	}
	s0 := item.Sentences[0]
	if s0.Kind != "fact" || s0.Text != "Custo segue três vezes o da banda larga." {
		t.Fatalf("first sentence = %+v", s0)
	}
	if len(s0.Citations) != 1 || s0.Citations[0] != 1 {
		t.Fatalf("citations = %v", s0.Citations)
	}
	if s0.Flag == nil || s0.Flag.Note != "unverifiable" {
		t.Fatalf("matching flag not attached: %+v", s0.Flag)
	}
	s1 := item.Sentences[1]
	if s1.Kind != "fact" || s1.Flag != nil || len(s1.Citations) != 0 {
		t.Fatalf("uncited sentence = %+v", s1)
	}
	s2 := item.Sentences[2]
	if s2.Kind != "interpretation" || s2.Text != "tom do editor." {
		t.Fatalf("interpretation = %+v", s2)
	}

	item2 := doc.Sections[1].Items[0]
	if len(item2.Flags) != 1 || item2.Flags[0].Note != "loose flag" {
		t.Fatalf("unmatched flag should stay on the item: %+v", item2.Flags)
	}

	// item spans bound the block so edits stay inside their own item
	if item.spanStart != strings.Index(body, "### First story") ||
		item.spanEnd != strings.Index(body, "## Cena") ||
		item2.spanEnd != len(body) {
		t.Fatalf("item spans wrong: (%d,%d) (%d,%d)",
			item.spanStart, item.spanEnd, item2.spanStart, item2.spanEnd)
	}
}

// TestAppendCommentarySections: a publication section the body has no
// heading for is appended with its commentary mode and text.
func TestAppendCommentarySections(t *testing.T) {
	doc := &issueDocument{Sections: []docSection{{Title: "Radar"}}}
	pub := &store.Publication{Sections: []config.Section{
		{Title: "Radar", Commentary: "required"},
		{Title: "Weekly", Commentary: "required"},
	}}
	appendCommentarySections(doc, pub, map[string]string{"Weekly": "late note"})
	if len(doc.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(doc.Sections))
	}
	added := doc.Sections[1]
	if added.Title != "Weekly" || added.Commentary.Mode != "required" || added.Commentary.Text != "late note" {
		t.Fatalf("appended section = %+v", added)
	}
	if added.Items == nil {
		t.Fatal("appended section should carry an empty item list")
	}
}

// TestCoverageOf: only fact sentences count; ok requires a citation
// and no flag, and flagged mirrors the stored flag count.
func TestCoverageOf(t *testing.T) {
	doc := &issueDocument{Sections: []docSection{{Items: []docItem{{Sentences: []docSentence{
		{Kind: "fact", Citations: []int{1}},
		{Kind: "fact", Citations: []int{1}, Flag: &docFlag{Note: "x"}},
		{Kind: "fact"},
		{Kind: "interpretation"},
	}}}}}}
	cov := coverageOf(doc, 5)
	if cov.Total != 3 || cov.Cited != 2 || cov.Ok != 1 || cov.Flagged != 5 {
		t.Fatalf("coverage = %+v, want {Ok:1 Cited:2 Total:3 Flagged:5}", cov)
	}
}
