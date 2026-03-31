#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RUNOQ_ROOT="${RUNOQ_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
export RUNOQ_ROOT

issue_runner_implementation="${RUNOQ_ISSUE_RUNNER_IMPLEMENTATION:-shell}"
case "$issue_runner_implementation" in
  shell|runtime|"")
    # runtime remains a compatibility alias while issue-runner is shell-owned.
    ;;
  *)
    echo "runoq: Unknown RUNOQ_ISSUE_RUNNER_IMPLEMENTATION: $issue_runner_implementation (expected shell)" >&2
    exit 1
    ;;
esac

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
  runoq::captured_exec codex "$worktree" "$codex_bin" "$@"
}

required_payload_schema_block() {
  cat <<'EOF'
<!-- runoq:payload:codex-return -->
```json
{
  "status": "completed" | "failed" | "stuck",
  "commits_pushed": ["<sha>", "..."],
  "commit_range": "<first-sha>..<last-sha>",
  "files_changed": ["path", "..."],
  "files_added": ["path", "..."],
  "files_deleted": ["path", "..."],
  "tests_run": true | false,
  "tests_passed": true | false,
  "test_summary": "<short summary>",
  "build_passed": true | false,
  "blockers": ["message", "..."],
  "notes": "<short note>"
}
```
EOF
}

build_schema_retry_prompt() {
  local schema_errors_json="$1"
  local errors_text
  errors_text="$(printf '%s' "$schema_errors_json" | jq -r '.[] | "- " + .' 2>/dev/null || true)"
  if [[ -z "$errors_text" ]]; then
    errors_text="- payload_missing_or_malformed"
  fi

  cat <<EOF
Your last payload block did not satisfy the required payload schema.

Detected schema errors:
${errors_text}

Return ONLY a corrected payload block using this exact schema (verbatim):
$(required_payload_schema_block)

Do not run additional commands. Re-emit only the corrected final payload block with strict JSON types.
EOF
}

extract_thread_id_from_events() {
  local event_log_file="$1"
  [[ -f "$event_log_file" ]] || return 0
  jq -Rsr '
    split("\n")
    | map((try fromjson catch empty))
    | map(
        select((.type // .event // "") == "thread.started")
        | (.thread_id // .thread.id // empty)
      )
    | map(select(type == "string" and length > 0))
    | last // empty
  ' <"$event_log_file" 2>/dev/null || true
}

inject_thread_id_into_payload_file() {
  local payload_json_file="$1"
  local thread_id="$2"
  [[ -n "$thread_id" ]] || return 0
  local tmp_payload
  tmp_payload="$(mktemp "${TMPDIR:-/tmp}/runoq-payload-thread.XXXXXX")"
  jq --arg thread_id "$thread_id" '. + {thread_id: $thread_id}' "$payload_json_file" >"$tmp_payload"
  mv "$tmp_payload" "$payload_json_file"
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
  local round_baseline="$5"
  local head_hash="$6"
  local round_commits_text="$7"
  local comment_file
  comment_file="$(mktemp "${TMPDIR:-/tmp}/runoq-verify-comment.XXXXXX")"

  local failure_count
  failure_count="$(jq 'length' <<< "$failures_json")"

  {
    printf '<!-- runoq:event:verification-failure -->\n'
    printf '## Verification failure — round %s\n\n' "$round"
    printf '> Posted by `issue-runner` / `verify.sh` — round %s of %s, branch `%s`\n\n' "$round" "$maxRounds" "$branch"
    printf '**Commit range**: `%s..%s`\n' "${round_baseline:0:7}" "${head_hash:0:7}"
    if [[ -n "$round_commits_text" ]]; then
      printf '\n**Commits this round**:\n'
      while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        local sha="${line%% *}"
        local subject="${line#* }"
        printf -- '- `%s` %s\n' "${sha:0:7}" "$subject"
      done <<< "$round_commits_text"
    else
      printf '\n**Commits this round**: none\n'
    fi
    printf '\n### Failures (%s)\n\n' "$failure_count"
    jq -r '.[] | "- " + .' <<< "$failures_json"
    printf '\n---\n_This is an automated verification check. The developer agent will attempt to fix these issues in the next round._\n'
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

  # Record the initial baseline once — verification always checks full diff
  local initial_baseline
  initial_baseline="$(git -C "$worktree" log -1 --format="%H")"
  baseline="$initial_baseline"

  # -----------------------------------------------------------------------
  # Step 2 — Developer loop
  # -----------------------------------------------------------------------

  for (( round = start_round; round <= maxRounds; round++ )); do

    runoq::log "issue-runner" "round ${round}/${maxRounds}: baseline=$(git -C "$worktree" log -1 --format="%H") budget=${cumulativeTokens}/${maxTokenBudget}"

    # Budget check before starting a round
    if (( cumulativeTokens >= maxTokenBudget )); then
      summary="Token budget exhausted before round $round"
      caveats_json='["Token budget exhausted"]'
      emit_payload "budget_exhausted"
      return 0
    fi

    # Per-round baseline for logging which commits were added this round
    local round_baseline
    round_baseline="$(git -C "$worktree" log -1 --format="%H")"

    # Build protected files warning if criteria_commit is set
    local protected_files_warning=""
    if [[ "$criteria_commit" != "null" && -n "$criteria_commit" ]]; then
      local criteria_files
      criteria_files="$(git -C "$worktree" diff-tree --no-commit-id --name-only -r "$criteria_commit" 2>/dev/null || true)"
      if [[ -n "$criteria_files" ]]; then
        protected_files_warning="
IMPORTANT: The following files are acceptance criteria set by the bar-setter and MUST NOT be modified. They are read-only. Your implementation must satisfy the tests in these files without changing them:
${criteria_files}
"
      fi
    fi

    # Run codex (event stream + final assistant message in separate artifacts)
    local codex_capture_dir="$logDir/codex-round-${round}"
    local event_log_file="$logDir/round-${round}-codex-events.jsonl"
    local last_message_file="$logDir/round-${round}-last-message.md"
    local last_message_file_abs
    last_message_file_abs="$(runoq::absolute_path "$last_message_file")"
    local thread_id_file="$logDir/round-${round}-thread-id.txt"
    local thread_id=""
    local round_tokens=0
    local max_schema_retries=2
    local schema_retry_count=0
    local payload_json_file="$logDir/round-${round}-payload.json"
    local payload_schema_valid
    local payload_schema_errors_json

    local codex_prompt
    if [[ "$previousChecklist" == "None — first round" ]]; then
      runoq::log "issue-runner" "round ${round}: invoking codex (first round — implement spec)"
      codex_prompt="$(cat <<EOF
Implement the following spec. Read the spec file and all AGENTS.md files for rules and constraints.

Spec: ${specPath}
${protected_files_warning}
Commit granularity: make one commit per semantic unit of work.
When done, push your branch: git push origin ${branch}

Then print the required final stdout payload block:
$(required_payload_schema_block)
EOF
)"
    else
      runoq::log "issue-runner" "round ${round}: invoking codex (subsequent round — address feedback)"
      codex_prompt="$(cat <<EOF
Address the following code review or verification feedback.

Checklist:
${previousChecklist}

Original spec: ${specPath}
Read all AGENTS.md files for rules and constraints.
${protected_files_warning}
Commit granularity: make one commit per semantic unit of work.
When done, push your branch: git push origin ${branch}

Then print the required final stdout payload block:
$(required_payload_schema_block)
EOF
)"
    fi

    RUNOQ_CODEX_CAPTURE_DIR="$codex_capture_dir" \
      codex_exec exec --dangerously-bypass-approvals-and-sandbox --json -o "$last_message_file_abs" "$codex_prompt" >"$event_log_file" 2>&1

    thread_id="$(extract_thread_id_from_events "$event_log_file")"
    printf '%s\n' "$thread_id" >"$thread_id_file"

    "$RUNOQ_ROOT/scripts/state.sh" validate-payload "$worktree" "$baseline" "$last_message_file_abs" >"$payload_json_file"
    inject_thread_id_into_payload_file "$payload_json_file" "$thread_id"

    payload_schema_valid="$(jq -r '.payload_schema_valid // false' "$payload_json_file")"
    payload_schema_errors_json="$(jq -c '.payload_schema_errors // []' "$payload_json_file")"
    round_tokens=$(( round_tokens + $(extract_tokens_from_log "$event_log_file") ))

    while [[ "$payload_schema_valid" != "true" && -n "$thread_id" && "$schema_retry_count" -lt "$max_schema_retries" ]]; do
      schema_retry_count=$((schema_retry_count + 1))
      local retry_event_log_file="$logDir/round-${round}-schema-retry-${schema_retry_count}-events.jsonl"
      local retry_last_message_file="$logDir/round-${round}-schema-retry-${schema_retry_count}-last-message.md"
      local retry_last_message_file_abs
      retry_last_message_file_abs="$(runoq::absolute_path "$retry_last_message_file")"
      local retry_prompt
      retry_prompt="$(build_schema_retry_prompt "$payload_schema_errors_json")"

      runoq::log "issue-runner" "round ${round}: schema retry ${schema_retry_count}/${max_schema_retries} on thread ${thread_id}"
      RUNOQ_CODEX_CAPTURE_DIR="$codex_capture_dir/schema-retry-${schema_retry_count}" \
        codex_exec exec resume "$thread_id" --json -o "$retry_last_message_file_abs" "$retry_prompt" >"$retry_event_log_file" 2>&1

      local resumed_thread_id
      resumed_thread_id="$(extract_thread_id_from_events "$retry_event_log_file")"
      if [[ -n "$resumed_thread_id" ]]; then
        thread_id="$resumed_thread_id"
      fi
      printf '%s\n' "$thread_id" >"$thread_id_file"

      "$RUNOQ_ROOT/scripts/state.sh" validate-payload "$worktree" "$baseline" "$retry_last_message_file_abs" >"$payload_json_file"
      inject_thread_id_into_payload_file "$payload_json_file" "$thread_id"

      payload_schema_valid="$(jq -r '.payload_schema_valid // false' "$payload_json_file")"
      payload_schema_errors_json="$(jq -c '.payload_schema_errors // []' "$payload_json_file")"
      round_tokens=$(( round_tokens + $(extract_tokens_from_log "$retry_event_log_file") ))
    done

    # Capture new commits — full diff for emit_payload, per-round for index logging
    local commits_text round_commits_text
    commits_text="$(git -C "$worktree" log --reverse --format="%H %s" "${baseline}..HEAD" 2>/dev/null || true)"
    round_commits_text="$(git -C "$worktree" log --reverse --format="%H %s" "${round_baseline}..HEAD" 2>/dev/null || true)"
    commit_subjects_json="$(printf '%s\n' "$commits_text" | { grep -v '^$' || true; } | jq -Rsc 'split("\n") | map(select(length > 0))')"
    head_hash="$(git -C "$worktree" log -1 --format="%H")"
    local commit_count
    commit_count="$(printf '%s\n' "$round_commits_text" | grep -c -v '^$' || true)"
    runoq::log "issue-runner" "round ${round}: after codex — commit_count=${commit_count} head=${head_hash}"

    # Inject criteria_commit into payload file if present
    if [[ "$criteria_commit" != "null" ]]; then
      local tmp_payload
      tmp_payload="$(mktemp "${TMPDIR:-/tmp}/runoq-payload-cc.XXXXXX")"
      jq --arg cc "$criteria_commit" '. + {criteria_commit: $cc}' "$payload_json_file" > "$tmp_payload"
      mv "$tmp_payload" "$payload_json_file"
    fi

    # Track tokens
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
    if [[ "$payload_schema_valid" != "true" ]]; then
      local schema_failure_reason
      if [[ -n "$thread_id" ]]; then
        schema_failure_reason="codex payload schema invalid after ${schema_retry_count} resume attempt(s)"
      else
        schema_failure_reason="codex payload schema invalid and thread_id missing from codex events"
      fi
      verify_output="$(jq -n \
        --arg reason "$schema_failure_reason" \
        --argjson schema_errors "$payload_schema_errors_json" \
        '{
          review_allowed: false,
          failures: ([$reason] + ($schema_errors | map("payload schema error: " + .))),
          actual: {
            commits_pushed: [],
            commit_range: "",
            files_changed: [],
            files_added: [],
            files_deleted: []
          }
        }')"
      runoq::log "issue-runner" "round ${round}: schema validation failed before verification"
    else
      runoq::log "issue-runner" "round ${round}: running verification"
      verify_output="$("$RUNOQ_ROOT/scripts/verify.sh" round "$worktree" "$branch" "$baseline" "$payload_json_file")"
    fi

    local review_allowed
    review_allowed="$(printf '%s' "$verify_output" | jq -r '.review_allowed')"
    verification_failures_json="$(printf '%s' "$verify_output" | jq -c '.failures')"
    local verify_failure_count
    verify_failure_count="$(printf '%s' "$verify_output" | jq -r '.failures | length')"
    runoq::log "issue-runner" "round ${round}: verification result — review_allowed=${review_allowed} failure_count=${verify_failure_count}"

    if [[ "$review_allowed" != "true" ]]; then
      # Verification failed
      verification_passed="false"

      # Post failures as PR comment with full context
      post_verification_comment "$repo" "$prNumber" "$round" "$verification_failures_json" \
        "$round_baseline" "$head_hash" "$round_commits_text"

      # Build commit subjects text for index (per-round commits only)
      local commit_subjects_text=""
      while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        local sha="${line%% *}"
        local subject="${line#* }"
        commit_subjects_text="${commit_subjects_text}  - \`${sha}\` — ${subject}
"
      done <<< "$round_commits_text"

      local failure_detail
      failure_detail="$(printf '%s' "$verify_output" | jq -r '.failures | join("; ")')"

      append_index_entry "$logDir" "$round" "$round_baseline" "$head_hash" \
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
      runoq::log "issue-runner" "round ${round}: verification failed — continuing to next round"
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
    runoq::log "issue-runner" "round ${round}: verification passed — returning review_ready"
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
