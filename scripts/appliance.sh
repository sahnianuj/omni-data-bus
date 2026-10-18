#!/usr/bin/env bash
# Builds, packages and shares the all-in-one image (OC-073). The Makefile's appliance* targets call
# this; run it from the repository root.
#
#   scripts/appliance.sh build <amd64|arm64>              load omni-data-bus-appliance:<version>
#   scripts/appliance.sh save  <amd64|arm64|all>          archive(s), checksums and run kit in dist/appliance
#   scripts/appliance.sh push  <registry>                 both platforms as one multi-arch image
#
# Settings come from the environment (the Makefile passes them): VERSION, COMMIT, BUILD_TIME,
# CHANNEL, BUILD, DIRTY (OC-172), APPLIANCE (image name, default omni-data-bus-appliance), DIST (default dist/appliance).
set -euo pipefail

VERSION=${VERSION:-dev}
COMMIT=${COMMIT:-none}
BUILD_TIME=${BUILD_TIME:-}
CHANNEL=${CHANNEL:-}
BUILD=${BUILD:-}
DIRTY=${DIRTY:-}
APPLIANCE=${APPLIANCE:-omni-data-bus-appliance}
DIST=${DIST:-dist/appliance}
KIT=deploy/appliance

log() { printf '==> %s\n' "$*"; }
die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

build_args() {
  printf '%s\n' --build-arg "VERSION=$VERSION" --build-arg "COMMIT=$COMMIT" \
    --build-arg "BUILD_TIME=$BUILD_TIME" --build-arg "CHANNEL=$CHANNEL" \
    --build-arg "BUILD=$BUILD" --build-arg "DIRTY=$DIRTY"
}

# need_platform ARCH: the builder can build linux/ARCH (QEMU for the other CPU).
need_platform() {
  local platforms
  platforms=$(docker buildx inspect 2>/dev/null || true) # not piped into grep -q: SIGPIPE + pipefail
  [[ $platforms == *"linux/$1"* ]] || die "docker buildx can't build linux/$1 here. Enable emulation with:
  docker run --privileged --rm tonistiigi/binfmt --install all"
}

check_arch() {
  case "$1" in
    amd64 | arm64) ;;
    *) die "ARCH must be amd64 or arm64 (got '$1')" ;;
  esac
}

# build ARCH: the regular image, then the appliance on top of its binary, both for linux/ARCH.
build() {
  local arch=$1 odb
  check_arch "$arch"
  need_platform "$arch"
  odb="omni-data-bus:$VERSION-$arch"
  log "building $odb (linux/$arch)"
  # shellcheck disable=SC2046 # one argument per line
  docker buildx build --platform "linux/$arch" -f deploy/docker/Dockerfile $(build_args) --load -t "$odb" .
  log "building $APPLIANCE:$VERSION (linux/$arch)"
  # shellcheck disable=SC2046
  docker buildx build --platform "linux/$arch" -f "$KIT/Dockerfile" $(build_args) --build-arg "ODB_IMAGE=$odb" \
    --load -t "$APPLIANCE:$VERSION-$arch" -t "$APPLIANCE:$VERSION" .
  log "built $APPLIANCE:$VERSION for linux/$arch"
}

sha256() {
  if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

# save ARCH: build, then archive the image so `docker load` restores $APPLIANCE:$VERSION.
save_one() {
  local arch=$1 file
  build "$arch"
  mkdir -p "$DIST"
  file="$APPLIANCE-$VERSION-$arch.tar.gz"
  log "saving $DIST/$file"
  docker tag "$APPLIANCE:$VERSION-$arch" "$APPLIANCE:$VERSION"
  docker save "$APPLIANCE:$VERSION" | gzip -6 >"$DIST/$file.tmp"
  mv "$DIST/$file.tmp" "$DIST/$file"
  (cd "$DIST" && sha256 "$file" >"$file.sha256")
}

save() {
  local arch=$1
  if [ "$arch" = all ]; then
    save_one amd64
    save_one arm64
  else
    save_one "$arch"
  fi
  # The run kit the customer needs next to the archive (OC-074).
  cp "$KIT/run.sh" "$KIT/compose.yml" "$KIT/README.md" "$DIST/"
  chmod +x "$DIST/run.sh"
  sed -i.bak "s/^ODB_APPLIANCE_VERSION=.*/ODB_APPLIANCE_VERSION=$VERSION/" "$DIST/run.sh" && rm -f "$DIST/run.sh.bak"
  sed -i.bak "s/omni-data-bus-appliance:latest/$APPLIANCE:$VERSION/" "$DIST/compose.yml" && rm -f "$DIST/compose.yml.bak"
  log "ready to share: $DIST/"
  du -h "$DIST"/* | sed "s|$DIST/||"
  cat <<EOF

Send the customer the whole folder (or the archive for their CPU, run.sh, compose.yml and README.md).
They run:
  ./run.sh load        # checks the checksum, then docker load
  ./run.sh start       # http://localhost:8080
EOF
}

# use_multiarch_builder REGISTRY: a docker-container builder (the default docker builder can't
# push a multi-platform image), on the host network, allowed to use plain http for a registry on
# this machine (localhost:5000 and the like).
use_multiarch_builder() {
  local host=${1%%/*} cfg
  if ! docker buildx inspect odb-appliance >/dev/null 2>&1; then
    cfg=$(mktemp)
    case "$host" in
      localhost* | 127.0.0.1*) printf '[registry."%s"]\n  http = true\n  insecure = true\n' "$host" >"$cfg" ;;
    esac
    docker buildx create --name odb-appliance --driver docker-container --driver-opt network=host \
      --buildkitd-config "$cfg" >/dev/null
    rm -f "$cfg"
  fi
  export BUILDX_BUILDER=odb-appliance
}

# push REGISTRY: both platforms as one manifest list. The regular image goes first, under the same
# registry, because the appliance copies its binary from it.
push() {
  local reg=${1%/} odb
  [ -n "$reg" ] || die "set REGISTRY, e.g. make appliance-push REGISTRY=registry.example.com/team"
  need_platform amd64
  need_platform arm64
  use_multiarch_builder "$reg"
  odb="$reg/omni-data-bus:$VERSION"
  log "pushing $odb (linux/amd64, linux/arm64)"
  # shellcheck disable=SC2046
  docker buildx build --platform linux/amd64,linux/arm64 -f deploy/docker/Dockerfile $(build_args) --push -t "$odb" .
  log "pushing $reg/$APPLIANCE:$VERSION (linux/amd64, linux/arm64)"
  # shellcheck disable=SC2046
  docker buildx build --platform linux/amd64,linux/arm64 -f "$KIT/Dockerfile" $(build_args) --build-arg "ODB_IMAGE=$odb" \
    --push -t "$reg/$APPLIANCE:$VERSION" .
  log "customers run: docker pull $reg/$APPLIANCE:$VERSION"
}

cmd=${1:-}
shift || true
case "$cmd" in
  build) build "${1:?ARCH}" ;;
  save) save "${1:?ARCH}" ;;
  push) push "${1:-}" ;;
  *) die "usage: $0 build|save <amd64|arm64|all> | push <registry>" ;;
esac
