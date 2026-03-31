#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RUNOQ_ROOT="${RUNOQ_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
export RUNOQ_ROOT

orchestrator_implementation="${RUNOQ_ORCHESTRATOR_IMPLEMENTATION:-${RUNOQ_IMPLEMENTATION:-runtime}}"
case "$orchestrator_implementation" in
  shell|"")
    ;;
  runtime)
    runtime_bin="${RUNOQ_RUNTIME_BIN:-}"
    if [[ -n "$runtime_bin" ]]; then
      exec "$runtime_bin" "__orchestrator" "$@"
    fi
    go_bin="${RUNOQ_GO_BIN:-go}"
    command -v "$go_bin" >/dev/null 2>&1 || {
      echo "runoq: Go toolchain not found: $go_bin" >&2
      exit 1
    }
    cd "$RUNOQ_ROOT"
    exec "$go_bin" run "$RUNOQ_ROOT/cmd/runoq-runtime" "__orchestrator" "$@"
    ;;
  *)
    echo "runoq: Unknown RUNOQ_ORCHESTRATOR_IMPLEMENTATION: $orchestrator_implementation (expected shell or runtime)" >&2
    exit 1
    ;;
esac

export RUNOQ_LOG=1

# shellcheck source=./scripts/lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"

SCRIPTS_DIR="$SCRIPT_DIR"
RUNOQ_ROOT="${RUNOQ_ROOT:-$(runoq::root)}"

# Ensure we use the app installation token for all GitHub operations
# so comments/labels appear as the runoq bot, not the operator
export RUNOQ_FORCE_REFRESH_TOKEN=1
if eval "$("$SCRIPTS_DIR/gh-auth.sh" export-token)" 2>/dev/null; then
  printf '[orchestrator] Token mint succeeded\n' >&2
else
  printf '[orchestrator] Token mint failed or skipped (will use ambient credentials)\n' >&2
fi

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
  runoq::captured_exec claude "$(runoq::target_root)" "$claude_bin" "$@"
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

config_done_label() {
  runoq::config_get '.labels.done'
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

save_state_checked() {
  local issue="$1"
  local state_json="$2"
  local context="$3"
  if ! save_state "$issue" "$state_json" >/dev/null; then
    log_error "${context}: failed to save state"
    return 1
  fi
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

capture_command_output() {
  local __stdout_var="$1"
  local __stderr_var="$2"
  shift 2

  local stdout_file stderr_file status
  stdout_file="$(mktemp "${TMPDIR:-/tmp}/runoq-capture-stdout.XXXXXX")"
  stderr_file="$(mktemp "${TMPDIR:-/tmp}/runoq-capture-stderr.XXXXXX")"

  set +e
  "$@" >"$stdout_file" 2>"$stderr_file"
  status=$?
  set -e

  printf -v "$__stdout_var" '%s' "$(cat "$stdout_file")"
  printf -v "$__stderr_var" '%s' "$(cat "$stderr_file")"

  rm -f "$stdout_file" "$stderr_file"
  return "$status"
}

init_failure_state() {
  local reason="$1"
  local branch="${2:-}"
  local worktree="${3:-}"
  local pr_number="${4:-}"

  jq -n \
    --arg phase "FAILED" \
    --arg failure_stage "INIT" \
    --arg failure_scope "internal" \
    --arg failure_reason "$reason" \
    --arg branch "$branch" \
    --arg worktree "$worktree" \
    --arg pr_number "$pr_number" '
    {
      phase: $phase,
      failure_stage: $failure_stage,
      failure_scope: $failure_scope,
      failure_reason: $failure_reason
    }
    + (if $branch != "" then {branch: $branch} else {} end)
    + (if $worktree != "" then {worktree: $worktree} else {} end)
    + (if ($pr_number | test("^[0-9]+$")) then {pr_number: ($pr_number | tonumber)} else {} end)
  '
}

handle_init_failure() {
  local repo="$1"
  local issue_number="$2"
  local reason="$3"
  local branch="${4:-}"
  local worktree="${5:-}"
  local pr_number="${6:-}"
  local fail_state

  log_error "INIT: ${reason}"
  fail_state="$(init_failure_state "$reason" "$branch" "$worktree" "$pr_number")"
  save_state "$issue_number" "$fail_state" >/dev/null 2>&1 || true

  if [[ -z "$pr_number" || "$pr_number" == "null" ]]; then
    "$SCRIPTS_DIR/gh-issue-queue.sh" set-status "$repo" "$issue_number" "ready" >/dev/null 2>&1 || true
    if [[ -n "$worktree" ]]; then
      "$SCRIPTS_DIR/worktree.sh" remove "$issue_number" >/dev/null 2>&1 || true
    fi
  fi

  return 1
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
  printf '<!-- runoq:event:%s -->\n> Posted by `orchestrator` — %s phase\n\n%s\n' "$event" "$event" "$body" >"$comment_file"
  "$SCRIPTS_DIR/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$comment_file" >/dev/null 2>&1 || true
  rm -f "$comment_file"
}

post_issue_comment() {
  local repo="$1"
  local issue_number="$2"
  local event="$3"
  local body="$4"
  local full_body
  full_body="$(printf '<!-- runoq:event:%s -->\n> Posted by `orchestrator` — %s phase\n\n%s' "$event" "$event" "$body")"
  runoq::gh issue comment "$issue_number" --repo "$repo" --body "$full_body" >/dev/null 2>&1 || true
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
  body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-meta.XXXXXX")"
  printf '%s' "$issue_json" | jq -r '.body // ""' >"$body_file"

  metadata="$("$SCRIPTS_DIR/gh-issue-queue.sh" list "$repo" "$(config_ready_label)" 2>/dev/null | jq --argjson n "$issue_number" '.[] | select(.number == $n)' 2>/dev/null || printf '{}')"

  if [[ -z "$metadata" || "$metadata" == "{}" ]]; then
    # Parse from body directly
    local block complexity complexity_rationale type_val
    block="$(awk '/<!-- runoq:meta/{in_block=1;next} in_block && /-->/{exit} in_block{print}' "$body_file")"
    complexity="$(printf '%s\n' "$block" | sed -n 's/^estimated_complexity:[[:space:]]*//p' | head -n1)"
    complexity_rationale="$(printf '%s\n' "$block" | sed -n 's/^complexity_rationale:[[:space:]]*//p' | head -n1)"
    type_val="$(printf '%s\n' "$block" | sed -n 's/^type:[[:space:]]*//p' | head -n1)"
    metadata="$(jq -n \
      --argjson issue "$issue_json" \
      --arg complexity "${complexity:-medium}" \
      --arg complexity_rationale "${complexity_rationale:-}" \
      --arg type_val "${type_val:-task}" '{
      number: $issue.number,
      title: $issue.title,
      body: $issue.body,
      url: $issue.url,
      estimated_complexity: $complexity,
      complexity_rationale: (if $complexity_rationale == "" or $complexity_rationale == "null" then null else $complexity_rationale end),
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
  local worktree_stderr pr_stderr

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
  if ! "$SCRIPTS_DIR/gh-issue-queue.sh" set-status "$repo" "$issue_number" "in-progress" >/dev/null; then
    handle_init_failure "$repo" "$issue_number" "failed to set issue status to in-progress"
    return 1
  fi

  # 3. Create worktree (bot identity is set by worktree.sh)
  if ! capture_command_output worktree_json worktree_stderr "$SCRIPTS_DIR/worktree.sh" create "$issue_number" "$title"; then
    handle_init_failure "$repo" "$issue_number" "worktree creation failed: ${worktree_stderr:-unknown error}"
    return 1
  fi
  if ! printf '%s' "$worktree_json" | jq -e '
    (.branch | type == "string" and length > 0)
    and (.worktree | type == "string" and length > 0)
  ' >/dev/null 2>&1; then
    handle_init_failure "$repo" "$issue_number" "worktree creation returned an invalid payload"
    return 1
  fi
  worktree="$(printf '%s' "$worktree_json" | jq -r '.worktree')"
  branch="$(printf '%s' "$worktree_json" | jq -r '.branch')"
  log_info "INIT: worktree=${worktree} branch=${branch}"

  # 4. Configure HTTPS remote with bot token so pushes are attributed to the app
  if runoq::configure_git_bot_remote "$worktree" "$repo" 2>/dev/null; then
    log_info "INIT: bot remote configured for worktree"
  else
    log_info "INIT: bot remote configuration failed or skipped for worktree"
  fi

  # 5. Create initial empty commit and push
  if ! git -C "$worktree" commit --allow-empty -m "runoq: begin work on #${issue_number}" >/dev/null 2>&1; then
    handle_init_failure "$repo" "$issue_number" "failed to create the initial worktree commit" "$branch" "$worktree"
    return 1
  fi
  if ! git -C "$worktree" push -u origin "$branch" >/dev/null 2>&1; then
    handle_init_failure "$repo" "$issue_number" "failed to push the initial worktree branch" "$branch" "$worktree"
    return 1
  fi

  # 5. Create draft PR
  if ! capture_command_output pr_json pr_stderr "$SCRIPTS_DIR/gh-pr-lifecycle.sh" create "$repo" "$branch" "$issue_number" "$title"; then
    handle_init_failure "$repo" "$issue_number" "draft PR creation failed: ${pr_stderr:-unknown error}" "$branch" "$worktree"
    return 1
  fi
  if ! printf '%s' "$pr_json" | jq -e '.number | numbers' >/dev/null 2>&1; then
    handle_init_failure "$repo" "$issue_number" "draft PR creation returned an invalid payload" "$branch" "$worktree"
    return 1
  fi
  pr_number="$(printf '%s' "$pr_json" | jq -r '.number')"
  log_info "INIT: created draft PR #${pr_number} for branch=${branch}"

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
  if ! save_state_checked "$issue_number" "$state" "INIT"; then
    handle_init_failure "$repo" "$issue_number" "failed to persist INIT state" "$branch" "$worktree" "$pr_number"
    return 1
  fi

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
  local complexity_rationale
  complexity_rationale="$(printf '%s' "$4" | jq -r '.complexity_rationale // empty')"
  worktree="$(printf '%s' "$state_json" | jq -r '.worktree')"
  branch="$(printf '%s' "$state_json" | jq -r '.branch')"
  pr_number="$(printf '%s' "$state_json" | jq -r '.pr_number')"

  # Skip criteria for low complexity
  if [[ "$complexity" == "low" ]]; then
    log_info "CRITERIA: skipped (low complexity)"
    local state
    state="$(printf '%s' "$state_json" | jq '.phase = "CRITERIA"')"
    if ! save_state_checked "$issue_number" "$state" "CRITERIA"; then
      return 1
    fi
    printf '%s\n' "$state"
    return 0
  fi

  log_info "CRITERIA: spawning bar-setter for issue #${issue_number}"

  local spec_file payload bar_setter_output output_file criteria_commit

  # Write issue body to spec file
  spec_file="$(mktemp "${TMPDIR:-/tmp}/runoq-spec.XXXXXX")"
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
  runoq::claude_stream "$output_file" \
    --permission-mode bypassPermissions --agent bar-setter --add-dir "$RUNOQ_ROOT" -- "$payload"

  # Extract criteria_commit from bar-setter output
  criteria_commit="$(extract_marked_block "$output_file" 'runoq:payload:bar-setter' | jq -r '.criteria_commit // empty' 2>/dev/null || printf '')"

  if [[ -z "$criteria_commit" ]]; then
    # Try to get the latest commit on the branch as criteria commit
    criteria_commit="$(git -C "$worktree" rev-parse HEAD 2>/dev/null || printf '')"
  fi

  # Post criteria summary as PR comment
  local summary_file
  summary_file="$(mktemp "${TMPDIR:-/tmp}/runoq-criteria-summary.XXXXXX")"
  {
    printf '<!-- runoq:event:criteria -->\n## Acceptance Criteria Set\n\nCriteria commit: `%s`\nComplexity: **%s**' "$criteria_commit" "$complexity"
    if [[ -n "$complexity_rationale" ]]; then
      printf -- ' — %s' "$complexity_rationale"
    fi
    printf '\n'
  } >"$summary_file"
  "$SCRIPTS_DIR/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$summary_file" >/dev/null 2>&1 || true
  rm -f "$summary_file"

  # Save state
  local state
  state="$(printf '%s' "$state_json" | jq \
    --arg phase "CRITERIA" \
    --arg criteria_commit "$criteria_commit" '.phase = $phase | .criteria_commit = $criteria_commit')"
  if ! save_state_checked "$issue_number" "$state" "CRITERIA"; then
    return 1
  fi

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
  spec_file="$(mktemp "${TMPDIR:-/tmp}/runoq-spec.XXXXXX")"
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

  # Add criteria_commit if present in state
  local criteria_commit
  criteria_commit="$(printf '%s' "$state_json" | jq -r '.criteria_commit // empty')"
  if [[ -n "$criteria_commit" ]]; then
    payload="$(printf '%s' "$payload" | jq --arg cc "$criteria_commit" '. + {criteria_commit: $cc}')"
  fi

  # Write payload to temp file for issue-runner
  local payload_file output_file runner_result
  payload_file="$(mktemp "${TMPDIR:-/tmp}/runoq-runner-payload.XXXXXX")"
  printf '%s' "$payload" > "$payload_file"
  output_file="$(mktemp "${TMPDIR:-/tmp}/runoq-runner-out.XXXXXX")"

  local runner_stderr_file
  runner_stderr_file="$(mktemp "${TMPDIR:-/tmp}/runoq-runner-err.XXXXXX")"
  local runner_exit=0

  if [[ -x "$SCRIPTS_DIR/issue-runner.sh" ]]; then
    set +e
    "$SCRIPTS_DIR/issue-runner.sh" run "$payload_file" >"$output_file" 2>"$runner_stderr_file"
    runner_exit=$?
    set -e
  else
    set +e
    runoq::claude_stream "$output_file" \
      --permission-mode bypassPermissions --agent issue-runner --add-dir "$RUNOQ_ROOT" -- "$payload"
    runner_exit=$?
    set -e
  fi
  rm -f "$payload_file"

  # Log runner stderr for diagnostics
  if [[ -s "$runner_stderr_file" ]]; then
    log_info "issue-runner stderr ($(wc -l < "$runner_stderr_file") lines): $(head -10 "$runner_stderr_file")"
    log_info "issue-runner stderr tail: $(tail -5 "$runner_stderr_file")"
  else
    log_info "issue-runner stderr: <empty>"
  fi
  rm -f "$runner_stderr_file"

  # Parse issue-runner return payload
  # The issue-runner script outputs clean JSON to stdout; try direct parse first
  log_info "issue-runner output file: size=$(wc -c < "$output_file") lines=$(wc -l < "$output_file")"
  log_info "issue-runner output head: $(head -3 "$output_file" 2>/dev/null | tr '\n' ' ')"
  runner_result="$(jq -e '.' "$output_file" 2>/dev/null || printf '')"
  if [[ -z "$runner_result" ]]; then
    # Fallback: extract from marked payload blocks (agent-based runner)
    runner_result="$("$SCRIPTS_DIR/state.sh" extract-payload "$output_file" 2>/dev/null || printf '')"
  fi
  if [[ -z "$runner_result" ]]; then
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
    log_error "Failed to parse issue-runner output"
    rm -f "$spec_file" "$output_file"
    return 1
  fi

  if [[ "$runner_exit" -ne 0 ]]; then
    log_error "issue-runner exited with status ${runner_exit}"
    rm -f "$spec_file" "$output_file"
    return 1
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
  if ! save_state_checked "$issue_number" "$state" "DEVELOP"; then
    rm -f "$spec_file" "$output_file"
    return 1
  fi

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
  runoq::claude_stream "$review_output_file" \
    --permission-mode bypassPermissions --agent diff-reviewer --add-dir "$RUNOQ_ROOT" -- "$review_payload"

  # Parse verdict from reviewLogPath
  local verdict_json verdict score review_checklist
  local review_log_abs="${review_log_path}"
  if [[ -n "$review_log_path" && "$review_log_path" != /* ]]; then
    review_log_abs="${worktree}/${review_log_path}"
  fi
  log_info "REVIEW: review_log_path=${review_log_path} review_log_abs=${review_log_abs} exists=$([[ -f "${review_log_abs:-/dev/null}" ]] && echo yes || echo no)"
  if [[ -n "$review_log_abs" && -f "$review_log_abs" ]]; then
    verdict_json="$(parse_review_verdict "$review_log_abs")"
  else
    log_info "REVIEW: review log not found, parsing from claude output file"
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

  # Post review result as PR comment with full context
  local review_comment_file max_rounds_val
  max_rounds_val="$(config_max_rounds)"
  review_comment_file="$(mktemp "${TMPDIR:-/tmp}/runoq-review-comment.XXXXXX")"
  cat >"$review_comment_file" <<REVIEWEOF
<!-- runoq:event:review -->
## Diff Review — round ${round} / ${max_rounds_val}

> Posted by \`orchestrator\` via \`diff-reviewer\` agent

| Field | Value |
|-------|-------|
| **Verdict** | ${verdict} |
| **Score** | ${score} |
| **Commit range** | \`${baseline_hash:0:7}..${head_hash:0:7}\` |
| **Changed files** | ${changed_files} |

$(if [[ -n "$review_checklist" ]]; then printf '### Checklist\n%s\n' "$review_checklist"; fi)
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
  if ! save_state_checked "$issue_number" "$state" "REVIEW"; then
    rm -f "$review_output_file"
    return 1
  fi

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
  if ! save_state_checked "$issue_number" "$state" "DECIDE"; then
    return 1
  fi

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
  local score round max_rounds
  score="$(printf '%s' "$state_json" | jq -r '.score // "n/a"')"
  round="$(printf '%s' "$state_json" | jq -r '.round // "?"')"
  max_rounds="$(config_max_rounds)"

  log_info "FINALIZE: issue #${issue_number} decision=${decision} complexity=${complexity}"

  # Decision table
  local finalize_reason=""
  local auto_merge_enabled max_complexity
  auto_merge_enabled="$(config_auto_merge_enabled)"
  max_complexity="$(config_auto_merge_max_complexity)"

  if [[ "$verdict" != "PASS" ]]; then
    finalize_verdict="needs-review"
    issue_status="needs-review"
    finalize_reason="Review verdict was ${verdict} (not PASS)."
  elif [[ -n "$caveats" && "$caveats" != "[]" && "$caveats" != "" ]]; then
    finalize_verdict="needs-review"
    issue_status="needs-review"
    finalize_reason="Caveats present: ${caveats}"
  elif [[ "$auto_merge_enabled" != "true" ]]; then
    finalize_verdict="needs-review"
    issue_status="needs-review"
    finalize_reason="Auto-merge is disabled in config."
  else
    # Check complexity against auto-merge threshold
    local complexity_ok=false
    case "$max_complexity" in
      low)    [[ "$complexity" == "low" ]] && complexity_ok=true ;;
      medium) [[ "$complexity" == "low" || "$complexity" == "medium" ]] && complexity_ok=true ;;
      high)   complexity_ok=true ;;
    esac
    if [[ "$complexity_ok" == "true" ]]; then
      finalize_verdict="auto-merge"
      issue_status="done"
    else
      finalize_verdict="needs-review"
      issue_status="needs-review"
      finalize_reason="Complexity '${complexity}' exceeds auto-merge threshold '${max_complexity}'."
    fi
  fi

  log_info "FINALIZE: decision table: auto_merge_enabled=${auto_merge_enabled} max_complexity=${max_complexity} complexity=${complexity} complexity_ok=${complexity_ok:-n/a} finalize_verdict=${finalize_verdict} finalize_reason=${finalize_reason:-none} issue_status=${issue_status}"

  # Finalize PR
  local reviewer finalize_args
  reviewer="$(config_reviewer)"
  finalize_args=()
  if [[ "$finalize_verdict" == "needs-review" && -n "$reviewer" ]]; then
    finalize_args=(--reviewer "$reviewer")
  fi
  log_info "FINALIZE: calling pr-lifecycle finalize verdict=${finalize_verdict} pr=#${pr_number}"
  "$SCRIPTS_DIR/gh-pr-lifecycle.sh" finalize "$repo" "$pr_number" "$finalize_verdict" "${finalize_args[@]}" >/dev/null 2>&1 || true

  # Set issue status
  log_info "FINALIZE: setting issue #${issue_number} status to ${issue_status}"
  if "$SCRIPTS_DIR/gh-issue-queue.sh" set-status "$repo" "$issue_number" "$issue_status" >/dev/null 2>&1; then
    log_info "FINALIZE: set-status succeeded for issue #${issue_number}"
  else
    log_info "FINALIZE: set-status failed for issue #${issue_number}"
  fi

  # Remove worktree if auto-merged
  if [[ "$finalize_verdict" == "auto-merge" ]]; then
    log_info "FINALIZE: removing worktree for issue #${issue_number} (auto-merged)"
    if "$SCRIPTS_DIR/worktree.sh" remove "$issue_number" >/dev/null 2>&1; then
      log_info "FINALIZE: worktree removed successfully"
    else
      log_info "FINALIZE: worktree removal failed"
    fi
  fi

  # Post finalization comment with full decision context
  local finalize_detail
  finalize_detail="$(cat <<FINALIZE
## Finalize — issue #${issue_number}

| Field | Value |
|-------|-------|
| **Decision** | \`${finalize_verdict}\` |
| **Issue status** | \`${issue_status}\` |
| **Review verdict** | ${verdict} |
| **Review score** | ${score} |
| **Complexity** | ${complexity} |
| **Auto-merge enabled** | ${auto_merge_enabled} |
| **Auto-merge max complexity** | ${max_complexity} |
| **Round** | ${round} / ${max_rounds} |

$(if [[ -n "$finalize_reason" ]]; then printf '**Reason**: %s\n' "$finalize_reason"; fi)
$(if [[ -n "$caveats" && "$caveats" != "[]" && "$caveats" != "" ]]; then printf '**Caveats**: %s\n' "$caveats"; fi)
FINALIZE
)"
  post_audit_comment "$repo" "$pr_number" "finalize" "$finalize_detail"

  # Also post status on the issue itself for visibility
  if [[ "$finalize_verdict" == "needs-review" ]]; then
    post_issue_comment "$repo" "$issue_number" "finalize" "Marked for human review.\n\n**Reason**: ${finalize_reason}\n**Verdict**: ${verdict}\n**Score**: ${score}\n**Complexity**: ${complexity}"
  fi

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
  if ! save_state_checked "$issue_number" "$state" "FINALIZE"; then
    return 1
  fi

  # Transition to DONE
  local done_state
  done_state="$(printf '%s' "$state" | jq '.phase = "DONE"')"
  if ! save_state_checked "$issue_number" "$done_state" "FINALIZE"; then
    return 1
  fi

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
        phase="DEVELOP"
        ;;

      DEVELOP)
        # Move to REVIEW (skip review if status is not review_ready but still transition properly)
        local dev_status
        dev_status="$(printf '%s' "$state_json" | jq -r '.status // "fail"')"
        if [[ "$dev_status" != "review_ready" ]]; then
          # Transition through REVIEW -> DECIDE with FAIL verdict
          state_json="$(printf '%s' "$state_json" | jq '.phase = "REVIEW" | .verdict = "FAIL"')"
          save_state "$issue_number" "$state_json" >/dev/null
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

  # Configure target repo git identity + HTTPS remote so all git operations
  # (commits, pushes) are attributed to the bot, not the operator
  local target_root
  target_root="$(runoq::target_root)"
  log_info "Configuring bot identity for target root: ${target_root}"
  if runoq::configure_git_bot_identity "$target_root" 2>/dev/null; then
    log_info "Bot identity configured successfully"
  else
    log_info "Bot identity configuration failed or skipped"
  fi
  if runoq::configure_git_bot_remote "$target_root" "$repo" 2>/dev/null; then
    log_info "Bot remote configured successfully for repo=${repo}"
  else
    log_info "Bot remote configuration failed or skipped"
  fi

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

    local total_skipped skipped_summary
    total_skipped="$(printf '%s' "$queue_result" | jq -r '.skipped | length')"
    skipped_summary="$(printf '%s' "$queue_result" | jq -r '.skipped | map("#\(.number // "?") — \(.blocked_reasons // ["unknown"] | join(", "))") | join("; ")')"
    next_issue="$(printf '%s' "$queue_result" | jq -r '.issue // empty')"

    if [[ -z "$next_issue" || "$next_issue" == "null" ]]; then
      log_info "Queue result: 0 actionable issues, ${total_skipped} skipped"
      if [[ "$total_skipped" -gt 0 ]]; then
        log_info "Skipped details: ${skipped_summary}"
      fi
      break
    else
      log_info "Queue result: 1 actionable issue found, ${total_skipped} skipped"
      if [[ "$total_skipped" -gt 0 ]]; then
        log_info "Skipped details: ${skipped_summary}"
      fi
    fi

    next_issue_number="$(printf '%s' "$next_issue" | jq -r '.number')"
    next_title="$(printf '%s' "$next_issue" | jq -r '.title // "untitled"')"

    log_info "Processing issue #${next_issue_number}: ${next_title}"

    if process_issue "$repo" "$next_issue_number" "$dry_run" "$next_title"; then
      local terminal_phase
      terminal_phase="$(load_state "$next_issue_number" | jq -r '.phase // "unknown"' 2>/dev/null || printf 'unknown')"
      log_info "Issue #${next_issue_number} succeeded — terminal phase: ${terminal_phase}"
      consecutive_failures=0
      # Verify label change propagated before re-querying the queue
      if [[ "$terminal_phase" == "DONE" ]]; then
        local issue_status expected_label
        issue_status="$(load_state "$next_issue_number" | jq -r '.issue_status // "done"' 2>/dev/null || printf 'done')"
        expected_label="$(runoq::label_for_status "$issue_status" 2>/dev/null || config_done_label)"
        local _attempt
        for _attempt in 1 2 3 4 5; do
          if runoq::gh issue view "$next_issue_number" --repo "$repo" --json labels \
            | jq -e --arg l "$expected_label" '.labels | map(.name) | index($l) != null' >/dev/null 2>&1; then
            log_info "Label propagation confirmed for issue #${next_issue_number}: ${expected_label} (attempt ${_attempt})"
            break
          fi
          log_info "Waiting for label propagation on issue #${next_issue_number}: expecting ${expected_label} (attempt ${_attempt}/5)"
          sleep 3
        done
      fi
    else
      consecutive_failures=$((consecutive_failures + 1))
      local terminal_phase
      terminal_phase="$(load_state "$next_issue_number" | jq -r '.phase // "unknown"' 2>/dev/null || printf 'unknown')"
      log_error "Issue #${next_issue_number} failed — terminal phase: ${terminal_phase} (consecutive: ${consecutive_failures}/${failure_limit})"
    fi

    # Check for dry run
    if [[ "$dry_run" == "true" ]]; then
      break
    fi
  done

  # ---------------------------------------------------------------------------
  # Epic sweep: after all tasks drain, check if any epics can be integrated
  # ---------------------------------------------------------------------------
  if [[ "$dry_run" != "true" ]]; then
    local ready_label_epic epic_issues
    ready_label_epic="$(config_ready_label)"
    epic_issues="$("$SCRIPTS_DIR/gh-issue-queue.sh" list "$repo" "$ready_label_epic" | jq -c '[.[] | select(.type == "epic")]')"
    local epic_count
    epic_count="$(printf '%s' "$epic_issues" | jq 'length')"

    if [[ "$epic_count" -gt 0 ]]; then
      log_info "Epic sweep: found ${epic_count} epic(s) to evaluate"

      while IFS= read -r epic; do
        [[ -z "$epic" ]] && continue
        local epic_number epic_title epic_status_json all_children_done
        epic_number="$(printf '%s' "$epic" | jq -r '.number')"
        epic_title="$(printf '%s' "$epic" | jq -r '.title // "untitled"')"

        epic_status_json="$("$SCRIPTS_DIR/gh-issue-queue.sh" epic-status "$repo" "$epic_number")"
        all_children_done="$(printf '%s' "$epic_status_json" | jq -r '.all_done')"

        if [[ "$all_children_done" != "true" ]]; then
          local pending_children
          pending_children="$(printf '%s' "$epic_status_json" | jq -r '.pending | map("#" + tostring) | join(", ")')"
          log_info "Epic sweep: epic #${epic_number} (${epic_title}) — children pending: ${pending_children}"
          continue
        fi

        log_info "Epic sweep: all children done for epic #${epic_number} (${epic_title}) — running integration"

        # Build minimal state for phase_integrate
        local epic_state
        if state_file_exists "$epic_number"; then
          epic_state="$(load_state "$epic_number")"
        else
          epic_state="$(jq -n --argjson n "$epic_number" '{issue_number: $n, phase: "DECIDE", next_phase: "INTEGRATE"}')"
        fi

        if phase_integrate "$repo" "$epic_number" "$epic_state"; then
          local epic_phase
          epic_phase="$(load_state "$epic_number" | jq -r '.phase // "unknown"' 2>/dev/null || printf 'unknown')"
          log_info "Epic sweep: epic #${epic_number} integration complete — phase: ${epic_phase}"
        else
          log_error "Epic sweep: epic #${epic_number} integration failed"
        fi
      done < <(printf '%s' "$epic_issues" | jq -c '.[]')
    fi
  fi
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
