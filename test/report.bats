#!/usr/bin/env bats

load test_helper

@test "report summary aggregates completed state files" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  cat >"$AGENDEV_STATE_DIR/42.json" <<'EOF'
{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 100,
  "rounds": [
    { "tokens": { "input": 50, "cached_input": 10, "output": 40 } }
  ]
}
EOF
  cat >"$AGENDEV_STATE_DIR/43.json" <<'EOF'
{
  "phase": "FAILED",
  "outcome": { "verdict": "FAIL" },
  "tokens_used": 200,
  "rounds": [
    { "tokens": { "input": 100, "cached_input": 20, "output": 80 } }
  ]
}
EOF

  run "$AGENDEV_ROOT/scripts/report.sh" summary

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.issues')" = "2" ]
  [ "$(printf '%s' "$output" | jq -r '.pass')" = "1" ]
  [ "$(printf '%s' "$output" | jq -r '.fail')" = "1" ]
  [ "$(printf '%s' "$output" | jq -r '.tokens.total')" = "300" ]
}

@test "report issue returns the stored state for a specific issue" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  cat >"$AGENDEV_STATE_DIR/42.json" <<'EOF'
{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 100
}
EOF

  run "$AGENDEV_ROOT/scripts/report.sh" issue 42

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.phase')" = "DONE" ]
}

@test "report cost uses config-driven rates" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  cat >"$AGENDEV_STATE_DIR/42.json" <<'EOF'
{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 100,
  "rounds": [
    { "tokens": { "input": 1000000, "cached_input": 0, "output": 500000 } }
  ]
}
EOF
  cat >"$TEST_TMPDIR/config.json" <<'EOF'
{
  "labels": {
    "ready": "agendev:ready",
    "inProgress": "agendev:in-progress",
    "done": "agendev:done",
    "needsReview": "agendev:needs-human-review",
    "blocked": "agendev:blocked",
    "maintenanceReview": "agendev:maintenance-review"
  },
  "identity": {
    "appSlug": "agendev",
    "handle": "agendev"
  },
  "authorization": {
    "minimumPermission": "write",
    "denyResponse": "comment"
  },
  "maxRounds": 5,
  "maxTokenBudget": 500000,
  "tokenCost": {
    "inputPerMillion": 1,
    "cachedInputPerMillion": 0,
    "outputPerMillion": 2
  },
  "autoMerge": {
    "enabled": true,
    "requireVerification": true,
    "requireZeroCritical": true,
    "maxComplexity": "low"
  },
  "reviewers": ["username"],
  "branchPrefix": "agendev/",
  "worktreePrefix": "agendev-wt-",
  "consecutiveFailureLimit": 3,
  "verification": {
    "testCommand": "true",
    "buildCommand": "true"
  },
  "stall": {
    "timeoutSeconds": 600
  }
}
EOF
  export AGENDEV_CONFIG="$TEST_TMPDIR/config.json"

  run "$AGENDEV_ROOT/scripts/report.sh" cost

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.estimated_cost')" = "2" ]
}

@test "report summary handles an empty state directory" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export AGENDEV_STATE_DIR="$TARGET_ROOT/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"

  run "$AGENDEV_ROOT/scripts/report.sh" summary

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.issues')" = "0" ]
}
