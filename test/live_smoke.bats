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
  [ "$(printf '%s' "$output" | jq -r '.missing | length')" = "3" ]
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
  export AGENDEV_SMOKE_APP_KEY="$key_path"
  export AGENDEV_CLAUDE_BIN="sh"
  export AGENDEV_SMOKE_CODEX_BIN="sh"

  run "$AGENDEV_ROOT/scripts/smoke-lifecycle.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.repo_owner')" = "owner" ]
  [ "$(printf '%s' "$output" | jq -r '.repo_prefix')" = "agendev-live-eval" ]
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
