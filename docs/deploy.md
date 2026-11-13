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

A PR is not done until both pass against the Fly URL, and its description
carries the URL, the release version and both outputs.

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
