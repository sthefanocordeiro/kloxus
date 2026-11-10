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

## FIXED — PhaseB replay-key instability (2026-11-10)

Two separate issues made the UI draft request miss fixtures:

1. `Dossier.Title` was added to persisted JSON so PhaseB could reload the
   storylist-picked title, but PhaseA also serialized that field into the
   deepdive prompt. That changed the deepdive request key from `465c2c…`
   to `d0fec090…`. The prompt copy now clears `Title` before marshaling;
   the stored dossier keeps it.
2. PhaseB built draft/citecheck input from only non-rejected items, so
   arbitrary review decisions produced arbitrary replay keys. In replay
   mode only, PhaseB now sends the canonical all-selected entry set to
   draft and citecheck, while the stored issue body and citation flags are
   built/filtered from non-rejected items. Real mode still sends only the
   active items. See decision D15.

The temporary `79754cea…` fixture (a response copied to a request produced
by incomplete/mixed state) was removed; `ab783555…` is the intended draft
fixture again.

Verified with an ignored `.devin` HTTP harness through the real
`web.Server`: PhaseA → reject items 2/3/6/8 (one with a guide reason) →
alternative 7 → draft twice → active body contains refs 1/4/5/7 only →
planted item-1 flag appears → commentary + dismiss flag clears blockers →
approve → markdown/HTML export → paid/unpaid reader view → send to outbox.
`go vet ./...`, `go test ./...`, and `demo --headless` all pass; headless
result is still 35 items → 32 stories → 8 selected → 1 planted flag,
~$0.1814.

## Deployment/submission prep added (2026-11-10)

- `/healthz` endpoint; optional `KLOXUS_DEMO_RESET_INTERVAL` (Go duration)
  resets+reseeds demo data in-process. Fly config uses `24h`.
- Demo outbox now lives under `KLOXUS_OUT_DIR/outbox`, so it persists on
  the same volume as the DB/output.
- `Dockerfile`, `.dockerignore`, `fly.toml`: Go 1.27 build → Debian slim
  non-root runtime, `/data` volume, always-on `http_service`, health check.
- `.github/workflows/release.yml`: tag `v*` → Linux/macOS Intel/macOS ARM/
  Windows binaries → GitHub Release via `gh`. Cross-compile verified for
  Linux and Windows locally.
- `.gitignore` binary patterns are anchored to the repo root so
  `cmd/kloxus` is no longer hidden from ignored-file-aware tools.
- README now documents the working demo, deployment, current architecture;
  `docs/submission.md` has EN/PT copy, deploy checklist, video shot list.

## Also verify next session

- Hosted browser pass after deployment (same judge loop + `/healthz`).
- First tag release upload (workflow is written but not yet executed).
- Kill stray `kloxus.exe`/`:8080` before re-running (`taskkill //IM kx.exe //F`).
- Untracked leftovers to keep out of git: `server.log`, `out/`, `kloxus.db`,
  `.devin/repro*`, `.devin/headless*`, `.devin/release-check/`.

## Timeline reminder

Deadline Nov 30 23:59. Remaining owner actions: deploy to Fly/VPS (see
`docs/submission.md`), verify hosted judge loop, record English ≤3min
video, submit ~Nov 28. Firecrawl /monitor = sponsor feature, already
demo-visible on Sources page.
