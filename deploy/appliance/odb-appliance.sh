#!/usr/bin/env bash
# shellcheck disable=SC2329 # cb_up, bucket_healthy and query_up are called through wait_for
# odb-appliance: runs Couchbase Server CE and Omni-Data-Bus in one container (OC-072).
#
#   odb-appliance                    start both (the image's entrypoint, under tini)
#   odb-appliance show-credentials   print the bundled Couchbase's admin user and password
#
# Couchbase starts first; on the first start it is initialized (cluster, buckets, generated
# secrets). Omni-Data-Bus then starts against it. On SIGTERM/SIGINT Omni-Data-Bus stops first, so
# running jobs drain and checkpoint into Couchbase, then Couchbase stops. If either process dies,
# the other is stopped and the container exits non-zero, so Docker's restart policy restarts both.
#
# We supervise the two processes here rather than adding Omni-Data-Bus as a runit service in the
# Couchbase image: runsvdir exits on SIGTERM without stopping its services in order.
set -Eeuo pipefail
trap 'printf "appliance: error: line %s failed (exit %s)\\n" "$LINENO" "$?" >&2' ERR

VAR=/opt/couchbase/var
STATE=$VAR/odb
SECRETS=$STATE/secrets
CB_BIN=/opt/couchbase/bin
CB_RUN=/etc/service/couchbase-server/run
CB_INIT=/usr/local/lib/odb/cb-init.sh
CB_USER=Administrator
META_BUCKET=omniconnect
TARGET_BUCKET=sandbox
CB_START_TIMEOUT=${ODB_CB_START_TIMEOUT:-180}
ODB_STOP_TIMEOUT=${ODB_STOP_TIMEOUT:-45}
CB_STOP_TIMEOUT=${ODB_CB_STOP_TIMEOUT:-40}
MIN_MEMORY_MB=3072

log() { printf 'appliance: %s\n' "$*"; }
# die MESSAGE [EXIT_CODE]
die() {
  log "error: $1" >&2
  exit "${2:-1}"
}

# secret NAME FILE: the value of $NAME, else of the file named by $NAME_FILE, else empty.
secret() {
  local name=$1 file_var="${1}_FILE"
  if [ -n "${!name:-}" ]; then
    printf '%s' "${!name}"
  elif [ -n "${!file_var:-}" ]; then
    tr -d '\r\n' <"${!file_var}"
  fi
  return 0
}

if [ "${1:-}" = "show-credentials" ]; then
  [ -f "$SECRETS/cb_admin_password" ] || die "not initialized yet: start the container first"
  printf 'Couchbase web console: http://localhost:8091\nUser:     %s\nPassword: %s\n' "$CB_USER" "$(cat "$SECRETS/cb_admin_password")"
  exit 0
fi
[ $# -eq 0 ] || exec "$@"

# ── Memory and RAM quotas ─────────────────────────────────────────────────────
# The container's memory limit (cgroup v2, then v1), else the machine's memory, capped at 8 GiB.
memory_mb() {
  local limit=""
  if [ -r /sys/fs/cgroup/memory.max ]; then
    limit=$(cat /sys/fs/cgroup/memory.max)
  elif [ -r /sys/fs/cgroup/memory/memory.limit_in_bytes ]; then
    limit=$(cat /sys/fs/cgroup/memory/memory.limit_in_bytes)
  fi
  local total_kb
  total_kb=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
  local mb=$((total_kb / 1024))
  if [ -n "$limit" ] && [ "$limit" != "max" ] && [ "$limit" -lt $((mb * 1024 * 1024)) ]; then
    mb=$((limit / 1024 / 1024))
  fi
  [ "$mb" -gt 8192 ] && mb=8192
  echo "$mb"
}

MEM_MB=$(memory_mb)
# Docker Desktop reports a "4g" VM as a little less than 4096 MiB; allow 5% slack.
[ "$MEM_MB" -ge $((MIN_MEMORY_MB * 95 / 100)) ] || die "the appliance needs at least 3 GiB of memory; it has ${MEM_MB} MiB (docker run --memory 4g, or raise Docker Desktop's memory)" 78
if [ "$MEM_MB" -le 4096 ]; then
  DATA_RAM=1024 INDEX_RAM=256
else
  DATA_RAM=$((MEM_MB * 40 / 100)) INDEX_RAM=$((MEM_MB * 10 / 100))
fi
DATA_RAM=${ODB_CB_DATA_RAM_MB:-$DATA_RAM}
INDEX_RAM=${ODB_CB_INDEX_RAM_MB:-$INDEX_RAM}
META_RAM=256
TARGET_RAM=$((DATA_RAM - META_RAM))

# ── Couchbase ─────────────────────────────────────────────────────────────────
CB_PID="" ODB_PID="" STOPPING=""

# stop PID NAME TIMEOUT: SIGTERM, then SIGKILL after TIMEOUT seconds.
stop() {
  local pid=$1 name=$2 timeout=$3 deadline
  kill -0 "$pid" 2>/dev/null || return 0
  kill -TERM "$pid" 2>/dev/null || true
  deadline=$((SECONDS + timeout))
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      log "$name didn't stop within ${timeout}s; killing it"
      kill -KILL "$pid" 2>/dev/null || true
      break
    fi
    sleep 0.5
  done
  wait "$pid" 2>/dev/null || true
}

stop_couchbase() {
  kill -0 "$CB_PID" 2>/dev/null || return 0
  log "stopping Couchbase Server"
  chpst -u couchbase:couchbase "$CB_BIN/couchbase-server" -k >/dev/null 2>&1 || true
  stop "$CB_PID" "Couchbase Server" "$CB_STOP_TIMEOUT"
}

# A stop during startup stops Couchbase too.
trap 'log "stopping during startup"; [ -z "$CB_PID" ] || stop_couchbase; exit 0' TERM INT

cb_up() { "$CB_BIN/curl" -fsS -o /dev/null "http://127.0.0.1:8091/ui/index.html" 2>/dev/null; }

# wait_for WHAT CHECK: run CHECK until it succeeds, Couchbase dies, or the start timeout passes.
wait_for() {
  local what=$1 deadline=$((SECONDS + CB_START_TIMEOUT))
  shift
  until "$@"; do
    kill -0 "$CB_PID" 2>/dev/null || die "Couchbase Server exited while waiting for $what; see $VAR/lib/couchbase/logs"
    if [ "$SECONDS" -ge "$deadline" ]; then
      tail -n 20 "$VAR/lib/couchbase/logs/error.log" 2>/dev/null >&2 || true
      die "Couchbase Server wasn't ready ($what) after ${CB_START_TIMEOUT}s (ODB_CB_START_TIMEOUT)"
    fi
    sleep 2
  done
}

log "starting Couchbase Server ${ODB_CB_VERSION:-}"
# The base image's own run script: creates /opt/couchbase/var, fixes ownership, drops to uid 1000.
"$CB_RUN" &
CB_PID=$!
wait_for "its web console" cb_up

mkdir -p "$SECRETS" "$STATE/home"
chown couchbase:couchbase "$STATE" "$SECRETS" "$STATE/home"
chmod 700 "$SECRETS"

# store NAME VALUE: writes a secret file readable only by the couchbase user.
store() {
  (umask 077 && printf '%s' "$2" >"$SECRETS/$1.tmp")
  chown couchbase:couchbase "$SECRETS/$1.tmp"
  mv "$SECRETS/$1.tmp" "$SECRETS/$1"
}

if [ ! -f "$STATE/initialized" ]; then
  log "first start: initializing the bundled Couchbase (data ${DATA_RAM} MB, index ${INDEX_RAM} MB)"
  if [ ! -f "$SECRETS/cb_admin_password" ]; then
    pw=$(secret ODB_CB_ADMIN_PASSWORD)
    # 48 random bytes → ~60 letters and digits (no early-closed pipe: SIGPIPE + pipefail).
    [ -n "$pw" ] || pw=$(head -c 48 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')
    store cb_admin_password "$pw"
  fi
  if [ -z "$(secret OC_MASTER_KEY)" ] && [ -z "${OC_MASTER_KEYS:-}${OC_MASTER_KEYS_FILE:-}" ] && [ ! -f "$SECRETS/master_key" ]; then
    store master_key "$(/usr/local/bin/odb admin gen-key)"
  fi
  CB_HOST=127.0.0.1 CB_USERNAME=$CB_USER CB_PASSWORD="$(cat "$SECRETS/cb_admin_password")" \
    META_BUCKET=$META_BUCKET TARGET_BUCKET=$TARGET_BUCKET CLUSTER_NAME=omni-data-bus \
    DATA_RAM_MB=$DATA_RAM INDEX_RAM_MB=$INDEX_RAM META_RAM_MB=$META_RAM TARGET_RAM_MB=$TARGET_RAM \
    bash "$CB_INIT" | sed 's/^/appliance: /'
  date -u +%FT%TZ >"$STATE/initialized" # last: an interrupted first start is redone
fi
CB_PASSWORD=$(cat "$SECRETS/cb_admin_password")

bucket_healthy() {
  local body
  body=$("$CB_BIN/curl" -fsS -u "$CB_USER:$CB_PASSWORD" "http://127.0.0.1:8091/pools/default/buckets/$META_BUCKET" 2>/dev/null) || return 1
  [[ $body == *'"status":"healthy"'* ]]
}
query_up() { "$CB_BIN/curl" -fsS -o /dev/null -u "$CB_USER:$CB_PASSWORD" "http://127.0.0.1:8093/admin/ping" 2>/dev/null; }
wait_for "the $META_BUCKET bucket" bucket_healthy
wait_for "the query service" query_up

# ── Omni-Data-Bus ─────────────────────────────────────────────────────────────
for v in OC_META_CONNSTR OC_META_USERNAME OC_META_PASSWORD OC_META_PASSWORD_FILE OC_META_BUCKET OC_META_CAPELLA; do
  [ -z "${!v:-}" ] || log "warning: $v is ignored: the appliance always uses its bundled Couchbase"
  unset "$v"
done
export OC_META_CONNSTR=couchbase://127.0.0.1 OC_META_USERNAME=$CB_USER OC_META_PASSWORD_FILE=$SECRETS/cb_admin_password
export OC_META_BUCKET=$META_BUCKET OC_META_SCOPE=${OC_META_SCOPE:-meta} OC_META_AUTO_PROVISION=true
if [ -z "${OC_MASTER_KEY:-}${OC_MASTER_KEY_FILE:-}${OC_MASTER_KEYS:-}${OC_MASTER_KEYS_FILE:-}" ]; then
  export OC_MASTER_KEY_FILE=$SECRETS/master_key
fi
export OC_ROLE=${OC_ROLE:-all} OC_HTTP_ADDR=${OC_HTTP_ADDR:-:8080} OC_LOG_FORMAT=${OC_LOG_FORMAT:-json}
# The bundled node is on loopback, which egress control blocks unless private networks are allowed.
export OC_ALLOW_PRIVATE_NETWORKS=${OC_ALLOW_PRIVATE_NETWORKS:-true}
export OC_PUBLIC_URL=${OC_PUBLIC_URL:-${ODB_PUBLIC_URL:-http://localhost:8080}}
# Secure cookies are dropped over plain http (http://<server>:8080), so only send them over https.
case "$OC_PUBLIC_URL" in
  https://*) export OC_SESSION_COOKIE_SECURE=${OC_SESSION_COOKIE_SECURE:-true} ;;
  *) export OC_SESSION_COOKIE_SECURE=${OC_SESSION_COOKIE_SECURE:-false} ;;
esac
export OC_BUNDLED_TARGET=couchbase://127.0.0.1 OC_BUNDLED_TARGET_USERNAME=$CB_USER
export OC_BUNDLED_TARGET_PASSWORD_FILE=$SECRETS/cb_admin_password OC_BUNDLED_TARGET_BUCKETS=$TARGET_BUCKET
export OC_BUNDLED_STORE="Couchbase Server CE ${ODB_CB_VERSION:-}"
# Directory targets may write under the server user's home: keep it on the volume.
export HOME=$STATE/home

# A folder of the user's computer mounted at /data (./run.sh start --output <folder>, OC-080)
# becomes the directory targets' only area, shown with its path on the computer.
OUTPUT_HOST=${ODB_OUTPUT_HOST_DIR:-/data}
if [ -n "$(awk '$5 == "/data" {print "mounted"; exit}' /proc/self/mountinfo)" ]; then
  export OC_DIRECTORY_SHARED="/data=$OUTPUT_HOST"
  if chpst -u couchbase:couchbase sh -c 'touch /data/.odb-write-test && rm -f /data/.odb-write-test' 2>/dev/null; then
    log "JSON output folder on your computer: $OUTPUT_HOST (in the app: /data)"
  else
    log "warning: the shared folder $OUTPUT_HOST isn't writable by the app (uid 1000), so directory targets can't write there. On Linux run: sudo setfacl -m u:1000:rwx -m d:u:1000:rwx $OUTPUT_HOST   (or: sudo chown -R 1000 $OUTPUT_HOST), then restart."
  fi
else
  # Nothing shared: the picker marks its folders as inside the container.
  export OC_DIRECTORY_CONTAINER_ONLY=true
fi

log "starting Omni-Data-Bus"
chpst -u couchbase:couchbase /usr/local/bin/odb &
ODB_PID=$!

# Announce readiness once, in the background.
(
  until /usr/local/bin/odb healthcheck >/dev/null 2>&1; do
    kill -0 "$ODB_PID" 2>/dev/null || exit 0
    sleep 2
  done
  log "ready: open ${OC_PUBLIC_URL}"
) &

# ── Supervision and shutdown ──────────────────────────────────────────────────
trap 'STOPPING=1' TERM INT
# A signal interrupts wait (exit 128+n): expected, so no ERR report here.
trap - ERR
set +e
wait -n "$ODB_PID" "$CB_PID"
set -e
if [ -n "$STOPPING" ]; then
  log "stopping Omni-Data-Bus (running jobs drain and checkpoint)"
  stop "$ODB_PID" "Omni-Data-Bus" "$ODB_STOP_TIMEOUT"
  stop_couchbase
  log "stopped"
  exit 0
fi
if ! kill -0 "$ODB_PID" 2>/dev/null; then
  log "error: Omni-Data-Bus exited unexpectedly; stopping Couchbase Server"
  stop_couchbase
else
  log "error: Couchbase Server exited unexpectedly; stopping Omni-Data-Bus"
  stop "$ODB_PID" "Omni-Data-Bus" "$ODB_STOP_TIMEOUT"
fi
exit 1
