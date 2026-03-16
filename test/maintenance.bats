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

@test "maintenance post-findings records grouped findings and flags in-flight PRs" {
  repo_dir="$TEST_TMPDIR/project"
  prepare_maintenance_repo "$repo_dir"
  cat >"$AGENDEV_STATE_DIR/maintenance.json" <<'EOF'
{
  "phase": "STARTED",
  "tracking_issue": 120,
  "partitions": [
    { "name": "src", "path": "src" }
  ]
}
EOF
  findings_file="$TEST_TMPDIR/findings.json"
  cat >"$findings_file" <<'EOF'
{
  "recurring_patterns": ["validation duplication"],
  "findings": [
    {
      "id": "F1",
      "title": "Grouped validation cleanup",
      "dimension": "design",
      "severity": "medium",
      "files": ["src/a.ts", "src/b.ts"],
      "description": "Extract duplicated validation logic.",
      "suggested_fix": "Create a shared helper.",
      "grouped": true,
      "inflight_pr": 91
    }
  ]
}
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["issue", "comment", "120", "--repo", "owner/repo", "--body", "Finding ID: F1"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/maintenance.sh" post-findings owner/repo 120 "$findings_file"

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.findings | length')" = "1" ]
  run jq -r '.findings[0].status' "$AGENDEV_STATE_DIR/maintenance.json"
  [ "$status" -eq 0 ]
  [ "$output" = "pending" ]
  run jq -r '.recurring_patterns[0]' "$AGENDEV_STATE_DIR/maintenance.json"
  [ "$status" -eq 0 ]
  [ "$output" = "validation duplication" ]
}

@test "maintenance triage approves denies and modifies findings while reusing mention authorization rules" {
  repo_dir="$TEST_TMPDIR/project"
  prepare_maintenance_repo "$repo_dir"
  cat >"$AGENDEV_STATE_DIR/maintenance.json" <<'EOF'
{
  "phase": "FINDINGS_POSTED",
  "tracking_issue": 120,
  "partitions": [
    { "name": "src", "path": "src" }
  ],
  "recurring_patterns": ["validation duplication"],
  "findings": [
    {
      "id": "F1",
      "title": "Approval finding",
      "description": "Document the approval path.",
      "suggested_fix": "Add missing documentation.",
      "status": "pending",
      "priority": 1
    },
    {
      "id": "F2",
      "title": "Declined finding",
      "description": "This is not worth filing.",
      "suggested_fix": "Skip it.",
      "status": "pending",
      "priority": 2
    },
    {
      "id": "F3",
      "title": "Modified finding",
      "description": "Lower the priority before filing.",
      "suggested_fix": "Create a follow-up.",
      "status": "pending",
      "priority": 1
    }
  ]
}
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "repos/owner/repo/issues/120/comments"],
    "stdout": "[{\"id\":5001,\"body\":\"@agendev approve F1\",\"user\":{\"login\":\"reviewer1\"},\"created_at\":\"2026-03-17T03:00:00Z\"},{\"id\":5002,\"body\":\"@agendev skip F2\",\"user\":{\"login\":\"reviewer1\"},\"created_at\":\"2026-03-17T03:05:00Z\"},{\"id\":5003,\"body\":\"@agendev file this F3 but lower priority\",\"user\":{\"login\":\"reviewer1\"},\"created_at\":\"2026-03-17T03:10:00Z\"}]"
  },
  {
    "contains": ["api", "repos/owner/repo/collaborators/reviewer1/permission"],
    "stdout_file": "$(fixture_path "comments/permission-write.json")"
  },
  {
    "contains": ["issue", "create", "--repo", "owner/repo", "--title", "Approval finding", "--label", "agendev:ready"],
    "stdout": "https://github.com/owner/repo/issues/99"
  },
  {
    "contains": ["issue", "comment", "120", "--repo", "owner/repo", "--body", "Finding F1 approved. Filed as #99."],
    "stdout": ""
  },
  {
    "contains": ["api", "repos/owner/repo/collaborators/reviewer1/permission"],
    "stdout_file": "$(fixture_path "comments/permission-write.json")"
  },
  {
    "contains": ["issue", "comment", "120", "--repo", "owner/repo", "--body", "Finding F2 declined."],
    "stdout": ""
  },
  {
    "contains": ["api", "repos/owner/repo/collaborators/reviewer1/permission"],
    "stdout_file": "$(fixture_path "comments/permission-write.json")"
  },
  {
    "contains": ["issue", "create", "--repo", "owner/repo", "--title", "Modified finding", "--label", "agendev:ready"],
    "stdout": "https://github.com/owner/repo/issues/100"
  },
  {
    "contains": ["issue", "comment", "120", "--repo", "owner/repo", "--body", "Finding F3 approved with priority 3. Filed as #100."],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "120", "--repo", "owner/repo", "--body", "Recurring patterns: validation duplication."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/maintenance.sh" triage owner/repo 120

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.processed | length')" = "3" ]
  run jq -r '.findings[] | select(.id == "F1") | .status' "$AGENDEV_STATE_DIR/maintenance.json"
  [ "$status" -eq 0 ]
  [ "$output" = "approved" ]
  run jq -r '.findings[] | select(.id == "F2") | .status' "$AGENDEV_STATE_DIR/maintenance.json"
  [ "$status" -eq 0 ]
  [ "$output" = "declined" ]
  run jq -r '.findings[] | select(.id == "F3") | .priority' "$AGENDEV_STATE_DIR/maintenance.json"
  [ "$status" -eq 0 ]
  [ "$output" = "3" ]
  run jq -r 'length' "$AGENDEV_STATE_DIR/processed-mentions.json"
  [ "$status" -eq 0 ]
  [ "$output" = "3" ]
}
