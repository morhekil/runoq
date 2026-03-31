#!/usr/bin/env bats

load test_helper

write_runtime_orchestrator_config() {
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
    "inputPerMillion": 0,
    "cachedInputPerMillion": 0,
    "outputPerMillion": 0
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

write_fake_codex() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

mode="${1:-}"
shift || true
[[ "$mode" == "exec" ]] || exit 2

if [[ "${1:-}" == "resume" ]]; then
  shift 2 || true
fi

output_file=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dangerously-bypass-approvals-and-sandbox|--json)
      shift
      ;;
    -o)
      output_file="${2:-}"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

[[ -n "$output_file" ]] || exit 3

round=1
if [[ -n "${RUNOQ_TEST_ISSUE_RUNNER_ROUND_STATE_FILE:-}" ]]; then
  if [[ -f "${RUNOQ_TEST_ISSUE_RUNNER_ROUND_STATE_FILE}" ]]; then
    round="$(( $(cat "${RUNOQ_TEST_ISSUE_RUNNER_ROUND_STATE_FILE}") + 1 ))"
  fi
  printf '%s\n' "$round" >"${RUNOQ_TEST_ISSUE_RUNNER_ROUND_STATE_FILE}"
fi

dev_command="${RUNOQ_TEST_ISSUE_RUNNER_DEV_COMMAND:-}"
if [[ -n "${dev_command:-}" ]]; then
  bash -lc "cd \"$PWD\" && ${dev_command}"
fi

tokens="${RUNOQ_TEST_ISSUE_RUNNER_TOKENS:-}"
if [[ -n "${tokens:-}" ]]; then
  printf 'tokens: %s\n' "$tokens" >&2
fi

payload_file_var="RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_FILE_${round}"
payload_json_var="RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_JSON_${round}"
payload_file="${!payload_file_var:-${RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_FILE:-}}"
payload_json="${!payload_json_var:-${RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_JSON:-}}"

printf '{"type":"thread.started","thread_id":"thread-issue-runner-1"}\n'

{
  printf '<!-- runoq:payload:codex-return -->\n'
  printf '```json\n'
  if [[ -n "${payload_file:-}" ]]; then
    cat "$payload_file"
  elif [[ -n "${payload_json:-}" ]]; then
    printf '%s\n' "$payload_json"
  else
    printf '%s\n' "${RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_JSON:-{"status":"completed","commits_pushed":[],"commit_range":"","files_changed":[],"files_added":[],"files_deleted":[],"tests_run":true,"tests_passed":true,"test_summary":"ok","build_passed":true,"blockers":[],"notes":""}}"
  fi
  printf '```\n'
} >"$output_file"
EOF
  chmod +x "$path"
}

write_fake_review_claude() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"REVIEW-TYPE: diff-review\nVERDICT: PASS\nSCORE: 42\nCHECKLIST:\n- Acceptance criteria satisfied.\n"}]}}'
printf '%s\n' '{"type":"result","result":"done"}'
EOF
  chmod +x "$path"
}

write_fake_review_claude_iterate_then_pass() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

round=1
if [[ -n "${RUNOQ_TEST_REVIEW_ROUND_STATE_FILE:-}" ]]; then
  if [[ -f "${RUNOQ_TEST_REVIEW_ROUND_STATE_FILE}" ]]; then
    round="$(( $(cat "${RUNOQ_TEST_REVIEW_ROUND_STATE_FILE}") + 1 ))"
  fi
  printf '%s\n' "$round" >"${RUNOQ_TEST_REVIEW_ROUND_STATE_FILE}"
fi

if [[ "$round" -eq 1 ]]; then
  printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"REVIEW-TYPE: diff-review\nVERDICT: ITERATE\nSCORE: 21\nCHECKLIST:\n- tighten acceptance coverage.\n"}]}}'
else
  printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"REVIEW-TYPE: diff-review\nVERDICT: PASS\nSCORE: 42\nCHECKLIST:\n- Acceptance criteria satisfied.\n"}]}}'
fi
printf '%s\n' '{"type":"result","result":"done"}'
EOF
  chmod +x "$path"
}

write_fake_runtime_orchestrator_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail
printf 'FAKE_RUNTIME:%s\n' "$*"
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

happy_issue_body() {
  cat <<'EOF'
<!-- runoq:meta
depends_on: []
priority: 1
estimated_complexity: low
-->

## Acceptance Criteria

- [ ] Adds the queue implementation file.
EOF
}

epic_issue_body() {
  cat <<'EOF'
<!-- runoq:meta
depends_on: []
priority: 1
estimated_complexity: medium
type: epic
-->

## Acceptance Criteria

- [ ] Coordinate the runtime migration.
EOF
}

prepare_orchestrator_repo() {
  local remote_dir="$1"
  local local_dir="$2"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
}

normalize_gh_log() {
  printf '%s' "$1" | sed -E 's#--body-file [^ ]+#--body-file <temp-body>#g'
}

normalize_json_output() {
  printf '%s' "$1" | jq -S -c .
}

normalize_orchestrator_stderr() {
  printf '%s' "$1" | sed -E 's#/(shell|runtime)-project#/<project>#g'
}

@test "orchestrator wrapper defaults to runtime and preserves explicit shell override" {
  project_dir="$TEST_TMPDIR/default-wrapper-project"
  remote_dir="$TEST_TMPDIR/default-wrapper-remote.git"
  prepare_orchestrator_repo "$remote_dir" "$project_dir"

  fake_runtime_bin="$TEST_TMPDIR/fake-runtime-orchestrator"
  write_fake_runtime_orchestrator_bin "$fake_runtime_bin"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_ORCHESTRATOR_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" --help'
  [ "$status" -eq 0 ]
  [ "$output" = "FAKE_RUNTIME:__orchestrator --help" ]

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_ORCHESTRATOR_IMPLEMENTATION="shell" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" --help'
  [ "$status" -ne 127 ]
  [[ "$output" != *"FAKE_RUNTIME:"* ]]
  [[ "$output" == *"Usage:"* ]]
  [[ "$output" == *"orchestrator.sh run"* ]]
}

@test "orchestrator wrapper go fallback runs from RUNOQ_ROOT when runtime bin is unset" {
  project_dir="$TEST_TMPDIR/default-wrapper-go-cwd-project"
  remote_dir="$TEST_TMPDIR/default-wrapper-go-cwd-remote.git"
  prepare_orchestrator_repo "$remote_dir" "$project_dir"

  fake_go_bin="$TEST_TMPDIR/fake-go-orchestrator"
  write_fake_go_bin "$fake_go_bin"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_ORCHESTRATOR_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="" RUNOQ_GO_BIN="'"$fake_go_bin"'" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" --help'
  [ "$status" -eq 0 ]
  [[ "$output" == *"FAKE_GO_CWD:$RUNOQ_ROOT"* ]]
  [[ "$output" == *"FAKE_GO_ARGS:run $RUNOQ_ROOT/cmd/runoq-runtime __orchestrator --help"* ]]
}

@test "acceptance parity: orchestrator init-failure rollback matches shell and runtime" {
  shell_remote="$TEST_TMPDIR/shell-remote.git"
  shell_project="$TEST_TMPDIR/shell-project"
  runtime_remote="$TEST_TMPDIR/runtime-remote.git"
  runtime_project="$TEST_TMPDIR/runtime-project"
  prepare_orchestrator_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_orchestrator_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/runoq.json"
  write_runtime_orchestrator_config "$config_path"

  issue_body="$(happy_issue_body)"
  ready_queue="$(jq -n --arg body "$issue_body" '[
    {number: 42, title: "Implement queue", body: $body, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/42"}
  ]')"

  shell_scenario="$TEST_TMPDIR/shell-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "title"],
    "stdout": "{\"title\":\"Implement queue\"}"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_queue")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "runoq/42-implement-queue"],
    "exit_code": 1,
    "stderr": "aborted: you must first push the current branch to a remote, or use the --head flag"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:ready"],
    "stdout": ""
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-gh.log"

  run bash -lc 'cd "'"$shell_project"'" && TARGET_ROOT="'"$shell_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-gh.state" FAKE_GH_LOG="'"$shell_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42'
  shell_status="$status"

  run bash -lc 'cd "'"$runtime_project"'" && TARGET_ROOT="'"$runtime_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-gh.state" FAKE_GH_LOG="'"$runtime_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42'
  runtime_status="$status"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 1 ]

  run jq -r '.phase' "$shell_project/.runoq/state/42.json"
  shell_phase="$output"
  run jq -r '.failure_stage' "$shell_project/.runoq/state/42.json"
  shell_failure_stage="$output"
  run jq -r '.phase' "$runtime_project/.runoq/state/42.json"
  runtime_phase="$output"
  run jq -r '.failure_stage' "$runtime_project/.runoq/state/42.json"
  runtime_failure_stage="$output"

  [ "$shell_phase" = "$runtime_phase" ]
  [ "$shell_phase" = "FAILED" ]
  [ "$shell_failure_stage" = "$runtime_failure_stage" ]
  [ "$shell_failure_stage" = "INIT" ]

  shell_worktree="$(cd "$shell_project/.." && pwd)/runoq-wt-42"
  runtime_worktree="$(cd "$runtime_project/.." && pwd)/runoq-wt-42"
  [ ! -e "$shell_worktree" ]
  [ ! -e "$runtime_worktree" ]

  run cat "$shell_log"
  shell_gh_log="$(normalize_gh_log "$output")"
  run cat "$runtime_log"
  runtime_gh_log="$(normalize_gh_log "$output")"
  [ "$shell_gh_log" = "$runtime_gh_log" ]
}

@test "acceptance parity: orchestrator run --issue --dry-run matches shell and runtime" {
  shell_remote="$TEST_TMPDIR/shell-remote.git"
  shell_project="$TEST_TMPDIR/shell-project"
  runtime_remote="$TEST_TMPDIR/runtime-remote.git"
  runtime_project="$TEST_TMPDIR/runtime-project"
  prepare_orchestrator_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_orchestrator_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/runoq.json"
  write_runtime_orchestrator_config "$config_path"

  issue_body="$(happy_issue_body)"
  ready_queue="$(jq -n --arg body "$issue_body" '[
    {number: 42, title: "Implement queue", body: $body, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/42"}
  ]')"

  shell_scenario="$TEST_TMPDIR/shell-dry-run-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-dry-run-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "title"],
    "stdout": "{\"title\":\"Implement queue\"}"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_queue")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-dry-run-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-dry-run-gh.log"

  run bash -lc 'cd "'"$shell_project"'" && TARGET_ROOT="'"$shell_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-dry-run-gh.state" FAKE_GH_LOG="'"$shell_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42 --dry-run'
  shell_status="$status"
  shell_stdout="$output"

  run bash -lc 'cd "'"$runtime_project"'" && TARGET_ROOT="'"$runtime_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-dry-run-gh.state" FAKE_GH_LOG="'"$runtime_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42 --dry-run'
  runtime_status="$status"
  runtime_stdout="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_json_output "$shell_stdout")" = "$(normalize_json_output "$runtime_stdout")" ]

  run cat "$shell_log"
  shell_gh_log="$(normalize_gh_log "$output")"
  run cat "$runtime_log"
  runtime_gh_log="$(normalize_gh_log "$output")"
  [ "$shell_gh_log" = "$runtime_gh_log" ]
}

@test "acceptance parity: orchestrator run --issue low-complexity verification failure reaches deterministic needs-review handoff" {
  shell_remote="$TEST_TMPDIR/shell/remote.git"
  shell_project="$TEST_TMPDIR/shell/project"
  runtime_remote="$TEST_TMPDIR/runtime/remote.git"
  runtime_project="$TEST_TMPDIR/runtime/project"
  mkdir -p "$TEST_TMPDIR/shell" "$TEST_TMPDIR/runtime"
  prepare_orchestrator_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_orchestrator_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_base="$TEST_TMPDIR/runoq.json"
  config_path="$TEST_TMPDIR/runoq-one-round.json"
  write_runtime_orchestrator_config "$config_base"
  jq '.maxRounds = 1' "$config_base" >"$config_path"

  fake_codex="$TEST_TMPDIR/fake-codex"
  write_fake_codex "$fake_codex"

  issue_body="$(happy_issue_body)"
  ready_queue="$(jq -n --arg body "$issue_body" '[
    {number: 42, title: "Implement queue", body: $body, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/42"}
  ]')"

  shell_scenario="$TEST_TMPDIR/shell-needs-review-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-needs-review-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "title"],
    "stdout": "{\"title\":\"Implement queue\"}"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_queue")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "runoq/42-implement-queue"],
    "stdout": "https://example.test/pull/87"
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "body"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"body":$body}')
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "ready", "87", "--repo", "owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "edit", "87", "--repo", "owner/repo", "--add-reviewer", "username", "--add-assignee", "username"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:needs-human-review"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body"],
    "stdout": ""
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-needs-review-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-needs-review-gh.log"

  run bash -lc 'cd "'"$shell_project"'" && TARGET_ROOT="'"$shell_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=shell RUNOQ_CODEX_BIN="'"$fake_codex"'" RUNOQ_TEST_ISSUE_RUNNER_DEV_COMMAND="true" RUNOQ_TEST_ISSUE_RUNNER_TOKENS="12" RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_JSON='"'"'{"status":"completed","commits_pushed":[],"commit_range":"","files_changed":[],"files_added":[],"files_deleted":[],"tests_run":true,"tests_passed":true,"test_summary":"ok","build_passed":true,"blockers":[],"notes":""}'"'"' FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-needs-review-gh.state" FAKE_GH_LOG="'"$shell_log"'" FAKE_GH_CAPTURE_DIR="'"$TEST_TMPDIR"'/shell-needs-review-capture" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42 2>"'"$TEST_TMPDIR"'/shell-needs-review.err"'
  shell_status="$status"
  shell_stdout="$output"

  run bash -lc 'cd "'"$runtime_project"'" && TARGET_ROOT="'"$runtime_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=runtime RUNOQ_CODEX_BIN="'"$fake_codex"'" RUNOQ_TEST_ISSUE_RUNNER_DEV_COMMAND="true" RUNOQ_TEST_ISSUE_RUNNER_TOKENS="12" RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_JSON='"'"'{"status":"completed","commits_pushed":[],"commit_range":"","files_changed":[],"files_added":[],"files_deleted":[],"tests_run":true,"tests_passed":true,"test_summary":"ok","build_passed":true,"blockers":[],"notes":""}'"'"' FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-needs-review-gh.state" FAKE_GH_LOG="'"$runtime_log"'" FAKE_GH_CAPTURE_DIR="'"$TEST_TMPDIR"'/runtime-needs-review-capture" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42 2>"'"$TEST_TMPDIR"'/runtime-needs-review.err"'
  runtime_status="$status"
  runtime_stdout="$output"
  run cat "$TEST_TMPDIR/shell-needs-review.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime-needs-review.err"
  runtime_err="$output"

  [ "$shell_status" -eq 0 ] || { printf '%s\n' "$shell_err"; false; }
  [ "$runtime_status" -eq 0 ] || { printf '%s\n' "$runtime_err"; false; }
  [ "$(printf '%s' "$shell_stdout" | jq -c '{phase,status,finalize_verdict,issue_status,round,pr_number}')" = "$(printf '%s' "$runtime_stdout" | jq -c '{phase,status,finalize_verdict,issue_status,round,pr_number}')" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.phase')" = "DONE" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.status')" = "fail" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.finalize_verdict')" = "needs-review" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.issue_status')" = "needs-review" ]

  run jq -c '{phase,status,finalize_verdict,issue_status,round,pr_number}' "$shell_project/.runoq/state/42.json"
  shell_state="$output"
  run jq -c '{phase,status,finalize_verdict,issue_status,round,pr_number}' "$runtime_project/.runoq/state/42.json"
  runtime_state="$output"
  [ "$shell_state" = "$runtime_state" ]

  run cat "$shell_log"
  shell_gh_log="$output"
  run cat "$runtime_log"
  runtime_gh_log="$output"
  [[ "$shell_gh_log" == *"issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review"* ]]
  [[ "$runtime_gh_log" == *"issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review"* ]]
  [[ "$shell_gh_log" == *"pr edit 87 --repo owner/repo --add-reviewer username --add-assignee username"* ]]
  [[ "$runtime_gh_log" == *"pr edit 87 --repo owner/repo --add-reviewer username --add-assignee username"* ]]
}

@test "acceptance parity: orchestrator run --issue low-complexity review_ready reaches auto-merge finalize" {
  shell_remote="$TEST_TMPDIR/shell/remote.git"
  shell_project="$TEST_TMPDIR/shell/project"
  runtime_remote="$TEST_TMPDIR/runtime/remote.git"
  runtime_project="$TEST_TMPDIR/runtime/project"
  mkdir -p "$TEST_TMPDIR/shell" "$TEST_TMPDIR/runtime"
  prepare_orchestrator_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_orchestrator_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/runoq.json"
  write_runtime_orchestrator_config "$config_path"

  fake_codex="$TEST_TMPDIR/fake-codex"
  write_fake_codex "$fake_codex"
  fake_review_claude="$TEST_TMPDIR/fake-review-claude"
  write_fake_review_claude "$fake_review_claude"
  codex_payload_file="$TEST_TMPDIR/codex-success.json"
  printf '%s\n' '{"status":"completed","commits_pushed":[],"commit_range":"","files_changed":[],"files_added":[],"files_deleted":[],"tests_run":true,"tests_passed":true,"test_summary":"ok","build_passed":true,"blockers":[],"notes":""}' >"$codex_payload_file"

  issue_body="$(happy_issue_body)"
  ready_queue="$(jq -n --arg body "$issue_body" '[
    {number: 42, title: "Implement queue", body: $body, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/42"}
  ]')"

  shell_scenario="$TEST_TMPDIR/shell-auto-merge-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-auto-merge-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "title"],
    "stdout": "{\"title\":\"Implement queue\"}"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_queue")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "runoq/42-implement-queue"],
    "stdout": "https://example.test/pull/87"
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "body"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"body":$body}')
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "ready", "87", "--repo", "owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "merge", "87", "--repo", "owner/repo", "--auto", "--squash"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:done"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-auto-merge-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-auto-merge-gh.log"

  run bash -lc 'cd "'"$shell_project"'" && TARGET_ROOT="'"$shell_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=shell RUNOQ_ISSUE_RUNNER_IMPLEMENTATION=runtime RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_CODEX_BIN="'"$fake_codex"'" RUNOQ_CLAUDE_BIN="'"$fake_review_claude"'" RUNOQ_TEST_ISSUE_RUNNER_DEV_COMMAND="mkdir -p src && printf \"export const queue = true;\n\" > src/queue.ts && git add src/queue.ts && git commit -m \"Add queue implementation\" >/dev/null && git push -u origin HEAD >/dev/null 2>&1" RUNOQ_TEST_ISSUE_RUNNER_TOKENS="12" RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_FILE="'"$codex_payload_file"'" FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-auto-merge-gh.state" FAKE_GH_LOG="'"$shell_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42 2>"'"$TEST_TMPDIR"'/shell-auto-merge.err"'
  shell_status="$status"
  shell_stdout="$output"

  run bash -lc 'cd "'"$runtime_project"'" && TARGET_ROOT="'"$runtime_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=runtime RUNOQ_CODEX_BIN="'"$fake_codex"'" RUNOQ_CLAUDE_BIN="'"$fake_review_claude"'" RUNOQ_TEST_ISSUE_RUNNER_DEV_COMMAND="mkdir -p src && printf \"export const queue = true;\n\" > src/queue.ts && git add src/queue.ts && git commit -m \"Add queue implementation\" >/dev/null && git push -u origin HEAD >/dev/null 2>&1" RUNOQ_TEST_ISSUE_RUNNER_TOKENS="12" RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_FILE="'"$codex_payload_file"'" FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-auto-merge-gh.state" FAKE_GH_LOG="'"$runtime_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42 2>"'"$TEST_TMPDIR"'/runtime-auto-merge.err"'
  runtime_status="$status"
  runtime_stdout="$output"
  run cat "$TEST_TMPDIR/shell-auto-merge.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime-auto-merge.err"
  runtime_err="$output"

  [ "$shell_status" -eq 0 ] || { printf '%s\n' "$shell_err"; false; }
  [ "$runtime_status" -eq 0 ] || { printf '%s\n' "$runtime_err"; false; }
  [ "$(printf '%s' "$shell_stdout" | jq -r '.phase')" = "DONE" ]
  [ "$(printf '%s' "$shell_stdout" | jq -r '.finalize_verdict')" = "auto-merge" ]
  [ "$(printf '%s' "$shell_stdout" | jq -r '.issue_status')" = "done" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.phase')" = "DONE" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.finalize_verdict')" = "auto-merge" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.issue_status')" = "done" ]

  run jq -r '.phase' "$shell_project/.runoq/state/42.json"
  [ "$output" = "DONE" ]
  run jq -r '.finalize_verdict' "$shell_project/.runoq/state/42.json"
  [ "$output" = "auto-merge" ]
  run jq -r '.issue_status' "$shell_project/.runoq/state/42.json"
  [ "$output" = "done" ]
  run jq -r '.phase' "$runtime_project/.runoq/state/42.json"
  [ "$output" = "DONE" ]
  run jq -r '.finalize_verdict' "$runtime_project/.runoq/state/42.json"
  [ "$output" = "auto-merge" ]
  run jq -r '.issue_status' "$runtime_project/.runoq/state/42.json"
  [ "$output" = "done" ]

  shell_worktree="$(cd "$shell_project/.." && pwd)/runoq-wt-42"
  runtime_worktree="$(cd "$runtime_project/.." && pwd)/runoq-wt-42"
  [ ! -e "$shell_worktree" ]
  [ ! -e "$runtime_worktree" ]

}

@test "acceptance parity: orchestrator run --issue ITERATE loops back to DEVELOP and then auto-merges" {
  shell_remote="$TEST_TMPDIR/shell/remote.git"
  shell_project="$TEST_TMPDIR/shell/project"
  runtime_remote="$TEST_TMPDIR/runtime/remote.git"
  runtime_project="$TEST_TMPDIR/runtime/project"
  mkdir -p "$TEST_TMPDIR/shell" "$TEST_TMPDIR/runtime"
  prepare_orchestrator_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_orchestrator_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/runoq.json"
  write_runtime_orchestrator_config "$config_path"

  fake_codex="$TEST_TMPDIR/fake-codex"
  write_fake_codex "$fake_codex"
  fake_review_claude="$TEST_TMPDIR/fake-review-claude-iterate"
  write_fake_review_claude_iterate_then_pass "$fake_review_claude"
  codex_payload_file="$TEST_TMPDIR/codex-success.json"
  printf '%s\n' '{"status":"completed","commits_pushed":[],"commit_range":"","files_changed":[],"files_added":[],"files_deleted":[],"tests_run":true,"tests_passed":true,"test_summary":"ok","build_passed":true,"blockers":[],"notes":""}' >"$codex_payload_file"

  issue_body="$(happy_issue_body)"
  ready_queue="$(jq -n --arg body "$issue_body" '[
    {number: 42, title: "Implement queue", body: $body, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/42"}
  ]')"

  shell_scenario="$TEST_TMPDIR/shell-iterate-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-iterate-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "title"],
    "stdout": "{\"title\":\"Implement queue\"}"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_queue")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "runoq/42-implement-queue"],
    "stdout": "https://example.test/pull/87"
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "body"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"body":$body}')
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "body"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"body":$body}')
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "ready", "87", "--repo", "owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "merge", "87", "--repo", "owner/repo", "--auto", "--squash"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:done"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-iterate-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-iterate-gh.log"
  shell_round_state="$TEST_TMPDIR/shell-round.state"
  runtime_round_state="$TEST_TMPDIR/runtime-round.state"
  shell_review_round_state="$TEST_TMPDIR/shell-review-round.state"
  runtime_review_round_state="$TEST_TMPDIR/runtime-review-round.state"

  run bash -lc 'cd "'"$shell_project"'" && TARGET_ROOT="'"$shell_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=shell RUNOQ_ISSUE_RUNNER_IMPLEMENTATION=runtime RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_CODEX_BIN="'"$fake_codex"'" RUNOQ_CLAUDE_BIN="'"$fake_review_claude"'" RUNOQ_TEST_ISSUE_RUNNER_ROUND_STATE_FILE="'"$shell_round_state"'" RUNOQ_TEST_REVIEW_ROUND_STATE_FILE="'"$shell_review_round_state"'" RUNOQ_TEST_ISSUE_RUNNER_DEV_COMMAND="mkdir -p src && round=\$(cat \"$RUNOQ_TEST_ISSUE_RUNNER_ROUND_STATE_FILE\") && printf \"export const queueRound%s = true;\n\" \"\$round\" > \"src/queue-\$round.ts\" && git add \"src/queue-\$round.ts\" && git commit -m \"Add queue implementation round \$round\" >/dev/null && git push -u origin HEAD >/dev/null 2>&1" RUNOQ_TEST_ISSUE_RUNNER_TOKENS="12" RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_FILE="'"$codex_payload_file"'" FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-iterate-gh.state" FAKE_GH_LOG="'"$shell_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42 2>"'"$TEST_TMPDIR"'/shell-iterate.err"'
  shell_status="$status"
  shell_stdout="$output"

  run bash -lc 'cd "'"$runtime_project"'" && TARGET_ROOT="'"$runtime_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=runtime RUNOQ_CODEX_BIN="'"$fake_codex"'" RUNOQ_CLAUDE_BIN="'"$fake_review_claude"'" RUNOQ_TEST_ISSUE_RUNNER_ROUND_STATE_FILE="'"$runtime_round_state"'" RUNOQ_TEST_REVIEW_ROUND_STATE_FILE="'"$runtime_review_round_state"'" RUNOQ_TEST_ISSUE_RUNNER_DEV_COMMAND="mkdir -p src && round=\$(cat \"$RUNOQ_TEST_ISSUE_RUNNER_ROUND_STATE_FILE\") && printf \"export const queueRound%s = true;\n\" \"\$round\" > \"src/queue-\$round.ts\" && git add \"src/queue-\$round.ts\" && git commit -m \"Add queue implementation round \$round\" >/dev/null && git push -u origin HEAD >/dev/null 2>&1" RUNOQ_TEST_ISSUE_RUNNER_TOKENS="12" RUNOQ_TEST_ISSUE_RUNNER_PAYLOAD_FILE="'"$codex_payload_file"'" FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-iterate-gh.state" FAKE_GH_LOG="'"$runtime_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --issue 42 2>"'"$TEST_TMPDIR"'/runtime-iterate.err"'
  runtime_status="$status"
  runtime_stdout="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(printf '%s' "$shell_stdout" | jq -c '{phase,finalize_verdict,issue_status,round,pr_number}')" = "$(printf '%s' "$runtime_stdout" | jq -c '{phase,finalize_verdict,issue_status,round,pr_number}')" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.phase')" = "DONE" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.finalize_verdict')" = "auto-merge" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.issue_status')" = "done" ]
  [ "$(printf '%s' "$runtime_stdout" | jq -r '.round')" = "2" ]

  run cat "$shell_log"
  shell_gh_log="$(normalize_gh_log "$output")"
  run cat "$runtime_log"
  runtime_gh_log="$(normalize_gh_log "$output")"
  [[ "$shell_gh_log" == *"issue view 42 --repo owner/repo --json body"* ]]
  [[ "$runtime_gh_log" == *"issue view 42 --repo owner/repo --json body"* ]]
  [[ "$shell_gh_log" == *"pr merge 87 --repo owner/repo --auto --squash"* ]]
  [[ "$runtime_gh_log" == *"pr merge 87 --repo owner/repo --auto --squash"* ]]
  [[ "$shell_gh_log" == *"issue edit 42 --repo owner/repo"* ]]
  [[ "$runtime_gh_log" == *"issue edit 42 --repo owner/repo"* ]]
  [[ "$shell_gh_log" == *"--add-label runoq:done"* ]]
  [[ "$runtime_gh_log" == *"--add-label runoq:done"* ]]
}

@test "acceptance parity: orchestrator run queue --dry-run preserves selection and blocked reasons" {
  shell_remote="$TEST_TMPDIR/shell-remote.git"
  shell_project="$TEST_TMPDIR/shell-project"
  runtime_remote="$TEST_TMPDIR/runtime-remote.git"
  runtime_project="$TEST_TMPDIR/runtime-project"
  prepare_orchestrator_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_orchestrator_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/runoq.json"
  write_runtime_orchestrator_config "$config_path"

  epic_body="$(epic_issue_body)"
  task_body="$(happy_issue_body)"
  ready_queue="$(jq -n --arg epic_body "$epic_body" --arg task_body "$task_body" '[
    {number: 41, title: "Coordinate migration", body: $epic_body, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/41"},
    {number: 42, title: "Implement queue", body: $task_body, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/42"}
  ]')"

  shell_scenario="$TEST_TMPDIR/shell-queue-dry-run-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-queue-dry-run-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_queue")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$task_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_queue")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$task_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-queue-dry-run-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-queue-dry-run-gh.log"

  run bash -lc 'cd "'"$shell_project"'" && TARGET_ROOT="'"$shell_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-queue-dry-run-gh.state" FAKE_GH_LOG="'"$shell_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --dry-run 2>"'"$TEST_TMPDIR"'/shell-queue-dry-run.err"'
  shell_status="$status"
  shell_stdout="$output"

  run bash -lc 'cd "'"$runtime_project"'" && TARGET_ROOT="'"$runtime_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-queue-dry-run-gh.state" FAKE_GH_LOG="'"$runtime_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo --dry-run 2>"'"$TEST_TMPDIR"'/runtime-queue-dry-run.err"'
  runtime_status="$status"
  runtime_stdout="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_json_output "$shell_stdout")" = "$(normalize_json_output "$runtime_stdout")" ]

  run cat "$TEST_TMPDIR/shell-queue-dry-run.err"
  shell_err="$(normalize_orchestrator_stderr "$output")"
  run cat "$TEST_TMPDIR/runtime-queue-dry-run.err"
  runtime_err="$(normalize_orchestrator_stderr "$output")"
  [ "$shell_err" = "$runtime_err" ]
  [[ "$runtime_err" == *"Queue result: 1 actionable issue found, 1 skipped"* ]]
  [[ "$runtime_err" == *"Skipped details: #41 — epic issues are not directly dispatchable"* ]]

  run cat "$shell_log"
  shell_gh_log="$(normalize_gh_log "$output")"
  run cat "$runtime_log"
  runtime_gh_log="$(normalize_gh_log "$output")"
  [ "$shell_gh_log" = "$runtime_gh_log" ]
}

@test "acceptance parity: orchestrator run queue performs epic integrate sweep when children are done" {
  shell_remote="$TEST_TMPDIR/shell-remote.git"
  shell_project="$TEST_TMPDIR/shell-project"
  runtime_remote="$TEST_TMPDIR/runtime-remote.git"
  runtime_project="$TEST_TMPDIR/runtime-project"
  prepare_orchestrator_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_orchestrator_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/runoq.json"
  write_runtime_orchestrator_config "$config_path"

  epic_body="$(epic_issue_body)"
  ready_epic_queue="$(jq -n --arg epic_body "$epic_body" '[
    {number: 41, title: "Coordinate migration", body: $epic_body, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/41"}
  ]')"

  shell_scenario="$TEST_TMPDIR/shell-queue-epic-integrate-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-queue-epic-integrate-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_epic_queue")
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_epic_queue")
  },
  {
    "contains": ["api", "repos/owner/repo/issues/41/sub_issues", "--paginate"],
    "stdout": "[]"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/41/sub_issues", "--paginate"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "41", "--repo", "owner/repo", "--json", "title"],
    "stdout": "{\"title\":\"Coordinate migration\"}"
  },
  {
    "contains": ["issue", "view", "41", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "41", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:done"],
    "stdout": ""
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-queue-epic-integrate-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-queue-epic-integrate-gh.log"

  run bash -lc 'cd "'"$shell_project"'" && TARGET_ROOT="'"$shell_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-queue-epic-integrate-gh.state" FAKE_GH_LOG="'"$shell_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo 2>"'"$TEST_TMPDIR"'/shell-queue-epic-integrate.err"'
  shell_status="$status"
  shell_stdout="$output"

  run bash -lc 'cd "'"$runtime_project"'" && TARGET_ROOT="'"$runtime_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-queue-epic-integrate-gh.state" FAKE_GH_LOG="'"$runtime_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" run owner/repo 2>"'"$TEST_TMPDIR"'/runtime-queue-epic-integrate.err"'
  runtime_status="$status"
  runtime_stdout="$output"

  [ "$shell_status" -eq 0 ]
  [ "$runtime_status" -eq 0 ]
  [ "$(normalize_json_output "$shell_stdout")" = "$(normalize_json_output "$runtime_stdout")" ]

  run cat "$shell_log"
  shell_gh_log="$(normalize_gh_log "$output")"
  run cat "$runtime_log"
  runtime_gh_log="$(normalize_gh_log "$output")"
  [ "$shell_gh_log" = "$runtime_gh_log" ]
  [[ "$runtime_gh_log" == *"api repos/owner/repo/issues/41/sub_issues --paginate"* ]]
  [[ "$runtime_gh_log" == *"issue edit 41 --repo owner/repo --remove-label runoq:ready --add-label runoq:done"* ]]
}

@test "acceptance parity: mention-triage matches poll contract for zero-mention scenario" {
  shell_remote="$TEST_TMPDIR/shell-remote.git"
  shell_project="$TEST_TMPDIR/shell-project"
  runtime_remote="$TEST_TMPDIR/runtime-remote.git"
  runtime_project="$TEST_TMPDIR/runtime-project"
  prepare_orchestrator_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_orchestrator_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/runoq.json"
  write_runtime_orchestrator_config "$config_path"

  shell_scenario="$TEST_TMPDIR/shell-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<EOF
[
  {
    "contains": ["api", "repos/owner/repo/issues?state=open&per_page=100"],
    "stdout_file": "$(fixture_path "comments/open-items.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/87/comments"],
    "stdout": "[]"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/90/comments"],
    "stdout": "[]"
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-gh.log"

  run bash -lc 'cd "'"$shell_project"'" && TARGET_ROOT="'"$shell_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-gh.state" FAKE_GH_LOG="'"$shell_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" mention-triage owner/repo 87 2>"'"$TEST_TMPDIR"'/shell-mention-triage.err"'
  shell_status="$status"
  shell_stdout="$output"

  run bash -lc 'cd "'"$runtime_project"'" && TARGET_ROOT="'"$runtime_project"'" RUNOQ_REPO="owner/repo" REPO="owner/repo" RUNOQ_CONFIG="'"$config_path"'" RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ORCHESTRATOR_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-gh.state" FAKE_GH_LOG="'"$runtime_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/orchestrator.sh" mention-triage owner/repo 87 2>"'"$TEST_TMPDIR"'/runtime-mention-triage.err"'
  runtime_status="$status"
  runtime_stdout="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$shell_stdout" = "$runtime_stdout" ]
  [ -z "$shell_stdout" ]

  run cat "$shell_log"
  shell_gh_log="$(normalize_gh_log "$output")"
  run cat "$runtime_log"
  runtime_gh_log="$(normalize_gh_log "$output")"
  [ "$shell_gh_log" = "$runtime_gh_log" ]
}
