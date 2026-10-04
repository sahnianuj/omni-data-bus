#!/usr/bin/env bash
# Smoke test of the all-in-one image (OC-072 AC1–AC6): a real start, sign-up, bundled target,
# clean stop, restart on the same data, the memory floor, secret hygiene and crash handling.
#
#   IMAGE=omni-data-bus-appliance:<version> deploy/appliance/test/smoke.sh     (make appliance-test)
#
# Needs Docker with ≥ 4 GiB, curl. Uses its own container, port and volume, removed at the end.
set -euo pipefail

IMAGE=${IMAGE:?set IMAGE=omni-data-bus-appliance:<version>}
NAME=odb-appliance-smoke-$$
VOL=$NAME-data
PORT=${SMOKE_PORT:-18089}
API=http://localhost:$PORT/api/v1
JAR=$(mktemp)
OUT=$(cd "$(mktemp -d)" && pwd -P) # the "computer" folder shared as /data (OC-080)
FAILS=0

pass() { printf '  ✓ %s\n' "$*"; }
fail() {
  printf '  ✗ %s\n' "$*" >&2
  FAILS=$((FAILS + 1))
}
check() { # check DESCRIPTION COMMAND...
  local what=$1
  shift
  if "$@"; then pass "$what"; else fail "$what"; fi
}
cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker volume rm "$VOL" >/dev/null 2>&1 || true
  rm -f "$JAR"
  rm -rf "$OUT"
}
trap cleanup EXIT

run() { # run [MOUNT_OPTS]: the shared folder, read-write unless e.g. ":ro"
  docker run -d --name "$NAME" -p "$PORT:8080" -v "$VOL:/opt/couchbase/var" -v "$OUT:/data${1:-}" -e "ODB_OUTPUT_HOST_DIR=$OUT" \
    --memory 4g --stop-timeout 90 "$IMAGE" >/dev/null
}
health() { docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' "$NAME" 2>/dev/null; }
wait_healthy() { # wait_healthy TIMEOUT → seconds taken, or fails
  local start=$SECONDS h
  while [ $((SECONDS - start)) -lt "$1" ]; do
    h=$(health)
    [ "$h" = "running healthy" ] && echo $((SECONDS - start)) && return 0
    case "$h" in exited*) return 1 ;; esac
    sleep 3
  done
  return 1
}
logs() { docker logs "$NAME" 2>&1; }
json_field() { sed -n "s/.*\"$1\":\"\\([^\"]*\\)\".*/\\1/p"; }

echo "AC1 — first start on an empty volume"
run
if secs=$(wait_healthy 240); then pass "healthy after ${secs}s (limit 240s)"; else
  fail "not healthy within 240s"
  logs | tail -30 >&2
  exit 1
fi
check "logs say where to open the app" grep -q 'appliance: ready: open http://localhost:8080' <(logs)
csrf=$(curl -fsS -c "$JAR" -b "$JAR" -H 'Content-Type: application/json' -X POST "$API/auth/signup" \
  -d '{"email":"smoke@example.com","password":"Sm0ke-test-pass!","displayName":"Smoke"}' | json_field csrfToken)
check "sign-up works over plain http" test -n "$csrf"
check "session cookie isn't Secure-only (plain http keeps it)" bash -c "! grep -q TRUE <(awk '\$4==\"TRUE\"' '$JAR')"
check "version names the bundled Couchbase" grep -q '"bundledStore":"Couchbase Server CE [0-9.]*"' <(curl -fsS -b "$JAR" "$API/version")

echo "AC6 — the bundled node as a target"
check "/target-kinds offers it" grep -q '"bundled":{"kind":"couchbase"' <(curl -fsS -b "$JAR" "$API/target-kinds")
project=$(curl -fsS -b "$JAR" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -X POST "$API/projects" -d '{"name":"Smoke"}' | json_field id)
saved=$(curl -fsS -b "$JAR" -H "X-CSRF-Token: $csrf" -X POST "$API/projects/$project/connections/target/bundled")
check "saved and tested in one call" grep -q '"lastTestStatus":"ok"' <<<"$saved"
check "Couchbase reports Community Edition" grep -q 'community' <<<"$saved"

echo "OC-080 — directory targets write to the shared folder"
check "logs name the output folder" grep -qF "JSON output folder on your computer: $OUT" <(logs)
check "/target-kinds names the shared folder" grep -qF "\"sharedFolder\":{\"path\":\"/data\",\"hostPath\":\"$OUT\"}" <(curl -fsS -b "$JAR" "$API/target-kinds")
areas=$(curl -fsS -b "$JAR" "$API/target-kinds/directory/folders")
check "the picker's only area is the computer's folder" grep -qF "\"label\":\"Your computer: $OUT\"" <<<"$areas"
check "  … not the container's home" bash -c "! grep -q 'odb/home' <<<'$areas'"
curl -fsS -b "$JAR" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -X POST "$API/target-kinds/directory/folders" \
  -d '{"parent":"/data","name":"smoke-orders"}' >/dev/null
check "a folder made in the picker appears on the computer" test -d "$OUT/smoke-orders"
tested=$(curl -fsS -b "$JAR" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -X POST "$API/connections/test" \
  -d '{"role":"target","type":"directory","config":{"path":"/data/smoke-orders"}}')
check "Test connection passes and shows the computer path" grep -qF "on your computer: $OUT/smoke-orders" <<<"$tested"
check "  … and leaves nothing behind" test -z "$(ls -A "$OUT/smoke-orders")"

echo "AC5 — secrets"
pw=$(docker exec "$NAME" cat /opt/couchbase/var/odb/secrets/cb_admin_password)
key=$(docker exec "$NAME" cat /opt/couchbase/var/odb/secrets/master_key)
check "generated Couchbase password is long" test "${#pw}" -ge 32
check "secret files are 0600, owned by couchbase" test "$(docker exec "$NAME" stat -c '%a %U' /opt/couchbase/var/odb/secrets/cb_admin_password /opt/couchbase/var/odb/secrets/master_key | sort -u)" = "600 couchbase"
check "no secret in logs, inspect or API answers" bash -c "! grep -qF -e '$pw' -e '$key' <(docker logs '$NAME' 2>&1; docker inspect '$NAME'; printf '%s' '$saved')"
check "the app runs as couchbase, not root" test "$(docker exec "$NAME" sh -c 'ps -o user= -p $(pidof odb)')" = couchbase
check "show-credentials prints the password" grep -qF "$pw" <(docker exec "$NAME" odb-appliance show-credentials)

echo "AC3 — clean stop"
start=$SECONDS
docker stop --time 90 "$NAME" >/dev/null
check "stopped within 90s ($((SECONDS - start))s)" test $((SECONDS - start)) -lt 90
check "exit code 0" test "$(docker inspect -f '{{.State.ExitCode}}' "$NAME")" = 0
order=$(logs | grep -E '^appliance: stopping (Omni-Data-Bus|Couchbase Server)' | sed 's/ (.*//' | tr '\n' '|')
check "Omni-Data-Bus stops before Couchbase ($order)" test "$order" = "appliance: stopping Omni-Data-Bus|appliance: stopping Couchbase Server|"
check "no error lines" bash -c "! grep -q '^appliance: error' <(docker logs '$NAME' 2>&1)"

echo "AC2 — restart and recreate keep data"
docker start "$NAME" >/dev/null
if secs=$(wait_healthy 180); then pass "restart healthy after ${secs}s"; else fail "restart not healthy"; fi
check "first-start setup ran once" test "$(logs | grep -c 'first start: initializing')" = 1
docker rm -f "$NAME" >/dev/null
run :ro # OC-080 AC4: the shared folder read-only this time
if secs=$(wait_healthy 180); then pass "recreated on the same volume, healthy after ${secs}s"; else fail "recreate not healthy"; fi
check "OC-080 AC4: an unwritable shared folder is reported with the fix" grep -q "isn't writable by the app (uid 1000).*setfacl" <(logs)
check "not initialized again" bash -c "! grep -q 'first start' <(docker logs '$NAME' 2>&1)"
check "the user can still sign in" test "$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -X POST "$API/auth/login" \
  -d '{"email":"smoke@example.com","password":"Sm0ke-test-pass!"}')" = 200

echo "AC4 — failures are clear"
docker exec "$NAME" sh -c 'kill -9 $(pidof odb)'
sleep 10
check "an odb crash stops the container with exit 1" test "$(docker inspect -f '{{.State.Status}} {{.State.ExitCode}}' "$NAME")" = "exited 1"
check "and says so" grep -q 'Omni-Data-Bus exited unexpectedly; stopping Couchbase Server' <(logs)
set +e
low=$(docker run --rm --memory 2g "$IMAGE" 2>&1)
code=$?
set -e
check "2 GiB of memory is refused with exit 78" test "$code" = 78
check "with a message saying what to do" grep -q 'needs at least 3 GiB' <<<"$low"

echo
if [ "$FAILS" -gt 0 ]; then
  echo "FAILED: $FAILS check(s)"
  exit 1
fi
echo "All checks passed."
