package web

import (
	"slices"
	"strings"
	"testing"
)

// TestRemoveInItemDropsCitationMarker: removing a flagged sentence
// must take its trailing "[n]" too — no orphaned citation left behind.
func TestRemoveInItemDropsCitationMarker(t *testing.T) {
	remove := func(body, sent string) string {
		out, ok := removeInItem(body, &docItem{spanStart: 0, spanEnd: len(body)}, sent)
		if !ok {
			t.Fatalf("sentence not found in %q", body)
		}
		return out
	}
	body := "### H <!-- item 1 -->\n\nO custo segue três vezes o da banda larga urbana. [1] Outra afirmação sem fonte."
	out := remove(body, "O custo segue três vezes o da banda larga urbana. [1]")
	if strings.Contains(out, "[1]") {
		t.Fatalf("citation marker left behind: %q", out)
	}
	if !strings.Contains(out, "Outra afirmação sem fonte.") {
		t.Fatalf("kept sentence lost: %q", out)
	}

	// flag recorded without the marker (period-then-citation form)
	out = remove(body, "O custo segue três vezes o da banda larga urbana.")
	if strings.Contains(out, "[1]") {
		t.Fatalf("orphaned citation left behind: %q", out)
	}

	// the sentence's terminal punctuation goes with it — no ". ." stray
	out = remove("Duas pediram prazo maior [1]. Especialistas divergem.",
		"Duas pediram prazo maior [1]")
	if strings.Contains(out, ". .") || !strings.HasSuffix(out, "Especialistas divergem.") {
		t.Fatalf("stray punctuation left behind: %q", out)
	}
}

// TestGateSections: a paid section keeps its heading, the first item's
// headline and opening lines, then a single subscribers-only note.
// Everything after the note — the rest of that item and every other
// item in the section — must not reach the gated output; free sections
// pass through untouched.
func TestGateSections(t *testing.T) {
	body := strings.Join([]string{
		"# Title",
		"",
		"## Paid",
		"### Item A",
		"First sentence of A. Second sentence of A. Third of A.",
		"",
		"Second paragraph of A leaks if not skipped.",
		"**Sources:** [1] [X](http://x)",
		"### Item B",
		"Only sentence of B.",
		"",
		"> **Comentário do editor:** paid commentary must be hidden",
		"",
		"## Free",
		"### Item C",
		"All of C stays visible. Every sentence of it.",
		"",
		"**Sources:** [1] [Y](http://y)",
	}, "\n")

	got := gateSections(body, []string{"Paid"}, "en")

	for _, want := range []string{
		"## Paid", "### Item A",
		"First sentence of A.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("gated body missing %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{
		"Second sentence of A", "Third of A",
		"Second paragraph of A leaks",
		"### Item B", "Only sentence of B",
		"Comentário do editor",
		"[X](http://x)",
		"## Free", "### Item C",
		"All of C stays visible",
		"[Y](http://y)",
	} {
		if strings.Contains(got, leak) {
			t.Fatalf("gated body leaked %q:\n%s", leak, got)
		}
	}
	// one wall ends the issue, counting everything dropped below it
	if c := strings.Count(got, "Subscribers only"); c != 1 {
		t.Fatalf("expected one paywall note, got %d:\n%s", c, got)
	}
	if !strings.Contains(got, "2 more stories") {
		t.Fatalf("note should count items B and C:\n%s", got)
	}

	// a single-item paid section gets the plain note
	single := "## Paid\n### Only\nOne sentence. Another one."
	if out := gateSections(single, []string{"Paid"}, "en"); !strings.Contains(out, "subscribe to keep reading") {
		t.Fatalf("single-item note should not carry a count:\n%s", out)
	}
	// no paid sections configured → body passes through unchanged
	if out := gateSections(body, nil, "en"); out != body {
		t.Fatalf("empty paid list should pass body through:\n%s", out)
	}
	// a section name that merely resembles a paid one is not gated
	if out := gateSections("## Paid extra\n### I\nFull text stays.", []string{"Paid"}, "en"); !strings.Contains(out, "Full text stays.") {
		t.Fatalf("non-matching section was gated:\n%s", out)
	}
}

func TestPaywallNoteLocalized(t *testing.T) {
	if n := paywallNote("pt-BR", 0); !strings.Contains(n, "assinantes") {
		t.Fatalf("pt note expected, got %q", n)
	}
	if n := paywallNote("pt-BR", 3); !strings.Contains(n, "mais 3 matérias") {
		t.Fatalf("pt counted note expected, got %q", n)
	}
	if n := paywallNote("en", 0); !strings.Contains(n, "Subscribers only") {
		t.Fatalf("en note expected, got %q", n)
	}
	if n := paywallNote("en", 2); !strings.Contains(n, "2 more stories") {
		t.Fatalf("en counted note expected, got %q", n)
	}
}

// TestRemoveRawBulk: removing several sentences from one item segment in
// sequence — the bulk remove-flagged core — leaves survivors, citation
// markers of kept sentences and the sources block intact.
func TestRemoveRawBulk(t *testing.T) {
	seg := "Primeira frase. [1] Segunda apoiada. [2] Terceira livre.\n\n**Sources:** [1] A [2] B"
	var ok bool
	seg, ok = removeRaw(seg, "Primeira frase. [1]")
	if !ok {
		t.Fatal("first sentence not removed")
	}
	seg, ok = removeRaw(seg, "Terceira livre.")
	if !ok {
		t.Fatal("last sentence not removed")
	}
	if !strings.Contains(seg, "Segunda apoiada. [2]") {
		t.Fatalf("kept sentence damaged: %q", seg)
	}
	if strings.Contains(seg, "Primeira") || strings.Contains(seg, "Terceira") {
		t.Fatalf("removed sentence survived: %q", seg)
	}
	if strings.Contains(seg, " [1]") && !strings.Contains(seg, "**Sources:**") {
		t.Fatalf("orphaned citation: %q", seg)
	}
	if !strings.Contains(seg, "**Sources:**") {
		t.Fatalf("sources block lost: %q", seg)
	}
}

// TestInsertRawBatchRestore exercises the multi-entry splice the batch
// undo performs: entries land at their recorded index in ascending
// order while the raw sequence is kept in sync between inserts — an
// original [A,B,C,D] minus {B,D} must come back exactly.
func TestInsertRawBatchRestore(t *testing.T) {
	// the stored body keeps one sentence per line
	seg := "A.\nC.\n\n**Sources:** [1] x"
	cur := []string{"A.", "C."}
	insert := func(idx int, raw string) {
		if idx > len(cur) {
			idx = len(cur)
		}
		seg = insertRaw(seg, cur, idx, raw)
		cur = slices.Insert(cur, idx, raw)
	}
	insert(1, "B.") // removed from index 1
	insert(3, "D.") // removed from index 3 — after B landed;
	// overflow lands after the item's blank line, before the sources
	// block (the pre-existing append position)
	if want := "A.\nB.\nC.\n\nD.\n**Sources:** [1] x"; seg != want {
		t.Fatalf("restored segment = %q, want %q", seg, want)
	}

	// an entry recorded at index 0 lands first
	seg = "B.\nC."
	cur = []string{"B.", "C."}
	insert(0, "A.")
	if !strings.HasPrefix(seg, "A.\nB.\nC.") {
		t.Fatalf("index-0 restore placed wrong: %q", seg)
	}

	// an index past the end appends before the sources block
	seg = "A.\n\n**Sources:** x"
	cur = []string{"A."}
	insert(5, "Z.")
	i, j := strings.Index(seg, "Z."), strings.Index(seg, "**Sources:**")
	if i < 0 || i > j {
		t.Fatalf("overflow index must land before sources: %q", seg)
	}
}
