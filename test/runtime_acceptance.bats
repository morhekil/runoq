#!/usr/bin/env bats

load test_helper

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

@test "acceptance parity: run --dry-run queue mode matches shell and runtime" {
  project_dir="$TEST_TMPDIR/project"
  setup_acceptance_project "$project_dir"

  shell_scenario="$TEST_TMPDIR/shell-scenario.json"
  write_empty_queue_scenario "$shell_scenario"
  use_fake_gh "$shell_scenario" "$TEST_TMPDIR/shell-gh.state"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/bin/runoq" run --dry-run 2>"'"$TEST_TMPDIR"'/shell-run.err"'
  shell_status="$status"
  shell_output="$output"

  runtime_scenario="$TEST_TMPDIR/runtime-scenario.json"
  write_empty_queue_scenario "$runtime_scenario"
  use_fake_gh "$runtime_scenario" "$TEST_TMPDIR/runtime-gh.state"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/bin/runoq" run --dry-run 2>"'"$TEST_TMPDIR"'/runtime-run.err"'
  runtime_status="$status"
  runtime_output="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  shell_norm="$(printf '%s' "$shell_output" | jq -S -c .)"
  runtime_norm="$(printf '%s' "$runtime_output" | jq -S -c .)"
  [ "$shell_norm" = "$runtime_norm" ]
}

@test "acceptance parity: plan --dry-run matches shell and runtime output contract" {
  project_dir="$TEST_TMPDIR/project"
  setup_acceptance_project "$project_dir"
  mkdir -p "$project_dir/docs"
  echo "# Plan" >"$project_dir/docs/plan.md"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/runtime-plan-claude"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/bin/runoq" plan docs/plan.md --dry-run 2>"'"$TEST_TMPDIR"'/shell-plan.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/bin/runoq" plan docs/plan.md --dry-run 2>"'"$TEST_TMPDIR"'/runtime-plan.err"'
  runtime_status="$status"
  runtime_output="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  shell_norm="$(printf '%s' "$shell_output" | jq -S -c .)"
  runtime_norm="$(printf '%s' "$runtime_output" | jq -S -c .)"
  [ "$shell_norm" = "$runtime_norm" ]
}
