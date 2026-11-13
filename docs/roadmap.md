# Kloxus — implementation roadmap

Scope: the HackBrazil 2026 submission. Deadline **Nov 30, 23:59** — repo
link, **hosted demo URL (must stay up through evaluation)**, and a
**≤3-minute video** (no login, English). Design decisions live in
[decisions.md](decisions.md); product context in the root README.

**Direction changed on 2026-11-12 (D27–D34):** we ship a hosted real app —
real sources, real model calls, real email, Postgres — instead of the
zero-setup fixture demo. Judges are not required to run anything locally.

**Direction changed again on 2026-11-13 (D37):** after USD 7.86 of
Anthropic spend, the owner froze all paid LLM use until a measured budget
is approved and deferred AI deduplication/analysis/generative drafting to
the post-hackathon roadmap. The MVP is now **no-paid-AI**: ingestion +
deterministic normalization + canonical-URL/exact-hash dedup + manual
compose + delivery, with the spend kill switch live in prod.

Constraints that still hold: nothing is sent without human approval, the AI
never writes the commentary, every factual sentence carries a source, and
publisher rules (robots, terms) are respected without workarounds. The fake
providers stay as the test bed and as the runtime fallback.

One issue and one PR per milestone; each milestone lists its exit test.

## Evaluation surfaces (per submission rules §7)

1. **Hosted real app** — new Fly app + Supabase Postgres. Landing page with
   the live cited sample, "Get started without login".
2. **Fallback URL** — the tagged fixture demo stays deployed, in case a
   real dependency fails during evaluation.
3. **Video** — ≤3 min, English, recorded by the owner; YouTube unlisted.
4. **Code** — the repo itself.

## Done

- [x] Phase 0: `.gitignore`, `.env.example`, `docs/decisions.md`.
- [x] M1 skeleton: CLI, config, SQLite + embedded migrations, fake
  headless demo, CI on 3 OSes. (commit `f1055db`)
- [x] M2 real pipeline on fixtures. (commit `3e0c49e`)
- [x] M3 web UI + M4a Firecrawl monitor backend. (WIP commit `1a6830c`)
- [x] PhaseB replay determinism + judge-loop verification. (2026-11-10;
  see `docs/devlog.md`)
- [x] M6b prep: README run/deploy docs, `docs/submission.md`, tag-based
  binary release workflow, Dockerfile/fly.toml for the hosted demo.
- [x] v2 M1–M5: `/api/v1`, React editor, approve & send, reader pages,
  onboarding. (D21–D26)

## R1 — One stack (Nov 13–14)

- Tag the current demo (`demo-fixture-v2`) and keep it deployed as the
  fallback URL.
- Delete the classic htmx UI; keep the reader page, webhooks and the fake
  providers. Port the Go tests that go through classic handlers to
  `/api/v1`. Add the frontend build and tests to CI (review F7). Fix the
  README.

**Exit:** no htmx route or template left; CI builds and tests Go and React.

## R2 — Landing page (Nov 13–16, parallel with R1)

- Home page inside the React app: what Kloxus is, how the loop works, the
  cited sample as the centrepiece, sponsors used, "Get started without
  login" into the existing anonymous workspace.

**Exit:** a first-time visitor reaches a workspace in one click, EN + pt-BR.

## R3 — Postgres on Supabase (Nov 14–17)

- [x] Port the store and migrations to Postgres; `workspace_id` on every
  tenant table; drop the per-visitor SQLite files. Jobs queue on
  `FOR UPDATE SKIP LOCKED`; nightly `pg_dump` workflow; idle-workspace
  deletion. (D28)

**Exit:** full test suite green on Postgres by **Nov 17**. If not, fall back
to SQLite + Litestream and move on.

## R4 — Real data and real runs (Nov 17–21)

- **Source catalog and global ingestion** — seeded catalog with policy,
  scheduler, conditional GET, robots re-check, shared `items`, fetch log;
  Daily Journal as a signal source. (D29, D31)
- **LLM guards and second provider** — run budgets, daily cap, kill switch,
  retries, fallback to the recorded sample; OpenAI-compatible provider.
  (D32)
- **Resend and workspace claim** — magic-link confirmation, caps, real
  delivery of approved issues. (D33)

**Exit:** a new anonymous workspace picks catalog sources, runs a real
issue from today's news, claims the workspace by email and receives the
approved issue in a real inbox.

## R4d — No-paid-AI MVP (Nov 13–21) — owner directive, D37

- **Spend containment** — PR #17 (KLOX-10) kill switch + $1/day global cap
  + 2 runs/day/workspace deployed to `kloxus` inside a serialized release
  window; `KLOXUS_MODE=demo` as the pre-deploy kill path. Hosted verify:
  `GET /api/v1/admin/llm` → `kill: true`.
- **Deterministic run mode** (KLOX-31) — zero model calls; deterministic
  rank; canonical-URL/exact-hash dedup; AI endpoints return 409.
- **Manual compose** (KLOX-32) — human fills lineup items with cited
  excerpts; no AI-written text.
- **Parked (not deleted):** AI story grouping, analysis, generative
  drafting. Returns only as opt-in, capped, behind the deployed kill
  switch with owner-approved budget.

**Exit:** `llm_ledger` shows zero new paid `call` rows after the freeze;
the full loop (ingest → dedupe → manual compose → send) runs with zero
Anthropic spend.

## R5 — Discovery and checkout (Nov 21–24)

- **Discover a topic** — query expansion + Firecrawl Search + Daily Journal
  search, capped per workspace. (D30)
- **AbacatePay test-mode checkout** for the paid section. (D34)

**Exit:** typing a niche returns dated candidate stories with sources; a
reader unlocks the paid section through a test-mode payment.

## Freeze (Nov 25)

Bug fixes only. Verify caps, kill switch and the fallback URL.

## Video (Nov 26–28) — owner-produced

- ≤3 min, English. Shot-list: ~15s problem → ~60s prepare run → ~60s
  review (reject + reason → guide updates → approve) → ~30s export/send +
  paid-section unlock → ~15s close.

## Submit (Nov 28) → buffer Nov 29–30

Submit early; the last version within the deadline counts.

## Cut order if we slip

1. AbacatePay (keep the fake provider).
2. Firecrawl monitoring (keep search and scrape only).
3. Topic discovery.

The real pipeline and the landing page are never cut.

## Post-hackathon — validation & SaaS path (Dec 2026+)

- Pilot with up to 5 real publications; go/no-go (Feb 2027).
- SaaS evolution: Kloxus subscription billing, creator-owned AbacatePay
  accounts (D8), semantic search (Exa or pgvector over our own items).
- Parked MVP scope (D37): AI story grouping, AI analysis and generative
  drafting — return only behind the deployed kill switch with an
  owner-approved budget.

## Risks

- **Schedule.** About 11–14 engineer-days of work in 13 calendar days.
- **Five external dependencies during evaluation** (Supabase, the LLM
  provider, Resend, Firecrawl, Daily Journal). Each has a fake or a recorded
  fallback; the fixture demo stays up as the fallback URL.
- **LLM spend and quality.** Anonymous visitors trigger real calls; caps and
  the kill switch are mandatory. The zero-cost provider must pass the
  quality gate in D32 or we stay on Anthropic with a tighter cap.
- **Free tiers.** Supabase pauses after one idle week (the scheduler keeps
  it awake) and has no backups (nightly dump). Resend allows 100 emails a
  day.
- **Publisher goodwill.** Four launch sources block AI crawlers by name; we
  read their public feed and publish summaries with links. Ask each for an
  explicit OK, and get Daily Journal's permission in writing.
