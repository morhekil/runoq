#!/usr/bin/env bats

load test_helper

@test "live smoke preflight requires explicit sandbox configuration" {
  run "$RUNOQ_ROOT/scripts/smoke-sandbox.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "false" ]
  [ "$(printf '%s' "$output" | jq -r '.enabled')" = "false" ]
  [ "$(printf '%s' "$output" | jq -r '.missing | length')" = "6" ]
}

@test "live smoke preflight accepts explicit sandbox configuration" {
  key_path="$TEST_TMPDIR/app-key.pem"
  printf 'not-a-real-key\n' >"$key_path"
  export RUNOQ_SMOKE=1
  export RUNOQ_SMOKE_REPO="owner/sandbox"
  export RUNOQ_SMOKE_APP_ID="123"
  export RUNOQ_SMOKE_INSTALLATION_ID="456"
  export RUNOQ_SMOKE_APP_KEY="$key_path"
  export RUNOQ_SMOKE_PERMISSION_USER="sandbox-user"
  export RUNOQ_SMOKE_PERMISSION_LEVEL="write"

  run "$RUNOQ_ROOT/scripts/smoke-sandbox.sh" preflight

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
  export RUNOQ_SMOKE=1
  export RUNOQ_SMOKE_REPO="owner/sandbox"
  export RUNOQ_SMOKE_APP_ID="123"
  export RUNOQ_SMOKE_INSTALLATION_ID="456"
  export RUNOQ_SMOKE_APP_KEY="$key_path"
  export RUNOQ_SMOKE_PERMISSION_USER="sandbox-user"
  export RUNOQ_SMOKE_PERMISSION_LEVEL="write"
  export RUNOQ_SMOKE_VERBOSE=1

  "$RUNOQ_ROOT/scripts/smoke-sandbox.sh" preflight >"$stdout_file" 2>"$stderr_file"

  [ "$(jq -r '.ready' "$stdout_file")" = "true" ]
  grep -F "[smoke-sandbox] checking sandbox preflight prerequisites" "$stderr_file"
  grep -F "[smoke-sandbox] sandbox preflight is ready" "$stderr_file"
}

@test "live smoke run surfaces sandbox clone failures" {
  key_path="$TEST_TMPDIR/app-key.pem"
  git_helper_dir="$TEST_TMPDIR/git-bin"
  real_git="$(command -v git)"
  printf 'not-a-real-key\n' >"$key_path"
  mkdir -p "$git_helper_dir"
  cat >"$git_helper_dir/git" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [[ "\${1:-}" == "clone" ]]; then
  printf 'fatal: simulated clone failure\n' >&2
  exit 128
fi
exec "$real_git" "\$@"
EOF
  chmod +x "$git_helper_dir/git"

  export PATH="$git_helper_dir:$PATH"
  export RUNOQ_SMOKE=1
  export RUNOQ_SMOKE_REPO="owner/sandbox"
  export RUNOQ_SMOKE_APP_ID="123"
  export RUNOQ_SMOKE_INSTALLATION_ID="456"
  export RUNOQ_SMOKE_APP_KEY="$key_path"
  export RUNOQ_SMOKE_PERMISSION_USER="sandbox-user"
  export RUNOQ_TEST_GH_TOKEN="test-token"

  run "$RUNOQ_ROOT/scripts/smoke-sandbox.sh" run

  [ "$status" -ne 0 ]
  [[ "$output" == *"fatal: simulated clone failure"* ]]
  [[ "$output" == *"Failed to clone sandbox repo owner/sandbox"* ]]
}

@test "live smoke resolves the default branch from the cloned remote" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"

  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    default_branch_from_clone "'"$local_dir"'"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "main" ]
}

@test "live smoke bootstraps an empty default branch" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  git init --bare "$remote_dir" >/dev/null
  git -C "$remote_dir" symbolic-ref HEAD refs/heads/main
  git clone "$remote_dir" "$local_dir" >/dev/null 2>&1

  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    ensure_default_branch_commit "'"$local_dir"'" owner/sandbox main
  '

  [ "$status" -eq 0 ]
  run git --git-dir "$remote_dir" rev-parse --verify --quiet refs/heads/main^{commit}
  [ "$status" -eq 0 ]
}

@test "live smoke force-adds marker commit when target repo ignores .runoq" {
  repo_dir="$TEST_TMPDIR/repo"
  make_git_repo "$repo_dir"
  printf '.runoq/\n' >"$repo_dir/.gitignore"
  git -C "$repo_dir" add .gitignore
  git -C "$repo_dir" commit -m "Ignore runoq artifacts" >/dev/null

  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    commit_smoke_marker "'"$repo_dir"'" run-id-123
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *"/.runoq/smoke/run-id-123.md" ]]
  run git -C "$repo_dir" show --name-only --pretty=format: HEAD
  [ "$status" -eq 0 ]
  [[ "$output" == *".runoq/smoke/run-id-123.md"* ]]
}

@test "live smoke normalizes trailing newlines when matching comment attribution" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["api", "repos/owner/repo/issues/5/comments"],
    "stdout": "[{\"body\":\"runoq live smoke pr comment 123\\n\",\"user\":{\"login\":\"runoq[bot]\"}}]"
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    export GH_BIN="'"$GH_BIN"'"
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    find_comment_author owner/repo 5 "runoq live smoke pr comment 123"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "runoq[bot]" ]
}

@test "live smoke retries comment attribution lookups until the comment appears" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["api", "repos/owner/repo/issues/5/comments"],
    "stdout": "[]"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/5/comments"],
    "stdout": "[{\"body\":\"runoq live smoke pr comment 123\",\"user\":{\"login\":\"runoq[bot]\"}}]"
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    export GH_BIN="'"$GH_BIN"'"
    export RUNOQ_SMOKE_COMMENT_LOOKUP_ATTEMPTS=2
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    find_comment_author owner/repo 5 "runoq live smoke pr comment 123"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "runoq[bot]" ]
}

@test "managed repo creation fails fast when gh repo create fails" {
  scenario="$TEST_TMPDIR/scenario.json"
  target_dir="$TEST_TMPDIR/target"
  mkdir -p "$target_dir"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["repo", "create", "owner/runoq-live-eval-run-123"],
    "stdout": "GraphQL: Resource not accessible by integration (createRepository)"
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    export GH_BIN="'"$GH_BIN"'"
    export RUNOQ_SMOKE_REPO_OWNER=owner
    export RUNOQ_SMOKE_REPO_PREFIX=runoq-live-eval
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    create_managed_repo "'"$target_dir"'" run-123
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"Failed to create managed repo"* ]]
  run grep -c 'repo edit' "$FAKE_GH_LOG"
  [ "$status" -eq 1 ]
}

@test "managed repo creation retries git push while the new repo propagates" {
  scenario="$TEST_TMPDIR/scenario.json"
  target_dir="$TEST_TMPDIR/target"
  git_helper_dir="$TEST_TMPDIR/git-bin"
  push_count_file="$TEST_TMPDIR/git-push.count"
  real_git="$(command -v git)"
  make_git_repo "$target_dir"
  mkdir -p "$git_helper_dir"
  cat >"$git_helper_dir/git" <<EOF
#!/usr/bin/env bash
set -euo pipefail
count_file="$push_count_file"
if [[ "\${1:-}" == "-C" && "\${3:-}" == "push" ]]; then
  count=0
  [[ -f "\$count_file" ]] && count="\$(cat "\$count_file")"
  count="\$((count + 1))"
  printf '%s' "\$count" >"\$count_file"
  if [[ "\$count" -eq 1 ]]; then
    printf 'ERROR: Repository not found.\n' >&2
    exit 128
  fi
  exit 0
fi
if [[ "\${1:-}" == "push" ]]; then
  count=0
  [[ -f "\$count_file" ]] && count="\$(cat "\$count_file")"
  count="\$((count + 1))"
  printf '%s' "\$count" >"\$count_file"
  if [[ "\$count" -eq 1 ]]; then
    printf 'ERROR: Repository not found.\n' >&2
    exit 128
  fi
  exit 0
fi
exec "$real_git" "\$@"
EOF
  chmod +x "$git_helper_dir/git"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["repo", "create", "owner/runoq-live-eval-run-123"],
    "stdout": "https://github.com/owner/runoq-live-eval-run-123"
  },
  {
    "contains": ["repo", "view", "owner/runoq-live-eval-run-123"],
    "stdout": "{\"name\":\"runoq-live-eval-run-123\"}"
  },
  {
    "contains": ["repo", "edit", "owner/runoq-live-eval-run-123"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc '
    set -euo pipefail
    export PATH="'"$git_helper_dir"':$PATH"
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    export GH_BIN="'"$GH_BIN"'"
    export RUNOQ_SMOKE_REPO_OWNER=owner
    export RUNOQ_SMOKE_REPO_PREFIX=runoq-live-eval
    export RUNOQ_SMOKE_RETRY_DELAY_SECONDS=0
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    create_managed_repo "'"$target_dir"'" run-123
  '

  [ "$status" -eq 0 ]
  run cat "$push_count_file"
  [ "$status" -eq 0 ]
  [ "$output" = "2" ]
}

@test "operator_gh runs without inherited bot token" {
  helper="$TEST_TMPDIR/gh-env"
  cat >"$helper" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'GH_TOKEN=%s\n' "${GH_TOKEN:-}"
EOF
  chmod +x "$helper"

  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    export GH_BIN="'"$helper"'"
    export GH_TOKEN="bot-token"
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    operator_gh repo view owner/repo --json name
  '

  [ "$status" -eq 0 ]
  [ "$output" = "GH_TOKEN=" ]
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
  export RUNOQ_CLAUDE_BIN="sh"
  export RUNOQ_SMOKE_CODEX_BIN="sh"

  run "$RUNOQ_ROOT/scripts/smoke-lifecycle.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "false" ]
  [ "$(printf '%s' "$output" | jq -r '.gh_authenticated')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.missing | length')" = "5" ]
  [[ "$(printf '%s' "$output" | jq -r '.missing | join(" ")')" == *"RUNOQ_SMOKE_INSTALLATION_ID"* ]]
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
  export RUNOQ_SMOKE=1
  export RUNOQ_SMOKE_REPO_OWNER="owner"
  export RUNOQ_SMOKE_APP_ID="123"
  export RUNOQ_SMOKE_INSTALLATION_ID="456"
  export RUNOQ_SMOKE_APP_KEY="$key_path"
  export RUNOQ_CLAUDE_BIN="sh"
  export RUNOQ_SMOKE_CODEX_BIN="sh"

  run "$RUNOQ_ROOT/scripts/smoke-lifecycle.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.repo_owner')" = "owner" ]
  [ "$(printf '%s' "$output" | jq -r '.repo_prefix')" = "runoq-live-eval" ]
}

@test "live tick smoke preflight requires explicit managed repo configuration" {
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

  run "$RUNOQ_ROOT/scripts/smoke-tick.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "false" ]
  [ "$(printf '%s' "$output" | jq -r '.gh_authenticated')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.missing | length')" -ge 5 ]
  [[ "$(printf '%s' "$output" | jq -r '.missing | join(" ")')" == *"RUNOQ_SMOKE_INSTALLATION_ID"* ]]
}

@test "live tick smoke preflight accepts explicit managed repo configuration" {
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
  export RUNOQ_SMOKE=1
  export RUNOQ_SMOKE_REPO_OWNER="owner"
  export RUNOQ_SMOKE_APP_ID="123"
  export RUNOQ_SMOKE_INSTALLATION_ID="456"
  export RUNOQ_SMOKE_APP_KEY="$key_path"

  run "$RUNOQ_ROOT/scripts/smoke-tick.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.repo_owner')" = "owner" ]
  [ "$(printf '%s' "$output" | jq -r '.repo_prefix')" = "runoq-live-eval" ]
}

@test "live lifecycle run preflight failure does not trip cleanup on unset locals" {
  run "$RUNOQ_ROOT/scripts/smoke-lifecycle.sh" run

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
  export RUNOQ_SMOKE_MANIFEST_PATH="$manifest_path"

  run "$RUNOQ_ROOT/scripts/smoke-lifecycle.sh" cleanup --repo owner/repo-one

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
  export RUNOQ_SMOKE_MANIFEST_PATH="$manifest_path"
  export RUNOQ_SMOKE_VERBOSE=1

  "$RUNOQ_ROOT/scripts/smoke-lifecycle.sh" cleanup --repo owner/repo-one >"$stdout_file" 2>"$stderr_file"

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
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    create_claude_capture_wrapper "'"$wrapper_path"'"
    export RUNOQ_SMOKE_REAL_CLAUDE_BIN="'"$real_claude"'"
    export RUNOQ_SMOKE_CLAUDE_CAPTURE_DIR="'"$capture_dir"'"
    export TARGET_ROOT="'"$target_dir"'"
    export REPO="owner/repo"
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    mkdir -p "$TARGET_ROOT"
    cd "$TARGET_ROOT"
    "'"$wrapper_path"'" --print --agent github-orchestrator -- "{\"command\":\"runoq run\"}"
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
  real_bin_dir="$TEST_TMPDIR/real-bin"
  mkdir -p "$real_bin_dir"
  real_codex="$real_bin_dir/codex"
  cat >"$real_codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'codex stdout\n'
printf 'codex stderr\n' >&2
EOF
  chmod +x "$real_codex"

  run bash -lc '
    set -euo pipefail
    export PATH="'"$real_bin_dir"':$PATH"
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    real_codex_bin="$(smoke_codex_bin)"
    create_codex_capture_wrapper "'"$wrapper_path"'"
    export RUNOQ_SMOKE_REAL_CODEX_BIN="$real_codex_bin"
    export RUNOQ_SMOKE_CODEX_CAPTURE_DIR="'"$capture_dir"'"
    export TARGET_ROOT="'"$target_dir"'"
    export REPO="owner/repo"
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export PATH="'"$TEST_TMPDIR"':$PATH"
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
  grep -F "REAL_BIN=$real_codex" "$invocation_dir/context.log"
  grep -F "codex stdout" "$invocation_dir/stdout.log"
  grep -F "codex stderr" "$invocation_dir/stderr.log"
}

@test "lifecycle summary treats copied lifecycle state files as completed issues" {
  target_dir="$TEST_TMPDIR/target"
  artifacts_dir="$TEST_TMPDIR/artifacts"
  mkdir -p "$target_dir/.runoq/state" "$artifacts_dir"
  cat >"$target_dir/.runoq/state/1.json" <<'EOF'
{
  "issueNumber": 1,
  "status": "done",
  "dispatchedAt": "2026-03-21T00:23:00Z",
  "completedAt": "2026-03-21T00:27:00Z",
  "rounds": 1,
  "result": { "verdict": "PASS" }
}
EOF
  cat >"$target_dir/.runoq/state/2.json" <<'EOF'
{
  "issueNumber": 2,
  "status": "done",
  "dispatchedAt": "2026-03-21T00:28:00Z",
  "completedAt": "2026-03-21T00:37:00Z",
  "rounds": 2,
  "result": { "verdict": "PASS" }
}
EOF

  run bash -lc '
    set -euo pipefail
    source "'"$RUNOQ_ROOT"'/scripts/lib/smoke-common.sh"
    seeded='\''[
      {"key":"one","number":1,"title":"One","depends_on_numbers":[],"url":"https://example.test/issues/1"},
      {"key":"two","number":2,"title":"Two","depends_on_numbers":[1],"url":"https://example.test/issues/2"}
    ]'\''
    statuses='\''[
      {"number":1,"state":"OPEN","labels":[{"name":"runoq:done"}],"url":"https://example.test/issues/1"},
      {"number":2,"state":"OPEN","labels":[{"name":"runoq:done"}],"url":"https://example.test/issues/2"}
    ]'\''
    prs='\''[
      {"number":10,"state":"MERGED","isDraft":false,"title":"One","url":"https://example.test/pull/10","headRefName":"runoq/1-one","baseRefName":"main"},
      {"number":11,"state":"MERGED","isDraft":false,"title":"Two","url":"https://example.test/pull/11","headRefName":"runoq/2-two","baseRefName":"main"}
    ]'\''
    report='\''{"issues":2,"pass":2,"fail":0,"caveats":0,"tokens":{"input":0,"cached_input":0,"output":0,"total":0},"average_rounds":1.5}'\''
    states="$(read_state_files_json "'"$target_dir"'")"
    build_lifecycle_summary \
      "owner/repo" \
      "run-123" \
      "'"$artifacts_dir"'" \
      "0" \
      "$seeded" \
      "$states" \
      "$statuses" \
      "$prs" \
      "$report" \
      "[]" \
      "[\"lifecycle_run_invoked\"]"
  '

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "ok" ]
  [ "$(printf '%s' "$output" | jq -r '.lifecycle.completed_issues')" = "2" ]
  [ "$(printf '%s' "$output" | jq -r '.lifecycle.all_tasks_done')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.lifecycle.one_shot_completed')" = "1" ]
  [ "$(printf '%s' "$output" | jq -r '.failures | map(select(. == "Not all seeded task issues reached DONE.")) | length')" = "0" ]
  [ "$(printf '%s' "$output" | jq -r '.failures | map(select(. == "Not all seeded issues completed in one round.")) | length')" = "0" ]
}

@test "copy_state_artifacts collects sibling worktree breadcrumbs for lifecycle reporting" {
  smoke_root="$TEST_TMPDIR/live-smoke"
  target_dir="$smoke_root/target"
  artifacts_dir="$TEST_TMPDIR/artifacts"
  worktree_dir="$smoke_root/runoq-wt-2"
  mkdir -p "$target_dir" "$worktree_dir/.runoq/state" "$artifacts_dir"
  cat >"$worktree_dir/.runoq/state/2.json" <<'EOF'
{
  "issueNumber": 2,
  "status": "done",
  "rounds": 1,
  "result": { "verdict": "PASS" }
}
EOF

  source "$RUNOQ_ROOT/scripts/lib/smoke-common.sh"
  copy_state_artifacts "$target_dir" "$artifacts_dir"
  states="$(read_state_files_json_from_dir "$artifacts_dir/state")"
  report="$(
    TARGET_ROOT="$target_dir" \
    RUNOQ_STATE_DIR="$artifacts_dir/state" \
    "$RUNOQ_ROOT/scripts/report.sh" summary
  )"

  [ "$(printf '%s' "$states" | jq -r 'length')" = "1" ]
  [ "$(printf '%s' "$states" | jq -r '.[0].issue')" = "2" ]
  [ "$(printf '%s' "$states" | jq -r '.[0].phase')" = "DONE" ]
  [ "$(printf '%s' "$report" | jq -r '.issues')" = "1" ]
  [ "$(printf '%s' "$report" | jq -r '.pass')" = "1" ]
}
