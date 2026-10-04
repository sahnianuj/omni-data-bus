#!/usr/bin/env bash
# Go integration tests against Couchbase Server Community Edition (OC-072 AC7): the edition the
# all-in-one image bundles. Starts a throwaway CE node with no published ports and runs the tests
# inside its network namespace, so localhost:8091… is CE even while the dev stack's Enterprise
# node uses the same ports on the host. PostgreSQL is the dev stack's (make dev-stack), reached
# through the host.
#
#   make test-integration-ce         (or: scripts/integration-ce.sh [go test args…])
set -euo pipefail
cd "$(dirname "$0")/.."

# IT_CB_IMAGE runs the same tests against another image (e.g. Enterprise, to compare).
CB_IMAGE=${IT_CB_IMAGE:-$(sed -n 's/^ARG CB_IMAGE=//p' deploy/appliance/Dockerfile)}
GO_VERSION=$(sed -n 's/^ARG GO_VERSION=//p' deploy/docker/Dockerfile)
NAME=odb-it-ce-$$
PASS=password
PG_HOST=${OC_IT_PG_HOST:-host.docker.internal}
PG_PORT=${OC_IT_PG_PORT:-5434}

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> starting $CB_IMAGE"
docker run -d --name "$NAME" --memory 4g --add-host host.docker.internal:host-gateway "$CB_IMAGE" >/dev/null
for _ in $(seq 1 90); do
  docker exec "$NAME" /opt/couchbase/bin/curl -fsS -o /dev/null http://127.0.0.1:8091/ui/index.html 2>/dev/null && break
  sleep 2
done
docker exec -i -e CB_HOST=127.0.0.1 -e CB_PASSWORD=$PASS -e CLUSTER_NAME=odb-it-ce "$NAME" bash -s <deploy/compose/couchbase/init.sh
# The query service answers a little after the buckets exist.
for _ in $(seq 1 60); do
  docker exec "$NAME" /opt/couchbase/bin/curl -fsS -o /dev/null -u "Administrator:$PASS" http://127.0.0.1:8093/admin/ping 2>/dev/null && break
  sleep 2
done

echo "==> go test -tags=integration against Couchbase CE (Go $GO_VERSION)"
docker run --rm --network "container:$NAME" \
  -v "$PWD:/repo" -w /repo/backend \
  -v odb-go-mod:/go/pkg/mod -v odb-go-build:/root/.cache/go-build \
  -e CGO_ENABLED=0 -e OC_IT_CB_EDITION=community -e OC_IT_REQUIRE_QUERY=1 \
  -e OC_IT_META_CONNSTR=couchbase://127.0.0.1 -e OC_IT_META_USERNAME=Administrator -e OC_IT_META_PASSWORD=$PASS \
  -e OC_IT_TARGET_CONNSTR=couchbase://127.0.0.1 -e OC_IT_TARGET_USERNAME=Administrator -e OC_IT_TARGET_PASSWORD=$PASS \
  -e OC_IT_PG_HOST="$PG_HOST" -e OC_IT_PG_PORT="$PG_PORT" \
  "golang:$GO_VERSION-alpine" go test -tags=integration -count=1 "${@:-./...}"
