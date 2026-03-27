#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

SCRIPTS_DIR="$(cd "$(dirname "$0")" && pwd)"
RUNOQ_ROOT="${RUNOQ_ROOT:-$(runoq::root)}"

###############################################################################
# Usage
###############################################################################

usage() {
  cat <<'EOF'
Usage:
  orchestrator.sh run <repo> [--issue N] [--dry-run]
  orchestrator.sh mention-triage <repo> <pr-number>
EOF
}

###############################################################################
# Claude CLI helper
###############################################################################

claude_exec() {
  local claude_bin="${RUNOQ_CLAUDE_BIN:-claude}"
  command -v "$claude_bin" >/dev/null 2>&1 || runoq::die "Claude CLI not found: $claude_bin"
  (
    cd "$(runoq::target_root)"
    "$claude_bin" "$@"
  )
}

###############################################################################
# Logging
###############################################################################

log_info() {
  printf '[orchestrator] %s\n' "$*" >&2
}

log_error() {
  printf '[orchestrator] ERROR: %s\n' "$*" >&2
}

###############################################################################
# Config helpers
###############################################################################

config_max_rounds() {
  runoq::config_get '.maxRounds'
}

config_max_token_budget() {
  runoq::config_get '.maxTokenBudget'
}

config_consecutive_failure_limit() {
  runoq::config_get '.consecutiveFailureLimit'
}

config_ready_label() {
  runoq::config_get '.labels.ready'
}

config_reviewer() {
  runoq::config_get '.reviewers[0]' 2>/dev/null || printf ''
}

config_auto_merge_enabled() {
  runoq::config_get '.autoMerge.enabled' 2>/dev/null || printf 'false'
}

config_auto_merge_max_complexity() {
  runoq::config_get '.autoMerge.maxComplexity' 2>/dev/null || printf 'low'
}

###############################################################################
# State helpers
###############################################################################

save_state() {
  local issue="$1"
  shift
  printf '%s' "$1" | "$SCRIPTS_DIR/state.sh" save "$issue"
}

load_state() {
  local issue="$1"
  "$SCRIPTS_DIR/state.sh" load "$issue" 2>/dev/null || printf ''
}

state_file_exists() {
  local issue="$1"
  local state_dir
  state_dir="$(runoq::state_dir)"
  [[ -f "${state_dir}/${issue}.json" ]]
}

###############################################################################
# Audit trail helpers
###############################################################################

post_audit_comment() {
  local repo="$1"
  local pr_number="$2"
  local event="$3"
  local body="$4"
  local comment_file
  comment_file="$(mktemp "${TMPDIR:-/tmp}/runoq-audit.XXXXXX")"
  printf '<!-- runoq:event:%s -->\n%s\n' "$event" "$body" >"$comment_file"
  "$SCRIPTS_DIR/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$comment_file" >/dev/null 2>&1 || true
  rm -f "$comment_file"
}

post_issue_comment() {
  local repo="$1"
  local issue_number="$2"
  local body="$3"
  runoq::gh issue comment "$issue_number" --repo "$repo" --body "$body" >/dev/null 2>&1 || true
}

###############################################################################
# Extract marked block from agent output
###############################################################################

extract_marked_block() {
  local source_file="$1"
  local marker="$2"
  awk -v marker="$marker" '
    $0 ~ marker {
      saw_marker = 1
      next
    }
    saw_marker && /^```/ {
      if (!in_block) {
        in_block = 1
        block = ""
        next
      }
      printf "%s", block
      exit
    }
    saw_marker && in_block {
      block = block $0 ORS
    }
  ' "$source_file"
}

###############################################################################
# Parse review verdict from reviewLogPath
###############################################################################

parse_review_verdict() {
  local review_log="$1"
  local verdict score checklist review_type

  review_type="$(grep -m1 '^REVIEW-TYPE:' "$review_log" | sed 's/^REVIEW-TYPE:[[:space:]]*//' || printf '')"
  verdict="$(grep -m1 '^VERDICT:' "$review_log" | sed 's/^VERDICT:[[:space:]]*//' || printf '')"
  score="$(grep -m1 '^SCORE:' "$review_log" | sed 's/^SCORE:[[:space:]]*//' || printf '')"
  checklist="$(awk '/^CHECKLIST:/{found=1; next} found{print}' "$review_log" || printf '')"

  jq -n \
    --arg review_type "$review_type" \
    --arg verdict "$verdict" \
    --arg score "$score" \
    --arg checklist "$checklist" '{
    review_type: $review_type,
    verdict: $verdict,
    score: $score,
    checklist: $checklist
  }'
}

###############################################################################
# Get issue metadata (complexity, type, etc.)
###############################################################################

get_issue_metadata() {
  local repo="$1"
  local issue_number="$2"
  local issue_json body_file metadata

  issue_json="$(runoq::gh issue view "$issue_number" --repo "$repo" --json number,title,body,labels,url)"
  body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-meta.XXXXXX.md")"
  printf '%s' "$issue_json" | jq -r '.body // ""' >"$body_file"

  metadata="$("$SCRIPTS_DIR/gh-issue-queue.sh" list "$repo" "$(config_ready_label)" 2>/dev/null | jq --argjson n "$issue_number" '.[] | select(.number == $n)' 2>/dev/null || printf '{}')"

  if [[ -z "$metadata" || "$metadata" == "{}" ]]; then
    # Parse from body directly
    local block complexity type_val
    block="$(awk '/<!-- runoq:meta/{in_block=1;next} in_block && /-->/{exit} in_block{print}' "$body_file")"
    complexity="$(printf '%s\n' "$block" | sed -n 's/^estimated_complexity:[[:space:]]*//p' | head -n1)"
    type_val="$(printf '%s\n' "$block" | sed -n 's/^type:[[:space:]]*//p' | head -n1)"
    metadata="$(jq -n \
      --argjson issue "$issue_json" \
      --arg complexity "${complexity:-medium}" \
      --arg type_val "${type_val:-task}" '{
      number: $issue.number,
      title: $issue.title,
      body: $issue.body,
      url: $issue.url,
      estimated_complexity: $complexity,
      type: $type_val
    }')"
  fi

  rm -f "$body_file"
  printf '%s\n' "$metadata"
}

###############################################################################
# PHASE: INIT
###############################################################################

phase_init() {
  local repo="$1"
  local issue_number="$2"
  local dry_run="$3"
  local title="$4"
  local eligibility_json worktree_json branch worktree pr_json pr_number

  log_info "INIT: issue #${issue_number}"

  # 1. Eligibility check
  if ! eligibility_json="$("$SCRIPTS_DIR/dispatch-safety.sh" eligibility "$repo" "$issue_number")"; then
    log_error "Issue #${issue_number} is not eligible"
    return 1
  fi

  branch="$(printf '%s' "$eligibility_json" | jq -r '.branch')"

  if [[ "$dry_run" == "true" ]]; then
    log_info "DRY-RUN: would create worktree, branch ${branch}, draft PR for issue #${issue_number}"
    jq -n --argjson issue "$issue_number" --arg branch "$branch" '{phase:"INIT", dry_run:true, issue:$issue, branch:$branch}'
    return 0
  fi

  # 2. Set issue in-progress
  "$SCRIPTS_DIR/gh-issue-queue.sh" set-status "$repo" "$issue_number" "in-progress" >/dev/null

  # 3. Create worktree
  worktree_json="$("$SCRIPTS_DIR/worktree.sh" create "$issue_number" "$title")"
  worktree="$(printf '%s' "$worktree_json" | jq -r '.worktree')"
  branch="$(printf '%s' "$worktree_json" | jq -r '.branch')"

  # 4. Create initial empty commit and push
  git -C "$worktree" commit --allow-empty -m "runoq: begin work on #${issue_number}" >/dev/null 2>&1
  git -C "$worktree" push -u origin "$branch" >/dev/null 2>&1

  # 5. Create draft PR
  pr_json="$("$SCRIPTS_DIR/gh-pr-lifecycle.sh" create "$repo" "$branch" "$issue_number" "$title")"
  pr_number="$(printf '%s' "$pr_json" | jq -r '.number')"

  # 6. Save state
  local state
  state="$(jq -n \
    --argjson issue "$issue_number" \
    --arg phase "INIT" \
    --arg branch "$branch" \
    --arg worktree "$worktree" \
    --argjson pr_number "$pr_number" \
    --argjson round 0 \
    --argjson cumulative_tokens 0 \
    --argjson consecutive_failures 0 '{
    issue: $issue,
    phase: $phase,
    branch: $branch,
    worktree: $worktree,
    pr_number: $pr_number,
    round: $round,
    cumulative_tokens: $cumulative_tokens,
    consecutive_failures: $consecutive_failures
  }')"
  save_state "$issue_number" "$state" >/dev/null

  post_audit_comment "$repo" "$pr_number" "init" "Orchestrator initialized. Branch: \`${branch}\`"

  printf '%s\n' "$state"
}

###############################################################################
# PHASE: CRITERIA
###############################################################################

phase_criteria() {
  local repo="$1"
  local issue_number="$2"
  local state_json="$3"
  local complexity worktree branch pr_number

  complexity="$(printf '%s' "$4" | jq -r '.estimated_complexity // "medium"')"
  worktree="$(printf '%s' "$state_json" | jq -r '.worktree')"
  branch="$(printf '%s' "$state_json" | jq -r '.branch')"
  pr_number="$(printf '%s' "$state_json" | jq -r '.pr_number')"

  # Skip criteria for low complexity
  if [[ "$complexity" == "low" ]]; then
    log_info "CRITERIA: skipped (low complexity)"
    local state
    state="$(printf '%s' "$state_json" | jq '.phase = "CRITERIA"')"
    save_state "$issue_number" "$state" >/dev/null
    printf '%s\n' "$state"
    return 0
  fi

  log_info "CRITERIA: spawning bar-setter for issue #${issue_number}"

  local spec_file payload bar_setter_output output_file criteria_commit

  # Write issue body to spec file
  spec_file="$(mktemp "${TMPDIR:-/tmp}/runoq-spec.XXXXXX.md")"
  runoq::gh issue view "$issue_number" --repo "$repo" --json body | jq -r '.body // ""' >"$spec_file"

  payload="$(jq -n \
    --argjson issueNumber "$issue_number" \
    --arg worktree "$worktree" \
    --arg branch "$branch" \
    --arg specPath "$spec_file" \
    --arg repo "$repo" '{
    issueNumber: $issueNumber,
    worktree: $worktree,
    branch: $branch,
    specPath: $specPath,
    repo: $repo
  }')"

  output_file="$(mktemp "${TMPDIR:-/tmp}/runoq-barsetter-out.XXXXXX")"
  claude_exec --print --permission-mode bypassPermissions --agent bar-setter --add-dir "$RUNOQ_ROOT" -- "$payload" >"$output_file" 2>&1 || true

  # Extract criteria_commit from bar-setter output
  criteria_commit="$(extract_marked_block "$output_file" 'runoq:payload:bar-setter' | jq -r '.criteria_commit // empty' 2>/dev/null || printf '')"

  if [[ -z "$criteria_commit" ]]; then
    # Try to get the latest commit on the branch as criteria commit
    criteria_commit="$(git -C "$worktree" rev-parse HEAD 2>/dev/null || printf '')"
  fi

  # Post criteria summary as PR comment
  local summary_file
  summary_file="$(mktemp "${TMPDIR:-/tmp}/runoq-criteria-summary.XXXXXX")"
  printf '<!-- runoq:event:criteria -->\n## Acceptance Criteria Set\n\nCriteria commit: `%s`\nComplexity: %s\n' "$criteria_commit" "$complexity" >"$summary_file"
  "$SCRIPTS_DIR/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$summary_file" >/dev/null 2>&1 || true
  rm -f "$summary_file"

  # Save state
  local state
  state="$(printf '%s' "$state_json" | jq \
    --arg phase "CRITERIA" \
    --arg criteria_commit "$criteria_commit" '.phase = $phase | .criteria_commit = $criteria_commit')"
  save_state "$issue_number" "$state" >/dev/null

  rm -f "$spec_file" "$output_file"
  printf '%s\n' "$state"
}

###############################################################################
# PHASE: DEVELOP
###############################################################################

phase_develop() {
  local repo="$1"
  local issue_number="$2"
  local state_json="$3"
  local metadata_json="$4"
  local checklist="${5:-}"
  local worktree branch pr_number round cumulative_tokens log_dir
  local max_rounds max_token_budget

  worktree="$(printf '%s' "$state_json" | jq -r '.worktree')"
  branch="$(printf '%s' "$state_json" | jq -r '.branch')"
  pr_number="$(printf '%s' "$state_json" | jq -r '.pr_number')"
  round="$(printf '%s' "$state_json" | jq -r '.round // 0')"
  cumulative_tokens="$(printf '%s' "$state_json" | jq -r '.cumulative_tokens // 0')"
  log_dir="$(printf '%s' "$state_json" | jq -r '.log_dir // empty')"
  max_rounds="$(config_max_rounds)"
  max_token_budget="$(config_max_token_budget)"

  round=$((round + 1))
  log_info "DEVELOP: round ${round}/${max_rounds} for issue #${issue_number}"

  # Write issue body to spec file
  local spec_file
  spec_file="$(mktemp "${TMPDIR:-/tmp}/runoq-spec.XXXXXX.md")"
  runoq::gh issue view "$issue_number" --repo "$repo" --json body | jq -r '.body // ""' >"$spec_file"

  # Build issue-runner payload
  local payload
  payload="$(jq -n \
    --argjson issueNumber "$issue_number" \
    --argjson prNumber "$pr_number" \
    --arg worktree "$worktree" \
    --arg branch "$branch" \
    --arg specPath "$spec_file" \
    --arg repo "$repo" \
    --argjson maxRounds "$max_rounds" \
    --argjson maxTokenBudget "$max_token_budget" \
    --argjson round "$round" \
    --arg logDir "${log_dir:-}" \
    --arg previousChecklist "$checklist" \
    --argjson cumulativeTokens "$cumulative_tokens" \
    --arg guidelines "" '{
    issueNumber: $issueNumber,
    prNumber: $prNumber,
    worktree: $worktree,
    branch: $branch,
    specPath: $specPath,
    repo: $repo,
    maxRounds: $maxRounds,
    maxTokenBudget: $maxTokenBudget,
    round: $round,
    guidelines: $guidelines
  } + (if $logDir != "" then {logDir: $logDir} else {} end)
    + (if $previousChecklist != "" then {previousChecklist: $previousChecklist} else {} end)
    + (if $cumulativeTokens > 0 then {cumulativeTokens: $cumulativeTokens} else {} end)')"

  # Invoke issue-runner
  local output_file runner_result
  output_file="$(mktemp "${TMPDIR:-/tmp}/runoq-runner-out.XXXXXX")"

  if [[ -x "$SCRIPTS_DIR/issue-runner.sh" ]]; then
    "$SCRIPTS_DIR/issue-runner.sh" "$payload" >"$output_file" 2>&1 || true
  else
    claude_exec --print --permission-mode bypassPermissions --agent issue-runner --add-dir "$RUNOQ_ROOT" -- "$payload" >"$output_file" 2>&1 || true
  fi

  # Parse issue-runner return payload
  runner_result="$("$SCRIPTS_DIR/state.sh" extract-payload "$output_file" 2>/dev/null || printf '')"
  if [[ -z "$runner_result" ]]; then
    # Try extracting the last JSON block
    runner_result="$(extract_marked_block "$output_file" 'runoq:payload:issue-runner' 2>/dev/null || printf '')"
  fi

  local status runner_log_dir runner_worktree runner_branch
  local baseline_hash head_hash commit_range review_log_path
  local spec_requirements changed_files related_files runner_cumulative_tokens
  local verification_passed caveats summary

  if [[ -n "$runner_result" ]] && printf '%s' "$runner_result" | jq -e '.' >/dev/null 2>&1; then
    status="$(printf '%s' "$runner_result" | jq -r '.status // "fail"')"
    runner_log_dir="$(printf '%s' "$runner_result" | jq -r '.logDir // empty')"
    baseline_hash="$(printf '%s' "$runner_result" | jq -r '.baselineHash // empty')"
    head_hash="$(printf '%s' "$runner_result" | jq -r '.headHash // empty')"
    commit_range="$(printf '%s' "$runner_result" | jq -r '.commitRange // empty')"
    review_log_path="$(printf '%s' "$runner_result" | jq -r '.reviewLogPath // empty')"
    spec_requirements="$(printf '%s' "$runner_result" | jq -r '.specRequirements // empty')"
    changed_files="$(printf '%s' "$runner_result" | jq -r '.changedFiles // empty')"
    related_files="$(printf '%s' "$runner_result" | jq -r '.relatedFiles // empty')"
    runner_cumulative_tokens="$(printf '%s' "$runner_result" | jq -r '.cumulativeTokens // 0')"
    verification_passed="$(printf '%s' "$runner_result" | jq -r '.verificationPassed // false')"
    caveats="$(printf '%s' "$runner_result" | jq -r '.caveats // empty')"
    summary="$(printf '%s' "$runner_result" | jq -r '.summary // empty')"
  else
    status="fail"
    runner_cumulative_tokens="0"
    verification_passed="false"
    caveats="issue-runner did not return a parseable payload"
    summary=""
    log_error "Failed to parse issue-runner output"
  fi

  # Save state
  local state
  state="$(printf '%s' "$state_json" | jq \
    --arg phase "DEVELOP" \
    --argjson round "$round" \
    --arg status "$status" \
    --arg log_dir "${runner_log_dir:-$log_dir}" \
    --arg baseline_hash "${baseline_hash:-}" \
    --arg head_hash "${head_hash:-}" \
    --arg commit_range "${commit_range:-}" \
    --arg review_log_path "${review_log_path:-}" \
    --arg spec_requirements "${spec_requirements:-}" \
    --arg changed_files "${changed_files:-}" \
    --arg related_files "${related_files:-}" \
    --argjson cumulative_tokens "${runner_cumulative_tokens:-0}" \
    --arg verification_passed "${verification_passed:-false}" \
    --arg caveats "${caveats:-}" \
    --arg summary "${summary:-}" '
    .phase = $phase
    | .round = $round
    | .status = $status
    | .log_dir = $log_dir
    | .baseline_hash = $baseline_hash
    | .head_hash = $head_hash
    | .commit_range = $commit_range
    | .review_log_path = $review_log_path
    | .spec_requirements = $spec_requirements
    | .changed_files = $changed_files
    | .related_files = $related_files
    | .cumulative_tokens = ($cumulative_tokens | tonumber)
    | .verification_passed = ($verification_passed == "true")
    | .caveats = $caveats
    | .summary = $summary
  ')"
  save_state "$issue_number" "$state" >/dev/null

  rm -f "$spec_file" "$output_file"
  printf '%s\n' "$state"
}

###############################################################################
# PHASE: REVIEW
###############################################################################

phase_review() {
  local repo="$1"
  local issue_number="$2"
  local state_json="$3"
  local worktree branch pr_number round baseline_hash head_hash
  local review_log_path spec_requirements changed_files related_files checklist

  worktree="$(printf '%s' "$state_json" | jq -r '.worktree')"
  branch="$(printf '%s' "$state_json" | jq -r '.branch')"
  pr_number="$(printf '%s' "$state_json" | jq -r '.pr_number')"
  round="$(printf '%s' "$state_json" | jq -r '.round')"
  baseline_hash="$(printf '%s' "$state_json" | jq -r '.baseline_hash // empty')"
  head_hash="$(printf '%s' "$state_json" | jq -r '.head_hash // empty')"
  review_log_path="$(printf '%s' "$state_json" | jq -r '.review_log_path // empty')"
  spec_requirements="$(printf '%s' "$state_json" | jq -r '.spec_requirements // empty')"
  changed_files="$(printf '%s' "$state_json" | jq -r '.changed_files // empty')"
  related_files="$(printf '%s' "$state_json" | jq -r '.related_files // empty')"
  checklist="$(printf '%s' "$state_json" | jq -r '.previous_checklist // empty')"

  log_info "REVIEW: spawning diff-reviewer for issue #${issue_number} round ${round}"

  # Build review payload
  local review_payload
  review_payload="$(jq -n \
    --argjson issueNumber "$issue_number" \
    --argjson round "$round" \
    --arg worktree "$worktree" \
    --arg baselineHash "$baseline_hash" \
    --arg headHash "$head_hash" \
    --arg reviewLogPath "$review_log_path" \
    --arg specRequirements "$spec_requirements" \
    --arg guidelines "" \
    --arg changedFiles "$changed_files" \
    --arg relatedFiles "$related_files" \
    --arg previousChecklist "$checklist" '{
    issueNumber: $issueNumber,
    round: $round,
    worktree: $worktree,
    baselineHash: $baselineHash,
    headHash: $headHash,
    reviewLogPath: $reviewLogPath,
    specRequirements: $specRequirements,
    guidelines: $guidelines,
    changedFiles: $changedFiles,
    relatedFiles: $relatedFiles,
    previousChecklist: $previousChecklist
  }')"

  local review_output_file
  review_output_file="$(mktemp "${TMPDIR:-/tmp}/runoq-review-out.XXXXXX")"
  claude_exec --print --permission-mode bypassPermissions --agent diff-reviewer --add-dir "$RUNOQ_ROOT" -- "$review_payload" >"$review_output_file" 2>&1 || true

  # Parse verdict from reviewLogPath
  local verdict_json verdict score review_checklist
  if [[ -n "$review_log_path" && -f "$review_log_path" ]]; then
    verdict_json="$(parse_review_verdict "$review_log_path")"
  else
    # Try to parse from the output file itself
    verdict_json="$(parse_review_verdict "$review_output_file" 2>/dev/null || jq -n '{verdict:"FAIL",score:"0",checklist:"",review_type:""}')"
  fi

  verdict="$(printf '%s' "$verdict_json" | jq -r '.verdict // "FAIL"')"
  score="$(printf '%s' "$verdict_json" | jq -r '.score // "0"')"
  review_checklist="$(printf '%s' "$verdict_json" | jq -r '.checklist // ""')"

  if [[ -z "$verdict" || "$verdict" == "null" ]]; then
    verdict="FAIL"
    log_error "Could not parse review verdict; treating as FAIL"
  fi

  log_info "REVIEW: verdict=${verdict} score=${score}"

  # Post review result as PR comment
  local review_comment_file
  review_comment_file="$(mktemp "${TMPDIR:-/tmp}/runoq-review-comment.XXXXXX")"
  cat >"$review_comment_file" <<REVIEWEOF
<!-- runoq:event:review -->
## Diff Review - Round ${round}

- **Verdict**: ${verdict}
- **Score**: ${score}
$(if [[ -n "$review_checklist" ]]; then printf '\n### Checklist\n%s\n' "$review_checklist"; fi)
REVIEWEOF
  "$SCRIPTS_DIR/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$review_comment_file" >/dev/null 2>&1 || true
  rm -f "$review_comment_file"

  # Save state
  local state
  state="$(printf '%s' "$state_json" | jq \
    --arg phase "REVIEW" \
    --arg verdict "$verdict" \
    --arg score "$score" \
    --arg review_checklist "$review_checklist" '
    .phase = $phase
    | .verdict = $verdict
    | .score = $score
    | .review_checklist = $review_checklist
  ')"
  save_state "$issue_number" "$state" >/dev/null

  rm -f "$review_output_file"
  printf '%s\n' "$state"
}

###############################################################################
# PHASE: DECIDE
###############################################################################

phase_decide() {
  local repo="$1"
  local issue_number="$2"
  local state_json="$3"
  local verdict round max_rounds review_checklist

  verdict="$(printf '%s' "$state_json" | jq -r '.verdict // "FAIL"')"
  round="$(printf '%s' "$state_json" | jq -r '.round // 1')"
  max_rounds="$(config_max_rounds)"
  review_checklist="$(printf '%s' "$state_json" | jq -r '.review_checklist // ""')"

  log_info "DECIDE: verdict=${verdict} round=${round}/${max_rounds}"

  local decision next_phase
  if [[ "$verdict" == "PASS" ]]; then
    decision="finalize"
    next_phase="FINALIZE"
  elif [[ "$verdict" == "ITERATE" && "$round" -lt "$max_rounds" ]]; then
    decision="iterate"
    next_phase="DEVELOP"
  else
    # ITERATE at max rounds or FAIL
    decision="finalize-needs-review"
    next_phase="FINALIZE"
  fi

  local state
  state="$(printf '%s' "$state_json" | jq \
    --arg phase "DECIDE" \
    --arg decision "$decision" \
    --arg next_phase "$next_phase" '
    .phase = $phase
    | .decision = $decision
    | .next_phase = $next_phase
  ')"
  save_state "$issue_number" "$state" >/dev/null

  printf '%s\n' "$state"
}

###############################################################################
# PHASE: FINALIZE
###############################################################################

phase_finalize() {
  local repo="$1"
  local issue_number="$2"
  local state_json="$3"
  local metadata_json="$4"
  local pr_number verdict decision complexity caveats worktree
  local finalize_verdict issue_status

  pr_number="$(printf '%s' "$state_json" | jq -r '.pr_number')"
  verdict="$(printf '%s' "$state_json" | jq -r '.verdict // "FAIL"')"
  decision="$(printf '%s' "$state_json" | jq -r '.decision // "finalize-needs-review"')"
  complexity="$(printf '%s' "$metadata_json" | jq -r '.estimated_complexity // "medium"')"
  caveats="$(printf '%s' "$state_json" | jq -r '.caveats // ""')"
  worktree="$(printf '%s' "$state_json" | jq -r '.worktree')"

  log_info "FINALIZE: issue #${issue_number} decision=${decision} complexity=${complexity}"

  # Decision table
  if [[ "$verdict" == "PASS" && "$complexity" == "low" && -z "$caveats" ]]; then
    finalize_verdict="auto-merge"
    issue_status="done"
  elif [[ "$verdict" == "PASS" ]]; then
    finalize_verdict="needs-review"
    issue_status="needs-review"
  else
    finalize_verdict="needs-review"
    issue_status="needs-review"
  fi

  # Check auto-merge config
  if [[ "$finalize_verdict" == "auto-merge" ]]; then
    local auto_merge_enabled max_complexity
    auto_merge_enabled="$(config_auto_merge_enabled)"
    max_complexity="$(config_auto_merge_max_complexity)"
    if [[ "$auto_merge_enabled" != "true" ]]; then
      finalize_verdict="needs-review"
      issue_status="needs-review"
    fi
    # Validate complexity against max
    case "$max_complexity" in
      low)
        [[ "$complexity" == "low" ]] || { finalize_verdict="needs-review"; issue_status="needs-review"; }
        ;;
      medium)
        [[ "$complexity" == "low" || "$complexity" == "medium" ]] || { finalize_verdict="needs-review"; issue_status="needs-review"; }
        ;;
      high)
        ;;
    esac
  fi

  # Finalize PR
  local reviewer finalize_args
  reviewer="$(config_reviewer)"
  finalize_args=()
  if [[ "$finalize_verdict" == "needs-review" && -n "$reviewer" ]]; then
    finalize_args=(--reviewer "$reviewer")
  fi
  "$SCRIPTS_DIR/gh-pr-lifecycle.sh" finalize "$repo" "$pr_number" "$finalize_verdict" "${finalize_args[@]}" >/dev/null 2>&1 || true

  # Set issue status
  "$SCRIPTS_DIR/gh-issue-queue.sh" set-status "$repo" "$issue_number" "$issue_status" >/dev/null 2>&1 || true

  # Remove worktree if auto-merged
  if [[ "$finalize_verdict" == "auto-merge" ]]; then
    "$SCRIPTS_DIR/worktree.sh" remove "$issue_number" >/dev/null 2>&1 || true
  fi

  # Post finalization comment
  post_audit_comment "$repo" "$pr_number" "finalize" "Finalized: ${finalize_verdict}. Issue status: ${issue_status}."

  # Save terminal state
  local final_phase
  if [[ "$finalize_verdict" == "auto-merge" ]]; then
    final_phase="DONE"
  else
    final_phase="DONE"
  fi

  local state
  state="$(printf '%s' "$state_json" | jq \
    --arg phase "FINALIZE" \
    --arg finalize_verdict "$finalize_verdict" \
    --arg issue_status "$issue_status" '
    .phase = $phase
    | .finalize_verdict = $finalize_verdict
    | .issue_status = $issue_status
  ')"
  save_state "$issue_number" "$state" >/dev/null

  # Transition to DONE
  local done_state
  done_state="$(printf '%s' "$state" | jq '.phase = "DONE"')"
  save_state "$issue_number" "$done_state" >/dev/null

  printf '%s\n' "$done_state"
}

###############################################################################
# PHASE: INTEGRATE (epics)
###############################################################################

phase_integrate() {
  local repo="$1"
  local issue_number="$2"
  local state_json="$3"
  local epic_status criteria_commit worktree

  log_info "INTEGRATE: checking epic #${issue_number}"

  epic_status="$("$SCRIPTS_DIR/gh-issue-queue.sh" epic-status "$repo" "$issue_number")"
  local all_done
  all_done="$(printf '%s' "$epic_status" | jq -r '.all_done')"

  if [[ "$all_done" != "true" ]]; then
    log_info "INTEGRATE: not all children done for epic #${issue_number}"
    local state
    state="$(printf '%s' "$state_json" | jq '.phase = "DECIDE" | .decision = "integrate-pending"')"
    save_state "$issue_number" "$state" >/dev/null
    printf '%s\n' "$state"
    return 0
  fi

  criteria_commit="$(printf '%s' "$state_json" | jq -r '.criteria_commit // empty')"

  # Create integration worktree
  local title
  title="$(runoq::gh issue view "$issue_number" --repo "$repo" --json title | jq -r '.title')"
  local int_worktree_json int_worktree
  int_worktree_json="$("$SCRIPTS_DIR/worktree.sh" create "$issue_number" "${title}-integrate" 2>/dev/null || printf '{}')"
  int_worktree="$(printf '%s' "$int_worktree_json" | jq -r '.worktree // empty')"

  if [[ -z "$int_worktree" ]]; then
    int_worktree="$(printf '%s' "$state_json" | jq -r '.worktree')"
  fi

  # Run verification
  if [[ -n "$criteria_commit" ]]; then
    local verify_result
    verify_result="$("$SCRIPTS_DIR/verify.sh" integrate "$int_worktree" "$criteria_commit" 2>/dev/null || printf '{"ok":false}')"
    local ok
    ok="$(printf '%s' "$verify_result" | jq -r '.ok')"

    if [[ "$ok" == "true" ]]; then
      "$SCRIPTS_DIR/gh-issue-queue.sh" set-status "$repo" "$issue_number" "done" >/dev/null 2>&1 || true
      local state
      state="$(printf '%s' "$state_json" | jq '.phase = "INTEGRATE"')"
      save_state "$issue_number" "$state" >/dev/null
      state="$(printf '%s' "$state" | jq '.phase = "DONE"')"
      save_state "$issue_number" "$state" >/dev/null
      printf '%s\n' "$state"
    else
      # Create fix task
      local failures
      failures="$(printf '%s' "$verify_result" | jq -r '.failures | join(", ")')"
      log_error "INTEGRATE: verification failed for epic #${issue_number}: ${failures}"
      "$SCRIPTS_DIR/gh-issue-queue.sh" set-status "$repo" "$issue_number" "needs-review" >/dev/null 2>&1 || true
      local state
      state="$(printf '%s' "$state_json" | jq --arg failures "$failures" '.phase = "INTEGRATE" | .integrate_failures = $failures')"
      save_state "$issue_number" "$state" >/dev/null
      state="$(printf '%s' "$state" | jq '.phase = "FAILED"')"
      save_state "$issue_number" "$state" >/dev/null
      printf '%s\n' "$state"
    fi
  else
    # No criteria commit, just mark done
    "$SCRIPTS_DIR/gh-issue-queue.sh" set-status "$repo" "$issue_number" "done" >/dev/null 2>&1 || true
    local state
    state="$(printf '%s' "$state_json" | jq '.phase = "INTEGRATE"')"
    save_state "$issue_number" "$state" >/dev/null
    state="$(printf '%s' "$state" | jq '.phase = "DONE"')"
    save_state "$issue_number" "$state" >/dev/null
    printf '%s\n' "$state"
  fi
}

###############################################################################
# Process a single issue through the state machine
###############################################################################

process_issue() {
  local repo="$1"
  local issue_number="$2"
  local dry_run="$3"
  local title="$4"
  local metadata_json state_json phase current_state

  # Get issue metadata
  metadata_json="$(get_issue_metadata "$repo" "$issue_number")"

  # Check for existing state (resumption)
  if state_file_exists "$issue_number"; then
    current_state="$(load_state "$issue_number")"
    if [[ -n "$current_state" ]]; then
      phase="$(printf '%s' "$current_state" | jq -r '.phase // empty')"
      if [[ "$phase" == "DONE" || "$phase" == "FAILED" ]]; then
        log_info "Issue #${issue_number} is already in terminal phase ${phase}"
        printf '%s\n' "$current_state"
        return 0
      fi
      log_info "Resuming issue #${issue_number} from phase ${phase}"
      state_json="$current_state"
    fi
  fi

  phase="${phase:-}"

  # State machine loop
  while true; do
    case "${phase:-}" in
      ""|"")
        # No state yet, start from INIT
        if ! state_json="$(phase_init "$repo" "$issue_number" "$dry_run" "$title")"; then
          log_error "INIT failed for issue #${issue_number}"
          return 1
        fi
        if [[ "$dry_run" == "true" ]]; then
          printf '%s\n' "$state_json"
          return 0
        fi
        phase="INIT"
        ;;

      INIT)
        # Move to CRITERIA
        if ! state_json="$(phase_criteria "$repo" "$issue_number" "$state_json" "$metadata_json")"; then
          log_error "CRITERIA failed for issue #${issue_number}"
          local fail_state
          fail_state="$(printf '%s' "$state_json" | jq '.phase = "FAILED" | .failure_reason = "criteria phase failed"')"
          save_state "$issue_number" "$fail_state" >/dev/null
          return 1
        fi
        phase="CRITERIA"
        ;;

      CRITERIA)
        # Move to DEVELOP
        if ! state_json="$(phase_develop "$repo" "$issue_number" "$state_json" "$metadata_json")"; then
          log_error "DEVELOP failed for issue #${issue_number}"
          local fail_state
          fail_state="$(printf '%s' "$state_json" | jq '.phase = "FAILED" | .failure_reason = "develop phase failed"')"
          save_state "$issue_number" "$fail_state" >/dev/null
          return 1
        fi
        local dev_status
        dev_status="$(printf '%s' "$state_json" | jq -r '.status // "fail"')"
        if [[ "$dev_status" == "fail" || "$dev_status" == "budget_exhausted" ]]; then
          # Skip review, go straight to FINALIZE
          state_json="$(printf '%s' "$state_json" | jq '.verdict = "FAIL" | .decision = "finalize-needs-review"')"
          phase="DECIDE"
          # Save decide state
          state_json="$(printf '%s' "$state_json" | jq '.phase = "DECIDE"')"
          save_state "$issue_number" "$state_json" >/dev/null
          phase="DECIDE"
          continue
        fi
        phase="DEVELOP"
        ;;

      DEVELOP)
        # Move to REVIEW (only if status is review_ready)
        local dev_status
        dev_status="$(printf '%s' "$state_json" | jq -r '.status // "fail"')"
        if [[ "$dev_status" != "review_ready" ]]; then
          # Go to DECIDE with FAIL verdict
          state_json="$(printf '%s' "$state_json" | jq '.phase = "REVIEW" | .verdict = "FAIL"')"
          save_state "$issue_number" "$state_json" >/dev/null
          phase="REVIEW"
          # Skip review, go straight to decide
          state_json="$(printf '%s' "$state_json" | jq '.phase = "DECIDE" | .decision = "finalize-needs-review" | .next_phase = "FINALIZE"')"
          save_state "$issue_number" "$state_json" >/dev/null
          phase="DECIDE"
          continue
        fi
        if ! state_json="$(phase_review "$repo" "$issue_number" "$state_json")"; then
          log_error "REVIEW failed for issue #${issue_number}"
          local fail_state
          fail_state="$(printf '%s' "$state_json" | jq '.phase = "FAILED" | .failure_reason = "review phase failed"')"
          save_state "$issue_number" "$fail_state" >/dev/null
          return 1
        fi
        phase="REVIEW"
        ;;

      REVIEW)
        # Move to DECIDE
        if ! state_json="$(phase_decide "$repo" "$issue_number" "$state_json")"; then
          log_error "DECIDE failed for issue #${issue_number}"
          return 1
        fi
        phase="DECIDE"
        ;;

      DECIDE)
        local next_phase decision
        next_phase="$(printf '%s' "$state_json" | jq -r '.next_phase // "FINALIZE"')"
        decision="$(printf '%s' "$state_json" | jq -r '.decision // "finalize-needs-review"')"

        if [[ "$next_phase" == "DEVELOP" && "$decision" == "iterate" ]]; then
          # Loop back to DEVELOP with checklist
          local review_checklist
          review_checklist="$(printf '%s' "$state_json" | jq -r '.review_checklist // ""')"
          phase="CRITERIA"  # Set to CRITERIA so the machine transitions to DEVELOP
          if ! state_json="$(phase_develop "$repo" "$issue_number" "$state_json" "$metadata_json" "$review_checklist")"; then
            log_error "DEVELOP (iterate) failed for issue #${issue_number}"
            local fail_state
            fail_state="$(printf '%s' "$state_json" | jq '.phase = "FAILED" | .failure_reason = "develop iterate failed"')"
            save_state "$issue_number" "$fail_state" >/dev/null
            return 1
          fi
          phase="DEVELOP"
          continue
        fi

        if [[ "$next_phase" == "INTEGRATE" ]]; then
          if ! state_json="$(phase_integrate "$repo" "$issue_number" "$state_json")"; then
            log_error "INTEGRATE failed for issue #${issue_number}"
            return 1
          fi
          phase="$(printf '%s' "$state_json" | jq -r '.phase')"
          [[ "$phase" == "DONE" || "$phase" == "FAILED" ]] && break
          continue
        fi

        # Default: FINALIZE
        if ! state_json="$(phase_finalize "$repo" "$issue_number" "$state_json" "$metadata_json")"; then
          log_error "FINALIZE failed for issue #${issue_number}"
          return 1
        fi
        phase="DONE"
        break
        ;;

      FINALIZE)
        # Already handled, just need to complete
        local done_state
        done_state="$(printf '%s' "$state_json" | jq '.phase = "DONE"')"
        save_state "$issue_number" "$done_state" >/dev/null
        phase="DONE"
        break
        ;;

      DONE|FAILED)
        break
        ;;

      *)
        log_error "Unknown phase: ${phase}"
        return 1
        ;;
    esac
  done

  printf '%s\n' "$state_json"
}

###############################################################################
# Queue loop
###############################################################################

run_queue() {
  local repo="$1"
  local issue_number="${2:-}"
  local dry_run="$3"
  local consecutive_failures=0
  local failure_limit

  failure_limit="$(config_consecutive_failure_limit)"

  # Reconcile stale state first
  log_info "Running reconciliation"
  "$SCRIPTS_DIR/dispatch-safety.sh" reconcile "$repo" >/dev/null 2>&1 || true

  # Single-issue mode
  if [[ -n "$issue_number" ]]; then
    local title
    title="$(runoq::gh issue view "$issue_number" --repo "$repo" --json title | jq -r '.title // "untitled"')"
    if ! process_issue "$repo" "$issue_number" "$dry_run" "$title"; then
      log_error "Issue #${issue_number} failed"
      return 1
    fi
    return 0
  fi

  # Queue loop
  while true; do
    # Circuit breaker
    if [[ "$consecutive_failures" -ge "$failure_limit" ]]; then
      log_error "Circuit breaker tripped: ${consecutive_failures} consecutive failures"
      break
    fi

    # Get next issue
    local ready_label queue_result next_issue next_issue_number next_title
    ready_label="$(config_ready_label)"
    queue_result="$("$SCRIPTS_DIR/gh-issue-queue.sh" next "$repo" "$ready_label")"

    next_issue="$(printf '%s' "$queue_result" | jq -r '.issue // empty')"
    if [[ -z "$next_issue" || "$next_issue" == "null" ]]; then
      log_info "No actionable issues in queue"
      # Report skipped reasons
      local skipped
      skipped="$(printf '%s' "$queue_result" | jq -r '.skipped | length')"
      if [[ "$skipped" -gt 0 ]]; then
        log_info "Skipped ${skipped} issues (blocked or ineligible)"
      fi
      break
    fi

    next_issue_number="$(printf '%s' "$next_issue" | jq -r '.number')"
    next_title="$(printf '%s' "$next_issue" | jq -r '.title // "untitled"')"

    log_info "Processing issue #${next_issue_number}: ${next_title}"

    if process_issue "$repo" "$next_issue_number" "$dry_run" "$next_title"; then
      consecutive_failures=0
    else
      consecutive_failures=$((consecutive_failures + 1))
      log_error "Issue #${next_issue_number} failed (consecutive: ${consecutive_failures}/${failure_limit})"
    fi

    # Check for dry run
    if [[ "$dry_run" == "true" ]]; then
      break
    fi
  done
}

###############################################################################
# mention-triage subcommand
###############################################################################

mention_triage() {
  local repo="$1"
  local pr_number="$2"
  local handle

  handle="$(runoq::config_get '.identity.handle')"
  log_info "Polling mentions for @${handle} in ${repo}"

  local mentions
  mentions="$("$SCRIPTS_DIR/gh-pr-lifecycle.sh" poll-mentions "$repo" "$handle")"

  local mention_count
  mention_count="$(printf '%s' "$mentions" | jq -r 'length')"

  if [[ "$mention_count" -eq 0 ]]; then
    log_info "No unprocessed mentions found"
    return 0
  fi

  log_info "Processing ${mention_count} mention(s)"

  while IFS= read -r mention; do
    [[ -z "$mention" ]] && continue
    local comment_id author body classification

    comment_id="$(printf '%s' "$mention" | jq -r '.comment_id')"
    author="$(printf '%s' "$mention" | jq -r '.author')"
    body="$(printf '%s' "$mention" | jq -r '.body')"

    log_info "Classifying mention from @${author} (comment ${comment_id})"

    # Classify using haiku
    local classify_result
    classify_result="$(claude_exec --print --model haiku -- "Classify this PR comment. Return ONLY a JSON object: {\"type\": \"question|change-request|approval|irrelevant\"}. Comment: ${body}" 2>/dev/null || printf '{"type":"irrelevant"}')"

    # Extract JSON from classification
    classification="$(printf '%s' "$classify_result" | grep -o '{[^}]*}' | head -n1 | jq -r '.type // "irrelevant"' 2>/dev/null || printf 'irrelevant')"

    log_info "Classification: ${classification}"

    case "$classification" in
      question)
        # Spawn mention-responder agent
        local mention_payload
        mention_payload="$(jq -n \
          --arg repo "$repo" \
          --argjson pr_number "$pr_number" \
          --argjson comment_id "$comment_id" \
          --arg author "$author" \
          --arg body "$body" '{
          repo: $repo,
          pr_number: $pr_number,
          comment_id: $comment_id,
          author: $author,
          body: $body
        }')"
        claude_exec --print --permission-mode bypassPermissions --agent mention-responder --add-dir "$RUNOQ_ROOT" -- "$mention_payload" >/dev/null 2>&1 || true
        ;;

      change-request)
        # Extract checklist and create a develop iteration
        log_info "Change request from @${author} - would feed into develop loop"
        local change_comment_file
        change_comment_file="$(mktemp "${TMPDIR:-/tmp}/runoq-change-ack.XXXXXX")"
        printf '<!-- runoq:event:mention-triage -->\nAcknowledged change request from @%s. Will incorporate in next iteration.\n' "$author" >"$change_comment_file"
        "$SCRIPTS_DIR/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$change_comment_file" >/dev/null 2>&1 || true
        rm -f "$change_comment_file"
        ;;

      approval)
        log_info "Approval from @${author}"
        local approval_comment_file
        approval_comment_file="$(mktemp "${TMPDIR:-/tmp}/runoq-approval-ack.XXXXXX")"
        printf '<!-- runoq:event:mention-triage -->\nNoted approval from @%s.\n' "$author" >"$approval_comment_file"
        "$SCRIPTS_DIR/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$approval_comment_file" >/dev/null 2>&1 || true
        rm -f "$approval_comment_file"
        ;;

      irrelevant|*)
        log_info "Irrelevant mention from @${author} - recording as processed"
        ;;
    esac

    # Record mention as processed
    "$SCRIPTS_DIR/state.sh" record-mention "$comment_id" >/dev/null 2>&1 || true

  done < <(printf '%s' "$mentions" | jq -c '.[]')
}

###############################################################################
# Argument parsing
###############################################################################

parse_run_args() {
  local repo="$1"
  shift

  RUN_REPO="$repo"
  RUN_ISSUE=""
  RUN_DRY_RUN=false

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --issue)
        [[ $# -ge 2 ]] || runoq::die "--issue requires a value"
        RUN_ISSUE="$2"
        shift 2
        ;;
      --dry-run)
        RUN_DRY_RUN=true
        shift
        ;;
      *)
        usage >&2
        exit 1
        ;;
    esac
  done
}

###############################################################################
# Main
###############################################################################

main() {
  local subcommand="${1:-}"
  [[ -n "$subcommand" ]] || { usage >&2; exit 1; }
  shift

  case "$subcommand" in
    run)
      [[ $# -ge 1 ]] || { usage >&2; exit 1; }
      parse_run_args "$@"
      run_queue "$RUN_REPO" "$RUN_ISSUE" "$RUN_DRY_RUN"
      ;;
    mention-triage)
      [[ $# -eq 2 ]] || { usage >&2; exit 1; }
      mention_triage "$1" "$2"
      ;;
    *)
      usage >&2
      exit 1
      ;;
  esac
}

main "$@"
