package web

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

// fetchFunc adapts a function to sources.Fetcher.
type fetchFunc func(ctx context.Context, typ, url string) ([]sources.RawItem, error)

func (f fetchFunc) Fetch(ctx context.Context, typ, url string) ([]sources.RawItem, error) {
	return f(ctx, typ, url)
}

// TestShutdownCancelsAndDrainsRuns: a pipeline run in flight is tied to
// the server's lifecycle — Shutdown cancels its context (the fetch below
// only unblocks on cancellation) and waits for the goroutine to report
// itself done, instead of exiting mid-run.
func TestShutdownCancelsAndDrainsRuns(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "kloxus.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Name:     "Shutdown Test",
		Slug:     "shutdown-test",
		Sections: []config.Section{{Title: "Main", Items: 1, Commentary: "none"}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feed.xml"}},
	})
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, strconv.FormatInt(pubID, 10))
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	wsm := workspace.OpenLocal(db, nil)

	fetchCtx := make(chan context.Context, 1)
	srv := New(Deps{
		WS:  wsm,
		Cfg: &config.Config{Mode: "demo", ModelTriage: "t", ModelDraft: "d", MaxDeepDives: 1},
		Fetcher: fetchFunc(func(ctx context.Context, _, _ string) ([]sources.RawItem, error) {
			select {
			case fetchCtx <- ctx:
			default:
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		Monitor: sources.FakeMonitor{},
	})
	st := srv.startRun(wsm.Local(), pub)
	var runCtx context.Context
	select {
	case runCtx = <-fetchCtx:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not reach the fetcher")
	}
	shCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := runCtx.Err(); err != context.Canceled {
		t.Fatalf("run ctx err = %v, want context.Canceled", err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.st.Done {
		t.Fatalf("run not drained after shutdown: %+v", st.st)
	}

	// an idle server shuts down immediately
	if err := New(Deps{}).Shutdown(ctx); err != nil {
		t.Fatalf("idle Shutdown: %v", err)
	}
}

// TestRemoveSentenceDropsCitationMarker: removing a flagged sentence
// must take its trailing "[n]" too — no orphaned citation left behind.
func TestRemoveSentenceDropsCitationMarker(t *testing.T) {
	body := "### H <!-- item 1 -->\n\nO custo segue três vezes o da banda larga urbana. [1] Outra afirmação sem fonte."
	out := removeSentence(body, "O custo segue três vezes o da banda larga urbana. [1]")
	if strings.Contains(out, "[1]") {
		t.Fatalf("citation marker left behind: %q", out)
	}
	if !strings.Contains(out, "Outra afirmação sem fonte.") {
		t.Fatalf("kept sentence lost: %q", out)
	}

	// flag recorded without the marker (period-then-citation form)
	out = removeSentence(body, "O custo segue três vezes o da banda larga urbana.")
	if strings.Contains(out, "[1]") {
		t.Fatalf("orphaned citation left behind: %q", out)
	}

	// the sentence's terminal punctuation goes with it — no ". ." stray
	out = removeSentence("Duas pediram prazo maior [1]. Especialistas divergem.",
		"Duas pediram prazo maior [1]")
	if strings.Contains(out, ". .") || !strings.HasPrefix(out, "Especialistas divergem.") {
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
