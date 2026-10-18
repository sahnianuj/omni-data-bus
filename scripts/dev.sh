#!/usr/bin/env bash
# One-command local development: API (:8080) + web (:5173). Ctrl+C stops both.
#
# - .env points the metadata store at a remote cluster (e.g. Couchbase Capella):
#     Docker is not needed; only the API and web app start.
#     (Run `make dev-stack` separately if you want the sample PostgreSQL source / local target.)
# - No .env, or a local metadata store:
#     the Docker dev stack (Couchbase 8.0.3 + sample PostgreSQL) is started first.
# - FORCE=1 (`make dev FORCE=1` or `make dev-force`): source schemas saved before this start
#     are rescanned when next opened, so row counts and new tables show without Rediscover.
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

if [ "${FORCE:-}" = 1 ]; then
  echo "FORCE=1: saved source schemas are rescanned when next opened."
  export OC_REDISCOVER_ON_START=true
fi
trap 'exit 130' INT TERM

# Stamp the release and build as `make build` does (OC-172): the footer then shows
# "2.5.0-dev · build 7f6cea2", not "dev · build none". `make dev` passes them; run directly,
# the script works them out the same way.
if [ -z "${ODB_LDFLAGS:-}" ]; then
  release=$(cat VERSION 2>/dev/null || echo 0.0.0)
  version="$release-dev"
  [ "$(git describe --tags --exact-match 2>/dev/null)" = "v$release" ] && version="$release"
  commit=$(git rev-parse --short HEAD 2>/dev/null || echo none)
  dirty=false
  [ -n "$(git status --porcelain 2>/dev/null)" ] && dirty=true
  bi=omnidatabus/internal/buildinfo
  ODB_LDFLAGS="-X $bi.version=$version -X $bi.commit=$commit -X $bi.buildID=$commit -X $bi.dirty=$dirty -X $bi.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  export VITE_ODB_VERSION="${VITE_ODB_VERSION:-$version}"
fi

# Every machine's odb for exports (OC-188): built in the background into dist/odb when it's
# missing or older than the sources; the server picks it up as soon as it's there.
if [ -z "${OC_EXPORT_DIST_DIR:-}" ]; then
  export OC_EXPORT_DIST_DIR="$PWD/dist/odb"
  scripts/dev-dist.sh "${VITE_ODB_VERSION:-$(cat VERSION 2>/dev/null || echo 0.0.0)-dev}" "$ODB_LDFLAGS" &
fi

(cd backend && CGO_ENABLED=0 go run -ldflags "$ODB_LDFLAGS" ./cmd/odb) &
(cd web && npm run dev) &
wait
