# Deploying to Fly.io

Two apps exist, and the difference matters:

| App | URL | Config | Role |
| --- | --- | --- | --- |
| `kloxus` | https://kloxus.fly.dev | `fly.toml` | **The real app.** Every PR deploys and is verified here (D35). |
| `kloxus-demo` | https://kloxus-demo.fly.dev | `fly.demo.toml` | The fixture demo — the fallback URL for judges. **Untouched** by this routine: no deploys, no secret changes. |

`fly.toml` points at `kloxus`, so a bare `fly deploy` can only ever hit the
real app. Deploying the demo deliberately means saying so:
`fly deploy -c fly.demo.toml`.

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
  the send ledger and the global suppression list. Point the Resend
  webhook at `https://kloxus.fly.dev/webhooks/resend` and subscribe to
  those three events; the Resend key cannot query delivery status, so
  without the webhook every send stalls at `sent` and bounces keep
  mailing dead addresses.
- `KLOXUS_EMAIL_DAILY_CAP` (80) and `KLOXUS_EMAIL_WS_DAILY_CAP` (25)
  are env-tunable; the ledger in Postgres makes them survive restarts.

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
cd frontend && E2E_BASE_URL=https://kloxus.fly.dev npx playwright test
```

Hosted verification is **outbox-only** under the freeze: it must never
send real email or call a paid model. The safe path is selected by server-side
evidence, not a client flag — `smoke.sh` and every e2e spec preflight
`/api/v1/workspace` and refuse to run a mail-capable step unless the
target itself reports the deterministic demo pipeline
(`mode=demo`) with the outbox mail transport (`mail=outbox`,
`mail_transport=outbox`). A live or unknown transport fails closed
before the first mail-capable request.

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
