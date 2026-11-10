// Command kloxus is the Kloxus CLI: demo, serve, run, issue, sources,
// guide, costs, record.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/editorial"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
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

// demoRuntime builds the fixture-backed fetcher and replay provider used
// by demo-mode runs.
func demoRuntime(cfg *config.Config) (sources.Fetcher, llm.Provider) {
	fetcher := sources.FixtureFetcher{FS: examples.FS}
	provider := llm.Replay{
		FS:      examples.FS,
		Dir:     "fixtures/llm",
		DumpDir: cfg.OutDir + "/llm-dump",
	}
	return fetcher, provider
}

func printResult(res *pipeline.Result) {
	fmt.Printf("collected %d items → %d stories → %d selected\n", res.Items, res.Stories, res.Selected)
	fmt.Printf("estimated model cost: $%.4f\n", res.CostUSD)
	if len(res.Flagged) > 0 {
		fmt.Printf("citation check flagged %d sentence(s):\n", len(res.Flagged))
		for _, f := range res.Flagged {
			fmt.Printf("  - item %d: %s (%s)\n", f.Item, f.Sentence, f.Note)
		}
	}
	fmt.Println("wrote", res.Path)
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
	fetcher, provider := demoRuntime(cfg)
	if !*headless {
		return serveUI(cfg, db, fetcher, provider, func(ctx context.Context) error {
			_, err := seedExample(ctx, db)
			return err
		})
	}
	res, err := pipeline.RunHeadless(ctx, db, pub, cfg.OutDir, fetcher, provider, cfg.ModelTriage, cfg.ModelDraft, cfg.MaxDeepDives)
	if err != nil {
		return err
	}
	printResult(res)
	return nil
}

// serveUI starts the local web UI and opens the browser.
func serveUI(cfg *config.Config, db *store.DB, fetcher sources.Fetcher, provider llm.Provider, reseed func(ctx context.Context) error) error {
	var monitor sources.MonitorProvider = sources.FakeMonitor{}
	if cfg.Mode == "real" && cfg.FirecrawlAPIKey != "" {
		monitor = sources.FirecrawlMonitor{
			APIKey:     cfg.FirecrawlAPIKey,
			WebhookURL: strings.TrimRight(cfg.PublicURL, "/") + "/webhooks/firecrawl",
			Secret:     cfg.WebhookSecret,
		}
	}
	srv := web.New(web.Deps{
		DB: db, Cfg: cfg, Fetcher: fetcher, LLM: provider,
		Monitor: monitor, Mailer: delivery.Outbox{Dir: "outbox"},
		Reseed: reseed,
	})
	addr := fmt.Sprintf(":%d", cfg.Port)
	url := fmt.Sprintf("http://localhost:%d", cfg.Port)
	fmt.Printf("Kloxus UI on %s (Ctrl+C to stop)\n", url)
	go openBrowser(url)
	return http.ListenAndServe(addr, srv)
}

func openBrowser(url string) {
	time.Sleep(500 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
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
	fetcher, provider := demoRuntime(cfg)
	res, err := pipeline.RunHeadless(ctx, db, pub, cfg.OutDir, fetcher, provider, cfg.ModelTriage, cfg.ModelDraft, cfg.MaxDeepDives)
	if err != nil {
		return err
	}
	printResult(res)
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
	if len(args) == 0 || (args[0] != "show" && args[0] != "rebuild") {
		return errors.New("guide subcommand required: show | rebuild")
	}
	if args[0] == "rebuild" {
		return cmdGuideRebuild(args[1:])
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

func cmdGuideRebuild(args []string) error {
	fs := flag.NewFlagSet("guide rebuild", flag.ContinueOnError)
	pubRef := fs.String("publication", "", "publication id or slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pubRef == "" {
		return errors.New("--publication is required")
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
	_, provider := demoRuntime(cfg)
	content, version, err := editorial.Rebuild(ctx, db, pub, provider, cfg.ModelTriage)
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
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	pubRef := fs.String("publication", "", "publication id or slug")
	dir := fs.String("dir", "examples/fixtures/llm", "where to write recorded fixtures")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pubRef == "" {
		return errors.New("--publication is required")
	}
	cfg, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer db.Close()
	if cfg.AnthropicAPIKey == "" {
		return errors.New("record needs ANTHROPIC_API_KEY in .env or the environment")
	}
	ctx := context.Background()
	pub, err := db.GetPublication(ctx, *pubRef)
	if err != nil {
		return err
	}
	fetcher := sources.FixtureFetcher{FS: examples.FS}
	provider := llm.Recorder{
		Inner: llm.Anthropic{APIKey: cfg.AnthropicAPIKey},
		Dir:   *dir,
	}
	res, err := pipeline.RunHeadless(ctx, db, pub, cfg.OutDir, fetcher, provider, cfg.ModelTriage, cfg.ModelDraft, cfg.MaxDeepDives)
	if err != nil {
		return err
	}
	printResult(res)
	fmt.Println("recorded fixtures to", *dir)
	return nil
}
