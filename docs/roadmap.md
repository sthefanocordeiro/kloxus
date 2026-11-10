# Kloxus — implementation roadmap

Scope: the HackBrazil 2026 MVP. Hard deadline **Nov 27, 2026** for the offline
demo plus release artifacts. Design decisions live in
[decisions.md](decisions.md); product context in the root README.

Key constraints (non-negotiable): zero setup (`go run ./cmd/kloxus demo` with
only Go installed), no external database, no API keys in demo mode, no CDN,
nothing sent without human approval, the AI never writes the commentary.

One PR per milestone; each milestone lists its exit test.

## Phase 0 — Hygiene & decisions (Nov 9–10)

- [x] `.gitignore`: `.env`, `*.db`, `outbox/`, `out/`, key files.
- [x] `.env.example` with every supported variable.
- [x] `docs/decisions.md` seeded (SQLite-over-Postgres, replay keying, etc.).
- [ ] `go mod init`, `cmd/kloxus` CLI skeleton with stub subcommands.
- [ ] GitHub Actions skeleton.

Parallel track: creator interviews continue (see README open questions).

## M1 — Skeleton (Nov 10–12) → PR 1

- `internal/config`: env + `.env` + YAML publication loading and validation.
- `internal/store`: SQLite via `modernc.org/sqlite`, `go:embed` migrations,
  schema for `publications`, `sources`, `items`, `stories`, `story_items`,
  `issues`, `issue_items`, `dossiers`, `decisions`, `editorial_guides`,
  `readers`, `subscriptions`, `payment_accounts`, `jobs`, `runs`.
- `kloxus demo --headless`: fake pipeline writes `./out/issue-1.md`.
- CI: `go vet`, `go test ./...`, headless demo on Linux, macOS, Windows.

**Exit:** clean clone → `go run ./cmd/kloxus demo --headless` produces a file.

## M2 — Real pipeline on fixtures (Nov 12–19) → PR 2

The bulk of the work (~60% of effort). Internal order matters:

1. **Fixture feeds** — 25–40 fictional original items across 3–4 fictional
   outlets, with deliberate duplicates and off-topic noise. Parallelizable
   with schema work.
2. **Dedupe** — canonical URL + SimHash near-duplicate grouping into `stories`.
3. **`internal/llm` provider interface + Anthropic client** — needed now
   (not M4) because replay fixtures are produced by real calls. See D4.
4. **Replay provider** — keyed by hash(model, prompt template, normalized
   input); fails loudly naming a missing key.
5. **Pipeline steps** — triage (batches ≤80 items, structured JSON, retry
   once on invalid) → storylist (5–10 items, themed, "why it matters") →
   dossier (summary, facts `[n]`, quote, unknowns, labeled interpretation) →
   deepdive (only agentic step; ≤3 items, ≤5 tool calls each) → draft
   (creator's sections, empty commentary slots) → citecheck (model maps
   sentences to sources; deterministic rule blocks unsourced facts).
6. **Editorial guide** — regenerated (≤ ~1,500 tokens) from decisions after
   each issue; sent with every triage/draft call.
7. **Cost tracking** — input/output tokens + estimated cost in `runs`.

**Exit:** `demo --headless` produces a believable issue fully offline.

## M3 — Web UI (Nov 19–24) → PR 3

- Server-rendered `html/template` + embedded htmx. All 11 pages:
  publications, settings, sources (with `robots.txt` validation), run with
  live progress, story-list review (approve/reject+reason/alternative),
  dossier view, draft editor with flagged sentences, send/export, paywall,
  editorial guide, costs.
- `kloxus demo` starts the server and opens the browser.
- Demo-mode "Simulate reader payment" (fake provider) lands here — it is
  UI-trivial and completes the demo narrative without waiting for M5.

**Exit:** a judge completes the full loop — prepare, reject with reason, see
guide update, approve, export — in under 5 minutes.

## M6 — Release & docs (Nov 24–26) → PR 4

Deadline-bound, pulled ahead of M4/M5:

- GoReleaser (or Actions matrix) binaries: Windows, macOS Intel + ARM, Linux.
- Rewrite README "Running the project" + architecture table
  (PostgreSQL → SQLite); update demo-page status; screenshots.
- **Clean-machine rehearsal:** fresh clone, `go mod download`, disconnect,
  run the definition-of-done checklist end to end.

Buffer: Nov 26–27.

## M4 — Real mode (post-deadline / if ahead) → PR 5

- RSS (`gofeed`) and page fetching: `robots.txt` check, `KloxusBot/0.1` UA,
  per-host rate limiting, `go-readability` extraction, Firecrawl optional.
- `record` command polish; Resend mailer.
- The demo must remain complete without M4/M5 — they are enhancements.

## M5 — Paywall (post-deadline / if ahead) → PR 6

- AbacatePay test mode, magic links for readers (no passwords), webhook
  verification + "check payment status" polling fallback (webhooks cannot
  reach localhost), AES-256-GCM key encryption via `KLOXUS_MASTER_KEY`.

## Post-hackathon — validation & SaaS path (Dec 2026+)

- Pilot with up to 5 real publications; measure story-list retention and
  time saved per issue → go/no-go decision (Feb 2027).
- If continuing, SaaS evolution path: same binary deployed centrally →
  per-tenant storage → Kloxus's own subscription billing (distinct from
  reader payments, which stay direct-to-creator).

## Risks

- **Fixture quality gates the demo.** Iterate prompts against the real API
  before recording; thin fixtures make the demo look fake.
- **M2 sequencing.** Anthropic client precedes replay fixtures; `record`
  belongs to M4 only as CLI polish.
- **Model IDs.** Spec names are placeholders (D5); verify at integration.
- **M2 scope creep.** If it slips past Nov 19, cut UI polish in M3, never
  pipeline features.
