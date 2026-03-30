#!/usr/bin/env bats

load test_helper

write_verify_config() {
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

@test "verify round succeeds when commits files push and commands all match" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  shas="$(prepare_verify_repo "$remote_dir" "$local_dir")"
  base_sha="$(printf '%s\n' "$shas" | sed -n '1p')"
  commit_sha="$(printf '%s\n' "$shas" | sed -n '2p')"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  export RUNOQ_CONFIG="$config_file"

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

  run "$RUNOQ_ROOT/scripts/verify.sh" round "$local_dir" runoq/42-test "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ok')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.review_allowed')" = "true" ]
}

@test "verify round reports mismatched commits and files" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  shas="$(prepare_verify_repo "$remote_dir" "$local_dir")"
  base_sha="$(printf '%s\n' "$shas" | sed -n '1p')"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  export RUNOQ_CONFIG="$config_file"

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

  run "$RUNOQ_ROOT/scripts/verify.sh" round "$local_dir" runoq/42-test "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.review_allowed')" = "false" ]
  [[ "$output" == *"missing commit deadbeef"* ]]
  [[ "$output" == *"file lists do not match ground truth"* ]]
}

@test "verify round reports missing remote push and failing commands" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" checkout -b runoq/42-test >/dev/null 2>&1
  base_sha="$(git -C "$local_dir" rev-parse HEAD)"
  echo "console.log('ok')" >"$local_dir/src.ts"
  git -C "$local_dir" add src.ts
  git -C "$local_dir" commit -m "Add src" >/dev/null
  commit_sha="$(git -C "$local_dir" rev-parse HEAD)"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  jq '.verification.testCommand = "false" | .verification.buildCommand = "false"' "$config_file" >"$TEST_TMPDIR/config-fail.json"
  export RUNOQ_CONFIG="$TEST_TMPDIR/config-fail.json"

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

  run "$RUNOQ_ROOT/scripts/verify.sh" round "$local_dir" runoq/42-test "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [[ "$output" == *"branch tip is not pushed to origin"* ]]
  [[ "$output" == *"test command failed"* ]]
  [[ "$output" == *"build command failed"* ]]
}

@test "verify round detects criteria tamper when criteria_commit is present" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  shas="$(prepare_verify_repo "$remote_dir" "$local_dir")"
  base_sha="$(printf '%s\n' "$shas" | sed -n '1p')"
  commit_sha="$(printf '%s\n' "$shas" | sed -n '2p')"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  export RUNOQ_CONFIG="$config_file"

  # Create a criteria commit: add a test file
  mkdir -p "$local_dir/test"
  echo "test('acceptance', () => {})" >"$local_dir/test/acceptance.test.js"
  git -C "$local_dir" add test/acceptance.test.js
  git -C "$local_dir" commit -m "bar-setter: acceptance criteria" >/dev/null
  criteria_commit="$(git -C "$local_dir" rev-parse HEAD)"

  # Now modify the criteria file (simulating codex tampering)
  echo "test('acceptance', () => { /* modified */ })" >"$local_dir/test/acceptance.test.js"
  git -C "$local_dir" add test/acceptance.test.js
  git -C "$local_dir" commit -m "Modify acceptance test" >/dev/null
  git -C "$local_dir" push origin runoq/42-test >/dev/null 2>&1
  head_sha="$(git -C "$local_dir" rev-parse HEAD)"

  payload_file="$TEST_TMPDIR/payload.json"
  cat >"$payload_file" <<EOF
{
  "status": "completed",
  "criteria_commit": "$criteria_commit",
  "commits_pushed": ["$head_sha"],
  "commit_range": "$commit_sha..$head_sha",
  "files_changed": ["test/acceptance.test.js"],
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

  run "$RUNOQ_ROOT/scripts/verify.sh" round "$local_dir" runoq/42-test "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ok')" = "false" ]
  [[ "$output" == *"criteria tampered"* ]]
  [[ "$output" == *"acceptance.test.js"* ]]
}

@test "verify round passes when criteria files are untouched" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  shas="$(prepare_verify_repo "$remote_dir" "$local_dir")"
  base_sha="$(printf '%s\n' "$shas" | sed -n '1p')"
  commit_sha="$(printf '%s\n' "$shas" | sed -n '2p')"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  export RUNOQ_CONFIG="$config_file"

  # Create a criteria commit: add a test file
  mkdir -p "$local_dir/test"
  echo "test('acceptance', () => {})" >"$local_dir/test/acceptance.test.js"
  git -C "$local_dir" add test/acceptance.test.js
  git -C "$local_dir" commit -m "bar-setter: acceptance criteria" >/dev/null
  criteria_commit="$(git -C "$local_dir" rev-parse HEAD)"

  # Add implementation WITHOUT modifying criteria file
  echo "module.exports = {}" >"$local_dir/src/impl.js"
  git -C "$local_dir" add src/impl.js
  git -C "$local_dir" commit -m "Add implementation" >/dev/null
  git -C "$local_dir" push origin runoq/42-test >/dev/null 2>&1

  # Build payload from ground truth so file lists match exactly
  all_commits="$(git -C "$local_dir" rev-list --reverse "${base_sha}..HEAD" | jq -Rsc 'split("\n") | map(select(length > 0))')"
  head_sha="$(git -C "$local_dir" rev-parse HEAD)"

  payload_file="$TEST_TMPDIR/payload.json"
  jq -n \
    --arg criteria "$criteria_commit" \
    --argjson commits "$all_commits" '{
      status: "completed",
      criteria_commit: $criteria,
      commits_pushed: $commits,
      commit_range: ($commits[0] + ".." + $commits[-1]),
      files_changed: [],
      files_added: ["src/app.ts", "src/impl.js", "test/acceptance.test.js"],
      files_deleted: [],
      tests_run: true,
      tests_passed: true,
      test_summary: "ok",
      build_passed: true,
      blockers: [],
      notes: ""
    }' >"$payload_file"

  run "$RUNOQ_ROOT/scripts/verify.sh" round "$local_dir" runoq/42-test "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ok')" = "true" ]
}

@test "verify round skips criteria check when no criteria_commit in payload" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  shas="$(prepare_verify_repo "$remote_dir" "$local_dir")"
  base_sha="$(printf '%s\n' "$shas" | sed -n '1p')"
  commit_sha="$(printf '%s\n' "$shas" | sed -n '2p')"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  export RUNOQ_CONFIG="$config_file"

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

  run "$RUNOQ_ROOT/scripts/verify.sh" round "$local_dir" runoq/42-test "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ok')" = "true" ]
}

@test "verify integrate passes when criteria files are intact and tests pass" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" checkout -b runoq/epic-test >/dev/null 2>&1
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  export RUNOQ_CONFIG="$config_file"

  # Create criteria commit
  mkdir -p "$local_dir/test"
  echo "test('integration', () => {})" >"$local_dir/test/integration.test.js"
  git -C "$local_dir" add test/integration.test.js
  git -C "$local_dir" commit -m "bar-setter: epic criteria" >/dev/null
  criteria_commit="$(git -C "$local_dir" rev-parse HEAD)"

  # Add more files without modifying criteria
  mkdir -p "$local_dir/src"
  echo "module.exports = {}" >"$local_dir/src/feature.js"
  git -C "$local_dir" add src/feature.js
  git -C "$local_dir" commit -m "Add feature" >/dev/null

  run "$RUNOQ_ROOT/scripts/verify.sh" integrate "$local_dir" "$criteria_commit"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ok')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.failures | length')" = "0" ]
}

@test "verify integrate fails when criteria files are tampered" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" checkout -b runoq/epic-test >/dev/null 2>&1
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  export RUNOQ_CONFIG="$config_file"

  # Create criteria commit
  mkdir -p "$local_dir/test"
  echo "test('integration', () => {})" >"$local_dir/test/integration.test.js"
  git -C "$local_dir" add test/integration.test.js
  git -C "$local_dir" commit -m "bar-setter: epic criteria" >/dev/null
  criteria_commit="$(git -C "$local_dir" rev-parse HEAD)"

  # Tamper with criteria file
  echo "test('integration', () => { /* hacked */ })" >"$local_dir/test/integration.test.js"
  git -C "$local_dir" add test/integration.test.js
  git -C "$local_dir" commit -m "Tamper" >/dev/null

  run "$RUNOQ_ROOT/scripts/verify.sh" integrate "$local_dir" "$criteria_commit"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ok')" = "false" ]
  [[ "$output" == *"criteria tampered"* ]]
}

@test "verify round fails fast when verification commands are not configured" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  shas="$(prepare_verify_repo "$remote_dir" "$local_dir")"
  base_sha="$(printf '%s\n' "$shas" | sed -n '1p')"
  commit_sha="$(printf '%s\n' "$shas" | sed -n '2p')"
  config_file="$TEST_TMPDIR/config.json"
  write_verify_config "$config_file"
  jq '.verification.testCommand = ""' "$config_file" >"$TEST_TMPDIR/config-bad.json"
  export RUNOQ_CONFIG="$TEST_TMPDIR/config-bad.json"

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

  run "$RUNOQ_ROOT/scripts/verify.sh" round "$local_dir" runoq/42-test "$base_sha" "$payload_file"

  [ "$status" -ne 0 ]
  [[ "$output" == *"verification.testCommand is not configured"* ]]
}
