#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"
source "$(cd "$(dirname "$0")" && pwd)/lib/planning.sh"

usage() {
  cat <<'EOF'
Usage:
  tick.sh
EOF
}

metadata_value() { runoq::metadata_value "$@"; }
issue_type() { runoq::issue_type "$@"; }
issue_parent_epic() { runoq::issue_parent_epic "$@"; }
issue_milestone_type() { runoq::issue_milestone_type "$@"; }

issue_priority() {
  local body="$1"
  local value
  value="$(runoq::metadata_value "$body" "priority")"
  printf '%s\n' "${value:-999999}"
}

has_label() {
  local issue_json="$1"
  local label="$2"
  printf '%s' "$issue_json" | jq -e --arg label "$label" '.labels // [] | map(.name) | index($label)' >/dev/null 2>&1
}

extract_proposal_json_from_text() {
  local text="$1"
  printf '%s' "$text" | "$(runoq::root)/scripts/tick-fmt.sh" extract-json 'runoq:payload:plan-proposal' 2>/dev/null || true
}

extract_fenced_json_from_text() {
  local text="$1"
  printf '%s' "$text" | perl -0ne 'if (/```json\s*(\{.*?\})\s*```/s) { print $1 }'
}

read_json_output() {
  local path="$1"
  local marker="$2"
  if jq -e . "$path" >/dev/null 2>&1; then
    cat "$path"
    return 0
  fi
  local extracted
  extracted="$(runoq::extract_marked_json_block "$path" "$marker" 2>/dev/null || true)"
  [[ -n "$extracted" ]] || return 1
  printf '%s' "$extracted" | jq -e . >/dev/null 2>&1 || return 1
  printf '%s\n' "$extracted"
}

proposal_json_from_issue_view() {
  local issue_view_json="$1"
  local body proposal
  body="$(printf '%s' "$issue_view_json" | jq -r '.body // ""')"
  proposal="$(extract_proposal_json_from_text "$body")"
  [[ -n "$proposal" ]] || return 1
  printf '%s\n' "$proposal"
}

adjustment_json_from_issue_view() {
  local issue_view_json="$1"
  local text tmp extracted
  text="$(printf '%s' "$issue_view_json" | jq -r '.body // ""')"
  if printf '%s' "$text" | jq -e . >/dev/null 2>&1; then
    printf '%s\n' "$text"
    return 0
  fi
  tmp="$(mktemp "${TMPDIR:-/tmp}/runoq-adjustment-text.XXXXXX")"
  printf '%s' "$text" >"$tmp"
  extracted="$(runoq::extract_marked_json_block "$tmp" 'runoq:payload:milestone-reviewer' 2>/dev/null || true)"
  if [[ -z "$extracted" ]]; then
    extracted="$(extract_fenced_json_from_text "$text")"
  fi
  [[ -n "$extracted" ]] || return 1
  printf '%s\n' "$extracted"
}

human_comment_selection() {
  local issue_view_json="$1"
  printf '%s' "$issue_view_json" | "$(runoq::root)/scripts/tick-fmt.sh" human-comment-selection
}

select_items_from_proposal() {
  local proposal_json="$1"
  local selection_json="$2"
  printf '%s' "$proposal_json" | "$(runoq::root)/scripts/tick-fmt.sh" select-items --selection "$selection_json"
}

react_to_last_human_comment() {
  local issue_view_json="$1"
  local comment_id
  comment_id="$(printf '%s' "$issue_view_json" | jq -r '
    [.comments // [] | .[] | select((.author.login // "") != "runoq" and ((.body // "") | contains("runoq:event") | not))]
    | last | .id // empty
  ')"
  [[ -n "$comment_id" ]] || return 0
  runoq::gh api graphql -f query="$(printf 'mutation { addReaction(input: {subjectId: "%s", content: EYES}) { reaction { content } } }' "$comment_id")" >/dev/null 2>&1 || true
}

latest_human_comment_unanswered() {
  local issue_view_json="$1"
  printf '%s' "$issue_view_json" | jq -e '
    (.comments // [] | last) as $last
    | if $last == null then false
      else (($last.author.login // "") != "runoq" and (($last.body // "") | contains("runoq:event") | not))
      end
  ' >/dev/null 2>&1
}

normalize_issue() {
  printf '%s' "$1" | jq -c '.'
}

first_open_epic() {
  local issues_json="$1"
  local best="" best_priority=999999
  while IFS= read -r encoded; do
    local issue body state type priority
    issue="$(printf '%s' "$encoded" | base64 -d)"
    state="$(printf '%s' "$issue" | jq -r '.state')"
    body="$(printf '%s' "$issue" | jq -r '.body // ""')"
    type="$(issue_type "$body")"
    [[ "$state" == "OPEN" && "$type" == "epic" ]] || continue
    priority="$(issue_priority "$body")"
    if (( priority < best_priority )); then
      best_priority="$priority"
      best="$issue"
    fi
  done < <(printf '%s' "$issues_json" | jq -r '.[] | @base64')
  printf '%s\n' "$best"
}

any_epic_exists() {
  local issues_json="$1"
  while IFS= read -r encoded; do
    local issue body type
    issue="$(printf '%s' "$encoded" | base64 -d)"
    body="$(printf '%s' "$issue" | jq -r '.body // ""')"
    type="$(issue_type "$body")"
    [[ "$type" == "epic" ]] && return 0
  done < <(printf '%s' "$issues_json" | jq -r '.[] | @base64')
  return 1
}

find_review_issue() {
  local issues_json="$1"
  local mode="$2"
  local plan_approved_label="$3"
  while IFS= read -r encoded; do
    local issue body state type
    issue="$(printf '%s' "$encoded" | base64 -d)"
    state="$(printf '%s' "$issue" | jq -r '.state')"
    body="$(printf '%s' "$issue" | jq -r '.body // ""')"
    type="$(issue_type "$body")"
    [[ "$state" == "OPEN" ]] || continue
    [[ "$type" == "planning" || "$type" == "adjustment" ]] || continue
    if [[ "$mode" == "approved" ]]; then
      has_label "$issue" "$plan_approved_label" || continue
    else
      has_label "$issue" "$plan_approved_label" && continue
    fi
    printf '%s\n' "$issue"
    return 0
  done < <(printf '%s' "$issues_json" | jq -r '.[] | @base64')
  return 1
}

children_for_epic() {
  local issues_json="$1"
  local epic_number="$2"
  while IFS= read -r encoded; do
    local issue body parent
    issue="$(printf '%s' "$encoded" | base64 -d)"
    body="$(printf '%s' "$issue" | jq -r '.body // ""')"
    parent="$(issue_parent_epic "$body")"
    [[ "$parent" == "$epic_number" ]] || continue
    printf '%s\n' "$issue"
  done < <(printf '%s' "$issues_json" | jq -r '.[] | @base64')
}

planning_issue_needs_dispatch() {
  local issue_json="$1" repo="$2"
  local issue_number issue_view
  issue_number="$(printf '%s' "$issue_json" | jq -r '.number')"
  issue_view="$(runoq::gh issue view "$issue_number" --repo "$repo" --json number,title,body,comments,labels,state)"
  if printf '%s' "$issue_view" | jq -r '.body // ""' | grep -q 'runoq:payload:plan-proposal'; then
    return 1
  fi
  return 0
}

create_planning_issue() {
  local issue_queue_script="$1" repo="$2" parent_epic="$3" title="$4" body="$5"
  "$issue_queue_script" create "$repo" "$title" "$body" --type planning --priority 1 --estimated-complexity low --parent-epic "$parent_epic"
}

assign_issue() {
  local issue_queue_script="$1" repo="$2" issue_number="$3"
  "$issue_queue_script" assign "$repo" "$issue_number" >/dev/null 2>&1 || true
}

create_task_issue() {
  local issue_queue_script="$1" repo="$2" title="$3" body="$4" priority="$5" complexity="$6" rationale="$7" parent_epic="$8"
  local max_attempts pause attempt output
  max_attempts="${RUNOQ_TICK_TASK_CREATE_ATTEMPTS:-2}"
  pause="${RUNOQ_TICK_TASK_CREATE_RETRY_DELAY_SECONDS:-1}"
  attempt=1

  while true; do
    if output="$("$issue_queue_script" create "$repo" "$title" "$body" --type task --priority "$priority" --estimated-complexity "$complexity" --complexity-rationale "$rationale" --parent-epic "$parent_epic")"; then
      printf '%s\n' "$output"
      return 0
    fi
    if (( attempt >= max_attempts )); then
      return 1
    fi
    runoq::log "tick" "task create failed for '${title}', retrying (${attempt}/${max_attempts})"
    sleep "$pause"
    attempt=$((attempt + 1))
  done
}

handle_bootstrap() {
  local epic_output planning_output planning_number epic_number
  runoq::step "Bootstrapping project"
  runoq::info "creating Project Planning epic"
  epic_output="$("$issue_queue_script" create "$repo" "Project Planning" "## Acceptance Criteria\n\n- [ ] Milestones proposed." --type epic --priority 1 --estimated-complexity low)"
  epic_number="$(printf '%s' "$epic_output" | jq -r '.url | capture("(?<n>[0-9]+)$").n')"
  runoq::detail "epic" "#$epic_number"

  runoq::info "creating planning issue"
  planning_output="$(create_planning_issue "$issue_queue_script" "$repo" "$epic_number" "Break plan into milestones" "## Acceptance Criteria\n\n- [ ] Milestones proposed.")"
  planning_number="$(printf '%s' "$planning_output" | jq -r '.url | capture("(?<n>[0-9]+)$").n')"
  runoq::detail "planning issue" "#$planning_number"

  runoq::step "Running milestone decomposition on #$planning_number"
  "$plan_dispatch_script" "$repo" "$planning_number" milestone "$plan_file" >/dev/null
  assign_issue "$issue_queue_script" "$repo" "$planning_number"
  runoq::success "Proposal posted on #$planning_number"
  printf 'Created planning milestone. Proposal posted on #%s\n' "$planning_number"
}

handle_pending_review() {
  local pending_review="$1"
  local pending_number issue_view
  pending_number="$(printf '%s' "$pending_review" | jq -r '.number')"
  runoq::step "Loading review #$pending_number"
  issue_view="$(runoq::gh issue view "$pending_number" --repo "$repo" --json number,title,body,comments,labels,state)"
  if [[ "$(issue_type "$(printf '%s' "$pending_review" | jq -r '.body // ""')")" == "planning" ]] && ! printf '%s' "$issue_view" | jq -r '.body // ""' | grep -q 'runoq:payload:plan-proposal'; then
    runoq::info "planning issue #$pending_number has no proposal yet"
    return 1
  elif latest_human_comment_unanswered "$issue_view"; then
    runoq::step "Responding to unanswered comments on #$pending_number"
    if ! "$comment_handler_script" "$repo" "$pending_number" "$plan_file" >/dev/null; then
      runoq::warn "Comment handler failed for #$pending_number"
      printf 'Comment handler failed for #%s\n' "$pending_number"
      return 0
    fi
    react_to_last_human_comment "$issue_view"
    runoq::success "Responded to comments on #$pending_number"
    printf 'Responded to comments on #%s\n' "$pending_number"
    return 0
  else
    runoq::warn "Awaiting human decision on #$pending_number"
    printf 'Awaiting human decision on #%s\n' "$pending_number"
    return 0
  fi
}

handle_approved_planning() {
  local review_view="$1" review_number="$2" review_parent="$3" selection_json="$4"
  local proposal_json filtered_json parent_title item_count
  proposal_json="$(proposal_json_from_issue_view "$review_view")"
  filtered_json="$(select_items_from_proposal "$proposal_json" "$selection_json")"
  parent_title="$(printf '%s' "$issues_json" | jq -r --argjson n "$review_parent" '.[] | select(.number == $n) | .title')"
  item_count="$(printf '%s' "$filtered_json" | jq '.items | length')"
  runoq::detail "parent" "#$review_parent $parent_title"
  runoq::detail "items to create" "$item_count"

  if [[ "$parent_title" == "Project Planning" ]]; then
    runoq::info "creating milestone epics"
    local first_milestone=""
    while IFS= read -r item; do
      [[ -n "$item" ]] || continue
      local title body priority milestone_type create_output created_number
      title="$(printf '%s' "$item" | jq -r '.title')"
      priority="$(printf '%s' "$item" | jq -r '.priority // 1')"
      milestone_type="$(printf '%s' "$item" | jq -r '.type')"
      body="$(printf '%s' "$item" | "$(runoq::root)/scripts/tick-fmt.sh" milestone-body)"
      create_output="$("$issue_queue_script" create "$repo" "$title" "$body" --type epic --priority "$priority" --estimated-complexity low --milestone-type "$milestone_type")"
      created_number="$(printf '%s' "$create_output" | jq -r '.url | capture("(?<n>[0-9]+)$").n')"
      runoq::info "created epic #$created_number: $title"
      if [[ -z "$first_milestone" ]]; then
        first_milestone="$created_number"
      fi
    done < <(printf '%s' "$filtered_json" | jq -c '.items[]')
    if [[ -n "$first_milestone" ]]; then
      runoq::info "creating planning issue for first milestone #$first_milestone"
      create_planning_issue "$issue_queue_script" "$repo" "$first_milestone" "Break down milestone into tasks" "## Acceptance Criteria\n\n- [ ] Tasks proposed." >/dev/null
    fi
    runoq::info "closing review #$review_number and parent #$review_parent"
    "$issue_queue_script" set-status "$repo" "$review_number" done >/dev/null
    "$issue_queue_script" set-status "$repo" "$review_parent" done >/dev/null
  else
    runoq::info "creating task issues under epic #$review_parent"
    while IFS= read -r item; do
      [[ -n "$item" ]] || continue
      local title body priority complexity rationale
      title="$(printf '%s' "$item" | jq -r '.title')"
      body="$(printf '%s' "$item" | jq -r '.body')"
      priority="$(printf '%s' "$item" | jq -r '.priority // 1')"
      complexity="$(printf '%s' "$item" | jq -r '.estimated_complexity // "medium"')"
      rationale="$(printf '%s' "$item" | jq -r '.complexity_rationale // ""')"
      create_task_issue "$issue_queue_script" "$repo" "$title" "$body" "$priority" "$complexity" "$rationale" "$review_parent" >/dev/null
      runoq::info "created task: $title ($complexity)"
    done < <(printf '%s' "$filtered_json" | jq -c '.items[]')
    runoq::info "closing review #$review_number"
    "$issue_queue_script" set-status "$repo" "$review_number" done >/dev/null
  fi
  react_to_last_human_comment "$review_view"
  runoq::success "Applied approvals from #$review_number, created issues"
  printf 'Applied approvals from #%s, created issues\n' "$review_number"
}

handle_approved_adjustment() {
  local review_view="$1" review_number="$2" review_parent="$3" selection_json="$4"
  local adjustment_json accepted_adjustments next_epic adj_count
  adjustment_json="$(adjustment_json_from_issue_view "$review_view")"
  accepted_adjustments="$(printf '%s' "$adjustment_json" | jq --argjson selection "$selection_json" '
    .proposed_adjustments |= (
      to_entries
      | map(
          . as $entry
          | select(
              ((($selection.approved | length) == 0) or (($selection.approved | index($entry.key + 1)) != null))
              and (($selection.rejected | index($entry.key + 1)) == null)
            )
        )
      | map(.value)
    )')"
  adj_count="$(printf '%s' "$accepted_adjustments" | jq '.proposed_adjustments | length')"
  runoq::detail "adjustments to apply" "$adj_count"

  while IFS= read -r adj; do
    [[ -n "$adj" ]] || continue
    local adj_type target title description
    adj_type="$(printf '%s' "$adj" | jq -r '.type')"
    target="$(printf '%s' "$adj" | jq -r '.target_milestone_number // empty')"
    title="$(printf '%s' "$adj" | jq -r '.title // empty')"
    description="$(printf '%s' "$adj" | jq -r '.description // .reason // ""')"
    if [[ "$adj_type" == "modify" && -n "$target" ]]; then
      runoq::info "modifying issue #$target"
      local target_issue target_body edit_file
      target_issue="$(printf '%s' "$issues_json" | jq -c --argjson n "$target" '.[] | select(.number == $n)')"
      target_body="$(printf '%s' "$target_issue" | jq -r '.body // ""')"
      edit_file="$(mktemp "${TMPDIR:-/tmp}/runoq-adjustment-edit.XXXXXX")"
      printf '%s\n\n%s\n' "$target_body" "$description" >"$edit_file"
      runoq::gh issue edit "$target" --repo "$repo" --body-file "$edit_file" >/dev/null
      rm -f "$edit_file"
    elif [[ "$adj_type" == "new_milestone" ]]; then
      runoq::info "creating new milestone: $title"
      "$issue_queue_script" create "$repo" "$title" "## Context\n\n$description\n\n## Acceptance Criteria\n\n- [ ] $description" --type epic --priority 99 --estimated-complexity low >/dev/null
    else
      runoq::info "applying $adj_type adjustment"
    fi
  done < <(printf '%s' "$accepted_adjustments" | jq -c '.proposed_adjustments[]?')

  runoq::info "closing review #$review_number and parent #$review_parent"
  "$issue_queue_script" set-status "$repo" "$review_number" done >/dev/null
  "$issue_queue_script" set-status "$repo" "$review_parent" done >/dev/null

  runoq::step "Refreshing issues after adjustments"
  issues_json="$(runoq::gh issue list --repo "$repo" --state all --limit 200 --json number,title,body,labels,state,url)"
  next_epic="$(first_open_epic "$issues_json")"
  if [[ -n "$next_epic" ]]; then
    local next_number
    next_number="$(printf '%s' "$next_epic" | jq -r '.number')"
    runoq::info "seeding planning issue for next epic #$next_number"
    create_planning_issue "$issue_queue_script" "$repo" "$next_number" "Break down milestone into tasks" "## Acceptance Criteria\n\n- [ ] Tasks proposed." >/dev/null
  fi
  react_to_last_human_comment "$review_view"
  runoq::success "Applied adjustments from #$review_number"
  printf 'Applied approvals from #%s, created issues\n' "$review_number"
}

handle_planning_dispatch() {
  local planning_child="$1"
  local planning_number mode milestone_file
  planning_number="$(printf '%s' "$planning_child" | jq -r '.number')"
  mode="task"
  if [[ "$current_epic_title" == "Project Planning" ]]; then
    mode="milestone"
  fi
  runoq::detail "mode" "$mode"
  runoq::detail "issue" "#$planning_number"

  milestone_file="$(mktemp "${TMPDIR:-/tmp}/runoq-milestone.XXXXXX")"
  printf '%s' "$current_epic" >"$milestone_file"
  if [[ "$mode" == "milestone" ]]; then
    "$plan_dispatch_script" "$repo" "$planning_number" milestone "$plan_file" >/dev/null
  else
    "$plan_dispatch_script" "$repo" "$planning_number" task "$plan_file" "$milestone_file" >/dev/null
  fi
  assign_issue "$issue_queue_script" "$repo" "$planning_number"
  runoq::success "Proposal posted on #$planning_number"
  printf 'Proposal posted on #%s\n' "$planning_number"
}

handle_implementation() {
  runoq::info "reconciling dispatch safety"
  "$dispatch_safety_script" reconcile "$repo" >/dev/null 2>&1 || true
  runoq::info "running dry-run dispatch"
  "$run_script" --dry-run >/dev/null 2>&1 || true
  runoq::success "Dispatched #$current_epic_number"
  printf 'Dispatched #%s\n' "$current_epic_number"
}

handle_milestone_complete() {
  local claude_bin payload reviewer_path reviewer_json adjustment_count body create_output next_epic
  claude_bin="${RUNOQ_CLAUDE_BIN:-claude}"

  runoq::step "Running milestone reviewer"
  runoq::detail "milestone" "#$current_epic_number $current_epic_title"
  runoq::detail "type" "${current_epic_type:-standard}"

  payload="$(jq -cn --arg milestonePath "$plan_file" --arg planPath "$plan_file" --arg completedTasksPath "$plan_file" --arg remainingMilestonesPath "$plan_file" '{milestonePath:$milestonePath, planPath:$planPath, completedTasksPath:$completedTasksPath, remainingMilestonesPath:$remainingMilestonesPath}')"
  runoq::captured_exec claude "$(runoq::target_root)" "$claude_bin" --agent milestone-reviewer --add-dir "$(runoq::root)" -- "$payload" >/dev/null
  reviewer_path="$RUNOQ_LAST_CLAUDE_CAPTURE_DIR/response.txt"
  if jq -e . "$reviewer_path" >/dev/null 2>&1; then
    reviewer_json="$(cat "$reviewer_path")"
  else
    reviewer_json="$(read_json_output "$reviewer_path" 'runoq:payload:milestone-reviewer')" || reviewer_json='{"proposed_adjustments":[]}'
  fi
  adjustment_count="$(printf '%s' "$reviewer_json" | jq '.proposed_adjustments | length')"
  runoq::detail "proposed adjustments" "$adjustment_count"

  if [[ "$current_epic_type" == "discovery" && "$adjustment_count" -eq 0 ]]; then
    runoq::info "discovery milestone — forcing human review pause"
    reviewer_json="$(printf '%s' "$reviewer_json" | jq '.proposed_adjustments = [{"type":"discovery","title":"Discovery review","description":"Discovery milestones always pause for human review.","reason":"discovery milestone completed"}]')"
    adjustment_count=1
  fi
  if [[ "$adjustment_count" -gt 0 ]]; then
    runoq::info "creating adjustment review issue"
    body="$(printf '%s' "$reviewer_json" | "$(runoq::root)/scripts/tick-fmt.sh" adjustment-review-body)"
    create_output="$("$issue_queue_script" create "$repo" "Review milestone adjustments" "$body" --type adjustment --priority 1 --estimated-complexity low --parent-epic "$current_epic_number")"
    local adj_issue_number
    adj_issue_number="$(printf '%s' "$create_output" | jq -r '.url | capture("(?<n>[0-9]+)$").n')"
    assign_issue "$issue_queue_script" "$repo" "$adj_issue_number"
    runoq::success "Milestone #$current_epic_number reviewed. Adjustments on #$adj_issue_number"
    printf 'Milestone #%s review complete. Adjustments proposed on #%s\n' "$current_epic_number" "$adj_issue_number"
    return 0
  fi

  runoq::info "no adjustments — closing milestone #$current_epic_number"
  "$issue_queue_script" set-status "$repo" "$current_epic_number" done >/dev/null

  runoq::step "Finding next epic"
  issues_json="$(runoq::gh issue list --repo "$repo" --state all --limit 200 --json number,title,body,labels,state,url)"
  next_epic="$(first_open_epic "$issues_json")"
  if [[ -n "$next_epic" ]]; then
    local next_number
    next_number="$(printf '%s' "$next_epic" | jq -r '.number')"
    runoq::info "seeding planning issue for epic #$next_number"
    create_planning_issue "$issue_queue_script" "$repo" "$next_number" "Break down milestone into tasks" "## Acceptance Criteria\n\n- [ ] Tasks proposed." >/dev/null
  else
    runoq::info "no more epics"
  fi
  runoq::success "Milestone #$current_epic_number complete"
  printf 'Milestone #%s complete. Planning for next milestone\n' "$current_epic_number"
}

main() {
  if [[ $# -gt 0 && ( "$1" == "-h" || "$1" == "--help" || "$1" == "help" ) ]]; then
    usage
    exit 0
  fi
  [[ $# -eq 0 ]] || { usage >&2; exit 1; }

  local plan_file repo issue_queue_script plan_dispatch_script comment_handler_script run_script dispatch_safety_script
  local plan_approved_label issues_json current_epic current_epic_number current_epic_title current_epic_type
  plan_file="$(runoq::plan_file)"
  repo="$(runoq::repo)"
  issue_queue_script="${RUNOQ_TICK_ISSUE_QUEUE_SCRIPT:-$(runoq::root)/scripts/gh-issue-queue.sh}"
  plan_dispatch_script="${RUNOQ_TICK_PLAN_DISPATCH_SCRIPT:-$(runoq::root)/scripts/plan-dispatch.sh}"
  comment_handler_script="${RUNOQ_TICK_COMMENT_HANDLER_SCRIPT:-$(runoq::root)/scripts/plan-comment-handler.sh}"
  run_script="${RUNOQ_TICK_RUN_SCRIPT:-$(runoq::root)/scripts/run.sh}"
  dispatch_safety_script="${RUNOQ_TICK_DISPATCH_SAFETY_SCRIPT:-$(runoq::root)/scripts/dispatch-safety.sh}"
  plan_approved_label="$(runoq::config_get '.labels.planApproved')"

  runoq::step "Starting tick"
  runoq::detail "repo" "$repo"
  runoq::detail "plan" "$plan_file"

  runoq::step "Fetching issues"
  issues_json="$(runoq::gh issue list --repo "$repo" --state all --limit 200 --json number,title,body,labels,state,url)"
  local issue_count
  issue_count="$(printf '%s' "$issues_json" | jq 'length')"
  runoq::info "found $issue_count issues"

  runoq::step "Finding current epic"
  current_epic="$(first_open_epic "$issues_json")"
  if [[ -z "$current_epic" ]]; then
    if ! any_epic_exists "$issues_json"; then
      runoq::info "no epics exist — bootstrapping project"
      handle_bootstrap
    else
      runoq::success "Project complete"
      printf 'Project complete\n'
    fi
    exit 0
  fi
  runoq::detail "epic" "#$(printf '%s' "$current_epic" | jq -r '.number') $(printf '%s' "$current_epic" | jq -r '.title')"

  runoq::step "Checking for pending review"
  local pending_review
  pending_review="$(find_review_issue "$issues_json" pending "$plan_approved_label" || true)"
  if [[ -n "$pending_review" ]]; then
    runoq::info "found pending review #$(printf '%s' "$pending_review" | jq -r '.number')"
    if handle_pending_review "$pending_review"; then
      exit 0
    fi
    runoq::info "pending review not actionable, continuing"
  else
    runoq::info "none"
  fi

  runoq::step "Checking for approved review"
  local approved_review
  approved_review="$(find_review_issue "$issues_json" approved "$plan_approved_label" || true)"
  if [[ -n "$approved_review" ]]; then
    local review_number review_body review_type review_parent review_view selection_json
    review_number="$(printf '%s' "$approved_review" | jq -r '.number')"
    review_body="$(printf '%s' "$approved_review" | jq -r '.body // ""')"
    review_type="$(issue_type "$review_body")"
    review_parent="$(issue_parent_epic "$review_body")"
    runoq::info "found approved $review_type review #$review_number (parent #$review_parent)"

    runoq::step "Loading review details for #$review_number"
    review_view="$(runoq::gh issue view "$review_number" --repo "$repo" --json number,title,body,comments,labels,state)"
    selection_json="$(human_comment_selection "$review_view")"
    local approved_items rejected_items
    approved_items="$(printf '%s' "$selection_json" | jq '.approved | length')"
    rejected_items="$(printf '%s' "$selection_json" | jq '.rejected | length')"
    if (( approved_items > 0 || rejected_items > 0 )); then
      runoq::detail "approved items" "$approved_items"
      runoq::detail "rejected items" "$rejected_items"
    fi

    if [[ "$review_type" == "planning" ]]; then
      runoq::step "Applying approved planning from #$review_number"
      handle_approved_planning "$review_view" "$review_number" "$review_parent" "$selection_json"
      exit 0
    elif [[ "$review_type" == "adjustment" ]]; then
      runoq::step "Applying approved adjustments from #$review_number"
      handle_approved_adjustment "$review_view" "$review_number" "$review_parent" "$selection_json"
      exit 0
    fi
  else
    runoq::info "none"
  fi

  current_epic_number="$(printf '%s' "$current_epic" | jq -r '.number')"
  current_epic_title="$(printf '%s' "$current_epic" | jq -r '.title')"
  current_epic_type="$(issue_milestone_type "$(printf '%s' "$current_epic" | jq -r '.body // ""')")"

  runoq::step "Scanning children of epic #$current_epic_number"
  local planning_child=""
  while IFS= read -r child; do
    [[ -n "$child" ]] || continue
    if [[ "$(printf '%s' "$child" | jq -r '.state')" == "OPEN" && "$(issue_type "$(printf '%s' "$child" | jq -r '.body // ""')")" == "planning" ]]; then
      planning_child="$child"
      break
    fi
  done < <(children_for_epic "$issues_json" "$current_epic_number")
  if [[ -n "$planning_child" ]]; then
    local planning_child_number
    planning_child_number="$(printf '%s' "$planning_child" | jq -r '.number')"
    runoq::info "found planning issue #$planning_child_number"
    if planning_issue_needs_dispatch "$planning_child" "$repo"; then
      runoq::step "Dispatching plan decomposition for #$planning_child_number"
      handle_planning_dispatch "$planning_child"
      exit 0
    fi
    runoq::info "planning issue #$planning_child_number already has a proposal"
  fi

  local open_children=0 has_open_task=0
  while IFS= read -r child; do
    [[ -n "$child" ]] || continue
    if [[ "$(printf '%s' "$child" | jq -r '.state')" == "OPEN" ]]; then
      open_children=$((open_children + 1))
      if [[ "$(issue_type "$(printf '%s' "$child" | jq -r '.body // ""')")" == "task" ]]; then
        has_open_task=1
      fi
    fi
  done < <(children_for_epic "$issues_json" "$current_epic_number")
  runoq::detail "open children" "$open_children"
  runoq::detail "has open tasks" "$has_open_task"

  if (( has_open_task == 1 )); then
    runoq::step "Dispatching implementation for epic #$current_epic_number"
    handle_implementation
    exit 0
  fi

  if (( open_children == 0 )); then
    runoq::step "All tasks complete — reviewing milestone #$current_epic_number"
    handle_milestone_complete
    exit 0
  fi

  runoq::warn "$open_children tasks in progress, none ready"
  printf '%s tasks in progress, none ready\n' "$open_children"
}

main "$@"
