#!/usr/bin/env bats

load test_helper

@test "tick helpers merge checklist with empty inputs" {
  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    source "'"$RUNOQ_ROOT"'/scripts/lib/planning.sh"
    runoq::merge_checklists "" ""
  '

  [ "$status" -eq 0 ]
  [ "$output" = "" ]
}

@test "tick helpers merge checklist with both reviewer lists" {
  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    source "'"$RUNOQ_ROOT"'/scripts/lib/planning.sh"
    runoq::merge_checklists $'"'"'- [ ] item a\n- [ ] item b'"'"' $'"'"'- [ ] item c'"'"'
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *"item a"* ]]
  [[ "$output" == *"item b"* ]]
  [[ "$output" == *"item c"* ]]
}

@test "tick helpers parse valid verdict block" {
  verdict_file="$TEST_TMPDIR/verdict.txt"
  cat >"$verdict_file" <<'EOF'
<!-- runoq:payload:plan-review-technical -->
REVIEW-TYPE: plan-technical
VERDICT: ITERATE
SCORE: 31/35
CHECKLIST:
- [ ] fix sequencing
- [ ] reduce scope
EOF

  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    source "'"$RUNOQ_ROOT"'/scripts/lib/planning.sh"
    runoq::parse_verdict_block "'"$verdict_file"'"
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *'"verdict":"ITERATE"'* ]]
  [[ "$output" == *'fix sequencing'* ]]
}

@test "tick helpers parse malformed verdict block without crashing" {
  verdict_file="$TEST_TMPDIR/invalid-verdict.txt"
  printf '%s\n' 'not a verdict' >"$verdict_file"

  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    source "'"$RUNOQ_ROOT"'/scripts/lib/planning.sh"
    runoq::parse_verdict_block "'"$verdict_file"'"
  '

  [ "$status" -ne 0 ]
}

@test "tick helpers format numbered plan proposal" {
  proposal_file="$TEST_TMPDIR/proposal.json"
  cat >"$proposal_file" <<'EOF'
{
  "items": [
    {"title": "First item", "type": "implementation", "goal": "Goal 1", "criteria": ["A"]},
    {"title": "Second item", "type": "discovery", "goal": "Goal 2", "criteria": ["B"]}
  ]
}
EOF

  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    source "'"$RUNOQ_ROOT"'/scripts/lib/planning.sh"
    runoq::format_plan_proposal "'"$proposal_file"'"
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *"<!-- runoq:payload:plan-proposal -->"* ]]
  [[ "$output" == *$'1. First item'* ]]
  [[ "$output" == *$'2. Second item'* ]]
}
