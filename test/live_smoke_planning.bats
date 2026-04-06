#!/usr/bin/env bats

# Resolve real binaries before test_helper prepends test/helpers (which contains fake stubs).
GH_BIN="$(command -v gh)"
export GH_BIN
RUNOQ_CLAUDE_BIN="$(command -v claude)"
export RUNOQ_CLAUDE_BIN

load test_helper

@test "live planning smoke preflight requires installation id for managed repo auth" {
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
  export RUNOQ_SMOKE_APP_KEY="$key_path"
  export RUNOQ_CLAUDE_BIN="sh"

  run "$RUNOQ_ROOT/scripts/smoke-planning.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "false" ]
  [[ "$(printf '%s' "$output" | jq -r '.missing | join(" ")')" == *"RUNOQ_SMOKE_INSTALLATION_ID"* ]]
}

@test "live planning smoke reports init failure without shell crash" {
  key_path="$TEST_TMPDIR/app-key.pem"
  git_helper_dir="$TEST_TMPDIR/git-bin"
  real_git="$(command -v git)"
  printf 'not-a-real-key\n' >"$key_path"
  mkdir -p "$git_helper_dir"
  cat >"$git_helper_dir/git" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [[ "\${1:-}" == "-C" && "\${3:-}" == "push" ]]; then
  exit 0
fi
if [[ "\${1:-}" == "push" ]]; then
  exit 0
fi
exec "$real_git" "\$@"
EOF
  chmod +x "$git_helper_dir/git"
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["auth", "status"],
    "stdout": ""
  },
  {
    "contains": ["repo", "create", "owner/runoq-live-eval-"],
    "stdout": "https://github.com/owner/runoq-live-eval-test"
  },
  {
    "contains": ["repo", "view", "owner/runoq-live-eval-"],
    "stdout": "{\"name\":\"runoq-live-eval-test\"}"
  },
  {
    "contains": ["auth", "token"],
    "stdout": "test-token"
  },
  {
    "contains": ["repo", "edit", "owner/runoq-live-eval-"],
    "stdout": ""
  },
  {
    "contains": ["api", "user", "--jq", ".login"],
    "stdout": "operator"
  },
  {
    "contains": ["repo", "view", "owner/runoq-live-eval-"],
    "stdout": "{\"name\":\"runoq-live-eval-test\"}"
  },
  {
    "contains": ["api", "repos/owner/runoq-live-eval-"],
    "stdout": "\"owner/runoq-live-eval-test\""
  }
]
EOF
  use_fake_gh "$scenario"
  export PATH="$git_helper_dir:$PATH"
  export RUNOQ_SMOKE=1
  export RUNOQ_SMOKE_REPO_OWNER="owner"
  export RUNOQ_SMOKE_APP_ID="123"
  export RUNOQ_SMOKE_INSTALLATION_ID="456"
  export RUNOQ_SMOKE_APP_KEY="$key_path"
  export RUNOQ_CLAUDE_BIN="sh"
  export RUNOQ_SMOKE_RETRY_DELAY_SECONDS=0

  run "$RUNOQ_ROOT/scripts/smoke-planning.sh" run

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "failed" ]
  [[ "$(printf '%s' "$output" | jq -r '.failures | join(" ")')" == *"runoq init failed"* ]]
}

@test "live planning smoke retries issue listing after transient failures" {
  attempt_file="$TEST_TMPDIR/issue-list-attempts"
  helper_dir="$TEST_TMPDIR/scripts"
  helper_script="$helper_dir/smoke-planning.sh"
  mkdir -p "$helper_dir"
  ln -s "$RUNOQ_ROOT/scripts/lib" "$helper_dir/lib"
  sed '/^case "${1:-}" in$/,$d' "$RUNOQ_ROOT/scripts/smoke-planning.sh" >"$helper_script"
  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    export RUNOQ_SMOKE_RETRY_DELAY_SECONDS=0
    export RUNOQ_SMOKE_ISSUE_LIST_ATTEMPTS=2
    attempt_file="'"$attempt_file"'"
    printf "%s" "0" >"$attempt_file"
    source "'"$helper_script"'"
    runoq::gh() {
      attempts="$(cat "$attempt_file")"
      attempts=$((attempts + 1))
      printf "%s" "$attempts" >"$attempt_file"
      if [[ "$attempts" -eq 1 ]]; then
        return 1
      fi
      printf "%s\n" "[]"
    }
    list_issues_json_retry owner/repo >/dev/null
    cat "$attempt_file"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "2" ]
}

@test "live planning smoke validates the tick-based planning workflow" {
  if [[ "${RUNOQ_SMOKE_PLANNING:-0}" != "1" ]]; then
    skip "Set RUNOQ_SMOKE_PLANNING=1 plus the required RUNOQ_SMOKE_* variables to run live planning smoke."
  fi

  run "$RUNOQ_ROOT/scripts/smoke-planning.sh" preflight
  [ "$status" -eq 0 ]
  if [[ "$(printf '%s' "$output" | jq -r '.ready')" != "true" ]]; then
    skip "Live planning smoke preflight is not ready: $output"
  fi

  run "$RUNOQ_ROOT/scripts/smoke-planning.sh" run

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "ok" ]
  [ "$(printf '%s' "$output" | jq -r '.planning.milestones')" -ge 1 ]
  [ "$(printf '%s' "$output" | jq -r '.planning.tasks')" -ge 1 ]
  [ "$(printf '%s' "$output" | jq -r '.planning.has_discovery_milestone')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.comment_interactions')" -ge 1 ]
}
