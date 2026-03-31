#!/usr/bin/env bats

load test_helper

write_run_config() {
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

normalize_json() {
  printf '%s' "$1" | jq -S -c .
}

normalize_run_issue_output() {
  printf '%s' "$1" | jq '.worktree = (.worktree | split("/")[-1])' | normalize_json
}

write_fake_codex_schema_resume_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

log_file="${RUNOQ_TEST_FAKE_CODEX_LOG:?}"
state_file="${RUNOQ_TEST_FAKE_CODEX_STATE:?}"
printf '%s\n' "$*" >>"$log_file"

mode="${1:-}"
shift || true
[[ "$mode" == "exec" ]] || exit 2

resume_mode="false"
if [[ "${1:-}" == "resume" ]]; then
  resume_mode="true"
  resume_thread_id="${2:-}"
  printf 'RESUME_THREAD_ID=%s\n' "$resume_thread_id" >>"$log_file"
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
      prompt="$1"
      shift
      ;;
  esac
done

[[ -n "$output_file" ]] || exit 3

if [[ ! -f "$state_file" ]]; then
  printf '0\n' >"$state_file"
fi
count="$(cat "$state_file")"
count=$((count + 1))
printf '%s\n' "$count" >"$state_file"

if [[ "$count" -eq 1 ]]; then
  if [[ -n "${RUNOQ_TEST_DEV_COMMAND:-}" ]]; then
    bash -lc "$RUNOQ_TEST_DEV_COMMAND"
  fi
  printf '{"type":"thread.started","thread_id":"thread-issue-runner-1"}\n'
  cat >"$output_file" <<'PAYLOAD'
<!-- runoq:payload:codex-return -->
```json
{
  "status": "completed",
  "tests_run": true,
  "tests_passed": "yes",
  "build_passed": true
}
```
PAYLOAD
  exit 0
fi

commit_sha="$(git rev-parse HEAD)"
printf '{"type":"thread.started","thread_id":"thread-issue-runner-1"}\n'
cat >"$output_file" <<PAYLOAD
<!-- runoq:payload:codex-return -->
\`\`\`json
{
  "status": "completed",
  "commits_pushed": ["$commit_sha"],
  "commit_range": "$commit_sha..$commit_sha",
  "files_changed": [],
  "files_added": ["src/queue.ts"],
  "files_deleted": [],
  "tests_run": true,
  "tests_passed": true,
  "test_summary": "ok",
  "build_passed": true,
  "blockers": [],
  "notes": "ok"
}
\`\`\`
PAYLOAD
EOF
  chmod +x "$path"
}

write_fake_codex_always_schema_invalid_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

log_file="${RUNOQ_TEST_FAKE_CODEX_LOG:?}"
state_file="${RUNOQ_TEST_FAKE_CODEX_STATE:?}"
printf '%s\n' "$*" >>"$log_file"

mode="${1:-}"
shift || true
[[ "$mode" == "exec" ]] || exit 2

if [[ "${1:-}" == "resume" ]]; then
  resume_thread_id="${2:-}"
  printf 'RESUME_THREAD_ID=%s\n' "$resume_thread_id" >>"$log_file"
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
      prompt="$1"
      shift
      ;;
  esac
done

[[ -n "$output_file" ]] || exit 3

if [[ ! -f "$state_file" ]]; then
  printf '0\n' >"$state_file"
fi
count="$(cat "$state_file")"
count=$((count + 1))
printf '%s\n' "$count" >"$state_file"

printf '{"type":"thread.started","thread_id":"thread-issue-runner-bad"}\n'
cat >"$output_file" <<'PAYLOAD'
<!-- runoq:payload:codex-return -->
```json
{
  "status": "completed",
  "tests_passed": "yes"
}
```
PAYLOAD
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

write_issue_view_scenario_rules() {
  local issue_body="$1"
  cat <<EOF
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  }
EOF
}

write_needs_review_scenario() {
  local scenario="$1"
  local issue_body="$2"
  local issue_comment="$3"

  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
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
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "body"],
    "stdout_file": "$RUNOQ_ROOT/test/fixtures/comments/pr-view-body.json"
  },
  {
    "contains": ["pr", "edit", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
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
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "$issue_comment"],
    "stdout": ""
  }
]
EOF
}

write_malformed_payload_scenario() {
  local scenario="$1"
  local issue_body="$2"
  local issue_comment="$3"

  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
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
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "body"],
    "stdout_file": "$RUNOQ_ROOT/test/fixtures/comments/pr-view-body.json"
  },
  {
    "contains": ["pr", "edit", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
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
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "$issue_comment"],
    "stdout": ""
  }
]
EOF
}

prepare_issue_repo() {
  local remote_dir="$1"
  local local_dir="$2"

  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
}

run_issue_with_impl() {
  local implementation="$1"
  local project_dir="$2"
  local config_path="$3"
  local scenario_path="$4"
  local state_path="$5"
  local log_path="$6"
  local capture_dir="$7"

  export TARGET_ROOT="$project_dir"
  export RUNOQ_REPO="owner/repo"
  export REPO="owner/repo"
  export GH_TOKEN="existing-token"
  export RUNOQ_TEST_RUN_MODE="fixture"
  export RUNOQ_TEST_ORCHESTRATOR_RETURN_FILE=""
  export RUNOQ_CONFIG="$config_path"
  export FAKE_GH_SCENARIO="$scenario_path"
  export FAKE_GH_STATE="$state_path"
  export FAKE_GH_LOG="$log_path"
  export FAKE_GH_CAPTURE_DIR="$capture_dir"
  export GH_BIN="$RUNOQ_ROOT/test/helpers/gh"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="'"$implementation"'" "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42'
  RUN_STATUS="$status"
  RUN_OUTPUT="$output"
}

assert_log_contains() {
  local log_path="$1"
  local needle="$2"
  run rg -F -n "$needle" "$log_path"
  [ "$status" -eq 0 ]
}

assert_capture_contains() {
  local capture_dir="$1"
  local needle="$2"
  run rg -F -n "$needle" "$capture_dir"
  [ "$status" -eq 0 ]
}

@test "issue-runner wrapper defaults to shell and ignores runtime-bin env" {
  local_dir="$TEST_TMPDIR/default-wrapper-local"
  make_git_repo "$local_dir"

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_IMPLEMENTATION=runtime RUNOQ_ISSUE_RUNNER_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="/definitely/not/used" "'"$RUNOQ_ROOT"'/scripts/issue-runner.sh"'
  [ "$status" -ne 0 ]
  [[ "$output" == *"Usage:"* ]]
  [[ "$output" == *"issue-runner.sh run"* ]]
}

@test "issue-runner wrapper accepts runtime compatibility alias without runtime dispatch" {
  local_dir="$TEST_TMPDIR/default-wrapper-go-cwd-local"
  make_git_repo "$local_dir"

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_ISSUE_RUNNER_IMPLEMENTATION=runtime RUNOQ_RUNTIME_BIN="/definitely/not/used" RUNOQ_GO_BIN="/definitely/not/used" "'"$RUNOQ_ROOT"'/scripts/issue-runner.sh"'
  [ "$status" -ne 0 ]
  [[ "$output" == *"Usage:"* ]]
  [[ "$output" == *"issue-runner.sh run"* ]]
}

@test "issue-runner resumes the same codex thread for payload schema retries" {
  remote_dir="$TEST_TMPDIR/direct-runner-resume-remote.git"
  project_dir="$TEST_TMPDIR/direct-runner-resume-project"
  prepare_issue_repo "$remote_dir" "$project_dir"

  config_path="$TEST_TMPDIR/direct-runner-resume-config.json"
  write_run_config "$config_path"

  fake_codex="$TEST_TMPDIR/fake-codex-schema-resume"
  write_fake_codex_schema_resume_bin "$fake_codex"
  export RUNOQ_TEST_FAKE_CODEX_LOG="$TEST_TMPDIR/fake-codex-schema-resume.log"
  export RUNOQ_TEST_FAKE_CODEX_STATE="$TEST_TMPDIR/fake-codex-schema-resume.state"
  export RUNOQ_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'

  spec_path="$TEST_TMPDIR/direct-runner-resume-spec.md"
  cat >"$spec_path" <<'EOF'
## Acceptance Criteria
- [ ] Add queue implementation
EOF

  payload_file="$TEST_TMPDIR/direct-runner-resume-payload.json"
  log_dir="$project_dir/log/direct-runner-resume"
  cat >"$payload_file" <<EOF
{
  "issueNumber": 42,
  "prNumber": 87,
  "worktree": "$project_dir",
  "branch": "main",
  "logDir": "$log_dir",
  "specPath": "$spec_path",
  "repo": "owner/repo",
  "maxRounds": 2,
  "maxTokenBudget": 500000,
  "guidelines": []
}
EOF

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="shell" RUNOQ_ISSUE_RUNNER_IMPLEMENTATION="shell" RUNOQ_CODEX_BIN="'"$fake_codex"'" RUNOQ_CONFIG="'"$config_path"'" "'"$RUNOQ_ROOT"'/scripts/issue-runner.sh" run "'"$payload_file"'"'
  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "review_ready" ]
  [ "$(printf '%s' "$output" | jq -r '.status')" != "budget_exhausted" ]

  log_dir="$(printf '%s' "$output" | jq -r '.logDir')"
  round_payload="$log_dir/round-1-payload.json"
  thread_file="$log_dir/round-1-thread-id.txt"
  retry_message_file="$log_dir/round-1-schema-retry-1-last-message.md"

  run test -f "$thread_file"
  [ "$status" -eq 0 ]
  [ "$(cat "$thread_file")" = "thread-issue-runner-1" ]
  run test -f "$retry_message_file"
  [ "$status" -eq 0 ]
  [ "$(jq -r '.thread_id' "$round_payload")" = "thread-issue-runner-1" ]
  [ "$(jq -r '.payload_schema_valid' "$round_payload")" = "true" ]
  [ "$(jq -c '.payload_schema_errors' "$round_payload")" = "[]" ]

  run rg -F -n "exec resume thread-issue-runner-1 --json -o" "$RUNOQ_TEST_FAKE_CODEX_LOG"
  [ "$status" -eq 0 ]
}

@test "issue-runner uses repo-root absolute last-message paths when codex runs in sibling worktree" {
  remote_dir="$TEST_TMPDIR/direct-runner-abs-paths-remote.git"
  root_project_dir="$TEST_TMPDIR/direct-runner-abs-paths-root-project"
  worktree_dir="$TEST_TMPDIR/direct-runner-abs-paths-worktree"
  prepare_issue_repo "$remote_dir" "$root_project_dir"
  git -C "$root_project_dir" worktree add -b runoq/42-abs-paths "$worktree_dir" >/dev/null

  config_path="$TEST_TMPDIR/direct-runner-abs-paths-config.json"
  write_run_config "$config_path"

  fake_codex="$TEST_TMPDIR/fake-codex-abs-paths"
  write_fake_codex_schema_resume_bin "$fake_codex"
  export RUNOQ_TEST_FAKE_CODEX_LOG="$TEST_TMPDIR/fake-codex-abs-paths.log"
  export RUNOQ_TEST_FAKE_CODEX_STATE="$TEST_TMPDIR/fake-codex-abs-paths.state"
  export RUNOQ_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'

  spec_path="$TEST_TMPDIR/direct-runner-abs-paths-spec.md"
  cat >"$spec_path" <<'EOF'
## Acceptance Criteria
- [ ] Add queue implementation
EOF

  payload_file="$TEST_TMPDIR/direct-runner-abs-paths-payload.json"
  log_dir="$root_project_dir/log/direct-runner-abs-paths"
  cat >"$payload_file" <<EOF
{
  "issueNumber": 42,
  "prNumber": 87,
  "worktree": "$worktree_dir",
  "branch": "runoq/42-abs-paths",
  "logDir": "$log_dir",
  "specPath": "$spec_path",
  "repo": "owner/repo",
  "maxRounds": 2,
  "maxTokenBudget": 500000,
  "guidelines": []
}
EOF

  run bash -lc 'cd "'"$root_project_dir"'" && RUNOQ_IMPLEMENTATION="shell" RUNOQ_ISSUE_RUNNER_IMPLEMENTATION="shell" RUNOQ_CODEX_BIN="'"$fake_codex"'" RUNOQ_CONFIG="'"$config_path"'" "'"$RUNOQ_ROOT"'/scripts/issue-runner.sh" run "'"$payload_file"'"'
  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "review_ready" ]

  log_dir="$(printf '%s' "$output" | jq -r '.logDir')"
  round_payload="$log_dir/round-1-payload.json"
  root_message_file="$log_dir/round-1-last-message.md"
  root_retry_message_file="$log_dir/round-1-schema-retry-1-last-message.md"
  worktree_message_file="$worktree_dir/log/direct-runner-abs-paths/round-1-last-message.md"
  worktree_retry_message_file="$worktree_dir/log/direct-runner-abs-paths/round-1-schema-retry-1-last-message.md"

  run test -f "$round_payload"
  [ "$status" -eq 0 ]
  [ "$(jq -r '.payload_schema_valid' "$round_payload")" = "true" ]
  [ "$(jq -c '.payload_schema_errors' "$round_payload")" = "[]" ]
  run test -f "$root_message_file"
  [ "$status" -eq 0 ]
  run test -f "$root_retry_message_file"
  [ "$status" -eq 0 ]
  run test ! -f "$worktree_message_file"
  [ "$status" -eq 0 ]
  run test ! -f "$worktree_retry_message_file"
  [ "$status" -eq 0 ]

  run rg -F -n -- "exec --dangerously-bypass-approvals-and-sandbox --json -o /" "$RUNOQ_TEST_FAKE_CODEX_LOG"
  [ "$status" -eq 0 ]
  run rg -F -n -- "exec resume thread-issue-runner-1 --json -o /" "$RUNOQ_TEST_FAKE_CODEX_LOG"
  [ "$status" -eq 0 ]
}

@test "issue-runner enforces bounded schema retry cutoff" {
  remote_dir="$TEST_TMPDIR/direct-runner-cutoff-remote.git"
  project_dir="$TEST_TMPDIR/direct-runner-cutoff-project"
  prepare_issue_repo "$remote_dir" "$project_dir"

  config_path="$TEST_TMPDIR/direct-runner-cutoff-config.json"
  write_run_config "$config_path"

  fake_codex="$TEST_TMPDIR/fake-codex-schema-cutoff"
  write_fake_codex_always_schema_invalid_bin "$fake_codex"
  export RUNOQ_TEST_FAKE_CODEX_LOG="$TEST_TMPDIR/fake-codex-schema-cutoff.log"
  export RUNOQ_TEST_FAKE_CODEX_STATE="$TEST_TMPDIR/fake-codex-schema-cutoff.state"
  export RUNOQ_TEST_DEV_COMMAND=''

  spec_path="$TEST_TMPDIR/direct-runner-cutoff-spec.md"
  cat >"$spec_path" <<'EOF'
## Acceptance Criteria
- [ ] Add queue implementation
EOF

  payload_file="$TEST_TMPDIR/direct-runner-cutoff-payload.json"
  cat >"$payload_file" <<EOF
{
  "issueNumber": 42,
  "prNumber": 87,
  "worktree": "$project_dir",
  "branch": "main",
  "specPath": "$spec_path",
  "repo": "owner/repo",
  "maxRounds": 1,
  "maxTokenBudget": 500000,
  "guidelines": []
}
EOF

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="shell" RUNOQ_ISSUE_RUNNER_IMPLEMENTATION="shell" RUNOQ_CODEX_BIN="'"$fake_codex"'" RUNOQ_CONFIG="'"$config_path"'" "'"$RUNOQ_ROOT"'/scripts/issue-runner.sh" run "'"$payload_file"'"'
  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "fail" ]

  [ "$(cat "$RUNOQ_TEST_FAKE_CODEX_STATE")" = "3" ]
  [ "$(printf '%s' "$output" | jq -r '.verificationFailures[0]')" = "codex payload schema invalid after 2 resume attempt(s)" ]
  run rg -F -n "exec resume thread-issue-runner-bad --json -o" "$RUNOQ_TEST_FAKE_CODEX_LOG"
  [ "$status" -eq 0 ]
}

@test "acceptance parity: run --issue no-commit escalation matches shell and runtime" {
  shell_remote="$TEST_TMPDIR/shell/remote.git"
  shell_project="$TEST_TMPDIR/shell/project"
  runtime_remote="$TEST_TMPDIR/runtime/remote.git"
  runtime_project="$TEST_TMPDIR/runtime/project"
  mkdir -p "$TEST_TMPDIR/shell" "$TEST_TMPDIR/runtime"
  prepare_issue_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_issue_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/config.json"
  write_run_config "$config_path"

  issue_body="$(happy_issue_body)"
  issue_comment="Escalated to human review: post-dev verification failed: no new commits were created, branch tip is not pushed to origin."
  shell_scenario="$TEST_TMPDIR/shell-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-scenario.json"
  write_needs_review_scenario "$shell_scenario" "$issue_body" "$issue_comment"
  write_needs_review_scenario "$runtime_scenario" "$issue_body" "$issue_comment"

  export RUNOQ_TEST_DEV_COMMAND='true'
  export RUNOQ_TEST_CODEX_OUTPUT_FILE=""

  run_issue_with_impl "shell" "$shell_project" "$config_path" "$shell_scenario" "$TEST_TMPDIR/shell-gh.state" "$TEST_TMPDIR/shell-gh.log" "$TEST_TMPDIR/shell-capture"
  shell_status="$RUN_STATUS"
  shell_output="$RUN_OUTPUT"

  run_issue_with_impl "runtime" "$runtime_project" "$config_path" "$runtime_scenario" "$TEST_TMPDIR/runtime-gh.state" "$TEST_TMPDIR/runtime-gh.log" "$TEST_TMPDIR/runtime-capture"
  runtime_status="$RUN_STATUS"
  runtime_output="$RUN_OUTPUT"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_run_issue_output "$shell_output")" = "$(normalize_run_issue_output "$runtime_output")" ]
  [ "$(printf '%s' "$shell_output" | jq -r '.worktree | split("/")[-1]')" = "runoq-wt-42" ]
  [ "$(printf '%s' "$runtime_output" | jq -r '.worktree | split("/")[-1]')" = "runoq-wt-42" ]

  assert_log_contains "$TEST_TMPDIR/shell-gh.log" "issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review"
  assert_log_contains "$TEST_TMPDIR/runtime-gh.log" "issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review"
  assert_log_contains "$TEST_TMPDIR/shell-gh.log" "pr edit 87 --repo owner/repo --add-reviewer username --add-assignee username"
  assert_log_contains "$TEST_TMPDIR/runtime-gh.log" "pr edit 87 --repo owner/repo --add-reviewer username --add-assignee username"
  assert_capture_contains "$TEST_TMPDIR/shell-capture" "Post-dev verification failed: no new commits were created, branch tip is not pushed to origin"
  assert_capture_contains "$TEST_TMPDIR/runtime-capture" "Post-dev verification failed: no new commits were created, branch tip is not pushed to origin"
  assert_capture_contains "$TEST_TMPDIR/shell-capture" "Assigned to @username for human review. Reason: post-dev verification failed: no new commits were created, branch tip is not pushed to origin."
  assert_capture_contains "$TEST_TMPDIR/runtime-capture" "Assigned to @username for human review. Reason: post-dev verification failed: no new commits were created, branch tip is not pushed to origin."
}

@test "acceptance parity: run --issue missing-push escalation matches shell and runtime" {
  shell_remote="$TEST_TMPDIR/shell/remote.git"
  shell_project="$TEST_TMPDIR/shell/project"
  runtime_remote="$TEST_TMPDIR/runtime/remote.git"
  runtime_project="$TEST_TMPDIR/runtime/project"
  mkdir -p "$TEST_TMPDIR/shell" "$TEST_TMPDIR/runtime"
  prepare_issue_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_issue_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/config.json"
  write_run_config "$config_path"

  issue_body="$(happy_issue_body)"
  issue_comment="Escalated to human review: post-dev verification failed: branch tip is not pushed to origin."
  shell_scenario="$TEST_TMPDIR/shell-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-scenario.json"
  write_needs_review_scenario "$shell_scenario" "$issue_body" "$issue_comment"
  write_needs_review_scenario "$runtime_scenario" "$issue_body" "$issue_comment"

  export RUNOQ_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null'
  export RUNOQ_TEST_CODEX_OUTPUT_FILE=""

  run_issue_with_impl "shell" "$shell_project" "$config_path" "$shell_scenario" "$TEST_TMPDIR/shell-gh.state" "$TEST_TMPDIR/shell-gh.log" "$TEST_TMPDIR/shell-capture"
  shell_status="$RUN_STATUS"
  shell_output="$RUN_OUTPUT"

  run_issue_with_impl "runtime" "$runtime_project" "$config_path" "$runtime_scenario" "$TEST_TMPDIR/runtime-gh.state" "$TEST_TMPDIR/runtime-gh.log" "$TEST_TMPDIR/runtime-capture"
  runtime_status="$RUN_STATUS"
  runtime_output="$RUN_OUTPUT"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_run_issue_output "$shell_output")" = "$(normalize_run_issue_output "$runtime_output")" ]
  [ "$(printf '%s' "$shell_output" | jq -r '.worktree | split("/")[-1]')" = "runoq-wt-42" ]
  [ "$(printf '%s' "$runtime_output" | jq -r '.worktree | split("/")[-1]')" = "runoq-wt-42" ]

  assert_log_contains "$TEST_TMPDIR/shell-gh.log" "issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review"
  assert_log_contains "$TEST_TMPDIR/runtime-gh.log" "issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review"
  assert_capture_contains "$TEST_TMPDIR/shell-capture" "Post-dev verification failed: branch tip is not pushed to origin"
  assert_capture_contains "$TEST_TMPDIR/runtime-capture" "Post-dev verification failed: branch tip is not pushed to origin"
}

@test "acceptance parity: run --issue malformed-payload recovery matches shell and runtime" {
  shell_remote="$TEST_TMPDIR/shell/remote.git"
  shell_project="$TEST_TMPDIR/shell/project"
  runtime_remote="$TEST_TMPDIR/runtime/remote.git"
  runtime_project="$TEST_TMPDIR/runtime/project"
  mkdir -p "$TEST_TMPDIR/shell" "$TEST_TMPDIR/runtime"
  prepare_issue_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_issue_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  config_path="$TEST_TMPDIR/config.json"
  write_run_config "$config_path"

  issue_body="$(happy_issue_body)"
  issue_comment="Escalated to human review: post-dev verification failed: payload reported failing tests, payload reported failing build."
  shell_scenario="$TEST_TMPDIR/shell-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-scenario.json"
  write_malformed_payload_scenario "$shell_scenario" "$issue_body" "$issue_comment"
  write_malformed_payload_scenario "$runtime_scenario" "$issue_body" "$issue_comment"

  export RUNOQ_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  export RUNOQ_TEST_CODEX_OUTPUT_FILE="$RUNOQ_ROOT/test/fixtures/payloads/codex-return-malformed.txt"

  run_issue_with_impl "shell" "$shell_project" "$config_path" "$shell_scenario" "$TEST_TMPDIR/shell-gh.state" "$TEST_TMPDIR/shell-gh.log" "$TEST_TMPDIR/shell-capture"
  shell_status="$RUN_STATUS"
  shell_output="$RUN_OUTPUT"

  run_issue_with_impl "runtime" "$runtime_project" "$config_path" "$runtime_scenario" "$TEST_TMPDIR/runtime-gh.state" "$TEST_TMPDIR/runtime-gh.log" "$TEST_TMPDIR/runtime-capture"
  runtime_status="$RUN_STATUS"
  runtime_output="$RUN_OUTPUT"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_run_issue_output "$shell_output")" = "$(normalize_run_issue_output "$runtime_output")" ]
  [ "$(printf '%s' "$shell_output" | jq -r '.worktree | split("/")[-1]')" = "runoq-wt-42" ]
  [ "$(printf '%s' "$runtime_output" | jq -r '.worktree | split("/")[-1]')" = "runoq-wt-42" ]

  assert_log_contains "$TEST_TMPDIR/shell-gh.log" "issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review"
  assert_log_contains "$TEST_TMPDIR/runtime-gh.log" "issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review"
  assert_capture_contains "$TEST_TMPDIR/shell-capture" "Codex payload required reconstruction. Source=synthetic"
  assert_capture_contains "$TEST_TMPDIR/runtime-capture" "Codex payload required reconstruction. Source=synthetic"
  assert_capture_contains "$TEST_TMPDIR/shell-capture" "payload_missing_or_malformed"
  assert_capture_contains "$TEST_TMPDIR/runtime-capture" "payload_missing_or_malformed"
}
