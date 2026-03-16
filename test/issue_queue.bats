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
