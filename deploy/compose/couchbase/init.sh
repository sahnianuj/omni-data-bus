#!/usr/bin/env bash
# Initializes a Couchbase node and creates the Omni-Data-Bus buckets: the dev stack's node, and the
# all-in-one image's bundled node (OC-072), which passes its own sizes.
# Safe to re-run: "already initialized" and "bucket exists" are treated as success.
set -euo pipefail

HOST="${CB_HOST:-couchbase}"
USER="${CB_USERNAME:-Administrator}"
PASS="${CB_PASSWORD:-password}"
META_BUCKET="${META_BUCKET:-omniconnect}"
TARGET_BUCKET="${TARGET_BUCKET:-sandbox}"
CLUSTER_NAME="${CLUSTER_NAME:-omni-connect-dev}"
DATA_RAM_MB="${DATA_RAM_MB:-1024}"
INDEX_RAM_MB="${INDEX_RAM_MB:-512}"
META_RAM_MB="${META_RAM_MB:-256}"
TARGET_RAM_MB="${TARGET_RAM_MB:-512}"
CLI=/opt/couchbase/bin/couchbase-cli

echo "==> Initializing cluster on ${HOST}"
if out=$("$CLI" cluster-init -c "${HOST}:8091" \
      --cluster-username "$USER" --cluster-password "$PASS" \
      --cluster-name "$CLUSTER_NAME" \
      --services data,index,query \
      --cluster-ramsize "$DATA_RAM_MB" --cluster-index-ramsize "$INDEX_RAM_MB" \
      --index-storage-setting default 2>&1); then
  echo "$out"
elif echo "$out" | grep -qi "already initialized\|already been initialized"; then
  echo "cluster already initialized"
else
  echo "$out" >&2
  exit 1
fi

create_bucket() {
  local name="$1" ram="$2"
  echo "==> Creating bucket ${name}"
  if out=$("$CLI" bucket-create -c "${HOST}:8091" -u "$USER" -p "$PASS" \
        --bucket "$name" --bucket-type couchbase --bucket-ramsize "$ram" \
        --bucket-replica 0 --storage-backend couchstore --wait 2>&1); then
    echo "$out"
  elif echo "$out" | grep -qi "already exists"; then
    echo "bucket ${name} already exists"
  else
    echo "$out" >&2
    exit 1
  fi
}

create_bucket "$META_BUCKET" "$META_RAM_MB"
create_bucket "$TARGET_BUCKET" "$TARGET_RAM_MB"

echo "==> Couchbase ready: metadata bucket '${META_BUCKET}', target bucket '${TARGET_BUCKET}'"
