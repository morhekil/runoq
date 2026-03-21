#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

# ---------------------------------------------------------------------------
# Fixture-mode pipeline: exercises the full issue-to-PR lifecycle using
# shell scripts and env-var-driven test doubles instead of the Claude CLI.
# ---------------------------------------------------------------------------

issue_body_file() {
  mktemp "${TMPDIR:-/tmp}/runoq-issue.XXXXXX"
}

comment_file() {
  mktemp "${TMPDIR:-/tmp}/runoq-comment.XXXXXX"
}

summary_file() {
  mktemp "${TMPDIR:-/tmp}/runoq-summary.XXXXXX"
}

event_comment_file() {
  local message="$1"
  local path
  path="$(comment_file)"
  {
    echo "<!-- runoq:event -->"
    echo "$message"
  } >"$path"
  printf '%s\n' "$path"
}

issue_json() {
  local issue="$1"
  runoq::gh issue view "$issue" --repo "$REPO" --json number,title,body,labels,url
}

body_summary() {
  local body_file="$1"
  tr '\n' ' ' <"$body_file" | sed -E 's/[[:space:]]+/ /g' | cut -c1-500
}

metadata_complexity() {
  local body_file="$1"
  awk '
    /<!-- runoq:meta/ { in_block = 1; next }
    in_block && /-->/ { exit }
    in_block && /^estimated_complexity:/ {
      sub(/^estimated_complexity:[[:space:]]*/, "", $0)
      print
      exit
    }
  ' "$body_file"
}

save_state_json() {
  local issue="$1"
  local payload="$2"
  printf '%s\n' "$payload" | "$(runoq::root)/scripts/state.sh" save "$issue" >/dev/null
}

post_pr_event() {
  local pr_number="$1"
  local message="$2"
  local comment_path
  comment_path="$(event_comment_file "$message")"
  "$(runoq::root)/scripts/gh-pr-lifecycle.sh" comment "$REPO" "$pr_number" "$comment_path" >/dev/null 2>&1 || true
  rm -f "$comment_path"
}

post_issue_event() {
  local issue="$1"
  local message="$2"
  local body
  body="$(printf '<!-- runoq:event -->\n%s\n' "$message")"
  runoq::gh issue comment "$issue" --repo "$REPO" --body "$body" >/dev/null 2>&1 || true
}

first_reviewer() {
  jq -r '.reviewers[0] // empty' "$(runoq::config_path)" 2>/dev/null || true
}

fixture_env_value() {
  local base="$1"
  local issue="$2"
  local issue_key="${base}_${issue}"
  if [[ -n "${!issue_key:-}" ]]; then
    printf '%s\n' "${!issue_key}"
    return
  fi
  printf '%s\n' "${!base:-}"
}

ready_label() {
  runoq::config_get '.labels.ready'
}

write_dispatch_comment() {
  local pr_number="$1"
  local payload_file="$2"
  local comment_path
  comment_path="$(comment_file)"
  {
    echo "<!-- runoq:payload:github-orchestrator-dispatch -->"
    echo
    echo '```json'
    cat "$payload_file"
    echo
    echo '```'
  } >"$comment_path"
  "$(runoq::root)/scripts/gh-pr-lifecycle.sh" comment "$REPO" "$pr_number" "$comment_path" >/dev/null
  rm -f "$comment_path"
}

write_codex_comment() {
  local pr_number="$1"
  local payload_file="$2"
  local comment_path commits_count files_count tests_summary status
  comment_path="$(comment_file)"
  commits_count="$(jq -r '.commits_pushed | length' "$payload_file")"
  files_count="$(jq -r '(.files_changed + .files_added + .files_deleted) | length' "$payload_file")"
  tests_summary="$(jq -r '.test_summary' "$payload_file")"
  status="$(jq -r '.status' "$payload_file")"
  {
    echo "<!-- runoq:payload:codex-return -->"
    printf '**Dev round 1 complete:** status=%s, %s commits, %s files changed, tests %s\n' "$status" "$commits_count" "$files_count" "$tests_summary"
    echo
    echo '<details>'
    echo '<summary>Full payload</summary>'
    echo
    echo '```json'
    cat "$payload_file"
    echo
    echo '```'
    echo '</details>'
  } >"$comment_path"
  "$(runoq::root)/scripts/gh-pr-lifecycle.sh" comment "$REPO" "$pr_number" "$comment_path" >/dev/null
  rm -f "$comment_path"
}

write_orchestrator_comment() {
  local pr_number="$1"
  local payload_file="$2"
  local comment_path
  comment_path="$(comment_file)"
  {
    echo "<!-- runoq:payload:orchestrator-return -->"
    echo
    echo '```json'
    cat "$payload_file"
    echo
    echo '```'
  } >"$comment_path"
  "$(runoq::root)/scripts/gh-pr-lifecycle.sh" comment "$REPO" "$pr_number" "$comment_path" >/dev/null
  rm -f "$comment_path"
}

write_summary_update() {
  local orchestrator_file="$1"
  local summary_path
  summary_path="$(summary_file)"
  {
    echo "<!-- runoq:summary:start -->"
    jq -r '.summary' "$orchestrator_file"
    echo "<!-- runoq:summary:end -->"
    echo
    echo "<!-- runoq:attention:start -->"
    if [[ "$(jq -r '.caveats | length' "$orchestrator_file")" -eq 0 ]]; then
      echo "None."
    else
      jq -r '.caveats[]' "$orchestrator_file"
    fi
    echo "<!-- runoq:attention:end -->"
  } >"$summary_path"
  printf '%s\n' "$summary_path"
}

write_payload_reconstruction_comment() {
  local pr_number="$1"
  local payload_file="$2"
  local message source patched_fields discrepancies
  source="$(jq -r '.payload_source // "unknown"' "$payload_file")"
  patched_fields="$(jq -r '(.patched_fields // []) | join(", ")' "$payload_file")"
  discrepancies="$(jq -r '(.discrepancies // []) | join(", ")' "$payload_file")"
  message="Codex payload required reconstruction. Source=${source}."
  if [[ -n "$patched_fields" ]]; then
    message="${message} Patched fields: ${patched_fields}."
  fi
  if [[ -n "$discrepancies" ]]; then
    message="${message} Discrepancies: ${discrepancies}."
  fi
  post_pr_event "$pr_number" "$message"
}

write_verification_failure_comment() {
  local pr_number="$1"
  local verification_file="$2"
  local failures
  failures="$(jq -r '.failures | join(", ")' "$verification_file")"
  post_pr_event "$pr_number" "Post-dev verification failed: ${failures}. Feeding errors to next dev round."
}

write_failure_result() {
  local summary="$1"
  local reason="$2"
  local output_file
  output_file="$(mktemp "${TMPDIR:-/tmp}/runoq-orchestrator-failure.XXXXXX")"
  jq -n \
    --arg verdict "FAIL" \
    --arg summary "$summary" \
    --arg reason "$reason" '
    {
      verdict: $verdict,
      rounds_used: 1,
      final_score: 0,
      summary: $summary,
      caveats: [$reason],
      tokens_used: 0
    }
  ' >"$output_file"
  printf '%s\n' "$output_file"
}

save_failed_state() {
  local issue="$1"
  local branch="$2"
  local worktree="$3"
  local pr_number="$4"
  local outcome_file="$5"
  save_state_json "$issue" "$(jq -n \
    --arg phase "FAILED" \
    --arg branch "$branch" \
    --arg worktree "$worktree" \
    --argjson pr_number "$pr_number" \
    --slurpfile outcome "$outcome_file" '
    {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number, outcome:$outcome[0]}
  ')"
}

finalize_needs_review() {
  local issue="$1"
  local branch="$2"
  local worktree="$3"
  local pr_number="$4"
  local reason="$5"
  local summary="$6"
  local outcome_file="${7:-}"
  local reviewer orchestrator_file summary_path cleanup_outcome=false

  reviewer="$(first_reviewer)"
  if [[ -n "$outcome_file" ]]; then
    orchestrator_file="$outcome_file"
  else
    orchestrator_file="$(write_failure_result "$summary" "$reason")"
    write_orchestrator_comment "$pr_number" "$orchestrator_file"
    cleanup_outcome=true
  fi
  summary_path="$(write_summary_update "$orchestrator_file")"
  "$(runoq::root)/scripts/gh-pr-lifecycle.sh" update-summary "$REPO" "$pr_number" "$summary_path" >/dev/null
  if [[ -n "$reviewer" ]]; then
    post_pr_event "$pr_number" "Assigned to @${reviewer} for human review. Reason: ${reason}."
    "$(runoq::root)/scripts/gh-pr-lifecycle.sh" finalize "$REPO" "$pr_number" needs-review --reviewer "$reviewer" >/dev/null
  else
    post_pr_event "$pr_number" "Marked for human review. Reason: ${reason}."
    "$(runoq::root)/scripts/gh-pr-lifecycle.sh" finalize "$REPO" "$pr_number" needs-review >/dev/null
  fi
  "$(runoq::root)/scripts/gh-issue-queue.sh" set-status "$REPO" "$issue" "needs-review" >/dev/null
  post_issue_event "$issue" "Escalated to human review: ${reason}."
  save_failed_state "$issue" "$branch" "$worktree" "$pr_number" "$orchestrator_file"
  if [[ "$cleanup_outcome" == "true" ]]; then
    rm -f "$orchestrator_file"
  fi
  rm -f "$summary_path"
  jq -n \
    --argjson issue "$issue" \
    --argjson pr_number "$pr_number" \
    --arg worktree "$worktree" \
    --arg reason "$reason" '
    {
      issue: $issue,
      pr_number: $pr_number,
      worktree: $worktree,
      status: "needs-review",
      reason: $reason
    }
  '
}

run_dev_command() {
  local worktree="$1"
  local issue="$2"
  local timeout dev_command
  timeout="$(runoq::config_get '.stall.timeoutSeconds')"
  dev_command="$(fixture_env_value "RUNOQ_TEST_DEV_COMMAND" "$issue")"
  [[ -n "$dev_command" ]] || dev_command="true"
  "$(runoq::root)/scripts/watchdog.sh" \
    --timeout "$timeout" \
    --issue "$issue" \
    --state-dir "$(runoq::state_dir)" \
    -- env RUNOQ_TEST_CURRENT_ISSUE="$issue" bash -lc "cd \"$worktree\" && ${dev_command}"
}

post_circuit_breaker_event() {
  local pr_number="$1"
  local issue="$2"
  local limit="$3"
  local failed_issues_json="$4"
  local failed_list message
  failed_list="$(printf '%s' "$failed_issues_json" | jq -r 'map("#" + tostring) | join(", ")')"
  message="Queue halted after ${limit} consecutive failures. Failed issues: ${failed_list}. Investigate before resuming."
  post_pr_event "$pr_number" "$message"
  post_issue_event "$issue" "$message"
}

generate_fixture_codex_output() {
  local worktree="$1"
  local base_sha="$2"
  local output_file="$3"
  local commits_json diff_json commit_range first_commit last_commit

  commits_json="$(git -C "$worktree" rev-list --reverse "${base_sha}..HEAD" | jq -Rsc 'split("\n") | map(select(length > 0))')"
  first_commit="$(printf '%s' "$commits_json" | jq -r '.[0] // ""')"
  last_commit="$(printf '%s' "$commits_json" | jq -r '.[-1] // ""')"
  if [[ -n "$first_commit" && -n "$last_commit" ]]; then
    commit_range="${first_commit}..${last_commit}"
  else
    commit_range=""
  fi
  diff_json="$(git -C "$worktree" diff --name-status "${base_sha}..HEAD" | jq -Rsc '
    split("\n")
    | map(select(length > 0))
    | map(split("\t"))
    | {
        files_changed: [ .[] | select(.[0] != "A" and .[0] != "D") | .[-1] ],
        files_added: [ .[] | select(.[0] == "A") | .[-1] ],
        files_deleted: [ .[] | select(.[0] == "D") | .[-1] ]
      }
  ')"

  jq -n \
    --argjson commits "$commits_json" \
    --arg commit_range "$commit_range" \
    --argjson diff "$diff_json" '
    {
      status: "completed",
      commits_pushed: $commits,
      commit_range: $commit_range,
      files_changed: $diff.files_changed,
      files_added: $diff.files_added,
      files_deleted: $diff.files_deleted,
      tests_run: true,
      tests_passed: true,
      test_summary: "ok",
      build_passed: true,
      blockers: [],
      notes: ""
    }
  ' >"${output_file}.json"

  {
    echo "<!-- runoq:payload:codex-return -->"
    echo '```json'
    cat "${output_file}.json"
    echo
    echo '```'
  } >"$output_file"
  rm -f "${output_file}.json"
}

fixture_run_issue() {
  local current_issue="$1"
  local issue_data body_file title worktree_json branch_name worktree_path pr_json pr_number base_sha
  local dispatch_payload_file codex_output_file payload_file verification_file orchestrator_file summary_path verdict complexity
  local dev_exit timeout_seconds failure_reason codex_output_source orchestrator_result_source

  issue_data="$(issue_json "$current_issue")"
  body_file="$(issue_body_file)"
  printf '%s' "$issue_data" | jq -r '.body // ""' >"$body_file"
  title="$(printf '%s' "$issue_data" | jq -r '.title')"
  complexity="$(metadata_complexity "$body_file")"
  [[ -n "$complexity" ]] || complexity="medium"

  "$(runoq::root)/scripts/dispatch-safety.sh" eligibility "$REPO" "$current_issue" >/dev/null
  "$(runoq::root)/scripts/gh-issue-queue.sh" set-status "$REPO" "$current_issue" in-progress >/dev/null

  worktree_json="$("$(runoq::root)/scripts/worktree.sh" create "$current_issue" "$title")"
  branch_name="$(printf '%s' "$worktree_json" | jq -r '.branch')"
  worktree_path="$(printf '%s' "$worktree_json" | jq -r '.worktree')"

  pr_json="$("$(runoq::root)/scripts/gh-pr-lifecycle.sh" create "$REPO" "$branch_name" "$current_issue" "$title")"
  pr_number="$(printf '%s' "$pr_json" | jq -r '.number')"
  base_sha="$(git -C "$worktree_path" rev-parse HEAD)"

  save_state_json "$current_issue" "$(jq -n \
    --arg phase "INIT" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:0, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  dispatch_payload_file="$(mktemp "${TMPDIR:-/tmp}/runoq-dispatch.XXXXXX")"
  jq -n \
    --argjson issue_number "$current_issue" \
    --arg issue_title "$title" \
    --arg issue_body_summary "$(body_summary "$body_file")" \
    --arg branch "$branch_name" \
    --argjson pr_number "$pr_number" \
    --arg repo "$REPO" \
    --arg worktree "$worktree_path" \
    --argjson max_rounds "$(runoq::config_get '.maxRounds')" \
    --argjson max_token_budget "$(runoq::config_get '.maxTokenBudget')" '
    {
      issue_number: $issue_number,
      issue_title: $issue_title,
      issue_body_summary: $issue_body_summary,
      branch: $branch,
      pr_number: $pr_number,
      repo: $repo,
      worktree: $worktree,
      max_rounds: $max_rounds,
      max_token_budget: $max_token_budget
    }
  ' >"$dispatch_payload_file"
  write_dispatch_comment "$pr_number" "$dispatch_payload_file"

  save_state_json "$current_issue" "$(jq -n \
    --arg phase "DEVELOP" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  set +e
  run_dev_command "$worktree_path" "$current_issue"
  dev_exit="$?"
  set -e
  if [[ "$dev_exit" -ne 0 ]]; then
    timeout_seconds="$(runoq::config_get '.stall.timeoutSeconds')"
    if [[ "$dev_exit" -eq 124 ]]; then
      failure_reason="Agent stalled after ${timeout_seconds} seconds of inactivity. Process terminated. State preserved for resume."
    else
      failure_reason="Agent exited unexpectedly (exit code ${dev_exit}). Last phase: DEVELOP, round 1."
    fi
    post_pr_event "$pr_number" "$failure_reason"
    post_issue_event "$current_issue" "$failure_reason"
    rm -f "$body_file" "$dispatch_payload_file"
    exit "$dev_exit"
  fi

  codex_output_file="$(mktemp "${TMPDIR:-/tmp}/runoq-codex-output.XXXXXX")"
  codex_output_source="$(fixture_env_value "RUNOQ_TEST_CODEX_OUTPUT_FILE" "$current_issue")"
  if [[ -n "$codex_output_source" ]]; then
    cp "$codex_output_source" "$codex_output_file"
  else
    generate_fixture_codex_output "$worktree_path" "$base_sha" "$codex_output_file"
  fi

  payload_file="$(mktemp "${TMPDIR:-/tmp}/runoq-codex-payload.XXXXXX")"
  "$(runoq::root)/scripts/state.sh" validate-payload "$worktree_path" "$base_sha" "$codex_output_file" >"$payload_file"
  if [[ "$(jq -r '((.patched_fields // []) | length > 0) or ((.discrepancies // []) | length > 0)' "$payload_file")" == "true" ]]; then
    write_payload_reconstruction_comment "$pr_number" "$payload_file"
  fi
  write_codex_comment "$pr_number" "$payload_file"

  verification_file="$(mktemp "${TMPDIR:-/tmp}/runoq-verify.XXXXXX")"
  "$(runoq::root)/scripts/verify.sh" round "$worktree_path" "$branch_name" "$base_sha" "$payload_file" >"$verification_file"
  if [[ "$(jq -r '.ok' "$verification_file")" != "true" ]]; then
    write_verification_failure_comment "$pr_number" "$verification_file"
    failure_reason="post-dev verification failed: $(jq -r '.failures | join(", ")' "$verification_file")"
    finalize_needs_review "$current_issue" "$branch_name" "$worktree_path" "$pr_number" "$failure_reason" "Escalated to human review after verification failure."
    rm -f "$body_file" "$dispatch_payload_file" "$codex_output_file" "$payload_file" "$verification_file"
    return
  fi

  save_state_json "$current_issue" "$(jq -n \
    --arg phase "REVIEW" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  save_state_json "$current_issue" "$(jq -n \
    --arg phase "DECIDE" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  save_state_json "$current_issue" "$(jq -n \
    --arg phase "FINALIZE" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  orchestrator_file="$(mktemp "${TMPDIR:-/tmp}/runoq-orchestrator.XXXXXX")"
  orchestrator_result_source="$(fixture_env_value "RUNOQ_TEST_ORCHESTRATOR_RETURN_FILE" "$current_issue")"
  if [[ -n "$orchestrator_result_source" ]]; then
    cp "$orchestrator_result_source" "$orchestrator_file"
  else
    jq -n '{verdict:"PASS", rounds_used:1, final_score:42, summary:"Completed successfully.", caveats:[], tokens_used:0}' >"$orchestrator_file"
  fi
  write_orchestrator_comment "$pr_number" "$orchestrator_file"

  summary_path="$(write_summary_update "$orchestrator_file")"
  "$(runoq::root)/scripts/gh-pr-lifecycle.sh" update-summary "$REPO" "$pr_number" "$summary_path" >/dev/null
  verdict="$(jq -r '.verdict' "$orchestrator_file")"

  if [[ "$verdict" == "PASS" && "$complexity" == "low" && "$(jq -r '.caveats | length' "$orchestrator_file")" -eq 0 ]]; then
    "$(runoq::root)/scripts/gh-pr-lifecycle.sh" finalize "$REPO" "$pr_number" auto-merge >/dev/null
    "$(runoq::root)/scripts/gh-issue-queue.sh" set-status "$REPO" "$current_issue" "done" >/dev/null
    save_state_json "$current_issue" "$(jq -n \
      --arg phase "DONE" \
      --arg branch "$branch_name" \
      --arg worktree "$worktree_path" \
      --argjson pr_number "$pr_number" \
      --slurpfile outcome "$orchestrator_file" '
      {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number, outcome:$outcome[0]}
    ')"
    "$(runoq::root)/scripts/worktree.sh" remove "$current_issue" >/dev/null
  else
    if [[ "$verdict" != "PASS" ]]; then
      failure_reason="orchestrator returned ${verdict}: $(jq -r '.summary' "$orchestrator_file")"
    elif [[ "$(jq -r '.caveats | length' "$orchestrator_file")" -gt 0 ]]; then
      failure_reason="$(jq -r '.caveats | join(", ")' "$orchestrator_file")"
    else
      failure_reason="issue complexity ${complexity} requires human review"
    fi
    finalize_needs_review "$current_issue" "$branch_name" "$worktree_path" "$pr_number" "$failure_reason" "$(jq -r '.summary' "$orchestrator_file")" "$orchestrator_file"
    rm -f "$body_file" "$dispatch_payload_file" "$codex_output_file" "$payload_file" "$verification_file" "$orchestrator_file" "$summary_path"
    return
  fi

  jq -n \
    --argjson issue "$current_issue" \
    --argjson pr_number "$pr_number" \
    --arg worktree "$worktree_path" '
    {
      issue: $issue,
      pr_number: $pr_number,
      worktree: $worktree,
      status: "completed"
    }
  '

  rm -f "$body_file" "$dispatch_payload_file" "$codex_output_file" "$payload_file" "$verification_file" "$orchestrator_file" "$summary_path"
}

fixture_mode_run() {
  local issue_number="$1"
  local dry_run="$2"
  local reconcile_output queue_listing selection current_issue result status runs
  local consecutive_failures failure_limit failed_streak latest_pr latest_issue

  reconcile_output="$("$(runoq::root)/scripts/dispatch-safety.sh" reconcile "$REPO")"

  if [[ "$dry_run" == "true" && -n "$issue_number" ]]; then
    jq -n \
      --argjson reconciliation "$reconcile_output" \
      --argjson issue "$issue_number" '
      {
        mode: "dry-run",
        reconciliation: $reconciliation,
        issue: $issue
      }
    '
    return
  fi

  if [[ "$dry_run" == "true" ]]; then
    queue_listing="$("$(runoq::root)/scripts/gh-issue-queue.sh" list "$REPO" "$(ready_label)")"
    selection="$("$(runoq::root)/scripts/gh-issue-queue.sh" next "$REPO" "$(ready_label)")"
    jq -n \
      --argjson reconciliation "$reconcile_output" \
      --argjson queue "$queue_listing" \
      --argjson selection "$selection" '
      {
        mode: "dry-run",
        reconciliation: $reconciliation,
        queue: $queue,
        selection: $selection
      }
    '
    return
  fi

  if [[ -n "$issue_number" ]]; then
    fixture_run_issue "$issue_number"
    return
  fi

  runs='[]'
  consecutive_failures=0
  failure_limit="$(runoq::config_get '.consecutiveFailureLimit')"
  failed_streak='[]'
  latest_pr=""
  latest_issue=""

  while true; do
    selection="$("$(runoq::root)/scripts/gh-issue-queue.sh" next "$REPO" "$(ready_label)")"
    current_issue="$(printf '%s' "$selection" | jq -r '.issue.number // empty')"
    if [[ -z "$current_issue" ]]; then
      jq -n \
        --argjson reconciliation "$reconcile_output" \
        --argjson runs "$runs" \
        --argjson skipped "$(printf '%s' "$selection" | jq '.skipped')" '
        {
          status: "completed",
          reconciliation: $reconciliation,
          runs: $runs,
          skipped: $skipped
        }
      '
      return
    fi

    result="$(fixture_run_issue "$current_issue")"
    runs="$(jq -n --argjson runs "$runs" --argjson result "$result" '$runs + [$result]')"
    status="$(printf '%s' "$result" | jq -r '.status')"
    if [[ "$status" == "completed" ]]; then
      consecutive_failures=0
      failed_streak='[]'
      continue
    fi

    consecutive_failures=$((consecutive_failures + 1))
    failed_streak="$(jq -n --argjson failed "$failed_streak" --argjson issue "$current_issue" '$failed + [$issue]')"
    latest_pr="$(printf '%s' "$result" | jq -r '.pr_number')"
    latest_issue="$current_issue"

    if (( consecutive_failures >= failure_limit )); then
      post_circuit_breaker_event "$latest_pr" "$latest_issue" "$failure_limit" "$failed_streak"
      jq -n \
        --argjson reconciliation "$reconcile_output" \
        --argjson runs "$runs" \
        --argjson failed_issues "$failed_streak" '
        {
          status: "halted",
          reconciliation: $reconciliation,
          runs: $runs,
          failed_issues: $failed_issues
        }
      '
      return
    fi
  done
}

# Allow sourcing without executing
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  echo "This script is meant to be called from run.sh, not executed directly." >&2
  exit 1
fi
