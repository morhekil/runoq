#!/usr/bin/env bats

load test_helper

write_mentions_config() {
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

@test "authorized mentions are processed once and preserve PR versus issue context" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"
  export RUNOQ_CONFIG="$TEST_TMPDIR/config.json"
  write_mentions_config "$RUNOQ_CONFIG"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "repos/owner/repo/issues?state=open&per_page=100"],
    "stdout_file": "$(fixture_path "comments/open-items.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/87/comments"],
    "stdout": "[{\"id\":3001,\"body\":\"@runoq review this PR\",\"user\":{\"login\":\"reviewer1\"},\"created_at\":\"2026-03-17T01:00:00Z\"}]"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/90/comments"],
    "stdout": "[{\"id\":4001,\"body\":\"@runoq file this follow-up\",\"user\":{\"login\":\"reviewer2\"},\"created_at\":\"2026-03-17T02:00:00Z\"}]"
  },
  {
    "contains": ["api", "repos/owner/repo/collaborators/reviewer1/permission"],
    "stdout_file": "$(fixture_path "comments/permission-write.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/collaborators/reviewer2/permission"],
    "stdout_file": "$(fixture_path "comments/permission-write.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/issues?state=open&per_page=100"],
    "stdout_file": "$(fixture_path "comments/open-items.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/87/comments"],
    "stdout": "[{\"id\":3001,\"body\":\"@runoq review this PR\",\"user\":{\"login\":\"reviewer1\"},\"created_at\":\"2026-03-17T01:00:00Z\"}]"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/90/comments"],
    "stdout": "[{\"id\":4001,\"body\":\"@runoq file this follow-up\",\"user\":{\"login\":\"reviewer2\"},\"created_at\":\"2026-03-17T02:00:00Z\"}]"
  }
]
EOF
  use_fake_gh "$scenario"

  first_result="$TEST_TMPDIR/mentions-first.json"
  second_result="$TEST_TMPDIR/mentions-second.json"

  "$RUNOQ_ROOT/scripts/mentions.sh" process owner/repo runoq >"$first_result"
  [ "$?" -eq 0 ]
  [ "$(jq -r 'length' "$first_result")" = "2" ]
  [ "$(jq -r '.[0].context_type' "$first_result")" = "pr" ]
  [ "$(jq -r '.[1].context_type' "$first_result")" = "issue" ]
  [ "$(jq -r '.[0].action' "$first_result")" = "process" ]
  [ "$(jq -r '.[1].action' "$first_result")" = "process" ]

  "$RUNOQ_ROOT/scripts/mentions.sh" process owner/repo runoq >"$second_result"
  [ "$?" -eq 0 ]
  [ "$(jq -r 'length' "$second_result")" = "0" ]

  run jq -r 'length' "$RUNOQ_STATE_DIR/processed-mentions.json"
  [ "$status" -eq 0 ]
  [ "$output" = "2" ]
}

@test "unauthorized mentions are denied with comments and recorded once" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"
  export RUNOQ_CONFIG="$TEST_TMPDIR/config.json"
  write_mentions_config "$RUNOQ_CONFIG"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "repos/owner/repo/issues?state=open&per_page=100"],
    "stdout_file": "$(fixture_path "comments/open-items.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/87/comments"],
    "stdout": "[{\"id\":3001,\"body\":\"@runoq take another pass on this PR\",\"user\":{\"login\":\"reviewer1\"},\"created_at\":\"2026-03-17T01:00:00Z\"}]"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/90/comments"],
    "stdout": "[{\"id\":4001,\"body\":\"@runoq file this follow-up\",\"user\":{\"login\":\"reviewer2\"},\"created_at\":\"2026-03-17T02:00:00Z\"}]"
  },
  {
    "contains": ["api", "repos/owner/repo/collaborators/reviewer1/permission"],
    "stdout_file": "$(fixture_path "comments/permission-read.json")"
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["api", "repos/owner/repo/collaborators/reviewer2/permission"],
    "stdout_file": "$(fixture_path "comments/permission-read.json")"
  },
  {
    "contains": ["issue", "comment", "90", "--repo", "owner/repo", "--body", "Permission denied for @reviewer2. Requires write access to address @runoq mentions."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  result_file="$TEST_TMPDIR/mentions.json"
  "$RUNOQ_ROOT/scripts/mentions.sh" process owner/repo runoq >"$result_file"

  [ "$?" -eq 0 ]
  [ "$(jq -r 'length' "$result_file")" = "2" ]
  [ "$(jq -r '.[0].action' "$result_file")" = "deny" ]
  [ "$(jq -r '.[1].action' "$result_file")" = "deny" ]
  [ "$(jq -r '.[0].context_type' "$result_file")" = "pr" ]
  [ "$(jq -r '.[1].context_type' "$result_file")" = "issue" ]

  run jq -r 'length' "$RUNOQ_STATE_DIR/processed-mentions.json"
  [ "$status" -eq 0 ]
  [ "$output" = "2" ]

  run rg -n "Permission denied for @reviewer1" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}
