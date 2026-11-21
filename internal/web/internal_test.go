package web

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
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
// original [A,B,C,D] minus {B,D} must come back exactly. The second
// half covers the mid-line '. ' separator: a newline splice would
// leave 'X [1].' at line end, where SplitSentences re-attaches the '.'
// to the raw — the bug the KLOX-47 CI caught.
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

// TestInsertRawMidLineSeparator: the drafted body keeps sentences on
// one line separated by '. ' — restoring a mid-line entry must splice
// inline so the '. ' keeps bounding the previous piece ('transição [1]'
// must not come back as 'transição [1].').
func TestInsertRawMidLineSeparator(t *testing.T) {
	seg := "S0 [1]. S1 [1]. Duas associações [1]. Especialistas [4]. A norma [1].\n\n**Sources:** x"
	cur := []string{"S0 [1]", "S1 [1]", "Duas associações [1]", "Especialistas [4]", "A norma [1]."}
	// restoring 'O texto [1]' at index 3 must be byte-identical to a
	// segment that never lost it
	got := insertRaw(seg, cur, 3, "O texto prevê multa [1]")
	want := "S0 [1]. S1 [1]. Duas associações [1]. O texto prevê multa [1]. Especialistas [4]. A norma [1].\n\n**Sources:** x"
	if got != want {
		t.Fatalf("mid-line splice = %q, want %q", got, want)
	}
	// and the pieces the parser sees must be exactly the original raws —
	// 'Duas associações [1]' must not come back as 'Duas associações [1].'
	line := strings.TrimSpace(strings.SplitN(got, "\n\n", 2)[0])
	pieces := pipeline.SplitSentences(line)
	if want := []string{"S0 [1]", "S1 [1]", "Duas associações [1]", "O texto prevê multa [1]", "Especialistas [4]", "A norma [1]."}; !slices.Equal(pieces, want) {
		t.Fatalf("re-parsed pieces = %#v, want %#v", pieces, want)
	}

	// restoring at index 0 lands the entry on its own line — the piece
	// is identical even though the bytes differ from the original
	got = insertRaw("S1 [1]. S2 [1].", []string{"S1 [1]", "S2 [1]."}, 0, "S0 [1]")
	if want := "S0 [1]\nS1 [1]. S2 [1]."; got != want {
		t.Fatalf("index-0 mid-line splice = %q, want %q", got, want)
	}

	// an interpretation still lands on its own '>' line even mid-item
	got = insertRaw("A [1]. B [1].", []string{"A [1]", "B [1]."}, 1, "> _Interpretação: boa_")
	if !strings.Contains(got, "\n> _Interpretação: boa_\n") {
		t.Fatalf("interpretation not on its own line: %q", got)
	}
}

// TestParseUncitedFactFlag: an uncited fact that reaches the stored
// body without a flag row must still get the flag treatment — the
// editor's Edit / interpretation / Remove affordances render off the
// flag, and the uncited_sentences check already blocks approval, so a
// flagless uncited fact is a dead-end (the owner's v88 report,
// KLOX-52). Stored flags count against readiness only while they
// describe the current body: a flag on gone text is stale.
func TestParseUncitedFactFlag(t *testing.T) {
	pub := &store.Publication{Sections: []config.Section{{Title: "Top", Commentary: "none"}}}
	dc := dcFromPairs(nil, nil)
	parse := func(body string, flags []store.Flag) *issueDocument {
		return parseIssueDocument(&store.Issue{BodyMD: body, Flags: flags}, pub, dc)
	}
	sentences := func(doc *issueDocument) []docSentence {
		return doc.Sections[0].Items[0].Sentences
	}
	uncited := "## Top\n### H <!-- item 1 -->\n\nA claim nobody can trace.\n\n**Sources:** [1] x"
	cited := "## Top\n### H <!-- item 1 -->\n\nA claim with support [1].\n\n**Sources:** [1] x"

	// legacy body: uncited fact, zero flag rows — the flag is derived
	doc := parse(uncited, nil)
	if sn := sentences(doc)[0]; sn.Flag == nil || sn.Flag.Note != "sentence has no citation" {
		t.Fatalf("uncited fact flag = %+v, want 'sentence has no citation'", sn.Flag)
	}
	// the derived flag is affordance only — uncited_sentences carries
	// the readiness block, so coverage.flagged must not double-count it
	if doc.Coverage.Flagged != 0 {
		t.Fatalf("coverage.flagged = %d with only a derived flag", doc.Coverage.Flagged)
	}

	// a stored flag matching the live sentence wins and counts once
	doc = parse(uncited, []store.Flag{
		{Item: 1, Sentence: "A claim nobody can trace.", Note: "not supported by [1]"},
	})
	if sn := sentences(doc)[0]; sn.Flag == nil || sn.Flag.Note != "not supported by [1]" {
		t.Fatalf("stored flag = %+v, want 'not supported by [1]'", sn.Flag)
	}
	if doc.Coverage.Flagged != 1 {
		t.Fatalf("coverage.flagged = %d, want 1", doc.Coverage.Flagged)
	}

	// "entry has no citations" stays live — and blocks — while the item
	// still carries an uncited fact
	doc = parse(uncited, []store.Flag{
		{Item: 1, Sentence: "The item once lacked citations.", Note: "entry has no citations"},
	})
	if doc.Coverage.Flagged != 1 || len(doc.Sections[0].Items[0].Flags) != 1 {
		t.Fatalf("live entry flag: flagged=%d item.flags=%d", doc.Coverage.Flagged, len(doc.Sections[0].Items[0].Flags))
	}

	// once every fact is cited the same flags describe nothing — stale
	// sentence flags, stale item flags and flags on vanished items must
	// not count: nothing exists for the UI to act on (KLOX-52)
	doc = parse(cited, []store.Flag{
		{Item: 1, Sentence: "A claim nobody can trace.", Note: "not supported by [1]"},
		{Item: 9, Sentence: "On an item that no longer exists.", Note: "sentence has no citation"},
		{Item: 1, Sentence: "The item once lacked citations.", Note: "entry has no citations"},
	})
	if sn := sentences(doc)[0]; sn.Flag != nil || len(sn.Citations) != 1 {
		t.Fatalf("cited fact = flag %+v citations %v", sn.Flag, sn.Citations)
	}
	if doc.Coverage.Flagged != 0 || len(doc.Sections[0].Items[0].Flags) != 0 {
		t.Fatalf("stale flags counted: flagged=%d item.flags=%d",
			doc.Coverage.Flagged, len(doc.Sections[0].Items[0].Flags))
	}
}

// TestLocalizeReaderMD: a pt-BR reader never sees canonical English
// chrome — section headings, "issue N" and the AI disclosure swap to
// pt, while an English publication passes through unchanged (KLOX-56).
func TestLocalizeReaderMD(t *testing.T) {
	s := &Server{d: Deps{Cfg: &config.Config{Mode: "demo"}}}
	md := "# Futuro Próximo — issue 3\n\n## The big one\n\nFact. [1]\n\n## Worth knowing\n\nMore. [2]\n\n**Sources:** [1] A.\n\n_Prepared with AI assistance and reviewed by the editor._"

	pt := s.localizeReaderMD(md, &store.Publication{Language: "pt-BR"})
	for _, want := range []string{
		"## O destaque", "## Vale saber", " — edição 3", "**Fontes:**",
		"_Preparado com auxílio de IA e revisado pelo editor._",
	} {
		if !strings.Contains(pt, want) {
			t.Fatalf("pt body missing %q:\n%s", want, pt)
		}
	}
	if strings.Contains(pt, "The big one") || strings.Contains(pt, "AI assistance") {
		t.Fatalf("pt body leaked English:\n%s", pt)
	}
	// untouched mid-word headings stay: the swap is full-line only
	if strings.Contains(pt, "Quick links") {
		// present in input? no — but ensure no partial mangling happened
		t.Fatalf("unexpected partial heading:\n%s", pt)
	}

	en := s.localizeReaderMD(md, &store.Publication{Language: "en"})
	if en != md {
		t.Fatalf("en body mangled:\n%s", en)
	}

	// API-created publications are stored custom=TRUE while replaying
	// the canonical pipeline — the internal body carries the sample's
	// pt titles and the creator's renamed section swaps in first, so
	// nothing canonical leaks (KLOX-56, KLOX-64).
	custom := s.localizeReaderMD(
		"# Futuro Próximo — edição 3\n\n## O destaque\n\nFact. [1]\n\n## Vale saber\n\nMore. [2]\n",
		&store.Publication{Language: "pt-BR", Custom: true,
			Sections: []config.Section{
				{Title: "Meus favoritos"}, {Title: "Vale saber"}, {Title: "Links rápidos"},
			},
		})
	if !strings.Contains(custom, "## Meus favoritos") || !strings.Contains(custom, "## Vale saber") {
		t.Fatalf("creator section titles not honored:\n%s", custom)
	}
	if strings.Contains(custom, "O destaque") {
		t.Fatalf("custom pub leaked canonical heading:\n%s", custom)
	}
	// bodies written before the pt rename still carry the old canonical
	// English headings — the legacy swap keeps localizing them (KLOX-64).
	legacy := s.localizeReaderMD(md, &store.Publication{Language: "pt-BR", Custom: true,
		Sections: []config.Section{
			{Title: "Meus favoritos"}, {Title: "Vale saber"}, {Title: "Links rápidos"},
		}})
	if !strings.Contains(legacy, "## O destaque") {
		t.Fatalf("legacy canonical heading not swapped:\n%s", legacy)
	}
	if strings.Contains(legacy, "The big one") {
		t.Fatalf("legacy body leaked canonical heading:\n%s", legacy)
	}
}

// TestLocalizeSubject: pt publications render "edição N" in issue
// chrome, mail subject and export title; English stays "issue N"
// (KLOX-56 QA rework).
func TestLocalizeSubject(t *testing.T) {
	if got := localizeSubject("Futuro Próximo — issue 3", "pt-BR"); got != "Futuro Próximo — edição 3" {
		t.Fatalf("pt subject = %q", got)
	}
	if got := localizeSubject("Some Letter — issue 3", "en"); got != "Some Letter — issue 3" {
		t.Fatalf("en subject = %q", got)
	}
	if got := localizeSubject("Futuro Próximo — edição 3", "pt-BR"); got != "Futuro Próximo — edição 3" {
		t.Fatalf("already-pt subject = %q", got)
	}
}

// TestLocalizePrice: the fictional "V$" and the English period suffix
// never reach a pt reader; an en publication gets the en suffix back;
// creator-typed values without a suffix pass through (KLOX-81).
func TestLocalizePrice(t *testing.T) {
	cases := []struct{ in, lang, want string }{
		{"V$ 12/month", "pt-BR", "R$ 12/mês"},
		{"R$ 12/month", "pt-BR", "R$ 12/mês"},
		{"R$ 12/mês", "pt-BR", "R$ 12/mês"},
		{"R$ 12 per month", "pt-BR", "R$ 12 por mês"},
		{"V$ 5/month", "en", "R$ 5/month"},
		{"R$ 12/mês", "en", "R$ 12/month"},
		{"R$ 12", "pt-BR", "R$ 12"},
		{"free", "pt-BR", "free"},
		{"", "pt-BR", ""},
	}
	for _, c := range cases {
		if got := localizePrice(c.in, c.lang); got != c.want {
			t.Errorf("localizePrice(%q, %q) = %q, want %q", c.in, c.lang, got, c.want)
		}
	}
}

// TestRobotsAndSecurityHeaders: /robots.txt answers without minting a
// workspace cookie, and baseline security headers ride every response
// (KLOX-56).
func TestRobotsAndSecurityHeaders(t *testing.T) {
	srv := New(Deps{Cfg: &config.Config{Mode: "demo"}})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest("GET", "/robots.txt", nil))
	if rr.Code != 200 {
		t.Fatalf("robots = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Disallow: /api/", "Disallow: /outbox/", "Disallow: /a/"} {
		if !strings.Contains(body, want) {
			t.Fatalf("robots missing %q:\n%s", want, body)
		}
	}
	if c := rr.Header().Get("Set-Cookie"); c != "" {
		t.Fatalf("robots minted a workspace cookie: %q", c)
	}
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rr.Header().Get(k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
	if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") ||
		// frame-src covers every embed provider the sanitizer allows —
		// www.facebook.com included (KLOX-73).
		!strings.Contains(csp, "frame-src") || !strings.Contains(csp, "https://www.facebook.com") {
		t.Fatalf("CSP missing: %q", csp)
	}
}

// TestLocalizeEditorialLine: the sample's editorial line stores the
// recorded English sentence so replay prompts stay stable — pt
// publications see the pt sentence in API responses (KLOX-56).
func TestLocalizeEditorialLine(t *testing.T) {
	canonical := "Priorize adoção.\n\nSkip deals, coupons and buying guides.\n\nSem hype."
	pt := localizeEditorialLine(canonical, "pt-BR")
	if !strings.Contains(pt, "Pule ofertas, cupons e guias de compra.") {
		t.Fatalf("pt line missing swap:\n%s", pt)
	}
	if strings.Contains(pt, "Skip deals") {
		t.Fatalf("pt line leaked English:\n%s", pt)
	}
	if en := localizeEditorialLine(canonical, "en"); en != canonical {
		t.Fatal("en publication line mangled")
	}
	// a creator's own English line is never rewritten — only the
	// canonical sentence swaps
	if custom := localizeEditorialLine("Only English here.", "pt-BR"); custom != "Only English here." {
		t.Fatal("custom editorial line rewritten")
	}
}
