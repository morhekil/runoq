#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"
source "$(cd "$(dirname "$0")" && pwd)/lib/planning.sh"

usage() {
  cat <<'EOF'
Usage:
  plan-dispatch.sh <repo> <issue-number> <milestone|task> <plan-file> [milestone-file] [prior-findings-file]
EOF
}

require_file() {
  local path="$1"
  [[ -f "$path" ]] || runoq::die "Required file not found: $path"
}

call_agent() {
  local agent="$1"
  local payload="$2"
  shift 2 || true
  local claude_bin="${RUNOQ_CLAUDE_BIN:-claude}"
  local attempt response_path
  local -a args
  args=(--agent "$agent" --add-dir "$(runoq::root)")
  while [[ $# -gt 0 ]]; do
    if [[ -n "${1:-}" ]]; then
      args+=(--add-dir "$1")
    fi
    shift || true
  done
  args+=(-- "$payload")
  for attempt in 1 2; do
    runoq::captured_exec claude "$(runoq::target_root)" "$claude_bin" "${args[@]}" >/dev/null
    response_path="$RUNOQ_LAST_CLAUDE_CAPTURE_DIR/response.txt"
    if grep -q '[^[:space:]]' "$response_path"; then
      printf '%s\n' "$response_path"
      return 0
    fi
  done
  printf '%s\n' "$response_path"
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

proposal_comment_body() {
  local proposal_path="$1"
  local technical_json="$2"
  local product_json="$3"
  local warning="${4:-}"

  {
    printf '## Review scores\n\n'
    printf '| Reviewer | Score | Verdict |\n'
    printf '|----------|-------|---------|\n'
    printf '| Technical | %s | %s |\n' \
      "$(printf '%s' "$technical_json" | jq -r '.score')" \
      "$(printf '%s' "$technical_json" | jq -r '.verdict')"
    printf '| Product | %s | %s |\n\n' \
      "$(printf '%s' "$product_json" | jq -r '.score')" \
      "$(printf '%s' "$product_json" | jq -r '.verdict')"
    if [[ -n "$warning" ]]; then
      printf '> **Warning:** %s\n\n' "$warning"
    fi
    local warning_lines
    warning_lines="$(jq -r '(.warnings // [])[] // empty' "$proposal_path" 2>/dev/null)"
    if [[ -n "$warning_lines" ]]; then
      printf '**Warnings from decomposer:**\n'
      while IFS= read -r line; do
        [[ -n "$line" ]] && printf '- %s\n' "$line"
      done <<< "$warning_lines"
      printf '\n'
    fi
    printf '## Proposed milestones\n\n'
    runoq::format_plan_proposal "$proposal_path"
    printf '\n<details>\n<summary>Raw JSON payload</summary>\n\n'
    printf '```json\n'
    cat "$proposal_path"
    printf '\n```\n\n</details>\n'
  }
}

main() {
  [[ $# -ge 4 ]] || { usage >&2; exit 1; }
  local repo="$1" issue_number="$2" review_type="$3" plan_file="$4"
  local milestone_file="${5:-}"
  local prior_findings_file="${6:-}"
  shift 4 || true

  require_file "$plan_file"
  if [[ "$review_type" == "task" ]]; then
    require_file "$milestone_file"
  fi

  local max_rounds
  max_rounds="$(runoq::config_get '.planning.maxDecompositionRounds // 3')"

  runoq::step "Plan dispatch: $review_type decomposition for #$issue_number"
  runoq::detail "plan" "$plan_file"
  runoq::detail "max rounds" "$max_rounds"
  [[ -z "$milestone_file" ]] || runoq::detail "milestone" "$milestone_file"
  local plan_dir milestone_dir prior_findings_dir
  plan_dir="$(dirname "$plan_file")"
  milestone_dir=""
  prior_findings_dir=""
  if [[ -n "$milestone_file" ]]; then
    milestone_dir="$(dirname "$milestone_file")"
  fi
  if [[ -n "$prior_findings_file" ]]; then
    prior_findings_dir="$(dirname "$prior_findings_file")"
  fi

  local round=1
  local merged_checklist=""
  local proposal_json="" technical_json="" product_json="" warning=""
  local template_path
  template_path="$(runoq::root)/templates/issue-template.md"

  while (( round <= max_rounds )); do
    local decomposer payload response_path marker proposal_tmp
    decomposer="milestone-decomposer"
    marker='runoq:payload:milestone-decomposer'
    if [[ "$review_type" == "task" ]]; then
      decomposer="task-decomposer"
      marker='runoq:payload:task-decomposer'
    fi

    runoq::step "Decomposition round $round/$max_rounds"
    runoq::detail "agent" "$decomposer"

    local feedback_payload="$merged_checklist"
    if [[ -n "$feedback_payload" ]]; then
      runoq::info "feeding back reviewer checklist from previous round"
      feedback_payload=$'CHECKLIST:\n'"$feedback_payload"
    fi

    payload="$(
      jq -cn \
        --arg planPath "$plan_file" \
        --arg templatePath "$template_path" \
        --arg milestonePath "$milestone_file" \
        --arg priorFindingsPath "$prior_findings_file" \
        --arg reviewType "$review_type" \
        --arg feedbackChecklist "$feedback_payload" '
        {
          planPath: $planPath,
          templatePath: $templatePath,
          reviewType: $reviewType,
          feedbackChecklist: $feedbackChecklist
        }
        + (if $milestonePath == "" then {} else {milestonePath: $milestonePath} end)
        + (if $priorFindingsPath == "" then {} else {priorFindingsPath: $priorFindingsPath} end)
      '
    )"

    runoq::info "calling $decomposer agent"
    response_path="$(call_agent "$decomposer" "$payload" "$plan_dir" "$milestone_dir" "$prior_findings_dir")"
    proposal_json="$(read_json_output "$response_path" "$marker")" ||
      runoq::die "Invalid ${decomposer} output"
    local item_count
    item_count="$(printf '%s' "$proposal_json" | jq '.items | length')"
    runoq::detail "items proposed" "$item_count"
    proposal_tmp="$(mktemp "${TMPDIR:-/tmp}/runoq-plan-proposal.XXXXXX")"
    printf '%s\n' "$proposal_json" >"$proposal_tmp"

    runoq::step "Reviewing proposal (round $round)"
    local review_payload tech_path product_path
    review_payload="$(jq -cn --arg proposalPath "$proposal_tmp" --arg planPath "$plan_file" --arg reviewType "$review_type" '{proposalPath:$proposalPath, planPath:$planPath, reviewType:$reviewType}')"
    runoq::info "calling plan-reviewer-technical"
    tech_path="$(call_agent plan-reviewer-technical "$review_payload" "$plan_dir" "$(dirname "$proposal_tmp")")"
    runoq::info "calling plan-reviewer-product"
    product_path="$(call_agent plan-reviewer-product "$review_payload" "$plan_dir" "$(dirname "$proposal_tmp")")"
    technical_json="$(runoq::parse_verdict_block "$tech_path")"
    product_json="$(runoq::parse_verdict_block "$product_path")"

    local tech_verdict product_verdict tech_score product_score
    tech_verdict="$(printf '%s' "$technical_json" | jq -r '.verdict')"
    product_verdict="$(printf '%s' "$product_json" | jq -r '.verdict')"
    tech_score="$(printf '%s' "$technical_json" | jq -r '.score')"
    product_score="$(printf '%s' "$product_json" | jq -r '.score')"
    runoq::detail "technical" "$tech_verdict ($tech_score)"
    runoq::detail "product" "$product_verdict ($product_score)"

    if [[ "$tech_verdict" == "PASS" && "$product_verdict" == "PASS" ]]; then
      runoq::success "Both reviewers passed"
      warning=""
      break
    fi

    merged_checklist="$(runoq::merge_checklists "$(printf '%s' "$technical_json" | jq -r '.checklist')" "$(printf '%s' "$product_json" | jq -r '.checklist')")"
    if (( round == max_rounds )); then
      runoq::warn "Max review rounds reached — proceeding with current proposal"
      warning="max review rounds reached"
      break
    fi
    runoq::info "reviewers requested changes, iterating"
    round=$((round + 1))
  done

  runoq::step "Posting proposal comment on #$issue_number"
  local body_file
  body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-plan-comment.XXXXXX")"
  proposal_tmp="$(mktemp "${TMPDIR:-/tmp}/runoq-plan-proposal-final.XXXXXX")"
  printf '%s\n' "$proposal_json" >"$proposal_tmp"
  proposal_comment_body "$proposal_tmp" "$technical_json" "$product_json" "$warning" >"$body_file"

  runoq::gh issue comment "$issue_number" --repo "$repo" --body-file "$body_file" >/dev/null
  runoq::success "Proposal posted on #$issue_number"
  printf 'Proposal posted on #%s\n' "$issue_number"
}

main "$@"
