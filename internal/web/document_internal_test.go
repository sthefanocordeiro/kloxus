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

// TestPubSectionIndex: the parser resolves a `## ` heading's commentary
// mode and format from the edition's section snapshot.
func TestPubSectionIndex(t *testing.T) {
	idx := pubSectionIndex(&store.Publication{Sections: []config.Section{
		{Title: "Radar", Format: "news", Commentary: "required"},
	}})
	if sc := idx["Radar"]; sc.Format != "news" || sc.Commentary != "required" {
		t.Fatalf("Radar attrs = %+v", sc)
	}
	if _, ok := idx["Missing"]; ok {
		t.Fatal("unexpected section entry")
	}
}

// TestDocBodyParserParse: the body walk splits sections, items and
// sentences, attaches a matching flag to its sentence, derives the
// no-citation flag on an uncited fact (KLOX-52), and leaves an
// unmatched "entry has no citations" flag on its item while an uncited
// fact still stands.
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
		doc: doc,
		dc:  dcFromPairs(nil, nil),
		flags: indexDocFlags([]store.Flag{
			{Item: 1, Sentence: "Custo segue três vezes o da banda larga. [1]", Note: "unverifiable"},
			{Item: 2, Sentence: "no matching sentence", Note: "entry has no citations"},
		}),
		sections:   map[string]config.Section{"Radar": {Commentary: "required"}},
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
	if got := "Custo segue três vezes o da banda larga. Outra afirmação sem fonte."; item.Text != got {
		t.Fatalf("item text = %q, want %q", item.Text, got)
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
	if s1.Kind != "fact" || s1.Flag == nil || s1.Flag.Note != "sentence has no citation" {
		t.Fatalf("uncited sentence should carry the derived flag: %+v", s1)
	}
	s2 := item.Sentences[2]
	if s2.Kind != "interpretation" || s2.Text != "tom do editor." {
		t.Fatalf("interpretation = %+v", s2)
	}

	item2 := doc.Sections[1].Items[0]
	if len(item2.Flags) != 1 || item2.Flags[0].Note != "entry has no citations" {
		t.Fatalf("live unmatched flag should stay on the item: %+v", item2.Flags)
	}
	if p.liveFlags != 2 {
		t.Fatalf("liveFlags = %d, want 2 (1 sentence + 1 item)", p.liveFlags)
	}

	// each item carries its item block, aligned to the item's span
	if len(radar.Blocks) != 1 || radar.Blocks[0].Kind != "item" || radar.Blocks[0].Ref != 1 {
		t.Fatalf("radar blocks = %+v", radar.Blocks)
	}
	if radar.Blocks[0].spanStart != item.spanStart || radar.Blocks[0].spanEnd != item.spanEnd {
		t.Fatalf("item block span = (%d,%d), item = (%d,%d)",
			radar.Blocks[0].spanStart, radar.Blocks[0].spanEnd, item.spanStart, item.spanEnd)
	}

	// item spans bound the block so edits stay inside their own item
	if item.spanStart != strings.Index(body, "### First story") ||
		item.spanEnd != strings.Index(body, "## Cena") ||
		item2.spanEnd != len(body) {
		t.Fatalf("item spans wrong: (%d,%d) (%d,%d)",
			item.spanStart, item.spanEnd, item2.spanStart, item2.spanEnd)
	}
}

// TestDocBodyParserSectionBlocks: layout blocks between items — a
// divider, an item, an author heading and contiguous `> ` lines that
// merge into one text block (KLOX-66).
func TestDocBodyParserSectionBlocks(t *testing.T) {
	body := strings.Join([]string{
		"## Top",
		"---",
		"### H <!-- item 1 -->",
		"Fato com fonte [1].",
		"#### Mid",
		"> note one",
		"> note two",
	}, "\n")

	doc := &issueDocument{Sections: []docSection{}}
	p := &docBodyParser{
		doc:      doc,
		dc:       dcFromPairs(nil, nil),
		flags:    indexDocFlags(nil),
		sections: map[string]config.Section{},
		usedIDs:  map[string]int{},
	}
	p.parse(body)

	if len(doc.Sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(doc.Sections))
	}
	blocks := doc.Sections[0].Blocks
	if len(blocks) != 4 {
		t.Fatalf("blocks = %d, want 4: %+v", len(blocks), blocks)
	}
	if blocks[0].Kind != "divider" {
		t.Fatalf("block 0 = %+v, want divider", blocks[0])
	}
	if blocks[1].Kind != "item" || blocks[1].Ref != 1 {
		t.Fatalf("block 1 = %+v, want item 1", blocks[1])
	}
	if blocks[2].Kind != "heading" || blocks[2].Text != "Mid" {
		t.Fatalf("block 2 = %+v, want heading Mid", blocks[2])
	}
	if blocks[3].Kind != "text" || blocks[3].Text != "note one\nnote two" {
		t.Fatalf("block 3 = %+v, want merged text block", blocks[3])
	}
}

// TestAppendCommentarySections: a publication section the body has no
// heading for is appended with its format, commentary mode and text.
func TestAppendCommentarySections(t *testing.T) {
	doc := &issueDocument{Sections: []docSection{{Title: "Radar"}}}
	pub := &store.Publication{Sections: []config.Section{
		{Title: "Radar", Commentary: "required"},
		{Title: "Weekly", Format: "links", Commentary: "required"},
	}}
	appendCommentarySections(doc, pub, map[string]string{"Weekly": "late note"})
	if len(doc.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(doc.Sections))
	}
	added := doc.Sections[1]
	if added.Title != "Weekly" || added.Format != "links" ||
		added.Commentary.Mode != "required" || added.Commentary.Text != "late note" {
		t.Fatalf("appended section = %+v", added)
	}
	if added.Items == nil || added.Blocks == nil {
		t.Fatal("appended section should carry empty item and block lists")
	}
}

// TestCoverageOf: only fact sentences count; ok requires a citation
// and no flag, and flagged mirrors the live-flag count the parser
// tracked.
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
