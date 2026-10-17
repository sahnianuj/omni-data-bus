#!/usr/bin/env bats
# OC-159 AC2: migrate.sh reads config.json and mappings.json from its folder, or where
# ODB_CONFIG and ODB_MAPPINGS say.

load helpers

@test "OC-159 AC2: passes mappings.json and config.json from the script's folder" {
  with_on_path odb-path odb
  here=$(folder_of "$SCRIPT_PLAIN")
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  grep -q "path run --mappings $here/mappings.json --config $here/config.json --state-dir $ODB_STATE_DIR --target-path $ODB_TARGET_PATH" "$STUB_LOG"
}

@test "OC-159 AC2: ODB_CONFIG and ODB_MAPPINGS use other files, e.g. one per environment" {
  with_on_path odb-path odb
  here=$(folder_of "$SCRIPT_PLAIN")
  cp "$here/config.json" "$BATS_TEST_TMPDIR/prod.json"
  cp "$here/mappings.json" "$BATS_TEST_TMPDIR/other.json"
  tmp=$(cd "$BATS_TEST_TMPDIR" && pwd)
  # Relative paths are taken from where the script is run.
  cd "$BATS_TEST_TMPDIR"
  ODB_CONFIG=prod.json ODB_MAPPINGS=other.json run "$SCRIPT_PLAIN" preflight
  [ "$status" -eq 0 ]
  grep -q "path run preflight --mappings $tmp/other.json --config $tmp/prod.json" "$STUB_LOG"
}

@test "OC-159 AC2: a missing mappings.json or config.json stops with exit 4" {
  with_on_path odb-path odb
  script=$(copy_export)
  rm "$(dirname "$script")/mappings.json"
  run "$script" run
  [ "$status" -eq 4 ]
  [[ "$output" == *"mappings.json not found: it holds the mappings to run"* ]]
  ODB_CONFIG="$BATS_TEST_TMPDIR/none.json" run "$SCRIPT_PLAIN" run
  [ "$status" -eq 4 ]
  [[ "$output" == *"none.json not found"* ]]
  [ ! -s "$STUB_LOG" ]
}

@test "inspect shows what mappings.json migrates" {
  with_on_path odb-path odb
  here=$(folder_of "$SCRIPT_PLAIN")
  run "$SCRIPT_PLAIN" inspect
  [ "$status" -eq 0 ]
  grep -q "path bundle inspect $here/mappings.json" "$STUB_LOG"
}

@test "nothing is left in TMPDIR" {
  with_on_path odb-path odb
  run "$SCRIPT_PLAIN" run
  [ "$status" -eq 0 ]
  [ -z "$(ls -A "$TMPDIR")" ]
}
