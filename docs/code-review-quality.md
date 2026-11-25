# Code review quality — consolidated findings

Four independent quality reviews ran on 2026-11-24 against `main` @ `fa5ad77`:
backend Go (KLOX-188, 17 findings), Postgres/DBA (KLOX-189, 23 findings),
frontend React (KLOX-190, 30 findings) and the e2e/unit test suite
(KLOX-191, 16 findings). This page records every finding and what was done
about it — the fix card, the merged pull request and the deployed Fly
release, or the documented decision to defer.

Issue keys (`KLOX-NNN`) are the team's internal tracker; the linked pull
requests and releases are the publicly verifiable evidence. Releases are
the Fly deploy versions recorded on the serialized release ledger
(KLOX-26, per decision D36). "Still present" notes were re-verified on
`main` @ `8ff7995a`.

Status legend:

- **Fixed** — implemented in the named card, merged via the linked PR,
  deployed in the named release.
- **Partial** — the dispatched fix covered part of the finding; the
  remainder is noted.
- **Deferred** — scoped out of the pre-freeze fix waves by the team's
  recorded triage (KLOX-208 dispatch notes); registered debt, not a
  silent drop.
- **Open** — no fix card was dispatched; still observable in the code
  pointer given.

## Totals

| Review | Scope | Findings | Fixed | Deferred / open |
| --- | --- | --- | --- | --- |
| KLOX-188 — backend Go | `internal/` pipeline, web, LLM | 17 | 17 | 0 |
| KLOX-189 — Postgres/DBA | `migrations/`, `internal/store` | 23 (4 high, 9 med, 10 low) | 6 | 17 |
| KLOX-190 — frontend | `frontend/src` | 30 (3 high, 9 med, 18 low) | 9 | 21 |
| KLOX-191 — tests | `frontend/e2e`, `src/test`, CI | 16 (1 high, 5 med, 10 low) | 5 | 11 |

## KLOX-188 — backend Go (17/17 fixed)

Reviewer verdict: solid base (coherent domain packages, propagated `ctx`,
`crypto/subtle` admin, LLM cost guard isolated); what a senior judge would
notice first were the god-functions and the silenced errors.

| # | Finding | Status | Evidence |
| --- | --- | --- | --- |
| 1 | `RunPhaseA` 594 / `RunPhaseB` 298 lines, `pipeline.go` 2202 | **Fixed** | KLOX-209 · [PR #160](https://github.com/sthefanocordeiro/kloxus/pull/160) · v248 |
| 2 | `internal/web/api.go` 4737 lines / 122 funcs mixing features | **Fixed** | KLOX-210 · [PR #159](https://github.com/sthefanocordeiro/kloxus/pull/159) · v246 |
| 3 | persistence errors discarded silently (~46 `_ =`) | **Fixed** | KLOX-211 · [PR #154](https://github.com/sthefanocordeiro/kloxus/pull/154) · v250 |
| 4 | `json.Unmarshal` error ignored on approval receipt | **Fixed** | KLOX-211 · [PR #154](https://github.com/sthefanocordeiro/kloxus/pull/154) · v250 |
| 5 | `truncate` duplicated 6×; byte-slices could split UTF-8 (mojibake in PT-BR content) | **Fixed** | KLOX-211 · [PR #154](https://github.com/sthefanocordeiro/kloxus/pull/154) · v250 |
| 6 | `time.Sleep` + `DemoMode` branches inside the production runner | **Fixed** | KLOX-225 · [PR #167](https://github.com/sthefanocordeiro/kloxus/pull/167) · v258 |
| 7 | `context.Background()` in async handlers, untied to server lifecycle | **Fixed** | KLOX-226 · [PR #170](https://github.com/sthefanocordeiro/kloxus/pull/170) · v259 |
| 8 | long wiring funcs: `serveUI` 133, `web.New` 139, `cmdLLMCheck` 132, `config.Load` 102 | **Fixed** | KLOX-227 · [PR #168](https://github.com/sthefanocordeiro/kloxus/pull/168) · v256 |
| 9 | `parseIssueDocument` 361 lines | **Fixed** | KLOX-228 · [PR #165](https://github.com/sthefanocordeiro/kloxus/pull/165) · v257 |
| 10 | status literals scattered (~77 occurrences) | **Fixed** | KLOX-211 · [PR #154](https://github.com/sthefanocordeiro/kloxus/pull/154) · v250 |
| 11 | magic values (snippet limits, `max_tokens`, URLs, thresholds) | **Fixed** | KLOX-229 · [PR #166](https://github.com/sthefanocordeiro/kloxus/pull/166) · v261 |
| 12 | ad-hoc `http.Client{Timeout}` in 9 places | **Fixed** | KLOX-211 · [PR #154](https://github.com/sthefanocordeiro/kloxus/pull/154) · v250 |
| 13 | two logging mechanisms (`log.Printf` + `slog` + own `logf`) | **Fixed** | KLOX-230 · [PR #169](https://github.com/sthefanocordeiro/kloxus/pull/169) · v260 |
| 14 | `panic(err)` in `workspace.NewID` | **Fixed** | KLOX-230 · [PR #169](https://github.com/sthefanocordeiro/kloxus/pull/169) · v260 |
| 15 | test files named by ticket (`klox67_test.go`…) | **Fixed** | KLOX-216 · [PR #158](https://github.com/sthefanocordeiro/kloxus/pull/158) · v247 |
| 16 | `Claude outputs/` + `attachments/` audit reports at repo root | **Fixed** — moved to `docs/audits/` | KLOX-216 · [PR #158](https://github.com/sthefanocordeiro/kloxus/pull/158) · v247 |
| 17 | generic filenames in `internal/store` (`pipeline.go` 1211, `store.go` 815) | **Fixed** — split by aggregate (`issues.go`, `stories.go`, `catalog.go`…) | KLOX-216 + KLOX-230 · [PR #158](https://github.com/sthefanocordeiro/kloxus/pull/158), [PR #169](https://github.com/sthefanocordeiro/kloxus/pull/169) · v247, v260 |

## KLOX-189 — Postgres / DBA (6/23 fixed)

Reviewer verdict: good point decisions (advisory-lock migrations,
`FOR UPDATE SKIP LOCKED`, `ON CONFLICT` idempotency), but a DBA would not
approve as-was: a real FK bug, the pipeline's hottest query scanning the
whole `items` table, non-sargable TEXT timestamps, and stale-job
measurement on the wrong column. The four high-severity items plus the
schema-integrity mediums were fixed; the remaining structural debt is open.

| # | Finding | Status | Evidence |
| --- | --- | --- | --- |
| A1 | `DeletePublication` fails on any publication with `pauta_days`; `renditions`/`issue_exports` had no FK | **Fixed** | KLOX-195 · [PR #139](https://github.com/sthefanocordeiro/kloxus/pull/139) · v235; FKs completed in KLOX-220 · [PR #161](https://github.com/sthefanocordeiro/kloxus/pull/161) · v254 |
| A2 | `ItemsForPublication` windowed the entire `items` table before tenant filter | **Fixed** (candidate-scoped CTEs; EXPLAIN 527→27 input rows, 7.6 ms→0.4 ms) | KLOX-223 · [PR #164](https://github.com/sthefanocordeiro/kloxus/pull/164) · v255 |
| A3 | TEXT timestamps + `safe_ts()` make every range filter non-sargable; mixed formats sort wrong | **Fixed** (`timestamptz` columns + backfill + normalized write path) | KLOX-223 · [PR #164](https://github.com/sthefanocordeiro/kloxus/pull/164) · v255 |
| A4 | `RequeueStaleJobs` measured staleness from enqueue, not claim → parallel re-execution | **Fixed** (`claimed_at` recorded at claim; stale = `running AND claimed_at < cutoff`) | KLOX-195 · [PR #139](https://github.com/sthefanocordeiro/kloxus/pull/139) · v235 |
| M1 | duplicate migration numbering (4×`0002`…), no checksum | **Fixed** (renumbered `0001–0033`, sha256 checksums validated at boot, rename map) | KLOX-222 · [PR #162](https://github.com/sthefanocordeiro/kloxus/pull/162) · v253 |
| M2 | twin tables `item_embedding`/`item_embeddings`, confusingly named | **Open** — both still exist (`migrations/0013`, `0016`; `internal/store/embed.go`) | — |
| M3 | missing FKs/UNIQUEs on newer tables (`media`, `standard_messages`, `subscriptions`, `issue_items`) | **Fixed** (orphan purge + `ON DELETE CASCADE`, `UNIQUE(workspace_id, reader_id)`, `UNIQUE(issue_id, story_id)`, atomic upsert) | KLOX-220 · [PR #161](https://github.com/sthefanocordeiro/kloxus/pull/161) · v254 |
| M4 | missing indexes on real lookup paths (`story_items(item_id)`, `outbox_emails(provider_id)`, `decisions(issue_item_id)`…) | **Open** — no new indexes on those columns | — |
| M5 | per-row INSERT loops on remote Postgres (`SaveCatalogItems` and siblings) | **Open** — loop still per-row (`internal/store/catalog.go:210`) | — |
| M6 | read-modify-write `MAX(number)+1` outside a tx (`NextIssueNumber`) | **Open** — unchanged (`internal/store/issues.go:18`) | — |
| M7 | multi-statement writes without a transaction (`ScheduleIssue`, `ClearStories`…) | **Open** — unchanged (`internal/store/issues.go:200`, `stories.go:59`) | — |
| M8 | append-only tables without retention; `PurgeOldBodies` a single table-wide UPDATE | **Open** — unchanged (`internal/store/catalog.go:557`) | — |
| M9 | `subscriptions.magic_token` stored in plaintext (vs `magic_links.token_hash`) | **Open** — unchanged (`migrations/0001_init.sql:169`) | — |
| B1–B10 | statuses without CHECK, NULL-vs-`''` dual convention, JSON-in-TEXT, `score DESC` NULLS-first (`issue_items.go:61`), dead indexes, unpaginated `ListIssues` selecting `body_md`, `waitlist` off-convention, fragile `splitStatements`, naming, UNIQUE admitting NULLs | **Open** — all still observable on `main` | — |

## KLOX-190 — frontend React (9/30 fixed)

Reviewer verdict: good general state (lazy routes, centralized
`api.request`, `errText`, real a11y care); problems concentrated in dead
surface left by the builder removal, silently-failing mutations/autosave,
a POST inside `useQuery`, and pattern inconsistencies.

| # | Finding | Status | Evidence |
| --- | --- | --- | --- |
| A1 | rendition autosave fails silently and can lose the last edit | **Fixed** (pending body re-queued, `error` badge state, backoff retry) | KLOX-196 · [PR #140](https://github.com/sthefanocordeiro/kloxus/pull/140) · v231 |
| A2 | "Montar pauta" could loop on an invisible 404 (stale `picked` ids) | **Fixed** (stale picks pruned on 404; error surfaced) | KLOX-197 · [PR #143](https://github.com/sthefanocordeiro/kloxus/pull/143) · v234 |
| A3 | More/Account menus don't close on navigation | **Fixed** (close on `pathname` effect) | KLOX-213 · [PR #155](https://github.com/sthefanocordeiro/kloxus/pull/155) · v252 |
| M1 | `pautaFinish` POST as a `useQuery` queryFn (double-fire, no error/retry) | **Fixed** (`useMutation` on mount + error + retry) | KLOX-213 · [PR #155](https://github.com/sthefanocordeiro/kloxus/pull/155) · v252 |
| M2 | destination mutations never render errors; dialogs closed before completion | **Fixed** (`errText` rendered, dialogs close on `onSuccess`) | KLOX-213 · [PR #155](https://github.com/sthefanocordeiro/kloxus/pull/155) · v252 |
| M3 | Radar-delete error rendered behind the open `KDialog` | **Fixed** (error moved inside the dialog) | KLOX-213 · [PR #155](https://github.com/sthefanocordeiro/kloxus/pull/155) · v252 |
| M4 | ~45% of `api.ts` dead (≈50 methods + 26 interfaces) shipped in the landing bundle | **Fixed** (`api.ts` 859→~390 lines) | KLOX-212 · [PR #153](https://github.com/sthefanocordeiro/kloxus/pull/153) · v251 |
| M5 | dead code elsewhere (`claims.ts`, `KTabs`/`KPopover`, `fmtUSD`, `todayWrite`…) | **Fixed** | KLOX-212 · [PR #153](https://github.com/sthefanocordeiro/kloxus/pull/153) · v251 |
| M6 | local `gone`/`liked`/`picked` state duplicating server state without reconciliation | **Deferred** — still local-only (`frontend/src/pages/Today.tsx:35-38`); partially mitigated by server-side pauta filtering (KLOX-236 · [PR #175](https://github.com/sthefanocordeiro/kloxus/pull/175)) | — |
| M7 | pauta autosave loses keystrokes on tab close; no retry after failed persist | **Fixed** (`beforeunload` + `error` state + retry, `internal` verification on `main`) | KLOX-196 · [PR #140](https://github.com/sthefanocordeiro/kloxus/pull/140) · v231 |
| M8 | incomplete ARIA: `aria-haspopup="menu"` without menu semantics; tablist without tabpanel | **Deferred** — still present (`AppShell.tsx:249,296`; `Destinations.tsx:707`) | — |
| M9 | `window.prompt` for link insertion | **Deferred** — still present (`Destinations.tsx:514`) | — |
| B | 18 low items: three coexisting i18n patterns, `fill()` vs `.replace` split, inconsistent loading states, `role="alert"` coverage, `useSiteEntry` instantiated per-section, `useMutation` for a GET, inconsistent `useOutletContext`, full-page reload to toggle preview, `UnsubPage` raw fetch + destructive GET, duplicated `?sem_texto=`, duplicated `FORMAT_LABEL`, ref writes during render, missing pending guard on Calibrate, 660-line `DestinationStep`, `IssueRedirect` NaN path, add-news error collapsing into "no other news", double `["workspace"]` subscription, `key={i}` | **Deferred** — scoped out pre-freeze; most still present (spot-verified: `Today.tsx:15`/`PublicationPage.tsx:11`, `PautaPage.tsx:384`, `ReaderPage.tsx:101,386`, `AppShell.tsx:86-87`). Note: `role="alert"` coverage has since grown to 18 uses across 8 files, and the add-news path now renders `errText` (`PautaPage.tsx:219`) | — |

## KLOX-191 — e2e / unit test suite (5/16 fixed)

Reviewer verdict: the e2e safety plumbing is the most mature part of the
repo (3-mode harness failing closed, credential scrubbing, a meta-test for
the harness itself); gaps were a spec that never failed, no failure
artifacts in CI, missing transport preflight on mutating specs, and the
main flow stopping one step before "export".

| # | Finding | Status | Evidence |
| --- | --- | --- | --- |
| A1 | `live-checks.spec.ts` collected banned-word/EN-leak hits but asserted nothing (incl. a zero-assert `llm_mode` "gate") | **Fixed** (folded into `live-safe.spec.ts` with real assertions) | KLOX-215 · [PR #157](https://github.com/sthefanocordeiro/kloxus/pull/157) · v249 |
| M1 | no `forbidOnly`, no trace/screenshot on failure in Playwright | **Fixed** (`forbidOnly: !!CI`, `trace: retain-on-failure`, `screenshot: only-on-failure`) | KLOX-215 · [PR #157](https://github.com/sthefanocordeiro/kloxus/pull/157) · v249 |
| M2 | mutating specs ran without the transport preflight | **Fixed** (`preflight` inside `onboard()`) | KLOX-215 · [PR #157](https://github.com/sthefanocordeiro/kloxus/pull/157) · v249 |
| M3 | helper/spec duplication (claim helpers, dead `claimIfSynthetic`, duplicated "legal pages", cross-language copy of `supportedSentence`/`approvedIssue` vs `smoke.sh`) | **Partial** — helpers unified, dead code removed, "legal pages" deduplicated, `approvedIssue`↔`smoke.sh` cross-commented; the word-extraction helper still lives in both languages | KLOX-215 · [PR #157](https://github.com/sthefanocordeiro/kloxus/pull/157) · v249 |
| M4 | e2e main flow stops before actually exporting | **Fixed** (`pauta-flow.spec.ts` now drives Copy + asserts `exported`/`exported_formats` via API) | KLOX-215 · [PR #157](https://github.com/sthefanocordeiro/kloxus/pull/157) · v249 |
| M5 | surfaces with no tests (`Sources`, `Settings`, `Publications`, `ReaderPage`, `markdown`, `ClaimEmail`, `SiteChrome`, `ErrorPage`, `BackButton`, most of `api.ts`) | **Deferred** — partially mitigated: new vitest specs for `sources`, `publications`, `landing`, `start`, `stepcounter`, `today` exist; `ReaderPage`/`markdown`/`SiteChrome`/`ErrorPage`/`api.ts` internals still untested | — |
| B1 | `waitRun` manual polling discards the last status | **Deferred** — unchanged (`e2e/fallback.spec.ts:50`) | — |
| B2 | positional `querySelectorAll("button")[1]` selector | **Fixed incidentally** — rewritten as `within(first).getByRole("button", …)` when the buttons became icons (KLOX-200 · [PR #145](https://github.com/sthefanocordeiro/kloxus/pull/145)); not a dedicated fix card | — |
| B3 | singular `getByRole("link")` assumes exactly one link | **Deferred** — unchanged (`src/test/publication.test.tsx:101`) | — |
| B4 | obfuscated `replace("T\nd","T\n")` test string | **Deferred** — unchanged (`src/test/renditions.test.ts:64`) | — |
| B5 | `as never` silences mock typing | **Deferred** — unchanged (`src/test/pauta.test.tsx:55`) | — |
| B6 | `boundingBox()!` non-null assertion | **Deferred** — unchanged (`e2e/mobile-pauta.spec.ts:12-13`) | — |
| B7 | `test.exclude` replaces vitest defaults instead of extending `configDefaults` | **Deferred** — unchanged (`vite.config.ts:24`) | — |
| B8 | `webServer.timeout: 120_000` can flake cold local boots | **Deferred** — unchanged (`playwright.config.ts:75`) | — |
| B9 | `no-publishing` spec gated on env, not the target's actual flag | **Deferred** — unchanged (`e2e/no-publishing.spec.ts:7-10`) | — |
| B10 | stale comments referencing deleted specs (`compose.spec.ts`, `journey.spec.ts`) | **Deferred** — unchanged (`e2e/fallback.spec.ts:38,97`) | — |

## Process notes (for the jury)

- Every fix card ran the full relay: author → QA PASS on the exact head
  SHA → CTO technical approval on the same SHA → merge → serialized
  deploy window (ledger: KLOX-26, decision D36) → hosted smoke + e2e.
- Three DB cards were re-scoped and consolidated during execution rather
  than silently dropped: KLOX-218/219 folded into KLOX-223, KLOX-221
  closed as a duplicate of KLOX-222 (recorded on the KLOX-217 epic).
- The two largest refactors (KLOX-188 #1, #2) were behavior-preserving
  mechanical moves backed by the existing suite — each re-gated after
  every rebase against `main`.
- What the reviews praised and which remains true: advisory-locked
  versioned migrations, tenant-scoped queries, `FOR UPDATE SKIP LOCKED`
  claims, `unnest` batching where it mattered, the fail-closed e2e
  harness, `getByRole` selector discipline, the `api.request` +
  `ApiRequestError` + `errText` error base, and route-level bundle
  budgets.

Deferred/open items are tracked as debt for the post-freeze backlog; the
documented scope decisions live in the KLOX-208 dispatch triage and the
KLOX-217 epic reconciliation.
