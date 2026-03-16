#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  run.sh [--issue N] [--dry-run]
EOF
}

claude_exec() {
  local claude_bin="${AGENDEV_CLAUDE_BIN:-claude}"
  command -v "$claude_bin" >/dev/null 2>&1 || agendev::die "Claude CLI not found: $claude_bin"
  "$claude_bin" "$@"
}

parse_args() {
  issue_number=""
  dry_run=false

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --issue)
        [[ $# -ge 2 ]] || agendev::die "--issue requires a value"
        issue_number="$2"
        shift 2
        ;;
      --dry-run)
        dry_run=true
        shift
        ;;
      *)
        usage >&2
        exit 1
        ;;
    esac
  done
}

fallback_to_claude() {
  local args=()
  if [[ -n "$issue_number" ]]; then
    args+=(--issue "$issue_number")
  fi
  if [[ "$dry_run" == "true" ]]; then
    args+=(--dry-run)
  fi
  claude_exec --agent github-orchestrator --add-dir "$AGENDEV_ROOT" -- "${args[@]}"
}

issue_body_file() {
  mktemp "${TMPDIR:-/tmp}/agendev-issue.XXXXXX"
}

comment_file() {
  mktemp "${TMPDIR:-/tmp}/agendev-comment.XXXXXX"
}

summary_file() {
  mktemp "${TMPDIR:-/tmp}/agendev-summary.XXXXXX"
}

issue_json() {
  local issue="$1"
  agendev::gh issue view "$issue" --repo "$REPO" --json number,title,body,labels,url
}

body_summary() {
  local body_file="$1"
  tr '\n' ' ' <"$body_file" | sed -E 's/[[:space:]]+/ /g' | cut -c1-500
}

metadata_complexity() {
  local body_file="$1"
  awk '
    /<!-- agendev:meta/ { in_block = 1; next }
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
  printf '%s\n' "$payload" | "$(agendev::root)/scripts/state.sh" save "$issue" >/dev/null
}

write_dispatch_comment() {
  local pr_number="$1"
  local payload_file="$2"
  local comment_path
  comment_path="$(comment_file)"
  {
    echo "<!-- agendev:payload:github-orchestrator-dispatch -->"
    echo
    echo '```json'
    cat "$payload_file"
    echo
    echo '```'
  } >"$comment_path"
  "$(agendev::root)/scripts/gh-pr-lifecycle.sh" comment "$REPO" "$pr_number" "$comment_path" >/dev/null
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
    echo "<!-- agendev:payload:codex-return -->"
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
  "$(agendev::root)/scripts/gh-pr-lifecycle.sh" comment "$REPO" "$pr_number" "$comment_path" >/dev/null
  rm -f "$comment_path"
}

write_orchestrator_comment() {
  local pr_number="$1"
  local payload_file="$2"
  local comment_path
  comment_path="$(comment_file)"
  {
    echo "<!-- agendev:payload:orchestrator-return -->"
    echo
    echo '```json'
    cat "$payload_file"
    echo
    echo '```'
  } >"$comment_path"
  "$(agendev::root)/scripts/gh-pr-lifecycle.sh" comment "$REPO" "$pr_number" "$comment_path" >/dev/null
  rm -f "$comment_path"
}

write_summary_update() {
  local orchestrator_file="$1"
  local summary_path
  summary_path="$(summary_file)"
  {
    echo "<!-- agendev:summary:start -->"
    jq -r '.summary' "$orchestrator_file"
    echo "<!-- agendev:summary:end -->"
    echo
    echo "<!-- agendev:attention:start -->"
    if [[ "$(jq -r '.caveats | length' "$orchestrator_file")" -eq 0 ]]; then
      echo "None."
    else
      jq -r '.caveats[]' "$orchestrator_file"
    fi
    echo "<!-- agendev:attention:end -->"
  } >"$summary_path"
  printf '%s\n' "$summary_path"
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
    echo "<!-- agendev:payload:codex-return -->"
    echo '```json'
    cat "${output_file}.json"
    echo
    echo '```'
  } >"$output_file"
  rm -f "${output_file}.json"
}

fixture_mode_run() {
  local reconcile_output issue_data body_file title worktree_json branch_name worktree_path pr_json pr_number base_sha
  local dispatch_payload_file codex_output_file payload_file verification_file orchestrator_file summary_path verdict complexity

  reconcile_output="$("$(agendev::root)/scripts/dispatch-safety.sh" reconcile "$REPO")"
  if [[ "$dry_run" == "true" ]]; then
    jq -n --argjson reconciliation "$reconcile_output" '{mode:"dry-run", reconciliation:$reconciliation}'
    return
  fi

  [[ -n "$issue_number" ]] || agendev::die "fixture mode currently requires --issue"

  issue_data="$(issue_json "$issue_number")"
  body_file="$(issue_body_file)"
  printf '%s' "$issue_data" | jq -r '.body // ""' >"$body_file"
  title="$(printf '%s' "$issue_data" | jq -r '.title')"
  complexity="$(metadata_complexity "$body_file")"
  [[ -n "$complexity" ]] || complexity="medium"

  "$(agendev::root)/scripts/dispatch-safety.sh" eligibility "$REPO" "$issue_number" >/dev/null
  "$(agendev::root)/scripts/gh-issue-queue.sh" set-status "$REPO" "$issue_number" in-progress >/dev/null

  worktree_json="$("$(agendev::root)/scripts/worktree.sh" create "$issue_number" "$title")"
  branch_name="$(printf '%s' "$worktree_json" | jq -r '.branch')"
  worktree_path="$(printf '%s' "$worktree_json" | jq -r '.worktree')"

  pr_json="$("$(agendev::root)/scripts/gh-pr-lifecycle.sh" create "$REPO" "$branch_name" "$issue_number" "$title")"
  pr_number="$(printf '%s' "$pr_json" | jq -r '.number')"
  base_sha="$(git -C "$worktree_path" rev-parse HEAD)"

  save_state_json "$issue_number" "$(jq -n \
    --arg phase "INIT" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:0, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  dispatch_payload_file="$(mktemp "${TMPDIR:-/tmp}/agendev-dispatch.XXXXXX")"
  jq -n \
    --argjson issue_number "$issue_number" \
    --arg issue_title "$title" \
    --arg issue_body_summary "$(body_summary "$body_file")" \
    --arg branch "$branch_name" \
    --argjson pr_number "$pr_number" \
    --arg repo "$REPO" \
    --arg worktree "$worktree_path" \
    --argjson max_rounds "$(agendev::config_get '.maxRounds')" \
    --argjson max_token_budget "$(agendev::config_get '.maxTokenBudget')" '
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

  save_state_json "$issue_number" "$(jq -n \
    --arg phase "DEVELOP" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  if [[ -n "${AGENDEV_TEST_DEV_COMMAND:-}" ]]; then
    bash -lc "cd \"$worktree_path\" && ${AGENDEV_TEST_DEV_COMMAND}"
  fi

  codex_output_file="$(mktemp "${TMPDIR:-/tmp}/agendev-codex-output.XXXXXX")"
  if [[ -n "${AGENDEV_TEST_CODEX_OUTPUT_FILE:-}" ]]; then
    cp "$AGENDEV_TEST_CODEX_OUTPUT_FILE" "$codex_output_file"
  else
    generate_fixture_codex_output "$worktree_path" "$base_sha" "$codex_output_file"
  fi

  payload_file="$(mktemp "${TMPDIR:-/tmp}/agendev-codex-payload.XXXXXX")"
  "$(agendev::root)/scripts/state.sh" validate-payload "$worktree_path" "$base_sha" "$codex_output_file" >"$payload_file"
  write_codex_comment "$pr_number" "$payload_file"

  verification_file="$(mktemp "${TMPDIR:-/tmp}/agendev-verify.XXXXXX")"
  "$(agendev::root)/scripts/verify.sh" round "$worktree_path" "$branch_name" "$base_sha" "$payload_file" >"$verification_file"
  if [[ "$(jq -r '.ok' "$verification_file")" != "true" ]]; then
    agendev::die "fixture mode happy path requires a passing verification result"
  fi

  save_state_json "$issue_number" "$(jq -n \
    --arg phase "REVIEW" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  save_state_json "$issue_number" "$(jq -n \
    --arg phase "DECIDE" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  save_state_json "$issue_number" "$(jq -n \
    --arg phase "FINALIZE" \
    --arg branch "$branch_name" \
    --arg worktree "$worktree_path" \
    --argjson pr_number "$pr_number" '
    {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number}
  ')"

  orchestrator_file="$(mktemp "${TMPDIR:-/tmp}/agendev-orchestrator.XXXXXX")"
  if [[ -n "${AGENDEV_TEST_ORCHESTRATOR_RETURN_FILE:-}" ]]; then
    cp "$AGENDEV_TEST_ORCHESTRATOR_RETURN_FILE" "$orchestrator_file"
  else
    jq -n '{verdict:"PASS", rounds_used:1, final_score:42, summary:"Completed successfully.", caveats:[], tokens_used:0}' >"$orchestrator_file"
  fi
  write_orchestrator_comment "$pr_number" "$orchestrator_file"

  summary_path="$(write_summary_update "$orchestrator_file")"
  "$(agendev::root)/scripts/gh-pr-lifecycle.sh" update-summary "$REPO" "$pr_number" "$summary_path" >/dev/null
  verdict="$(jq -r '.verdict' "$orchestrator_file")"

  if [[ "$verdict" == "PASS" && "$complexity" == "low" ]]; then
    "$(agendev::root)/scripts/gh-pr-lifecycle.sh" finalize "$REPO" "$pr_number" auto-merge >/dev/null
    "$(agendev::root)/scripts/gh-issue-queue.sh" set-status "$REPO" "$issue_number" "done" >/dev/null
    save_state_json "$issue_number" "$(jq -n \
      --arg phase "DONE" \
      --arg branch "$branch_name" \
      --arg worktree "$worktree_path" \
      --argjson pr_number "$pr_number" \
      --slurpfile outcome "$orchestrator_file" '
      {phase:$phase, round:1, branch:$branch, worktree:$worktree, pr_number:$pr_number, outcome:$outcome[0]}
    ')"
    "$(agendev::root)/scripts/worktree.sh" remove "$issue_number" >/dev/null
  else
    agendev::die "fixture mode currently supports only PASS + low complexity happy path"
  fi

  jq -n \
    --argjson issue "$issue_number" \
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

main() {
  parse_args "$@"

  if [[ "${AGENDEV_TEST_RUN_MODE:-}" == "fixture" ]]; then
    fixture_mode_run
  else
    fallback_to_claude
  fi
}

main "$@"
