# Kloxus

**The research is ready. The writing is yours.**

> ### Live app: https://kloxus.fly.dev
>
> Sign up with your email — a magic link, no password — and create your
> Radar in about a minute. A deterministic fixture build stays deployed
> at https://kloxus-demo.fly.dev as the offline-capable fallback.

Kloxus is the editorial back office for people who publish every day.
You say what you cover once; Kloxus keeps watching your sources, ranks
the stories that matter to your niche, and lines them up every morning —
each with a link to the original article. You pick the stories, write
the piece in your own voice, and export it to wherever your audience is.

The AI never writes your text. It does the boring work — reading,
deduplicating, ranking — before you open the editor.

> **HackBrazil 2026** submission, track **Culture and Creative Economy**.

---

## Table of contents

- [The product](#the-product)
- [The flow, end to end](#the-flow-end-to-end)
- [Product rules](#product-rules)
- [Stack](#stack)
- [Running locally](#running-locally)
- [Testing](#testing)
- [Deploying](#deploying)
- [Documentation](#documentation)
- [License](#license)

---

## The product

Independent creators lose the invisible hours before writing: monitoring
feeds, discarding duplicates, deciding what matters, hunting links.
Kloxus does that work and stops exactly where authorship begins.

- **One Radar per account.** Your email is the account — confirmed by a
  magic link, no password.
- **18 editorial beats (editorias).** Pick any of the 18 built-in desks —
  Política, Economia, Mercado financeiro, Finanças pessoais, Negócios,
  Startups, Carreira, Tecnologia, Agronegócio, Internacional,
  Brasil e cidades, Justiça e direito, Saúde, Educação, Ciência,
  Meio ambiente, Cultura, Estilo de vida — plus your own topics, and
  Kloxus maps them to a curated catalog of sources (G1, InfoMoney,
  Agência Brasil, Finsiders, Mundo RH, Money Times, and more) audited
  for automated-reading rules. Import your own feeds via OPML.
- **Notícias de hoje.** Every day, a deck of ranked stories — deduplicated
  across outlets, scored against your Radar's profile and everything your
  Sim/Não feedback taught it. You always choose; the thumbs only teach.
- **Escrever.** The picked stories become a pauta: an opening plus one
  text slot per story, autosaved as you type, every story linking back
  to its source.
- **Exportar, 4 formats.** Each destination gets its own editable copy:
  **Post LinkedIn** (plain text, 3,000 chars), **Post X** (automatic
  280-char thread split), **Newsletter / blog / intranet** (rich HTML),
  **Markdown** (Ghost, Notion, Obsidian). Copy or download — the piece
  is marked exported per format, and editing it afterwards flags the
  older copies as stale until refreshed.

## The flow, end to end

| | |
| --- | --- |
| ![Landing — "A pauta pronta. A escrita é sua."](docs/screenshots/01-landing.png) | ![Criar seu Radar — name, topics and the 18 editorias](docs/screenshots/03-cadastro-radar.png) |
| ![Notícias de hoje — ranked deck, pick your stories](docs/screenshots/07-today-picks.png) | ![Escrever — opening plus one slot per story, autosaved](docs/screenshots/08-escrever.png) |
| ![Exportar — LinkedIn, X, HTML and Markdown destinations](docs/screenshots/09-exportar.png) | ![Dar match — Sim/Não calibration teaches the Radar](docs/screenshots/04-match.png) |

More shots in [docs/screenshots/](docs/screenshots/).

## Product rules

- **Authorship belongs to the creator.** Kloxus researches and organizes;
  every word of the piece is written by the person who signs it.
- **The human always picks the stories.** The ranking suggests; the deck
  is yours to accept, skip, or hide a whole topic from.
- **Every story links to its source.** Each pauta entry carries the
  original article link — in the editor and in every export.
- **Publisher rules are respected.** Sources enter the catalog only after
  an audit of their robots.txt and terms; stories that opt out are never
  fetched.
- **Nothing leaves without you.** Export is copy/download by the author;
  there is no auto-publish.
- **Spend is capped in code.** Per-run and per-day token/USD ceilings
  plus a kill switch guard every paid call — the deterministic pipeline
  runs with zero model spend (`KLOXUS_LLM_MODE=off`).

## Stack

| Layer | What it is |
| --- | --- |
| Backend | Go 1.25+ (`net/http`), one binary, no CDN — the whole app embeds with `go:embed` |
| Frontend | React 19 + TypeScript + Vite, TipTap editor, Tailwind 4 — built to `internal/web/appdist` |
| Data | Postgres (`pgx`), embedded migrations under `migrations/` — Supabase in production |
| Ranking | Voyage embeddings (stage 1) + reranker (stage 2) over a Rocchio profile built from your feedback; deterministic FTS fallback when no key/cap |
| LLM | Anthropic, behind per-run/per-day cost caps and a kill switch; `Replay` provider replays recorded calls in tests |
| Mail | Resend (magic-link sign-in); outbox `.eml` transport in demo/dev |
| Hosting | Fly.io (`gru`) — `kloxus` is the real app, `kloxus-demo` the fixture fallback |

The deep dive — pipeline diagram, data model, ranking engine, spend
guards and the decision log behind each choice — lives in
[docs/architecture.md](docs/architecture.md) and
[docs/decisions.md](docs/decisions.md).

## Running locally

Requires **Go 1.25+**, **Node 20.19+** and a **Postgres** database
(local Postgres, Docker, Supabase — anything that speaks the protocol).

```bash
(cd frontend && npm ci && npm run build)   # React app → internal/web/appdist
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/kloxus
go run ./cmd/kloxus demo                   # seeded UI at http://localhost:8080
```

`kloxus demo` runs the full product offline: fixture sources, replayed
model calls, `.eml` outbox for the magic link — no API keys needed.
`kloxus serve` is the deployed entrypoint; it picks demo or real runtime
from `KLOXUS_MODE` and `.env` (see [.env.example](.env.example)).

## Testing

```bash
go test ./...                              # Go suite (Postgres-backed tests need DATABASE_URL)
(cd frontend && npm test)                  # Vitest unit tests
(cd frontend && npx playwright test)       # e2e: drives the real flow against kloxus demo
```

`scripts/smoke.sh <base-url>` runs the production smoke checklist
against a deployed target. Postgres-backed tests skip when
`DATABASE_URL` is unset — CI always provides a disposable database.

## Deploying

The real app deploys to Fly.io (`fly.toml`, app `kloxus`); the fixture
demo is a separate app (`fly.demo.toml`, `kloxus-demo`). Migrations are
embedded and checksum-verified on boot. Step-by-step, secrets and the
backup routine: [docs/deploy.md](docs/deploy.md).

## Documentation

| Doc | What it covers |
| --- | --- |
| [docs/architecture.md](docs/architecture.md) | Pipeline diagram, package map, ranking engine, spend guards — the "why" behind the build |
| [docs/code-review-quality.md](docs/code-review-quality.md) | Consolidated quality review: findings from four code reviews and their resolution |
| [docs/decisions.md](docs/decisions.md) | The running decision log (D1–D69+) — every trade-off, dated |
| [docs/roadmap.md](docs/roadmap.md) | Milestones and the HackBrazil submission plan |
| [docs/api.md](docs/api.md) | The `/api/v1` JSON contract |
| [docs/deploy.md](docs/deploy.md) | Fly.io deploy, secrets, backups, demo app |
| [docs/eval/](docs/eval/) | Ranking evaluation labels used to tune the engine |

## License

MIT — see [LICENSE](LICENSE.md).
