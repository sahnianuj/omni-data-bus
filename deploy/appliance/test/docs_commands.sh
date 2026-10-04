#!/usr/bin/env bash
# shellcheck disable=SC2015 # pass/fail reporters always succeed, so A && ok || bad is safe
# Runs the READMEs' commands as written (OC-074 AC1, AC3, AC4), so the docs can't drift from the image.
#
#   deploy/appliance/test/docs_commands.sh [dist/appliance]
#
# 1. Customer guide: every ```bash block after a <!-- docs-test --> marker in
#    deploy/appliance/README.md runs, in order, in a copy of the shared folder (make appliance-save).
# 2. Then the data round trip from the guide: sign up, back up, delete everything, restore, upgrade;
#    the account survives each step.
# 3. Main README: its marked blocks must name real make targets (make -n; the builds take too long).
#
# Uses its own container name, port and volume, removed at the end.
set -euo pipefail
cd "$(dirname "$0")/../../.."

DIST=${1:-dist/appliance}
[ -f "$DIST/run.sh" ] || {
  echo "no $DIST/run.sh: run make appliance-save first" >&2
  exit 1
}
WORK=$(mktemp -d)
export ODB_NAME=odb-docs-test-$$ ODB_VOLUME=odb-docs-test-$$-data ODB_PORT=${DOCS_PORT:-18090}
export ODB_OUTPUT_DIR=$WORK/output # not the real ~/omni-data-bus-output
API=http://localhost:$ODB_PORT/api/v1
FAILS=0
cleanup() {
  docker rm -f "$ODB_NAME" >/dev/null 2>&1 || true
  docker volume rm "$ODB_VOLUME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

ok() { printf '  ✓ %s\n' "$*"; }
bad() {
  printf '  ✗ %s\n' "$*" >&2
  FAILS=$((FAILS + 1))
}

# blocks FILE: the marked bash blocks of FILE, one command per line.
blocks() {
  awk '/<!-- docs-test -->/ {mark=1; next} mark && /^```bash/ {inb=1; next} inb && /^```/ {inb=0; mark=0; next} inb {print}' "$1"
}

echo "Customer guide (deploy/appliance/README.md), in a copy of $DIST"
# Hard links: the archives are large.
cp -l "$DIST"/* "$WORK"/ 2>/dev/null || cp "$DIST"/* "$WORK"/
while IFS= read -r line; do
  cmd=${line%%#*}
  cmd=${cmd%"${cmd##*[![:space:]]}"}
  [ -n "$cmd" ] || continue
  if (cd "$WORK" && bash -c "$cmd") >"$WORK/out.txt" 2>&1; then ok "$cmd"; else
    bad "$cmd"
    tail -20 "$WORK/out.txt" >&2
  fi
done < <(blocks deploy/appliance/README.md)

echo "Data round trip (sections 8–10)"
(cd "$WORK" && ./run.sh start >/dev/null) && ok "./run.sh start (again)" || bad "./run.sh start"
signup=$(curl -fsS -H 'Content-Type: application/json' -X POST "$API/auth/signup" \
  -d '{"email":"docs@example.com","password":"D0cs-test-pass!","displayName":"Docs"}' -o /dev/null -w '%{http_code}' || true)
[ "$signup" = 201 ] && ok "sign up" || bad "sign up ($signup)"
login() { curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -X POST "$API/auth/login" -d '{"email":"docs@example.com","password":"D0cs-test-pass!"}'; }

(cd "$WORK" && ./run.sh backup "$WORK" >"$WORK/out.txt" 2>&1) && ok "./run.sh backup" || bad "./run.sh backup: $(tail -3 "$WORK/out.txt")"
backup=""
for f in "$WORK"/odb-backup-*.tar.gz; do [ -f "$f" ] && backup=$f; done
[ -n "$backup" ] && ok "backup file written ($(du -h "$backup" | cut -f1))" || bad "no backup file"
[ "$(login)" = 200 ] && ok "running again after backup; account there" || bad "login after backup"

(cd "$WORK" && printf 'DELETE\n' | ./run.sh remove --delete-data >/dev/null) && ok "./run.sh remove --delete-data (typed DELETE)" || bad "remove --delete-data"
docker volume inspect "$ODB_VOLUME" >/dev/null 2>&1 && bad "volume still there" || ok "volume deleted"

docker volume create "$ODB_VOLUME" >/dev/null
(cd "$WORK" && ./run.sh restore "$backup" --yes >/dev/null) && ok "./run.sh restore … --yes" || bad "restore"
(cd "$WORK" && ./run.sh start >/dev/null) && ok "start on the restored data" || bad "start after restore"
[ "$(login)" = 200 ] && ok "account restored" || bad "login after restore"

# The archive for Docker's CPU, as run.sh load picks it.
case "$(docker info --format '{{.Architecture}}')" in x86_64 | amd64) arch=amd64 ;; *) arch=arm64 ;; esac
archive=""
for f in "$WORK"/omni-data-bus-appliance-*-"$arch".tar.gz; do archive=$f; done
echo "  (upgrading from $(basename "$archive"))"
(cd "$WORK" && ./run.sh upgrade "$archive" >/dev/null) && ok "./run.sh upgrade <archive>" || bad "upgrade"
[ "$(login)" = 200 ] && ok "account kept across the upgrade" || bad "login after upgrade"
(cd "$WORK" && ./run.sh stop >/dev/null) && ok "./run.sh stop" || bad "stop"

echo "Main README: the marked build commands name real make targets"
while IFS= read -r line; do
  cmd=${line%%#*}
  cmd=${cmd%"${cmd##*[![:space:]]}"}
  [ -n "$cmd" ] || continue
  # shellcheck disable=SC2086 # the command's words, as written
  if make -n ${cmd#make } >/dev/null 2>&1; then ok "$cmd (make -n)"; else bad "$cmd"; fi
done < <(blocks README.md)

echo
[ "$FAILS" = 0 ] && echo "All checks passed." || {
  echo "FAILED: $FAILS"
  exit 1
}
