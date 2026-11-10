// Package pipeline runs the per-issue editorial pipeline. Milestone 1
// ships a fake headless run that exercises the wiring end to end;
// the real steps (sources, dedupe, llm) land in milestone 2.
package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// Steps is the canonical pipeline order, shared with the UI's progress display.
var Steps = []string{
	"collect", "dedupe", "triage", "storylist", "dossier", "deepdive", "draft", "citecheck",
}

// RunHeadlessFake executes a placeholder pipeline: it records a run row per
// step, creates an issue with a structurally-correct markdown body, and
// writes it to outDir. Replaced step by step by the real pipeline in M2.
func RunHeadlessFake(ctx context.Context, db *store.DB, pub *store.Publication, outDir string) (string, error) {
	number, err := db.NextIssueNumber(ctx, pub.ID)
	if err != nil {
		return "", err
	}

	body := fakeIssueBody(pub, number)
	subject := fmt.Sprintf("%s — issue %d", pub.Name, number)
	issueID, err := db.CreateIssue(ctx, pub.ID, number, subject, body)
	if err != nil {
		return "", err
	}

	for _, step := range Steps {
		start := time.Now()
		if err := db.RecordRun(ctx, store.Run{
			PublicationID: pub.ID,
			IssueID:       issueID,
			Step:          step,
			DurationMS:    time.Since(start).Milliseconds(),
		}); err != nil {
			return "", err
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(outDir, fmt.Sprintf("issue-%d.md", number))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func fakeIssueBody(pub *store.Publication, number int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — issue %d\n\n", pub.Name, number)
	b.WriteString("_M1 skeleton output: wiring only. Fixtures, dedupe, model steps and " +
		"citation checking land in milestone 2._\n\n")
	for _, s := range pub.Sections {
		fmt.Fprintf(&b, "## %s\n\n", s.Title)
		for i := 1; i <= s.Items; i++ {
			fmt.Fprintf(&b, "- [ ] story placeholder %d\n", i)
		}
		switch s.Commentary {
		case "required":
			b.WriteString("\n> Your commentary goes here (required — sending is blocked while this slot is empty)\n\n")
		case "optional":
			b.WriteString("\n> Optional commentary slot\n\n")
		}
	}
	b.WriteString("---\n\n## Sources\n\n_(none — source fetching lands in milestone 2)_\n")
	return b.String()
}
