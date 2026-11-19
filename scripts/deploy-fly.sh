#!/usr/bin/env bash
# deploy-fly.sh — deploy the current checkout to the real app on Fly and
# print the release version. Deploys are serialized by the routine in
# docs/deploy.md: whatever is deployed here is the release under review.
set -euo pipefail
cd "$(dirname "$0")/.."

FLYCTL="${FLYCTL:-flyctl}"

# fly.toml names the app — a bare `fly deploy` can only hit `kloxus`,
# never kloxus-demo.
"$FLYCTL" deploy --remote-only "$@"

echo "--- releases ---"
"$FLYCTL" releases -a kloxus | head -6
