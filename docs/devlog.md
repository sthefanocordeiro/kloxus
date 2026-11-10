# Devlog — session of 2026-11-09

Handoff notes for the next session. Read with `docs/roadmap.md` and
`docs/decisions.md`.

## Done and committed

- `a96c932` Phase 0: `.gitignore`, `.env.example`, `docs/decisions.md`, `docs/roadmap.md`.
- `f1055db` M1 skeleton: CLI, SQLite + embedded migrations, fake headless demo, CI (3 OSes).
- `3e0c49e` M2 real pipeline on fixtures: `internal/sources` (fixture fetcher RSS+HTML),
  `internal/dedupe` (canonical URL + SimHash), `internal/llm` (Provider iface,
  Anthropic, Replay, Recorder), `internal/pipeline` (7 embedded prompts,
  full run), `internal/editorial` (guide rebuild + deterministic fallback).
  `demo --headless` → 35 items → 32 stories → 8 selected → issue with
  flagged planted claim, ~$0.18 cost. All replay fixtures authored and
  committed under `examples/fixtures/llm/`.

## Committed as WIP (this session)

M3 web UI + M4a Firecrawl monitor backend:

- `internal/web`: full server (Go 1.22 mux patterns), templates in
  `internal/web/templates/` (layout + all pages, two-step render: page →
  buffer → layout; dynamic `{{template .Page}}` does NOT exist in
  html/template — that's why two-step), static `app.css`/`app.js` (vanilla,
  no htmx — vendoring needs download; noted).
- Pages wired: publications, overview, settings(readonly), sources
  (+monitor badge + Check-now + Watch-via-Firecrawl), run (PhaseA async +
  JSON status polling), story review (approve/reject+reason/alternative,
  reject triggers `editorial.Rebuild`), dossier, draft editor (commentary
  forms, flags dismiss, blockers), approve (blocks on empty required
  commentary / unresolved flags), send/export, guide (save/rebuild),
  costs, paywall (Simulate reader payment + reader view with gated
  sections), `/demo/reset`, `/webhooks/firecrawl` (secret verified,
  `monitor.page` → `SaveItems` via `FindSourceByMonitor`).
- `internal/sources/monitor.go`: `MonitorProvider` iface + `FakeMonitor`
  (demo badge) + `FirecrawlMonitor` (v2 API: Watch/Check/Unwatch).
- `internal/delivery`: Outbox mailer (.eml/.html) + markdown→HTML renderer.
- Pipeline split: `RunPhaseA` (collect→deepdive, issue=`reviewing`) /
  `RunPhaseB` (draft→citecheck→`drafted`, rebuilds entries from DB) /
  `finish`. `Runner.OnStep` for progress. Issue created before triage so
  model runs attach to it; `runs.issue_id`/NULL handled; CostReport LEFT JOIN.
- `kloxus demo` (no --headless) serves UI + opens browser (`serveUI` in
  main.go; monitor switches fake/real by mode+key). `guide rebuild` CLI added.
- Migration 0003: `sources.monitor_provider/monitor_id/monitor_status`.
- Config: `PublicURL`, `WebhookSecret`. Store: full web repo set
  (`internal/store/web.go`) + `Reset`, `SetIssueStatus`, `StoryTitle`,
  `ResolveFlags`, reader/subscription helpers, FK-suspending `ClearStories`.

## OPEN BUG — fixture key instability in PhaseB (UI draft)

`POST /issue/{id}/draft` fails: draft request key keeps changing per
invocation (seen keys: `ab783555`[orig headless], `24fe9f2e`, `79754cea`,
`20d8b549`). Root cause found via diffing dumps in `out/llm-dump/`:
**the drItems list differs between calls** — one request had Item 2 =
"Universidade libera modelo..." while another jumped to Item 4. So
`loadEntries` (pipeline.go ~line 500) returns a different entry set
sometimes. Suspects:

1. A second `POST /pub/{id}/run` ran PhaseA again in the same db →
   `ClearStories` wiped stories → story ids/refs changed → stored dossiers'
   refs belong to a different generation. Check whether loadEntries is
   mixing dossiers across issues/runs.
2. Rejected-item filtering interacting with `d.Ref` numbering.
3. `IssueItems` ORDER BY score then re-sorted by id — verify.

Steps to fix: reproduce with a fresh db → PhaseA → PhaseB twice, capture
both requests, diff; then make PhaseB state fully deterministic (e.g.,
persist entries/refs, or key fixtures on stable input). Then **re-author
draft (+citecheck) fixtures to the final key** (same response text, new
filename — or fix the input so the original `ab783555` key is restored).
IMPORTANT: replay reads fixtures from the embedded FS — **rebuild the
binary after adding fixture files** (burned ~15 min on this).

## Also verify next session

- Full judge loop in browser (reject→guide update→approve→export).
- `gateSections` correctness; `page-sent` nav; `page-reader`.
- CI still runs headless demo on 3 OSes (should pass — headless Run()
  unchanged externally).
- Kill stray `kloxus.exe`/`:8080` before re-running (`taskkill //IM kx.exe //F`).
- Untracked leftovers to keep out of git: `server.log`, `out/`, `kloxus.db`.

## Timeline reminder

Deadline Nov 30 23:59. Remaining: fix bug → verify judge loop → M6a
hosted deploy (fly.io/VPS) → M6b release/docs/submission → video (owner,
English, ≤3min) → submit ~Nov 28. Firecrawl /monitor = sponsor feature,
already demo-visible on Sources page.
