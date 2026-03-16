#!/usr/bin/env bats

load test_helper

write_maintenance_config() {
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

prepare_maintenance_repo() {
  local repo_dir="$1"
  mkdir -p "$repo_dir"
  make_git_repo "$repo_dir"
  export TARGET_ROOT="$repo_dir"
  export AGENDEV_STATE_DIR="$repo_dir/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  export AGENDEV_CONFIG="$TEST_TMPDIR/config.json"
  write_maintenance_config "$AGENDEV_CONFIG"
}

@test "maintenance derive-partitions uses top-level include directories for single-project repos" {
  repo_dir="$TEST_TMPDIR/project"
  prepare_maintenance_repo "$repo_dir"
  cat >"$repo_dir/.gitignore" <<'EOF'
node_modules
dist
EOF
  cat >"$repo_dir/tsconfig.json" <<'EOF'
{
  "include": ["src/**/*.ts", "lib/**/*.ts"],
  "exclude": ["coverage", "generated"]
}
EOF

  run "$AGENDEV_ROOT/scripts/maintenance.sh" derive-partitions "$repo_dir"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.mode')" = "single-project" ]
  [ "$(printf '%s' "$output" | jq -r '.partitions | map(.name) | join(",")')" = "lib,src" ]
  [ "$(printf '%s' "$output" | jq -r '.exclusions | join(",")')" = "node_modules,dist,coverage,generated" ]
}

@test "maintenance derive-partitions uses tsconfig references for monorepos" {
  repo_dir="$TEST_TMPDIR/project"
  prepare_maintenance_repo "$repo_dir"
  cat >"$repo_dir/tsconfig.json" <<'EOF'
{
  "references": [
    { "path": "packages/core" },
    { "path": "packages/web" }
  ],
  "exclude": ["dist"]
}
EOF

  run "$AGENDEV_ROOT/scripts/maintenance.sh" derive-partitions "$repo_dir"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.mode')" = "references" ]
  [ "$(printf '%s' "$output" | jq -r '.partitions | map(.path) | join(",")')" = "packages/core,packages/web" ]
}

@test "maintenance start creates a tracking issue and partition progress comments without touching source files" {
  repo_dir="$TEST_TMPDIR/project"
  prepare_maintenance_repo "$repo_dir"
  cat >"$repo_dir/tsconfig.json" <<'EOF'
{
  "include": ["src/**/*.ts", "lib/**/*.ts"],
  "exclude": ["coverage"]
}
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "create", "--repo", "owner/repo", "--title", "Maintenance review"],
    "stdout": "https://github.com/owner/repo/issues/120"
  },
  {
    "contains": ["issue", "comment", "120", "--repo", "owner/repo", "--body", "Partition lib reviewed. PERFECT-D score: pending. Findings: 0."],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "120", "--repo", "owner/repo", "--body", "Partition src reviewed. PERFECT-D score: pending. Findings: 0."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  before_sha="$(git -C "$repo_dir" rev-parse HEAD)"
  run "$AGENDEV_ROOT/scripts/maintenance.sh" start owner/repo
  after_sha="$(git -C "$repo_dir" rev-parse HEAD)"

  [ "$status" -eq 0 ]
  [ "$before_sha" = "$after_sha" ]
  [ "$(printf '%s' "$output" | jq -r '.tracking_issue.number')" = "120" ]
  [ "$(printf '%s' "$output" | jq -r '.partitions | length')" = "2" ]
  run grep -n "Partitions: lib, src" "$FAKE_GH_CAPTURE_DIR/0.body"
  [ "$status" -eq 0 ]
}
