# Deploying the demo to Fly.io

The hosted demo runs the same `kloxus demo` binary as local mode, switched
into multi-workspace mode by `KLOXUS_DATA_DIR`. One always-on machine, one
volume, zero external services, zero secrets.

## What's already configured

`fly.toml`:

- `KLOXUS_MODE=demo`, `KLOXUS_PORT=8080`
- `KLOXUS_DATA_DIR=/data` — enables hosted mode: each visitor gets a signed
  cookie and their own SQLite file under `/data/workspaces/`, cloned from a
  seeded `template.db` built at boot
- `KLOXUS_OUT_DIR=/data/out`
- `[mounts]` `kloxus_data` → `/data` — workspace data survives deploys and
  restarts
- `auto_stop_machines = "off"`, `min_machines_running = 1` — the demo stays up
- `[http_service.checks]` GET `/healthz` every 30s

`Dockerfile` builds a static binary (`CGO_ENABLED=0`) and runs it as a
non-root user with `/data` owned by that user.

## First deploy

```bash
fly launch --name kloxus-demo --region gru --no-deploy   # or reuse fly.toml
fly volumes create kloxus_data --region gru --size 1
fly deploy
fly status
curl https://kloxus-demo.fly.dev/healthz                 # → ok
```

## Redeploy

```bash
fly deploy
```

The volume is preserved; existing visitor workspaces keep their data. Old
workspaces are reaped by the cleanup loop (idle age cap) and open handles
are LRU-evicted.

## Verify

- `https://kloxus-demo.fly.dev/healthz` → `ok`
- Open the site in two browsers (or normal + private window): each gets a
  different `kloxus_ws` cookie and its own publication state; "Reset demo"
  affects only that browser.
- `fly logs` shows request ids, not cookies or URLs with user data.

## Ops notes

- The cookie-signing secret is generated at `/data/cookie.secret` on first
  boot (or set `KLOXUS_COOKIE_SECRET`). Losing it just invalidates existing
  session cookies — visitors get fresh workspaces.
- SQLite runs in WAL mode; runs and resets are serialized per workspace.
- The demo never fetches visitor-entered URLs and never calls external
  APIs; egress can be left unrestricted but nothing needs it.
