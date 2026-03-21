#!/usr/bin/env bats

load test_helper

setup_cli_project() {
  local dir="$1"
  make_git_repo "$dir" "git@github.com:owner/repo.git"
}

@test "runoq run passes through issue and dry-run flags and exports context" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
  export RUNOQ_CLAUDE_BIN="claude"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export FAKE_CLAUDE_ENV_LOG="$TEST_TMPDIR/claude-env.log"
  export GH_TOKEN="existing-token"
  resolved_project_dir="$(cd "$project_dir" && pwd -P)"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42 --dry-run'

  [ "$status" -eq 0 ]
  run cat "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"--print --permission-mode bypassPermissions --agent github-orchestrator --add-dir $RUNOQ_ROOT -- "* ]]
  [[ "$output" == *'"command":"runoq run"'* ]]
  [[ "$output" == *'"issue":42'* ]]
  [[ "$output" == *'"dry_run":true'* ]]
  run cat "$FAKE_CLAUDE_ENV_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"PWD=$resolved_project_dir"* ]]
  [[ "$output" == *"TARGET_ROOT=$resolved_project_dir"* ]]
  [[ "$output" == *"REPO=owner/repo"* ]]
}

@test "runoq plan resolves the file to an absolute path" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  mkdir -p "$project_dir/docs"
  echo "# Plan" >"$project_dir/docs/plan.md"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
  export RUNOQ_CLAUDE_BIN="claude"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export GH_TOKEN="existing-token"
  resolved_project_dir="$(cd "$project_dir" && pwd -P)"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" plan docs/plan.md'

  [ "$status" -eq 0 ]
  run cat "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"--skill plan-to-issues --add-dir $RUNOQ_ROOT -- "*"/docs/plan.md" ]]
}

@test "runoq maintenance routes to the maintenance reviewer" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
  export RUNOQ_CLAUDE_BIN="claude"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export GH_TOKEN="existing-token"
  resolved_project_dir="$(cd "$project_dir" && pwd -P)"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" maintenance'

  [ "$status" -eq 0 ]
  run cat "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"--agent maintenance-reviewer --add-dir $RUNOQ_ROOT"* ]]
}

@test "runoq report delegates to report.sh" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  mkdir -p "$project_dir/.runoq/state"
  cat >"$project_dir/.runoq/state/42.json" <<'EOF'
{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 10,
  "rounds": []
}
EOF

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" report summary'

  [ "$status" -eq 0 ]
  [[ "$output" == *'"issues": 1'* ]]
}

@test "runoq shows usage for unknown subcommands" {
  run "$RUNOQ_ROOT/bin/runoq" unknown

  [ "$status" -ne 0 ]
  [[ "$output" == *"Usage:"* ]]
}

@test "runoq fails outside a git repository" {
  outside="$TEST_TMPDIR/outside"
  mkdir -p "$outside"

  run bash -lc 'cd "'"$outside"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run'

  [ "$status" -ne 0 ]
  [[ "$output" == *"Run runoq from inside a git repository."* ]]
}

@test "runoq fails cleanly when the plan file is missing" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
  export RUNOQ_CLAUDE_BIN="claude"
  export GH_TOKEN="existing-token"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" plan docs/missing.md'

  [ "$status" -ne 0 ]
  [[ "$output" == *"Plan file not found"* ]]
}
