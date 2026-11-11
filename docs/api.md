# Kloxus JSON API — `/api/v1`

The v2 contract: the existing behaviour exposed as JSON. The product
rules do not change — the AI never writes commentary, nothing is sent
without explicit approval, every factual sentence links to a source. The
interface shows state; the server decides it.

- **Auth/isolation:** the same workspace cookie, rate limits and
  per-visitor isolation as the HTML routes. No other auth in demo mode.
- **Bodies:** JSON in, JSON out. `Content-Type: application/json`.
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
       "message": "1 flagged sentence(s) unresolved — edit, mark as interpretation or remove each",
       "target": {"kind": "sentence", "ref": "3.2"}},
      {"code": "commentary_required",
       "message": "commentary required in \"The big one\"",
       "target": {"kind": "section", "ref": "The big one"}}
    ]
  }
}
```

Stable codes: `not_found`, `validation`, `bad_json`, `conflict`,
`blocked`, `read_only` (sent issues), `reason_required`,
`invalid_source`, `invalid_citation`, `citation_required`, `unchanged`,
`unsupported_claim`, `sentence_gone`, `no_alternative`, `not_rejected`,
`not_approved`, `no_runs`, `monitor`, `rate_limited`, `internal`.

Sentence ids are `"{itemRef}.{seq}"` (e.g. `3.2`) — stable for the life
of the document but reassigned when sentences above them are removed.

---

## Workspace

### `GET /api/v1/workspace`
```json
{"id": "local", "label": "local demo", "mode": "demo", "hosted": false}
```

## Publications

### `GET /api/v1/publications`
```json
{"publications": [{"id": 1, "slug": "futuro-proximo", "name": "Futuro Próximo", "...": "…"}]}
```

### `GET /api/v1/publications/{id|slug}` — publication + settings
```json
{
  "publication": {
    "id": 1, "slug": "futuro-proximo", "name": "Futuro Próximo",
    "language": "pt-BR", "topic": "…", "audience": "…",
    "editorial_line": "…", "tone": "…", "frequency": "weekly",
    "ai_disclosure": "short",
    "sections": [{"title": "The big one", "items": 1, "commentary": "required"}],
    "paywall": {"enabled": true, "paid_sections": ["The big one"], "price": "V$ 12/month"}
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
  "monitor": {"provider": "", "id": "", "status": ""}
}]}
```

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
→ `sent`. Sent issues are read-only (`read_only`).

### `GET /api/v1/issues/{id}` — one issue, same shape as the list row.

### `GET /api/v1/issues/{id}/items` — triage cards
```json
{"items": [{
  "id": 9, "issue_id": 1, "story_id": 4, "section": "The big one",
  "score": 0.91, "reason": "why this belongs in the issue",
  "status": "pending", "origin": "phaseA",
  "title": "Regulator opens consultation on AI labelling",
  "outlets": ["Radar Tech", "Pulso Digital"],
  "reject_reason": "…"
}], "pending": 5}
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
`/runs/{id}/events`). Open decisions → `blocked` with a
`pending_decisions` reason.

## The issue document

### `GET /api/v1/issues/{id}/document`
Structured, never HTML: sections → items → sentences, each sentence with
`id`, `text` (citations stripped for display), `kind` (`fact` |
`interpretation`), `citations` (source refs) and `flag` when the
citation check could not verify it; plus one `commentary` per section
and the sources of each item. Flags that match no single sentence stay
on the item.
```json
{"document": {
  "issue_id": 1, "number": 1, "status": "drafted",
  "subject": "Futuro Próximo — issue 1",
  "sections": [{
    "title": "The big one",
    "commentary": {"mode": "required", "text": "My editorial take…"},
    "items": [{
      "ref": 1, "issue_item_id": 9,
      "headline": "Regulator opens consultation on AI labelling",
      "sentences": [
        {"id": "1.1", "text": "The regulator opened a consultation.",
         "kind": "fact", "citations": [1]},
        {"id": "1.2", "text": "Comments close December 15.",
         "kind": "fact", "citations": [],
         "flag": {"note": "sentence has no citation"}},
        {"id": "1.3", "text": "This changes the game.",
         "kind": "interpretation", "citations": []}
      ],
      "sources": [{"ref": 1, "name": "Radar Tech",
        "url": "https://fictional.example/radar/ai-labelling",
        "item_id": 41, "excerpt": "…", "date": "2026-11-10"}],
      "flags": [{"sentence": "…", "note": "entry has no citations"}]
    }]
  }],
  "coverage": {"cited": 11, "total": 12, "flagged": 1}
}}
```

### `PATCH /api/v1/issues/{id}/sentences/{sid}` — edit a sentence
`{"text": "…new text…"}` → `{"sentence": {…}, "coverage": {…}, "flags": 0}`
- Fact sentences: the new text must cite an existing source of that item
  (`invalid_citation`, `citation_required`), must differ (`unchanged`)
  and must pass the re-check (`unsupported_claim` — the flag stays).
- Interpretation sentences: the text is stored as an editor-attributed
  block; no citation required.
- A successful edit on a flagged sentence resolves the flag. Any edit on
  an approved issue reverts it to `drafted`.

### `DELETE /api/v1/issues/{id}/sentences/{sid}` — remove a sentence
Resolves its flag when present → `{"coverage": {…}, "flags": 0}`.

### `POST /api/v1/issues/{id}/sentences/{sid}/interpretation`
`{"text": "…"}` (optional — defaults to the sentence's text without
citations). Marks the sentence as interpretation and resolves its flag.

### `PUT /api/v1/issues/{id}/commentary`
`{"section": "The big one", "text": "…"}` — the only write path for
commentary; the AI never writes here. Reverts an approved issue to
`drafted`.

## Readiness / approve / send

### `GET /api/v1/issues/{id}/readiness`
```json
{"ready": false, "coverage": {"cited": 11, "total": 12, "flagged": 1},
 "checks": [
  {"code": "pending_decisions", "ok": true},
  {"code": "uncited_sentences", "ok": false,
   "message": "1 sentence(s) without a citation",
   "target": {"kind": "sentence", "ref": "1.2"}},
  {"code": "flags_unresolved", "ok": true},
  {"code": "commentary_required", "ok": true}
 ]}
```

### `POST /api/v1/issues/{id}/approve`
Failed checks → `409 blocked` with the reasons. Otherwise:
```json
{"issue": {"id": 1, "status": "approved", "…": "…"},
 "receipt": {"issue_id": 1, "number": 1,
             "approved_at": "2026-11-11T12:00:00Z", "checks": […]}}
```

### `POST /api/v1/issues/{id}/send`
`{"audience": "test"|"readers", "to": "optional@test.address"}`
Safe audience by default: `audience` is required — sending needs an
explicit choice. Requires `approved` (`blocked`/`not_approved`
otherwise). In demo mode every send lands in the outbox.
```json
{"issue": {"id": 1, "status": "sent", "…": "…"}, "outbox_email_id": 7}
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
             "created_at": "2026-11-11 12:00:00"}]}
```
Kinds: `issue`, `test`, `magic_link`.

### `GET /api/v1/outbox/{id}` — full email incl. `body_md`/`body_html`.

## Paywall & reader state

### `GET /api/v1/publications/{id}/reader`
```json
{"reader": {"email": "demo@reader.test", "name": "Demo Reader"},
 "subscribed": false,
 "issue": {"id": 1, "number": 1, "status": "sent", "…": "…"}}
```

### `POST /api/v1/publications/{id}/paywall/simulate`
Simulates a paid subscription and records the magic-link email in the
outbox.
```json
{"subscribed": true, "outbox_email_id": 8,
 "magic_link": "https://demo.example/pub/1/reader?key=…&ws=…"}
```

### `POST /api/v1/publications/{id}/paywall/cancel` → `{"subscribed": false}`

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
