# Kloxus JSON API — `/api/v1`

The v2 contract: the existing behaviour exposed as JSON. The product
rules do not change — the AI never writes commentary, nothing is sent
without explicit approval, every factual sentence links to a source. The
interface shows state; the server decides it.

- **Auth/isolation:** the same workspace cookie, rate limits and
  per-visitor isolation as the HTML routes. No other auth in demo mode.
- **Bodies:** JSON in, JSON out. Mutating routes require
  `Content-Type: application/json` — anything else is `415
  unsupported_media_type`.
- **Errors:** every error is an envelope with a stable `code`, a human
  `message` and, for blocked actions, `reasons` — each with a code, a
  message and a `target` pointing at what must be fixed
  (`{kind: "section"|"sentence"|"item"|"issue", ref}`).

```json
HTTP/1.1 409 Conflict
{
  "error": {
    "code": "blocked",
    "message": "the issue is not ready to approve — fix each reason first",
    "reasons": [
      {"code": "flags_unresolved",
       "message": "1 flagged sentence unresolved — edit, mark as interpretation or remove each",
       "target": {"kind": "sentence", "ref": "3.9f1c04ab"}},
      {"code": "commentary_required",
       "message": "commentary required in \"O destaque\"",
       "target": {"kind": "section", "ref": "O destaque"}}
    ]
  }
}
```

Stable codes: `not_found`, `validation`, `bad_json`, `conflict`,
`blocked`, `read_only` (sent issues), `reason_required`,
`invalid_source`, `invalid_citation`, `citation_required`, `unchanged`,
`unsupported_claim`, `sentence_gone`, `no_alternative`, `not_rejected`,
`not_approved`, `no_runs`, `monitor`, `rate_limited`,
`unsupported_media_type`, `internal`.

**Sentence ids** are `"{itemRef}.{hash}"` (e.g. `3.9f1c04ab`) — the hash
is taken over the stored text, so an id always resolves to the same
words and a stale id fails loudly instead of hitting the sentence that
moved into its place. Identical sentences inside one item get a `.{occ}`
suffix from the second occurrence on.

**Document `version`** is the optimistic-concurrency token: every
document mutation (sentence edit/remove/interpretation, commentary) must
send back the `version` it read. A stale or missing version answers `409
conflict` and the response carries the fresh `document`.

---

## Workspace

### `GET /api/v1/workspace`
```json
{"id": "local", "label": "local", "mode": "demo", "hosted": false, "llm_mode": "auto",
 "claimed": false, "owner_email": "", "mail": "outbox", "mail_transport": "outbox"}
```
`mail` is `"live"` when a mail sender is configured, `"outbox"`
otherwise. `mail_transport` is the sender's own wire identity —
positive server-side evidence verification scripts preflight before
any mail-capable step: `"outbox"` (no sender), `"resend"` (the real
provider), `"synthetic"` (the loopback fake built by
`KLOXUS_FAKE_RESEND_URL`), `"custom"` (any other injected sender).
`claimed`/`owner_email` describe the workspace claim below.

### `POST /api/v1/workspace/claim`
`{"email": "creator@example.com"}` — starts the workspace claim: a
single-use magic link (15 min) is mailed to the address; only the
token's hash is stored. Rate-limited to five links per workspace per
hour (`429 rate_limited`). On a claimed workspace this re-verifies the
new address.
```json
{"sent": true, "email": "creator@example.com", "outbox_email_id": 7,
 "delivery": "resend"}
```
`delivery` is `resend`, `outbox` (demo — the response then also carries
`claim_url` so the link can be opened without an inbox), or
`outbox_fallback` (the provider failed; the mail is a ledger copy).
A send refused by the daily caps answers `sent:false` + `refused`.

### `GET /claim?t={token}` → `POST /claim?t={token}` — not JSON
The emailed link is two-step so mail-scanner prefetches can't spend it:
`GET` only validates the token and renders a confirm page (`400` page
when the link is invalid, spent or expired); the page's explicit `POST`
spends it — must still be unexpired and unused. The workspace becomes
owned by the link's email and the browser that confirmed is signed in
via the workspace cookie — whichever browser that is. Redirects to `/`.

Emailed links are built on `KLOXUS_PUBLIC_URL` when configured, not the
request host — the app may answer on throwaway verification hosts, but
mail must point at the canonical deployment. `GET /claim/`,
`GET /confirm-sub/` and `POST /unsub/` (a stray trailing slash on the
emailed link) resolve to the same handlers.

`llm_mode` is `"off"` when `KLOXUS_LLM_MODE=off`: runs rank and draft
deterministically with zero model calls, and AI-only endpoints (guide
rebuild) answer `409 ai_disabled`. Deterministic runs bypass AI run
admission — the kill switch, run budget and daily cap guard real model
work only — and write nothing to the AI run ledger.

## Publications

### `GET /api/v1/sample` — the landing page's cited dossier
Workspace-free: no workspace is resolved or created.
```json
{"publication": "Futuro Próximo", "issue_number": 7, "date": "2026-03-02",
 "dossiers": [{"ref": 1, "title": "…", "summary": "…",
   "facts": [{"text": "…", "ref": 1}],
   "quote": {"text": "…", "attribution": "…"},
   "unknowns": ["…"], "interpretation": ["…"],
   "sources": [{"ref": 1, "url": "…", "name": "…"}]}],
 "how_it_works": ["…"]}
```
The sample's sources live on fictional `.example` outlets; their links
resolve at `GET /sample/a?u=<url>` — an app-served stub article, also
workspace-free, so every citation opens an original inside the demo.

### `GET /api/v1/proposal` — the create screen's five editable blocks
```json
{"proposal": {
  "name": "Futuro Próximo", "topic": "…", "audience": "…",
  "editorial_line": "…", "tone": "…",
  "language": "pt-BR", "frequency": "weekly", "ai_disclosure": "short",
  "sections": [{"title": "O destaque", "items": 1, "commentary": "required"}],
  "sources": [{"type": "rss", "url": "fixtures/feeds/radar-tech.xml"}],
  "paywall": {"enabled": true, "paid_sections": ["O destaque"], "price": "R$ 12/mês"}
}}
```

### `POST /api/v1/publications/propose`
`{"material": "https://…"}` — KLOX-99 "cole um link": scrapes the pasted
newsletter/blog URL through the configured provider (Firecrawl in real
mode, the offline fake in demo) and deterministically proposes the
publication definition for review — `proposal` has the same shape as
`GET /proposal` (with empty `sources`: the pasted URL feeds the template
only, never the source catalog) and `scrape` reports what the provider
extracted (`url`, `title`, `provider`, `simulated`, `words`, `warnings`
— `thin_content`/`generic_sections`). Errors: `400 invalid_url`,
`503 scrape_unavailable` (no provider configured), `429 scrape_cap`
(`KLOXUS_SCRAPES_PER_DAY`, default 5 — the paid_actions ledger counts
every attempt), `502 scrape_failed`, `422 scrape_blocked` (the page gave
almost no readable text — paywall, sign-in or script-heavy).

### `POST /api/v1/publications` → `201`
Creates a publication from the (possibly edited) proposal blocks.
`{"name": "…", "topic": "…", "audience": "…", "editorial_line": "…",
  "tone": "…", "language": "pt-BR", "frequency": "weekly",
  "ai_disclosure": "short", "sections": [{…}], "paywall": {…}}`
Validation failure → `validation`. The stored publication is flagged
`"custom": true`; in demo mode its prompt-bearing fields are substituted
by the canonical sample at run time so fixture replay stays stable.

### `GET /api/v1/publications`
```json
{"publications": [{"id": 1, "slug": "futuro-proximo", "name": "Futuro Próximo",
  "custom": false, "...": "…"}]}
```

### `GET /api/v1/publications/{id|slug}` — publication + settings
```json
{
  "publication": {
    "id": 1, "slug": "futuro-proximo", "name": "Futuro Próximo",
    "language": "pt-BR", "topic": "…", "audience": "…",
    "editorial_line": "…", "tone": "…", "frequency": "weekly",
    "ai_disclosure": "short",
    "sections": [{"title": "O destaque", "items": 1, "commentary": "required"}],
    "paywall": {"enabled": true, "paid_sections": ["O destaque"], "price": "R$ 12/mês"}
  },
  "latest_issue": {"id": 1, "number": 1, "status": "reviewing", "…": "…"}
}
```

## Sources

### `GET /api/v1/publications/{id}/sources`
```json
{"sources": [{
  "id": 1, "name": "Radar Tech", "type": "rss",
  "url": "fixtures/feeds/radar-tech.xml",
  "canonical_url": "fixtures/feeds/radar-tech.xml",
  "last_fetch_at": "2026-11-11 12:00:00", "last_status": "ok: 12 items, 9 new",
  "items_per_week": 12,
  "monitor": {"provider": "", "id": "", "status": ""}
}]}
```

### `GET /api/v1/publications/{id}/sources/catalog`
The catalog sources not yet added — the "suggested" cards on the sources
screen. Only fetchable policies (`launch`, `signal`) are listed;
`hold`/`blocked` catalog rows never appear in user-facing source
lists.
```json
{"offered": [{"type": "rss", "url": "fixtures/feeds/radar-tech.xml",
  "name": "Radar Tech", "items_per_week": 3}]}
```

### `GET /api/v1/publications/{id}/sources/preview?type=rss&url=…`
The three latest items a source would bring in. Fixture URLs read the
embedded files; other URLs are format-validated only and return a
simulation note (demo never fetches). Bad input → `invalid_source`.
```json
{"items": [{"title": "…", "url": "…", "outlet": "Radar Tech",
  "published_at": "2026-11-10"}]}
```

### `POST /api/v1/publications/{id}/sources/confirm`
Adds the selected offered sources and marks onboarding done →
`{"sources": […]}`. An optional `{"include": ["…url…"]}` body limits
which offered sources are added (the sources screen sends the checked
URLs); no body accepts them all. The client then starts the first run
itself.

### `DELETE /api/v1/publications/{id}/sources/{sid}`
Removes a source — collected items go with it (stories and past issues
keep their own rows).

### `POST /api/v1/publications/{id}/sources`
`{"type": "rss", "url": "https://feed.test/rss.xml"}` → `201`
```json
{"source": {"id": 5, "type": "rss", "url": "https://feed.test/rss.xml", "…": "…"},
 "note": "Simulated in demo mode: sources are not fetched on the hosted demo."}
```
Demo mode validates format only and never fetches a visitor URL;
`fixtures/...` paths must exist. Bad input → `invalid_source`.

### `POST /api/v1/publications/{id}/sources/{sid}/watch`
Registers the source with the monitor provider → `{"source": {…}}`.

### `POST /api/v1/publications/{id}/sources/{sid}/check`
Runs a monitor check → `{"source": {…}}` with updated `monitor.status`.

## Runs

A run is the pipeline working: `kind: "prepare"` (Phase A, collect →
deepdive, started from `POST …/runs`) or `kind: "draft"` (Phase B,
draft + citecheck, started from `POST /issues/{id}/draft`). `status` is
`running`, `done` or `failed`. Events carry real counts, never
percentages.

### `POST /api/v1/publications/{id}/runs` → `202`
```json
{"run": {"id": "a1b2c3d4", "kind": "prepare", "status": "running",
         "step": "starting", "events": []}}
```
One run at a time per workspace: a second start while running returns
the in-flight run.

### `GET /api/v1/runs/{run_id}`
```json
{"run": {"id": "a1b2c3d4", "kind": "prepare", "status": "done",
         "step": "citecheck", "issue_id": 1, "no_new_items": false,
         "events": [
           {"seq": 1, "step": "collect", "counts": {"items_read": 35, "new_items": 12}},
           {"seq": 2, "step": "dedupe", "counts": {"stories": 9, "duplicates_merged": 3}},
           {"seq": 3, "step": "triage", "counts": {"ranked": 9}},
           {"seq": 4, "step": "storylist", "counts": {"selected": 8}},
           {"seq": 5, "step": "dossier", "counts": {"dossiers": 8}},
           {"seq": 6, "step": "deepdive", "counts": {"items": 3}},
           {"seq": 7, "step": "draft", "counts": {"entries": 5}},
           {"seq": 8, "step": "citecheck", "counts": {"checked": 12, "flagged": 1}}
         ]}}
```

### `GET /api/v1/publications/{id}/runs/latest`
The current or most recent prepare run for the publication (`no_runs`
when none).

### `GET /api/v1/runs/{run_id}/events` — Server-Sent Events
Replays the step log, streams new `step` events as steps finish, then
one `done` event and closes.
```
event: step
data: {"seq": 1, "step": "collect", "counts": {"items_read": 35, "new_items": 12}}

event: done
data: {"id": "a1b2c3d4", "kind": "prepare", "status": "done", "issue_id": 1, "…": "…"}
```

## Issues

### `GET /api/v1/publications/{id}/issues`
```json
{"issues": [{
  "id": 1, "publication_id": 1, "number": 1, "status": "reviewing",
  "subject": "Futuro Próximo — issue 1", "created_at": "2026-11-11 12:00:00",
  "items": {"pending": 4, "approved": 3, "rejected": 1, "alternative": 0},
  "flags": 0, "missing_required_commentary": 1
}]}
```
`status` ∈ `""` (being prepared) → `reviewing` → `drafted` → `approved`
→ `sent`. Sent issues are read-only (`read_only`). `cost_usd` is the
issue's recorded model spend — the sum of `runs.cost_usd` for that
issue — shown on the issue page (KLOX-44).

### `GET /api/v1/issues/{id}` — one issue, same shape as the list row.

### `GET /api/v1/issues/{id}/items` — triage cards
```json
{"items": [{
  "id": 9, "issue_id": 1, "story_id": 4, "section": "O destaque",
  "score": 8.7, "reason": "why this belongs in the issue",
  "status": "pending", "origin": "phaseA",
  "title": "Regulator opens consultation on AI labelling",
  "outlets": ["Radar Tech", "Pulso Digital"],
  "reject_reason": "…"
}], "pending": 5}
```
`score` is the triage score on a 0–10 scale.
```

### `POST /api/v1/issues/{id}/items/{iid}/decisions`
`{"kind": "approve"|"reject"|"alternative"|"undo", "reason": "…"}` →
```json
{"item": {"id": 9, "status": "rejected", "…": "…"}, "pending": 4,
 "guide": {"version": 3, "added": ["- Skip stories about X"]}}
```
- `reject` without `reason` → `reason_required`; the reason feeds the
  editorial guide (`guide` reports the new version and added rules).
- `alternative` swaps in the next-best candidate (`no_alternative` when
  none); the replacement comes back `pending`.
- `undo` restores a rejected item (`not_rejected` otherwise).
- A decision on an `approved` issue reverts it to `drafted`; on a `sent`
  issue → `read_only`.

### `GET /api/v1/issues/{id}/candidates` — the build board
The ranked stories of the run, best first — the left "Pautas" column
plus the lineup state in one snapshot. The pool is capped at the top
150 candidates and the bench is paginated: `?page=N&page_size=M`
(`page_size` defaults and clamps to 50). Every page carries all
in-issue stories plus that page's bench slice, so the lineup is never
paged away.
```json
{"candidates": [{
  "story_id": 4, "item_id": 9, "title": "…", "outlets": ["Radar Tech"],
  "sources": 2, "duplicates": 1,
  "score": 8.7, "reason": "why this belongs", "rank": 1,
  "in_issue": true, "section": "O destaque",
  "suggested_section": "O destaque",
  "status": "approved", "origin": "phaseA", "position": 0,
  "dossier": true, "available": true,
  "summary": "One-line AI summary of the story.",
  "kind": "noticia", "language": "pt", "topics": ["tecnologia"],
  "published_at": "2026-11-24T09:12:00Z",
  "ai_draft": false
}],
 "sections": [{"title": "O destaque", "capacity": 1, "commentary": "required"}],
 "page": 0, "page_size": 50, "bench_total": 137, "has_more": true,
 "hidden_bench": 6,
 "demo": true}
```
- `bench_total` counts bench stories only (in-issue stories excluded);
  `has_more` means another bench page exists.
- `in_issue` = currently on the board (`pending`/`approved`); stories
  removed without a reason come back `status: "removed"`, rejections
  `status: "rejected"` — neither is in the issue.
- `suggested_section` only on AI-picked items (`origin: "phaseA"`).
- `dossier` = a dossier already exists; creator-added stories get one
  generated during the draft run.
- `summary` is the shared AI enrichment's one-line PT summary
  (KLOX-67); absent while an item is un-enriched — enrichment fills in
  asynchronously after ingest.
- `kind`/`language`/`topics`/`published_at` are the card's detail
  fields (KLOX-95): the article-type slug (`noticia`, `analise`,
  `tutorial`, `forum`, `lancamento`, `lista`, `publieditorial`,
  `entretenimento`, `esporte`, `efemeride`, `outro`), the detected
  language, the fixed-list topic tags and the newest member's publish
  time. The first three are AI enrichment — absent while the story is
  un-enriched, and the card falls back to title/outlet; `published_at`
  is item metadata and appears whenever a member carries a parseable
  date.
- `foreign`/`off_topic` mark the suggestions the bench hides by
  default (KLOX-95): a member language different from the
  publication's, or classified topics that never intersect the
  declared `topic`/`editorial_line`. `?all=1` serves them back;
  `hidden_bench` counts how many bench stories the default view
  conceals (same number either way — in-issue stories are never
  filtered). A story without enrichment is never filtered.
- `ai_draft` on a lineup card means the pauta body arrived AI-written
  and is still unedited; the composer renders its "Gerado por IA"
  marker from the same flag, and any author edit clears it.
- Items the enrichment marked promotional or a non-editorial kind
  (buying guide, entertainment, sport, almanac, advertorial) never
  enter the ranked pool at all.
- In demo mode only the top-`14` ranked stories are `available`;
  adding another → `demo_unavailable`.

### `POST /api/v1/issues/{id}/rerank` — "Ordenar pela minha linha editorial"
User-triggered editorial rerank (KLOX-67 §7.2): one Haiku call
re-orders at most 120 suggestions by the publication's editorial line
(title + summary only) and persists the new order as triage scores.
`200 {"reranked": true|false}` — `false` when the model is off, the
kill switch is set, the per-newsletter cap (2/day) or the workspace
paid-action cap (5/day) is reached, or the provider fails. Never an
error page; the free ranking always stands.

### `PUT /api/v1/issues/{id}/lineup` — set the board
`{"entries": [{"story_id": 4, "section": "Vale saber"}, …]}` —
the full desired lineup; order within a section is the array order
(`position` 0-based). Returns the first candidates page in the same
shape as `GET …/candidates` (plus `guide` when a re-included rejection
rebuilt it).
- Stories already in the issue keep their row (`manual` adds create
  one); everything in the lineup becomes `approved`.
- In-issue stories absent from `entries` become `removed` — not
  rejected. Reject-with-reason stays on `…/decisions`.
- `unknown_story` (not a ranked candidate), `duplicate_story`,
  `unknown_section`, `section_full` (409, carries the section target),
  `demo_unavailable` (409) on violations. Sent issues → `read_only`.

### `GET /api/v1/stories/{sid}` — story detail for the board sheet
```json
{"story": {"id": 4, "title": "…", "score": 8.7, "reason": "…",
  "outlets": ["Radar Tech"],
  "items": [{"id": 41, "title": "…", "url": "/a/41", "outlet": "Radar Tech",
             "published_at": "…", "snippet": "…"}]}}
```

### `GET /api/v1/items/{iid}/dossier`
```json
{"item": {"id": 9, "…": "…"},
 "dossier": {"ref": 1, "title": "…", "summary": "…",
   "facts": [{"text": "…", "ref": 1}],
   "deep_facts": [{"text": "…", "ref": 2}],
   "quote": {"text": "…", "attribution": "…"},
   "unknowns": ["…"], "interpretation": ["…"],
   "sources": [{"ref": 1, "url": "…", "item_id": 41, "name": "Radar Tech"}]}}
```

### `POST /api/v1/issues/{id}/draft` → `202` `{"run": {…}}`
Starts Phase B as a `kind: "draft"` run (events via
`/runs/{id}/events`). Every configured section must hold at least one
lineup item — an empty one → `blocked` with `section_empty` reasons.
Lineup items still `pending` are promoted to `approved` at draft.

### `POST /api/v1/issues/{id}/compose` → `200` `{"issue": {…}, "selected": N}`
With AI on (KLOX-67): each pauta arrives **prefilled** from the shared
per-story research cache — sourced facts with `[n]` citations and an
AI-written, author-editable draft body, marked `ai_draft` (one Haiku
call per new story, capped by the workspace's 5 paid actions/day; a
story the research cannot serve — cap, kill switch, provider failure —
gets no research dossier at all: the deterministic dossier fills the
slot with real sourced material and the pauta stays an empty manual
slot — owner decision D4 hides the unavailable element instead of
showing any "unavailable" placeholder or error copy). The research
JSON is shared across newsletters: two publications picking the same
story pay for it once. The author-only "Seu comentário" field is never
AI-populated.
With the model off the path is exactly the deterministic one below.
Deterministic: the lineup becomes the issue document as **empty item
slots** — section headings, item headlines and `Sources:` lists, no
body text. Pending lineup items are promoted to `approved`, items
missing a dossier get a deterministic one built from their collected
articles (summary/facts are verbatim extractive, so excerpts and
citations stay valid). Never calls the model.
- Same lineup gate as `draft` (`blocked` with `section_empty` reasons).
- `409 not_empty` when the document already holds sentences — compose
  never overwrites written work.

### `POST /api/v1/issues/{id}/items/{ref}/compose` — write an item body
`{"text": "…", "citations": [1], "kind": "fact",
"version": "9f1c04ab22dd77ee"}` → `{"coverage": {…}, "flags": 0,
"version": "…", "added": N}`.
- `text` is the display form — free of `[n]` markers. Multi-sentence
  input is stored one sentence per line, each carrying every chosen
  citation; `added` reports how many sentences landed.
- `citations` must reference the item's sources (`invalid_citation`);
  an empty list stores an uncited fact, which `uncited_sentences`
  flags exactly like an AI-drafted one.
- `kind: "interpretation"` stores an editor-attributed block (no
  citation needed).
- The text lands at the end of the item's body, before its `Sources:`
  list. An `empty_items` readiness check blocks approval until every
  item has content.

## The issue document

### `GET /api/v1/issues/{id}/document`
Structured, never HTML: sections → items → sentences, each sentence with
`id`, `text` (citations stripped for display), `kind` (`fact` |
`interpretation`), `citations` (source refs), one `evidence` entry per
citation (the supporting passage for the evidence pane) and `flag` when
the citation check could not verify it; plus one `commentary` per
section and the sources of each item. Flags that match no single
sentence stay on the item.
```json
{"document": {
  "issue_id": 1, "number": 1, "status": "drafted",
  "version": "9f1c04ab22dd77ee",
  "subject": "Futuro Próximo — issue 1",
  "sections": [{
    "title": "O destaque",
    "commentary": {"mode": "required", "text": "My editorial take…"},
    "items": [{
      "ref": 1, "issue_item_id": 9,
      "headline": "Regulator opens consultation on AI labelling",
      "sentences": [
        {"id": "1.9f1c04ab", "text": "The regulator opened a consultation.",
         "kind": "fact", "citations": [1],
         "evidence": [{"ref": 1, "excerpt": "…the dossier passage supporting it…"}]},
        {"id": "1.22dd77ee", "text": "Comments close December 15.",
         "kind": "fact", "citations": [],
         "flag": {"note": "sentence has no citation"}},
        {"id": "1.33aa55bb", "text": "This changes the game.",
         "kind": "interpretation", "citations": []}
      ],
      "sources": [{"ref": 1, "name": "Radar Tech",
        "url": "https://fictional.example/radar/ai-labelling",
        "item_id": 41, "excerpt": "…", "date": "2026-11-10"}],
      "flags": [{"sentence": "…", "note": "entry has no citations"}]
    }]
  }],
  "coverage": {"ok": 10, "cited": 11, "total": 12, "flagged": 1}
}}
```
- `evidence[].excerpt` is the dossier fact behind that citation when one
  is known, else the article's opening — one entry per sentence
  citation.
- `coverage.ok` counts fact sentences that are cited **and** unflagged —
  the counter shows `ok/total`. `cited` counts every cited sentence
  (flagged ones included).

### `PATCH /api/v1/issues/{id}/sentences/{sid}` — edit a sentence
`{"text": "…new text…", "citations": [1], "version": "9f1c04ab22dd77ee"}`
→ `{"sentence": {…}, "coverage": {…}, "flags": 0, "version": "…"}`
- `text` is the display form — free of `[n]` markers; the server
  composes the stored form from `citations`.
- Fact sentences: `citations` must reference sources of that item
  (`invalid_citation`, `citation_required`), the edit must differ
  (`unchanged`) and must pass the re-check (`unsupported_claim` — the
  flag stays).
- Interpretation sentences: the text is stored as an editor-attributed
  block; no citation required.
- A successful edit on a flagged sentence resolves the flag. Any edit on
  an approved issue reverts it to `drafted` and voids its receipt.

### `DELETE /api/v1/issues/{id}/sentences/{sid}` — remove a sentence
`{"version": "9f1c04ab22dd77ee"}` — resolves its flag when present →
`{"coverage": {…}, "flags": 0, "version": "…", "removed": {…}}`.
`removed` is the undo payload: `{ref, index, text, kind, citations,
flag?}` — `index` is the sentence's position inside its item.

### `DELETE /api/v1/issues/{id}/items/{ref}/sentences` — remove all flagged
`{"version": "9f1c04ab22dd77ee"}` — the editor's bulk action: removes
every flagged sentence of item `ref` in one write and resolves their
flags → `{"coverage": {…}, "flags": 0, "version": "…", "removed": [{…}]}`
(`removed` entries in document order, same shape as the single-sentence
undo payload). Unflagged sentences and flags on other items are
untouched. `409 nothing_flagged` when the item has no flagged
sentences; `read_only` on sent/scheduled issues.

### `POST /api/v1/issues/{id}/items/{ref}/sentences` — restore sentences
The undo for both removals. Send one removed payload flat
(`{"index": 2, "text": "…", "kind": "fact", "citations": [1],
"flag": "…", "version": "…"}`) or a batch
(`{"sentences": [{…}], "version": "…"}`) — every entry lands back at
its recorded `index` inside the item, in one write, and a carried
`flag` is re-attached. An empty text or a citation the item does not
have refuses the whole restore; nothing is half-written.

### `POST /api/v1/issues/{id}/sentences/{sid}/interpretation`
`{"text": "…", "version": "9f1c04ab22dd77ee"}` (`text` optional —
defaults to the sentence's text without citations). Marks the sentence
as interpretation and resolves its flag.

### `PUT /api/v1/issues/{id}/commentary`
`{"section": "O destaque", "text": "…", "version": "9f1c04ab22dd77ee"}`
— the only write path for commentary; the AI never writes here. Reverts
an approved issue to `drafted` and voids its receipt.

## Composer blocks (KLOX-66)

Each section of the edition is an ordered list of blocks, reported in
`sections[].blocks` on the document: a pauta (`{"kind": "item",
"ref": N}`) or an author block (`{"kind": "heading"|"divider"|"link"|
"text", "text", "url?"}`). Blocks persist inside the stored markdown —
`####`, `---`, `- [label](url)`, `> text` — so every output surface
renders them with no schema change. A non-item block written right
after an item carries an `<!-- /item -->` boundary line (the parser
honors it, outputs never render it) since `> ` inside an item is an
interpretation sentence. Mutations address one block by
`(section, index)` inside that array, carry the document `version`
(stale → `409 conflict`), answer with the fresh `{"document": {…}}`,
and revert an approved issue to `drafted` like a sentence edit.

### `POST /api/v1/issues/{id}/blocks` — insert a block
`{"section": "O destaque", "kind": "heading", "text": "…",
"version": "…"}` — `position` (0-based) defaults to the end of the
section. `kind: "item"` takes `story_id` instead of `text`: the story
joins the edition as an approved `origin=manual` issue item and gets a
deterministic manual dossier — no model call. A story already in the
edition answers `409 blocked`; a rejected one must be un-rejected on
the board first. `link` needs a valid `url`.

### `PUT /api/v1/issues/{id}/blocks` — update a block's content
`{"section": "…", "index": 2, "text": "…", "url": "…", "version": "…"}`
— heading/link/text only; `item` and `divider` blocks have no editable
content (`400 validation`).

### `POST /api/v1/issues/{id}/blocks/move` — reorder inside a section
`{"section": "…", "from": 0, "to": 2, "version": "…"}` — an item block
moves with its whole story body.

### `DELETE /api/v1/issues/{id}/blocks` — remove a block
`{"section": "…", "index": 1, "version": "…"}` — removing an `item`
block also marks the issue item `removed` (it resurfaces in
suggestions) and drops its sentence flags.

## Newsletter template (KLOX-72)

The publication's default edition layout is an ordered block list on
the same vocabulary as the composer, plus `{"kind": "section",
"section": "O destaque"}` slots. Edition creation materializes it:
fixed blocks copy into the stored body verbatim, a `section` slot
emits that section's `##` heading and the week's items (the heading
keeps its place even with no items), and a `divider`/`link`/`image`/
`text` landing right after an item is written behind an
`<!-- /item -->` boundary. Sections the template never names fall back
to the stored section order at the tail; sections the template names
but the snapshot no longer has are skipped. Changes apply to editions
created after the save — in-progress editions keep the blocks their
body already materialized, and no edition edit writes back. Template
header blocks (before the first `##`) parse into an untitled leading
section the composer addresses as `section: ""`.

### `GET /api/v1/publications/{id}/template`
```json
{"template": {"version": "9f1c04ab22dd77ee",
              "blocks": [{"kind": "section", "section": "O destaque"}, …]}}
```
Until the first save the effective template is one `section` slot per
configured section — which materializes exactly like the legacy
layout. `version` is a hash of the stored list, shaped like the
document version.

### `PUT /api/v1/publications/{id}/template`
`{"blocks": […], "version": "…"}` — max 100 blocks; each entry
validates against the same `blockLine` rules the composer insert path
applies (`400 validation` otherwise: `section` slots need a title,
fixed kinds need renderable content, embeds store sanitized). A stale
or missing `version`, or a concurrent save, answers `409 conflict`;
the response carries the saved blocks and the rotated `version`.

### `POST /api/v1/publications/{id}/media`
Same base64 JSON contract as `POST /api/v1/issues/{id}/media`
(`content_type`, `data_b64`, `name`) scoped to the publication — the
template editor attaches banner art before any edition exists. The
media row is publication-scoped either way and serves publicly at
`/m/{id}-{token}`.

## Readiness / approve / send

### `GET /api/v1/issues/{id}/readiness`
```json
{"ready": false, "coverage": {"ok": 10, "cited": 11, "total": 12, "flagged": 1},
 "checks": [
  {"code": "pending_decisions", "ok": true},
  {"code": "uncited_sentences", "ok": false,
   "message": "1 sentence without a citation",
   "target": {"kind": "sentence", "ref": "1.22dd77ee"}},
  {"code": "flags_unresolved", "ok": true},
  {"code": "commentary_required", "ok": true}
 ]}
```

### `POST /api/v1/issues/{id}/approve`
Failed checks → `409 blocked` with the reasons.
When AI is on, approval then runs the citation check (KLOX-67 §7.5):
the deterministic lexical floor first (uncited facts, unsupported or
too-close claims already fail readiness), then exactly one Haiku call
re-checks every cited fact — author-edited AI claims included — for
inverted meaning. An `unsupported` verdict becomes a flag that blocks
approval until the author edits or removes the sentence. Capped at 3
checks per edition per day and counted against the workspace's 5 paid
actions/day; on cap, kill switch or provider failure the deterministic
results stand alone and approval proceeds. Otherwise the stored
receipt is returned — the same receipt on every repeated approve, and
from `GET /api/v1/issues/{id}` while the approval stands. An edit that
reverts the issue to `drafted` voids it; the next approve records a
fresh one.
```json
{"issue": {"id": 1, "status": "approved", "receipt": {…}, "…": "…"},
 "receipt": {"issue_id": 1, "number": 1,
             "document_version": "9f1c04ab22dd77ee",
             "approver": "creator@example.com",
             "approved_at": "2026-11-11 12:00:00", "checks": […]}}
```
`approver` is the claiming owner's e-mail — `""` while the session is
anonymous, never the workspace label.

### `POST /api/v1/issues/{id}/send`
`{"audience": "test"|"readers", "to": "optional@test.address", "when": "now"|"schedule", "at": "RFC3339"}`
Safe audience by default: `audience` is required — sending needs an
explicit choice. Requires `approved` (`blocked`/`not_approved`
otherwise). In demo mode every send lands in the outbox.
- `"test"` only records the outbox email and stamps
  `issue.test_sent_at` — the issue stays `approved` and editable, and
  can still go to readers. The marker unlocks `/after` and the public
  page (a test-sent issue shows "Test send recorded" there).
- `"readers"` + `"when":"now"` marks the issue `sent`; sent issues are
  read-only.
- `"readers"` + `"when":"schedule"` requires `"at"`, an RFC3339 time in
  the future (`bad_time` when missing, malformed or past). It marks the
  issue `scheduled`, stores the send time on `issue.scheduled_at`:
  locked like a sent issue, but nothing reaches the outbox yet (demo
  mode has no dispatch clock). Scheduling a `test` send is rejected.
```json
{"issue": {"id": 1, "status": "sent", "…": "…"},
 "outbox_email_id": 7, "audience": "readers",
 "to": ["readers@kloxus.demo"], "subject": "Futuro Próximo — issue 1"}
```
A scheduled send answers `{"issue": {…}, "scheduled": true,
"scheduled_at": "2026-11-12T08:00:00Z"}`.

**Live delivery** (`mail: "live"`): every rule below also holds for the
scheduled dispatch.
- `"test"` on an unclaimed workspace records to the outbox and stamps
  `issue.test_sent_at` — it completes the journey (after-sending
  screen, public page) without touching the provider and without
  marking the issue `sent`. `"readers"` unclaimed still answers
  `409 unclaimed_workspace` — claim first.
- `"test"` on a claimed workspace mails the claimed creator address
  only; any other `"to"` is rejected (`400`). The issue stays
  `approved` — stamped `test_sent_at`, never `sent`.
- `"readers"` mails confirmed (double opt-in, not unsubscribed) readers
  only. Addresses on the suppression list or sends over the daily caps
  (global and per-workspace, counted from the outbox ledger so they
  survive restarts) are refused and recorded — never dropped silently.
  A send where nothing leaves the app (no confirmed readers, every
  recipient refused, or every message falling back) keeps the issue
  `approved` so it can be sent again once deliverable; the response
  carries `delivery:"none"` or `"outbox_fallback"` and a `warnings`
  entry.
- Per-recipient idempotency keys dedupe retries; an explicit
  `Idempotency-Key` request header namespaces them, so a retried
  request mails nobody twice. A `status:"fallback"` row does not
  satisfy idempotency — it records a send that never left the app, so
  a retry requeues the same ledger row and really reaches the
  provider.
- A provider failure keeps the message as an outbox copy
  (`"fallback": true`) so the creator can still see what went out.
```json
{"issue": {…}, "audience": "readers", "delivery": "resend",
 "sent": 3, "refused": ["bounced@x.dev: address suppressed"],
 "deduped": 1, "to": ["…"], "outbox_email_id": 9}
```
`delivery` is `resend`, `outbox_fallback`, `mixed`, or `none`. A
`"test"` send refused outright answers `429 send_refused`.

### `POST /api/v1/issues/{id}/hold`
Only valid on a `scheduled` issue. Returns it to `drafted`, clears the
queued send time and drops the approval receipt (re-approval required —
Hold is an un-approval).
```json
{"issue": {"id": 1, "status": "drafted", "…": "…"}}
```

### `GET /api/v1/issues/{id}/after`
The post-send summary. Requires `sent`, `scheduled` or a stamped test
send (`issue.test_sent_at` — KLOX-53); `blocked`/`not_sent` otherwise.
```json
{"issue": {…}, "public_url": "https://demo.example/p/futuro-proximo/1?tok=…",
 "learned": ["Skip stories that just broke, with no confirmed outcome yet"],
 "guide": {"version": 3},
 "export_html": "/api/v1/issues/1/export?fmt=html",
 "export_md": "/api/v1/issues/1/export"}
```

### `GET /api/v1/issues/{id}/export?fmt=markdown|html`
Attachment download; only `approved`/`sent` (`blocked` otherwise).

### `GET /api/v1/issues/{id}/preview?reader=subscriber|non-subscriber&surface=email|web|phone`
```json
{"body_md": "# Futuro Próximo — issue 1\n\n…", "gated": true,
 "reader": "non-subscriber", "surface": "email"}
```
`non-subscriber` gates paid sections to the teaser; `surface` is
presentation-only (the client styles it).

## Outbox

### `GET /api/v1/publications/{id}/outbox`
```json
{"emails": [{"id": 7, "kind": "test", "to": "demo@reader.test",
             "subject": "Futuro Próximo — issue 1",
             "created_at": "2026-11-11 12:00:00",
             "provider": "outbox", "status": "sent"}]}
```
Kinds: `issue`, `test`, `magic_link`, `reader_confirm`. In live mode the
outbox doubles as the send ledger — every attempted message is a row
with `status` (`queued`, `sent`, `delivered`, `bounced`, `complained`,
`refused`, `fallback`), `provider` (`resend`/`outbox`) and
`provider_id` for webhook correlation.

### `GET /api/v1/outbox/{id}` — full email incl. `body_md`/`body_html`,
`provider`, `provider_id`, `status` and `error` (sanitized fallback
reason).

## Paywall & reader state

### `GET /api/v1/publications/{id}/reader`
```json
{"reader": {"email": "demo@reader.test", "name": "Demo Reader"},
 "subscribed": false, "subscribers": 0, "readers": 1,
 "issue": {"id": 1, "number": 1, "status": "sent", "…": "…"}}
```

### `POST /api/v1/publications/{id}/paywall/simulate`
Simulates a paid subscription and records the magic-link email in the
outbox.
```json
{"subscribed": true, "outbox_email_id": 8,
 "magic_link": "https://demo.example/p/futuro-proximo/1?key=…&tok=…"}
```

### `POST /api/v1/publications/{id}/paywall/cancel` → `{"subscribed": false}`

## Reader-facing endpoints

These serve the public reader pages (`/p/<slug>/<num>`, `/unsub`).
They accept `?tok=` — an AES-GCM sealed workspace reference — so a
reader link opens in any browser without ever exposing the editor's
workspace id. `?ws=` from links delivered before the change still works.

### `GET /api/v1/p/{slug}/{num}?key=…&tok=…&view=free`
`num` is the issue number (`latest` also works). Only `sent` or
`scheduled` issues are visible (`404` otherwise). The magic-link `key`
unlocks the full body; `view=free` forces the gated teaser.
```json
{"publication": {"name": "Futuro Próximo", "slug": "futuro-proximo", "language": "pt-BR"},
 "issue": {"number": 1, "subject": "…", "created_at": "…"},
 "body_md": "…", "gated": true, "subscribed": false,
 "reader": {"name": "Demo Reader"}, "paid_sections": ["O destaque"]}
```

### `POST /api/v1/p/{slug}/subscribe`
`{"email": "reader@example.com"}` — starts the double opt-in: a
confirmation email is sent; the reader receives nothing until they
confirm. A `reader_confirm` mail is deduped to one per reader per day.
An already-confirmed address answers `{"sent": false,
"already_confirmed": true}`; in demo mode the response carries
`confirm_url` so the link can be opened without an inbox.

### `GET /confirm-sub?pub={slug}&r={reader_id}&t={mac}&tok={tok}` — not JSON
Lands the double opt-in: the signed MAC proves the inbox was reached;
the reader is stamped `confirmed_at` and redirected to the publication
page (`400` page on a forged or malformed link).

### `GET /api/v1/unsub?pub={id|slug}&r={reader_id}&t={token}&tok={tok}`
One-click unsubscribe behind the signed link in every issue email;
forged tokens get `403`.
```json
{"unsubscribed": true}
```

### `POST /unsub?pub={slug}&r={reader_id}&t={mac}&tok={tok}` — not JSON
RFC 8058 one-click unsubscribe: issue emails carry
`List-Unsubscribe`/`List-Unsubscribe-Post` headers pointing here, so
mail clients unsubscribe with no page load (`200` empty, `403` forged).

## Inbound webhooks

### `POST /webhooks/resend`
Resend delivery events (`email.delivered`, `email.bounced`,
`email.complained`). The Svix signature is verified
(`svix-id`/`svix-timestamp`/`svix-signature`, 5-minute skew) — forged
requests get `401`. `delivered` marks the ledger row; `bounced` and
`complained` mark it, suppress the address globally (no workspace ever
mails it again) and unsubscribe every matching reader. Unknown event
types or unknown email ids answer `202` without work.

## Editorial guide

### `GET /api/v1/publications/{id}/guide`
```json
{"guide": {"version": 3, "content": "- Skip stories about X\n- Prefer primary sources"}}
```
`version: 0` / empty content before the first version.

### `GET /api/v1/publications/{id}/guide/versions`
```json
{"versions": [{"version": 3, "content": "…"}, {"version": 2, "content": "…"}]}
```

### `PUT /api/v1/publications/{id}/guide`
`{"content": "…"}` → `{"guide": {"version": 4, "content": "…"}}`
(same content → same version, nothing written).

### `POST /api/v1/publications/{id}/guide/rebuild`
Rebuilds the guide from recorded rejection reasons → `{"guide": {…}}`;
`conflict` when there is nothing to rebuild from.

## Costs

### `GET /api/v1/publications/{id}/costs?last=4`
```json
{"issues": [{"number": 1,
  "rows": [{"step": "triage", "model": "claude-haiku-5-5",
            "input_tokens": 1200, "output_tokens": 300, "cost_usd": 0.0012}],
  "total_usd": 0.0042}]}
```
Issue `0` holds unattributed pre-issue steps.

## Demo workspace

### `POST /api/v1/demo/reset` → `{"ok": true}`
Wipes and reseeds only the visitor's own workspace.

### `POST /api/v1/demo/new` → `{"workspace": {"id": "…", "label": "…"}}`
Abandons the current workspace, sets a fresh signed cookie
(`rate_limited` over the per-IP cap).
