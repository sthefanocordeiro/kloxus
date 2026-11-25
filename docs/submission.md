# HackBrazil submission notes

Deadline: **Nov 30, 2026, 23:59**. Submit early (~Nov 28) and keep the app online through evaluation.

Track: **Logística & Automação** (Logistics and Automation).

## Links

- Repository: https://github.com/sthefanocordeiro/kloxus
- Hosted app: https://kloxus.fly.dev (real app: live sources, Postgres on Supabase, Resend email)
- Deterministic fixture demo (offline fallback): https://kloxus-demo.fly.dev
- Video: **TBD — owner records in English, ≤3 minutes, unlisted YouTube**

## Paste-ready copy

### Tagline (EN)

Kloxus automates the invisible work of people who write about a sector every day: it watches the sources, separates what matters and delivers the ready brief by 7 a.m. The writing stays theirs.

### Tagline (PT-BR)

A Kloxus automatiza o trabalho invisível de quem comenta um setor todo dia: acompanhar as fontes, separar o que importa e entregar a pauta pronta às 7h. A escrita continua sua.

### Description (EN)

People who publish about a sector every day — the analyst, the consultant, the banker who posts about fintech, the HR professional who writes about careers — lose hours to the manual work before writing: opening dozens of sites, discarding repeats, deciding what matters. Kloxus automates that flow. Overnight it collects audited sources, removes duplicates and ranks what matters with embeddings and a reranker that learn from every Yes/No. The brief is assembled at ~06:30 and reaches the inbox by 7 a.m. The person picks the stories, writes, and one text becomes a LinkedIn post, an X thread, HTML and Markdown. The AI never writes the opinion, and every source's own rules are respected.

### Description (PT-BR)

Quem publica sobre um setor todo dia — o analista, o consultor, o bancário que posta sobre fintech, o profissional de RH que escreve sobre carreira — perde horas no trabalho manual que vem antes de escrever: abrir dezenas de sites, descartar repetições, decidir o que importa. A Kloxus automatiza esse fluxo. De madrugada, ela coleta fontes auditadas, remove duplicatas e ordena o que importa com embeddings e um reranker que aprendem com cada Sim/Não. A pauta é montada ~6h30 e chega por e-mail às 7h. A pessoa escolhe as notícias, escreve, e um texto vira post do LinkedIn, thread do X, HTML e Markdown. A IA nunca escreve a opinião, e as regras de cada fonte são respeitadas.

## Hosted app checklist

1. Deploys to the shared `kloxus` Fly app are serialized through release windows (KLOX-26 ledger); `fly.toml` targets `kloxus`, `fly.demo.toml` targets `kloxus-demo` — never deploy to `kloxus-demo`.
2. Verify `GET /healthz` returns `ok` on https://kloxus.fly.dev.
3. Run `scripts/smoke.sh https://kloxus.fly.dev` and the Playwright e2e suite with `E2E_BASE_URL=https://kloxus.fly.dev`.
4. Confirm the public URL stays up (`auto_stop_machines = "off"`, `min_machines_running = 1`) — the feed scheduler lives in the web process.

## Demo flow for judges/video

1. Landing → **Create your Radar** → confirm the email magic link.
2. Name the Radar and say what you cover — topics plus optional beats; Kloxus assigns the curated sources.
3. **Match**: the calibration deck — real stories from the last 48 h, Yes/No on each one.
4. **Today's stories**: the morning deck matched to the Radar; tick the stories that go into the brief, 👍 / 👎 to keep training the match. (A claimed Radar gets the same deck by email at 7 a.m.)
5. **Write**: the pauta — an optional opening plus one autosaved text box per story, the person's words only.
6. **Export**: one text → LinkedIn post, X thread (auto split at 280), rich-text HTML, and a `.md` download.
7. Optional: switch the interface EN ↔ pt-BR; show that source links on every card lead to the original outlet.

## Video shot list (≤3 min, English)

- 0:00–0:15 — Problem: the bottleneck is not writing, it is the manual work before it.
- 0:15–1:00 — Automation: overnight collection, dedupe, ranking that learns from Yes/No; brief assembled ~06:30, in the inbox by 7 a.m.
- 1:00–2:00 — The loop: create the Radar, calibrate with Yes/No, pick today's stories, write in your own voice.
- 2:00–2:30 — One text, four formats: LinkedIn, X thread, HTML, Markdown. Source links on every card.
- 2:30–3:00 — Close: the AI never writes the opinion; sources' rules respected; expansion to market intelligence and team clipping comes later.
