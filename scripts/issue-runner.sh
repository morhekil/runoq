#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

RUNOQ_ROOT="$(runoq::root)"

usage() {
  cat <<'EOF'
Usage:
  issue-runner.sh run <payload-json-file>
EOF
}

# ---------------------------------------------------------------------------
# Codex execution wrapper
# ---------------------------------------------------------------------------

codex_exec() {
  local codex_bin="${RUNOQ_CODEX_BIN:-codex}"
  command -v "$codex_bin" >/dev/null 2>&1 || runoq::die "Codex CLI not found: $codex_bin"
  (cd "$worktree" && "$codex_bin" "$@")
}

# ---------------------------------------------------------------------------
# Token tracking — best-effort extraction from codex output
# ---------------------------------------------------------------------------

extract_tokens_from_log() {
  local log_file="$1"
  local tokens=0
  # Look for token usage patterns in codex output (e.g. "tokens: 12345" or "token_usage": 12345)
  local found
  found="$(grep -oiE '(tokens?[_ ]*(used|usage|count)?[[:space:]]*[:=][[:space:]]*[0-9]+)' "$log_file" 2>/dev/null | tail -1 | grep -oE '[0-9]+$' || true)"
  if [[ -n "$found" ]]; then
    tokens="$found"
  fi
  printf '%s\n' "$tokens"
}

# ---------------------------------------------------------------------------
# Review scope expansion — find files that import changed files
# ---------------------------------------------------------------------------

expand_review_scope() {
  local worktree="$1"
  shift
  local changed_files=("$@")
  local related_files=()
  local seen=()

  # Build a set of changed files for deduplication
  for f in "${changed_files[@]}"; do
    seen+=("$f")
  done

  for changed in "${changed_files[@]}"; do
    local basename_no_ext
    basename_no_ext="$(basename "$changed")"
    basename_no_ext="${basename_no_ext%.*}"
    [[ -z "$basename_no_ext" ]] && continue

    local grep_results
    grep_results="$(grep -rl --include='*.ts' --include='*.js' --include='*.py' --include='*.go' \
      "$basename_no_ext" "$worktree/" 2>/dev/null || true)"

    while IFS= read -r hit; do
      [[ -z "$hit" ]] && continue
      # Make path relative to worktree
      local rel="${hit#"$worktree/"}"
      # Filter out generated/vendored directories
      case "$rel" in
        node_modules/*|vendor/*|dist/*|build/*) continue ;;
      esac
      # Filter out test files
      case "$rel" in
        *.test.*|*.spec.*|*_test.*|*_spec.*|test/*|tests/*|__tests__/*) continue ;;
      esac
      # Deduplicate against changed files and already-found related files
      local already=false
      for s in "${seen[@]}"; do
        if [[ "$s" == "$rel" ]]; then
          already=true
          break
        fi
      done
      if [[ "$already" == "false" ]]; then
        related_files+=("$rel")
        seen+=("$rel")
      fi
    done <<< "$grep_results"
  done

  # Output as JSON array
  printf '%s\n' "${related_files[@]}" | jq -Rsc 'split("\n") | map(select(length > 0))'
}

# ---------------------------------------------------------------------------
# Post verification failures as a PR comment
# ---------------------------------------------------------------------------

post_verification_comment() {
  local repo="$1"
  local pr_number="$2"
  local round="$3"
  local failures_json="$4"
  local comment_file
  comment_file="$(mktemp "${TMPDIR:-/tmp}/runoq-verify-comment.XXXXXX")"

  {
    printf '<!-- runoq:event:verification-failure -->\n'
    printf '## Verification failure — round %s\n\n' "$round"
    jq -r '.[] | "- " + .' <<< "$failures_json"
  } > "$comment_file"

  "$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$comment_file" >/dev/null 2>&1 || true
  rm -f "$comment_file"
}

# ---------------------------------------------------------------------------
# Append round entry to index.md
# ---------------------------------------------------------------------------

append_index_entry() {
  local log_dir="$1"
  local round="$2"
  local baseline="$3"
  local head_hash="$4"
  local commit_count="$5"
  local commit_subjects_text="$6"
  local verification_result="$7"
  local verification_detail="$8"
  local review_status="$9"
  local score="${10}"
  local verdict="${11}"
  local key_issues="${12}"
  local cumulative_tokens="${13}"

  {
    printf '\n## Round %s\n\n' "$round"
    cat <<ENTRY
- **Commits**: \`${baseline}..${head_hash}\` (${commit_count} commit(s))
${commit_subjects_text}
- **Verification**: ${verification_result}${verification_detail:+ ($verification_detail)}
- **Review**: ${review_status}
- **Score**: ${score}
- **Verdict**: ${verdict}
- **Key issues**: ${key_issues}
- **Cumulative tokens**: ${cumulative_tokens}
ENTRY
  } >> "$log_dir/index.md"
}

# ---------------------------------------------------------------------------
# Build the final output payload
# ---------------------------------------------------------------------------

emit_payload() {
  local status="$1"
  jq -n \
    --arg status "$status" \
    --argjson issueNumber "$issueNumber" \
    --argjson prNumber "$prNumber" \
    --argjson round "$round" \
    --argjson maxRounds "$maxRounds" \
    --arg logDir "$logDir" \
    --arg worktree "$worktree" \
    --arg branch "$branch" \
    --arg baselineHash "$baseline" \
    --arg headHash "$head_hash" \
    --arg commitRange "${baseline}..${head_hash}" \
    --argjson commitSubjects "$commit_subjects_json" \
    --argjson verificationPassed "$verification_passed" \
    --argjson verificationFailures "$verification_failures_json" \
    --arg specRequirements "$spec_requirements" \
    --argjson guidelines "$guidelines_json" \
    --argjson changedFiles "$changed_files_json" \
    --argjson relatedFiles "$related_files_json" \
    --arg previousChecklist "$previousChecklist" \
    --arg reviewLogPath "$logDir/round-${round}-diff-review.md" \
    --argjson cumulativeTokens "$cumulativeTokens" \
    --arg summary "$summary" \
    --argjson caveats "$caveats_json" \
    '{
      status: $status,
      issueNumber: $issueNumber,
      prNumber: $prNumber,
      round: $round,
      maxRounds: $maxRounds,
      logDir: $logDir,
      worktree: $worktree,
      branch: $branch,
      baselineHash: $baselineHash,
      headHash: $headHash,
      commitRange: $commitRange,
      commitSubjects: $commitSubjects,
      verificationPassed: $verificationPassed,
      verificationFailures: $verificationFailures,
      specRequirements: $specRequirements,
      guidelines: $guidelines,
      changedFiles: $changedFiles,
      relatedFiles: $relatedFiles,
      previousChecklist: $previousChecklist,
      reviewLogPath: $reviewLogPath,
      cumulativeTokens: $cumulativeTokens,
      summary: $summary,
      caveats: $caveats
    }'
}

# ---------------------------------------------------------------------------
# Main: run
# ---------------------------------------------------------------------------

run() {
  local payload_file="$1"
  [[ -f "$payload_file" ]] || runoq::die "Payload file not found: $payload_file"

  # -----------------------------------------------------------------------
  # Step 1 — Setup: read payload fields
  # -----------------------------------------------------------------------

  issueNumber="$(jq -r '.issueNumber' "$payload_file")"
  prNumber="$(jq -r '.prNumber' "$payload_file")"
  worktree="$(jq -r '.worktree' "$payload_file")"
  branch="$(jq -r '.branch' "$payload_file")"
  specPath="$(jq -r '.specPath' "$payload_file")"
  repo="$(jq -r '.repo' "$payload_file")"
  maxRounds="$(jq -r '.maxRounds' "$payload_file")"
  maxTokenBudget="$(jq -r '.maxTokenBudget' "$payload_file")"
  guidelines_json="$(jq -c '.guidelines // []' "$payload_file")"
  criteria_commit="$(jq -r '.criteria_commit // "null"' "$payload_file")"

  # Optional resume fields
  local start_round
  start_round="$(jq -r '.round // 1' "$payload_file")"
  round="$start_round"
  logDir="$(jq -r '.logDir // ""' "$payload_file")"
  previousChecklist="$(jq -r '.previousChecklist // "None — first round"' "$payload_file")"
  cumulativeTokens="$(jq -r '.cumulativeTokens // 0' "$payload_file")"

  # Initialize logDir if absent
  if [[ -z "$logDir" || "$logDir" == "null" ]]; then
    logDir="log/issue-${issueNumber}-$(date -u +"%Y-%m-%d-%H%M%S")"
    mkdir -p "$logDir"
    cat > "$logDir/index.md" <<INDEXEOF
# Issue Runner Log

- **Issue**: #${issueNumber}
- **PR**: #${prNumber}
- **Branch**: ${branch}
- **Worktree**: ${worktree}
- **Started**: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
INDEXEOF
  else
    mkdir -p "$logDir"
  fi

  # Read spec (ONLY spec, not source code)
  spec_requirements=""
  if [[ -f "$specPath" ]]; then
    spec_requirements="$(cat "$specPath")"
  fi

  # Initialize shared state for emit_payload
  baseline=""
  head_hash=""
  commit_subjects_json="[]"
  verification_passed="false"
  verification_failures_json="[]"
  changed_files_json="[]"
  related_files_json="[]"
  summary=""
  caveats_json="[]"

  # -----------------------------------------------------------------------
  # Step 2 — Developer loop
  # -----------------------------------------------------------------------

  for (( round = start_round; round <= maxRounds; round++ )); do

    # Budget check before starting a round
    if (( cumulativeTokens >= maxTokenBudget )); then
      summary="Token budget exhausted before round $round"
      caveats_json='["Token budget exhausted"]'
      emit_payload "budget_exhausted"
      return 0
    fi

    # Record baseline
    baseline="$(git -C "$worktree" log -1 --format="%H")"

    # Run codex
    local dev_log="$logDir/round-${round}-dev.md"

    if [[ "$previousChecklist" == "None — first round" ]]; then
      codex_exec exec --dangerously-bypass-approvals-and-sandbox "Implement the following spec. Read the spec file and all AGENTS.md files for rules and constraints.

Spec: ${specPath}

Commit granularity: make one commit per semantic unit of work.
When done, push your branch: git push origin ${branch}

Then print the required final stdout payload block:
<!-- runoq:payload:codex-return -->
\`\`\`json
{ ... }
\`\`\`" >"$dev_log" 2>&1
    else
      codex_exec exec --dangerously-bypass-approvals-and-sandbox "Address the following code review or verification feedback.

Checklist:
${previousChecklist}

Original spec: ${specPath}
Read all AGENTS.md files for rules and constraints.

Commit granularity: make one commit per semantic unit of work.
When done, push your branch: git push origin ${branch}

Then print the required final stdout payload block:
<!-- runoq:payload:codex-return -->
\`\`\`json
{ ... }
\`\`\`" >"$dev_log" 2>&1
    fi

    # Capture new commits
    local commits_text
    commits_text="$(git -C "$worktree" log --reverse --format="%H %s" "${baseline}..HEAD" 2>/dev/null || true)"
    commit_subjects_json="$(printf '%s\n' "$commits_text" | grep -v '^$' | jq -Rsc 'split("\n") | map(select(length > 0))')"
    head_hash="$(git -C "$worktree" log -1 --format="%H")"
    local commit_count
    commit_count="$(printf '%s\n' "$commits_text" | grep -c -v '^$' || true)"

    # Validate payload via state.sh
    local payload_json_file="$logDir/round-${round}-payload.json"
    "$RUNOQ_ROOT/scripts/state.sh" validate-payload "$worktree" "$baseline" "$dev_log" > "$payload_json_file"

    # Inject criteria_commit into payload file if present
    if [[ "$criteria_commit" != "null" ]]; then
      local tmp_payload
      tmp_payload="$(mktemp "${TMPDIR:-/tmp}/runoq-payload-cc.XXXXXX")"
      jq --arg cc "$criteria_commit" '. + {criteria_commit: $cc}' "$payload_json_file" > "$tmp_payload"
      mv "$tmp_payload" "$payload_json_file"
    fi

    # Track tokens
    local round_tokens
    round_tokens="$(extract_tokens_from_log "$dev_log")"
    cumulativeTokens=$(( cumulativeTokens + round_tokens ))

    # Budget check after round
    if (( cumulativeTokens >= maxTokenBudget )); then
      summary="Token budget exhausted after round $round"
      caveats_json='["Token budget exhausted"]'
      emit_payload "budget_exhausted"
      return 0
    fi

    # -----------------------------------------------------------------
    # Step 3 — Verification
    # -----------------------------------------------------------------

    local verify_output
    verify_output="$("$RUNOQ_ROOT/scripts/verify.sh" round "$worktree" "$branch" "$baseline" "$payload_json_file")"

    local review_allowed
    review_allowed="$(printf '%s' "$verify_output" | jq -r '.review_allowed')"
    verification_failures_json="$(printf '%s' "$verify_output" | jq -c '.failures')"

    if [[ "$review_allowed" != "true" ]]; then
      # Verification failed
      verification_passed="false"

      # Post failures as PR comment
      post_verification_comment "$repo" "$prNumber" "$round" "$verification_failures_json"

      # Build commit subjects text for index
      local commit_subjects_text=""
      while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        local sha="${line%% *}"
        local subject="${line#* }"
        commit_subjects_text="${commit_subjects_text}  - \`${sha}\` — ${subject}
"
      done <<< "$commits_text"

      local failure_detail
      failure_detail="$(printf '%s' "$verify_output" | jq -r '.failures | join("; ")')"

      append_index_entry "$logDir" "$round" "$baseline" "$head_hash" \
        "$commit_count" "$commit_subjects_text" \
        "fail" "$failure_detail" \
        "skipped (verification failure)" "n/a" \
        "verification failure" "$failure_detail" \
        "$cumulativeTokens"

      # If max rounds reached, return fail
      if (( round >= maxRounds )); then
        summary="Verification failed after $round rounds"
        caveats_json="$verification_failures_json"
        emit_payload "fail"
        return 0
      fi

      # Feed failures to next round
      previousChecklist="$(printf '%s' "$verify_output" | jq -r '.failures | map("- " + .) | join("\n")')"
      continue
    fi

    # -----------------------------------------------------------------
    # Verification passed — expand review scope
    # -----------------------------------------------------------------

    verification_passed="true"
    verification_failures_json="[]"

    # Get changed files from verification actual field
    local actual_json
    actual_json="$(printf '%s' "$verify_output" | jq -c '.actual')"

    # Combine all file lists into a single changed files array
    changed_files_json="$(printf '%s' "$actual_json" | jq -c '[.files_changed[], .files_added[], .files_deleted[]] | unique')"

    # Expand review scope
    local changed_files_array=()
    while IFS= read -r f; do
      [[ -z "$f" ]] && continue
      changed_files_array+=("$f")
    done < <(printf '%s' "$changed_files_json" | jq -r '.[]')

    if [[ ${#changed_files_array[@]} -gt 0 ]]; then
      related_files_json="$(expand_review_scope "$worktree" "${changed_files_array[@]}")"
    else
      related_files_json="[]"
    fi

    summary="Verification passed on round $round; ready for review"
    caveats_json="[]"
    emit_payload "review_ready"
    return 0

  done

  # Exhausted all rounds without passing verification
  summary="Failed to converge after $maxRounds rounds"
  caveats_json="$verification_failures_json"
  emit_payload "fail"
}

# ---------------------------------------------------------------------------
# Dispatch
# ---------------------------------------------------------------------------

case "${1:-}" in
  run)
    [[ $# -eq 2 ]] || { usage >&2; exit 1; }
    run "$2"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
