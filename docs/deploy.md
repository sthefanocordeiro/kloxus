# Deploying to Fly.io

One Fly app matters for the submission:

| App | URL | Config | Role |
| --- | --- | --- | --- |
| `kloxus` | https://kloxus.fly.dev | `fly.toml` | **The real app.** Every PR deploys and is verified here (D35). |

`fly.toml` points at `kloxus`, so a bare `fly deploy` can only ever hit the
real app. `fly.demo.toml`/`kloxus-demo` is a legacy fixture fallback that is
not currently hosted for judges — never deploy to it or change its secrets.
The deterministic fixture demo runs locally with `go run ./cmd/kloxus demo`.

## The real app

`fly.toml` sets hosted mode (`KLOXUS_DATA_DIR=/data` on the `kloxus_data`
volume, `gru`), `auto_stop_machines = "stop"` and `min_machines_running = 0`
so it costs nothing while idle; a cold start answers `/healthz` inside the
check's grace period. Secrets (Anthropic, Resend, Firecrawl, AbacatePay,
`DATABASE_URL`, `KLOXUS_MASTER_KEY`, `KLOXUS_WEBHOOK_SECRET`,
`KLOXUS_PUBLIC_URL`) are set on the app with `fly secrets set` — never in
the file, the repo or a PR.

### Email (Resend)

- `RESEND_API_KEY` (sending-only, scoped to `kloxus-ai.trendfi.com.br`)
  flips mail to live mode; without it every send stays in the outbox.
- `KLOXUS_FROM_ADDRESS` is the full sender —
  `Kloxus <news@kloxus-ai.trendfi.com.br>` — a display name is required.
- `RESEND_WEBHOOK_SECRET` verifies the Svix signature on
  `POST /webhooks/resend`, which feeds delivered/bounced/complained into
  the send ledger and the global suppression list. The Resend key
  cannot query delivery status, so without the webhook every send
  stalls at `sent` and bounces keep mailing dead addresses.
- `KLOXUS_EMAIL_DAILY_CAP` (80) and `KLOXUS_EMAIL_WS_DAILY_CAP` (25)
  are env-tunable; the ledger in Postgres makes them survive restarts.

#### Resend dashboard state (required — check after any outage)

The app cannot see the dashboard: a disabled or misconfigured endpoint
fails silently from its side — every send stays `sent` forever. Keep
this checklist green; the workspace posture and smoke script trip on
it (below).

- **Domain** — `kloxus-ai.trendfi.com.br` verified: SPF + DKIM records
  green in Resend → Domains. An unverified domain still accepts sends
  but delivery is unreliable.
- **API key** — sending-only, scoped to that domain; lives only in the
  Fly secret `RESEND_API_KEY`.
- **Webhook endpoint** — Resend → Webhooks: one endpoint,
  `https://kloxus.fly.dev/webhooks/resend`, **enabled**, subscribed to
  `email.delivered`, `email.bounced`, `email.complained`.
- **Signing secret** — the endpoint's `whsec_…` value is the Fly
  secret `RESEND_WEBHOOK_SECRET`. Rotating the endpoint (delete +
  recreate) means rotating the secret too.

#### When the webhook dies

Svix (Resend's webhook layer) disables an endpoint after sustained
delivery failures — e.g. while the app was down, or while it answered
401/503 because the secret was missing. Reactivation is dashboard-only:
open the endpoint, click **Enable**, then re-send a test email.

The app-side tripwires so it never dies silently again:

- `GET /api/v1/workspace` reports `mail_webhook` (`configured` /
  `unconfigured` — is the secret loaded) and `mail_stale_sent` (sends
  the provider accepted more than 24h ago that no event ever marked).
  `mail_stale_sent > 0` with `mail_webhook = "configured"` is the exact
  signature of a dashboard-disabled endpoint.
- Every real send logs
  `resend webhook silent: sends older than 24h still 'sent'` while
  stale rows exist — visible in `fly logs -a kloxus`.
- `scripts/smoke.sh` fails a live target whose webhook secret is not
  configured, and probes `POST /webhooks/resend` for a 401 (route up,
  secret loaded) — a dead route or missing secret fails smoke before
  any send check runs.

Manual re-verify after re-enabling: approve an issue on the app and
send a test email to the claimed address; the outbox row must flip
`sent` → `delivered` within a minute and `mail_stale_sent` must be 0.

### Payments (AbacatePay)

- `ABACATEPAY_API_KEY` — the hackathon test-mode key (`abc_dev_…`)
  flips the paid-section checkout to the real provider (D34/D58).
  Unset or unreachable, the fake provider completes the checkout and
  the UI labels it "simulated"; the UI shows "test mode — no real
  charge" whenever the configured key is a dev key.
- `ABACATEPAY_WEBHOOK_SECRET` — the shared secret chosen in the
  AbacatePay dashboard for the webhook endpoint below; it arrives as
  `X-Webhook-Secret` or `?webhookSecret=`. Empty fails the receiver
  closed (`503`), like the other webhooks.
- **Webhook endpoint** — AbacatePay → Webhooks: one endpoint,
  `https://kloxus.fly.dev/webhooks/abacatepay`, subscribed to
  `billing.paid`. Confirmation is not webhook-dependent: the reader's
  return page also polls the provider (throttled), so a missing or
  delayed webhook only slows the grant.
- One PIX checkout buys 30 days of access (`subscriptions.paid_until`,
  D58) — PIX has no recurring rail on AbacatePay, so each expiry is a
  new one-time checkout.

## Postgres (Supabase)

All app data lives in one Supabase Postgres database reached through
`DATABASE_URL`. The `/data` volume now only persists the cookie-signing
secret, so losing it costs sessions, not data.

- `DATABASE_URL` is the Supabase **session pooler** URI
  (`postgresql://postgres.<ref>:<password>@aws-<n>-<region>.pooler.supabase.com:5432/postgres`).
  The app applies its migrations on boot; no separate migrate step.
- The app appends `default_query_exec_mode=exec` to the connection
  string automatically — Supabase's transaction pooler does not support
  the extended query protocol. Nothing to set by hand.
- Idle workspaces (no request in ~21 days) are deleted by the in-app
  sweeper to stay under Supabase's 500 MB free-tier cap.

### Backups

`.github/workflows/backup.yml` runs `pg_dump` nightly (06:00 UTC) and
keeps the custom-format dump as a 30-day workflow artifact. It reads the
`DATABASE_URL` repository secret — set it once in repo settings, same
value as the Fly secret.

### Restore

```bash
# download the artifact from the backup run you want, then:
pg_restore --clean --if-exists --no-owner --no-privileges \
  --dbname "$DATABASE_URL" kloxus-YYYYMMDD.dump
```

For a full rebuild on an empty database just run `pg_restore` without
`--clean`; the app's next boot applies any newer migrations on top.
Verify afterwards:

```bash
psql "$DATABASE_URL" -c 'select count(*) from workspaces; select count(*) from publications;'
scripts/smoke.sh https://kloxus.fly.dev
```

`pg_dump`/`pg_restore` come from `postgresql-client` — use client ≥ the
server's major version (Supabase is on Postgres 15+; client 17 works
either direction).

## Deploy a branch

```bash
scripts/deploy-fly.sh        # fly deploy + prints the release version
```

Deploys are serialized: the branch under review is what is deployed.
If two PRs are in flight, finish one round before deploying the next.

## Verify

```bash
scripts/smoke.sh https://kloxus.fly.dev            # health → SPA → sample → workspace → fixture run → reader page
cd frontend && E2E_BASE_URL=https://kloxus.fly.dev E2E_LIVE_SAFE=1 npx playwright test
```

Hosted verification must never send real email; model calls are
accepted only on the real app inside the spend guards. The safe path
is selected by server-side evidence, not a client flag — `smoke.sh`
and every e2e spec preflight `/api/v1/workspace` and refuse to run a
mail-capable step the target's own posture can't prove safe. On the
model dimension `smoke.sh` accepts three postures: the deterministic
demo pipeline (`mode=demo` — replay fixtures, model-free either way);
the real app with the model hard-off (`mode=real`, `llm_mode=off` —
deterministic runs over the shared ingested items, with `llm.None`
failing any model call loudly); and, since KLOX-95 turned the engine
on, the real app with `llm_mode=auto` — the same journey then makes
real Anthropic calls bounded by the KLOX-67 guards (≤2
runs/workspace/day, ≤200k tokens/run, ≤$0.25/run, ≤$1/day, kill
switch). A guard refusal surfaces as run status `blocked` and fails
the smoke loudly — that refusal is itself the cap-enforcement
evidence. On the mail dimension
(KLOX-59) it accepts two postures: outbox (`mail=outbox`,
`mail_transport=outbox`), which runs the full claim-and-send journey;
or live mail (`mail=live`) on an unclaimed workspace, where the readers
send must hit the claim gate (409 — the provider is never touched) and
the test send must land in the outbox ledger (`delivery=outbox`,
`provider=outbox` — nothing mailed). A live target that reports
`claimed=true` fails closed, since a test send there would reach the
owner for real — as do an unknown transport and a real target with an
unknown `llm_mode`. The mutating e2e specs still require the demo
pipeline — their journeys depend on fixture content the real target
does not serve — so against a `mail=live` target their preflight keeps
refusing closed. `E2E_LIVE_SAFE=1` is the hosted opt-in that narrows
collection to `e2e/live-safe.spec.ts` (KLOX-176): an explicit
read-only subset — posture evidence, landing, sample, legal pages,
admin-guard and webhook-refusal probes, the claim gate and the
publishing-off 404s — gated by `assertLiveSafe` on the target's own
`/api/v1/workspace` (mode=real, mail=live over resend, unclaimed,
webhook configured). No claims, sends, publications, runs or model
calls; the flag changes nothing for any other spec.

The live-mail contract (claim gate, confirmed readers, `delivery`
outcomes, fallback) is verified **locally** against the loopback fake
provider — never against the hosted app:

```bash
cd frontend
node ../scripts/fake-resend.mjs &            # or let playwright boot it
E2E_LIVE_MAIL=1 npx playwright test          # synthetic Resend on 127.0.0.1:8399
```

`E2E_LIVE_MAIL` boots the app with `KLOXUS_FAKE_RESEND_URL` — a
literal-loopback-only endpoint validated at startup (no userinfo, path,
query, fragment, DNS names or redirects; mutually exclusive with
`RESEND_API_KEY`; synthetic credential). The modes are mutually
exclusive: `E2E_BASE_URL` + `E2E_LIVE_MAIL` is a hard error, no
webServer is reused, and the app boots with `KLOXUS_NO_DOTENV=1` and
scrubbed credentials so nothing inherited can leak in.

A PR is not done until both hosted checks pass against the Fly URL, and
its description carries the URL, the release version and both outputs.

## Merge

The PR merges to `main` only after the hosted verification above. The
`deploy` workflow (`.github/workflows/deploy.yml`) then deploys `main` to
`kloxus` and re-runs the smoke script in CI — it needs the `FLY_API_TOKEN`
repository secret. Verify the run after merge:

```bash
fly status -a kloxus    # machine on the new image
scripts/smoke.sh https://kloxus.fly.dev
```

## Roll back

```bash
fly releases -a kloxus                 # find the previous good image
fly deploy -a kloxus --image registry.fly.io/kloxus:<previous-tag>
scripts/smoke.sh https://kloxus.fly.dev
```

Workspace data lives in Postgres, so a rollback does not lose visitor
workspaces (only schema-incompatible data would, so restore from a
nightly dump if an old build cannot read the migrated schema).

## Ops notes

- `fly logs -a kloxus` shows request ids, not cookies or user data.
- The cookie-signing secret is generated at `/data/cookie.secret` on first
  boot (or set `KLOXUS_COOKIE_SECRET`). Losing it only invalidates session
  cookies — visitors get fresh workspaces.
- Free allowances: two small `shared-cpu-1x` 256 MB machines and two 1 GB
  volumes across both apps stay inside Fly's free usage for a personal org.
