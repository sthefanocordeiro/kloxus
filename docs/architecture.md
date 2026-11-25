# Architecture

How Kloxus is built, and why — the map for reading this repository cold.
Product framing lives in the [README](../README.md); every dated call
below links into the running log in [decisions.md](decisions.md).

## The pipeline

Two loops share one Postgres database: a **global ingestion loop** that
polls sources once for every workspace, and a **per-issue run** that
turns the shared pool into one reviewed edition.

```mermaid
flowchart TB
    subgraph Global["Global ingestion — internal/ingest (D29)"]
        CAT["source_catalog<br/>full-text RSS + signal sources<br/>policy per source: launch | signal | hold | blocked"]
        CAT --> SCH["scheduler<br/>per-source interval · robots re-check · host throttle<br/>candidates bounded to 7 days / 60 per source"]
        SCH --> POOL[("items + item_embedding<br/>shared, workspace-agnostic pool")]
    end

    subgraph Run["Per-issue run — internal/pipeline"]
        COL["collect<br/>catalog picks + the workspace's own sources"] --> DD["dedupe<br/>canonical URL + SimHash"]
        DD --> RNK["triage = ranking<br/>stage 1: embedding score vs Rocchio profile, FTS fallback<br/>stage 2: Voyage rerank of the top 100"]
        RNK --> STL["storylist<br/>ranked stories for the board"]
        STL --> DOS["dossier + deepdive<br/>facts and quotes with [n] sources"]
        DOS --> REV{"human review<br/>approve / reject / swap, with reasons"}
        REV --> DRF["draft + citecheck<br/>factual sentences without a source get flagged"]
        DRF --> APR{"human approval<br/>stored receipt"}
        APR --> OUT["export — renditions<br/>fixed per-destination templates"]
        APR -. "send — publishing flag + explicit audience" .-> MAIL["Resend email<br/>confirmed readers only"]
    end

    POOL --> COL
    REV -. "rejection reasons" .-> GDE["editorial guide"]
    GDE -. "feeds the next run" .-> RNK
```

Phase A (prepare) runs collect through deepdive and stops for the human
review; Phase B (draft + citecheck) runs only on the reviewed lineup.
Approving re-runs the readiness checks and stores a receipt — document
version, checks, timestamp (D21) — and post-approval edits void it.
Export forks the approved text into per-destination renditions through
fixed templates with no model involved (`internal/web/renditions.go`);
sending requires the approved issue plus an explicit `test | readers`
audience and only ever mails confirmed addresses (D33).

Every model-dependent step has two implementations behind the same seam:
a guarded Anthropic call and a deterministic, zero-cost path.
`KLOXUS_LLM_MODE` picks the posture — `auto` allows real calls inside
hard caps, `off` runs the whole pipeline deterministically (D35). Under
the current owner freeze on paid AI (D37/D40) the deployed app takes the
deterministic path.

## Stack, and why

| Piece | Choice | Why |
| --- | --- | --- |
| Backend | Go (`net/http`), one binary | SPA, prompts, migrations and demo data are `go:embed`-ed into a single artifact — no Node server or CDN in production, tests run fully offline (D22). |
| Front end | React 19 + TypeScript + Vite → `internal/web/appdist` | One JSON contract (`/api/v1`, see [api.md](api.md)): the server parses and owns the issue document, the client only displays state and invokes mutations (D21, D22). |
| Database | Postgres on Supabase Free via `pgx`, embedded migrations, optional pgvector | A shared schema with `workspace_id` on tenant tables replaced one SQLite file per visitor; used as plain Postgres only (no Supabase auth/RLS/functions) so a single SQL dialect suffices (D28). `item_embedding` stores a real `vector(256)` when pgvector exists and an identical TEXT literal otherwise (D57). |
| Hosting | Fly.io — `kloxus` app (`fly.toml`, gru) + `kloxus-demo` (`fly.demo.toml`) | The feed scheduler runs inside the web process, so one machine stays up (a fully stopped app would stop ingesting). The fixture demo is the fallback URL and is never touched by deploys (D36). |
| Model calls | Anthropic behind `llm.Provider` (`internal/llm`) | `Anthropic`, `Replay` (recorded fixtures), `Recorder` and `None` share one interface, so the test bed and demo cost nothing and real calls are a config choice. Every call passes prompt-size caps, a per-run USD cap, a global daily cap and a kill switch (D32, D40). |
| Embeddings + rerank | Voyage AI (`voyage-4-lite`, `rerank-2.5-lite`) plus a deterministic local embedder | `internal/voyage` is a thin HTTP client with per-group rate limits, hard daily USD caps and a cost ledger (D60). `internal/embedding` is a free feature-hashing embedder (256-dim) + lexicon classifier that needs no model call and works with or without pgvector (D57). Stage-2 rerank runs once per run over the stage-1 top 100, cached 24 h, fail-soft to the stage-1 order (D63). |
| Email | Resend (`internal/delivery`) | Sending-only key restricted to the sender domain; confirmed addresses only; global and per-workspace daily caps sized under the free tier (D33). |
| Payments | AbacatePay PIX (`internal/payments`) | Readers pay the creator — Kloxus never holds money (D8). A one-time PIX charge grants 30 days of access because PIX has no recurring rail in test mode (D34, D58). |
| Fetch + search | Firecrawl (`internal/sources`) | Sponsor credits; monitors/scrapes sources without RSS and backs topic discovery, always inside the source policy (D14, D30). |

## Standing rules

- **The AI never writes the commentary.** The only editable region of an
  issue is the author's own text; AI output is atom content in the
  document schema, not user-editable prose.
- **Every factual sentence carries a source.** Citation markers (`[n]`)
  are produced with the text and re-checked deterministically;
  unsupported sentences are flagged and block approval.
- **Nothing is sent without human approval.** Approval requires all
  readiness checks green; send and rendition are refused on anything but
  an approved issue.
- **Publisher rules are respected.** Per-source catalog policies,
  robots.txt re-checked daily, no mirrors or endpoint workarounds, and a
  custom-URL SSRF guard (D29).
- **Zero paid AI is the current posture.** The owner froze paid model
  use after ~USD 7.86 of spend (D37, hardened into code caps by D40):
  real ingestion and the deterministic/manual-compose loop carry the
  MVP, and AI grouping/analysis/generative drafting are parked behind
  the deployed kill switch — not deleted.

## Decisions D27–D36

The pivot to the hosted real app, compressed — full text and rationale
in [decisions.md](decisions.md):

| # | Decision (gist) | Status |
| --- | --- | --- |
| D27 | Submission target is a hosted real app — real sources, model calls, email, Postgres. React becomes the only UI; fake providers stay as test bed and runtime fallback; the fixture demo stays deployed as the fallback URL. | Active; scoped by the D37 freeze and the KLOX-122 publishing flag |
| D28 | Storage moves to Postgres on Supabase Free, plain Postgres only, one shared schema keyed by `workspace_id`. | Active — supersedes D1/D10 |
| D29 | Ingestion splits from editorial work: feeds are fetched once, globally, from a seeded `source_catalog` with a policy per source; robots re-checked daily; anonymous workspaces pick catalog-only. | Active |
| D30 | Topic discovery = query expansion + keyword search over the catalog (Firecrawl Search; Daily Journal API per D31). | Partially dormant — the DJ half is off (see D31) |
| D31 | Daily Journal joins as a `signal` source via its public API only, with a hard daily request cap. | Reversed — D41 removed it, D57 restored it, D61 removed it again; the client stays in the tree dormant, opt-in off |
| D32 | Real LLM calls are guarded: per-workspace budget, global daily cap, kill switch, retries, panic recovery; a second provider fits the same `Provider` interface. | Active; caps hardened by D40 |
| D33 | Email via Resend with a sending-only restricted key, confirmed addresses only, daily caps; a magic-link claim is the auth model. | Active |
| D34 | Reader checkout demonstrates the paywall with a single Kloxus test-mode AbacatePay key. | Amended by D58 — one-time PIX granting 30 days |
| D35 | `KLOXUS_LLM_MODE=off` runs a documented deterministic pipeline at $0. | Active — the posture the app runs under the D37 freeze |
| D36 | Every PR to `main` deploys the shared `kloxus` Fly app and is verified by a fixed routine, serialized through release windows (KLOX-26 ledger); `kloxus-demo` is never touched. | Active |

## Reading the code

- `internal/ingest` — global scheduler, feed fetching, robots, the
  dormant Daily Journal API client.
- `internal/pipeline` — the run: `stage_collect`, `stage_dedupe`,
  `stage_triage`, `stage_storylist`, `stage_dossier`, `stage_deepdive`,
  `stage_draft`, `stage_citecheck`; `stage1.go` + `rank.go` are the
  deterministic ranking path.
- `internal/dedupe`, `internal/rank`, `internal/rerank`, `internal/embed`,
  `internal/embedding`, `internal/voyage` — free dedupe and the
  two-stage ranking engine.
- `internal/store` — Postgres repositories, workspace-scoped, embedded
  migrations under `migrations/`.
- `internal/web` — the `/api/v1` server: board, review, approve/send
  gates, renditions, reader surface.
- `internal/llm`, `internal/editorial`, `internal/delivery`,
  `internal/payments` — providers, the editorial guide, mail and PIX.
- `frontend/` — the React SPA built into `internal/web/appdist`.
