#!/usr/bin/env bats

load test_helper

setup_cli_project() {
  local dir="$1"
  make_git_repo "$dir" "git@github.com:owner/repo.git"
}

@test "agendev run passes through issue and dry-run flags and exports context" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  export PATH="$AGENDEV_ROOT/test/helpers:$PATH"
  export AGENDEV_CLAUDE_BIN="claude"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export FAKE_CLAUDE_ENV_LOG="$TEST_TMPDIR/claude-env.log"
  export GH_TOKEN="existing-token"

  run bash -lc 'cd "'"$project_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run --issue 42 --dry-run'

  [ "$status" -eq 0 ]
  run cat "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"--agent github-orchestrator --add-dir $AGENDEV_ROOT -- --issue 42 --dry-run"* ]]
  run cat "$FAKE_CLAUDE_ENV_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"TARGET_ROOT=$resolved_project_dir"* ]]
  [[ "$output" == *"REPO=owner/repo"* ]]
}

@test "agendev plan resolves the file to an absolute path" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  mkdir -p "$project_dir/docs"
  echo "# Plan" >"$project_dir/docs/plan.md"
  export PATH="$AGENDEV_ROOT/test/helpers:$PATH"
  export AGENDEV_CLAUDE_BIN="claude"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export GH_TOKEN="existing-token"
  resolved_project_dir="$(cd "$project_dir" && pwd -P)"

  run bash -lc 'cd "'"$project_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" plan docs/plan.md'

  [ "$status" -eq 0 ]
  run cat "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"--skill plan-to-issues --add-dir $AGENDEV_ROOT -- "*"/docs/plan.md" ]]
}

@test "agendev maintenance routes to the maintenance reviewer" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  export PATH="$AGENDEV_ROOT/test/helpers:$PATH"
  export AGENDEV_CLAUDE_BIN="claude"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export GH_TOKEN="existing-token"

  run bash -lc 'cd "'"$project_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" maintenance'

  [ "$status" -eq 0 ]
  run cat "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"--agent maintenance-reviewer --add-dir $AGENDEV_ROOT"* ]]
}

@test "agendev report delegates to report.sh" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  mkdir -p "$project_dir/.agendev/state"
  cat >"$project_dir/.agendev/state/42.json" <<'EOF'
{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 10,
  "rounds": []
}
EOF

  run bash -lc 'cd "'"$project_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" report summary'

  [ "$status" -eq 0 ]
  [[ "$output" == *'"issues": 1'* ]]
}

@test "agendev shows usage for unknown subcommands" {
  run "$AGENDEV_ROOT/bin/agendev" unknown

  [ "$status" -ne 0 ]
  [[ "$output" == *"Usage:"* ]]
}

@test "agendev fails outside a git repository" {
  outside="$TEST_TMPDIR/outside"
  mkdir -p "$outside"

  run bash -lc 'cd "'"$outside"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run'

  [ "$status" -ne 0 ]
  [[ "$output" == *"Run agendev from inside a git repository."* ]]
}

@test "agendev fails cleanly when the plan file is missing" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  export PATH="$AGENDEV_ROOT/test/helpers:$PATH"
  export AGENDEV_CLAUDE_BIN="claude"
  export GH_TOKEN="existing-token"

  run bash -lc 'cd "'"$project_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" plan docs/missing.md'

  [ "$status" -ne 0 ]
  [[ "$output" == *"Plan file not found"* ]]
}
