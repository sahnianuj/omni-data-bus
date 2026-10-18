#!/usr/bin/env bash
# Keeps dist/odb current for the dev server (OC-188), so its export dialog can add odb for every
# machine (Linux, Windows, macOS), not just the laptop's own. scripts/dev.sh runs it in the
# background: the server reads dist/odb on each request, so machines appear once it's built.
#
# Rebuilds only when the version changed or a backend source is newer than the last build.
#
# Usage: scripts/dev-dist.sh <version> [ldflags]
set -euo pipefail
cd "$(dirname "$0")/.."
version=$1
ldflags=${2:-}
stamp=dist/odb/.stamp

fresh() {
  [ -f "$stamp" ] && [ "$(cat "$stamp")" = "$version" ] &&
    [ -z "$(find backend -type f \( -name '*.go' -o -name go.mod -o -name go.sum \) -newer "$stamp" -print -quit)" ]
}

if fresh; then
  echo "dist/odb is current ($version): every machine's odb is available to exports"
  exit 0
fi
echo "building odb for every machine into dist/odb ($version), in the background…"
if VERSION=$version LDFLAGS="-s -w $ldflags" nice -n 10 scripts/dist.sh >dist-build.log 2>&1; then
  echo "$version" >"$stamp"
  rm -f dist-build.log
  echo "dist/odb is ready: exports can add odb for every machine"
else
  echo "building dist/odb failed (see dist-build.log): exports offer only this machine's odb" >&2
fi
