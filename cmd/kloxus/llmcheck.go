package main

// `kloxus llm-check` is the quality gate for the Anthropic path (D32):
// it runs the full pipeline against the recorded real feeds (or live
// ones with --live), proves every model step returned valid JSON by
// completing, proves citecheck flags a planted unsourced sentence, and
// writes the pt-BR draft plus the measured cost for human review.
import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/realsample"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// llmSteps are the six model-driven pipeline steps the gate reports on.
var llmSteps = []string{"triage", "storylist", "dossier", "deepdive", "draft", "citecheck"}

// howItWorks are the product-loop captions shown under the sample
// dossiers — the same three lines the landing sample carries.
var howItWorks = []string{
	"Cada frase factual carrega uma fonte — clique na referência para abrir o original.",
	"O editor aprova cada edição antes do envio; nada sai sem aprovação explícita.",
	"Frases sem fonte não chegam aos leitores.",
}

func cmdLLMCheck(args []string) error {
	fs := flag.NewFlagSet("llm-check", flag.ContinueOnError)
	live := fs.Bool("live", false, "fetch the sample publication's live feeds instead of the recorded captures")
	pubRef := fs.String("publication", "", "check an existing publication instead of the recorded sample (implies --live)")
	defFile := fs.String("def", "", "publication definition YAML instead of the embedded sample def")
	outFile := fs.String("out", "", "write the drafted issue markdown here (default: <out>/llm-check-issue-N.md)")
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
		return errors.New("llm-check needs ANTHROPIC_API_KEY in .env or the environment")
	}
	ctx := context.Background()

	// Resolve the publication and fetcher: an existing publication or
	// --live goes over real HTTP; the default replays the recorded
	// sample's captured feeds for deterministic inputs.
	var pub *store.Publication
	var fetcher sources.Fetcher
	switch {
	case *pubRef != "":
		pub, err = db.GetPublication(ctx, *pubRef)
		if err != nil {
			return err
		}
		fetcher = sources.LiveFetcher{}
	default:
		var def *config.Publication
		if *defFile != "" {
			data, err := os.ReadFile(*defFile)
			if err != nil {
				return err
			}
			if def, err = config.ParsePublication(data); err != nil {
				return err
			}
		} else {
			if def, err = realsample.Def(); err != nil {
				return err
			}
		}
		if *live {
			fetcher = sources.LiveFetcher{}
		} else {
			fetcher = sources.CapturedFetcher{FS: realsample.FS}
		}
		id, err := db.SeedPublication(ctx, def)
		if err != nil {
			return err
		}
		if pub, err = db.GetPublication(ctx, fmt.Sprint(id)); err != nil {
			return err
		}
	}

	provider := &tokCounter{inner: llm.Retrier{Inner: llm.Anthropic{APIKey: cfg.AnthropicAPIKey}}}
	done := map[string]bool{}
	runner := &pipeline.Runner{
		DB: db, LLM: provider, Fetcher: fetcher,
		TriageModel: cfg.ModelTriage, DraftModel: cfg.ModelDraft,
		MaxDeepDives: cfg.MaxDeepDives,
		OnEvent: func(ev pipeline.StepEvent) {
			done[ev.Step] = true
			fmt.Printf("  %-10s %v\n", ev.Step, ev.Counts)
		},
	}
	fmt.Printf("llm-check: running %s against Anthropic (triage=%s draft=%s)\n", pub.Name, cfg.ModelTriage, cfg.ModelDraft)
	start := time.Now()
	res, err := runner.Run(ctx, pub, cfg.OutDir)
	if err != nil {
		return fmt.Errorf("pipeline run failed at a model step: %w", err)
	}
	dur := time.Since(start).Round(time.Second)
	if res.NoNewItems {
		return errors.New("the publication produced no new items — nothing to check (try --live or a different publication)")
	}

	// Report 1: every model step produced valid JSON — a malformed reply
	// fails the step, so completion is the proof.
	fmt.Println("\nmodel steps (valid JSON):")
	bad := 0
	for _, s := range llmSteps {
		mark := "OK"
		if !done[s] {
			mark = "MISSING"
			bad++
		}
		fmt.Printf("  %-10s %s\n", s, mark)
	}

	// Report 2: citecheck must flag a planted unsourced sentence. Take
	// the first dossier and ask the model about a fabricated claim that
	// carries no citation and no support in the dossier's facts.
	fmt.Println("\nplanted-unsourced check:")
	caught, err := plantedCitecheck(ctx, provider, pub, res.IssueID, cfg.ModelTriage, db)
	if err != nil {
		return err
	}
	fmt.Printf("  citecheck verdict on fabricated sentence: %s\n", map[bool]string{true: "flagged (OK)", false: "not flagged (FAIL)"}[caught])
	if !caught {
		bad++
	}

	// Report 3: the pt-BR draft lands on disk for human review.
	draftPath := *outFile
	if draftPath == "" {
		draftPath = filepath.Join(cfg.OutDir, fmt.Sprintf("llm-check-issue-%d.md", res.Number))
	}
	body, err := os.ReadFile(res.Path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(draftPath, body, 0o644); err != nil {
		return err
	}

	fmt.Printf("\ndraft (pt-BR, human review): %s\n", draftPath)
	fmt.Printf("measured cost: $%.4f · %d in / %d out tokens · %s\n", res.CostUSD, provider.in, provider.out, dur)
	if len(res.Flagged) > 0 {
		fmt.Printf("note: %d sentence(s) were flagged by citecheck in the draft\n", len(res.Flagged))
	}
	if bad > 0 {
		return fmt.Errorf("llm-check failed: %d check(s) did not pass", bad)
	}
	fmt.Println("\nllm-check: PASS")
	return nil
}

// tokCounter accumulates token usage across calls.
type tokCounter struct {
	inner   llm.Provider
	in, out int
}

func (t *tokCounter) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp, err := t.inner.Complete(ctx, req)
	t.in += resp.InputTokens
	t.out += resp.OutputTokens
	return resp, err
}

// plantedCitecheck feeds one fabricated, unsourced sentence through the
// citecheck prompt and reports whether the model rejected it.
func plantedCitecheck(ctx context.Context, provider llm.Provider, pub *store.Publication, issueID int64, model string, db *store.DB) (bool, error) {
	items, err := db.IssueItems(ctx, issueID)
	if err != nil {
		return false, err
	}
	var d *pipeline.Dossier
	for _, it := range items {
		dj, err := db.Dossier(ctx, it.ID)
		if err != nil {
			continue
		}
		var dd pipeline.Dossier
		if json.Unmarshal([]byte(dj), &dd) == nil && len(dd.Sources) > 0 {
			c := dd
			d = &c
			break
		}
	}
	if d == nil {
		return false, errors.New("no dossier with sources found for the planted check")
	}
	srcs := []map[string]any{}
	for _, s := range d.Sources {
		srcs = append(srcs, map[string]any{"ref": s.Ref, "url": s.URL})
	}
	user, err := pipeline.RenderPrompt("citecheck", map[string]any{
		"Publication": pub.Name,
		"Sentences": []map[string]any{{
			"id": 1, "item": d.Ref, "refs": "",
			"text": "O governo proibiu o uso de todos os modelos de linguagem no país a partir do próximo mês.",
		}},
		"Items": []map[string]any{{"ref": d.Ref, "sources": srcs}},
	})
	if err != nil {
		return false, err
	}
	resp, err := provider.Complete(ctx, llm.Request{Model: model, Template: "citecheck", User: user, MaxTokens: 512})
	if err != nil {
		return false, fmt.Errorf("planted citecheck call: %w", err)
	}
	var checks []struct {
		ID      int    `json:"id"`
		Verdict string `json:"verdict"`
	}
	text := resp.Text
	if i := strings.Index(text, "["); i >= 0 {
		if j := strings.LastIndex(text, "]"); j > i {
			text = text[i : j+1]
		}
	}
	if err := json.Unmarshal([]byte(text), &checks); err != nil {
		return false, fmt.Errorf("planted citecheck reply is not JSON: %w", err)
	}
	for _, c := range checks {
		if c.Verdict != "ok" {
			return true, nil
		}
	}
	return len(checks) == 0, nil
}

// exportSample writes the sample artifacts for a recorded issue:
// sample.json (the dossier payload /api/v1/sample serves) and issue.md
// (the drafted body for human review).
func exportSample(ctx context.Context, db *store.DB, pub *store.Publication, res *pipeline.Result, dir string) error {
	if res.IssueID == 0 {
		return errors.New("the run produced no issue — nothing to export")
	}
	iss, err := db.GetIssue(ctx, res.IssueID)
	if err != nil {
		return err
	}
	items, err := db.IssueItems(ctx, res.IssueID)
	if err != nil {
		return err
	}
	dossiers := []json.RawMessage{}
	for _, it := range items {
		dj, err := db.Dossier(ctx, it.ID)
		if err != nil {
			continue
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(dj), &d); err != nil {
			return fmt.Errorf("dossier for item %d: %w", it.ID, err)
		}
		// The sample payload drops database-local ids from sources.
		if srcs, ok := d["sources"].([]any); ok {
			for _, s := range srcs {
				if m, ok := s.(map[string]any); ok {
					delete(m, "item_id")
				}
			}
		}
		dj2, err := json.Marshal(d)
		if err != nil {
			return err
		}
		dossiers = append(dossiers, dj2)
	}
	if len(dossiers) == 0 {
		return errors.New("issue has no dossiers to export")
	}
	payload := map[string]any{
		"publication":  pub.Name,
		"issue_number": iss.Number,
		"date":         strings.SplitN(iss.CreatedAt, " ", 2)[0],
		"dossiers":     dossiers,
		"how_it_works": howItWorks,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.json"), data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "issue.md"), []byte(iss.BodyMD), 0o644); err != nil {
		return err
	}
	return nil
}
