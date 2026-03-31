#!/usr/bin/env bats

load test_helper

write_verify_config() {
  local path="$1"
  local test_command="${2:-true}"
  local build_command="${3:-true}"
  cat >"$path" <<EOF
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
    "testCommand": "$test_command",
    "buildCommand": "$build_command"
  },
  "stall": {
    "timeoutSeconds": 600
  }
}
EOF
}

prepare_verify_repo() {
  local remote_dir="$1"
  local local_dir="$2"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" checkout -b runoq/42-test >/dev/null 2>&1
  base_sha="$(git -C "$local_dir" rev-parse HEAD)"
  mkdir -p "$local_dir/src"
  echo "console.log('ok')" >"$local_dir/src/app.ts"
  git -C "$local_dir" add src/app.ts
  git -C "$local_dir" commit -m "Add app" >/dev/null
  git -C "$local_dir" push -u origin runoq/42-test >/dev/null 2>&1
  commit_sha="$(git -C "$local_dir" rev-parse HEAD)"
  printf '%s\n%s\n' "$base_sha" "$commit_sha"
}

normalized_json() {
  printf '%s' "$1" | jq -S -c .
}

write_fake_runtime_verify_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'FAKE_RUNTIME:%s %s\n' "$1" "${*:2}"
EOF
  chmod +x "$path"
}

@test "verify wrapper defaults to runtime and preserves explicit shell override" {
  local_dir="$TEST_TMPDIR/default-wrapper-local"
  make_git_repo "$local_dir"

  fake_runtime_bin="$TEST_TMPDIR/fake-runtime-verify"
  write_fake_runtime_verify_bin "$fake_runtime_bin"

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_VERIFY_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/verify.sh" round one two three four'
  [ "$status" -eq 0 ]
  [ "$output" = "FAKE_RUNTIME:__verify round one two three four" ]

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_VERIFY_IMPLEMENTATION="shell" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/verify.sh" round one two three'
  [ "$status" -ne 0 ]
  [[ "$output" != *"FAKE_RUNTIME:"* ]]
  [[ "$output" == *"Usage:"* ]]
  [[ "$output" == *"verify.sh round"* ]]
}

@test "acceptance parity: verify round success matches shell and runtime contract" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  shas="$(prepare_verify_repo "$remote_dir" "$local_dir")"
  base_sha="$(printf '%s\n' "$shas" | sed -n '1p')"
  commit_sha="$(printf '%s\n' "$shas" | sed -n '2p')"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file" "true" "true"
  payload_file="$TEST_TMPDIR/payload.json"
  prepare_runtime_bin

  cat >"$payload_file" <<EOF
{
  "status": "completed",
  "commits_pushed": ["$commit_sha"],
  "commit_range": "$commit_sha..$commit_sha",
  "files_changed": [],
  "files_added": ["src/app.ts"],
  "files_deleted": [],
  "tests_run": true,
  "tests_passed": true,
  "test_summary": "ok",
  "build_passed": true,
  "blockers": [],
  "notes": ""
}
EOF

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_CONFIG="'"$config_file"'" RUNOQ_VERIFY_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/verify.sh" round "'"$local_dir"'" runoq/42-test "'"$base_sha"'" "'"$payload_file"'" 2>"'"$TEST_TMPDIR"'/shell.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_CONFIG="'"$config_file"'" RUNOQ_VERIFY_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/verify.sh" round "'"$local_dir"'" runoq/42-test "'"$base_sha"'" "'"$payload_file"'" 2>"'"$TEST_TMPDIR"'/runtime.err"'
  runtime_status="$status"
  runtime_output="$output"

  run cat "$TEST_TMPDIR/shell.err"
  shell_err="$output"
  run cat "$TEST_TMPDIR/runtime.err"
  runtime_err="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalized_json "$shell_output")" = "$(normalized_json "$runtime_output")" ]
  [ "$shell_err" = "$runtime_err" ]
}

@test "acceptance parity: verify round failure details match shell and runtime contract" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" checkout -b runoq/42-test >/dev/null 2>&1
  base_sha="$(git -C "$local_dir" rev-parse HEAD)"
  echo "console.log('ok')" >"$local_dir/src.ts"
  git -C "$local_dir" add src.ts
  git -C "$local_dir" commit -m "Add src" >/dev/null
  commit_sha="$(git -C "$local_dir" rev-parse HEAD)"
  config_file="$TEST_TMPDIR/config-fail.json"
  write_verify_config "$config_file" "false" "false"
  payload_file="$TEST_TMPDIR/payload.json"
  prepare_runtime_bin

  cat >"$payload_file" <<EOF
{
  "status": "completed",
  "commits_pushed": ["$commit_sha"],
  "commit_range": "$commit_sha..$commit_sha",
  "files_changed": [],
  "files_added": ["src.ts"],
  "files_deleted": [],
  "tests_run": true,
  "tests_passed": true,
  "test_summary": "ok",
  "build_passed": true,
  "blockers": [],
  "notes": ""
}
EOF

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_CONFIG="'"$config_file"'" RUNOQ_VERIFY_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/verify.sh" round "'"$local_dir"'" runoq/42-test "'"$base_sha"'" "'"$payload_file"'"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_CONFIG="'"$config_file"'" RUNOQ_VERIFY_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/verify.sh" round "'"$local_dir"'" runoq/42-test "'"$base_sha"'" "'"$payload_file"'"'
  runtime_status="$status"
  runtime_output="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalized_json "$shell_output")" = "$(normalized_json "$runtime_output")" ]
  [ "$(printf '%s' "$runtime_output" | jq -r '.ok')" = "false" ]
}

@test "acceptance parity: verify integrate tamper detection matches shell and runtime contract" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" checkout -b runoq/epic-test >/dev/null 2>&1
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file" "true" "true"
  prepare_runtime_bin

  mkdir -p "$local_dir/test"
  echo "test('integration', () => {})" >"$local_dir/test/integration.test.js"
  git -C "$local_dir" add test/integration.test.js
  git -C "$local_dir" commit -m "bar-setter: epic criteria" >/dev/null
  criteria_commit="$(git -C "$local_dir" rev-parse HEAD)"

  echo "test('integration', () => { /* hacked */ })" >"$local_dir/test/integration.test.js"
  git -C "$local_dir" add test/integration.test.js
  git -C "$local_dir" commit -m "Tamper criteria" >/dev/null

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_CONFIG="'"$config_file"'" RUNOQ_VERIFY_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/verify.sh" integrate "'"$local_dir"'" "'"$criteria_commit"'"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$local_dir"'" && RUNOQ_CONFIG="'"$config_file"'" RUNOQ_VERIFY_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/verify.sh" integrate "'"$local_dir"'" "'"$criteria_commit"'"'
  runtime_status="$status"
  runtime_output="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_status" -eq 0 ]
  [ "$(normalized_json "$shell_output")" = "$(normalized_json "$runtime_output")" ]
  [ "$(printf '%s' "$runtime_output" | jq -r '.ok')" = "false" ]
}
