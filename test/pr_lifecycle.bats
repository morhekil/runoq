#!/usr/bin/env bats

load test_helper

@test "pr lifecycle create opens a draft PR linked to the issue" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["pr", "create", "--repo owner/repo", "--draft", "--title Implement queue", "--head agendev/42-implement-queue"],
    "stdout": "https://github.com/owner/repo/pull/87"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/gh-pr-lifecycle.sh" create owner/repo agendev/42-implement-queue 42 "Implement queue"

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

  run "$AGENDEV_ROOT/scripts/gh-pr-lifecycle.sh" comment owner/repo 87 "$comment_file"

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

  run "$AGENDEV_ROOT/scripts/gh-pr-lifecycle.sh" update-summary owner/repo 87 "$(fixture_path "comments/update-summary.md")"

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

  run "$AGENDEV_ROOT/scripts/gh-pr-lifecycle.sh" finalize owner/repo 87 auto-merge
  [ "$status" -eq 0 ]
  [[ "$output" == *'"verdict": "auto-merge"'* ]]

  run "$AGENDEV_ROOT/scripts/gh-pr-lifecycle.sh" finalize owner/repo 88 needs-review --reviewer reviewer1
  [ "$status" -eq 0 ]
  [[ "$output" == *'"reviewer": "reviewer1"'* ]]
}
