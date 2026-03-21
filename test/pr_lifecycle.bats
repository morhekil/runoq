#!/usr/bin/env bats

load test_helper

@test "pr lifecycle create opens a draft PR linked to the issue" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["pr", "create", "--repo owner/repo", "--draft", "--title Implement queue", "--head runoq/42-implement-queue"],
    "stdout": "https://github.com/owner/repo/pull/87"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" create owner/repo runoq/42-implement-queue 42 "Implement queue"

  [ "$status" -eq 0 ]
  [[ "$output" == *'"number": 87'* ]]
  run grep -n "Closes #42" "$FAKE_GH_CAPTURE_DIR/0.body"
  [ "$status" -eq 0 ]
}

@test "pr lifecycle comment posts body content from file" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["pr", "comment", "87", "--repo owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"
  comment_file="$(fixture_path "comments/audit-event.md")"

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" comment owner/repo 87 "$comment_file"

  [ "$status" -eq 0 ]
  run diff -u "$comment_file" "$FAKE_GH_CAPTURE_DIR/0.body"
  [ "$status" -eq 0 ]
}

@test "pr lifecycle update-summary replaces only marker-delimited sections" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["pr", "view", "87", "--repo owner/repo", "--json body"],
    "stdout_file": "$(fixture_path "comments/pr-view-body.json")"
  },
  {
    "contains": ["pr", "edit", "87", "--repo owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" update-summary owner/repo 87 "$(fixture_path "comments/update-summary.md")"

  [ "$status" -eq 0 ]
  run grep -n "Implemented the queue selector" "$FAKE_GH_CAPTURE_DIR/1.body"
  [ "$status" -eq 0 ]
  run grep -n "Review the retry policy" "$FAKE_GH_CAPTURE_DIR/1.body"
  [ "$status" -eq 0 ]
  run grep -n "Closes #42" "$FAKE_GH_CAPTURE_DIR/1.body"
  [ "$status" -eq 0 ]
  run grep -n "| 1 | 28 | ITERATE | comment-1 |" "$FAKE_GH_CAPTURE_DIR/1.body"
  [ "$status" -eq 0 ]
}

@test "pr lifecycle finalize supports auto-merge and needs-review flows" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["pr", "ready", "87", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "merge", "87", "--repo owner/repo", "--auto", "--squash"],
    "stdout": ""
  },
  {
    "contains": ["pr", "ready", "88", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "edit", "88", "--repo owner/repo", "--add-reviewer reviewer1", "--add-assignee reviewer1"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" finalize owner/repo 87 auto-merge
  [ "$status" -eq 0 ]
  [[ "$output" == *'"verdict": "auto-merge"'* ]]

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" finalize owner/repo 88 needs-review --reviewer reviewer1
  [ "$status" -eq 0 ]
  [[ "$output" == *'"reviewer": "reviewer1"'* ]]
}

@test "pr lifecycle finalize falls back to direct squash merge when auto-merge is unavailable" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["pr", "ready", "87", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "merge", "87", "--repo owner/repo", "--auto", "--squash"],
    "stderr": "GraphQL: Pull request Protected branch rules not configured for this branch (enablePullRequestAutoMerge)",
    "exit_code": 1
  },
  {
    "contains": ["pr", "merge", "87", "--repo owner/repo", "--squash", "--delete-branch"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" finalize owner/repo 87 auto-merge

  [ "$status" -eq 0 ]
  [[ "$output" == *'"verdict": "auto-merge"'* ]]
}

@test "pr lifecycle finalize tolerates already-ready PRs and failed reviewer assignment" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["pr", "ready", "88", "--repo owner/repo"],
    "stderr": "! Pull request owner/repo#88 is already \"ready for review\"",
    "exit_code": 1
  },
  {
    "contains": ["pr", "edit", "88", "--repo owner/repo", "--add-reviewer reviewer1", "--add-assignee reviewer1"],
    "stderr": "'reviewer1' not found",
    "exit_code": 1
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" finalize owner/repo 88 needs-review --reviewer reviewer1

  [ "$status" -eq 0 ]
  [[ "$output" == *'"verdict": "needs-review"'* ]]
  [[ "$output" == *'"reviewer": ""'* ]]
}

@test "pr lifecycle line-comment supports multi-line review comments" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["pr", "view", "87", "--repo owner/repo", "--json headRefOid"],
    "stdout": "{\"headRefOid\":\"abc123def\"}"
  },
  {
    "contains": ["api", "repos/owner/repo/pulls/87/comments", "--method POST", "-f body=Needs a guard clause", "-f path=src/retry.ts", "-F commit_id=abc123def", "-F line=14", "-F start_line=10"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" line-comment owner/repo 87 src/retry.ts 10 14 "Needs a guard clause"

  [ "$status" -eq 0 ]
  [[ "$output" == *'"start_line": 10'* ]]
  [[ "$output" == *'"end_line": 14'* ]]
}

@test "pr lifecycle read-actionable returns only mentions and review comments" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "repos/owner/repo/issues/87/comments"],
    "stdout_file": "$(fixture_path "comments/issue-comments.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/pulls/87/comments"],
    "stdout_file": "$(fixture_path "comments/review-comments.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  result_file="$TEST_TMPDIR/actionable.json"
  "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" read-actionable owner/repo 87 runoq >"$result_file"

  [ "$?" -eq 0 ]
  [ "$(jq -r 'length' "$result_file")" = "2" ]
  [ "$(jq -r '.[0].id' "$result_file")" = "1001" ]
  [ "$(jq -r '.[1].comment_type' "$result_file")" = "review" ]
}

@test "pr lifecycle poll-mentions excludes already processed comment ids" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "repos/owner/repo/issues?state=open&per_page=100"],
    "stdout_file": "$(fixture_path "comments/open-items.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/87/comments"],
    "stdout_file": "$(fixture_path "comments/poll-pr-comments.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/issues/90/comments"],
    "stdout_file": "$(fixture_path "comments/poll-issue-comments.json")"
  }
]
EOF
  use_fake_gh "$scenario"
  export TARGET_ROOT="$TEST_TMPDIR/project"
  export RUNOQ_STATE_DIR="$TARGET_ROOT/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"
  "$RUNOQ_ROOT/scripts/state.sh" record-mention 3001 >/dev/null

  result_file="$TEST_TMPDIR/poll.json"
  "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" poll-mentions owner/repo runoq >"$result_file"

  [ "$?" -eq 0 ]
  [ "$(jq -r 'length' "$result_file")" = "1" ]
  [ "$(jq -r '.[0].comment_id' "$result_file")" = "4001" ]
  [ "$(jq -r '.[0].context_type' "$result_file")" = "issue" ]
}

@test "pr lifecycle check-permission enforces minimum access levels" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "repos/owner/repo/collaborators/reviewer1/permission"],
    "stdout_file": "$(fixture_path "comments/permission-write.json")"
  },
  {
    "contains": ["api", "repos/owner/repo/collaborators/reviewer2/permission"],
    "stdout_file": "$(fixture_path "comments/permission-read.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" check-permission owner/repo reviewer1 write
  [ "$status" -eq 0 ]
  [[ "$output" == *'"allowed": true'* ]]

  run "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" check-permission owner/repo reviewer2 admin
  [ "$status" -ne 0 ]
  [[ "$output" == *'"allowed": false'* ]]
}
