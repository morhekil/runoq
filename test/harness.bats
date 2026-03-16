#!/usr/bin/env bats

load test_helper

@test "fake gh harness returns fixture data and records calls" {
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo owner/repo"],
    "stdout_file": "$(fixture_path "issues/list-ready.json")"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$GH_BIN" issue list --repo owner/repo --json number,title

  [ "$status" -eq 0 ]
  [[ "$output" == *"Implement queue orchestration"* ]]
  run cat "$FAKE_GH_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"issue list --repo owner/repo --json number,title"* ]]
}
