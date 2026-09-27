#!/usr/bin/env bash
# Runs the Playwright end-to-end suite (OC-034) against the built image.
#   ./scripts/e2e.sh                 # all specs, Chromium
#   ./scripts/e2e.sh golden-path     # specs matching a pattern
#   E2E_BROWSERS=all ./scripts/e2e.sh   # + Firefox and WebKit (nightly)
#   E2E_KEEP=1 ./scripts/e2e.sh      # leave the stack running afterwards
#   E2E_SKIP_BUILD=1 ./scripts/e2e.sh   # reuse omni-data-bus:e2e
set -euo pipefail
cd "$(dirname "$0")/.."
COMPOSE="docker compose -f deploy/compose/e2e.yml"

if [ -z "${E2E_SKIP_BUILD:-}" ]; then
  make image IMAGE=omni-data-bus:e2e
fi
make dev-stack
# A clean metadata scope for every run (the dev app's own "meta" scope is untouched).
curl -fsS -u Administrator:password -X DELETE http://localhost:8091/pools/default/buckets/omniconnect/scopes/e2e >/dev/null 2>&1 || true
$COMPOSE up -d --wait --force-recreate

cleanup() {
  if [ -z "${E2E_KEEP:-}" ]; then
    $COMPOSE down >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
cd web && E2E_BASE_URL=http://localhost:18081 npx playwright test "$@"
