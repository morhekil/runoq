#!/usr/bin/env bats

load test_helper

write_plan_dispatch_fixture() {
  local dir="$1"
  mkdir -p "$dir"
}

@test "plan dispatch posts proposal when both reviewers pass on round 1" {
  fixture_dir="$TEST_TMPDIR/fixtures"
  write_plan_dispatch_fixture "$fixture_dir"
  cat >"$fixture_dir/milestone-decomposer.json" <<'EOF'
{"items":[
  {"key":"m1","title":"Core formatter","type":"implementation","goal":"Formatter works","criteria":["Formats input"],"scope":["core"],"sequencing_rationale":"Foundational","priority":1},
  {"key":"m2","title":"Caching strategy","type":"discovery","goal":"Decide whether caching is needed","criteria":["Decision recorded"],"scope":["performance"],"sequencing_rationale":"Needs validation","priority":2}
],"warnings":[]}
EOF
  cat >"$fixture_dir/plan-reviewer-technical.txt" <<'EOF'
<!-- runoq:payload:plan-review-technical -->
REVIEW-TYPE: plan-technical
VERDICT: PASS
SCORE: 34/35
CHECKLIST:
- [ ] None.
EOF
  cat >"$fixture_dir/plan-reviewer-product.txt" <<'EOF'
<!-- runoq:payload:plan-review-product -->
REVIEW-TYPE: plan-product
VERDICT: PASS
SCORE: 28/30
CHECKLIST:
- [ ] None.
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/capture"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export RUNOQ_LOG_ROOT="$TEST_TMPDIR/log"
  plan_file="$TEST_TMPDIR/plan.md"
  printf '%s\n' '# Plan' >"$plan_file"

  run "$RUNOQ_ROOT/scripts/plan-dispatch.sh" owner/repo 42 milestone "$plan_file"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Proposal posted on #42"* ]]
  run grep -c 'milestone-decomposer\|task-decomposer' "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [ "$output" = "1" ]
  run grep -c 'plan-reviewer-technical' "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [ "$output" = "1" ]
  run grep -c 'plan-reviewer-product' "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [ "$output" = "1" ]
  run grep -q 'runoq:payload:plan-proposal' "$TEST_TMPDIR/capture/0.body"
  [ "$status" -eq 0 ]
  run grep -qE '^1\. ' "$TEST_TMPDIR/capture/0.body"
  [ "$status" -eq 0 ]
  run grep -qE '^2\. ' "$TEST_TMPDIR/capture/0.body"
  [ "$status" -eq 0 ]
}

@test "plan dispatch re-invokes decomposer with merged checklist when review iterates" {
  fixture_dir="$TEST_TMPDIR/fixtures"
  write_plan_dispatch_fixture "$fixture_dir"
  cat >"$fixture_dir/milestone-decomposer-1.json" <<'EOF'
{"items":[{"key":"m1","title":"Core formatter","type":"implementation","goal":"Formatter works","criteria":["Formats input"],"scope":["core"],"sequencing_rationale":"Foundational","priority":1}],"warnings":[]}
EOF
  cat >"$fixture_dir/milestone-decomposer-2.json" <<'EOF'
{"items":[{"key":"m1","title":"Core formatter revised","type":"implementation","goal":"Formatter works","criteria":["Formats input"],"scope":["core"],"sequencing_rationale":"Foundational","priority":1}],"warnings":[]}
EOF
  cat >"$fixture_dir/plan-reviewer-technical-1.txt" <<'EOF'
<!-- runoq:payload:plan-review-technical -->
REVIEW-TYPE: plan-technical
VERDICT: ITERATE
SCORE: 20/35
CHECKLIST:
- [ ] tighten sequencing
EOF
  cat >"$fixture_dir/plan-reviewer-technical-2.txt" <<'EOF'
<!-- runoq:payload:plan-review-technical -->
REVIEW-TYPE: plan-technical
VERDICT: PASS
SCORE: 33/35
CHECKLIST:
- [ ] None.
EOF
  cat >"$fixture_dir/plan-reviewer-product-1.txt" <<'EOF'
<!-- runoq:payload:plan-review-product -->
REVIEW-TYPE: plan-product
VERDICT: PASS
SCORE: 27/30
CHECKLIST:
- [ ] None.
EOF
  cat >"$fixture_dir/plan-reviewer-product-2.txt" <<'EOF'
<!-- runoq:payload:plan-review-product -->
REVIEW-TYPE: plan-product
VERDICT: PASS
SCORE: 29/30
CHECKLIST:
- [ ] None.
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/capture"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export RUNOQ_LOG_ROOT="$TEST_TMPDIR/log"
  plan_file="$TEST_TMPDIR/plan.md"
  printf '%s\n' '# Plan' >"$plan_file"

  run "$RUNOQ_ROOT/scripts/plan-dispatch.sh" owner/repo 42 milestone "$plan_file"

  [ "$status" -eq 0 ]
  run grep -c 'milestone-decomposer' "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [ "$output" = "2" ]
  run grep -q 'CHECKLIST' "$TEST_TMPDIR"/log/claude/milestone-decomposer-*/request.txt
  [ "$status" -eq 0 ]
}

@test "plan dispatch posts best-effort proposal when max review rounds reached" {
  fixture_dir="$TEST_TMPDIR/fixtures"
  write_plan_dispatch_fixture "$fixture_dir"
  cat >"$fixture_dir/milestone-decomposer.json" <<'EOF'
{"items":[{"key":"m1","title":"Core formatter","type":"implementation","goal":"Formatter works","criteria":["Formats input"],"scope":["core"],"sequencing_rationale":"Foundational","priority":1}],"warnings":[]}
EOF
  cat >"$fixture_dir/plan-reviewer-technical.txt" <<'EOF'
<!-- runoq:payload:plan-review-technical -->
REVIEW-TYPE: plan-technical
VERDICT: ITERATE
SCORE: 20/35
CHECKLIST:
- [ ] tighten sequencing
EOF
  cat >"$fixture_dir/plan-reviewer-product.txt" <<'EOF'
<!-- runoq:payload:plan-review-product -->
REVIEW-TYPE: plan-product
VERDICT: ITERATE
SCORE: 18/30
CHECKLIST:
- [ ] improve MVP focus
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/capture"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export RUNOQ_LOG_ROOT="$TEST_TMPDIR/log"
  plan_file="$TEST_TMPDIR/plan.md"
  printf '%s\n' '# Plan' >"$plan_file"

  run "$RUNOQ_ROOT/scripts/plan-dispatch.sh" owner/repo 42 milestone "$plan_file"

  [ "$status" -eq 0 ]
  run grep -q 'max review rounds reached' "$TEST_TMPDIR/capture/0.body"
  [ "$status" -eq 0 ]
}

@test "plan dispatch fails without posting proposal on invalid decomposer output" {
  fixture_dir="$TEST_TMPDIR/fixtures"
  write_plan_dispatch_fixture "$fixture_dir"
  printf '%s\n' 'not json' >"$fixture_dir/milestone-decomposer.txt"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/capture"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export RUNOQ_LOG_ROOT="$TEST_TMPDIR/log"
  plan_file="$TEST_TMPDIR/plan.md"
  printf '%s\n' '# Plan' >"$plan_file"

  run "$RUNOQ_ROOT/scripts/plan-dispatch.sh" owner/repo 42 milestone "$plan_file"

  [ "$status" -ne 0 ]
  run ls "$TEST_TMPDIR/capture"
  [ "$status" -ne 0 ]
}

@test "plan dispatch supports task decomposer mode" {
  fixture_dir="$TEST_TMPDIR/fixtures"
  write_plan_dispatch_fixture "$fixture_dir"
  cat >"$fixture_dir/task-decomposer.json" <<'EOF'
{"items":[
  {"key":"t1","type":"task","title":"Implement formatter","body":"## Acceptance Criteria\n\n- [ ] Works.","priority":1,"estimated_complexity":"low","complexity_rationale":"single module","depends_on_keys":[]},
  {"key":"t2","type":"task","title":"Benchmark caching","body":"## Acceptance Criteria\n\n- [ ] Works.","priority":2,"estimated_complexity":"medium","complexity_rationale":"benchmarking","depends_on_keys":["t1"]}
],"warnings":[]}
EOF
  cat >"$fixture_dir/plan-reviewer-technical.txt" <<'EOF'
<!-- runoq:payload:plan-review-technical -->
REVIEW-TYPE: plan-technical
VERDICT: PASS
SCORE: 34/35
CHECKLIST:
- [ ] None.
EOF
  cat >"$fixture_dir/plan-reviewer-product.txt" <<'EOF'
<!-- runoq:payload:plan-review-product -->
REVIEW-TYPE: plan-product
VERDICT: PASS
SCORE: 28/30
CHECKLIST:
- [ ] None.
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["issue", "comment", "77", "--repo", "owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/capture"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export RUNOQ_LOG_ROOT="$TEST_TMPDIR/log"
  plan_file="$TEST_TMPDIR/plan.md"
  milestone_file="$TEST_TMPDIR/milestone.json"
  printf '%s\n' '# Plan' >"$plan_file"
  printf '%s\n' '{"title":"M1"}' >"$milestone_file"

  run "$RUNOQ_ROOT/scripts/plan-dispatch.sh" owner/repo 77 task "$plan_file" "$milestone_file"

  [ "$status" -eq 0 ]
  run grep -q 'task-decomposer' "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
}

@test "plan dispatch retries once when a reviewer returns an empty response" {
  fixture_dir="$TEST_TMPDIR/fixtures"
  write_plan_dispatch_fixture "$fixture_dir"
  cat >"$fixture_dir/task-decomposer.json" <<'EOF'
{"items":[
  {"key":"t1","type":"task","title":"Implement formatter","body":"## Acceptance Criteria\n\n- [ ] Works.","priority":1,"estimated_complexity":"low","complexity_rationale":"single module","depends_on_keys":[]}
],"warnings":[]}
EOF
  cat >"$fixture_dir/plan-reviewer-technical.txt" <<'EOF'
<!-- runoq:payload:plan-review-technical -->
REVIEW-TYPE: plan-technical
VERDICT: PASS
SCORE: 34/35
CHECKLIST:
- [ ] None.
EOF
  : >"$fixture_dir/plan-reviewer-product-1.txt"
  cat >"$fixture_dir/plan-reviewer-product-2.txt" <<'EOF'
<!-- runoq:payload:plan-review-product -->
REVIEW-TYPE: plan-product
VERDICT: PASS
SCORE: 28/30
CHECKLIST:
- [ ] None.
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["issue", "comment", "77", "--repo", "owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/capture"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export RUNOQ_LOG_ROOT="$TEST_TMPDIR/log"
  plan_file="$TEST_TMPDIR/plan.md"
  milestone_file="$TEST_TMPDIR/milestone.json"
  printf '%s\n' '# Plan' >"$plan_file"
  printf '%s\n' '{"title":"M1"}' >"$milestone_file"

  run "$RUNOQ_ROOT/scripts/plan-dispatch.sh" owner/repo 77 task "$plan_file" "$milestone_file"

  [ "$status" -eq 0 ]
  run grep -c 'plan-reviewer-product' "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [ "$output" = "2" ]
}

@test "plan dispatch retries once when a decomposer returns only whitespace" {
  fixture_dir="$TEST_TMPDIR/fixtures"
  write_plan_dispatch_fixture "$fixture_dir"
  printf '\n' >"$fixture_dir/task-decomposer-1.txt"
  cat >"$fixture_dir/task-decomposer-2.json" <<'EOF'
{"items":[
  {"key":"t1","type":"task","title":"Implement formatter","body":"## Acceptance Criteria\n\n- [ ] Works.","priority":1,"estimated_complexity":"low","complexity_rationale":"single module","depends_on_keys":[]}
],"warnings":[]}
EOF
  cat >"$fixture_dir/plan-reviewer-technical.txt" <<'EOF'
<!-- runoq:payload:plan-review-technical -->
REVIEW-TYPE: plan-technical
VERDICT: PASS
SCORE: 34/35
CHECKLIST:
- [ ] None.
EOF
  cat >"$fixture_dir/plan-reviewer-product.txt" <<'EOF'
<!-- runoq:payload:plan-review-product -->
REVIEW-TYPE: plan-product
VERDICT: PASS
SCORE: 28/30
CHECKLIST:
- [ ] None.
EOF

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["issue", "comment", "77", "--repo", "owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/capture"
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"
  export RUNOQ_LOG_ROOT="$TEST_TMPDIR/log"
  plan_file="$TEST_TMPDIR/plan.md"
  milestone_file="$TEST_TMPDIR/milestone.json"
  printf '%s\n' '# Plan' >"$plan_file"
  printf '%s\n' '{"title":"M1"}' >"$milestone_file"

  run "$RUNOQ_ROOT/scripts/plan-dispatch.sh" owner/repo 77 task "$plan_file" "$milestone_file"

  [ "$status" -eq 0 ]
  run grep -c 'task-decomposer' "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  [ "$output" = "2" ]
}
