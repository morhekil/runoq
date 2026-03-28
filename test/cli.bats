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
  export RUNOQ_ORCHESTRATOR_BIN="$RUNOQ_ROOT/test/helpers/orchestrator.sh"
  export FAKE_ORCHESTRATOR_LOG="$TEST_TMPDIR/orchestrator.log"
  export FAKE_ORCHESTRATOR_ENV_LOG="$TEST_TMPDIR/orchestrator-env.log"
  export GH_TOKEN="existing-token"
  resolved_project_dir="$(cd "$project_dir" && pwd -P)"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42 --dry-run'

  [ "$status" -eq 0 ]
  run cat "$FAKE_ORCHESTRATOR_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"run owner/repo --issue 42 --dry-run"* ]]
  run cat "$FAKE_ORCHESTRATOR_ENV_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"PWD=$resolved_project_dir"* ]]
  [[ "$output" == *"TARGET_ROOT=$resolved_project_dir"* ]]
  [[ "$output" == *"REPO=owner/repo"* ]]
}

@test "runoq plan invokes plan.sh with repo and absolute plan path" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  mkdir -p "$project_dir/docs"
  echo "# Plan" >"$project_dir/docs/plan.md"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
  export RUNOQ_CLAUDE_BIN="claude"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export GH_TOKEN="existing-token"
  resolved_project_dir="$(cd "$project_dir" && pwd -P)"

  # Fake claude must return a valid plan-decomposer payload so plan.sh can parse it
  local fake_claude_script="$TEST_TMPDIR/fake-claude"
  cat >"$fake_claude_script" <<'FAKECLAUDE'
#!/usr/bin/env bash
set -euo pipefail
if [[ -n "${FAKE_CLAUDE_LOG:-}" ]]; then
  printf '%s\n' "$*" >>"$FAKE_CLAUDE_LOG"
fi
cat <<'PAYLOAD'
<!-- runoq:payload:plan-decomposer -->
```json
{"items":[],"warnings":[]}
```
PAYLOAD
FAKECLAUDE
  chmod +x "$fake_claude_script"
  export RUNOQ_CLAUDE_BIN="$fake_claude_script"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" plan docs/plan.md --dry-run'

  [ "$status" -eq 0 ]
  run cat "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"--agent plan-decomposer --add-dir $RUNOQ_ROOT"* ]]
  # Verify the payload contains an absolute path to the plan file (not a relative one)
  [[ "$output" == *'"planPath": "/'* ]]
  [[ "$output" == *"/docs/plan.md"* ]]
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
