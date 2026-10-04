#!/usr/bin/env bash
# Runs the Omni-Data-Bus all-in-one image (app + Couchbase Server CE) on one machine (OC-074).
# Works with bash 3.2 (macOS) and later; needs only Docker. Run ./run.sh help for the commands.
set -euo pipefail

ODB_APPLIANCE_VERSION=dev # set by `make appliance-save`
IMAGE=${ODB_IMAGE:-omni-data-bus-appliance:$ODB_APPLIANCE_VERSION}
NAME=${ODB_NAME:-omni-data-bus}
VOLUME=${ODB_VOLUME:-odb-data}
PORT=${ODB_PORT:-8080}
CONSOLE_PORT=${ODB_CONSOLE_PORT:-8091} # Couchbase web console, this machine only; skipped when taken
# Folder of this computer the app writes JSON files to (directory targets), shared as /data (OC-080).
OUTPUT_DIR=${ODB_OUTPUT_DIR:-}
MEMORY=${ODB_MEMORY:-4g}
START_TIMEOUT=${ODB_START_TIMEOUT:-300}
HERE=$(cd "$(dirname "$0")" && pwd)

say() { printf '%s\n' "$*"; }
die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<EOF
Usage: ./run.sh <command> [options]

  load [file]                 check the checksum, then load the image (default: the archive here)
  start [--port 8080] [--memory 4g] [--output DIR]
                              start the app; first start takes 1-3 minutes. JSON files from
                              directory targets go to DIR (default ~/omni-data-bus-output)
  stop                        stop it (data is kept)
  status                      state, health and versions
  logs [-f]                   the container's logs
  upgrade <file>              load a newer archive and restart on the same data
  backup [dir]                stop, archive all data to dir (default: here), start again
  restore <file> --yes        replace all data with a backup (the app must be stopped)
  remove [--delete-data]      remove the container; its data only with --delete-data
  credentials                 the bundled Couchbase's admin password (for support)

Settings (environment): ODB_PORT=$PORT ODB_MEMORY=$MEMORY ODB_NAME=$NAME ODB_VOLUME=$VOLUME
  ODB_CONSOLE_PORT=$CONSOLE_PORT (Couchbase console, this machine only; skipped if in use)
  ODB_PUBLIC_URL   the address people open, if not http://localhost:<port>
  OC_*             app settings passed into the container when it's created (e.g. OC_SIGNUP_ENABLED=false)
  ODB_OUTPUT_DIR   folder for JSON files from directory targets (default ~/omni-data-bus-output)
  ODB_IMAGE        the image to run (default $IMAGE)
EOF
}

need_docker() {
  command -v docker >/dev/null 2>&1 || die "Docker isn't installed. Install Docker Desktop (macOS, Windows) or Docker Engine (Linux)."
  docker info >/dev/null 2>&1 || die "Docker isn't running. Start Docker Desktop (or: sudo systemctl start docker) and try again."
}

exists() { [ -n "$(docker ps -aq --filter "name=^${NAME}\$")" ]; }
running() { [ -n "$(docker ps -q --filter "name=^${NAME}\$")" ]; }

sha256_check() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum -c "$1"; else shasum -a 256 -c "$1"; fi
}

host_arch() {
  case "$(docker info --format '{{.Architecture}}' 2>/dev/null)" in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) uname -m ;;
  esac
}

# The archive to load: the argument, else the one for this machine's CPU next to this script.
archive() {
  if [ -n "${1:-}" ]; then
    [ -f "$1" ] || die "no such file: $1"
    echo "$1"
    return
  fi
  local arch f
  arch=$(host_arch)
  for f in "$HERE"/omni-data-bus-appliance-*-"$arch".tar.gz; do
    [ -f "$f" ] && echo "$f" && return
  done
  die "no omni-data-bus-appliance-*-$arch.tar.gz next to run.sh; pass the file: ./run.sh load <file>"
}

cmd_load() {
  need_docker
  local file dir base out
  file=$(archive "${1:-}")
  dir=$(cd "$(dirname "$file")" && pwd)
  base=$(basename "$file")
  if [ -f "$dir/$base.sha256" ]; then
    say "Checking $base"
    (cd "$dir" && sha256_check "$base.sha256") || die "checksum mismatch: the file is damaged or incomplete; download it again"
  else
    say "No $base.sha256 next to it; skipping the checksum."
  fi
  say "Loading $base (this takes a minute)"
  out=$(docker load -i "$file")
  say "$out"
  LOADED=$(printf '%s\n' "$out" | sed -n 's/^Loaded image: //p' | tail -1)
}

check_memory() {
  local total
  total=$(docker info --format '{{.MemTotal}}')
  # 3 GiB minimum, with a little slack for what the VM reports.
  [ "$total" -ge 3060000000 ] || die "Docker has $((total / 1048576)) MiB of memory; the app needs at least 3 GiB (4 GiB recommended).
In Docker Desktop: Settings → Resources → Memory."
}

port_free() { ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

wait_healthy() {
  local deadline=$((SECONDS + START_TIMEOUT)) health
  printf 'Waiting for the app to be ready'
  while :; do
    running || {
      printf '\n'
      docker logs --tail 30 "$NAME" >&2 || true
      die "the app stopped while starting; see the log lines above (./run.sh logs)"
    }
    health=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$NAME" 2>/dev/null || true)
    [ "$health" = healthy ] && break
    if [ "$health" = unhealthy ] || [ "$SECONDS" -ge "$deadline" ]; then
      printf '\n'
      docker logs --tail 30 "$NAME" >&2 || true
      die "the app isn't healthy after $((SECONDS)) s; see ./run.sh logs"
    fi
    printf '.'
    sleep 3
  done
  printf '\n'
}

cmd_start() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --port) PORT=${2:?--port needs a value}; shift 2 ;;
      --memory) MEMORY=${2:?--memory needs a value}; shift 2 ;;
      --name) NAME=${2:?--name needs a value}; shift 2 ;;
      --output) OUTPUT_DIR=${2:?--output needs a folder}; shift 2 ;;
      *) die "unknown option: $1" ;;
    esac
  done
  need_docker
  # The output folder is fixed when the container is created: a different one (or none, for a
  # container from before OC-080) means recreating it. The data volume is kept.
  if exists; then
    local current
    current=$(mounted_output)
    if [ -z "$current" ] || { [ -n "$OUTPUT_DIR" ] && [ "$(resolve_dir "$OUTPUT_DIR")" != "$current" ]; }; then
      [ -n "$OUTPUT_DIR" ] || OUTPUT_DIR=$DEFAULT_OUTPUT
      say "Output folder changed to $(resolve_dir "$OUTPUT_DIR"); recreating the container (data is kept)."
      running && docker stop --time 90 "$NAME" >/dev/null
      docker rm "$NAME" >/dev/null
    fi
  fi
  local url=${ODB_PUBLIC_URL:-http://localhost:$PORT}
  if running; then
    say "Already running: $url"
    return
  fi
  if exists; then
    say "Starting $NAME"
    docker start "$NAME" >/dev/null
  else
    docker image inspect "$IMAGE" >/dev/null 2>&1 || die "image $IMAGE isn't loaded; run ./run.sh load first"
    check_memory
    port_free "$PORT" || die "port $PORT is in use; pick another: ./run.sh start --port 8081"
    local out
    out=$(resolve_dir "${OUTPUT_DIR:-$DEFAULT_OUTPUT}")
    check_writable "$out"
    local console=(-p "127.0.0.1:$CONSOLE_PORT:8091")
    if ! port_free "$CONSOLE_PORT"; then
      # Another Couchbase on this machine, typically. The console is only for support.
      say "Port $CONSOLE_PORT is in use, so the Couchbase console isn't published (set ODB_CONSOLE_PORT to another port)."
      console=()
    fi
    say "Starting $NAME from $IMAGE"
    # Any OC_* setting in this shell is passed on (e.g. OC_SIGNUP_ENABLED=false ./run.sh start).
    local v extra=()
    for v in $(compgen -e | grep '^OC_' || true); do extra+=(-e "$v"); done
    # Docker Desktop resolves host.docker.internal (this computer, e.g. a database on it);
    # Docker Engine on Linux needs it added.
    [ "$(uname -s)" = Linux ] && extra+=(--add-host host.docker.internal:host-gateway)
    docker run -d --name "$NAME" --restart unless-stopped ${extra[@]+"${extra[@]}"} \
      -p "$PORT:8080" ${console[@]+"${console[@]}"} \
      -v "$VOLUME:/opt/couchbase/var" \
      -v "$out:/data" -e "ODB_OUTPUT_HOST_DIR=$out" \
      --memory "$MEMORY" --stop-timeout 90 \
      -e "ODB_PUBLIC_URL=$url" \
      "$IMAGE" >/dev/null
  fi
  wait_healthy
  say "Ready: open $url"
  say "JSON files from directory targets: $(mounted_output) (shown as /data in the app)"
}

DEFAULT_OUTPUT=$HOME/omni-data-bus-output

# resolve_dir DIR: DIR created if missing, as an absolute path without symlinks.
resolve_dir() {
  mkdir -p "$1" || die "can't create the output folder $1"
  (cd "$1" && pwd -P)
}

# The output folder of the existing container ("" when it has none).
mounted_output() {
  docker inspect -f '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Source}}{{end}}{{end}}' "$NAME" 2>/dev/null | sed 's#^/host_mnt##'
}

# check_writable DIR: on Linux the app (uid 1000) must be able to write DIR; Docker Desktop
# (macOS, Windows) maps permissions itself.
check_writable() {
  [ "$(uname -s)" = Linux ] || return 0
  [ "$(id -u)" = 1000 ] && return 0
  if docker run --rm --user 1000 --entrypoint sh -v "$1:/d" "$IMAGE" -c 'touch /d/.odb-write-test && rm -f /d/.odb-write-test' >/dev/null 2>&1; then
    return 0
  fi
  if command -v setfacl >/dev/null 2>&1 && setfacl -m u:1000:rwx -m d:u:1000:rwx "$1" 2>/dev/null; then
    say "Allowed the app (uid 1000) to write to $1."
    return 0
  fi
  die "the app (uid 1000) can't write to $1. Run one of these, then ./run.sh start again:
  sudo setfacl -m u:1000:rwx -m d:u:1000:rwx $1
  sudo chown -R 1000 $1"
}

cmd_stop() {
  need_docker
  running || {
    say "Not running."
    return
  }
  say "Stopping $NAME (running migrations save their progress first)"
  docker stop --time 90 "$NAME" >/dev/null
  say "Stopped. Data is kept in the $VOLUME volume."
}

cmd_status() {
  need_docker
  exists || {
    say "Not created yet: ./run.sh start"
    return
  }
  docker ps -a --filter "name=^${NAME}\$" --format 'Container: {{.Names}}  {{.Status}}  image {{.Image}}'
  say "JSON output folder: $(mounted_output)"
  if running; then
    docker exec "$NAME" odb version 2>/dev/null || true
    docker exec "$NAME" sh -c 'echo "Couchbase Server CE $ODB_CB_VERSION"'
  fi
}

cmd_logs() {
  need_docker
  docker logs "$@" "$NAME"
}

cmd_upgrade() {
  [ -n "${1:-}" ] || die "usage: ./run.sh upgrade <omni-data-bus-appliance-…tar.gz>"
  cmd_load "$1"
  [ -n "$LOADED" ] || die "couldn't tell which image the archive holds"
  IMAGE=$LOADED
  # Keep the output folder the old container had.
  if exists && [ -z "$OUTPUT_DIR" ]; then OUTPUT_DIR=$(mounted_output); fi
  if exists; then
    cmd_stop
    docker rm "$NAME" >/dev/null
  fi
  cmd_start
  say "Upgraded to $IMAGE; projects, users and migrated data are kept."
}

cmd_backup() {
  need_docker
  local dir was_running="" file
  dir=$(cd "${1:-$HERE}" && pwd)
  file="odb-backup-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
  exists || die "nothing to back up: the app was never started"
  if running; then
    was_running=1
    cmd_stop
  fi
  say "Archiving the $VOLUME volume to $dir/$file"
  docker run --rm --entrypoint tar -v "$VOLUME:/data:ro" -v "$dir:/backup" "$(docker inspect -f '{{.Config.Image}}' "$NAME")" \
    czf "/backup/$file" -C /data .
  [ -z "$was_running" ] || cmd_start
  say "Backup: $dir/$file"
}

cmd_restore() {
  local file="" yes=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --yes) yes=1; shift ;;
      *) file=$1; shift ;;
    esac
  done
  [ -n "$file" ] && [ -f "$file" ] || die "usage: ./run.sh restore <backup.tar.gz> --yes"
  need_docker
  running && die "stop the app first: ./run.sh stop"
  [ -n "$yes" ] || die "restore replaces ALL data in the $VOLUME volume; add --yes to confirm"
  local dir base image
  dir=$(cd "$(dirname "$file")" && pwd)
  base=$(basename "$file")
  image=$IMAGE
  exists && image=$(docker inspect -f '{{.Config.Image}}' "$NAME")
  say "Restoring $base into the $VOLUME volume"
  docker run --rm --entrypoint sh -v "$VOLUME:/data" -v "$dir:/backup:ro" "$image" \
    -c 'find /data -mindepth 1 -delete && tar xzpf "/backup/$1" -C /data --numeric-owner' sh "$base"
  say "Restored. Start it with ./run.sh start"
}

cmd_remove() {
  local delete=""
  [ "${1:-}" = "--delete-data" ] && delete=1
  need_docker
  if exists; then
    running && cmd_stop
    docker rm "$NAME" >/dev/null
    say "Removed the $NAME container."
  fi
  if [ -z "$delete" ]; then
    say "Data is kept in the $VOLUME volume (./run.sh start uses it again; ./run.sh remove --delete-data deletes it)."
    return
  fi
  printf 'This deletes ALL projects, users and migrated data in %s. Type DELETE to confirm: ' "$VOLUME"
  local answer=""
  read -r answer || true # no terminal (piped, CI): not confirmed
  [ "$answer" = DELETE ] || die "not confirmed; nothing deleted"
  docker volume rm "$VOLUME" >/dev/null
  say "Deleted the $VOLUME volume."
}

cmd_credentials() {
  need_docker
  running || die "start the app first: ./run.sh start"
  docker exec "$NAME" odb-appliance show-credentials
}

LOADED=""
command=${1:-help}
[ $# -gt 0 ] && shift
case "$command" in
  load) cmd_load "$@" ;;
  start) cmd_start "$@" ;;
  stop) cmd_stop ;;
  status) cmd_status ;;
  logs) cmd_logs "$@" ;;
  upgrade) cmd_upgrade "$@" ;;
  backup) cmd_backup "$@" ;;
  restore) cmd_restore "$@" ;;
  remove) cmd_remove "$@" ;;
  credentials) cmd_credentials ;;
  help | -h | --help) usage ;;
  *)
    usage >&2
    exit 2
    ;;
esac
