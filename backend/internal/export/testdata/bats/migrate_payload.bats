#!/usr/bin/env bats
# OC-053 AC5: the payload, its hash, and the temporary directory.

load helpers

@test "AC5: sha matches header; tmp removed on INT" {
  with_on_path odb-path odb
  # inspect shows the header's bundle hash.
  run "$SCRIPT_PLAIN" inspect
  [ "$status" -eq 0 ]
  header=$(sed -n 's/.*bundle sha256: \([0-9a-f]*\).*/\1/p' "$SCRIPT_PLAIN")
  [ "$header" = "$BUNDLE_SHA" ]
  [[ "$output" == *"$BUNDLE_SHA"* ]]

  # extract writes the same bundle the payload holds.
  run "$SCRIPT_PLAIN" extract "$BATS_TEST_TMPDIR/x"
  [ "$status" -eq 0 ]
  grep -q "\"sha256\": \"$BUNDLE_SHA\"" "$BATS_TEST_TMPDIR/x/bundle.odb.json"

  # The temporary directory is private and removed on exit.
  STUB_SLEEP=3 "$SCRIPT_PLAIN" run &
  pid=$!
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    tmp=$(ls -d "$TMPDIR"/odb-migrate.* 2>/dev/null || true)
    [ -n "$tmp" ] && break
    sleep 0.2
  done
  [ -n "$tmp" ]
  [ "$(perl -e 'printf "%o", (stat($ARGV[0]))[2] & 0777' "$tmp")" = "700" ]
  kill -INT "$pid"
  wait "$pid" || true
  [ -z "$(ls -A "$TMPDIR")" ]
}

@test "AC5: tmp removed on a normal exit" {
  with_on_path odb-path odb
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ -z "$(ls -A "$TMPDIR")" ]
}
