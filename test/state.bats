#!/usr/bin/env bats

load test_helper

@test "state save and load preserve the breadcrumb and timestamps" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"

  run bash -lc '
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42
{
  "phase": "INIT",
  "branch": "runoq/42-test",
  "round": 0
}
EOF
  '
  [ "$status" -eq 0 ]
  [[ "$output" == *'"phase": "INIT"'* ]]
  [[ "$output" == *'"issue": 42'* ]]

  run "$RUNOQ_ROOT/scripts/state.sh" load 42
  [ "$status" -eq 0 ]
  [[ "$output" == *'"started_at"'* ]]
  [[ "$output" == *'"updated_at"'* ]]
}

@test "state save allows valid loop transitions" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"

  run bash -lc '
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42
{"phase":"INIT","round":0}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"DEVELOP","round":1}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42
{"phase":"REVIEW","round":1}
EOF
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *'"phase": "REVIEW"'* ]]
}

@test "state save rejects invalid transitions" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"

  run bash -lc '
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"INIT","round":0}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42
{"phase":"REVIEW","round":1}
EOF
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"Invalid phase transition: INIT -> REVIEW"* ]]
}

@test "state save rejects transitions out of terminal phases" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"

  run bash -lc '
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"INIT","round":0}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"FINALIZE","round":1}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"DONE","round":1}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42
{"phase":"DEVELOP","round":2}
EOF
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"Invalid transition from terminal phase DONE to DEVELOP"* ]]
}

@test "state save allows CRITERIA and INTEGRATE phase transitions" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"

  # INIT → CRITERIA → DEVELOP path
  run bash -lc '
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42
{"phase":"INIT","round":0}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"CRITERIA","round":0}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42
{"phase":"DEVELOP","round":1}
EOF
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *'"phase": "DEVELOP"'* ]]

  # Test INTEGRATE path: DECIDE → INTEGRATE → DONE
  run bash -lc '
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 99
{"phase":"INIT","round":0}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 99 >/dev/null
{"phase":"DEVELOP","round":1}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 99 >/dev/null
{"phase":"REVIEW","round":1}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 99 >/dev/null
{"phase":"DECIDE","round":1}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 99 >/dev/null
{"phase":"INTEGRATE","round":1}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 99
{"phase":"DONE","round":1}
EOF
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *'"phase": "DONE"'* ]]
}

@test "state save rejects invalid CRITERIA transitions" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"

  # DEVELOP → CRITERIA should be invalid
  run bash -lc '
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"INIT","round":0}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42 >/dev/null
{"phase":"DEVELOP","round":1}
EOF
    cat <<EOF | "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42
{"phase":"CRITERIA","round":1}
EOF
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"Invalid phase transition: DEVELOP -> CRITERIA"* ]]
}

@test "state load fails for corrupted state files" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"
  printf '{bad json' >"$RUNOQ_STATE_DIR/42.json"

  run "$RUNOQ_ROOT/scripts/state.sh" load 42

  [ "$status" -ne 0 ]
  [[ "$output" == *"State file is corrupted for issue 42"* ]]
}

@test "processed mention state initializes cleanly and records ids atomically" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"

  run "$RUNOQ_ROOT/scripts/state.sh" has-mention 101
  [ "$status" -ne 0 ]
  [ "$output" = "false" ]

  run "$RUNOQ_ROOT/scripts/state.sh" record-mention 101
  [ "$status" -eq 0 ]
  [[ "$output" == *"101"* ]]

  run "$RUNOQ_ROOT/scripts/state.sh" has-mention 101
  [ "$status" -eq 0 ]
  [ "$output" = "true" ]
}

@test "processed mention state deduplicates ids across polling cycles" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"

  run bash -lc '
    "'"$RUNOQ_ROOT"'/scripts/state.sh" record-mention 101 >/dev/null
    "'"$RUNOQ_ROOT"'/scripts/state.sh" record-mention 101
  '

  [ "$status" -eq 0 ]
  run jq -r 'length' "$RUNOQ_STATE_DIR/processed-mentions.json"
  [ "$status" -eq 0 ]
  [ "$output" = "1" ]
}
