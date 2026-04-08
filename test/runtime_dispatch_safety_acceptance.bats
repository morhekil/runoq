#!/usr/bin/env bats

load test_helper

prepare_dispatch_repo() {
  local remote_dir="$1"
  local local_dir="$2"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
}

write_issue_state_file() {
  local path="$1"
  local issue="$2"
  local phase="$3"
  local round="$4"
  local branch="$5"
  local pr_number="$6"

  cat >"$path" <<EOF
{
  "issue": $issue,
  "phase": "$phase",
  "round": $round,
  "branch": "$branch",
  "pr_number": $pr_number,
  "updated_at": "2026-03-17T00:00:00Z"
}
EOF
}

issue_body_with_meta() {
  cat <<'EOF'
## Acceptance Criteria

- [ ] Works.
EOF
}

normalize_json() {
  printf '%s' "$1" | jq -S -c .
}

write_fake_runtime_dispatch_safety_bin() {
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

@test "dispatch-safety wrapper defaults to runtime and ignores explicit shell override" {
  project_dir="$TEST_TMPDIR/default-wrapper-project"
  make_git_repo "$project_dir"

  fake_runtime_bin="$TEST_TMPDIR/fake-runtime-dispatch-safety"
  write_fake_runtime_dispatch_safety_bin "$fake_runtime_bin"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/dispatch-safety.sh" reconcile owner/repo'
  [ "$status" -eq 0 ]
  [ "$output" = "FAKE_RUNTIME:__dispatch_safety reconcile owner/repo" ]

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION="shell" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/dispatch-safety.sh"'
  [ "$status" -eq 0 ]
  [ "$output" = "FAKE_RUNTIME:__dispatch_safety " ]
}

@test "dispatch-safety wrapper go fallback runs from RUNOQ_ROOT when runtime bin is unset" {
  project_dir="$TEST_TMPDIR/default-wrapper-go-cwd-project"
  make_git_repo "$project_dir"

  fake_go_bin="$TEST_TMPDIR/fake-go-dispatch-safety"
  write_fake_go_bin "$fake_go_bin"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="" RUNOQ_GO_BIN="'"$fake_go_bin"'" "'"$RUNOQ_ROOT"'/scripts/dispatch-safety.sh" reconcile owner/repo'
  [ "$status" -eq 0 ]
  [[ "$output" == *"FAKE_GO_CWD:$RUNOQ_ROOT"* ]]
  [[ "$output" == *"FAKE_GO_ARGS:run $RUNOQ_ROOT/cmd/runoq-runtime __dispatch_safety reconcile owner/repo"* ]]
}

@test "acceptance parity: dispatch-safety reconcile matches shell and runtime contract" {
  shell_remote="$TEST_TMPDIR/shell-remote.git"
  shell_project="$TEST_TMPDIR/shell-project"
  runtime_remote="$TEST_TMPDIR/runtime-remote.git"
  runtime_project="$TEST_TMPDIR/runtime-project"
  prepare_dispatch_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_dispatch_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  shell_state_dir="$shell_project/.runoq/state"
  runtime_state_dir="$runtime_project/.runoq/state"
  mkdir -p "$shell_state_dir" "$runtime_state_dir"

  git -C "$shell_project" checkout -b runoq/42-implement-queue >/dev/null 2>&1
  echo "work" >"$shell_project/work.txt"
  git -C "$shell_project" add work.txt
  git -C "$shell_project" commit -m "Work in progress" >/dev/null
  git -C "$shell_project" push -u origin runoq/42-implement-queue >/dev/null 2>&1

  git -C "$runtime_project" checkout -b runoq/42-implement-queue >/dev/null 2>&1
  echo "work" >"$runtime_project/work.txt"
  git -C "$runtime_project" add work.txt
  git -C "$runtime_project" commit -m "Work in progress" >/dev/null
  git -C "$runtime_project" push -u origin runoq/42-implement-queue >/dev/null 2>&1

  write_issue_state_file "$shell_state_dir/42.json" 42 REVIEW 2 runoq/42-implement-queue 87
  write_issue_state_file "$runtime_state_dir/42.json" 42 REVIEW 2 runoq/42-implement-queue 87

  shell_scenario="$TEST_TMPDIR/shell-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<'EOF'
[
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "number"],
    "stdout": "{\"number\":87}"
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: REVIEW round 2. Resuming."],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: REVIEW round 2. Resuming."],
    "stdout": ""
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-gh.log"

  run bash -lc 'cd "'"$shell_project"'" && GH_TOKEN=existing-token FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-gh.state" FAKE_GH_LOG="'"$shell_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" TARGET_ROOT="'"$shell_project"'" RUNOQ_STATE_DIR="'"$shell_state_dir"'" RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/dispatch-safety.sh" reconcile owner/repo 2>"'"$TEST_TMPDIR"'/shell-reconcile.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$runtime_project"'" && GH_TOKEN=existing-token FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-gh.state" FAKE_GH_LOG="'"$runtime_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" TARGET_ROOT="'"$runtime_project"'" RUNOQ_STATE_DIR="'"$runtime_state_dir"'" RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/dispatch-safety.sh" reconcile owner/repo 2>"'"$TEST_TMPDIR"'/runtime-reconcile.err"'
  runtime_status="$status"
  runtime_output="$output"

  run cat "$TEST_TMPDIR/shell-reconcile.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime-reconcile.err"
  runtime_err="$output"
  run cat "$shell_log"
  shell_gh_log="$output"
  run cat "$runtime_log"
  runtime_gh_log="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_json "$shell_output")" = "$(normalize_json "$runtime_output")" ]
  [ "$shell_err" = "$runtime_err" ]
  [ "$shell_gh_log" = "$runtime_gh_log" ]
}

@test "acceptance parity: dispatch-safety eligibility matches shell and runtime contract" {
  shell_remote="$TEST_TMPDIR/shell-remote.git"
  shell_project="$TEST_TMPDIR/shell-project"
  runtime_remote="$TEST_TMPDIR/runtime-remote.git"
  runtime_project="$TEST_TMPDIR/runtime-project"
  prepare_dispatch_repo "$shell_remote" "$shell_project"
  rm -rf "$TEST_TMPDIR/seed-repo"
  prepare_dispatch_repo "$runtime_remote" "$runtime_project"
  prepare_runtime_bin

  body="$(issue_body_with_meta "[]")"
  shell_scenario="$TEST_TMPDIR/shell-eligibility-scenario.json"
  runtime_scenario="$TEST_TMPDIR/runtime-eligibility-scenario.json"
  write_fake_gh_scenario "$shell_scenario" <<EOF
[
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue", "--json", "number,url"],
    "stdout": "[]"
  }
]
EOF
  cp "$shell_scenario" "$runtime_scenario"

  shell_log="$TEST_TMPDIR/shell-eligibility-gh.log"
  runtime_log="$TEST_TMPDIR/runtime-eligibility-gh.log"

  run bash -lc 'cd "'"$shell_project"'" && GH_TOKEN=existing-token FAKE_GH_SCENARIO="'"$shell_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/shell-eligibility-gh.state" FAKE_GH_LOG="'"$shell_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" TARGET_ROOT="'"$shell_project"'" RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/dispatch-safety.sh" eligibility owner/repo 42 2>"'"$TEST_TMPDIR"'/shell-eligibility.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$runtime_project"'" && GH_TOKEN=existing-token FAKE_GH_SCENARIO="'"$runtime_scenario"'" FAKE_GH_STATE="'"$TEST_TMPDIR"'/runtime-eligibility-gh.state" FAKE_GH_LOG="'"$runtime_log"'" GH_BIN="'"$RUNOQ_ROOT"'/test/helpers/gh" TARGET_ROOT="'"$runtime_project"'" RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/dispatch-safety.sh" eligibility owner/repo 42 2>"'"$TEST_TMPDIR"'/runtime-eligibility.err"'
  runtime_status="$status"
  runtime_output="$output"

  run cat "$TEST_TMPDIR/shell-eligibility.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime-eligibility.err"
  runtime_err="$output"
  run cat "$shell_log"
  shell_gh_log="$output"
  run cat "$runtime_log"
  runtime_gh_log="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalize_json "$shell_output")" = "$(normalize_json "$runtime_output")" ]
  [ "$shell_err" = "$runtime_err" ]
  [ "$shell_gh_log" = "$runtime_gh_log" ]
}
