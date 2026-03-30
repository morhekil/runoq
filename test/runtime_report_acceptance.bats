#!/usr/bin/env bats

load test_helper

setup_acceptance_project() {
  local dir="$1"
  if [[ -x "/opt/homebrew/bin/bash" ]]; then
    export PATH="/opt/homebrew/bin:$PATH"
  fi
  make_git_repo "$dir" "git@github.com:owner/repo.git"
  prepare_runtime_bin
  export GH_TOKEN="existing-token"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
}

write_report_config() {
  local path="$1"
  cat >"$path" <<'EOF'
{
  "labels": {
    "ready": "runoq:ready",
    "inProgress": "runoq:in-progress",
    "done": "runoq:done",
    "needsReview": "runoq:needs-human-review",
    "blocked": "runoq:blocked",
    "maintenanceReview": "runoq:maintenance-review"
  },
  "identity": {
    "appSlug": "runoq",
    "handle": "runoq"
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
  "branchPrefix": "runoq/",
  "worktreePrefix": "runoq-wt-",
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
}

@test "acceptance parity: report summary matches shell and runtime output contract" {
  project_dir="$TEST_TMPDIR/project"
  setup_acceptance_project "$project_dir"
  mkdir -p "$project_dir/.runoq/state"
  cat >"$project_dir/.runoq/state/42.json" <<'EOF'
{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 100,
  "rounds": [
    { "tokens": { "input": 50, "cached_input": 10, "output": 40 } }
  ]
}
EOF

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/bin/runoq" report summary 2>"'"$TEST_TMPDIR"'/shell-summary.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/bin/runoq" report summary 2>"'"$TEST_TMPDIR"'/runtime-summary.err"'
  runtime_status="$status"
  runtime_output="$output"

  run cat "$TEST_TMPDIR/shell-summary.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime-summary.err"
  runtime_err="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  shell_norm="$(printf '%s' "$shell_output" | jq -S -c .)"
  runtime_norm="$(printf '%s' "$runtime_output" | jq -S -c .)"
  [ "$shell_norm" = "$runtime_norm" ]
  [ "$shell_err" = "$runtime_err" ]
}

@test "acceptance parity: report issue missing file matches shell and runtime" {
  project_dir="$TEST_TMPDIR/project"
  setup_acceptance_project "$project_dir"
  mkdir -p "$project_dir/.runoq/state"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/bin/runoq" report issue 999 2>"'"$TEST_TMPDIR"'/shell-issue.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/bin/runoq" report issue 999 2>"'"$TEST_TMPDIR"'/runtime-issue.err"'
  runtime_status="$status"
  runtime_output="$output"

  run cat "$TEST_TMPDIR/shell-issue.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime-issue.err"
  runtime_err="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -ne 0 ]
  [ "$shell_output" = "$runtime_output" ]
  [ "$shell_err" = "$runtime_err" ]
}

@test "acceptance parity: report cost matches shell and runtime output contract" {
  project_dir="$TEST_TMPDIR/project"
  setup_acceptance_project "$project_dir"
  mkdir -p "$project_dir/.runoq/state"
  config_path="$TEST_TMPDIR/config.json"
  write_report_config "$config_path"
  cat >"$project_dir/.runoq/state/42.json" <<'EOF'
{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 100,
  "rounds": [
    { "tokens": { "input": 1000000, "cached_input": 0, "output": 500000 } }
  ]
}
EOF

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_CONFIG="'"$config_path"'" RUNOQ_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/bin/runoq" report cost 2>"'"$TEST_TMPDIR"'/shell-cost.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_CONFIG="'"$config_path"'" RUNOQ_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/bin/runoq" report cost 2>"'"$TEST_TMPDIR"'/runtime-cost.err"'
  runtime_status="$status"
  runtime_output="$output"

  run cat "$TEST_TMPDIR/shell-cost.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime-cost.err"
  runtime_err="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  shell_norm="$(printf '%s' "$shell_output" | jq -S -c .)"
  runtime_norm="$(printf '%s' "$runtime_output" | jq -S -c .)"
  [ "$shell_norm" = "$runtime_norm" ]
  [ "$shell_err" = "$runtime_err" ]
}
