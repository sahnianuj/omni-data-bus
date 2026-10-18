#!/usr/bin/env bash
# Builds the odb command-line runner for every released platform (OC-051): static binaries in
# dist/odb/odb_<version>_<os>_<arch>.tar.gz plus SHA256SUMS. Export READMEs link these with their
# checksums (OC-182); point the server's OC_EXPORT_DIST_DIR at the folder so it knows them (and
# can add any platform's odb to an export).
#
# Windows gets odb_<version>_windows_<arch>.zip holding odb.exe (OC-186); the release signs it
# with Authenticode on a Windows runner.
#
# With lipo (macOS), it also builds odb_<version>_darwin_universal.tar.gz: one binary for Intel
# and Apple silicon Macs (OC-185). The release signs and notarizes that one on a macOS runner and
# writes SIGNED; this script's binaries are unsigned.
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

# tarball DIR NAME: a reproducible tarball of DIR/odb (fixed owner, mode and time, so equal
# binaries give equal checksums). grep without -q reads all of tar's output: -q would exit early,
# and under pipefail the SIGPIPE'd tar would make a GNU tar look like BSD tar.
# Each archive holds odb with its licence, NOTICE and third-party notices (OC-189).
legal="LICENSE NOTICE THIRD_PARTY_NOTICES.md"
withLegal() {
  # shellcheck disable=SC2086 # legal is a word list
  (cd "$root" && cp $legal "$1/")
}
tarball() {
  withLegal "$1"
  # shellcheck disable=SC2086 # legal is a word list
  if tar --version 2>/dev/null | grep GNU >/dev/null; then
    tar -C "$1" --owner=0 --group=0 --mode=0755 --mtime='1970-01-01 00:00:00Z' -cf - odb $legal | gzip -n -9 >"$out/$2.tar.gz"
  else
    tar -C "$1" --uid 0 --gid 0 -cf - odb $legal | gzip -n -9 >"$out/$2.tar.gz"
  fi
  echo "built $out/$2.tar.gz"
}

for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os=${platform%/*}
  arch=${platform#*/}
  name="odb_${version}_${os}_${arch}"
  mkdir -p "$work/$name"
  (cd "$root/backend" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$work/$name/odb" ./cmd/odb)
  tarball "$work/$name" "$name"
done

# Windows: odb.exe in a zip (a reproducible one: fixed time, no extra attributes).
for arch in amd64 arm64; do
  name="odb_${version}_windows_${arch}"
  mkdir -p "$work/$name"
  (cd "$root/backend" && CGO_ENABLED=0 GOOS=windows GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$work/$name/odb.exe" ./cmd/odb)
  withLegal "$work/$name"
  # shellcheck disable=SC2086 # legal is a word list
  (cd "$work/$name" && touch -t 198001010000 odb.exe $legal && zip -q -X "$root/$out/$name.zip" odb.exe $legal)
  echo "built $out/$name.zip"
done

if command -v lipo >/dev/null 2>&1; then
  name="odb_${version}_darwin_universal"
  mkdir -p "$work/$name"
  lipo -create -output "$work/$name/odb" "$work/odb_${version}_darwin_amd64/odb" "$work/odb_${version}_darwin_arm64/odb"
  tarball "$work/$name" "$name"
else
  echo "no lipo: skipping the universal macOS binary (the release builds it on macOS)"
fi

(
  cd "$out"
  if command -v sha256sum >/dev/null 2>&1; then sha256sum ./*.tar.gz ./*.zip; else shasum -a 256 ./*.tar.gz ./*.zip; fi | sed 's#  \./#  #' >SHA256SUMS
)
cat "$out/SHA256SUMS"
