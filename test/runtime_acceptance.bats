#!/usr/bin/env bats

load test_helper

write_fake_runtime_cli_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'FAKE_RUNTIME:%s %s\n' "$1" "${*:2}"
EOF
  chmod +x "$path"
}

write_fake_go_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'FAKE_GO_CWD:%s\n' "$PWD"
printf 'FAKE_GO_ARGS:%s\n' "$*"
EOF
  chmod +x "$path"
}

setup_acceptance_project() {
  local dir="$1"
  make_git_repo "$dir" "git@github.com:owner/repo.git"
  prepare_runtime_bin
  export GH_TOKEN="existing-token"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
}

write_empty_queue_scenario() {
  local path="$1"
  write_fake_gh_scenario "$path" <<'EOF'
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": "[]"
  }
]
EOF
}

normalize_runtime_stderr() {
  printf '%s' "$1" | sed -E \
    -e 's/milestone-decomposer-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}-[0-9]+/milestone-decomposer-NORMALIZED/g' \
    -e 's/task-decomposer-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}-[0-9]+/task-decomposer-NORMALIZED/g'
}

@test "CLI wrapper defaults to runtime, accepts explicit runtime, and rejects shell override" {
  project_dir="$TEST_TMPDIR/default-cli-project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"

  fake_runtime_bin="$TEST_TMPDIR/fake-runtime-cli"
  write_fake_runtime_cli_bin "$fake_runtime_bin"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/bin/runoq" help'
  [ "$status" -eq 0 ]
  [ "$output" = "FAKE_RUNTIME:help " ]

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=runtime RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/bin/runoq" help'
  [ "$status" -eq 0 ]
  [ "$output" = "FAKE_RUNTIME:help " ]

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=shell RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/bin/runoq" help'
  [ "$status" -ne 0 ]
  [[ "$output" == *"Unknown RUNOQ_IMPLEMENTATION: shell (expected runtime)"* ]]
}

@test "CLI wrapper go fallback runs from RUNOQ_ROOT when runtime bin is unset" {
  project_dir="$TEST_TMPDIR/default-cli-go-cwd-project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"

  fake_go_bin="$TEST_TMPDIR/fake-go-cli"
  write_fake_go_bin "$fake_go_bin"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_RUNTIME_BIN="" RUNOQ_GO_BIN="'"$fake_go_bin"'" "'"$RUNOQ_ROOT"'/bin/runoq" help'
  [ "$status" -eq 0 ]
  [[ "$output" == *"FAKE_GO_CWD:$RUNOQ_ROOT"* ]]
  [[ "$output" == *"FAKE_GO_ARGS:run $RUNOQ_ROOT/cmd/runoq-runtime help"* ]]
}

@test "acceptance contract: run --dry-run queue mode matches default and explicit runtime" {
  project_dir="$TEST_TMPDIR/project"
  setup_acceptance_project "$project_dir"

  default_scenario="$TEST_TMPDIR/default-scenario.json"
  write_empty_queue_scenario "$default_scenario"
  use_fake_gh "$default_scenario" "$TEST_TMPDIR/default-gh.state"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --dry-run 2>"'"$TEST_TMPDIR"'/default-run.err"'
  default_status="$status"
  default_output="$output"

  runtime_scenario="$TEST_TMPDIR/runtime-scenario.json"
  write_empty_queue_scenario "$runtime_scenario"
  use_fake_gh "$runtime_scenario" "$TEST_TMPDIR/runtime-gh.state"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/bin/runoq" run --dry-run 2>"'"$TEST_TMPDIR"'/runtime-run.err"'
  runtime_status="$status"
  runtime_output="$output"
  run cat "$TEST_TMPDIR/default-run.err"
  default_err="$(normalize_runtime_stderr "$output")"
  run cat "$TEST_TMPDIR/runtime-run.err"
  runtime_err="$(normalize_runtime_stderr "$output")"

  [ "$default_status" -eq "$runtime_status" ]
  shell_norm="$(printf '%s' "$default_output" | jq -S -c .)"
  runtime_norm="$(printf '%s' "$runtime_output" | jq -S -c .)"
  [ "$shell_norm" = "$runtime_norm" ]
  [ "$default_err" = "$runtime_err" ]
}

@test "acceptance contract: plan --dry-run matches default and explicit runtime output" {
  project_dir="$TEST_TMPDIR/project"
  setup_acceptance_project "$project_dir"
  mkdir -p "$project_dir/docs"
  echo "# Plan" >"$project_dir/docs/plan.md"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/runtime-plan-claude"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" plan docs/plan.md --dry-run 2>"'"$TEST_TMPDIR"'/default-plan.err"'
  default_status="$status"
  default_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/bin/runoq" plan docs/plan.md --dry-run 2>"'"$TEST_TMPDIR"'/runtime-plan.err"'
  runtime_status="$status"
  runtime_output="$output"
  run cat "$TEST_TMPDIR/default-plan.err"
  default_err="$(normalize_runtime_stderr "$output")"
  run cat "$TEST_TMPDIR/runtime-plan.err"
  runtime_err="$(normalize_runtime_stderr "$output")"

  [ "$default_status" -eq "$runtime_status" ]
  shell_norm="$(printf '%s' "$default_output" | jq -S -c .)"
  runtime_norm="$(printf '%s' "$runtime_output" | jq -S -c .)"
  [ "$shell_norm" = "$runtime_norm" ]
  [ "$default_err" = "$runtime_err" ]
}
