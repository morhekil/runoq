#!/usr/bin/env bats

load test_helper

@test "issue queue list parses metadata for ready issues" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo", "--label agendev:ready"],
    "stdout_file": "$(fixture_path "issues/list-ready.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/gh-issue-queue.sh" list owner/repo agendev:ready

  [ "$status" -eq 0 ]
  [[ "$output" == *'"number": 42'* ]]
  [[ "$output" == *'"depends_on": ['* ]]
  [[ "$output" == *'"priority": 1'* ]]
  [[ "$output" == *'"metadata_valid": true'* ]]
}

@test "issue queue list handles absent and malformed metadata deterministically" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo", "--label agendev:ready"],
    "stdout_file": "$(fixture_path "issues/list-metadata-variants.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/gh-issue-queue.sh" list owner/repo agendev:ready

  [ "$status" -eq 0 ]
  [[ "$output" == *'"number": 11'* ]]
  [[ "$output" == *'"metadata_present": false'* ]]
  [[ "$output" == *'"number": 12'* ]]
  [[ "$output" == *'"metadata_valid": false'* ]]
  [[ "$output" == *'"labels": ['* ]]
}

@test "issue queue next skips blocked dependencies and returns the next actionable issue" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo", "--label agendev:ready"],
    "stdout_file": "$(fixture_path "issues/next-blocked-list.json")"
  },
  {
    "contains": ["issue", "view", "5", "--repo owner/repo"],
    "stdout_file": "$(fixture_path "issues/dependency-in-progress.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  result_file="$TEST_TMPDIR/next-blocked.json"
  "$AGENDEV_ROOT/scripts/gh-issue-queue.sh" next owner/repo agendev:ready >"$result_file"
  status="$?"
  [ "$status" -eq 0 ]
  [ "$(jq -r '.issue.number' "$result_file")" = "22" ]
  [ "$(jq -r '.skipped[0].blocked_reasons[0]' "$result_file")" = "dependency #5 is not agendev:done" ]
}

@test "issue queue next reports missing dependency issues deterministically" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo", "--label agendev:ready"],
    "stdout_file": "$(fixture_path "issues/next-missing-list.json")"
  },
  {
    "contains": ["issue", "view", "404", "--repo owner/repo"],
    "stderr": "not found",
    "exit_code": 1
  }
]
EOF
  use_fake_gh "$scenario"

  result_file="$TEST_TMPDIR/next-missing.json"
  "$AGENDEV_ROOT/scripts/gh-issue-queue.sh" next owner/repo agendev:ready >"$result_file"
  status="$?"
  [ "$status" -eq 0 ]
  [ "$(jq -r '.issue' "$result_file")" = "null" ]
  [ "$(jq -r '.skipped[0].blocked_reasons[0]' "$result_file")" = "missing dependency issue #404" ]
}

@test "issue queue next sorts by priority then issue number for actionable issues" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo", "--label agendev:ready"],
    "stdout_file": "$(fixture_path "issues/next-sorted-list.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  result_file="$TEST_TMPDIR/next-sorted.json"
  "$AGENDEV_ROOT/scripts/gh-issue-queue.sh" next owner/repo agendev:ready >"$result_file"
  status="$?"
  [ "$status" -eq 0 ]
  [ "$(jq -r '.issue.number' "$result_file")" = "31" ]
}
