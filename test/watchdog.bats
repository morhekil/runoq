#!/usr/bin/env bats

load test_helper

write_watchdog_state() {
  local issue="$1"
  cat <<EOF | "$RUNOQ_ROOT/scripts/state.sh" save "$issue" >/dev/null
{
  "phase": "DEVELOP",
  "round": 2,
  "branch": "runoq/$issue-test"
}
EOF
}

@test "watchdog allows commands with continued output activity" {
  run "$RUNOQ_ROOT/scripts/watchdog.sh" --timeout 2 -- bash -lc '
    printf "tick-1\n"
    sleep 1
    printf "tick-2\n"
    sleep 1
    printf "done\n"
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *"tick-1"* ]]
  [[ "$output" == *"tick-2"* ]]
  [[ "$output" == *"done"* ]]
}

@test "watchdog terminates silent commands and writes a stall marker to state" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"
  write_watchdog_state 42

  run "$RUNOQ_ROOT/scripts/watchdog.sh" --timeout 1 --issue 42 -- bash -lc 'sleep 2'

  [ "$status" -eq 124 ]
  [[ "$output" == *"stalled after 1s of inactivity"* ]]

  run jq -r '.stall.timed_out' "$RUNOQ_STATE_DIR/42.json"
  [ "$status" -eq 0 ]
  [ "$output" = "true" ]

  run jq -r '.stall.timeout_seconds' "$RUNOQ_STATE_DIR/42.json"
  [ "$status" -eq 0 ]
  [ "$output" = "1" ]

  run jq -r '.stall.exit_code' "$RUNOQ_STATE_DIR/42.json"
  [ "$status" -eq 0 ]
  [ "$output" = "124" ]
}

@test "watchdog passes through non-timeout exit codes without writing stall markers" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"
  write_watchdog_state 42

  run "$RUNOQ_ROOT/scripts/watchdog.sh" --timeout 5 --issue 42 -- bash -lc 'echo "before-exit"; exit 7'

  [ "$status" -eq 7 ]
  [[ "$output" == *"before-exit"* ]]

  run jq -r 'has("stall")' "$RUNOQ_STATE_DIR/42.json"
  [ "$status" -eq 0 ]
  [ "$output" = "false" ]
}
