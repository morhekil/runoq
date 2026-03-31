#!/usr/bin/env bats

load test_helper

normalize_json() {
  printf '%s' "$1" | jq -S .
}

write_fake_runtime_issue_queue_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'FAKE_RUNTIME:%s %s\n' "$1" "${*:2}"
EOF
  chmod +x "$path"
}

@test "issue queue wrapper defaults to runtime and preserves explicit shell override" {
  project_dir="$TEST_TMPDIR/default-wrapper-project"
  make_git_repo "$project_dir"

  fake_runtime_bin="$TEST_TMPDIR/fake-runtime-issue-queue"
  write_fake_runtime_issue_queue_bin "$fake_runtime_bin"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_ISSUE_QUEUE_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" list owner/repo runoq:ready'
  [ "$status" -eq 0 ]
  [ "$output" = "FAKE_RUNTIME:__issue_queue list owner/repo runoq:ready" ]

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_ISSUE_QUEUE_IMPLEMENTATION="shell" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" list owner/repo'
  [ "$status" -ne 0 ]
  [[ "$output" != *"FAKE_RUNTIME:"* ]]
  [[ "$output" == *"Usage:"* ]]
  [[ "$output" == *"gh-issue-queue.sh list"* ]]
}

@test "acceptance parity: issue queue list matches shell and runtime contracts" {
  prepare_runtime_bin

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo", "--label runoq:ready"],
    "stdout_file": "$(fixture_path "issues/list-ready.json")"
  }
]
EOF

  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/shell.log" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" list owner/repo runoq:ready 2>"'"$TEST_TMPDIR"'/shell.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/runtime.log" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" list owner/repo runoq:ready 2>"'"$TEST_TMPDIR"'/runtime.err"'
  runtime_status="$status"
  runtime_output="$output"

  run cat "$TEST_TMPDIR/shell.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime.err"
  runtime_err="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_json "$shell_output")" = "$(normalize_json "$runtime_output")" ]
  [ "$shell_err" = "$runtime_err" ]
}

@test "acceptance parity: issue queue next matches shell and runtime contracts" {
  prepare_runtime_bin

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo", "--label runoq:ready"],
    "stdout_file": "$(fixture_path "issues/next-blocked-list.json")"
  },
  {
    "contains": ["issue", "view", "5", "--repo owner/repo"],
    "stdout_file": "$(fixture_path "issues/dependency-in-progress.json")"
  }
]
EOF

  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/shell.log" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" next owner/repo runoq:ready 2>"'"$TEST_TMPDIR"'/shell.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/runtime.log" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" next owner/repo runoq:ready 2>"'"$TEST_TMPDIR"'/runtime.err"'
  runtime_status="$status"
  runtime_output="$output"

  run cat "$TEST_TMPDIR/shell.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime.err"
  runtime_err="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_json "$shell_output")" = "$(normalize_json "$runtime_output")" ]
  [ "$shell_err" = "$runtime_err" ]
}

@test "acceptance parity: issue queue set-status and unknown-status failure match shell and runtime" {
  prepare_runtime_bin

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "view", "42", "--repo owner/repo", "--json labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"},{\"name\":\"bug\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo owner/repo", "--remove-label runoq:ready", "--add-label runoq:in-progress"],
    "stdout": ""
  }
]
EOF

  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/shell.log" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" set-status owner/repo 42 in-progress 2>"'"$TEST_TMPDIR"'/shell.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/runtime.log" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" set-status owner/repo 42 in-progress 2>"'"$TEST_TMPDIR"'/runtime.err"'
  runtime_status="$status"
  runtime_output="$output"

  run cat "$TEST_TMPDIR/shell.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime.err"
  runtime_err="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_json "$shell_output")" = "$(normalize_json "$runtime_output")" ]
  [ "$shell_err" = "$runtime_err" ]

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" set-status owner/repo 42 impossible 2>"'"$TEST_TMPDIR"'/shell-fail.err"'
  shell_fail_status="$status"
  shell_fail_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" set-status owner/repo 42 impossible 2>"'"$TEST_TMPDIR"'/runtime-fail.err"'
  runtime_fail_status="$status"
  runtime_fail_output="$output"

  run cat "$TEST_TMPDIR/shell-fail.err"
  shell_fail_err="$output"
  run cat "$TEST_TMPDIR/runtime-fail.err"
  runtime_fail_err="$output"

  [ "$shell_fail_status" -eq "$runtime_fail_status" ]
  [ "$shell_fail_status" -ne 0 ]
  [ "$shell_fail_output" = "$runtime_fail_output" ]
  [ "$shell_fail_err" = "$runtime_fail_err" ]
}

@test "acceptance parity: issue queue create and epic-status match shell and runtime contracts" {
  prepare_runtime_bin

  create_scenario="$TEST_TMPDIR/create-scenario.json"
  write_fake_gh_scenario "$create_scenario" <<EOF
[
  {
    "contains": ["issue", "create", "--repo owner/repo", "--title Implement queue", "--label runoq:ready"],
    "stdout": "https://github.com/owner/repo/issues/99"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/99", "--jq", ".id"],
    "stdout": "12345"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/77/sub_issues", "--method", "POST", "-F", "sub_issue_id=12345"],
    "stdout": ""
  }
]
EOF

  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$create_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-create.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/shell-create.log" FAKE_GH_CAPTURE_DIR="'"$TEST_TMPDIR"'/shell-capture" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" create owner/repo "Implement queue" "## Acceptance Criteria\n\n- [ ] Works." --depends-on 12,14 --priority 1 --estimated-complexity low --complexity-rationale "touches queue scheduling" --parent-epic 77 2>"'"$TEST_TMPDIR"'/shell-create.err"'
  shell_create_status="$status"
  shell_create_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$create_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-create.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/runtime-create.log" FAKE_GH_CAPTURE_DIR="'"$TEST_TMPDIR"'/runtime-capture" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" create owner/repo "Implement queue" "## Acceptance Criteria\n\n- [ ] Works." --depends-on 12,14 --priority 1 --estimated-complexity low --complexity-rationale "touches queue scheduling" --parent-epic 77 2>"'"$TEST_TMPDIR"'/runtime-create.err"'
  runtime_create_status="$status"
  runtime_create_output="$output"

  run cat "$TEST_TMPDIR/shell-create.err"
  shell_create_err="$output"
  run cat "$TEST_TMPDIR/runtime-create.err"
  runtime_create_err="$output"

  [ "$shell_create_status" -eq "$runtime_create_status" ]
  [ "$shell_create_status" -eq 0 ]
  [ "$(normalize_json "$shell_create_output")" = "$(normalize_json "$runtime_create_output")" ]
  [ "$shell_create_err" = "$runtime_create_err" ]

  run diff -u "$TEST_TMPDIR/shell-capture/0.body" "$TEST_TMPDIR/runtime-capture/0.body"
  [ "$status" -eq 0 ]

  epic_scenario="$TEST_TMPDIR/epic-scenario.json"
  write_fake_gh_scenario "$epic_scenario" <<EOF
[
  {
    "contains": ["api", "repos/owner/repo/issues/77/sub_issues", "--paginate"],
    "stdout": "[{\"number\":12,\"labels\":[{\"name\":\"runoq:done\"}]},{\"number\":14,\"labels\":[{\"name\":\"runoq:ready\"}]}]"
  }
]
EOF

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=shell FAKE_GH_SCENARIO="'"$epic_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-epic.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/shell-epic.log" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" epic-status owner/repo 77 2>"'"$TEST_TMPDIR"'/shell-epic.err"'
  shell_epic_status="$status"
  shell_epic_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_NO_AUTO_TOKEN=1 RUNOQ_RUNTIME_BIN="'"$RUNOQ_RUNTIME_BIN"'" RUNOQ_ISSUE_QUEUE_IMPLEMENTATION=runtime FAKE_GH_SCENARIO="'"$epic_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-epic.state" FAKE_GH_LOG="'"$TEST_TMPDIR"'/runtime-epic.log" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" "'"$RUNOQ_ROOT"'/scripts/gh-issue-queue.sh" epic-status owner/repo 77 2>"'"$TEST_TMPDIR"'/runtime-epic.err"'
  runtime_epic_status="$status"
  runtime_epic_output="$output"

  run cat "$TEST_TMPDIR/shell-epic.err"
  shell_epic_err="$output"
  run cat "$TEST_TMPDIR/runtime-epic.err"
  runtime_epic_err="$output"

  [ "$shell_epic_status" -eq "$runtime_epic_status" ]
  [ "$shell_epic_status" -eq 0 ]
  [ "$(normalize_json "$shell_epic_output")" = "$(normalize_json "$runtime_epic_output")" ]
  [ "$shell_epic_err" = "$runtime_epic_err" ]
}
