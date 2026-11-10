# Kloxus — implementation roadmap

Scope: the HackBrazil 2026 MVP. Submission deadline **Nov 30, 23:59** —
repo link, **hosted demo URL (must stay up through evaluation)**, and a
**≤3-minute video** (no login, English). Design decisions live in
[decisions.md](decisions.md); product context in the root README.

Key constraints (non-negotiable): zero setup locally
(`go run ./cmd/kloxus demo` with only Go installed), no external database,
no API keys in demo mode, no CDN, nothing sent without human approval, the
AI never writes the commentary.

One PR per milestone; each milestone lists its exit test.

## Evaluation surfaces (per submission rules §7)

Judges are not required to install or run anything. What they evaluate:

1. **Hosted demo** — the demo-mode binary deployed to fly.io/VPS, seeded
   with the example publication and replayed fixtures. Deterministic,
   ~$0–5/mo, stays up into December. Nightly reset + in-UI "Reset demo".
2. **Video** — ≤3 min, English, recorded by the owner against a shot-list
   we provide; YouTube unlisted.
3. **Code** — the repo itself; the zero-setup story is still the
   engineering signal and the same artifact we deploy.

## Done

- [x] Phase 0: `.gitignore`, `.env.example`, `docs/decisions.md`.
- [x] M1 skeleton: CLI, config, SQLite + embedded migrations, fake
  headless demo, CI on 3 OSes. (commit `f1055db`)

## M2 — Real pipeline on fixtures (Nov 10–19) → PR 2

The bulk of the work. Internal order:

1. **Fixture feeds** — 25–40 fictional original items across 4 fictional
   outlets, deliberate duplicates (literal republishes + same-event
   rewrites) and off-topic noise.
2. **`internal/sources`** — fixture fetcher (RSS via gofeed, page via
   `x/net/html`), canonical URL, robots.txt interface stub.
3. **`internal/dedupe`** — canonical URL + SimHash near-duplicate
   grouping into `stories`.
4. **`internal/llm`** — provider interface; Anthropic client (needed now:
   fixtures are produced from real calls — see D4); replay provider keyed
   by hash(model, template, normalized input), failing loudly and dumping
   the request on a missing key.
5. **Pipeline steps** — triage (batched, structured JSON, retry once) →
   storylist (5–10 items, themed, "why it matters") → dossier (summary,
   facts `[n]`, quote, unknowns, labeled interpretation) → deepdive
   (≤3 items; batched call in demo, tool loop in real mode) → draft
   (sections + empty commentary slots) → citecheck (model maps sentences;
   deterministic rule blocks unsourced facts).
6. **Editorial guide** — regenerated from decisions; deterministic
   append fallback in demo mode when no fixture matches.
7. **Cost tracking** — tokens + cost in `runs`.

**Exit:** `demo --headless` produces a believable issue fully offline.

## M3 — Web UI (Nov 19–24) → PR 3

- Server-rendered `html/template` + embedded htmx. All 11 pages:
  publications, settings, sources (with `robots.txt` validation), run
  with live progress, story-list review (approve/reject+reason/
  alternative), dossier view, draft editor with flagged sentences,
  send/export, paywall, editorial guide, costs.
- `kloxus demo` starts the server, opens the browser.
- Demo-only "Reset demo" button (also serves the hosted instance).

**Exit:** a judge completes the full loop — prepare, reject with reason,
see guide update, approve, export — in under 5 minutes.

## M6a — Hosted demo (Nov 24–26)

- `fly launch` (or VPS + systemd + Caddy): TLS, always-on, stable URL.
- Nightly `kloxus.db` wipe + reseed; health endpoint; uptime check.
- No secrets on the instance (demo mode has none).

## M6b — Release & docs (Nov 25–26)

- GoReleaser (or Actions matrix) binaries: Windows, macOS Intel + ARM,
  Linux.
- Rewrite README "Running the project" + architecture table; update
  demo-page status; screenshots; `docs/submission.md` (EN+PT tagline and
  description ready to paste).

## Video (Nov 26–28) — owner-produced

- ≤3 min, English. Shot-list: ~15s problem → ~60s prepare run → ~60s
  review (reject + reason → guide updates → approve) → ~30s export/send +
  paid-section unlock → ~15s close.

## Submit (Nov 28) → buffer Nov 29–30

Submit early; the last version within the deadline counts.

## M4a — Firecrawl monitor (Nov 24–26)

- `MonitorProvider` interface; simulated "monitored via Firecrawl" badge
  + Check-now in demo; real `/v2/monitor` client, `monitor.page` webhook
  receiver with secret verification, and polling fallback for localhost.
- Page sources only (no crawl/website monitors). Decision D14.

## M4 — Real mode / M5 — Paywall (post-deadline / if ahead)

- M4: RSS/page fetching with `robots.txt` + `KloxusBot` UA + rate limits,
  `go-readability`, Firecrawl optional, `record` command, Resend mailer.
- M5: AbacatePay test mode, magic links, webhook verification +
  polling fallback, AES-256-GCM key encryption.
- The demo must remain complete without these.

## Post-hackathon — validation & SaaS path (Dec 2026+)

- Pilot with up to 5 real publications; go/no-go (Feb 2027).
- SaaS evolution (where Postgres/Supabase belongs): same binary deployed
  centrally → per-tenant storage → Kloxus subscription billing.

## Risks

- **Fixture quality gates the demo.** Iterate prompts against the real
  API before recording; thin fixtures make the demo look fake.
- **Hosted demo availability.** Sleeping free tiers and Supabase
  free-tier pauses can take it down mid-evaluation — avoided by D10.
- **M2 scope creep.** If it slips past Nov 19, cut UI polish in M3,
  never pipeline features.
