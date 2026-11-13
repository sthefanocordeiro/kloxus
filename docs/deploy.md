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
Supabase, `KLOXUS_MASTER_KEY`, `KLOXUS_WEBHOOK_SECRET`,
`KLOXUS_PUBLIC_URL`) are set on the app with `fly secrets set` — never in
the file, the repo or a PR.

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

Workspace data lives on the `kloxus_data` volume, so a rollback does not
lose visitor workspaces.

## Ops notes

- `fly logs -a kloxus` shows request ids, not cookies or user data.
- The cookie-signing secret is generated at `/data/cookie.secret` on first
  boot (or set `KLOXUS_COOKIE_SECRET`). Losing it only invalidates session
  cookies — visitors get fresh workspaces.
- Free allowances: two small `shared-cpu-1x` 256 MB machines and two 1 GB
  volumes across both apps stay inside Fly's free usage for a personal org.
