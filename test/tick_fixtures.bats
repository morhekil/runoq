#!/usr/bin/env bats

load test_helper

@test "tick fixtures include discovery planning input" {
  run test -f "$RUNOQ_ROOT/test/fixtures/plans/progress-library-discovery.md"
  [ "$status" -eq 0 ]

  run grep -Eqi 'discovery|feasib|uncertain|determine whether' \
    "$RUNOQ_ROOT/test/fixtures/plans/progress-library-discovery.md"
  [ "$status" -eq 0 ]
}

@test "tick fixtures exist and parse" {
  for f in \
    milestone-decomposer-output.json \
    milestone-decomposer-revised-output.json \
    task-decomposer-milestone-1-output.json \
    task-decomposer-milestone-2-output.json \
    milestone-reviewer-adjustment.json \
    milestone-reviewer-clean.json
  do
    run jq -e . "$RUNOQ_ROOT/test/fixtures/tick/$f"
    [ "$status" -eq 0 ]
  done

  run jq -e '.items | length == 3' \
    "$RUNOQ_ROOT/test/fixtures/tick/milestone-decomposer-output.json"
  [ "$status" -eq 0 ]
  run jq -e '[.items[] | select(.type == "discovery")] | length == 1' \
    "$RUNOQ_ROOT/test/fixtures/tick/milestone-decomposer-output.json"
  [ "$status" -eq 0 ]
  run jq -e '.items | length == 2' \
    "$RUNOQ_ROOT/test/fixtures/tick/milestone-decomposer-revised-output.json"
  [ "$status" -eq 0 ]
  run jq -e '[.proposed_adjustments[] | select(.type == "modify")] | length >= 1' \
    "$RUNOQ_ROOT/test/fixtures/tick/milestone-reviewer-adjustment.json"
  [ "$status" -eq 0 ]
  run jq -e '[.proposed_adjustments[] | select(.type == "new_milestone")] | length >= 1' \
    "$RUNOQ_ROOT/test/fixtures/tick/milestone-reviewer-adjustment.json"
  [ "$status" -eq 0 ]
  run jq -e '.proposed_adjustments | length == 0' \
    "$RUNOQ_ROOT/test/fixtures/tick/milestone-reviewer-clean.json"
  [ "$status" -eq 0 ]

  for f in reviewer-technical-pass.txt reviewer-product-pass.txt; do
    run grep -q 'VERDICT: PASS' "$RUNOQ_ROOT/test/fixtures/tick/$f"
    [ "$status" -eq 0 ]
  done

  for f in \
    comment-response-question.md \
    comment-response-partial-approve.md \
    comment-response-adjustment-partial.md
  do
    run test -f "$RUNOQ_ROOT/test/fixtures/tick/$f"
    [ "$status" -eq 0 ]
  done

  run test -x "$RUNOQ_ROOT/test/helpers/fixture-claude"
  [ "$status" -eq 0 ]
}

@test "fixture claude maps tick fixture names by agent and invocation count" {
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$RUNOQ_ROOT/test/fixtures/tick"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"

  run "$RUNOQ_ROOT/test/helpers/fixture-claude" --agent milestone-decomposer -- '{}'
  [ "$status" -eq 0 ]
  [[ "$output" == *'"items"'* ]]
  [[ "$output" == *'"Caching strategy"'* ]]

  run "$RUNOQ_ROOT/test/helpers/fixture-claude" --agent task-decomposer -- '{}'
  [ "$status" -eq 0 ]
  [[ "$output" == *'"formatter-contract"'* ]]

  run "$RUNOQ_ROOT/test/helpers/fixture-claude" --agent milestone-reviewer -- '{}'
  [ "$status" -eq 0 ]
  [[ "$output" == *'"new_milestone"'* ]]
}
