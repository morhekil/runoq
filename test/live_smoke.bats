#!/usr/bin/env bats

load test_helper

@test "live smoke preflight requires explicit sandbox configuration" {
  run "$AGENDEV_ROOT/scripts/smoke-sandbox.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "false" ]
  [ "$(printf '%s' "$output" | jq -r '.enabled')" = "false" ]
  [ "$(printf '%s' "$output" | jq -r '.missing | length')" = "6" ]
}

@test "live smoke preflight accepts explicit sandbox configuration" {
  key_path="$TEST_TMPDIR/app-key.pem"
  printf 'not-a-real-key\n' >"$key_path"
  export AGENDEV_SMOKE=1
  export AGENDEV_SMOKE_REPO="owner/sandbox"
  export AGENDEV_SMOKE_APP_ID="123"
  export AGENDEV_SMOKE_INSTALLATION_ID="456"
  export AGENDEV_SMOKE_APP_KEY="$key_path"
  export AGENDEV_SMOKE_PERMISSION_USER="sandbox-user"
  export AGENDEV_SMOKE_PERMISSION_LEVEL="write"

  run "$AGENDEV_ROOT/scripts/smoke-sandbox.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.repo')" = "owner/sandbox" ]
  [ "$(printf '%s' "$output" | jq -r '.permission_user')" = "sandbox-user" ]
  [ "$(printf '%s' "$output" | jq -r '.permission_level')" = "write" ]
}

@test "live smoke preflight logs progress to stderr when verbose mode is enabled" {
  key_path="$TEST_TMPDIR/app-key.pem"
  stdout_file="$TEST_TMPDIR/stdout.json"
  stderr_file="$TEST_TMPDIR/stderr.log"
  printf 'not-a-real-key\n' >"$key_path"
  export AGENDEV_SMOKE=1
  export AGENDEV_SMOKE_REPO="owner/sandbox"
  export AGENDEV_SMOKE_APP_ID="123"
  export AGENDEV_SMOKE_INSTALLATION_ID="456"
  export AGENDEV_SMOKE_APP_KEY="$key_path"
  export AGENDEV_SMOKE_PERMISSION_USER="sandbox-user"
  export AGENDEV_SMOKE_PERMISSION_LEVEL="write"
  export AGENDEV_SMOKE_VERBOSE=1

  "$AGENDEV_ROOT/scripts/smoke-sandbox.sh" preflight >"$stdout_file" 2>"$stderr_file"

  [ "$(jq -r '.ready' "$stdout_file")" = "true" ]
  grep -F "[smoke-sandbox] checking sandbox preflight prerequisites" "$stderr_file"
  grep -F "[smoke-sandbox] sandbox preflight is ready" "$stderr_file"
}

@test "live lifecycle smoke preflight requires explicit managed repo configuration" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["auth", "status"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"
  export AGENDEV_CLAUDE_BIN="sh"
  export AGENDEV_SMOKE_CODEX_BIN="sh"

  run "$AGENDEV_ROOT/scripts/smoke-lifecycle.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "false" ]
  [ "$(printf '%s' "$output" | jq -r '.gh_authenticated')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.missing | length')" = "4" ]
}

@test "live lifecycle smoke preflight accepts explicit managed repo configuration" {
  key_path="$TEST_TMPDIR/app-key.pem"
  printf 'not-a-real-key\n' >"$key_path"
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["auth", "status"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"
  export AGENDEV_SMOKE=1
  export AGENDEV_SMOKE_REPO_OWNER="owner"
  export AGENDEV_SMOKE_APP_ID="123"
  export AGENDEV_SMOKE_APP_KEY="$key_path"
  export AGENDEV_CLAUDE_BIN="sh"
  export AGENDEV_SMOKE_CODEX_BIN="sh"

  run "$AGENDEV_ROOT/scripts/smoke-lifecycle.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.repo_owner')" = "owner" ]
  [ "$(printf '%s' "$output" | jq -r '.repo_prefix')" = "agendev-live-eval" ]
}

@test "live lifecycle run preflight failure does not trip cleanup on unset locals" {
  run "$AGENDEV_ROOT/scripts/smoke-lifecycle.sh" run

  [ "$status" -ne 0 ]
  [[ "$output" == *"Live lifecycle smoke preflight failed."* ]]
  [[ "$output" != *"tmpdir: unbound variable"* ]]
}

@test "live lifecycle cleanup deletes selected managed repos and updates the manifest" {
  manifest_path="$TEST_TMPDIR/managed-repos.json"
  printf '%s\n' '[
    {
      "repo": "owner/repo-one",
      "run_id": "run-1",
      "cleanup_state": "active",
      "deleted_at": null
    },
    {
      "repo": "owner/repo-two",
      "run_id": "run-2",
      "cleanup_state": "active",
      "deleted_at": null
    }
  ]' >"$manifest_path"
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["auth", "status"],
    "stdout": ""
  },
  {
    "contains": ["repo", "delete", "owner/repo-one", "--yes"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"
  export AGENDEV_SMOKE_MANIFEST_PATH="$manifest_path"

  run "$AGENDEV_ROOT/scripts/smoke-lifecycle.sh" cleanup --repo owner/repo-one

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "ok" ]
  [ "$(printf '%s' "$output" | jq -r '.deleted[0]')" = "owner/repo-one" ]
  [ "$(jq -r '.[] | select(.repo == "owner/repo-one") | .cleanup_state' "$manifest_path")" = "deleted" ]
  [ "$(jq -r '.[] | select(.repo == "owner/repo-two") | .cleanup_state' "$manifest_path")" = "active" ]
}

@test "live lifecycle cleanup logs progress to stderr when verbose mode is enabled" {
  manifest_path="$TEST_TMPDIR/managed-repos.json"
  stdout_file="$TEST_TMPDIR/stdout.json"
  stderr_file="$TEST_TMPDIR/stderr.log"
  printf '%s\n' '[
    {
      "repo": "owner/repo-one",
      "run_id": "run-1",
      "cleanup_state": "active",
      "deleted_at": null
    }
  ]' >"$manifest_path"
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["auth", "status"],
    "stdout": ""
  },
  {
    "contains": ["repo", "delete", "owner/repo-one", "--yes"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"
  export AGENDEV_SMOKE_MANIFEST_PATH="$manifest_path"
  export AGENDEV_SMOKE_VERBOSE=1

  "$AGENDEV_ROOT/scripts/smoke-lifecycle.sh" cleanup --repo owner/repo-one >"$stdout_file" 2>"$stderr_file"

  [ "$(jq -r '.status' "$stdout_file")" = "ok" ]
  grep -F "[smoke-lifecycle] selected 1 managed repo(s) for cleanup" "$stderr_file"
  grep -F "[smoke-lifecycle] deleting managed repo owner/repo-one" "$stderr_file"
  grep -F "[smoke-lifecycle] deleted managed repo owner/repo-one" "$stderr_file"
}

@test "lifecycle Claude capture wrapper records invocation artifacts" {
  wrapper_path="$TEST_TMPDIR/claude-capture"
  capture_dir="$TEST_TMPDIR/claude-artifacts"
  target_dir="$TEST_TMPDIR/target"
  real_claude="$TEST_TMPDIR/fake-claude"
  cat >"$real_claude" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'fake stdout\n'
printf 'fake stderr\n' >&2
EOF
  chmod +x "$real_claude"

  run bash -lc '
    set -euo pipefail
    source "'"$AGENDEV_ROOT"'/scripts/lib/smoke-common.sh"
    create_claude_capture_wrapper "'"$wrapper_path"'"
    export AGENDEV_SMOKE_REAL_CLAUDE_BIN="'"$real_claude"'"
    export AGENDEV_SMOKE_CLAUDE_CAPTURE_DIR="'"$capture_dir"'"
    export TARGET_ROOT="'"$target_dir"'"
    export REPO="owner/repo"
    export AGENDEV_ROOT="'"$AGENDEV_ROOT"'"
    mkdir -p "$TARGET_ROOT"
    cd "$TARGET_ROOT"
    "'"$wrapper_path"'" --print --agent github-orchestrator -- "{\"command\":\"agendev run\"}"
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *"fake stdout"* ]]
  [[ "$output" == *"fake stderr"* ]]

  invocation_dir="$(find "$capture_dir" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
  [ -n "$invocation_dir" ]
  [ -f "$invocation_dir/argv.txt" ]
  [ -f "$invocation_dir/context.log" ]
  [ -f "$invocation_dir/stdout.log" ]
  [ -f "$invocation_dir/stderr.log" ]
  grep -F -- "--print" "$invocation_dir/argv.txt"
  grep -E '^cwd=.*/target$' "$invocation_dir/context.log"
  grep -F "REPO=owner/repo" "$invocation_dir/context.log"
  grep -F "fake stdout" "$invocation_dir/stdout.log"
  grep -F "fake stderr" "$invocation_dir/stderr.log"
}

@test "lifecycle Codex capture wrapper records invocation artifacts" {
  wrapper_path="$TEST_TMPDIR/codex"
  capture_dir="$TEST_TMPDIR/codex-artifacts"
  target_dir="$TEST_TMPDIR/target"
  real_codex="$TEST_TMPDIR/fake-codex"
  cat >"$real_codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'codex stdout\n'
printf 'codex stderr\n' >&2
EOF
  chmod +x "$real_codex"

  run bash -lc '
    set -euo pipefail
    source "'"$AGENDEV_ROOT"'/scripts/lib/smoke-common.sh"
    create_codex_capture_wrapper "'"$wrapper_path"'"
    export AGENDEV_SMOKE_REAL_CODEX_BIN="'"$real_codex"'"
    export AGENDEV_SMOKE_CODEX_CAPTURE_DIR="'"$capture_dir"'"
    export TARGET_ROOT="'"$target_dir"'"
    export REPO="owner/repo"
    export AGENDEV_ROOT="'"$AGENDEV_ROOT"'"
    mkdir -p "$TARGET_ROOT"
    cd "$TARGET_ROOT"
    "'"$wrapper_path"'" exec -s danger-full-access --full-auto "do the thing"
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *"codex stdout"* ]]
  [[ "$output" == *"codex stderr"* ]]

  invocation_dir="$(find "$capture_dir" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
  [ -n "$invocation_dir" ]
  [ -f "$invocation_dir/argv.txt" ]
  [ -f "$invocation_dir/context.log" ]
  [ -f "$invocation_dir/stdout.log" ]
  [ -f "$invocation_dir/stderr.log" ]
  grep -F -- "exec" "$invocation_dir/argv.txt"
  grep -E '^cwd=.*/target$' "$invocation_dir/context.log"
  grep -F "REPO=owner/repo" "$invocation_dir/context.log"
  grep -F "codex stdout" "$invocation_dir/stdout.log"
  grep -F "codex stderr" "$invocation_dir/stderr.log"
}
