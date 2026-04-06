#!/usr/bin/env bats

load test_helper

setup_cli_project() {
  local dir="$1"
  make_git_repo "$dir" "git@github.com:owner/repo.git"
  prepare_runtime_bin
}

write_empty_key() {
  local key_path="$1"
  openssl genrsa -out "$key_path" 2048 >/dev/null 2>&1
}

@test "runoq init preserves the caller cwd and bootstraps the target repo" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
  export RUNOQ_SYMLINK_DIR="$TEST_TMPDIR/bin"
  export RUNOQ_APP_KEY="$TEST_TMPDIR/app-key.pem"
  export RUNOQ_APP_ID="123"
  write_empty_key "$RUNOQ_APP_KEY"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "/repos/owner/repo/installation"],
    "stdout": "{\"id\":789,\"app_id\":123,\"app_slug\":\"runoq\"}"
  },
  {
    "contains": ["label", "list", "--repo owner/repo"],
    "stdout": "[]"
  },
  {
    "contains": ["label", "create", "runoq:ready", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:in-progress", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:done", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:needs-human-review", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:blocked", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:plan-approved", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:maintenance-review", "--repo owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" init'

  [ "$status" -eq 0 ]
  [ -d "$project_dir/.runoq/state" ]
  [ -f "$project_dir/.runoq/identity.json" ]
  [ -f "$project_dir/package.json" ]
  [ -L "$project_dir/.claude/agents/github-orchestrator.md" ]
  [ -L "$project_dir/.claude/skills/plan-to-issues/SKILL.md" ]
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

@test "runoq runtime implementation routes run flags through runtime cli" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  prepare_runtime_bin
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
  export RUNOQ_IMPLEMENTATION="runtime"
  export RUNOQ_ORCHESTRATOR_BIN="$RUNOQ_ROOT/test/helpers/runtime-orchestrator"
  export FAKE_RUNTIME_ORCHESTRATOR_LOG="$TEST_TMPDIR/runtime-orchestrator.log"
  export FAKE_RUNTIME_ORCHESTRATOR_ENV_LOG="$TEST_TMPDIR/runtime-orchestrator-env.log"
  export GH_TOKEN="existing-token"
  resolved_project_dir="$(cd "$project_dir" && pwd -P)"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42 --dry-run'

  [ "$status" -eq 0 ]
  run cat "$FAKE_RUNTIME_ORCHESTRATOR_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"run owner/repo --issue 42 --dry-run"* ]]
  run cat "$FAKE_RUNTIME_ORCHESTRATOR_ENV_LOG"
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
cat <<'EOF'
{"type":"assistant","message":{"content":[{"type":"text","text":"<!-- runoq:payload:plan-decomposer -->\n```json\n{\"items\":[],\"warnings\":[]}\n```"}]}}
{"type":"result","result":"<!-- runoq:payload:plan-decomposer -->\n```json\n{\"items\":[],\"warnings\":[]}\n```"}
EOF
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
  run bash -lc 'cd "'"$project_dir"'" && find log/claude -mindepth 1 -maxdepth 1 -type d | head -n 1'
  [ "$status" -eq 0 ]
  capture_dir="$output"
  [ -n "$capture_dir" ]
  [ -f "$project_dir/$capture_dir/request.txt" ]
  [ -f "$project_dir/$capture_dir/stdout.log" ]
  [ -f "$project_dir/$capture_dir/stderr.log" ]
  [ -f "$project_dir/$capture_dir/response.txt" ]
  run grep -F -- '"planPath": "' "$project_dir/$capture_dir/request.txt"
  [ "$status" -eq 0 ]
  run grep -F -- 'runoq:payload:plan-decomposer' "$project_dir/$capture_dir/response.txt"
  [ "$status" -eq 0 ]
}

@test "runoq plan uses target runoq.json when the file argument is omitted" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  mkdir -p "$project_dir/docs"
  echo "# Plan" >"$project_dir/docs/plan.md"
  cat >"$project_dir/runoq.json" <<'EOF'
{
  "plan": "docs/plan.md"
}
EOF
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
  export GH_TOKEN="existing-token"

  local fake_claude_script="$TEST_TMPDIR/fake-claude-config"
  cat >"$fake_claude_script" <<'FAKECLAUDE'
#!/usr/bin/env bash
set -euo pipefail
cat <<'EOF'
{"type":"assistant","message":{"content":[{"type":"text","text":"<!-- runoq:payload:plan-decomposer -->\n```json\n{\"items\":[],\"warnings\":[]}\n```"}]}}
{"type":"result","result":"<!-- runoq:payload:plan-decomposer -->\n```json\n{\"items\":[],\"warnings\":[]}\n```"}
EOF
FAKECLAUDE
  chmod +x "$fake_claude_script"
  export RUNOQ_CLAUDE_BIN="$fake_claude_script"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" plan --dry-run'

  [ "$status" -eq 0 ]
  [[ "$output" == *'"items"'* ]]
  run bash -lc 'cd "'"$project_dir"'" && find log/claude -mindepth 1 -maxdepth 1 -type d | head -n 1'
  [ "$status" -eq 0 ]
  capture_dir="$output"
  run grep -F -- '"planPath": "' "$project_dir/$capture_dir/request.txt"
  [ "$status" -eq 0 ]
  [[ "$output" == *"/docs/plan.md"* ]]
}

@test "runoq plan dry-run uses assistant payload when stream result is only done" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  mkdir -p "$project_dir/docs"
  echo "# Plan" >"$project_dir/docs/plan.md"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
  export GH_TOKEN="existing-token"

  local fake_claude_script="$TEST_TMPDIR/fake-claude-stream"
  cat >"$fake_claude_script" <<'FAKECLAUDE'
#!/usr/bin/env bash
set -euo pipefail
cat <<'EOF'
{"type":"assistant","message":{"content":[{"type":"text","text":"<!-- runoq:payload:plan-decomposer -->\n```json\n{\"items\":[{\"key\":\"task-1\",\"type\":\"task\",\"title\":\"Ship task\",\"body\":\"Body\",\"priority\":1,\"estimated_complexity\":\"medium\",\"complexity_rationale\":\"Touches multiple steps.\",\"depends_on_keys\":[]}],\"warnings\":[]}\n```"}]}}
{"type":"result","result":"done"}
EOF
FAKECLAUDE
  chmod +x "$fake_claude_script"
  export RUNOQ_CLAUDE_BIN="$fake_claude_script"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" plan docs/plan.md --dry-run'

  [ "$status" -eq 0 ]
  [[ "$output" == *'"items"'* ]]
  [[ "$output" == *'"task-1"'* ]]
}

@test "runoq runtime implementation supports plan dry-run routing" {
  project_dir="$TEST_TMPDIR/project"
  setup_cli_project "$project_dir"
  mkdir -p "$project_dir/docs"
  echo "# Plan" >"$project_dir/docs/plan.md"
  prepare_runtime_bin
  export RUNOQ_IMPLEMENTATION="runtime"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/runtime-plan-claude"
  export FAKE_RUNTIME_PLAN_CLAUDE_LOG="$TEST_TMPDIR/runtime-plan-claude.log"

  run bash -lc 'cd "'"$project_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" plan docs/plan.md --dry-run'

  [ "$status" -eq 0 ]
  [[ "$output" == *'"runtime-task"'* ]]
  run cat "$FAKE_RUNTIME_PLAN_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"--agent plan-decomposer --add-dir $RUNOQ_ROOT"* ]]
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
  run bash -lc 'cd "'"$project_dir"'" && find log/claude -mindepth 1 -maxdepth 1 -type d | head -n 1'
  [ "$status" -eq 0 ]
  capture_dir="$output"
  [ -n "$capture_dir" ]
  [ -f "$project_dir/$capture_dir/argv.txt" ]
  [ -f "$project_dir/$capture_dir/context.log" ]
  [ -f "$project_dir/$capture_dir/stdout.log" ]
  [ -f "$project_dir/$capture_dir/stderr.log" ]
  [ -f "$project_dir/$capture_dir/response.txt" ]
  run grep -F -- "maintenance-reviewer" "$project_dir/$capture_dir/argv.txt"
  [ "$status" -eq 0 ]
  run grep -F -- "fake claude invoked" "$project_dir/$capture_dir/response.txt"
  [ "$status" -eq 0 ]
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
  prepare_runtime_bin

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
