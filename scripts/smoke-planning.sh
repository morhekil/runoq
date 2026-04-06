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

  if [[ -z "${RUNOQ_SMOKE_INSTALLATION_ID:-}" ]]; then
    missing="$(append_missing "$missing" "Missing RUNOQ_SMOKE_INSTALLATION_ID.")"
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
# Tick-planning helpers
###############################################################################

metadata_value() {
  local body="$1"
  local key="$2"
  printf '%s\n' "$body" | awk -v key="$key" '
    /<!-- runoq:meta/ {in_meta=1; next}
    in_meta && /-->/ {exit}
    in_meta && index($0, key ":") == 1 {
      sub("^" key ":[[:space:]]*", "", $0)
      print $0
      exit
    }
  '
}

issue_type() {
  local body="$1"
  local value
  value="$(metadata_value "$body" "type")"
  printf '%s\n' "${value:-task}"
}

issue_parent_epic() {
  metadata_value "$1" "parent_epic"
}

list_issues_json() {
  local repo="$1"
  runoq::gh issue list --repo "$repo" --state all --limit 200 --json number,title,body,labels,state,url
}

find_issue_by_title() {
  local issues_json="$1"
  local title="$2"
  printf '%s' "$issues_json" | jq -c --arg title "$title" '.[] | select(.title == $title) | .'
}

find_open_child_by_type() {
  local issues_json="$1"
  local parent_epic="$2"
  local wanted_type="$3"
  while IFS= read -r issue; do
    [[ -n "$issue" ]] || continue
    local body parent type state
    body="$(printf '%s' "$issue" | jq -r '.body // ""')"
    parent="$(issue_parent_epic "$body")"
    type="$(issue_type "$body")"
    state="$(printf '%s' "$issue" | jq -r '.state')"
    if [[ "$parent" == "$parent_epic" && "$type" == "$wanted_type" && "$state" == "OPEN" ]]; then
      printf '%s\n' "$issue"
      return 0
    fi
  done < <(printf '%s' "$issues_json" | jq -c '.[]')
  return 1
}

issue_view_json() {
  local repo="$1"
  local issue_number="$2"
  runoq::gh issue view "$issue_number" --repo "$repo" --json number,title,body,comments,labels,state,url
}

extract_marked_json_from_text() {
  local text="$1"
  local marker="$2"
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/runoq-smoke-plan.XXXXXX")"
  printf '%s' "$text" >"$tmp"
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
    in_block {
      block = block $0 "\n"
    }
  ' "$tmp"
}

proposal_json_from_issue_view() {
  local issue_view_json="$1"
  local proposal body
  proposal="$(printf '%s' "$issue_view_json" | jq -r '
    .comments // []
    | map(.body // "")
    | map(select(contains("runoq:payload:plan-proposal")))
    | last // empty
  ')"
  [[ -n "$proposal" ]] || return 1
  body="$(extract_marked_json_from_text "$proposal" 'runoq:payload:plan-proposal')"
  [[ -n "$body" ]] || return 1
  printf '%s\n' "$body"
}

###############################################################################
# Run planning smoke
###############################################################################

run_planning() {
  local root run_id tmpdir target_dir artifacts_dir repo repo_url repo_json
  local failures_json checks_json summary_json
  local claude_capture_dir claude_wrapper_path real_claude_bin
  local plan_fixture rel_plan_path
  root="$(runoq::root)"
  run_id="$(smoke_run_id)"
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/runoq-live-planning.XXXXXX")"
  target_dir="$tmpdir/target"
  artifacts_dir="$(smoke_run_artifacts_dir "$run_id")"
  failures_json='[]'
  checks_json='[]'
  plan_fixture="$root/test/fixtures/plans/progress-library-discovery.md"
  rel_plan_path="docs/progress-library-discovery.md"

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
  mkdir -p "$target_dir/docs"
  cp "$plan_fixture" "$target_dir/$rel_plan_path"
  git -C "$target_dir" add "$rel_plan_path"
  git -C "$target_dir" commit -m "Add planning smoke discovery fixture" >/dev/null
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
  export RUNOQ_CLAUDE_BIN="$claude_wrapper_path"
  export RUNOQ_SMOKE_REAL_CLAUDE_BIN="$real_claude_bin"
  export RUNOQ_SMOKE_CLAUDE_CAPTURE_DIR="$claude_capture_dir"

  # Run runoq init
  smoke_log "running runoq init"
  local init_exit=0
  (
    cd "$target_dir"
    "$root/scripts/setup.sh" --plan "$rel_plan_path"
  ) >"$artifacts_dir/init.log" 2>&1 || init_exit=$?

  if [[ "$init_exit" -ne 0 ]]; then
    failures_json="$(append_missing "$failures_json" "runoq init failed (exit ${init_exit}).")"
  else
    checks_json="$(append_check "$checks_json" "repo_bootstrapped")"
  fi

  local output issues_json planning_issue planning_number planning_view proposal_json milestone1 milestone1_number
  local milestone1_plan milestone1_plan_number milestone1_plan_view created_tasks created_issues task_count milestone_count has_discovery_milestone comment_interactions
  created_issues='[]'
  comment_interactions=0

  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    smoke_log "running tick bootstrap"
    output="$(
      cd "$target_dir"
      "$root/scripts/tick.sh"
    )"
    printf '%s\n' "$output" >"$artifacts_dir/tick-bootstrap.log"

    issues_json="$(list_issues_json "$repo")"
    planning_issue="$(find_issue_by_title "$issues_json" "Break plan into milestones")"
    planning_number="$(printf '%s' "$planning_issue" | jq -r '.number // empty')"
    if [[ -z "$planning_number" ]]; then
      failures_json="$(append_missing "$failures_json" "Bootstrap tick did not create the initial planning issue.")"
    else
      checks_json="$(append_check "$checks_json" "bootstrap_planning_issue_created")"
      planning_view="$(issue_view_json "$repo" "$planning_number")"
      proposal_json="$(proposal_json_from_issue_view "$planning_view" || true)"
      if [[ -z "$proposal_json" ]]; then
        failures_json="$(append_missing "$failures_json" "Bootstrap tick did not post a parseable milestone proposal.")"
      else
        checks_json="$(append_check "$checks_json" "bootstrap_proposal_posted")"
      fi
    fi
  fi

  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    smoke_log "injecting planning question comment"
    runoq::gh issue comment "$planning_number" --repo "$repo" --body "Why this milestone order?" >/dev/null
    comment_interactions=$((comment_interactions + 1))
    output="$(
      cd "$target_dir"
      "$root/scripts/tick.sh"
    )"
    printf '%s\n' "$output" >"$artifacts_dir/tick-comment.log"
    planning_view="$(issue_view_json "$repo" "$planning_number")"
    if printf '%s' "$planning_view" | jq -e '(.comments // []) | any(.body // "" | contains("runoq:event"))' >/dev/null; then
      checks_json="$(append_check "$checks_json" "planning_comment_answered")"
    else
      failures_json="$(append_missing "$failures_json" "Tick did not answer the planning question with a runoq:event comment.")"
    fi
  fi

  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    runoq::gh issue edit "$planning_number" --repo "$repo" --add-label "$(runoq::config_get '.labels.planApproved')" >/dev/null
    output="$(
      cd "$target_dir"
      "$root/scripts/tick.sh"
    )"
    printf '%s\n' "$output" >"$artifacts_dir/tick-approve-milestones.log"
    issues_json="$(list_issues_json "$repo")"
    milestone1="$(printf '%s' "$issues_json" | jq -c '
      .[] | select(.state == "OPEN" and .title != "Project Planning" and (.body // "" | contains("type: epic")))
    ' | head -n 1)"
    milestone1_number="$(printf '%s' "$milestone1" | jq -r '.number // empty')"
    milestone_count="$(printf '%s' "$issues_json" | jq '[.[] | select(.state == "OPEN" and .title != "Project Planning" and (.body // "" | contains("type: epic")))] | length')"
    has_discovery_milestone="$(printf '%s' "$issues_json" | jq '[.[] | select(.state == "OPEN" and (.body // "" | contains("milestone_type: discovery")))] | length > 0')"
    if [[ -z "$milestone1_number" || "$milestone_count" -lt 1 ]]; then
      failures_json="$(append_missing "$failures_json" "Approved milestone review did not materialize milestone epics.")"
    else
      checks_json="$(append_check "$checks_json" "milestones_materialized")"
    fi
  else
    milestone_count=0
    has_discovery_milestone=false
  fi

  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    milestone1_plan="$(find_open_child_by_type "$issues_json" "$milestone1_number" planning || true)"
    milestone1_plan_number="$(printf '%s' "$milestone1_plan" | jq -r '.number // empty')"
    if [[ -z "$milestone1_plan_number" ]]; then
      failures_json="$(append_missing "$failures_json" "No planning issue exists under the first milestone.")"
    else
      output="$(
        cd "$target_dir"
        "$root/scripts/tick.sh"
      )"
      printf '%s\n' "$output" >"$artifacts_dir/tick-task-proposal.log"
      milestone1_plan_view="$(issue_view_json "$repo" "$milestone1_plan_number")"
      proposal_json="$(proposal_json_from_issue_view "$milestone1_plan_view" || true)"
      if [[ -z "$proposal_json" ]]; then
        failures_json="$(append_missing "$failures_json" "Tick did not post a task proposal for the first milestone.")"
      else
        checks_json="$(append_check "$checks_json" "task_proposal_posted")"
      fi
    fi
  fi

  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    runoq::gh issue edit "$milestone1_plan_number" --repo "$repo" --add-label "$(runoq::config_get '.labels.planApproved')" >/dev/null
    output="$(
      cd "$target_dir"
      "$root/scripts/tick.sh"
    )"
    printf '%s\n' "$output" >"$artifacts_dir/tick-approve-tasks.log"
    issues_json="$(list_issues_json "$repo")"
    created_tasks="$(printf '%s' "$issues_json" | jq -c --argjson parent "$milestone1_number" '
      [.[] | select(.state == "OPEN" and (.body // "" | contains("type: task")) and (.body // "" | contains("parent_epic: " + ($parent|tostring))))]
    ')"
    task_count="$(printf '%s' "$created_tasks" | jq 'length')"
    if [[ "$task_count" -lt 1 ]]; then
      failures_json="$(append_missing "$failures_json" "Approved task proposal did not create task issues.")"
    else
      checks_json="$(append_check "$checks_json" "tasks_materialized")"
    fi
  else
    task_count=0
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
    --argjson checks "$checks_json" \
    --argjson failures "$failures_json" \
    --argjson milestone_count "$milestone_count" \
    --argjson task_count "$task_count" \
    --argjson has_discovery_milestone "$has_discovery_milestone" \
    --argjson comment_interactions "$comment_interactions" '{
    status: $status,
    mode: $mode,
    repo: $repo,
    run_id: $run_id,
    artifacts_dir: $artifacts_dir,
    checks: $checks,
    failures: $failures,
    comment_interactions: $comment_interactions,
    planning: {
      milestones: $milestone_count,
      tasks: $task_count,
      has_discovery_milestone: $has_discovery_milestone
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
