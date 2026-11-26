# HackBrazil submission notes

Deadline: **Nov 30, 2026, 23:59**. Submit early (~Nov 28) and keep the app online through evaluation.

Track: **Logística & Automação** (Logistics and Automation).

## Links

- Repository: https://github.com/sthefanocordeiro/kloxus
- Hosted app: https://kloxus.fly.dev (real app: live sources, Postgres on Supabase, Resend email)
- Deterministic demo: `go run ./cmd/kloxus demo` locally — fixture sources, no API keys
- Video: **TBD — owner records in English, ≤3 minutes, unlisted YouTube**

## Paste-ready copy

### Tagline (EN)

Kloxus automates the daily news-triage workflow for people who publish about a sector every day — on LinkedIn, X, a blog, a newsletter or an internal channel.

### Tagline (PT-BR)

A Kloxus automatiza a triagem diária de notícias para quem publica todo dia sobre um setor — no LinkedIn, no X, num blog, numa newsletter ou num canal interno.

### Description (EN)

Kloxus automates the daily news-triage workflow for people who publish about a sector every day — on LinkedIn, X, a blog, a newsletter or an internal channel. One shared engine polls an audited catalog of Brazilian sources, drops duplicates, matches each story to the user's Radar with embeddings and a reranker, and rebuilds the day's stories every morning. The person picks the stories and writes; Kloxus turns that one text into LinkedIn, X-thread, HTML and Markdown versions. It never writes the commentary.

### Description (PT-BR)

A Kloxus automatiza a triagem diária de notícias para quem publica todo dia sobre um setor — no LinkedIn, no X, num blog, numa newsletter ou num canal interno. Um motor compartilhado lê um catálogo auditado de fontes brasileiras, remove duplicatas, cruza cada notícia com o Radar do usuário usando embeddings e um reranker, e monta as notícias do dia toda manhã. A pessoa escolhe e escreve; a Kloxus transforma esse texto em versões para LinkedIn, thread do X, HTML e Markdown. A IA nunca escreve o comentário.

## Hosted app checklist

1. Deploys to the shared `kloxus` Fly app are serialized through release windows (KLOX-26 ledger); `fly.toml` targets `kloxus`, `fly.demo.toml` targets `kloxus-demo` — never deploy to `kloxus-demo`.
2. Verify `GET /healthz` returns `ok` on https://kloxus.fly.dev.
3. Run `scripts/smoke.sh https://kloxus.fly.dev` and the Playwright e2e suite with `E2E_BASE_URL=https://kloxus.fly.dev`.
4. Confirm the public URL stays up (`auto_stop_machines = "off"`, `min_machines_running = 1`) — the feed scheduler lives in the web process.

## Demo flow for judges/video

1. Landing → **Create your Radar** → confirm the email magic link.
2. Name the Radar and say what you cover — topics plus optional beats; Kloxus assigns the curated sources.
3. **Match**: the calibration deck — real stories from the last 48 h, Yes/No on each one.
4. **Today's stories**: the morning deck matched to the Radar; click the cards that go into the brief, 👍 / 👎 to keep training the match. (A claimed Radar gets the same deck by email at 7 a.m. on days with new stories — the morning send is off in production today and returns with the daily-mail relight in flight.)
5. **Write**: the pauta — an optional opening plus one autosaved text box per story, the person's words only.
6. **Export**: one text → LinkedIn post, X thread (auto split at 280), rich-text HTML, and a `.md` download.
7. Optional: switch the interface EN ↔ pt-BR; show that source links on every card lead to the original outlet.

## Video shot list (≤3 min, English)

- 0:00–0:15 — Problem: the bottleneck is not writing, it is the manual work before it.
- 0:15–1:00 — Automation: overnight collection, dedupe, ranking that learns from Yes/No; brief assembled ~06:30, in the inbox by 7 a.m. *(7 a.m. send off in production today — daily-mail relight in flight)*
- 1:00–2:00 — The loop: create the Radar, calibrate with Yes/No, pick today's stories, write in your own voice.
- 2:00–2:30 — One text, four formats: LinkedIn, X thread, HTML, Markdown. Source links on every card.
- 2:30–3:00 — Close: the AI never writes the opinion; sources' rules respected; expansion to market intelligence and team clipping comes later.
