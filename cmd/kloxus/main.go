// Command kloxus is the Kloxus CLI: demo, serve, run, issue, sources,
// guide, costs, record.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch cmd := os.Args[1]; cmd {
	case "demo":
		err = cmdDemo(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "issue":
		err = cmdIssue(os.Args[2:])
	case "sources":
		err = cmdSources(os.Args[2:])
	case "guide":
		err = cmdGuide(os.Args[2:])
	case "costs":
		err = cmdCosts(os.Args[2:])
	case "record":
		err = cmdRecord(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`kloxus — the editorial back office for independent publishers

usage:
  kloxus demo [--headless]                    seed the example and start the UI (--headless: pipeline only, writes ./out)
  kloxus serve                                real-mode UI (requires .env)
  kloxus run --publication <id|slug> --once   run the pipeline once
  kloxus issue export --publication <ref> --issue <n> --format markdown|html
  kloxus issue send --publication <ref> --issue <n> [--test <email>]
  kloxus sources check --publication <ref>
  kloxus guide show --publication <ref>
  kloxus costs --publication <ref> [--last n]
  kloxus record --publication <ref>           regenerate LLM fixtures (real mode)`)
}

func openDemoDB() (*config.Config, *store.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	return cfg, db, nil
}

// seedExample loads the embedded example publication and seeds it.
func seedExample(ctx context.Context, db *store.DB) (*store.Publication, error) {
	data, err := examples.FS.ReadFile("publications/futuro-proximo.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading embedded publication: %w", err)
	}
	def, err := config.ParsePublication(data)
	if err != nil {
		return nil, err
	}
	id, err := db.SeedPublication(ctx, def)
	if err != nil {
		return nil, err
	}
	return db.GetPublication(ctx, strconv.FormatInt(id, 10))
}

func cmdDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	headless := fs.Bool("headless", false, "run the pipeline once and write ./out (no UI)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	pub, err := seedExample(ctx, db)
	if err != nil {
		return err
	}
	if !*headless {
		return fmt.Errorf("web UI is not implemented yet (milestone 3); use --headless. Seeded publication %q", pub.Name)
	}
	path, err := pipeline.RunHeadlessFake(ctx, db, pub, cfg.OutDir)
	if err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

func cmdServe(args []string) error {
	return errors.New("real-mode UI is not implemented yet (milestones 3–4)")
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	pubRef := fs.String("publication", "", "publication id or slug")
	once := fs.Bool("once", false, "run a single cycle")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pubRef == "" {
		return errors.New("--publication is required")
	}
	if !*once {
		return errors.New("only --once is supported until the scheduler lands")
	}
	cfg, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	pub, err := db.GetPublication(ctx, *pubRef)
	if err != nil {
		return err
	}
	path, err := pipeline.RunHeadlessFake(ctx, db, pub, cfg.OutDir)
	if err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

func cmdIssue(args []string) error {
	if len(args) == 0 {
		return errors.New("issue subcommand required: export | send")
	}
	switch args[0] {
	case "export":
		return cmdIssueExport(args[1:])
	case "send":
		return errors.New("issue send is not implemented yet (mailer lands with real mode); use issue export")
	default:
		return fmt.Errorf("unknown issue subcommand %q", args[0])
	}
}

func cmdIssueExport(args []string) error {
	fs := flag.NewFlagSet("issue export", flag.ContinueOnError)
	pubRef := fs.String("publication", "", "publication id or slug")
	number := fs.Int("issue", 0, "issue number")
	format := fs.String("format", "markdown", "markdown | html")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pubRef == "" || *number == 0 {
		return errors.New("--publication and --issue are required")
	}
	if *format != "markdown" {
		return fmt.Errorf("format %q is not implemented yet; use markdown", *format)
	}
	cfg, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	pub, err := db.GetPublication(ctx, *pubRef)
	if err != nil {
		return err
	}
	body, err := db.IssueBody(ctx, pub.ID, *number)
	if err == sql.ErrNoRows {
		return fmt.Errorf("issue %d not found for %q", *number, pub.Name)
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return err
	}
	path := fmt.Sprintf("%s/issue-%d.md", cfg.OutDir, *number)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

func cmdSources(args []string) error {
	if len(args) == 0 || args[0] != "check" {
		return errors.New("sources subcommand required: check")
	}
	fs := flag.NewFlagSet("sources check", flag.ContinueOnError)
	pubRef := fs.String("publication", "", "publication id or slug")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *pubRef == "" {
		return errors.New("--publication is required")
	}
	_, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	pub, err := db.GetPublication(ctx, *pubRef)
	if err != nil {
		return err
	}
	srcs, err := db.Sources(ctx, pub.ID)
	if err != nil {
		return err
	}
	if len(srcs) == 0 {
		fmt.Println("no sources configured")
		return nil
	}
	for _, s := range srcs {
		status := "never fetched"
		if s.LastStatus.Valid {
			status = s.LastStatus.String
		}
		fmt.Printf("%-4s  %-45s  %s\n", s.Type, s.URL, status)
	}
	fmt.Println("(fetching and validation land in milestone 2)")
	return nil
}

func cmdGuide(args []string) error {
	if len(args) == 0 || args[0] != "show" {
		return errors.New("guide subcommand required: show")
	}
	fs := flag.NewFlagSet("guide show", flag.ContinueOnError)
	pubRef := fs.String("publication", "", "publication id or slug")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *pubRef == "" {
		return errors.New("--publication is required")
	}
	_, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	pub, err := db.GetPublication(ctx, *pubRef)
	if err != nil {
		return err
	}
	version, content, err := db.LatestGuide(ctx, pub.ID)
	if err == sql.ErrNoRows {
		fmt.Println("(empty — the guide is generated from decisions after the first reviewed issue)")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("editorial guide v%d\n\n%s\n", version, content)
	return nil
}

func cmdCosts(args []string) error {
	fs := flag.NewFlagSet("costs", flag.ContinueOnError)
	pubRef := fs.String("publication", "", "publication id or slug")
	last := fs.Int("last", 4, "number of issues to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pubRef == "" {
		return errors.New("--publication is required")
	}
	_, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	pub, err := db.GetPublication(ctx, *pubRef)
	if err != nil {
		return err
	}
	rows, err := db.CostReport(ctx, pub.ID, *last)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no runs recorded yet")
		return nil
	}
	fmt.Printf("%-6s %-10s %-22s %8s %8s %10s\n", "issue", "step", "model", "in", "out", "cost")
	cur := -1
	var totIn, totOut int
	var totCost float64
	flush := func() {
		if cur >= 0 {
			fmt.Printf("%-6d %-10s %-22s %8d %8d %10s\n", cur, "(total)", "", totIn, totOut, fmt.Sprintf("$%.4f", totCost))
		}
		totIn, totOut, totCost = 0, 0, 0
	}
	for _, r := range rows {
		if r.IssueNumber != cur {
			flush()
			cur = r.IssueNumber
		}
		m := r.Model
		if m == "" {
			m = "-"
		}
		fmt.Printf("%-6d %-10s %-22s %8d %8d %10s\n", r.IssueNumber, r.Step, m, r.InputTokens, r.OutputTokens, fmt.Sprintf("$%.4f", r.CostUSD))
		totIn += r.InputTokens
		totOut += r.OutputTokens
		totCost += r.CostUSD
	}
	flush()
	return nil
}

func cmdRecord(args []string) error {
	return errors.New("record lands with the Anthropic provider (milestone 4)")
}
