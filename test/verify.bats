#!/usr/bin/env bats

load test_helper

write_verify_config() {
  local path="$1"
  cat >"$path" <<'EOF'
{
  "labels": {
    "ready": "agendev:ready",
    "inProgress": "agendev:in-progress",
    "done": "agendev:done",
    "needsReview": "agendev:needs-human-review",
    "blocked": "agendev:blocked",
    "maintenanceReview": "agendev:maintenance-review"
  },
  "identity": {
    "appSlug": "agendev",
    "handle": "agendev"
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
  "branchPrefix": "agendev/",
  "worktreePrefix": "agendev-wt-",
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

prepare_verify_repo() {
  local remote_dir="$1"
  local local_dir="$2"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" checkout -b agendev/42-test >/dev/null 2>&1
  base_sha="$(git -C "$local_dir" rev-parse HEAD)"
  mkdir -p "$local_dir/src"
  echo "console.log('ok')" >"$local_dir/src/app.ts"
  git -C "$local_dir" add src/app.ts
  git -C "$local_dir" commit -m "Add app" >/dev/null
  git -C "$local_dir" push -u origin agendev/42-test >/dev/null 2>&1
  commit_sha="$(git -C "$local_dir" rev-parse HEAD)"
  printf '%s\n%s\n' "$base_sha" "$commit_sha"
}

@test "verify round succeeds when commits files push and commands all match" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  mapfile -t shas < <(prepare_verify_repo "$remote_dir" "$local_dir")
  base_sha="${shas[0]}"
  commit_sha="${shas[1]}"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  export AGENDEV_CONFIG="$config_file"

  payload_file="$TEST_TMPDIR/payload.json"
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

  run "$AGENDEV_ROOT/scripts/verify.sh" round "$local_dir" agendev/42-test "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ok')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.review_allowed')" = "true" ]
}

@test "verify round reports mismatched commits and files" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  mapfile -t shas < <(prepare_verify_repo "$remote_dir" "$local_dir")
  base_sha="${shas[0]}"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  export AGENDEV_CONFIG="$config_file"

  payload_file="$TEST_TMPDIR/payload.json"
  cat >"$payload_file" <<'EOF'
{
  "status": "completed",
  "commits_pushed": ["deadbeef"],
  "commit_range": "deadbeef..deadbeef",
  "files_changed": ["src/other.ts"],
  "files_added": [],
  "files_deleted": [],
  "tests_run": true,
  "tests_passed": true,
  "test_summary": "ok",
  "build_passed": true,
  "blockers": [],
  "notes": ""
}
EOF

  run "$AGENDEV_ROOT/scripts/verify.sh" round "$local_dir" agendev/42-test "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.review_allowed')" = "false" ]
  [[ "$output" == *"missing commit deadbeef"* ]]
  [[ "$output" == *"file lists do not match ground truth"* ]]
}

@test "verify round reports missing remote push and failing commands" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" checkout -b agendev/42-test >/dev/null 2>&1
  base_sha="$(git -C "$local_dir" rev-parse HEAD)"
  echo "console.log('ok')" >"$local_dir/src.ts"
  git -C "$local_dir" add src.ts
  git -C "$local_dir" commit -m "Add src" >/dev/null
  commit_sha="$(git -C "$local_dir" rev-parse HEAD)"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  jq '.verification.testCommand = "false" | .verification.buildCommand = "false"' "$config_file" >"$TEST_TMPDIR/config-fail.json"
  export AGENDEV_CONFIG="$TEST_TMPDIR/config-fail.json"

  payload_file="$TEST_TMPDIR/payload.json"
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

  run "$AGENDEV_ROOT/scripts/verify.sh" round "$local_dir" agendev/42-test "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [[ "$output" == *"branch tip is not pushed to origin"* ]]
  [[ "$output" == *"test command failed"* ]]
  [[ "$output" == *"build command failed"* ]]
}

@test "verify round fails fast when verification commands are not configured" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  mapfile -t shas < <(prepare_verify_repo "$remote_dir" "$local_dir")
  base_sha="${shas[0]}"
  commit_sha="${shas[1]}"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  jq '.verification.testCommand = ""' "$config_file" >"$TEST_TMPDIR/config-bad.json"
  export AGENDEV_CONFIG="$TEST_TMPDIR/config-bad.json"

  payload_file="$TEST_TMPDIR/payload.json"
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

  run "$AGENDEV_ROOT/scripts/verify.sh" round "$local_dir" agendev/42-test "$base_sha" "$payload_file"

  [ "$status" -ne 0 ]
  [[ "$output" == *"verification.testCommand is not configured"* ]]
}
