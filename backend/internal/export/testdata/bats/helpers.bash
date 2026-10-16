# Shared setup for the migrate.sh suites. The Go test (bats_test.go) generates the scripts and
# stubs and exports: SCRIPT_EMBEDDED (auto, with an embedded stub odb), SCRIPT_PLAIN (auto, no
# embedded odb, downloads from a local server), SCRIPT_TAMPERED (binary, the server sends a
# tampered tarball), SCRIPT_CONFIG (auto, a config-mode export beside its config.json), STUBS
# (stub odb binaries and a stub docker), BUNDLE_SHA.

setup() {
  WORKDIR="$BATS_TEST_TMPDIR/work"
  mkdir -p "$WORKDIR"
  cd "$WORKDIR" || return 1
  export STUB_LOG="$BATS_TEST_TMPDIR/stub.log"
  : >"$STUB_LOG"
  export ODB_CACHE_DIR="$BATS_TEST_TMPDIR/cache"
  export ODB_STATE_DIR="$BATS_TEST_TMPDIR/state"
  export ODB_TARGET_PATH="$BATS_TEST_TMPDIR/out"
  export TMPDIR="$BATS_TEST_TMPDIR/tmp"
  mkdir -p "$TMPDIR"
  # A PATH with the system tools but no odb or docker, unless a test adds them.
  mkdir -p "$BATS_TEST_TMPDIR/sysbin"
  local t
  # The script's own needs (see its header), plus what these helpers use (ln, ls, tail…).
  for t in bash sh awk sed base64 gzip tar mktemp chmod cat cp mkdir rm mv uname id sleep grep env dirname curl wget shasum sha256sum perl tr wc ln ls tail; do
    if command -v "$t" >/dev/null 2>&1; then
      ln -sf "$(command -v "$t")" "$BATS_TEST_TMPDIR/sysbin/$t"
    fi
  done
  export PATH="$BATS_TEST_TMPDIR/sysbin"
  unset ODB_BIN ODB_RUNTIME ODB_CONFIG STUB_EXIT STUB_SLEEP
}

# with_stub NAME puts stubs/NAME on the PATH as odb (or docker).
with_on_path() {
  mkdir -p "$BATS_TEST_TMPDIR/extra"
  ln -sf "$STUBS/$1" "$BATS_TEST_TMPDIR/extra/$2"
  export PATH="$BATS_TEST_TMPDIR/extra:$PATH"
}

# used prints which odb ran last (the stub's NAME).
used() {
  awk '{ print $1 }' "$STUB_LOG" | tail -n 1
}
