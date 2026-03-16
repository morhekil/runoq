#!/usr/bin/env bats

load test_helper

@test "state save and load preserve the breadcrumb and timestamps" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"

  run bash -lc '
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42
{
  "phase": "INIT",
  "branch": "agendev/42-test",
  "round": 0
}
EOF
  '
  [ "$status" -eq 0 ]
  [[ "$output" == *'"phase": "INIT"'* ]]
  [[ "$output" == *'"issue": 42'* ]]

  run "$AGENDEV_ROOT/scripts/state.sh" load 42
  [ "$status" -eq 0 ]
  [[ "$output" == *'"started_at"'* ]]
  [[ "$output" == *'"updated_at"'* ]]
}

@test "state save allows valid loop transitions" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"

  run bash -lc '
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42
{"phase":"INIT","round":0}
EOF
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"DEVELOP","round":1}
EOF
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42
{"phase":"REVIEW","round":1}
EOF
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *'"phase": "REVIEW"'* ]]
}

@test "state save rejects invalid transitions" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"

  run bash -lc '
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"INIT","round":0}
EOF
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42
{"phase":"REVIEW","round":1}
EOF
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"Invalid phase transition: INIT -> REVIEW"* ]]
}

@test "state save rejects transitions out of terminal phases" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"

  run bash -lc '
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"INIT","round":0}
EOF
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"FINALIZE","round":1}
EOF
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"DONE","round":1}
EOF
    cat <<EOF | "'"$AGENDEV_ROOT"'/scripts/state.sh" save 42
{"phase":"DEVELOP","round":2}
EOF
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"Invalid transition from terminal phase DONE to DEVELOP"* ]]
}

@test "state load fails for corrupted state files" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  printf '{bad json' >"$AGENDEV_STATE_DIR/42.json"

  run "$AGENDEV_ROOT/scripts/state.sh" load 42

  [ "$status" -ne 0 ]
  [[ "$output" == *"State file is corrupted for issue 42"* ]]
}

@test "processed mention state initializes cleanly and records ids atomically" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"

  run "$AGENDEV_ROOT/scripts/state.sh" has-mention 101
  [ "$status" -ne 0 ]
  [ "$output" = "false" ]

  run "$AGENDEV_ROOT/scripts/state.sh" record-mention 101
  [ "$status" -eq 0 ]
  [[ "$output" == *"101"* ]]

  run "$AGENDEV_ROOT/scripts/state.sh" has-mention 101
  [ "$status" -eq 0 ]
  [ "$output" = "true" ]
}

@test "processed mention state deduplicates ids across polling cycles" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"

  run bash -lc '
    "'"$AGENDEV_ROOT"'/scripts/state.sh" record-mention 101 >/dev/null
    "'"$AGENDEV_ROOT"'/scripts/state.sh" record-mention 101
  '

  [ "$status" -eq 0 ]
  run jq -r 'length' "$AGENDEV_STATE_DIR/processed-mentions.json"
  [ "$status" -eq 0 ]
  [ "$output" = "1" ]
}
