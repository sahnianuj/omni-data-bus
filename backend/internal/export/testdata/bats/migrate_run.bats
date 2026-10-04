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
