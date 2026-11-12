# Kloxus

**The editorial back office for independent publishers.**

> ### Live demo: https://kloxus-demo.fly.dev
>
> No install, no signup, no API keys. Each visitor gets an isolated demo
> workspace (fixture-backed, fully offline-capable code). Try the whole
> editorial loop: run the pipeline, review the story list, resolve citation
> flags, write the commentary, approve, send, then open the outbox and the
> reader/paywall views.

Kloxus is an AI agent that reads the sources a creator follows, filters out
the noise, and prepares the next issue of their publication: a ranked story
list, a source-backed dossier for each item, and a structured draft with
slots for the creator's own commentary. The creator reviews, writes what
only they can write, and sends.

> **Project status: working HackBrazil demo.** Submitted to **HackBrazil
> 2026**, track **Culture and Creative Economy**. The hosted demo runs the
> same demo-mode binary you can run locally: fictional fixture sources,
> replayed model calls, SQLite persistence, simulated email outbox and
> paywall — deterministic and free to keep up.

---

## Table of contents

- [The problem](#the-problem)
- [The solution](#the-solution)
- [Editorial principles](#editorial-principles)
- [How it works](#how-it-works)
- [What the demo enforces](#what-the-demo-enforces)
- [Running the project](#running-the-project)
- [Deploying](#deploying)
- [Implementation architecture](#implementation-architecture)
- [Estimated costs](#estimated-costs)
- [Troubleshooting](#troubleshooting)
- [Open questions](#open-questions)
- [Roadmap](#roadmap)
- [License](#license)

---

## The problem

Running an independent publication takes far more than writing. Before the
first sentence, a creator has to:

1. Monitor dozens of websites, feeds and newsletters.
2. Spot what actually matters to their audience.
3. Throw away duplicates (the same story republished across many outlets) and noise.
4. Decide what goes into the issue, in what order, and why.
5. Collect links and check facts.

Only then does the writing begin. For a solo creator with a day job, this
invisible preparation is a likely reason issues slip, frequency drops, and
publications quietly stop.

Existing tools cover pieces of this:

| Tool type | What it does | What it does not do |
| --- | --- | --- |
| Feed readers (e.g. Feedly) | Collect, filter and summarize sources | Build or send an issue |
| Newsletter platforms (e.g. Substack, beehiiv) | Write, send, charge readers; some include AI writing help | Watch your external sources and build a story list |
| General chatbots with scheduled tasks | Produce a recurring briefing from a prompt | Keep a per-publication editorial memory, link every claim to a source, deliver to readers |
| AI newsletter generators | Curate sources and draft issues in the author's voice | Mostly English-only and aimed at marketing newsletters |

Kloxus does not claim to be first. It aims to be the option built for
Portuguese-language independent creators, with source traceability and
author control as core features.

## The solution

Kloxus works as a small editorial team behind the scenes, while the creator
keeps authorship, voice and final say.

For each publication, the creator defines name, topic, audience, editorial
line, sources and sections. Kloxus then runs this pipeline for each issue:

| Step | What the creator gets |
| --- | --- |
| Collect | New items from every source since the last issue |
| Deduplicate | One entry per story, listing every outlet that covered it |
| Triage | A 0 to 10 relevance score for each item, with a one-line reason |
| Story list | 5 to 10 items grouped by theme, each with "why it matters" |
| Dossier | An original summary, facts with links, a short quote, and what is still unknown |
| Draft | The issue laid out in the creator's sections |
| Citation check | A blocking flag on any factual sentence without a source |
| Review | Approve, reject with a reason, or ask for an alternative |
| Commentary | One slot per section, written by the editor, rendered once at the end of the section |
| Delivery | Simulated email outbox, plus HTML and Markdown export for Substack or beehiiv |
| Paywall (optional) | Paid sections unlocked for subscribers via a magic link |

Every rejection (with its reason) is condensed into a short **editorial
guide** the creator can read and edit; it feeds the next run.

## Editorial principles

- **Authorship belongs to the creator.** Kloxus prepares the material. The commentary is always written by the person who signs the issue.
- **Nothing ships without approval.** Export and send are refused until the issue is approved — and approval is refused while blockers remain.
- **Every factual claim has a source.** Each factual sentence links to its origin. Unlinked sentences block sending until the editor removes or fixes them.
- **Facts and interpretation are visibly separated** in every dossier.
- **Curation is not reproduction.** Original summaries, short attributed quotes, and only sources Kloxus is allowed to read.
- **Readers' money goes to the creator.** Kloxus never receives or holds reader payments.
- **Creator data is not used to train models.**

## How it works

```
Collect → Deduplicate → Triage → Story list → Dossier → Draft → Citation check
                                                                          │
                                                                          ▼
           Paying reader ← Paywall ← Delivery ← ─ ─ ─ ─ ─ ─ Human review (approve / reject / alternative)
                                                                          │
                                                                          ▼
                                          decisions + editorial guide → next run
```

Most of the pipeline is plain automation; language models are used only
where judgment is needed (clustering, triage, story list, dossier, draft,
citation check). Costs attach to the issue and are shown on the Costs page.

## What the demo enforces

The hosted demo is not a screenshot tour — the product rules are enforced
end to end:

- **Commentary:** one slot per section (`required|optional|none` in the publication YAML), rendered once at the end of the section, flowing into preview, both exports, the email and the reader view. Helper text never appears in reader-facing output.
- **Citation flags:** each flagged sentence can be *removed* or *edited*; an edit is rechecked deterministically and must cite an existing `[n]` source or be marked interpretation. Every resolution is recorded in `decisions`.
- **Gating:** export and send are refused before approval; approval is refused while required commentary is missing, flags are unresolved, or items are still pending.
- **Output hygiene:** approved output contains no score/reason lines, no placeholders, no internal metadata, and exactly one link per source.
- **Isolation:** each visitor gets a signed-cookie workspace with its own SQLite database; a reset affects only that workspace; workspaces expire via LRU/age cleanup and workspace creation is rate-limited.
- **Safety:** the hosted build is demo-only — visitor-added sources are stored but never fetched (no SSRF), and all fixture people, institutions and outlets are fictional on reserved `.example` domains served by the app itself.

## Running the project

Requires only Go 1.25+:

```bash
go run ./cmd/kloxus demo            # seeded UI at http://localhost:8080
go run ./cmd/kloxus demo --headless # pipeline only, writes ./out/issue-N.md
go test ./...                        # full suite, fully offline
```

Local mode runs a single shared workspace. Hosted (multi-workspace) mode is
enabled by `KLOXUS_DATA_DIR` — see [docs/deploy.md](docs/deploy.md).
Real mode (Anthropic + Resend + AbacatePay + Firecrawl) is configured via
`.env`; see `.env.example`. It is not needed for the demo.

## Deploying

The demo deploys to Fly.io with one always-on machine, a `/data` volume and
an HTTP health check. Step-by-step in [docs/deploy.md](docs/deploy.md).

## Implementation architecture

Go + `html/template` + embedded SQLite (`modernc.org/sqlite`), one binary,
no CDN. Packages:

| Package | Role |
| --- | --- |
| `cmd/kloxus` | CLI entrypoint (`demo`, `serve`, `run`, `record`, `issue export/send`, `sources check`, `costs`) |
| `internal/pipeline` | collect → dedupe → triage → storylist → dossier → deepdive → draft → citecheck; phase A (review prep) / phase B (draft+check) split; alternative substitution |
| `internal/sources` | `Fetcher` interface: `FixtureFetcher` (demo), `LiveFetcher` (real); `MonitorProvider` for Firecrawl watches |
| `internal/llm` | `Provider` interface: `Anthropic` (real), `Replay` (recorded fixtures keyed by prompt hash), `Recorder` (writes fixtures) |
| `internal/editorial` | rejection-reason → editorial-guide condensation (model call + deterministic fallback) |
| `internal/delivery` | reader-facing body composition, Markdown→HTML renderer, DB-backed outbox with `.eml` export |
| `internal/store` | SQLite repositories; embedded migrations under `migrations/` |
| `internal/workspace` | per-visitor workspaces: signed cookie, per-workspace SQLite cloned from a seeded template, LRU cache, cleanup, create rate limit |
| `internal/web` | `net/http` server-rendered UI (no JS framework, no CDN) |
| `examples/` | embedded demo data: fictional feeds/pages, replayed LLM fixtures, `futuro-proximo.yaml` publication |

## Estimated costs

Demo mode replays recorded calls and reports an *estimated* USD cost per
step on the Costs page — about **$0.18** for a full issue at list prices.
Real mode reports actual token usage. Fixture regeneration requires an
Anthropic key: `kloxus record --publication futuro-proximo`.

## Troubleshooting

- **The demo URL bounces or shows a blank page after an earlier visit:**
  the app moved from `/app/` to the site root during development, and
  browsers cache the old permanent redirects (`/` → `/app/` and back),
  which can loop forever. Nothing the server sends can clear a cached
  301 — **clear site data** for `kloxus-demo.fly.dev` (or open a private
  window) and reload once. Current builds never issue a 301 on app
  paths, so a fresh visit cannot get stuck again.

## Open questions

- Which source types do creators actually need beyond RSS? (Validating via interviews.)
- Does the editorial guide meaningfully reduce review time after a few issues?
- Fair per-creator pricing once hosted usage incurs real model costs?

## Roadmap

- Scheduler + background runs (runs are currently on demand)
- OPML import and richer source types
- Real-mode polish: AbacatePay checkout, Resend delivery, Firecrawl monitors
- Multi-user auth and per-creator publications (post-pilot SaaS)

## License

MIT — see [LICENSE](LICENSE).
