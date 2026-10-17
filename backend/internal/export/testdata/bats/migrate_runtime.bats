#!/usr/bin/env bats
# OC-053 AC3, AC4, OC-159 AC3: how migrate.sh finds odb.

load helpers

@test "AC4: resolution order — ODB_BIN first" {
  with_on_path odb-path odb
  script=$(copy_export)
  ln -s "$STUBS/odb-beside" "$(dirname "$script")/odb"
  ODB_BIN="$STUBS/odb-bin" run "$script" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "bin" ]
}

@test "OC-159 AC3: uses the odb beside the script, before odb on the PATH" {
  with_on_path odb-path odb
  script=$(copy_export)
  ln -s "$STUBS/odb-beside" "$(dirname "$script")/odb"
  run "$script" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "beside" ]
}

@test "OC-159 AC3: an odb of another version beside the script is skipped with a note" {
  with_on_path odb-path odb
  script=$(copy_export)
  ln -s "$STUBS/odb-old" "$(dirname "$script")/odb"
  run "$script" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "path" ]
  [[ "$output" == *"skipping"*"odb 1.9.0, this script needs 2.0.0"* ]]
}

@test "AC4: resolution order — odb on the PATH when there's none beside the script" {
  with_on_path odb-path odb
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "path" ]
}

@test "OC-159 AC3: docker mounts the folder in place" {
  with_on_path docker docker
  here=$(folder_of "$SCRIPT_PLAIN")
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "docker" ]
  grep -q -- "-w $here -v $here:$here" "$STUB_LOG"
  grep -q -- "-v $ODB_STATE_DIR:$ODB_STATE_DIR -v $ODB_TARGET_PATH:$ODB_TARGET_PATH" "$STUB_LOG"
  grep -q -- "omni-data-bus:2.0.0 run --mappings $here/mappings.json --config $here/config.json --state-dir $ODB_STATE_DIR --target-path $ODB_TARGET_PATH" "$STUB_LOG"
}

@test "OC-159 AC3: without ODB_TARGET_PATH, docker asks odb where a directory target writes" {
  with_on_path docker docker
  here=$(folder_of "$SCRIPT_PLAIN")
  unset ODB_TARGET_PATH
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  grep -q -- "run target-path --config $here/config.json" "$STUB_LOG"
  ! grep -q -- "--target-path" "$STUB_LOG"
}

@test "AC4: a PATH odb of another version is skipped with a note" {
  with_on_path odb-old odb
  with_on_path docker docker
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "docker" ]
  [[ "$output" == *"skipping"*"odb 1.9.0, this script needs 2.0.0"* ]]
}

@test "ODB_RUNTIME=binary never uses docker" {
  with_on_path docker docker
  ODB_RUNTIME=binary run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ "$(used)" = "download" ]
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
