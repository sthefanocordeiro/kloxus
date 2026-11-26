# Kloxus

**The research is ready. The writing is yours.**

> ### Live app — https://kloxus.fly.dev
>
> One email magic link — no password — and you have a **Radar**: the app
> reads the sources every day, matches today's stories to your beat, and
> hands you a ready brief. You write the take and export to LinkedIn, X,
> HTML or Markdown.
>
> A deterministic fixture demo runs locally with no API keys:
> `go run ./cmd/kloxus demo`.

Kloxus automates the daily news-triage workflow for people who publish
about a sector every day — on LinkedIn, X, a blog, a newsletter or an
internal channel. One shared engine polls an audited catalog of
Brazilian sources, drops duplicates, matches each story to the user's
Radar with embeddings and a reranker, and rebuilds the day's stories
every morning. The person picks the stories and writes; Kloxus turns
that one text into LinkedIn, X-thread, HTML and Markdown versions. It
never writes the commentary.

> **HackBrazil 2026** — track *Logística & Automação* (Logistics and
> Automation). This is the
> hosted real app: a curated catalog of 31 audited feeds — 21 currently
> active — Postgres on
> Supabase, real magic-link email via Resend, one Go binary on Fly.io.
> The reasoning lives in
> [docs/decisions.md](docs/decisions.md) (D27 onward).

---

## For the jury — 2 minutes

- **App:** https://kloxus.fly.dev · **Repo:** this repository ·
  **Video:** being recorded for the submission (see
  [docs/submission.md](docs/submission.md))
- **Try it:** e-mail → magic link → create a Radar → Match →
  **Today's stories** → click 3 cards → **Build brief** → write →
  **Export** → **Copy**.

**What is automated**

- scheduled global polling of the audited source catalog
- robots.txt and publisher-terms checks per source
- dedupe — canonical URL + SimHash near-duplicate text
- one embedding pass per item at ingest
- two-stage per-Radar ranking (cosine vs the Rocchio profile, then a
  Voyage rerank of the top candidates)
- learning from every Yes/No calibration and 👍/👎
- the 06:30 daily build of each Radar's stories
- four output formats rendered from fixed templates — no model involved
- the 07:00 morning email carrying the day's stories *(on in production)*

**Real production numbers** — production Postgres and the LLM ledger,
queried 2026-11-26 (UTC), nothing estimated:

| Metric | Value |
| --- | --- |
| Audited sources active / total | 21 / 31 |
| Items collected per day | ~2,450 |
| Folded into existing stories by dedupe | 3.6% of items |
| Stories per Radar per day | ~6.5 |
| Build brief time | ~2–3 s per Radar |
| Voyage embedding cost per day | ≈US$0.005 average |

The same engine fits team clipping and market-intelligence briefs.

---

## The product, end to end

One loop, five screens — cadastro → Radar → notícias → escrever → exportar:

| Step | What happens |
| --- | --- |
| **Sign up** | E-mail in, magic link out (Resend). No password, no form beyond the address. |
| **Create your Radar** | Name it and say what you cover. Pick among **18 editorias** — from *política* and *economia* to *tecnologia*, *agronegócio* and *cultura*. Kloxus assigns the curated sources for you; one Radar per account. |
| **Match** | A short calibration set — real stories from the last 48 h. *Yes* / *No* on each one teaches the Radar your sector. |
| **Today's stories** | Every morning, a set of stories from the feed catalog matched to your Radar — outlet and timestamp on each card. Click a card to pick it for the brief; 👍 / 👎 keeps training the match. On days with new stories the assembled brief also arrives by email at 7 a.m. — every send carries a one-click unsubscribe *(on in production)*. |
| **Write → Export** | The *pauta*: an optional opening plus one autosaved text box per story — your words only. Export to **4 formats**: LinkedIn post (3,000 chars), X post (280, auto thread split), rich-text HTML for newsletter/blog/intranet — with image upload in the editor and a `.html` download — or a `.md` download ready for Ghost, Notion and Obsidian. |

The interface speaks pt-BR and EN, and the morning email follows
the account language. A claimed Radar also gets the day's stories by email
on mornings with new stories — assembled at ~06:30, in the inbox by
7 a.m. (America/Sao_Paulo), with a one-click unsubscribe in every send.
*(The 7 a.m. send is on in production.)*

<p align="center">
  <img src="docs/screenshots/01-landing.png" width="720" alt="Landing — The research is ready. The writing is yours.">
</p>
<p align="center">
  <img src="docs/screenshots/03-radar.png" width="720" alt="Create your Radar — name, topics and the 18 beats">
</p>
<p align="center">
  <img src="docs/screenshots/04-match.png" width="720" alt="Match — real stories, yes/no calibration">
</p>
<p align="center">
  <img src="docs/screenshots/06-today.png" width="720" alt="Today's stories — real Brazilian outlets matched to the Radar">
</p>
<p align="center">
  <img src="docs/screenshots/07-write.png" width="720" alt="Write — an autosaved text box under each story">
</p>
<p align="center">
  <img src="docs/screenshots/08-export.png" width="720" alt="Export — LinkedIn, X, HTML and Markdown renditions with live counters">
</p>

## Editorial principles

- **Source rules come first.** Only catalog feeds whose terms allow this
  use are fetched — robots.txt is re-checked daily, per-source policies
  (full text vs links-only) are enforced, and refused sources stay
  refused. The source audit and the accept/reject list live in
  [docs/decisions.md](docs/decisions.md) (D29, D67).
- **Authorship belongs to the writer.** Kloxus finds, dedupes and matches; it never writes the commentary. The editor only holds the writer's own text.
- **Every story keeps its source.** Each card and each exported line links back to the outlet that published it.
- **Curation is not reproduction.** Headlines, short excerpts and links — never a republication of someone else's article.
- **The human decides what ships.** Nothing leaves Kloxus without an explicit export by the user, and sources are only the ones the catalog allows (robots and publisher rules respected, no workarounds).
- **Writer data is not used to train models.**
- **Budget is a feature, not a hope.** Paid calls sit behind per-run token/USD ceilings, a shared daily cap, a per-account run cap and a live kill switch — and the core loop keeps working with all of them off.

## How it works

```
                 curated feed catalog (21 active of 31 audited: g1, InfoMoney,
                  Agência Brasil, Finsiders, Tecnoblog, Olhar Digital…)
                                │  global poll · conditional GET ·
                                │  robots-aware · one fetch for all
                                ▼
        clean + normalize ──▶ dedupe (canonical URL + SimHash
                                      near-duplicate text)
                                │
                                ▼
        embed once at ingest (Voyage; deterministic hash classifier
        and full-text search as zero-cost fallbacks)
                                │
        ┌───────────────────────┴────────────────────────┐
        ▼                                                ▼
  each Radar: theme + 18 editorias               daily stories ("Hoje"),
  → match → rerank → learn from                  pick → brief → export
    every Yes/No and thumbs                      in 4 formats
```

- **Ingestion** is global: one scheduler polls the shared catalog for every
  workspace — N users still mean one request per source per interval, and
  new items are classified and embedded once for everyone.
- **Matching** is per-Radar: the beat (plus its editorias) is the query,
  story-level *Yes* / *No* / thumbs feedback is the supervision signal.
- Generative AI (Claude) is enabled in production under hard caps and a
  live kill switch; matching uses Voyage embeddings and reranking, and
  the AI never writes the commentary. `KLOXUS_LLM_MODE=off` runs the
  whole loop deterministically at $0.

## Stack

| Layer | Choice |
| --- | --- |
| Backend | Go 1.27 single binary — `net/http` JSON API under `/api/v1`, `pgx` Postgres driver, embedded migrations, in-process feed scheduler |
| Frontend | React 19 + TypeScript + Vite 7, Tailwind v4, Radix, Tiptap — built to `internal/web/appdist` and embedded with `go:embed` (no CDN, no Node in production) |
| Data | Postgres on Supabase (pgvector when available, TEXT fallback otherwise); every tenant row is `workspace_id`-scoped |
| Matching | Voyage embeddings + reranker; deterministic feature-hash classifier and Postgres FTS as zero-cost fallbacks |
| Generative | Anthropic Claude (Haiku/Sonnet) — optional, capped, behind a live kill switch |
| Mail | Resend — magic links + the morning email *(on in production)*, delivery/bounce webhooks; local outbox transport without a key |
| Ops | Fly.io (`gru`), one always-on machine so the scheduler never sleeps; health check on `/healthz` |
| Optional | Firecrawl — on-demand page scrape/watch for user-added sources (configured in production, daily scrape cap); legacy publishing surface (reader pages, PIX checkout, sending) dormant behind `KLOXUS_PUBLISHING=off` (D64) |

## Running locally

Requires Go 1.27+, Node 20.19+ (Vite 7) and any Postgres 16+:

```bash
export DATABASE_URL=postgres://postgres:password@localhost:5432/kloxus
(cd frontend && npm ci && npm run build)   # React app → internal/web/appdist, embedded by Go
go run ./cmd/kloxus demo                   # UI at http://localhost:8080 — offline, no API keys
```

`demo` runs the deterministic pipeline against fixture sources: the whole
flow works end to end (signup, Radar, match, today's stories, brief,
export) and emails land in the local outbox instead of a provider.
Migrations auto-apply on boot. `go test ./...` runs the Go suite against
the same `DATABASE_URL`; `npm test` / `npm run typecheck` / `npm run lint`
cover the frontend.

Real mode — `go run ./cmd/kloxus serve` — reads `.env` (see
`.env.example`): live feeds, Resend, Voyage/Anthropic keys, spend caps.
`npm run dev` in `frontend/` is the Vite dev server proxying `/api` to
:8080.

## Documentation

| Doc | What it covers |
| --- | --- |
| [docs/architecture.md](docs/architecture.md) | The pipeline end to end and the *why* of each choice |
| [docs/code-review-quality.md](docs/code-review-quality.md) | Consolidated engineering review — every finding and its disposition |
| [docs/decisions.md](docs/decisions.md) | The running decision log (D1–D60+) |
| [docs/api.md](docs/api.md) | The `/api/v1` contract |
| [docs/deploy.md](docs/deploy.md) | Fly.io deploys, release windows, the demo fallback |
| [docs/roadmap.md](docs/roadmap.md) | Milestones and what's next |
| [docs/audits/](docs/audits/) | Independent product audits and pre-freeze test rounds |

## Repository layout

| Path | Role |
| --- | --- |
| `cmd/kloxus` | CLI: `demo`, `serve`, `run`, `ingest`, `record`, `issue`, `sources`, `guide`, `costs`, `coverage` |
| `internal/web` | `net/http` server: `/api/v1`, embedded SPA, claim/magic-link, webhooks, outbox |
| `internal/pipeline` | the live flow: `BuildStories` (dedupe/cluster) → `Pauta` (stage-1 rank + MMR) → brief; the generative issue stages `dossier → deepdive → draft → citecheck` are inactive legacy |
| `internal/ingest` | the global feed scheduler, robots-aware fetcher, retention |
| `internal/rank`, `internal/rerank`, `internal/embed`, `internal/embedding` | the matching engine: FTS + hash classifier + Voyage |
| `internal/store` | Postgres repositories and the migrator (`migrations/`) |
| `internal/delivery` | rendition builders, Markdown→HTML, the mail ledger/outbox |
| `internal/sources`, `internal/llm`, `internal/voyage` | provider seams (fetchers, models, embeddings) |
| `frontend/` | the only UI — React app, its tests and e2e specs |
| `scripts/` | smoke/e2e harnesses, fake Resend, deploy helpers |
| `examples/` | embedded fixture world for `demo` (fictional outlets, replayed calls) |

## Deploying

`fly.toml` deploys the real app (`kloxus`); `fly.demo.toml` deploys the
fixture fallback (`kloxus-demo`) — an unqualified `fly deploy` can never
hit the wrong app. Step-by-step, release windows and the backup job in
[docs/deploy.md](docs/deploy.md).

## License

PolyForm Strict 1.0.0, with an additional permission for HackBrazil 2026
organizers and judges to read, run, test and modify the software solely
to evaluate it — see [LICENSE.md](LICENSE.md).
