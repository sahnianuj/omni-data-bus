#!/usr/bin/env bats
# OC-053 AC3, AC4: how migrate.sh finds odb.

load helpers

@test "AC4: resolution order — ODB_BIN first" {
  with_on_path odb-path odb
  ODB_BIN="$STUBS/odb-bin" run "$SCRIPT_EMBEDDED" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "bin" ]
}

@test "AC4: resolution order — embedded before odb on the PATH" {
  with_on_path odb-path odb
  run "$SCRIPT_EMBEDDED" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "embedded" ]
}

@test "AC4: resolution order — odb on the PATH when nothing is embedded" {
  with_on_path odb-path odb
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "path" ]
}

@test "AC4: resolution order — docker when there is no odb" {
  with_on_path docker docker
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "docker" ]
  grep -q -- "--bundle /work/bundle.odb.json --state-dir /state --target-path /out" "$STUB_LOG"
}

@test "AC4: a PATH odb of another version is skipped with a note" {
  with_on_path odb-old odb
  with_on_path docker docker
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "docker" ]
  [[ "$output" == *"skipping"*"odb 1.9.0, this script needs 2.0.0"* ]]
}

@test "AC4: download, checked against the baked-in checksum, when nothing else is there" {
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "download" ]
  # The second run uses the cache.
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [[ "$output" != *"downloading"* ]]
}

@test "AC3: rejects tampered download" {
  run "$SCRIPT_TAMPERED" run
  [ "$status" -eq 4 ]
  [[ "$output" == *"checksum mismatch"* ]]
  [ ! -s "$STUB_LOG" ]
  [ -z "$(ls -A "$ODB_CACHE_DIR"/*/ 2>/dev/null)" ]
}
