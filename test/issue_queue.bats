#!/usr/bin/env bats

load test_helper

@test "issue queue list parses metadata for ready issues" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo", "--label runoq:ready"],
    "stdout_file": "$(fixture_path "issues/list-ready.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" list owner/repo runoq:ready

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
    "contains": ["issue", "list", "--repo owner/repo", "--label runoq:ready"],
    "stdout_file": "$(fixture_path "issues/list-metadata-variants.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" list owner/repo runoq:ready

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
    "contains": ["issue", "list", "--repo owner/repo", "--label runoq:ready"],
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
  "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" next owner/repo runoq:ready >"$result_file"
  status="$?"
  [ "$status" -eq 0 ]
  [ "$(jq -r '.issue.number' "$result_file")" = "22" ]
  [ "$(jq -r '.skipped[0].blocked_reasons[0]' "$result_file")" = "dependency #5 is not runoq:done" ]
}

@test "issue queue next reports missing dependency issues deterministically" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo", "--label runoq:ready"],
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
  "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" next owner/repo runoq:ready >"$result_file"
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
    "contains": ["issue", "list", "--repo owner/repo", "--label runoq:ready"],
    "stdout_file": "$(fixture_path "issues/next-sorted-list.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  result_file="$TEST_TMPDIR/next-sorted.json"
  "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" next owner/repo runoq:ready >"$result_file"
  status="$?"
  [ "$status" -eq 0 ]
  [ "$(jq -r '.issue.number' "$result_file")" = "31" ]
}

@test "issue queue set-status removes old runoq labels and applies exactly one new state label" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "view", "42", "--repo owner/repo", "--json labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"},{\"name\":\"bug\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo owner/repo", "--remove-label runoq:ready", "--add-label runoq:in-progress"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" set-status owner/repo 42 in-progress

  [ "$status" -eq 0 ]
  [[ "$output" == *'"label": "runoq:in-progress"'* ]]
  run grep -q 'issue close' "$FAKE_GH_LOG"
  [ "$status" -ne 0 ]
}

@test "issue queue set-status done closes the issue with repo scope" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "view", "42", "--repo owner/repo", "--json labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"},{\"name\":\"bug\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo owner/repo", "--remove-label runoq:in-progress", "--add-label runoq:done"],
    "stdout": ""
  },
  {
    "contains": ["issue", "close", "42", "--repo owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" set-status owner/repo 42 done

  [ "$status" -eq 0 ]
  [[ "$output" == *'"label": "runoq:done"'* ]]
  run grep -q 'issue close.*42.*--repo owner/repo' "$FAKE_GH_LOG"
  [ "$status" -eq 0 ]
}

@test "issue queue create writes metadata block and ready label" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "create", "--repo owner/repo", "--title Implement queue", "--label runoq:ready"],
    "stdout": "https://github.com/owner/repo/issues/99"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create owner/repo "Implement queue" "## Acceptance Criteria\n\n- [ ] Happy path works." --depends-on 12,14 --priority 1 --estimated-complexity low

  [ "$status" -eq 0 ]
  [[ "$output" == *'"url": "https://github.com/owner/repo/issues/99"'* ]]
  run grep -n "depends_on: \\[12,14\\]" "$FAKE_GH_CAPTURE_DIR/0.body"
  [ "$status" -eq 0 ]
  run grep -n "priority: 1" "$FAKE_GH_CAPTURE_DIR/0.body"
  [ "$status" -eq 0 ]
  run grep -n "estimated_complexity: low" "$FAKE_GH_CAPTURE_DIR/0.body"
  [ "$status" -eq 0 ]
}

@test "issue queue create accepts planning and adjustment types" {
  planning_scenario="$TEST_TMPDIR/planning-scenario.json"
  write_fake_gh_scenario "$planning_scenario" <<EOF
[
  {
    "contains": ["issue", "create", "--repo owner/repo", "--title Plan milestone 1", "--label runoq:ready"],
    "stdout": "https://github.com/owner/repo/issues/99"
  }
]
EOF
  use_fake_gh "$planning_scenario" "$TEST_TMPDIR/planning.state" "$TEST_TMPDIR/planning.log" "$TEST_TMPDIR/planning-capture"

  run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create owner/repo "Plan milestone 1" "body" --type planning --priority 1 --estimated-complexity low

  [ "$status" -eq 0 ]
  run grep -n "type: planning" "$TEST_TMPDIR/planning-capture/0.body"
  [ "$status" -eq 0 ]

  adjustment_scenario="$TEST_TMPDIR/adjustment-scenario.json"
  write_fake_gh_scenario "$adjustment_scenario" <<EOF
[
  {
    "contains": ["issue", "create", "--repo owner/repo", "--title Adjust milestones", "--label runoq:ready"],
    "stdout": "https://github.com/owner/repo/issues/100"
  }
]
EOF
  use_fake_gh "$adjustment_scenario" "$TEST_TMPDIR/adjustment.state" "$TEST_TMPDIR/adjustment.log" "$TEST_TMPDIR/adjustment-capture"

  run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create owner/repo "Adjust milestones" "body" --type adjustment --priority 1 --estimated-complexity low

  [ "$status" -eq 0 ]
  run grep -n "type: adjustment" "$TEST_TMPDIR/adjustment-capture/0.body"
  [ "$status" -eq 0 ]
}

@test "issue queue create rejects invalid types" {
  run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create owner/repo "Bad" "body" --type bogus

  [ "$status" -ne 0 ]
}

@test "issue queue set-status fails cleanly for unknown statuses" {
  run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" set-status owner/repo 42 impossible

  [ "$status" -ne 0 ]
  [[ "$output" == *"Unknown status: impossible"* ]]
}
