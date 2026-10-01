#!/usr/bin/env bash
# One-command local development: API (:8080) + web (:5173). Ctrl+C stops both.
#
# - .env points the metadata store at a remote cluster (e.g. Couchbase Capella):
#     Docker is not needed; only the API and web app start.
#     (Run `make dev-stack` separately if you want the sample PostgreSQL source / local target.)
# - No .env, or a local metadata store:
#     the Docker dev stack (Couchbase 8.0.3 + sample PostgreSQL) is started first.
set -euo pipefail
cd "$(dirname "$0")/.."

meta_connstr=""
if [ -f .env ]; then
  meta_connstr=$(grep -E '^OC_META_CONNSTR=' .env | tail -1 | cut -d= -f2- | tr -d '"'"'" | xargs || true)
fi

is_local() {
  case "$1" in
    "" | couchbase://localhost* | couchbase://127.0.0.1* | couchbase://couchbase*) return 0 ;;
    *) return 1 ;;
  esac
}

if [ -f .env ] && ! is_local "$meta_connstr"; then
  echo "Metadata store: ${meta_connstr%%\?*} (from .env) — skipping the Docker dev stack."
  echo "Tip: 'make dev-stack' starts the optional sample PostgreSQL source and local Couchbase target."
else
  if [ ! -f .env ]; then
    echo "No .env found: using the local Docker dev stack (cp .env.example .env to use Capella)."
    export OC_DEV=true
  fi
  make dev-stack
fi

# `kill 0` signals the whole process group, this shell included, so ignore INT/TERM
# here first; otherwise the TERM re-enters this handler and it loops forever.
cleanup() {
  trap '' INT TERM
  trap - EXIT
  kill 0 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM

(cd backend && CGO_ENABLED=0 go run ./cmd/odb) &
(cd web && npm run dev) &
wait
