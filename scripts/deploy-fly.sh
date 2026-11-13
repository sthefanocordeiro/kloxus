#!/usr/bin/env bash
# deploy-fly.sh — deploy the current checkout to the real app on Fly.
#
# Deploys and hosted verification on the single `kloxus` app are serialized
# by a git-ref deploy lease (KLOX-25, D38): `refs/deploy-locks/kloxus` on origin is a
# compare-and-swap lock. Nothing reaches `flyctl deploy` without holding it.
#
# Usage:
#   scripts/deploy-fly.sh [deploy] [flyctl args...]  acquire → deploy → release
#   scripts/deploy-fly.sh acquire                    take the lease for a whole
#                                                    verify session (deploy + smoke)
#   scripts/deploy-fly.sh release                    give the lease back
#   scripts/deploy-fly.sh status                     show the current holder
#
# Env:
#   KLOXUS_DEPLOY_LEASE_TTL    seconds before a held lease is stale (default 2700)
#   KLOXUS_DEPLOY_LEASE_OWNER  holder identity (default: MULTICA_TASK_ID, else
#                              USER@hostname); set it so `status` is meaningful
#   FLYCTL                     flyctl binary (default: flyctl)
set -euo pipefail
cd "$(dirname "$0")/.."

FLYCTL="${FLYCTL:-flyctl}"
REMOTE="${KLOXUS_DEPLOY_LEASE_REMOTE:-origin}"
LEASE_REF="refs/deploy-locks/kloxus"
TTL="${KLOXUS_DEPLOY_LEASE_TTL:-2700}"
OWNER="${KLOXUS_DEPLOY_LEASE_OWNER:-${MULTICA_TASK_ID:-${USER:-$(id -un 2>/dev/null || echo agent)}@$(hostname 2>/dev/null || echo local)}}"

log() { printf 'deploy-lease: %s\n' "$*" >&2; }

lease_commit() { # $1=state(active|released) → prints new commit sha
    local msg
    msg=$(printf 'kloxus-deploy-lease\nstate: %s\nowner: %s\nacquired_at: %s\nttl_seconds: %s\n' \
        "$1" "$OWNER" "$(date -u +%s)" "$TTL")
    git commit-tree "$(git mktree </dev/null)" -m "$msg"
}

fetch_lease() { # → prints remote lease sha or empty
    git fetch -q "$REMOTE" "+$LEASE_REF:$LEASE_REF" 2>/dev/null || true
    git rev-parse --verify -q "$LEASE_REF" 2>/dev/null || true
}

lease_field() { # $1=sha $2=field
    git log -1 --format=%B "$1" | sed -n "s/^$2: //p"
}

# Acquire the lease. Prints the held lease sha on stdout. Exits 1 with the
# holder's identity when a fresh lease belongs to someone else.
acquire() {
    local cur state owner acquired age new
    cur=$(fetch_lease)
    new=$(lease_commit active)

    if [ -z "$cur" ]; then
        if git push -q "$REMOTE" "$new:$LEASE_REF" 2>/dev/null; then
            git update-ref "$LEASE_REF" "$new"
            log "acquired (owner: $OWNER, ttl: ${TTL}s)"
            echo "$new"; return 0
        fi
        cur=$(fetch_lease) # someone beat us to it — fall through to inspect
    fi

    state=$(lease_field "$cur" state)
    owner=$(lease_field "$cur" owner)
    acquired=$(lease_field "$cur" acquired_at)
    age=$(( $(date -u +%s) - ${acquired:-0} ))

    if [ "$state" = "active" ] && [ "$age" -lt "$TTL" ] && [ "$owner" != "$OWNER" ]; then
        log "HELD by ${owner:-unknown} (${age}s ago, ttl ${TTL}s). Try again later or inspect with: $0 status"
        return 1
    fi

    # Ref is free (released tombstone or stale lease) — or we already hold it.
    # Compare-and-swap: the push only lands if the ref still equals $cur.
    if git push -q --force-with-lease="$LEASE_REF:$cur" "$REMOTE" "$new:$LEASE_REF" 2>/dev/null; then
        git update-ref "$LEASE_REF" "$new"
        [ "$owner" = "$OWNER" ] && log "renewed (owner: $OWNER)" || log "acquired over ${state:-stale} lease from ${owner:-unknown} (owner: $OWNER)"
        echo "$new"; return 0
    fi
    log "lost a race for the lease — retry"
    return 1
}

release() { # $1=expected lease sha (optional; defaults to fetched ref)
    local cur new
    cur=$(fetch_lease)
    if [ -z "$cur" ]; then log "no lease on $REMOTE"; return 0; fi
    if [ -n "${1:-}" ] && [ "$cur" != "$1" ]; then
        log "ref moved since our lease ($cur != $1) — leaving it for $(lease_field "$cur" owner)"
        return 0
    fi
    new=$(lease_commit released)
    if git push -q --force-with-lease="$LEASE_REF:$cur" "$REMOTE" "$new:$LEASE_REF" 2>/dev/null; then
        git update-ref "$LEASE_REF" "$new"
        log "released"
    else
        log "release raced with another acquirer — new holder owns it now"
    fi
}

status() {
    local cur
    cur=$(fetch_lease)
    if [ -z "$cur" ]; then echo "no lease — app is free"; return 0; fi
    git log -1 --format='lease %h%n%B' "$cur"
}

cmd="${1:-deploy}"
case "$cmd" in
    acquire)  acquire >/dev/null ;;
    release)  release ;;
    status)   status ;;
    *)
    # `deploy` or bare flyctl flags — acquire → deploy → release.
    [ "$cmd" = "deploy" ] && shift
    prev=$(fetch_lease)
    held=$(acquire)
    # Only auto-release a lease this call took: inside an `acquire`d verify
    # session the deploy renews our own lease and must keep it held.
    { [ "$(lease_field "$prev" owner)" != "$OWNER" ] ||
      [ "$(lease_field "$prev" state)" != "active" ]; } &&
      trap 'release "$held"' EXIT
    # fly.toml names the app — a bare `fly deploy` can only hit `kloxus`,
    # never kloxus-demo.
    "$FLYCTL" deploy --remote-only "$@"
    echo "--- releases ---"
    "$FLYCTL" releases -a kloxus | head -6
    ;;
esac
