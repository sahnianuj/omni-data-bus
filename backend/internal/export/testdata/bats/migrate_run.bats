#!/usr/bin/env bats
# OC-053 AC1, AC6: running migrations and passing exit codes through.

load helpers

@test "AC6: exit codes propagate" {
  with_on_path odb-path odb
  for code in 0 2 3 4 5; do
    STUB_EXIT=$code run "$SCRIPT_PLAIN" run
    [ "$status" -eq "$code" ]
  done
  STUB_EXIT=5 run "$SCRIPT_PLAIN" run
  [[ "$output" == *"cancelled after saving the checkpoint"* ]]
  STUB_EXIT=2 run "$SCRIPT_PLAIN" run
  [[ "$output" == *"completed with errors"* ]]
}

@test "commands pass their options to odb run" {
  with_on_path odb-path odb
  run "$SCRIPT_PLAIN" run --mapping orders -- --throttle 5000
  [ "$status" -eq 0 ]
  grep -q "path run --mapping orders --throttle 5000 --bundle .*bundle.odb.json --state-dir $ODB_STATE_DIR --target-path $ODB_TARGET_PATH" "$STUB_LOG"
  run "$SCRIPT_PLAIN" resume
  grep -q "path run --resume" "$STUB_LOG"
  run "$SCRIPT_PLAIN" preflight
  grep -q "path run preflight" "$STUB_LOG"
  run "$SCRIPT_PLAIN" status
  grep -q "path run status --state-dir $ODB_STATE_DIR" "$STUB_LOG"
  run "$SCRIPT_PLAIN" nonsense
  [ "$status" -eq 4 ]
}

@test "config.json next to the script is passed to odb" {
  local here
  here=$(cd "$(dirname "$SCRIPT_CONFIG")" && pwd)
  with_on_path odb-path odb
  run "$SCRIPT_CONFIG" run
  [ "$status" -eq 0 ]
  grep -q "path run --bundle .*bundle.odb.json --state-dir $ODB_STATE_DIR --config $here/config.json --target-path" "$STUB_LOG"
  # ODB_CONFIG points at another file, e.g. one per environment.
  cp "$here/config.json" "$BATS_TEST_TMPDIR/prod.json"
  ODB_CONFIG="$BATS_TEST_TMPDIR/prod.json" run "$SCRIPT_CONFIG" preflight
  [ "$status" -eq 0 ]
  grep -q "path run preflight --bundle .* --config $(cd "$BATS_TEST_TMPDIR" && pwd)/prod.json" "$STUB_LOG"
  # Docker mounts the file's folder (odb may save it back) and passes it there.
  with_on_path docker docker
  ODB_RUNTIME=docker ODB_CONFIG="$BATS_TEST_TMPDIR/prod.json" run "$SCRIPT_CONFIG" run
  [ "$status" -eq 0 ]
  grep -q -- "-v $(cd "$BATS_TEST_TMPDIR" && pwd):/config .* --config /config/prod.json" "$STUB_LOG"
  # A missing config: a configuration error before odb runs.
  : >"$STUB_LOG"
  ODB_CONFIG="$BATS_TEST_TMPDIR/none.json" run "$SCRIPT_CONFIG" run
  [ "$status" -eq 4 ]
  [[ "$output" == *"none.json not found"* ]]
  [ ! -s "$STUB_LOG" ]
}

@test "AC1: run end to end (binary runtime)" {
  [ -n "${IT_SCRIPT:-}" ] || skip "set OC_IT_PG_HOST/PORT (the dev stack) for the end-to-end run"
  export PATH="$PATH:$(dirname "$IT_ODB")"
  export ODB_BUNDLE_PASSPHRASE="$IT_PASSPHRASE"
  ODB_BIN="$IT_ODB" run "$IT_SCRIPT" run
  echo "$output" | tail -n 5
  [ "$status" -eq 0 ]
  [ "$(ls "$ODB_TARGET_PATH/orders" | wc -l | tr -d ' ')" -eq "$IT_ROWS" ]
}

@test "AC1: run end to end (docker runtime)" {
  [ -n "${IT_SCRIPT_DOCKER:-}" ] || skip "set ODB_IT_IMAGE (an image of this build) and the dev stack for the docker run"
  export PATH="$PATH:$(dirname "$IT_DOCKER")"
  export ODB_BUNDLE_PASSPHRASE="$IT_PASSPHRASE"
  ODB_RUNTIME=docker ODB_DOCKER_ARGS="${IT_DOCKER_ARGS:---network host}" run "$IT_SCRIPT_DOCKER" run
  echo "$output" | tail -n 5
  [ "$status" -eq 0 ]
  [ "$(ls "$ODB_TARGET_PATH/orders" | wc -l | tr -d ' ')" -eq "$IT_ROWS" ]
}
