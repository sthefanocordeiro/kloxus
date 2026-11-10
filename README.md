# Kloxus

**The editorial back office for independent publishers.**

Kloxus is an AI agent that reads the sources a creator follows, filters out the noise, and prepares the next issue of their publication: a ranked story list, a source-backed dossier for each item, and a structured draft with empty slots for the creator's own commentary. The creator reviews, writes what only they can write, and sends.

> **Project status: working HackBrazil demo.**
> The MVP runs locally with only Go installed: fixture-backed sources, replayed model calls, SQLite persistence, review UI, editorial guide, export/send outbox, and a simulated paywall. The examples below are product copy; the demo itself is deterministic and offline.
>
> Submitted to **HackBrazil 2026**, track **Culture and Creative Economy**.

---

## Table of contents

- [The problem](#the-problem)
- [The solution](#the-solution)
- [Editorial principles](#editorial-principles)
- [How it works](#how-it-works)
- [Examples](#examples)
- [Running the project](#running-the-project)
- [Implementation architecture](#implementation-architecture)
- [Estimated costs](#estimated-costs)
- [Open questions](#open-questions)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

---

## The problem

Running an independent publication takes far more than writing. Before the first sentence, a creator has to:

1. Monitor dozens of websites, feeds and newsletters.
2. Spot what actually matters to their audience.
3. Throw away duplicates (the same story republished across many outlets) and noise.
4. Decide what goes into the issue, in what order, and why.
5. Collect links and check facts.

Only then does the writing begin. For a solo creator with a day job, this invisible preparation is a likely reason issues slip, frequency drops, and publications quietly stop. We are validating this through interviews with creators (see [Open questions](#open-questions)).

Existing tools cover pieces of this:

| Tool type | What it does | What it does not do |
| --- | --- | --- |
| Feed readers (e.g. Feedly) | Collect, filter and summarize sources | Build or send an issue |
| Newsletter platforms (e.g. Substack, beehiiv) | Write, send, charge readers; some include AI writing help | Watch your external sources and build a story list |
| General chatbots with scheduled tasks | Produce a recurring briefing from a prompt | Keep a per-publication editorial memory, link every claim to a source, deliver to readers |
| AI newsletter generators (e.g. NewsletterLab) | Curate sources and draft issues in the author's voice | Mostly English-only and aimed at marketing newsletters |

Kloxus does not claim to be first. It aims to be the option built for Portuguese-language independent creators, with source traceability and author control as core features.

## The solution

Kloxus is designed to work as a small editorial team behind the scenes, while the creator keeps authorship, voice and final say.

For each publication, the creator defines:

- name, topic and audience
- editorial line (what matters, what does not, tone)
- sources (RSS feeds, open web pages, OPML import)
- frequency and issue sections

Kloxus then runs this pipeline for each issue — on demand in the demo, on a schedule once the scheduler lands:

| Step | What the creator gets |
| --- | --- |
| Collect | New items from every source since the last issue |
| Deduplicate | One entry per story, listing every outlet that covered it |
| Triage | A 0 to 10 relevance score for each item, with a one-line reason |
| Story list | 5 to 10 items grouped by theme, each with "why it matters" |
| Dossier | An original summary, facts with links, a short quote, and what is still unknown |
| Draft | The issue laid out in the creator's sections, with empty slots for their commentary |
| Citation check | A warning on any factual sentence without a source |
| Review | Approve, reject with a reason, edit, or ask for an alternative |
| Delivery | Email to subscribers, plus HTML and Markdown export for Substack or beehiiv |
| Paywall (optional) | Paid issues unlocked for readers who subscribe through the creator's own payment account |

Every decision the creator makes (rejections and their reasons, edits) is condensed into a short **editorial guide** that the creator can read and edit. The guide is used in the next run, so each issue starts closer to what the creator would have chosen.

## Editorial principles

These are product requirements, not marketing copy:

- **Authorship belongs to the creator.** Kloxus prepares the material. The commentary is always written by the person who signs the issue.
- **Nothing ships without approval.** In the first version, every issue requires explicit human approval before it is sent.
- **Every factual claim has a source.** Each factual sentence links to its origin. Unlinked sentences block sending until the creator fixes or confirms them.
- **Facts and interpretation are visibly separated** in every dossier.
- **Curation is not reproduction.** Original summaries, short attributed quotes, and only sources Kloxus is allowed to read: RSS, public APIs and open pages that `robots.txt` permits. No paywalled content.
- **Readers' money goes to the creator.** Kloxus never receives or holds reader payments.
- **Creator data is not used to train models.**

## How it works

```
Scheduler → Collect → Deduplicate → Triage → Story list → Dossier → Draft → Citation check
                                                                                   │
                                                                                   ▼
                      Paying reader ← Paywall ← Delivery ← ─ ─ ─ ─ ─ ─ Human review
                                                                                   │
                                                                                   ▼
                                                     Database: decisions + editorial guide
                                                     (fed back into the next triage)
```

Most of the pipeline is plain automation. Language models are only used where judgment is needed:

| Step | Implementation | Why |
| --- | --- | --- |
| Collect, schedule, extract text | Conventional code | Fixed rules, no judgment |
| Exact and near-duplicate detection | Canonical URL + SimHash | Cheap and deterministic |
| Group different articles about the same event | Small, fast language model | Needs to understand content, not match text |
| Relevance to the editorial line | Small model, batched, with the editorial guide | Semantic and creator-specific |
| Story list, "why it matters" | Small model | Editorial judgment |
| Dossier and deep dive on up to 3 items | Larger model, with a bounded agent loop (search and read tools, step limit) | The only open-ended step |
| Draft assembly | Larger model + editorial guide | Fits the creator's structure and tone |
| Citation check | Small model + a blocking rule | The model maps sentence to source; the rule blocks what is unlinked |
| Email, paywall, reader access | Third-party services | Solved problems |

In short: a deterministic workflow with a few model calls and a single agentic step, not a free-roaming agent. That keeps costs low and behavior predictable enough for human review to stay fast.

### Paywall without intermediation

The planned design: each creator opens their own account with a Brazilian payment gateway (AbacatePay), completes identity verification there, and gives Kloxus an API key restricted to the permissions it needs. Kloxus creates subscription checkouts in the creator's account and listens to payment notifications to grant or revoke access to paid issues.

```
Reader ──pays (PIX or card)──▶ Creator's payment account ──▶ Creator
                                        │
                                        └── notification ──▶ Kloxus unlocks the paid issue
```

Kloxus would never touch the money and would take no commission. It would earn only from its own software subscription. Whether the gateway's terms allow a platform to store a creator's key is still being confirmed (see [Open questions](#open-questions)).

## Examples

**The stories and links in this section are fictional placeholders** used to illustrate the product design. The demo itself produces real output — from fictional Brazilian sources, with recorded model responses — following exactly this shape.

### A publication definition

How a creator describes a publication — the same YAML format the demo loads:

```yaml
name: Futuro Próximo
language: pt-BR
topic: Technology and trends that will affect Brazilian professionals
audience: Mid-career professionals who are curious but not engineers
editorial_line: |
  Prioritize real-world adoption over announcements.
  Skip product launches without availability in Brazil.
  Skip funding rounds unless they change a market.
  Prefer primary sources (company blogs, papers, regulators).
tone: Plain, first person, no hype
frequency: weekly
sections:
  - title: The big one
    items: 1
    commentary: required
  - title: Worth knowing
    items: 4
    commentary: optional
  - title: Quick links
    items: 5
    commentary: none
sources:
  - type: rss
    url: https://example.com/tech/feed.xml
  - type: rss
    url: https://example.org/science/rss
  - type: page
    url: https://example.net/regulator/news
paywall:
  enabled: true
  paid_sections: [The big one]
ai_disclosure: short   # none | short | detailed
```

### A story list, as the creator would see it

```
[9.1] Regulator publishes draft rules for AI in hiring
      Why: directly affects the audience; primary source; open for comments until Dec 15.
      Sources: example.net/regulator/news/123, example.com/tech/ai-hiring
      [Approve] [Reject…] [Alternative]

[4.2] Startup X raises Series B
      Why: low relevance; funding round without market impact (from your editorial guide).
      [Approve] [Reject…] [Alternative]
```

### A dossier entry

```markdown
### Regulator publishes draft rules for AI in hiring

**Summary (Kloxus):** The regulator released a draft that would require
companies to disclose when automated systems screen job applicants. [1]

**Facts**
- Public comments are open until Dec 15. [1]
- The draft applies to companies with more than 100 employees. [1]
- Two business associations have already asked for a longer transition. [2]

**Interpretation (marked as such)**
- If adopted, recruiting platforms will likely need new consent screens.

**Still unknown**
- Penalties and enforcement timeline.

**Sources**
[1] https://example.net/regulator/news/123
[2] https://example.com/tech/ai-hiring

> ✍️ Your commentary goes here (required for this section)
```

### How the editorial guide changes

If the creator rejects an item with the reason "too US-centric", a line like this is added to the guide, visible and editable:

```
- Deprioritize stories about US-only regulation unless a Brazilian equivalent is under discussion.
```

## Running the project

### Local demo — zero setup

Requirement: **Go 1.27 or newer**. No database, API key, account, or network access is needed for demo mode.

```bash
go run ./cmd/kloxus demo
```

The command seeds the fictional **Futuro Próximo** publication and opens `http://localhost:8080`. It stores demo data in `kloxus.db`, writes generated issues under `out/`, and writes demo email output under `out/outbox/`. The publication list includes a **Reset demo** button.

For a non-browser pipeline check:

```bash
go run ./cmd/kloxus demo --headless
```

Expected result: 35 collected items, 32 deduplicated stories, 8 selected entries, one deliberately unsupported citation flag, and `out/issue-1.md`.

Useful checks:

```bash
go vet ./...
go test ./...
```

### Hosted demo deployment

The repository includes a `Dockerfile` and `fly.toml` for the same demo-mode binary. The app listens on port 8080, exposes `/healthz`, stores SQLite and generated output on a Fly volume, and can reset the demo every 24 hours via `KLOXUS_DEMO_RESET_INTERVAL=24h`.

Typical Fly deployment:

```bash
fly launch --copy-config
fly volumes create kloxus_data --region gru --size 1
fly deploy
```

Change `app` in `fly.toml` first if `kloxus-demo` is unavailable. No secrets are required.

### Real mode

Real integrations are intentionally incomplete for the hackathon scope. With `KLOXUS_MODE=real` and `ANTHROPIC_API_KEY` set, `kloxus run` fetches live RSS/HTTP sources and calls the Anthropic API, and the Anthropic-backed `record` command can regenerate replay fixtures; `serve`, Resend delivery, production Firecrawl monitoring, and AbacatePay remain post-demo work.

## Implementation architecture

| Component | Current choice |
| --- | --- |
| Backend | Go single binary with server-rendered web UI |
| Database | SQLite via `modernc.org/sqlite`, embedded migrations |
| Jobs and scheduling | In-process run state; demo reset interval |
| Feeds and extraction | Embedded RSS/page fixtures in demo; HTTP fetcher + Firecrawl monitor in real mode |
| Dedupe | Canonical URL + SimHash |
| Models | Provider interface: Anthropic, replay fixtures, recorder |
| Email | Local `.eml`/HTML outbox in demo; Resend planned for real mode |
| AI Code Assistant | Devin |
| Payments | Simulated reader subscription/paywall in demo; AbacatePay planned |
| Monitoring | Firecrawl monitor interface with a simulated demo provider |
| UI | Go `html/template`, embedded CSS/vanilla JS, no CDN |

## Estimated costs

The recorded demo issue estimates **US$ 0.1814 in model calls**: 35 collected items, 32 deduplicated stories, 8 dossiers, 3 deep dives, draft, and citation check. This is still an estimate from token counts and configured model prices, but it is now measured from a real pipeline run.

## Open questions

These are the hypotheses we are testing before and during the build:

1. Do creators spend 3+ hours per issue on finding and filtering, and see it as the main bottleneck?
2. Will they pay for this instead of using free tools?
3. Will 60% or more of the suggested story list be kept without changes?
4. Will readers react badly to disclosed AI assistance?
5. Do the payment gateway's terms allow a platform to store a creator's restricted API key?

Findings will be added to `docs/` as they come in.

## Roadmap

- [x] Product design and specification (Nov 2026)
- [ ] Creator interviews and source mapping (Nov 2026)
- [x] Working prototype with demo mode (HackBrazil online round, Nov 2026)
- [ ] Pilot with up to 5 real publications (Dec 2026 to Jan 2027)
- [ ] Decide whether to continue, based on the pilot

The milestone-level implementation plan for the prototype — sequencing,
exit tests and risks — lives in [docs/roadmap.md](docs/roadmap.md).
Design decisions are recorded in [docs/decisions.md](docs/decisions.md).

Not planned for the first version: fully autonomous sending, multiple editors per publication, social media posting, paywalled sources, translation, or AI-written commentary.

## Contributing

The project is at the idea stage. The most useful contributions right now are:

- feedback from people who run independent newsletters, especially in Portuguese
- critiques of the editorial principles and the paywall design

Please open an issue to share feedback.

## License

Kloxus is **source-available, not open source**. It is licensed under the
[PolyForm Strict License 1.0.0](https://polyformproject.org/licenses/strict/1.0.0),
which allows noncommercial use but does not allow distributing the software
or making changes or new works based on it.

**For HackBrazil 2026:** the organizers and judges may read, run, test and
modify this software solely to evaluate it in the competition. See
[LICENSE](LICENSE) for the full terms.

For any other use, including commercial use, please open an issue to ask
for permission.

Copyright (c) 2026 Sthéfano Cordeiro.
