#!/usr/bin/env bats

load test_helper

write_empty_key() {
  local key_path="$1"
  openssl genrsa -out "$key_path" 2048 >/dev/null 2>&1
}

@test "setup init creates identity state package json and symlink" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"
  export AGENDEV_SYMLINK_DIR="$TEST_TMPDIR/bin"
  export AGENDEV_APP_KEY="$TEST_TMPDIR/app-key.pem"
  write_empty_key "$AGENDEV_APP_KEY"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "/apps/agendev", "--jq .id"],
    "stdout": "123"
  },
  {
    "contains": ["api", "/repos/owner/repo/installation", "--jq .id"],
    "stdout": "789"
  },
  {
    "contains": ["label", "list", "--repo owner/repo"],
    "stdout": "[]"
  },
  {
    "contains": ["label", "create", "agendev:ready", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "agendev:in-progress", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "agendev:done", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "agendev:needs-human-review", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "agendev:blocked", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "agendev:maintenance-review", "--repo owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/setup.sh"

  [ "$status" -eq 0 ]
  [ -d "$project_dir/.agendev/state" ]
  [ -f "$project_dir/.agendev/identity.json" ]
  [ -f "$project_dir/package.json" ]
  [ -L "$AGENDEV_SYMLINK_DIR/agendev" ]
  [ "$(jq -r '.appId' "$project_dir/.agendev/identity.json")" = "123" ]
}

@test "setup init is idempotent on repeated runs" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"
  export AGENDEV_SYMLINK_DIR="$TEST_TMPDIR/bin"
  mkdir -p "$project_dir/.agendev"
  cat >"$project_dir/.agendev/identity.json" <<EOF
{
  "appId": 123,
  "installationId": 789,
  "privateKeyPath": "$TEST_TMPDIR/app-key.pem"
}
EOF
  write_empty_key "$TEST_TMPDIR/app-key.pem"
  cat >"$project_dir/package.json" <<'EOF'
{
  "name": "existing",
  "scripts": {
    "test": "existing test",
    "build": "existing build"
  }
}
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["label", "list", "--repo owner/repo"],
    "stdout": "[{\"name\":\"agendev:ready\"},{\"name\":\"agendev:in-progress\"},{\"name\":\"agendev:done\"},{\"name\":\"agendev:needs-human-review\"},{\"name\":\"agendev:blocked\"},{\"name\":\"agendev:maintenance-review\"}]"
  },
  {
    "contains": ["label", "list", "--repo owner/repo"],
    "stdout": "[{\"name\":\"agendev:ready\"},{\"name\":\"agendev:in-progress\"},{\"name\":\"agendev:done\"},{\"name\":\"agendev:needs-human-review\"},{\"name\":\"agendev:blocked\"},{\"name\":\"agendev:maintenance-review\"}]"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/setup.sh"
  [ "$status" -eq 0 ]
  run "$AGENDEV_ROOT/scripts/setup.sh"
  [ "$status" -eq 0 ]
  [ "$(jq -r '.name' "$project_dir/package.json")" = "existing" ]
}
