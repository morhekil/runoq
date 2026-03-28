#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/smoke-common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/smoke-common.sh"

export RUNOQ_SMOKE_LOG_SCOPE="smoke-planning"

usage() {
  cat <<'EOF'
Usage:
  smoke-planning.sh preflight
  smoke-planning.sh run
  smoke-planning.sh cleanup (--repo OWNER/REPO | --run-id ID | --all)
EOF
}

###############################################################################
# Planning preflight — same as lifecycle but doesn't require codex
###############################################################################

planning_preflight_json() {
  local missing enabled key_path gh_ready owner repo_prefix visibility manifest_path claude_bin gh_bin
  missing='[]'
  enabled=false
  key_path="$(smoke_key_path)"
  owner="$(smoke_repo_owner)"
  repo_prefix="$(smoke_repo_prefix)"
  visibility="$(smoke_repo_visibility)"
  manifest_path="$(smoke_manifest_path)"
  claude_bin="$(smoke_claude_bin)"
  gh_bin="$(smoke_gh_bin)"
  gh_ready=false
  smoke_log "checking planning preflight prerequisites"

  if [[ "${RUNOQ_SMOKE:-0}" == "1" ]]; then
    enabled=true
  else
    missing="$(append_missing "$missing" "Set RUNOQ_SMOKE=1 to enable live GitHub smoke tests.")"
  fi

  if [[ -z "$owner" ]]; then
    missing="$(append_missing "$missing" "Missing RUNOQ_SMOKE_REPO_OWNER.")"
  fi

  if [[ -z "${RUNOQ_SMOKE_APP_ID:-}" ]]; then
    missing="$(append_missing "$missing" "Missing RUNOQ_SMOKE_APP_ID.")"
  fi

  if [[ -z "${RUNOQ_SMOKE_APP_KEY:-}" ]]; then
    missing="$(append_missing "$missing" "Missing RUNOQ_SMOKE_APP_KEY.")"
  fi

  if [[ -n "$key_path" && ! -f "$key_path" ]]; then
    missing="$(append_missing "$missing" "GitHub App key not found: ${key_path}")"
  fi

  if ! command_exists "$gh_bin"; then
    missing="$(append_missing "$missing" "GitHub CLI not found: ${gh_bin}.")"
  fi
  if ! command_exists git; then
    missing="$(append_missing "$missing" "Missing required command: git.")"
  fi
  if ! command_exists jq; then
    missing="$(append_missing "$missing" "Missing required command: jq.")"
  fi
  if ! command_exists "$claude_bin"; then
    missing="$(append_missing "$missing" "Claude CLI not found: ${claude_bin}.")"
  fi

  if command_exists "$gh_bin" && operator_auth_ready; then
    gh_ready=true
  else
    missing="$(append_missing "$missing" "Operator gh auth is not ready. Run gh auth login before planning smoke.")"
  fi

  jq -n \
    --argjson enabled "$enabled" \
    --argjson gh_authenticated "$gh_ready" \
    --arg repo_owner "$owner" \
    --arg repo_prefix "$repo_prefix" \
    --arg visibility "$visibility" \
    --arg key_path "$key_path" \
    --arg manifest_path "$manifest_path" \
    --arg claude_bin "$claude_bin" \
    --arg gh_bin "$gh_bin" \
    --argjson missing "$missing" '
    {
      enabled: $enabled,
      gh_authenticated: $gh_authenticated,
      repo_owner: (if $repo_owner == "" then null else $repo_owner end),
      repo_prefix: $repo_prefix,
      repo_visibility: $visibility,
      key_path: (if $key_path == "" then null else $key_path end),
      manifest_path: $manifest_path,
      claude_bin: $claude_bin,
      gh_bin: $gh_bin,
      missing: $missing,
      ready: ($missing | length == 0)
    }
  '
}

###############################################################################
# Run planning smoke
###############################################################################

run_planning() {
  local root run_id tmpdir target_dir artifacts_dir repo repo_url repo_json
  local plan_log plan_output_file failures_json checks_json summary_json
  local claude_capture_dir claude_wrapper_path real_claude_bin
  root="$(runoq::root)"
  run_id="$(smoke_run_id)"
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/runoq-live-planning.XXXXXX")"
  target_dir="$tmpdir/target"
  artifacts_dir="$(smoke_run_artifacts_dir "$run_id")"
  plan_log="$artifacts_dir/plan.log"
  plan_output_file="$artifacts_dir/plan-output.json"
  failures_json='[]'
  checks_json='[]'

  # shellcheck disable=SC2329
  cleanup() {
    if [[ -n "${tmpdir:-}" ]]; then
      smoke_log "removing temporary planning workspace ${tmpdir}"
      rm -rf "$tmpdir"
    fi
  }
  trap cleanup EXIT

  smoke_log "starting planning smoke run with run_id=${run_id}"
  mkdir -p "$artifacts_dir"

  # Preflight
  local preflight
  preflight="$(planning_preflight_json)"
  if [[ "$(printf '%s' "$preflight" | jq -r '.ready')" != "true" ]]; then
    printf '%s\n' "$preflight" >&2
    runoq::die "Planning smoke preflight failed."
  fi
  smoke_log "planning preflight is ready"

  # Create managed repo
  smoke_log "seeding local target repo into ${target_dir}"
  seed_lifecycle_repo "$target_dir"
  smoke_log "creating managed repo from seeded target"
  repo_json="$(create_managed_repo "$target_dir" "$run_id")"
  repo="$(printf '%s' "$repo_json" | jq -r '.repo')"
  repo_url="$(printf '%s' "$repo_json" | jq -r '.url')"
  smoke_log "created managed repo ${repo}: ${repo_url}"

  # Wait for API propagation
  local _repo_attempt
  for _repo_attempt in 1 2 3 4 5 6 7 8 9 10; do
    if runoq::gh repo view "$repo" --json name >/dev/null 2>&1 \
       && runoq::gh api "repos/${repo}" --jq '.full_name' >/dev/null 2>&1; then
      smoke_log "repo ${repo} confirmed accessible (attempt ${_repo_attempt})"
      break
    fi
    smoke_log "waiting for repo ${repo} API propagation (attempt ${_repo_attempt}/10)"
    sleep 3
  done
  manifest_record_repo "$repo" "$run_id" "$repo_url" "$artifacts_dir"
  checks_json="$(append_check "$checks_json" "managed_repo_created")"
  printf '%s\n' "$repo_json" >"$artifacts_dir/repo.json"

  # Auth and identity
  export RUNOQ_APP_KEY
  RUNOQ_APP_KEY="$(smoke_key_path)"
  export RUNOQ_APP_ID="${RUNOQ_SMOKE_APP_ID}"
  export RUNOQ_SYMLINK_DIR="$tmpdir/bin"
  export TARGET_ROOT="$target_dir"
  write_identity_file "$target_dir"

  # Claude capture wrapper
  claude_capture_dir="$artifacts_dir/claude"
  claude_wrapper_path="$tmpdir/claude-capture"
  real_claude_bin="$(smoke_claude_bin)"
  create_claude_capture_wrapper "$claude_wrapper_path"

  # Run runoq init
  smoke_log "running runoq init"
  local init_exit=0
  (
    cd "$target_dir"
    "$root/bin/runoq" init
  ) >"$artifacts_dir/init.log" 2>&1 || init_exit=$?

  if [[ "$init_exit" -ne 0 ]]; then
    failures_json="$(append_missing "$failures_json" "runoq init failed (exit ${init_exit}).")"
  else
    checks_json="$(append_check "$checks_json" "repo_bootstrapped")"
  fi

  # Run plan decomposition
  local plan_exit=0
  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    local plan_file="$root/test/fixtures/plans/progress-library.md"
    smoke_log "running runoq plan (auto-confirm) against ${plan_file}"
    smoke_log "log -> ${plan_log}"
    set +e
    (
      cd "$target_dir"
      export RUNOQ_AUTO_CONFIRM=1
      export RUNOQ_CLAUDE_BIN="$claude_wrapper_path"
      export RUNOQ_SMOKE_REAL_CLAUDE_BIN="$real_claude_bin"
      export RUNOQ_SMOKE_CLAUDE_CAPTURE_DIR="$claude_capture_dir"
      "$root/scripts/plan.sh" "$repo" "$plan_file" --auto-confirm
    ) >"$plan_output_file" 2>"$plan_log"
    plan_exit="$?"
    set -e
    smoke_log "plan.sh exited with code ${plan_exit}"

    if [[ "$plan_exit" -ne 0 ]]; then
      failures_json="$(append_missing "$failures_json" "plan.sh failed (exit ${plan_exit}). See ${plan_log}.")"
    else
      checks_json="$(append_check "$checks_json" "plan_decomposed")"
    fi
  fi

  # Evaluate results
  local created_issues='[]'
  if [[ -f "$plan_output_file" ]] && jq -e '.issues' "$plan_output_file" >/dev/null 2>&1; then
    created_issues="$(jq '.issues' "$plan_output_file")"
    checks_json="$(append_check "$checks_json" "issues_created")"
  fi

  local epic_count task_count total_count
  total_count="$(printf '%s' "$created_issues" | jq 'length')"
  epic_count="$(printf '%s' "$created_issues" | jq '[.[] | select(.type == "epic")] | length')"
  task_count="$(printf '%s' "$created_issues" | jq '[.[] | select(.type == "task")] | length')"
  smoke_log "created ${total_count} issues: ${epic_count} epics, ${task_count} tasks"

  # Structural assertions
  if [[ "$total_count" -eq 0 ]]; then
    failures_json="$(append_missing "$failures_json" "No issues were created.")"
  fi

  if [[ "$epic_count" -eq 0 ]]; then
    failures_json="$(append_missing "$failures_json" "No epics were created (expected at least 1).")"
  fi

  if [[ "$task_count" -lt 2 ]]; then
    failures_json="$(append_missing "$failures_json" "Expected at least 2 tasks, got ${task_count}.")"
  fi

  # Check that all tasks have complexity_rationale
  local tasks_without_rationale
  tasks_without_rationale="$(printf '%s' "$created_issues" | jq '[.[] | select(.type == "task" and (.complexity_rationale == null or .complexity_rationale == ""))] | length')"
  if [[ "$tasks_without_rationale" -gt 0 ]]; then
    failures_json="$(append_missing "$failures_json" "${tasks_without_rationale} task(s) missing complexity_rationale.")"
  fi

  # Check that tasks with parent_epic_key reference a valid epic
  local orphaned_tasks
  orphaned_tasks="$(printf '%s' "$created_issues" | jq '[.[] | select(.type == "task" and .parent_epic_key != null)] | length')"
  if [[ "$epic_count" -gt 0 && "$orphaned_tasks" -eq 0 && "$task_count" -gt 0 ]]; then
    failures_json="$(append_missing "$failures_json" "Tasks exist but none are linked to an epic.")"
  fi

  # Verify issues actually exist on GitHub
  if [[ "$total_count" -gt 0 ]]; then
    local verified_count=0
    while IFS= read -r issue; do
      [[ -n "$issue" ]] || continue
      local issue_number
      issue_number="$(printf '%s' "$issue" | jq -r '.number')"
      if runoq::gh issue view "$issue_number" --repo "$repo" --json number >/dev/null 2>&1; then
        verified_count=$((verified_count + 1))
      else
        failures_json="$(append_missing "$failures_json" "Issue #${issue_number} not found on GitHub.")"
      fi
    done < <(printf '%s' "$created_issues" | jq -c '.[]')
    smoke_log "verified ${verified_count}/${total_count} issues exist on GitHub"
    if [[ "$verified_count" -eq "$total_count" ]]; then
      checks_json="$(append_check "$checks_json" "issues_verified_on_github")"
    fi
  fi

  # Verify issue metadata on GitHub (complexity_rationale in body)
  if [[ "$task_count" -gt 0 ]]; then
    local rationale_verified=0
    while IFS= read -r issue; do
      [[ -n "$issue" ]] || continue
      local issue_number issue_body
      issue_number="$(printf '%s' "$issue" | jq -r '.number')"
      issue_body="$(runoq::gh issue view "$issue_number" --repo "$repo" --json body | jq -r '.body // ""')"
      if printf '%s' "$issue_body" | grep -q 'complexity_rationale:'; then
        rationale_verified=$((rationale_verified + 1))
      fi
    done < <(printf '%s' "$created_issues" | jq -c '.[] | select(.type == "task")')
    smoke_log "verified complexity_rationale in ${rationale_verified}/${task_count} task issue bodies"
    if [[ "$rationale_verified" -eq "$task_count" ]]; then
      checks_json="$(append_check "$checks_json" "complexity_rationale_in_metadata")"
    else
      failures_json="$(append_missing "$failures_json" "Only ${rationale_verified}/${task_count} tasks have complexity_rationale in issue body metadata.")"
    fi
  fi

  # Build summary
  local status
  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    status="ok"
  else
    status="failed"
  fi

  summary_json="$(jq -n \
    --arg status "$status" \
    --arg mode "planning-eval" \
    --arg repo "$repo" \
    --arg run_id "$run_id" \
    --arg artifacts_dir "$artifacts_dir" \
    --argjson plan_exit "$plan_exit" \
    --argjson checks "$checks_json" \
    --argjson failures "$failures_json" \
    --argjson created_issues "$created_issues" \
    --argjson epic_count "$epic_count" \
    --argjson task_count "$task_count" '{
    status: $status,
    mode: $mode,
    repo: $repo,
    run_id: $run_id,
    artifacts_dir: $artifacts_dir,
    plan_exit_code: $plan_exit,
    checks: $checks,
    failures: $failures,
    planning: {
      total_issues: ($created_issues | length),
      epics: $epic_count,
      tasks: $task_count,
      issues: $created_issues
    }
  }')"

  printf '%s\n' "$summary_json" >"$artifacts_dir/summary.json"
  manifest_update_run_result \
    "$repo" \
    "$status" \
    "$(printf '%s' "$created_issues" | jq '[.[].number]')" \
    "$(printf '%s' "$summary_json" | jq '.failures')"
  smoke_log "planning smoke complete — status: ${status}"

  printf '%s\n' "$summary_json"
}

case "${1:-}" in
  preflight)
    planning_preflight_json
    ;;
  run)
    run_planning
    ;;
  cleanup)
    shift
    cleanup_lifecycle "$@"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
