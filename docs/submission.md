# HackBrazil submission notes

Deadline: **Nov 30, 2026, 23:59**. Submit early (~Nov 28) and keep the demo online through evaluation.

## Links

- Repository: https://github.com/sthefanocordeiro/kloxus
- Hosted demo: https://kloxus-demo.fly.dev (deployed 2026-11-10, Fly.io gru, volume kloxus_data)
- Video: **TBD — owner records in English, ≤3 minutes, unlisted YouTube**

## Paste-ready copy

### Tagline (EN)

Kloxus is the editorial back office that turns the sources an independent creator already follows into a reviewed, source-linked issue — while the creator keeps authorship and final approval.

### Tagline (PT-BR)

Kloxus é o back office editorial que transforma as fontes que um criador independente já acompanha em uma edição revisada e com links para as fontes — mantendo autoria e aprovação final com o criador.

### Description (EN)

Independent creators lose hours every week monitoring feeds, filtering duplicates, checking facts, and assembling each issue. Kloxus works like a small editorial team behind the scenes: it collects sources, deduplicates stories, ranks what matters, builds source-backed dossiers, drafts the issue, flags unsupported claims, and leaves commentary and approval to the human editor. The HackBrazil demo runs fully offline with fictional Brazilian sources and recorded model responses.

### Description (PT-BR)

Criadores independentes perdem horas toda semana monitorando feeds, filtrando duplicatas, checando fatos e montando cada edição. Kloxus funciona como uma pequena equipe editorial nos bastidores: coleta fontes, remove duplicatas, prioriza o que importa, monta dossiês com fontes, prepara o rascunho, sinaliza afirmações sem respaldo e deixa comentários e aprovação final para o editor humano. A demo do HackBrazil roda totalmente offline com fontes brasileiras fictícias e respostas de modelos gravadas.

## Hosted demo checklist

1. Change `app = "kloxus-demo"` in `fly.toml` if the name is unavailable.
2. Run:
   ```bash
   fly launch --copy-config
   fly volumes create kloxus_data --region gru --size 1
   fly deploy
   ```
3. Verify `GET /healthz` returns `ok`.
4. Open the demo URL, click **Reset demo**, then run the judge flow once.
5. Confirm the public URL stays up (`auto_stop_machines = "off"`, `min_machines_running = 1`).

## Demo flow for judges/video

Keep item 1 selected if you want to show the planted unsupported-claim flag.

1. Publications → **Futuro Próximo** → Prepare next issue.
2. Wait through collect → dedupe → triage → storylist → dossier → deepdive.
3. Review story list; open one dossier; reject one lower-ranked item with a reason.
4. Show that the rejection reason is appended to the editorial guide.
5. Draft the issue; show the citation flag on item 1.
6. Add required commentary in **The big one**, dismiss the flag after explaining it.
7. Approve; export Markdown/HTML or send to the local outbox.
8. Paywall: simulate reader payment and compare unpaid/paid reader views.
9. Optional: Sources page shows the simulated Firecrawl monitor badge and Check now.

## Video shot list (≤3 min, English)

- 0:00–0:15 — Problem: creators lose invisible hours preparing each issue.
- 0:15–1:10 — Prepare run: sources, dedupe, triage, dossier/deep dive.
- 1:10–2:10 — Human review: dossier, reject with reason, guide update, draft, citation flag.
- 2:10–2:40 — Control: commentary stays human, approve, export/send, paid section unlock.
- 2:40–3:00 — Close: Kloxus keeps authorship and sourcing; demo is deterministic/offline.
