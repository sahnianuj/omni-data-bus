#!/usr/bin/env bash
# Builds the odb command-line runner for every released platform (OC-051): static binaries in
# dist/odb/odb_<version>_<os>_<arch>.tar.gz plus SHA256SUMS. migrate.sh scripts download these
# and check them against the checksums the server writes into each script (OC-053); point the
# server's OC_EXPORT_DIST_DIR at the folder so it knows them (and can embed any platform).
#
# Usage: VERSION=2.0.0 COMMIT=abc LDFLAGS="…" scripts/dist.sh
set -euo pipefail

: "${VERSION:?VERSION is required}"
: "${LDFLAGS:=-s -w}"
version=${VERSION#v}
out="dist/odb"
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
rm -rf "$out"
mkdir -p "$out"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os=${platform%/*}
  arch=${platform#*/}
  name="odb_${version}_${os}_${arch}"
  mkdir -p "$work/$name"
  (cd "$root/backend" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$work/$name/odb" ./cmd/odb)
  # A reproducible tarball: fixed owner, mode and time, so equal binaries give equal checksums.
  # grep without -q reads all of tar's output: -q would exit early, and under pipefail the
  # SIGPIPE'd tar would make a GNU tar look like BSD tar.
  if tar --version 2>/dev/null | grep GNU >/dev/null; then
    tar -C "$work/$name" --owner=0 --group=0 --mode=0755 --mtime='1970-01-01 00:00:00Z' -cf - odb | gzip -n -9 >"$out/$name.tar.gz"
  else
    tar -C "$work/$name" --uid 0 --gid 0 -cf - odb | gzip -n -9 >"$out/$name.tar.gz"
  fi
  echo "built $out/$name.tar.gz"
done

(
  cd "$out"
  if command -v sha256sum >/dev/null 2>&1; then sha256sum ./*.tar.gz; else shasum -a 256 ./*.tar.gz; fi | sed 's#  \./#  #' >SHA256SUMS
)
cat "$out/SHA256SUMS"
