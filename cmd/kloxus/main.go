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
	"path/filepath"

	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/examples"
	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/editorial"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/web"
	"github.com/sthefanocordeiro/kloxus/internal/workspace"
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
	case "llm-check":
		err = cmdLLMCheck(os.Args[2:])
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
  kloxus record --publication <ref> [--dir d]    regenerate LLM fixtures (real mode)
  kloxus record --live --def <yaml> [--dump-feeds d] [--sample-out d] [--dir d]
                                              record one real issue (feeds + LLM + sample export)
  kloxus llm-check [--live | --publication <ref>] [--def yaml] [--out file]
                                              quality gate: six model steps vs Anthropic`)
}

// localWorkspace is the fixed workspace id for single-user (local) mode:
// all CLI commands and the unhosted UI operate inside it.
const localWorkspace = "local"

func openDemoDB() (*config.Config, *store.Pool, *store.DB, error) {
	return openDB(localWorkspace)
}

func openDB(wsName string) (*config.Config, *store.Pool, *store.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	pool, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := pool.EnsureWorkspace(context.Background(), wsName); err != nil {
		pool.Close()
		return nil, nil, nil, err
	}
	return cfg, pool, pool.For(wsName), nil
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
	if cfg.LLMOff() {
		return fetcher, llm.None{}
	}
	return fetcher, llm.Replay{
		FS:      examples.FS,
		Dir:     "fixtures/llm",
		DumpDir: cfg.OutDir + "/llm-dump",
	}
}

// newGuard builds the spend guard over the durable ledger (D32/D36).
// The boot env kill flag lands as the persisted switch too.
func newGuard(cfg *config.Config, pool *store.Pool) *llm.Guard {
	g := &llm.Guard{
		Store:        pool,
		RunsPerDay:   cfg.RunsPerDay,
		TokensPerRun: int64(cfg.TokensPerRun),
		DailyCapUSD:  cfg.LLMDailyCap,
	}
	g.LoadKill(context.Background())
	if cfg.LLMKillEnv {
		_ = g.SetKill(context.Background(), true)
	}
	return g
}

// modeRuntime picks the fetcher and model provider for the configured
// mode: fixtures + replay in demo, live HTTP + Anthropic in real mode.
// KLOXUS_LLM_MODE=off swaps the provider for llm.None (fail-loud stub),
// which also frees real mode from requiring ANTHROPIC_API_KEY.
// The spend guard always exists (run admission, kill switch, admin
// surface); in real mode it also wraps the provider so every real call
// is retried, checked and ledgered.
func modeRuntime(cfg *config.Config, pool *store.Pool) (sources.Fetcher, llm.Provider, *llm.Guard, error) {
	guard := newGuard(cfg, pool)
	if cfg.Mode == "demo" {
		f, p := demoRuntime(cfg)
		return f, p, guard, nil
	}
	if cfg.LLMOff() {
		return sources.LiveFetcher{}, llm.None{}, guard, nil
	}
	if cfg.AnthropicAPIKey == "" {
		return nil, nil, nil, errors.New("real mode needs ANTHROPIC_API_KEY in .env or the environment")
	}
	guard.Inner = llm.Retrier{Inner: llm.Anthropic{APIKey: cfg.AnthropicAPIKey}}
	return sources.LiveFetcher{}, guard, guard, nil
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
	cfg, pool, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx := context.Background()
	fetcher, provider := demoRuntime(cfg)
	seed := func(ctx context.Context, sdb *store.DB) error {
		_, err := seedExample(ctx, sdb)
		return err
	}
	if !*headless {
		// The UI starts from an empty workspace — onboarding creates the
		// publication through the sample-proposal flow.
		return serveUI(cfg, pool, db, fetcher, provider, newGuard(cfg, pool), seed)
	}
	pub, err := seedExample(ctx, db)
	if err != nil {
		return err
	}
	res, err := pipeline.RunHeadless(ctx, db, pub, cfg.OutDir, fetcher, provider, cfg.ModelTriage, cfg.ModelDraft, cfg.MaxDeepDives, true, cfg.LLMOff())
	if err != nil {
		return err
	}
	printResult(res)
	return nil
}

// serveUI starts the web UI: hosted multi-workspace mode when
// KLOXUS_DATA_DIR is set (Fly.io), a single local workspace otherwise.
func serveUI(cfg *config.Config, pool *store.Pool, db *store.DB, fetcher sources.Fetcher, provider llm.Provider, guard *llm.Guard, seed func(context.Context, *store.DB) error) error {
	var monitor sources.MonitorProvider = sources.FakeMonitor{}
	if cfg.Mode == "real" && cfg.FirecrawlAPIKey != "" {
		monitor = sources.FirecrawlMonitor{
			APIKey:     cfg.FirecrawlAPIKey,
			WebhookURL: strings.TrimRight(cfg.PublicURL, "/") + "/webhooks/firecrawl",
			Secret:     cfg.WebhookSecret,
		}
	}
	var wsm *workspace.Manager
	var err error
	if cfg.DataDir != "" {
		secret := []byte(cfg.CookieSecret)
		if len(secret) == 0 {
			secret, err = workspace.LoadOrCreateSecret(filepath.Join(cfg.DataDir, "cookie.secret"))
			if err != nil {
				return err
			}
		}
		wsm, err = workspace.OpenHosted(context.Background(), pool, secret, seed)
		if err != nil {
			return err
		}
	} else {
		wsm = workspace.OpenLocal(db, seed)
	}
	srv := web.New(web.Deps{
		WS: wsm, Cfg: cfg, Fetcher: fetcher, LLM: provider, Guard: guard,
		Monitor: monitor, Seed: seed,
	})
	go srv.RunScheduler(context.Background(), pool, 20*time.Second)
	url := fmt.Sprintf("http://localhost:%d", cfg.Port)
	fmt.Printf("Kloxus UI on %s (Ctrl+C to stop)\n", url)
	go openBrowser(url)
	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return httpSrv.ListenAndServe()
}

func openBrowser(url string) {
	if os.Getenv("KLOXUS_NO_BROWSER") != "" {
		return
	}
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
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, pool, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer pool.Close()
	fetcher, provider, guard, err := modeRuntime(cfg, pool)
	if err != nil {
		return err
	}
	return serveUI(cfg, pool, db, fetcher, provider, guard, nil)
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
	cfg, pool, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx := context.Background()
	pub, err := db.GetPublication(ctx, *pubRef)
	if err != nil {
		return err
	}
	fetcher, provider, _, err := modeRuntime(cfg, pool)
	if err != nil {
		return err
	}
	res, err := pipeline.RunHeadless(ctx, db, pub, cfg.OutDir, fetcher, provider, cfg.ModelTriage, cfg.ModelDraft, cfg.MaxDeepDives, cfg.Mode == "demo", cfg.LLMOff())
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
	cfg, pool, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer pool.Close()
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
	_, pool, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer pool.Close()
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
	_, pool, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer pool.Close()
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
	cfg, pool, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx := context.Background()
	pub, err := db.GetPublication(ctx, *pubRef)
	if err != nil {
		return err
	}
	_, provider, _, err := modeRuntime(cfg, pool)
	if err != nil {
		return err
	}
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
	_, pool, db, err := openDemoDB()
	if err != nil {
		return err
	}
	defer pool.Close()
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
	defFile := fs.String("def", "", "publication definition YAML to seed before recording")
	dir := fs.String("dir", "examples/fixtures/llm", "where to write recorded fixtures")
	live := fs.Bool("live", false, "fetch real feeds over HTTP instead of fixtures")
	dumpFeeds := fs.String("dump-feeds", "", "dir to save fetched feed bodies (replayable feed fixtures)")
	sampleOut := fs.String("sample-out", "", "dir to export sample.json + issue.md into")
	wsName := fs.String("workspace", localWorkspace, "workspace to run in (a fresh one re-sees all items)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, pool, db, err := openDB(*wsName)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AnthropicAPIKey == "" {
		return errors.New("record needs ANTHROPIC_API_KEY in .env or the environment")
	}
	ctx := context.Background()
	var pub *store.Publication
	if *defFile != "" {
		data, err := os.ReadFile(*defFile)
		if err != nil {
			return err
		}
		def, err := config.ParsePublication(data)
		if err != nil {
			return err
		}
		id, err := db.SeedPublication(ctx, def)
		if err != nil {
			return err
		}
		pub, err = db.GetPublication(ctx, strconv.FormatInt(id, 10))
		if err != nil {
			return err
		}
	} else {
		if *pubRef == "" {
			return errors.New("--publication or --def is required")
		}
		var err error
		pub, err = db.GetPublication(ctx, *pubRef)
		if err != nil {
			return err
		}
	}
	var fetcher sources.Fetcher = sources.FixtureFetcher{FS: examples.FS}
	if *live {
		fetcher = sources.LiveFetcher{DumpDir: *dumpFeeds}
	}
	provider := llm.Recorder{
		Inner: llm.Retrier{Inner: llm.Anthropic{APIKey: cfg.AnthropicAPIKey}},
		Dir:   *dir,
	}
	res, err := pipeline.RunHeadless(ctx, db, pub, cfg.OutDir, fetcher, provider, cfg.ModelTriage, cfg.ModelDraft, cfg.MaxDeepDives, false, cfg.LLMOff())
	if err != nil {
		return err
	}
	printResult(res)
	fmt.Println("recorded fixtures to", *dir)
	if *sampleOut != "" {
		if err := exportSample(ctx, db, pub, res, *sampleOut); err != nil {
			return fmt.Errorf("sample export: %w", err)
		}
		fmt.Println("exported sample to", *sampleOut)
	}
	return nil
}
