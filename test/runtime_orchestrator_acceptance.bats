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
