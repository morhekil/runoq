#!/usr/bin/env bats

load test_helper

tick_issue_json() {
  local number="$1"
  local title="$2"
  local state="$3"
  local body="$4"
  local labels_json="${5:-[]}"
  jq -cn --argjson number "$number" --arg title "$title" --arg state "$state" --arg body "$body" --argjson labels "$labels_json" --arg url "https://example.test/issues/$number" \
    '{number:$number,title:$title,state:$state,body:$body,labels:$labels,url:$url}'
}

meta_body() {
  local type="$1"
  local priority="${2:-1}"
  local parent="${3:-}"
  local milestone_type="${4:-}"
  cat <<EOF
<!-- runoq:meta
depends_on: []
priority: $priority
estimated_complexity: low
type: $type
$( [[ -n "$milestone_type" ]] && printf 'milestone_type: %s\n' "$milestone_type" )
$( [[ -n "$parent" ]] && printf 'parent_epic: %s\n' "$parent" )
-->

## Acceptance Criteria

- [ ] Works.
EOF
}

write_fake_issue_queue_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

log_file="${TICK_ISSUE_QUEUE_LOG:?}"
state_file="${TICK_ISSUE_QUEUE_STATE_FILE:?}"
capture_dir="${TICK_ISSUE_QUEUE_CAPTURE_DIR:?}"
mkdir -p "$(dirname "$state_file")" "$capture_dir"
printf '%s\n' "$*" >>"$log_file"

count=100
if [[ -f "$state_file" ]]; then
  count="$(cat "$state_file")"
fi

cmd="${1:-}"
shift || true
case "$cmd" in
  create)
    repo="$1"; title="$2"; body="$3"
    shift 3 || true
    issue_type="task"
    priority="1"
    estimated_complexity="low"
    complexity_rationale=""
    parent_epic=""
    milestone_type=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --type)
          issue_type="$2"
          shift 2
          ;;
        --priority)
          priority="$2"
          shift 2
          ;;
        --estimated-complexity)
          estimated_complexity="$2"
          shift 2
          ;;
        --complexity-rationale)
          complexity_rationale="$2"
          shift 2
          ;;
        --parent-epic)
          parent_epic="$2"
          shift 2
          ;;
        --milestone-type)
          milestone_type="$2"
          shift 2
          ;;
        *)
          shift
          ;;
      esac
    done
    count=$((count + 1))
    printf '%s\n' "$count" >"$state_file"
    {
      printf '<!-- runoq:meta\n'
      printf 'depends_on: []\n'
      printf 'priority: %s\n' "$priority"
      printf 'estimated_complexity: %s\n' "$estimated_complexity"
      if [[ -n "$complexity_rationale" ]]; then
        printf 'complexity_rationale: %s\n' "$complexity_rationale"
      fi
      printf 'type: %s\n' "$issue_type"
      if [[ -n "$milestone_type" ]]; then
        printf 'milestone_type: %s\n' "$milestone_type"
      fi
      if [[ -n "$parent_epic" ]]; then
        printf 'parent_epic: %s\n' "$parent_epic"
      fi
      printf '%s\n\n' '-->'
      printf '%s\n' "$body"
    } >"$capture_dir/$count.body"
    jq -cn --arg title "$title" --arg url "https://example.test/issues/$count" '{title:$title,url:$url}'
    ;;
  set-status)
    repo="$1"; issue="$2"; status="$3"
    jq -cn --argjson issue "$issue" --arg status "$status" '{issue:$issue,status:$status}'
    ;;
  *)
    echo "unexpected issue-queue command: $cmd" >&2
    exit 1
    ;;
esac
EOF
  chmod +x "$path"
}

write_fake_plan_dispatch_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
repo="$1"
issue="$2"
body_file="$(mktemp "${TMPDIR:-/tmp}/tick-plan-dispatch.XXXXXX")"
printf '<!-- runoq:payload:plan-proposal -->\n1. Stub proposal\n\n```json\n{"items":[{"title":"Stub","type":"implementation","goal":"Goal","criteria":["Done"],"scope":["x"],"sequencing_rationale":"r","priority":1}]}\n```\n' >"$body_file"
"${GH_BIN:-gh}" issue comment "$issue" --repo "$repo" --body-file "$body_file" >/dev/null
printf 'Proposal posted on #%s\n' "$issue"
EOF
  chmod +x "$path"
}

write_fake_run_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TICK_RUN_LOG:?}"
EOF
  chmod +x "$path"
}

write_fake_dispatch_safety_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TICK_DISPATCH_SAFETY_LOG:?}"
EOF
  chmod +x "$path"
}

@test "tick bootstrap creates planning epic and planning issue then posts proposal" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  issue_queue_bin="$TEST_TMPDIR/fake-issue-queue"
  plan_dispatch_bin="$TEST_TMPDIR/fake-plan-dispatch"
  write_fake_issue_queue_bin "$issue_queue_bin"
  write_fake_plan_dispatch_bin "$plan_dispatch_bin"
  export TICK_ISSUE_QUEUE_LOG="$TEST_TMPDIR/issue-queue.log"
  export TICK_ISSUE_QUEUE_STATE_FILE="$TEST_TMPDIR/issue-queue.state"
  export TICK_ISSUE_QUEUE_CAPTURE_DIR="$TEST_TMPDIR/issue-queue-capture"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":"[]"},
  {"contains":["issue","comment","102","--repo","owner/repo"],"stdout":""}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run env RUNOQ_TICK_ISSUE_QUEUE_SCRIPT="$issue_queue_bin" RUNOQ_TICK_PLAN_DISPATCH_SCRIPT="$plan_dispatch_bin" "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Proposal posted on #102"* ]]
  run grep -c '^create ' "$TICK_ISSUE_QUEUE_LOG"
  [ "$status" -eq 0 ]
  [ "$output" = "2" ]
  run grep -q 'issue comment 102 --repo owner/repo' "$FAKE_GH_LOG"
  [ "$status" -eq 0 ]
}

@test "tick awaits human decision when pending review has no new comments" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  epic_body="$(meta_body epic 1)"
  planning_body="$(meta_body planning 1 1)"
  list_json="$(jq -cn --argjson a "$(tick_issue_json 1 'Project Planning' OPEN "$epic_body")" --argjson b "$(tick_issue_json 2 'Break plan into milestones' OPEN "$planning_body")" '[$a,$b]')"
  proposal_comment='<!-- runoq:payload:plan-proposal -->
1. Core formatter

```json
{"items":[{"title":"Core formatter","type":"implementation","goal":"Goal 1","criteria":["A"],"scope":["core"],"sequencing_rationale":"s","priority":1}]}
```'
  view_json="$(jq -cn --argjson number 2 --arg title 'Break plan into milestones' --arg body "$planning_body" --arg proposal "$proposal_comment" '{number:$number,title:$title,body:$body,comments:[{author:{login:"runoq"},body:$proposal}],labels:[],state:"OPEN"}')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')},
  {"contains":["issue","view","2","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$view_json" '$json')}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Awaiting human decision on #2"* ]]
}

@test "tick responds to pending review comments via plan-comment-handler" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  epic_body="$(meta_body epic 1)"
  planning_body="$(meta_body planning 1 1)"
  list_json="$(jq -cn --argjson a "$(tick_issue_json 1 'Project Planning' OPEN "$epic_body")" --argjson b "$(tick_issue_json 2 'Break plan into milestones' OPEN "$planning_body")" '[$a,$b]')"
  proposal_comment='<!-- runoq:payload:plan-proposal -->
1. Core formatter

```json
{"items":[{"title":"Core formatter","type":"implementation","goal":"Goal 1","criteria":["A"],"scope":["core"],"sequencing_rationale":"s","priority":1}]}
```'
  view_json="$(jq -cn --argjson number 2 --arg title 'Break plan into milestones' --arg body "$planning_body" --arg proposal "$proposal_comment" '{number:$number,title:$title,body:$body,comments:[{author:{login:"runoq"},body:$proposal},{author:{login:"human"},body:"Why this order?"}],labels:[],state:"OPEN"}')"

  fixture_dir="$TEST_TMPDIR/fixtures"
  mkdir -p "$fixture_dir"
  cat >"$fixture_dir/plan-comment-responder.txt" <<'EOF'
<!-- runoq:event -->

The order is based on dependency reduction.
EOF
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"
  export FAKE_CLAUDE_LOG="$TEST_TMPDIR/claude.log"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')},
  {"contains":["issue","view","2","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$view_json" '$json')},
  {"contains":["issue","view","2","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$view_json" '$json')},
  {"contains":["issue","comment","2","--repo","owner/repo"],"stdout":""}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Responded to comments on #2"* ]]
  run grep -q 'plan-comment-responder' "$FAKE_CLAUDE_LOG"
  [ "$status" -eq 0 ]
  run grep -q 'issue comment 2 --repo owner/repo' "$FAKE_GH_LOG"
  [ "$status" -eq 0 ]
}

@test "tick applies approved planning proposal and closes review issues" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  issue_queue_bin="$TEST_TMPDIR/fake-issue-queue"
  write_fake_issue_queue_bin "$issue_queue_bin"
  export TICK_ISSUE_QUEUE_LOG="$TEST_TMPDIR/issue-queue.log"
  export TICK_ISSUE_QUEUE_STATE_FILE="$TEST_TMPDIR/issue-queue.state"
  export TICK_ISSUE_QUEUE_CAPTURE_DIR="$TEST_TMPDIR/issue-queue-capture"

  epic_body="$(meta_body epic 1)"
  planning_labels='[{"name":"runoq:plan-approved"}]'
  planning_body="$(meta_body planning 1 1)"
  list_json="$(jq -cn --argjson a "$(tick_issue_json 1 'Project Planning' OPEN "$epic_body")" --argjson b "$(tick_issue_json 2 'Break plan into milestones' OPEN "$planning_body" "$planning_labels")" '[$a,$b]')"
  proposal_comment='<!-- runoq:payload:plan-proposal -->
1. Core formatter
2. Caching strategy
3. CLI wrapper

```json
{"items":[
  {"title":"Core formatter","type":"implementation","goal":"Goal 1","criteria":["A"],"scope":["core"],"sequencing_rationale":"s","priority":1},
  {"title":"Caching strategy","type":"discovery","goal":"Goal 2","criteria":["B"],"scope":["perf"],"sequencing_rationale":"s","priority":2},
  {"title":"CLI wrapper","type":"implementation","goal":"Goal 3","criteria":["C"],"scope":["cli"],"sequencing_rationale":"s","priority":3}
]}
```'
  view_json="$(jq -cn --argjson number 2 --arg title 'Break plan into milestones' --arg body "$planning_body" --arg proposal "$proposal_comment" '{number:$number,title:$title,body:$body,comments:[{author:{login:"runoq"},body:$proposal},{author:{login:"human"},body:"OK, approved with item 3 removed"}],labels:[{name:"runoq:plan-approved"}],state:"OPEN"}')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')},
  {"contains":["issue","view","2","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$view_json" '$json')}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run env RUNOQ_TICK_ISSUE_QUEUE_SCRIPT="$issue_queue_bin" "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Applied approvals from #2"* ]]
  run grep -c '^create ' "$TICK_ISSUE_QUEUE_LOG"
  [ "$status" -eq 0 ]
  [ "$output" = "3" ]
  run grep -q 'set-status owner/repo 2 done' "$TICK_ISSUE_QUEUE_LOG"
  [ "$status" -eq 0 ]
  run grep -q 'set-status owner/repo 1 done' "$TICK_ISSUE_QUEUE_LOG"
  [ "$status" -eq 0 ]
  run grep -q 'milestone_type: discovery' "$TICK_ISSUE_QUEUE_CAPTURE_DIR/102.body"
  [ "$status" -eq 0 ]
}

@test "tick dispatches planning issue when current milestone proposal is missing" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  plan_dispatch_bin="$TEST_TMPDIR/fake-plan-dispatch"
  write_fake_plan_dispatch_bin "$plan_dispatch_bin"

  epic_body="$(meta_body epic 1 '' implementation)"
  planning_body="$(meta_body planning 1 10)"
  list_json="$(jq -cn --argjson a "$(tick_issue_json 10 'Core formatter' OPEN "$epic_body")" --argjson b "$(tick_issue_json 11 'Break down milestone into tasks' OPEN "$planning_body")" '[$a,$b]')"
  planning_view="$(jq -cn --argjson number 11 --arg title 'Break down milestone into tasks' --arg body "$planning_body" '{number:$number,title:$title,body:$body,comments:[],labels:[],state:"OPEN"}')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')},
  {"contains":["issue","view","11","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$planning_view" '$json')},
  {"contains":["issue","comment","11","--repo","owner/repo"],"stdout":""}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run env RUNOQ_TICK_PLAN_DISPATCH_SCRIPT="$plan_dispatch_bin" "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Proposal posted on #11"* ]]
}

@test "tick implementation dispatch delegates to run and dispatch-safety" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  run_bin="$TEST_TMPDIR/fake-run"
  dispatch_safety_bin="$TEST_TMPDIR/fake-dispatch-safety"
  write_fake_run_bin "$run_bin"
  write_fake_dispatch_safety_bin "$dispatch_safety_bin"
  export TICK_RUN_LOG="$TEST_TMPDIR/run.log"
  export TICK_DISPATCH_SAFETY_LOG="$TEST_TMPDIR/dispatch.log"

  epic_body="$(meta_body epic 1 '' implementation)"
  task_body="$(meta_body task 1 10)"
  labels='[{"name":"runoq:ready"}]'
  list_json="$(jq -cn --argjson a "$(tick_issue_json 10 'Core formatter' OPEN "$epic_body")" --argjson b "$(tick_issue_json 12 'Implement formatter' OPEN "$task_body" "$labels")" '[$a,$b]')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run env RUNOQ_TICK_RUN_SCRIPT="$run_bin" RUNOQ_TICK_DISPATCH_SAFETY_SCRIPT="$dispatch_safety_bin" "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Dispatched #10"* ]]
  run grep -q -- '--dry-run' "$TICK_RUN_LOG"
  [ "$status" -eq 0 ]
}

@test "tick closes clean milestone and opens planning for next milestone" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  issue_queue_bin="$TEST_TMPDIR/fake-issue-queue"
  write_fake_issue_queue_bin "$issue_queue_bin"
  export TICK_ISSUE_QUEUE_LOG="$TEST_TMPDIR/issue-queue.log"
  export TICK_ISSUE_QUEUE_STATE_FILE="$TEST_TMPDIR/issue-queue.state"
  export TICK_ISSUE_QUEUE_CAPTURE_DIR="$TEST_TMPDIR/issue-queue-capture"

  fixture_dir="$TEST_TMPDIR/fixtures"
  mkdir -p "$fixture_dir"
  cat >"$fixture_dir/milestone-reviewer.json" <<'EOF'
{"milestone_number":10,"status":"complete","delivered_criteria":["A"],"missed_criteria":[],"learnings":["L"],"proposed_adjustments":[]}
EOF
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"

  epic1_body="$(meta_body epic 1 '' implementation)"
  epic2_body="$(meta_body epic 2 '' implementation)"
  task_body="$(meta_body task 1 10)"
  list_json="$(jq -cn --argjson a "$(tick_issue_json 10 'Core formatter' OPEN "$epic1_body")" --argjson b "$(tick_issue_json 20 'CLI wrapper' OPEN "$epic2_body")" --argjson c "$(tick_issue_json 12 'Implement formatter' CLOSED "$task_body")" '[$a,$b,$c]')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')},
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run env RUNOQ_TICK_ISSUE_QUEUE_SCRIPT="$issue_queue_bin" "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Milestone #10 complete"* ]]
  run grep -q 'set-status owner/repo 10 done' "$TICK_ISSUE_QUEUE_LOG"
  [ "$status" -eq 0 ]
  run grep -q 'create owner/repo Break down milestone into tasks' "$TICK_ISSUE_QUEUE_LOG"
  [ "$status" -eq 0 ]
}

@test "tick creates adjustment issue when milestone reviewer proposes changes" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  issue_queue_bin="$TEST_TMPDIR/fake-issue-queue"
  write_fake_issue_queue_bin "$issue_queue_bin"
  export TICK_ISSUE_QUEUE_LOG="$TEST_TMPDIR/issue-queue.log"
  export TICK_ISSUE_QUEUE_STATE_FILE="$TEST_TMPDIR/issue-queue.state"
  export TICK_ISSUE_QUEUE_CAPTURE_DIR="$TEST_TMPDIR/issue-queue-capture"

  fixture_dir="$TEST_TMPDIR/fixtures"
  mkdir -p "$fixture_dir"
  cat >"$fixture_dir/milestone-reviewer.json" <<'EOF'
{"milestone_number":10,"status":"complete","delivered_criteria":["A"],"missed_criteria":[],"learnings":["L"],"proposed_adjustments":[{"type":"modify","target_milestone_number":20,"title":"Add validation","description":"Add validation scope.","reason":"Needed"},{"type":"new_milestone","title":"Debt cleanup","description":"Clean up shortcuts","reason":"Debt"}]}
EOF
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"

  epic_body="$(meta_body epic 1 '' implementation)"
  task_body="$(meta_body task 1 10)"
  list_json="$(jq -cn --argjson a "$(tick_issue_json 10 'Core formatter' OPEN "$epic_body")" --argjson b "$(tick_issue_json 12 'Implement formatter' CLOSED "$task_body")" '[$a,$b]')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run env RUNOQ_TICK_ISSUE_QUEUE_SCRIPT="$issue_queue_bin" "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Adjustments proposed on #"* ]]
  run grep -q 'create owner/repo Review milestone adjustments' "$TICK_ISSUE_QUEUE_LOG"
  [ "$status" -eq 0 ]
  run grep -q '1. modify:' "$TICK_ISSUE_QUEUE_CAPTURE_DIR/101.body"
  [ "$status" -eq 0 ]
}

@test "tick forces adjustment issue for discovery milestones even with clean review" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  issue_queue_bin="$TEST_TMPDIR/fake-issue-queue"
  write_fake_issue_queue_bin "$issue_queue_bin"
  export TICK_ISSUE_QUEUE_LOG="$TEST_TMPDIR/issue-queue.log"
  export TICK_ISSUE_QUEUE_STATE_FILE="$TEST_TMPDIR/issue-queue.state"
  export TICK_ISSUE_QUEUE_CAPTURE_DIR="$TEST_TMPDIR/issue-queue-capture"

  fixture_dir="$TEST_TMPDIR/fixtures"
  mkdir -p "$fixture_dir"
  cat >"$fixture_dir/milestone-reviewer.json" <<'EOF'
{"milestone_number":10,"status":"complete","delivered_criteria":["A"],"missed_criteria":[],"learnings":["L"],"proposed_adjustments":[]}
EOF
  export RUNOQ_CLAUDE_BIN="$RUNOQ_ROOT/test/helpers/fixture-claude"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$TEST_TMPDIR/agent-state"

  epic_body="$(meta_body epic 1 '' discovery)"
  task_body="$(meta_body task 1 10)"
  list_json="$(jq -cn --argjson a "$(tick_issue_json 10 'Caching strategy' OPEN "$epic_body")" --argjson b "$(tick_issue_json 12 'Benchmark caching' CLOSED "$task_body")" '[$a,$b]')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run env RUNOQ_TICK_ISSUE_QUEUE_SCRIPT="$issue_queue_bin" "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Adjustments proposed on #"* ]]
  run grep -q 'type adjustment' "$TICK_ISSUE_QUEUE_LOG"
  [ "$status" -eq 0 ]
}

@test "tick reports project complete when all milestone epics are closed" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  epic_body="$(meta_body epic 1 '' implementation)"
  list_json="$(jq -cn --argjson a "$(tick_issue_json 10 'Core formatter' CLOSED "$epic_body")" '[$a]')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"Project complete"* ]]
}

@test "tick is idempotent in awaiting-review state" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  cat >"$project_dir/runoq.json" <<'EOF'
{"plan":"docs/prd.md"}
EOF
  mkdir -p "$project_dir/docs"
  printf '# Plan\n' >"$project_dir/docs/prd.md"
  export TARGET_ROOT="$project_dir"

  epic_body="$(meta_body epic 1)"
  planning_body="$(meta_body planning 1 1)"
  list_json="$(jq -cn --argjson a "$(tick_issue_json 1 'Project Planning' OPEN "$epic_body")" --argjson b "$(tick_issue_json 2 'Break plan into milestones' OPEN "$planning_body")" '[$a,$b]')"
  proposal_comment='<!-- runoq:payload:plan-proposal -->
1. Core formatter

```json
{"items":[{"title":"Core formatter","type":"implementation","goal":"Goal 1","criteria":["A"],"scope":["core"],"sequencing_rationale":"s","priority":1}]}
```'
  view_json="$(jq -cn --argjson number 2 --arg title 'Break plan into milestones' --arg body "$planning_body" --arg proposal "$proposal_comment" '{number:$number,title:$title,body:$body,comments:[{author:{login:"runoq"},body:$proposal}],labels:[],state:"OPEN"}')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')},
  {"contains":["issue","view","2","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$view_json" '$json')},
  {"contains":["issue","list","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$list_json" '$json')},
  {"contains":["issue","view","2","--repo","owner/repo"],"stdout":$(jq -Rn --arg json "$view_json" '$json')}
]
EOF
  use_fake_gh "$scenario" "$TEST_TMPDIR/gh.state" "$TEST_TMPDIR/gh.log" "$TEST_TMPDIR/gh-capture"

  run "$RUNOQ_ROOT/scripts/tick.sh"
  first="$output"
  [ "$status" -eq 0 ]
  run "$RUNOQ_ROOT/scripts/tick.sh"
  [ "$status" -eq 0 ]
  [ "$output" = "$first" ]
}

@test "tick fails clearly when runoq.json is missing" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"

  run "$RUNOQ_ROOT/scripts/tick.sh"

  [ "$status" -ne 0 ]
  [[ "$output" == *"runoq.json"* ]]
}
