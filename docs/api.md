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
       "message": "1 flagged sentence(s) unresolved — edit, mark as interpretation or remove each",
       "target": {"kind": "sentence", "ref": "3.9f1c04ab"}},
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
Every ranked story of the run, best first — the left "Pautas" column
plus the lineup state in one snapshot:
```json
{"candidates": [{
  "story_id": 4, "item_id": 9, "title": "…", "outlets": ["Radar Tech"],
  "sources": 2, "duplicates": 1,
  "score": 8.7, "reason": "why this belongs", "rank": 1,
  "in_issue": true, "section": "The big one",
  "suggested_section": "The big one",
  "status": "approved", "origin": "phaseA", "position": 0,
  "dossier": true, "available": true
}],
 "sections": [{"title": "The big one", "capacity": 1, "commentary": "required"}],
 "demo": true}
```
- `in_issue` = currently on the board (`pending`/`approved`); stories
  removed without a reason come back `status: "removed"`, rejections
  `status: "rejected"` — neither is in the issue.
- `suggested_section` only on AI-picked items (`origin: "phaseA"`).
- `dossier` = a dossier already exists; creator-added stories get one
  generated during the draft run.
- In demo mode only the top-`14` ranked stories are `available`;
  adding another → `demo_unavailable`.

### `PUT /api/v1/issues/{id}/lineup` — set the board
`{"entries": [{"story_id": 4, "section": "Worth knowing"}, …]}` —
the full desired lineup; order within a section is the array order
(`position` 0-based). Returns the candidates snapshot (plus `guide`
when a re-included rejection rebuilt it).
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
    "title": "The big one",
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
`{"coverage": {…}, "flags": 0, "version": "…"}`.

### `POST /api/v1/issues/{id}/sentences/{sid}/interpretation`
`{"text": "…", "version": "9f1c04ab22dd77ee"}` (`text` optional —
defaults to the sentence's text without citations). Marks the sentence
as interpretation and resolves its flag.

### `PUT /api/v1/issues/{id}/commentary`
`{"section": "The big one", "text": "…", "version": "9f1c04ab22dd77ee"}`
— the only write path for commentary; the AI never writes here. Reverts
an approved issue to `drafted` and voids its receipt.

## Readiness / approve / send

### `GET /api/v1/issues/{id}/readiness`
```json
{"ready": false, "coverage": {"ok": 10, "cited": 11, "total": 12, "flagged": 1},
 "checks": [
  {"code": "pending_decisions", "ok": true},
  {"code": "uncited_sentences", "ok": false,
   "message": "1 sentence(s) without a citation",
   "target": {"kind": "sentence", "ref": "1.22dd77ee"}},
  {"code": "flags_unresolved", "ok": true},
  {"code": "commentary_required", "ok": true}
 ]}
```

### `POST /api/v1/issues/{id}/approve`
Failed checks → `409 blocked` with the reasons. Otherwise the stored
receipt is returned — the same receipt on every repeated approve, and
from `GET /api/v1/issues/{id}` while the approval stands. An edit that
reverts the issue to `drafted` voids it; the next approve records a
fresh one.
```json
{"issue": {"id": 1, "status": "approved", "receipt": {…}, "…": "…"},
 "receipt": {"issue_id": 1, "number": 1,
             "document_version": "9f1c04ab22dd77ee",
             "approver": "local demo",
             "approved_at": "2026-11-11 12:00:00", "checks": […]}}
```

### `POST /api/v1/issues/{id}/send`
`{"audience": "test"|"readers", "to": "optional@test.address", "when": "now"|"schedule"}`
Safe audience by default: `audience` is required — sending needs an
explicit choice. Requires `approved` (`blocked`/`not_approved`
otherwise). In demo mode every send lands in the outbox.
- `"test"` only records the outbox email — the issue stays `approved`
  and can still go to readers.
- `"readers"` + `"when":"now"` marks the issue `sent`; sent issues are
  read-only.
- `"readers"` + `"when":"schedule"` marks the issue `scheduled`:
  locked like a sent issue, but nothing reaches the outbox yet (demo
  mode has no dispatch clock). Scheduling a `test` send is rejected.
```json
{"issue": {"id": 1, "status": "sent", "…": "…"},
 "outbox_email_id": 7, "audience": "readers"}
```

### `POST /api/v1/issues/{id}/hold`
Only valid on a `scheduled` issue. Returns it to `drafted` and drops the
approval receipt (re-approval required — Hold is an un-approval).
```json
{"issue": {"id": 1, "status": "drafted", "…": "…"}}
```

### `GET /api/v1/issues/{id}/after`
The post-send summary. Requires `sent` or `scheduled` (`blocked`/
`not_sent` otherwise).
```json
{"issue": {…}, "public_url": "https://demo.example/app/p/futuro-proximo/1?ws=…",
 "learned": ["Cedo demais — Universidade federal lança modelo…"],
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
 "magic_link": "https://demo.example/app/p/futuro-proximo/1?key=…&ws=…"}
```

### `POST /api/v1/publications/{id}/paywall/cancel` → `{"subscribed": false}`

## Reader-facing endpoints

These serve the public reader pages (`/app/p/<slug>/<num>`,
`/app/unsub`). They accept `?ws=` to resolve the creator's workspace —
the link a reader receives carries it so it opens in any browser.

### `GET /api/v1/p/{slug}/{num}?key=…&ws=…&view=free`
`num` is the issue number (`latest` also works). Only `sent` or
`scheduled` issues are visible (`404` otherwise). The magic-link `key`
unlocks the full body; `view=free` forces the gated teaser.
```json
{"publication": {"name": "Futuro Próximo", "slug": "futuro-proximo", "language": "pt-BR"},
 "issue": {"number": 1, "subject": "…", "created_at": "…"},
 "body_md": "…", "gated": true, "subscribed": false,
 "reader": {"name": "Demo Reader"}, "paid_sections": ["The big one"]}
```

### `GET /api/v1/unsub?pub={id|slug}&r={reader_id}&t={token}&ws={ws}`
One-click unsubscribe behind the signed link in every issue email;
forged tokens get `403`.
```json
{"unsubscribed": true}
```

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
