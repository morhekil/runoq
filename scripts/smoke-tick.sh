#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/smoke-common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/smoke-common.sh"

export RUNOQ_SMOKE_LOG_SCOPE="smoke-tick"

usage() {
  cat <<'EOF'
Usage:
  smoke-tick.sh preflight
  smoke-tick.sh run
  smoke-tick.sh cleanup (--repo OWNER/REPO | --run-id ID | --all)
EOF
}

tick_preflight_json() {
  local missing enabled key_path gh_ready owner repo_prefix visibility manifest_path gh_bin fixture_dir plan_fixture
  missing='[]'
  enabled=false
  key_path="$(smoke_key_path)"
  owner="$(smoke_repo_owner)"
  repo_prefix="$(smoke_repo_prefix)"
  visibility="$(smoke_repo_visibility)"
  manifest_path="$(smoke_manifest_path)"
  gh_bin="$(smoke_gh_bin)"
  fixture_dir="$(runoq::root)/test/fixtures/tick"
  plan_fixture="$(runoq::root)/test/fixtures/plans/progress-library-discovery.md"
  gh_ready=false
  smoke_log "checking tick preflight prerequisites"

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
  if [[ ! -x "$(runoq::root)/test/helpers/fixture-claude" ]]; then
    missing="$(append_missing "$missing" "fixture-claude helper is missing or not executable.")"
  fi
  if [[ ! -d "$fixture_dir" ]]; then
    missing="$(append_missing "$missing" "Tick fixture directory is missing: ${fixture_dir}")"
  fi
  if [[ ! -f "$plan_fixture" ]]; then
    missing="$(append_missing "$missing" "Tick plan fixture is missing: ${plan_fixture}")"
  fi
  if [[ ! -x "$(runoq::root)/scripts/tick.sh" ]]; then
    missing="$(append_missing "$missing" "tick.sh is missing or not executable.")"
  fi

  if command_exists "$gh_bin" && operator_auth_ready; then
    gh_ready=true
  else
    missing="$(append_missing "$missing" "Operator gh auth is not ready. Run gh auth login before tick smoke.")"
  fi

  jq -n \
    --argjson enabled "$enabled" \
    --argjson gh_authenticated "$gh_ready" \
    --arg repo_owner "$owner" \
    --arg repo_prefix "$repo_prefix" \
    --arg visibility "$visibility" \
    --arg key_path "$key_path" \
    --arg manifest_path "$manifest_path" \
    --arg fixture_dir "$fixture_dir" \
    --arg plan_fixture "$plan_fixture" \
    --argjson missing "$missing" '
    {
      enabled: $enabled,
      gh_authenticated: $gh_authenticated,
      repo_owner: (if $repo_owner == "" then null else $repo_owner end),
      repo_prefix: $repo_prefix,
      repo_visibility: $visibility,
      key_path: (if $key_path == "" then null else $key_path end),
      manifest_path: $manifest_path,
      fixture_dir: $fixture_dir,
      plan_fixture: $plan_fixture,
      missing: $missing,
      ready: ($missing | length == 0)
    }
  '
}

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

issue_milestone_type() {
  metadata_value "$1" "milestone_type"
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

find_open_epic_by_title() {
  local issues_json="$1"
  local title="$2"
  while IFS= read -r issue; do
    [[ -n "$issue" ]] || continue
    local body type state issue_title
    body="$(printf '%s' "$issue" | jq -r '.body // ""')"
    type="$(issue_type "$body")"
    state="$(printf '%s' "$issue" | jq -r '.state')"
    issue_title="$(printf '%s' "$issue" | jq -r '.title')"
    if [[ "$type" == "epic" && "$state" == "OPEN" && "$issue_title" == "$title" ]]; then
      printf '%s\n' "$issue"
      return 0
    fi
  done < <(printf '%s' "$issues_json" | jq -c '.[]')
  return 1
}

count_open_epics_excluding_project_planning() {
  local issues_json="$1"
  while IFS= read -r issue; do
    [[ -n "$issue" ]] || continue
    local body type state title
    body="$(printf '%s' "$issue" | jq -r '.body // ""')"
    type="$(issue_type "$body")"
    state="$(printf '%s' "$issue" | jq -r '.state')"
    title="$(printf '%s' "$issue" | jq -r '.title')"
    if [[ "$type" == "epic" && "$state" == "OPEN" && "$title" != "Project Planning" ]]; then
      printf '%s\n' "$issue"
    fi
  done < <(printf '%s' "$issues_json" | jq -c '.[]')
}

issue_view_json() {
  local repo="$1"
  local issue_number="$2"
  runoq::gh issue view "$issue_number" --repo "$repo" --json number,title,body,comments,labels,state,url,assignees
}

issue_assigned_to() {
  local issue_view_json="$1"
  local login="$2"
  printf '%s' "$issue_view_json" | jq -e --arg login "$login" '(.assignees // []) | any(.login == $login)' >/dev/null 2>&1
}

add_check_or_failure() {
  local ok="$1"
  local check="$2"
  local failure="$3"
  if [[ "$ok" == "true" ]]; then
    CHECKS_JSON="$(append_check "$CHECKS_JSON" "$check")"
  else
    FAILURES_JSON="$(append_missing "$FAILURES_JSON" "$failure")"
  fi
}

add_step() {
  local name="$1"
  local output="$2"
  STEPS_JSON="$(jq -n --argjson steps "$STEPS_JSON" --arg name "$name" --arg output "$output" '$steps + [{name:$name, output:$output}]')"
}

tick_once() {
  local root="$1"
  shift
  (
    cd "$TARGET_ROOT"
    "$root/scripts/tick.sh" "$@"
  )
}

create_fake_run_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${RUNOQ_SMOKE_TICK_RUN_LOG:?}"
EOF
  chmod +x "$path"
}

create_fake_dispatch_safety_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${RUNOQ_SMOKE_TICK_DISPATCH_LOG:?}"
EOF
  chmod +x "$path"
}

run_tick_smoke() {
  local root run_id tmpdir target_dir artifacts_dir repo repo_url repo_json summary_json
  local plan_fixture rel_plan_path issues_json project_planning planning_issue planning_number planning_view
  local milestone1 milestone1_number milestone2 milestone2_number milestone1_plan milestone1_plan_number
  local adjustment_issue adjustment_number milestone2_plan milestone2_plan_number discovery_adjustment discovery_adjustment_number
  local operator_login_value
  root="$(runoq::root)"
  run_id="$(smoke_run_id)"
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/runoq-live-tick.XXXXXX")"
  target_dir="$tmpdir/target"
  artifacts_dir="$(smoke_run_artifacts_dir "$run_id")"
  plan_fixture="$root/test/fixtures/plans/progress-library-discovery.md"
  rel_plan_path="docs/progress-library-discovery.md"
  FAILURES_JSON='[]'
  CHECKS_JSON='[]'
  STEPS_JSON='[]'
  local comment_interactions=0 items_rejected=0 discovery_forced_adjustment=false
  operator_login_value="$(operator_login)"
  export RUNOQ_OPERATOR_LOGIN="$operator_login_value"

  cleanup() {
    if [[ -n "${tmpdir:-}" ]]; then
      smoke_log "removing temporary tick workspace ${tmpdir}"
      rm -rf "$tmpdir"
    fi
  }
  trap cleanup EXIT

  mkdir -p "$artifacts_dir"
  local preflight
  preflight="$(tick_preflight_json)"
  if [[ "$(printf '%s' "$preflight" | jq -r '.ready')" != "true" ]]; then
    printf '%s\n' "$preflight" >&2
    runoq::die "Tick smoke preflight failed."
  fi

  smoke_log "starting tick smoke run with run_id=${run_id}"

  seed_lifecycle_repo "$target_dir"
  mkdir -p "$target_dir/docs"
  cp "$plan_fixture" "$target_dir/$rel_plan_path"
  git -C "$target_dir" add "$rel_plan_path"
  git -C "$target_dir" commit -m "Add tick smoke plan fixture" >/dev/null

  repo_json="$(create_managed_repo "$target_dir" "$run_id")"
  repo="$(printf '%s' "$repo_json" | jq -r '.repo')"
  repo_url="$(printf '%s' "$repo_json" | jq -r '.url')"
  manifest_record_repo "$repo" "$run_id" "$repo_url" "$artifacts_dir"
  CHECKS_JSON="$(append_check "$CHECKS_JSON" "managed_repo_created")"
  printf '%s\n' "$repo_json" >"$artifacts_dir/repo.json"

  export RUNOQ_APP_KEY
  RUNOQ_APP_KEY="$(smoke_key_path)"
  export RUNOQ_APP_ID="${RUNOQ_SMOKE_APP_ID}"
  export RUNOQ_SYMLINK_DIR="$tmpdir/bin"
  export TARGET_ROOT="$target_dir"
  export REPO="$repo"
  local fixture_dir
  fixture_dir="$tmpdir/tick-fixtures"
  mkdir -p "$fixture_dir"
  cp -R "$root/test/fixtures/tick/." "$fixture_dir/"
  export RUNOQ_TEST_AGENT_FIXTURE_DIR="$fixture_dir"
  export RUNOQ_TEST_AGENT_STATE_DIR="$tmpdir/agent-state"
  export RUNOQ_CLAUDE_BIN="$root/test/helpers/fixture-claude"
  export FAKE_CLAUDE_LOG="$artifacts_dir/claude.log"
  export RUNOQ_STATE_DIR="$target_dir/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"
  write_identity_file "$target_dir"

  local run_bin dispatch_bin
  run_bin="$tmpdir/fake-run"
  dispatch_bin="$tmpdir/fake-dispatch"
  export RUNOQ_SMOKE_TICK_RUN_LOG="$artifacts_dir/run-dispatch.log"
  export RUNOQ_SMOKE_TICK_DISPATCH_LOG="$artifacts_dir/dispatch-safety.log"
  create_fake_run_bin "$run_bin"
  create_fake_dispatch_safety_bin "$dispatch_bin"
  export RUNOQ_TICK_RUN_SCRIPT="$run_bin"
  export RUNOQ_TICK_DISPATCH_SAFETY_SCRIPT="$dispatch_bin"

  (
    cd "$target_dir"
    "$root/scripts/setup.sh" --plan "$rel_plan_path"
  ) >"$artifacts_dir/init.log" 2>&1
  CHECKS_JSON="$(append_check "$CHECKS_JSON" "repo_bootstrapped")"

  local output

  output="$(tick_once "$root")"
  add_step "bootstrap" "$output"
  project_planning="$(find_issue_by_title "$(list_issues_json "$repo")" "Project Planning")"
  issues_json="$(list_issues_json "$repo")"
  planning_issue="$(find_issue_by_title "$issues_json" "Break plan into milestones")"
  planning_number="$(printf '%s' "$planning_issue" | jq -r '.number')"
  planning_view="$(issue_view_json "$repo" "$planning_number")"
  add_check_or_failure \
    "$(printf '%s' "$planning_view" | jq -e '(.comments // []) | any(.body // "" | contains("runoq:payload:plan-proposal"))' >/dev/null && printf true || printf false)" \
    "bootstrap_proposal_posted" \
    "Bootstrap tick did not post a plan proposal comment."
  add_check_or_failure \
    "$(issue_assigned_to "$planning_view" "$operator_login_value" && printf true || printf false)" \
    "bootstrap_planning_issue_assigned" \
    "Bootstrap planning issue is not assigned to @${operator_login_value}."

  operator_gh issue comment "$planning_number" --repo "$repo" --body \
    "Why is caching before CLI? Also drop item 3, CLI wrapper is out of scope." >/dev/null
  comment_interactions=$((comment_interactions + 1))
  items_rejected=$((items_rejected + 1))

  output="$(tick_once "$root")"
  add_step "planning_comment_response" "$output"
  planning_view="$(issue_view_json "$repo" "$planning_number")"
  add_check_or_failure \
    "$(printf '%s' "$planning_view" | jq -e '(.comments // []) | any(.body // "" | contains("runoq:event"))' >/dev/null && printf true || printf false)" \
    "comment_reply_posted" \
    "Planning comment response was not posted."

  output="$(tick_once "$root")"
  add_step "awaiting_human_decision" "$output"
  add_check_or_failure \
    "$( [[ "$output" == *"Awaiting human decision on #"* ]] && printf true || printf false )" \
    "awaiting_state_observed" \
    "Tick did not report the awaiting-review state."

  operator_gh issue comment "$planning_number" --repo "$repo" --body "OK, approved with item 3 removed" >/dev/null
  runoq::gh issue edit "$planning_number" --repo "$repo" --add-label "$(runoq::config_get '.labels.planApproved')" >/dev/null
  comment_interactions=$((comment_interactions + 1))
  items_rejected=$((items_rejected + 1))

  output="$(tick_once "$root")"
  add_step "materialize_milestones" "$output"
  issues_json="$(list_issues_json "$repo")"
  milestone1="$(find_open_epic_by_title "$issues_json" "Core formatter")"
  milestone2="$(find_open_epic_by_title "$issues_json" "Caching strategy")"
  milestone1_number="$(printf '%s' "$milestone1" | jq -r '.number')"
  milestone2_number="$(printf '%s' "$milestone2" | jq -r '.number')"
  add_check_or_failure \
    "$( [[ -n "$milestone1" && -n "$milestone2" && "$(count_open_epics_excluding_project_planning "$issues_json" | wc -l | tr -d ' ')" == "2" ]] && printf true || printf false )" \
    "approved_milestones_materialized" \
    "Approved planning review did not materialize the expected two milestones."

  milestone1_plan="$(find_open_child_by_type "$issues_json" "$milestone1_number" planning || true)"
  milestone1_plan_number="$(printf '%s' "$milestone1_plan" | jq -r '.number // empty')"
  add_check_or_failure \
    "$( [[ -n "$milestone1_plan_number" ]] && printf true || printf false )" \
    "first_milestone_planning_issue_created" \
    "No planning issue was created under the first milestone."
  if [[ -n "$milestone1_plan_number" ]]; then
    planning_view="$(issue_view_json "$repo" "$milestone1_plan_number")"
    add_check_or_failure \
      "$(issue_assigned_to "$planning_view" "$operator_login_value" && printf true || printf false)" \
      "milestone_planning_issue_assigned" \
      "Milestone planning issue #${milestone1_plan_number} is not assigned to @${operator_login_value}."
  fi

  output="$(tick_once "$root")"
  add_step "milestone1_task_proposal" "$output"
  local milestone1_plan_view
  milestone1_plan_view="$(issue_view_json "$repo" "$milestone1_plan_number")"
  add_check_or_failure \
    "$(printf '%s' "$milestone1_plan_view" | jq -e '(.comments // []) | any(.body // "" | contains("runoq:payload:plan-proposal"))' >/dev/null && printf true || printf false)" \
    "milestone_task_proposal_posted" \
    "Task proposal for milestone 1 was not posted."

  runoq::gh issue edit "$milestone1_plan_number" --repo "$repo" --add-label "$(runoq::config_get '.labels.planApproved')" >/dev/null
  output="$(tick_once "$root")"
  add_step "materialize_tasks" "$output"
  issues_json="$(list_issues_json "$repo")"
  local milestone1_tasks
  milestone1_tasks="$(printf '%s' "$issues_json" | jq -c --argjson parent "$milestone1_number" '
    [.[] | select(.state == "OPEN" and (.body // "" | contains("type: task")) and (.body // "" | contains("parent_epic: " + ($parent|tostring))))]'
  )"
  add_check_or_failure \
    "$(printf '%s' "$milestone1_tasks" | jq -e 'length == 2' >/dev/null && printf true || printf false)" \
    "milestone1_tasks_created" \
    "Expected two task issues under milestone 1."

  output="$(tick_once "$root")"
  add_step "implementation_dispatch" "$output"
  add_check_or_failure \
    "$( [[ -s "$RUNOQ_SMOKE_TICK_RUN_LOG" ]] && printf true || printf false )" \
    "implementation_dispatch_invoked" \
    "Tick did not invoke the implementation dispatch shim."

  while IFS= read -r task_number; do
    [[ -n "$task_number" ]] || continue
    "$root/scripts/gh-issue-queue.sh" set-status "$repo" "$task_number" done >/dev/null
  done < <(printf '%s' "$milestone1_tasks" | jq -r '.[].number')

  rewrite_tick_adjustment_fixture "$fixture_dir" "$milestone2_number"
  output="$(tick_once "$root")"
  add_step "milestone1_adjustment_review" "$output"
  issues_json="$(list_issues_json "$repo")"
  adjustment_issue="$(find_open_child_by_type "$issues_json" "$milestone1_number" adjustment || true)"
  adjustment_number="$(printf '%s' "$adjustment_issue" | jq -r '.number // empty')"
  add_check_or_failure \
    "$( [[ -n "$adjustment_number" ]] && printf true || printf false )" \
    "adjustment_issue_created" \
    "Expected an adjustment issue after milestone 1 review."
  if [[ -n "$adjustment_number" ]]; then
    planning_view="$(issue_view_json "$repo" "$adjustment_number")"
    add_check_or_failure \
      "$(issue_assigned_to "$planning_view" "$operator_login_value" && printf true || printf false)" \
      "adjustment_issue_assigned" \
      "Adjustment issue #${adjustment_number} is not assigned to @${operator_login_value}."
  fi

  operator_gh issue comment "$adjustment_number" --repo "$repo" --body "Approve item 1, reject item 2" >/dev/null
  runoq::gh issue edit "$adjustment_number" --repo "$repo" --add-label "$(runoq::config_get '.labels.planApproved')" >/dev/null
  comment_interactions=$((comment_interactions + 1))
  items_rejected=$((items_rejected + 1))

  output="$(tick_once "$root")"
  add_step "apply_adjustments" "$output"
  issues_json="$(list_issues_json "$repo")"
  milestone2_plan="$(find_open_child_by_type "$issues_json" "$milestone2_number" planning || true)"
  milestone2_plan_number="$(printf '%s' "$milestone2_plan" | jq -r '.number // empty')"
  add_check_or_failure \
    "$( [[ -n "$milestone2_plan_number" ]] && printf true || printf false )" \
    "next_milestone_planning_issue_created" \
    "Expected planning issue under milestone 2 after adjustments."
  if [[ -n "$milestone2_plan_number" ]]; then
    planning_view="$(issue_view_json "$repo" "$milestone2_plan_number")"
    add_check_or_failure \
      "$(issue_assigned_to "$planning_view" "$operator_login_value" && printf true || printf false)" \
      "next_milestone_planning_issue_assigned" \
      "Next milestone planning issue #${milestone2_plan_number} is not assigned to @${operator_login_value}."
  fi

  output="$(tick_once "$root")"
  add_step "discovery_task_proposal" "$output"
  runoq::gh issue edit "$milestone2_plan_number" --repo "$repo" --add-label "$(runoq::config_get '.labels.planApproved')" >/dev/null
  output="$(tick_once "$root")"
  add_step "materialize_discovery_task" "$output"
  issues_json="$(list_issues_json "$repo")"
  local milestone2_tasks
  milestone2_tasks="$(printf '%s' "$issues_json" | jq -c --argjson parent "$milestone2_number" '
    [.[] | select(.state == "OPEN" and (.body // "" | contains("type: task")) and (.body // "" | contains("parent_epic: " + ($parent|tostring))))]'
  )"
  while IFS= read -r task_number; do
    [[ -n "$task_number" ]] || continue
    "$root/scripts/gh-issue-queue.sh" set-status "$repo" "$task_number" done >/dev/null
  done < <(printf '%s' "$milestone2_tasks" | jq -r '.[].number')

  output="$(tick_once "$root")"
  add_step "discovery_forced_adjustment" "$output"
  issues_json="$(list_issues_json "$repo")"
  discovery_adjustment="$(find_open_child_by_type "$issues_json" "$milestone2_number" adjustment || true)"
  discovery_adjustment_number="$(printf '%s' "$discovery_adjustment" | jq -r '.number // empty')"
  if [[ -n "$discovery_adjustment_number" ]]; then
    discovery_forced_adjustment=true
    CHECKS_JSON="$(append_check "$CHECKS_JSON" "discovery_adjustment_created")"
    planning_view="$(issue_view_json "$repo" "$discovery_adjustment_number")"
    add_check_or_failure \
      "$(issue_assigned_to "$planning_view" "$operator_login_value" && printf true || printf false)" \
      "discovery_adjustment_assigned" \
      "Discovery adjustment issue #${discovery_adjustment_number} is not assigned to @${operator_login_value}."
  else
    FAILURES_JSON="$(append_missing "$FAILURES_JSON" "Discovery milestone did not force an adjustment review.")"
  fi

  runoq::gh issue edit "$discovery_adjustment_number" --repo "$repo" --add-label "$(runoq::config_get '.labels.planApproved')" >/dev/null
  output="$(tick_once "$root")"
  add_step "apply_discovery_adjustment" "$output"

  output="$(tick_once "$root")"
  add_step "project_complete" "$output"
  add_check_or_failure \
    "$( [[ "$output" == *"Project complete"* ]] && printf true || printf false )" \
    "project_complete_reported" \
    "Final tick did not report project completion."

  issues_json="$(list_issues_json "$repo")"
  add_check_or_failure \
    "$(printf '%s' "$issues_json" | jq -e '[.[] | select(.state == "OPEN")] | length == 0' >/dev/null && printf true || printf false)" \
    "all_issues_closed" \
    "Expected all issues to be closed at the end of tick smoke."

  local status
  if [[ "$(printf '%s' "$FAILURES_JSON" | jq 'length')" -eq 0 ]]; then
    status="ok"
  else
    status="failed"
  fi

  summary_json="$(jq -n \
    --arg status "$status" \
    --arg mode "tick-fixture" \
    --arg repo "$repo" \
    --arg run_id "$run_id" \
    --arg artifacts_dir "$artifacts_dir" \
    --argjson checks "$CHECKS_JSON" \
    --argjson failures "$FAILURES_JSON" \
    --argjson steps "$STEPS_JSON" \
    --argjson comment_interactions "$comment_interactions" \
    --argjson items_rejected "$items_rejected" \
    --argjson discovery_forced_adjustment "$discovery_forced_adjustment" '
    {
      status: $status,
      mode: $mode,
      repo: $repo,
      run_id: $run_id,
      artifacts_dir: $artifacts_dir,
      checks: $checks,
      failures: $failures,
      steps: $steps,
      comment_interactions: $comment_interactions,
      items_rejected: $items_rejected,
      discovery_forced_adjustment: $discovery_forced_adjustment
    }
  ')"

  printf '%s\n' "$summary_json" >"$artifacts_dir/summary.json"
  manifest_update_run_result \
    "$repo" \
    "$status" \
    "$(printf '%s' "$issues_json" | jq '[.[].number]')" \
    "$(printf '%s' "$summary_json" | jq '.failures')"
  smoke_log "tick smoke complete — status: ${status}"
  printf '%s\n' "$summary_json"
}

case "${1:-}" in
  preflight)
    tick_preflight_json
    ;;
  run)
    run_tick_smoke
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
