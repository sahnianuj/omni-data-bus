#!/usr/bin/env bash
# shellcheck disable=SC2015 # pass/fail reporters always succeed, so A && ok || bad is safe
# Tests of run.sh's safety checks with a fake `docker` (OC-074 AC2). No Docker needed; runs under
# any bash ≥ 3.2:  deploy/appliance/test/run_sh_test.sh   (or: docker run --rm -v "$PWD:/w" bash:3.2 bash /w/deploy/appliance/test/run_sh_test.sh)
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
RUN=$HERE/../run.sh
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
FAILS=0

# A fake docker: behavior from FAKE_* variables, every call appended to $TMP/calls.
mkdir -p "$TMP/bin"
cat >"$TMP/bin/docker" <<'EOF'
#!/usr/bin/env bash
echo "docker $*" >>"$FAKE_LOG"
case "$1" in
  info)
    [ "${FAKE_DOWN:-}" = 1 ] && exit 1
    case "$*" in
      *MemTotal*) echo "${FAKE_MEM:-8000000000}" ;;
      *Architecture*) echo aarch64 ;;
    esac ;;
  ps)
    case "$*" in
      *-aq*) [ "${FAKE_EXISTS:-}" = 1 ] && echo abc123 ;;
      *-q*) [ "${FAKE_RUNNING:-}" = 1 ] && echo abc123 ;;
    esac ;;
  image) [ "${FAKE_IMAGE:-1}" = 1 ] || exit 1 ;;
  inspect)
    case "$*" in
      *Mounts*) echo "${FAKE_MOUNT-/Users/ana/omni-data-bus-output}" ;;
      *) echo omni-data-bus-appliance:dev ;;
    esac ;;
  run) case "$*" in *--user\ 1000*) [ "${FAKE_UNWRITABLE:-}" = 1 ] && exit 1 ;; esac ;;
esac
exit 0
EOF
chmod +x "$TMP/bin/docker"
# A fake Linux (uname, id) for the permission check, used only where FAKE_LINUX=1.
# shellcheck disable=SC2016 # the fakes' own $variables, expanded when they run
printf '#!/bin/sh\n[ "$FAKE_LINUX" = 1 ] && echo Linux || /usr/bin/uname "$@"\n' >"$TMP/bin/uname"
# shellcheck disable=SC2016
printf '#!/bin/sh\nif [ "$FAKE_LINUX" = 1 ] && [ "$1" = -u ]; then echo 1001; else /usr/bin/id "$@"; fi\n' >"$TMP/bin/id"
chmod +x "$TMP/bin/uname" "$TMP/bin/id"

# t NAME EXPECT_EXIT EXPECT_TEXT [ENV=…]… -- ARGS…: runs run.sh, checks exit code and output.
t() {
  local name=$1 want_code=$2 want_text=$3
  shift 3
  local envs=()
  while [ "$1" != -- ]; do envs+=("$1"); shift; done
  shift
  : >"$TMP/calls"
  local out code
  out=$(env ODB_PORT=18979 HOME="$TMP/home" PATH="$TMP/bin:$(dirname "$BASH"):/usr/bin:/bin" FAKE_LOG="$TMP/calls" ${envs[@]+"${envs[@]}"} "$BASH" "$RUN" "$@" 2>&1 </dev/null)
  code=$?
  if [ "$code" = "$want_code" ] && [[ $out == *"$want_text"* ]]; then
    printf '  ✓ %s\n' "$name"
  else
    printf '  ✗ %s (exit %s, want %s)\n%s\n' "$name" "$code" "$want_code" "$out" >&2
    FAILS=$((FAILS + 1))
  fi
}
no_call() { # no_call NAME PATTERN: the last run never called docker with PATTERN
  if grep -q "$2" "$TMP/calls"; then
    printf '  ✗ %s\n' "$1" >&2
    FAILS=$((FAILS + 1))
  else printf '  ✓ %s\n' "$1"; fi
}

echo "prechecks"
mkdir -p "$TMP/nodocker"
out=$(env PATH="$TMP/nodocker" "$BASH" "$RUN" start 2>&1)
if [ $? = 1 ] && [[ $out == *"Docker isn't installed"* ]]; then printf '  ✓ %s\n' "no Docker: says how to install it"; else
  printf '  ✗ no Docker (%s)\n' "$out" >&2
  FAILS=$((FAILS + 1))
fi
t "Docker not running: says to start it" 1 "Docker isn't running" FAKE_DOWN=1 -- start
t "image not loaded: says to load it" 1 "run ./run.sh load first" FAKE_IMAGE=0 -- start
t "2 GiB of memory: refused with where to change it" 1 "needs at least 3 GiB" FAKE_MEM=2147483648 -- start
no_call "  … and nothing was started" "docker run"
# A port in use: a real listener (python3), since run.sh probes it through bash's /dev/tcp.
if command -v python3 >/dev/null 2>&1; then
  python3 -c 'import socket,time; s=socket.socket(); s.bind(("127.0.0.1",18977)); s.listen(); time.sleep(10)' &
  lpid=$!
  sleep 1
  t "port in use: suggests another" 1 "port 18977 is in use" ODB_PORT=18977 -- start
  kill "$lpid" 2>/dev/null
fi
t "already running: says so, starts nothing" 0 "Already running" FAKE_EXISTS=1 FAKE_RUNNING=1 -- start
no_call "  … no docker run" "docker run"
# (The fake container never reports running, so start then fails waiting: only the docker run matters.)
t "OC_* settings are passed into a new container" 1 "Starting omni-data-bus" FAKE_IMAGE=1 OC_SIGNUP_ENABLED=false -- start
grep -q -- "-e OC_SIGNUP_ENABLED" "$TMP/calls" && printf '  ✓ %s\n' "  … as -e OC_SIGNUP_ENABLED" || {
  printf '  ✗ OC_SIGNUP_ENABLED not passed\n' >&2
  FAILS=$((FAILS + 1))
}

echo "output folder (OC-080)"
mkdir -p "$TMP/home"
called() { # called NAME PATTERN: the last run called docker with PATTERN
  if grep -q -- "$2" "$TMP/calls"; then printf '  ✓ %s\n' "$1"; else
    printf '  ✗ %s (no "%s" in: %s)\n' "$1" "$2" "$(cat "$TMP/calls")" >&2
    FAILS=$((FAILS + 1))
  fi
}
t "new container: shares --output as /data" 1 "Starting omni-data-bus" -- start --output "$TMP/out"
out_abs=$(cd "$TMP/out" && pwd -P)
called "  … -v <folder>:/data" "-v $out_abs:/data"
called "  … and tells the app its path" "-e ODB_OUTPUT_HOST_DIR=$out_abs"
t "default folder is ~/omni-data-bus-output, created" 1 "Starting" -- start
called "  … mounted" "omni-data-bus-output:/data"
t "another folder recreates the container" 1 "recreating the container (data is kept)" FAKE_EXISTS=1 -- start --output "$TMP/out"
called "  … removes the old one" "docker rm omni-data-bus"
t "a container from before OC-080 (no /data) is recreated" 1 "Output folder changed" FAKE_EXISTS=1 FAKE_MOUNT= -- start
t "same folder: no recreate" 0 "Already running" FAKE_EXISTS=1 FAKE_RUNNING=1 FAKE_MOUNT="$out_abs" -- start --output "$TMP/out"
no_call "  … no docker rm" "docker rm"
t "Linux, folder not writable by uid 1000: exact fix" 1 "sudo setfacl -m u:1000:rwx" FAKE_LINUX=1 FAKE_UNWRITABLE=1 -- start --output "$TMP/out"
no_call "  … nothing started" "docker run -d"
t "Linux: host.docker.internal is added" 1 "Starting" FAKE_LINUX=1 -- start --output "$TMP/out"
called "  … --add-host host.docker.internal:host-gateway" "--add-host host.docker.internal:host-gateway"
t "macOS: not added (Docker Desktop has it)" 1 "Starting" -- start --output "$TMP/out"
no_call "  … no --add-host" "--add-host"
t "status shows the output folder" 0 "JSON output folder: /Users/ana/omni-data-bus-output" FAKE_EXISTS=1 -- status

echo "data safety"
t "remove keeps the volume" 0 "Data is kept" FAKE_EXISTS=1 -- remove
no_call "  … no docker volume rm" "volume rm"
t "remove --delete-data needs DELETE typed" 1 "not confirmed" FAKE_EXISTS=1 -- remove --delete-data
no_call "  … no docker volume rm" "volume rm"
touch "$TMP/backup.tar.gz"
t "restore while running is refused" 1 "stop the app first" FAKE_EXISTS=1 FAKE_RUNNING=1 -- restore "$TMP/backup.tar.gz" --yes
t "restore without --yes is refused" 1 "add --yes" FAKE_EXISTS=1 -- restore "$TMP/backup.tar.gz"
no_call "  … nothing deleted" "find /data"
t "restore of a missing file is refused" 1 "usage" -- restore "$TMP/nope.tar.gz" --yes
t "upgrade without a file explains" 1 "usage: ./run.sh upgrade" -- upgrade
t "unknown command shows help" 2 "Usage: ./run.sh" -- frobnicate

echo
[ "$FAILS" = 0 ] && echo "All checks passed." || {
  echo "FAILED: $FAILS"
  exit 1
}
