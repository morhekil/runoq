#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

smoke_key_path() {
  local key_path="${AGENDEV_SMOKE_APP_KEY:-}"
  key_path="${key_path/#\~/$HOME}"
  printf '%s\n' "$key_path"
}

smoke_run_id() {
  local run_id="${AGENDEV_SMOKE_RUN_ID:-}"
  if [[ -z "$run_id" ]]; then
    run_id="$(date -u +%Y%m%d%H%M%S)-$RANDOM"
  fi
  printf '%s\n' "$(agendev::branch_slug "$run_id")"
}

smoke_repo_owner() {
  printf '%s\n' "${AGENDEV_SMOKE_REPO_OWNER:-}"
}

smoke_repo_prefix() {
  printf '%s\n' "${AGENDEV_SMOKE_REPO_PREFIX:-agendev-live-eval}"
}

smoke_repo_visibility() {
  printf '%s\n' "${AGENDEV_SMOKE_REPO_VISIBILITY:-private}"
}

smoke_manifest_path() {
  printf '%s\n' "${AGENDEV_SMOKE_MANIFEST_PATH:-$(agendev::root)/.agendev/live-smoke/managed-repos.json}"
}

smoke_runs_root() {
  printf '%s\n' "${AGENDEV_SMOKE_RUNS_DIR:-$(agendev::root)/.agendev/live-smoke/runs}"
}

smoke_run_artifacts_dir() {
  local run_id="$1"
  printf '%s/%s\n' "$(smoke_runs_root)" "$run_id"
}

json_append_string() {
  local array_json="$1"
  local value="$2"
  jq -n --argjson array "$array_json" --arg value "$value" '$array + [$value]'
}

append_missing() {
  local missing_json="$1"
  local message="$2"
  json_append_string "$missing_json" "$message"
}

append_check() {
  local checks_json="$1"
  local check="$2"
  json_append_string "$checks_json" "$check"
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

smoke_verbose_enabled() {
  case "${AGENDEV_SMOKE_VERBOSE:-auto}" in
    1|true|TRUE|yes|YES|on|ON)
      return 0
      ;;
    0|false|FALSE|no|NO|off|OFF)
      return 1
      ;;
  esac
  [[ -t 2 ]]
}

smoke_log() {
  smoke_verbose_enabled || return 0
  local scope="${AGENDEV_SMOKE_LOG_SCOPE:-smoke}"
  printf '[%s] %s\n' "$scope" "$*" >&2
}

smoke_gh_bin() {
  printf '%s\n' "${GH_BIN:-gh}"
}

smoke_claude_bin() {
  printf '%s\n' "${AGENDEV_CLAUDE_BIN:-claude}"
}

smoke_codex_bin() {
  printf '%s\n' "${AGENDEV_SMOKE_CODEX_BIN:-codex}"
}

operator_auth_ready() {
  agendev::gh auth status >/dev/null 2>&1
}

ensure_smoke_manifest() {
  local manifest
  manifest="$(smoke_manifest_path)"
  mkdir -p "$(dirname "$manifest")"
  if [[ ! -f "$manifest" ]]; then
    printf '[]\n' >"$manifest"
  fi
  jq -e '.' "$manifest" >/dev/null 2>&1 || printf '[]\n' >"$manifest"
}

manifest_record_repo() {
  local repo="$1"
  local run_id="$2"
  local url="$3"
  local artifacts_dir="$4"
  local manifest now tmp
  manifest="$(smoke_manifest_path)"
  ensure_smoke_manifest
  now="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  tmp="$(mktemp "$(dirname "$manifest")/.manifest.XXXXXX")"
  jq \
    --arg repo "$repo" \
    --arg run_id "$run_id" \
    --arg url "$url" \
    --arg artifacts_dir "$artifacts_dir" \
    --arg created_at "$now" '
    (map(select(.repo != $repo))) + [
      {
        repo: $repo,
        run_id: $run_id,
        url: (if $url == "" then null else $url end),
        artifacts_dir: $artifacts_dir,
        created_at: $created_at,
        deleted_at: null,
        cleanup_state: "active",
        lifecycle_status: null,
        issue_numbers: [],
        failures: []
      }
    ]
  ' "$manifest" >"$tmp"
  mv "$tmp" "$manifest"
}

manifest_update_run_result() {
  local repo="$1"
  local lifecycle_status="$2"
  local issue_numbers_json="$3"
  local failures_json="$4"
  local manifest now tmp
  manifest="$(smoke_manifest_path)"
  ensure_smoke_manifest
  now="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  tmp="$(mktemp "$(dirname "$manifest")/.manifest.XXXXXX")"
  jq \
    --arg repo "$repo" \
    --arg lifecycle_status "$lifecycle_status" \
    --arg updated_at "$now" \
    --argjson issue_numbers "$issue_numbers_json" \
    --argjson failures "$failures_json" '
    map(
      if .repo == $repo then
        . + {
          lifecycle_status: $lifecycle_status,
          issue_numbers: $issue_numbers,
          failures: $failures,
          updated_at: $updated_at
        }
      else
        .
      end
    )
  ' "$manifest" >"$tmp"
  mv "$tmp" "$manifest"
}

manifest_mark_deleted() {
  local repo="$1"
  local manifest now tmp
  manifest="$(smoke_manifest_path)"
  ensure_smoke_manifest
  now="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  tmp="$(mktemp "$(dirname "$manifest")/.manifest.XXXXXX")"
  jq \
    --arg repo "$repo" \
    --arg deleted_at "$now" '
    map(
      if .repo == $repo then
        . + {
          deleted_at: $deleted_at,
          cleanup_state: "deleted"
        }
      else
        .
      end
    )
  ' "$manifest" >"$tmp"
  mv "$tmp" "$manifest"
}

manifest_mark_cleanup_failure() {
  local repo="$1"
  local message="$2"
  local manifest now tmp
  manifest="$(smoke_manifest_path)"
  ensure_smoke_manifest
  now="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  tmp="$(mktemp "$(dirname "$manifest")/.manifest.XXXXXX")"
  jq \
    --arg repo "$repo" \
    --arg updated_at "$now" \
    --arg message "$message" '
    map(
      if .repo == $repo then
        . + {
          cleanup_state: "delete-failed",
          updated_at: $updated_at,
          last_cleanup_error: $message
        }
      else
        .
      end
    )
  ' "$manifest" >"$tmp"
  mv "$tmp" "$manifest"
}

manifest_select_entries() {
  local selector_type="$1"
  local selector_value="${2:-}"
  local manifest
  manifest="$(smoke_manifest_path)"
  ensure_smoke_manifest
  case "$selector_type" in
    repo)
      jq --arg repo "$selector_value" '[.[] | select(.cleanup_state != "deleted" and .repo == $repo)]' "$manifest"
      ;;
    run-id)
      jq --arg run_id "$selector_value" '[.[] | select(.cleanup_state != "deleted" and .run_id == $run_id)]' "$manifest"
      ;;
    all)
      jq '[.[] | select(.cleanup_state != "deleted")]' "$manifest"
      ;;
    *)
      agendev::die "Unknown manifest selector: $selector_type"
      ;;
  esac
}

preflight_json() {
  local missing enabled key_path
  missing='[]'
  enabled=false
  key_path="$(smoke_key_path)"
  smoke_log "checking sandbox preflight prerequisites"

  if [[ "${AGENDEV_SMOKE:-0}" == "1" ]]; then
    enabled=true
  else
    missing="$(append_missing "$missing" "Set AGENDEV_SMOKE=1 to enable live GitHub smoke tests.")"
  fi

  for required in \
    AGENDEV_SMOKE_REPO \
    AGENDEV_SMOKE_APP_ID \
    AGENDEV_SMOKE_INSTALLATION_ID \
    AGENDEV_SMOKE_APP_KEY \
    AGENDEV_SMOKE_PERMISSION_USER; do
    if [[ -z "${!required:-}" ]]; then
      missing="$(append_missing "$missing" "Missing ${required}.")"
    fi
  done

  if [[ -n "$key_path" && ! -f "$key_path" ]]; then
    missing="$(append_missing "$missing" "GitHub App key not found: ${key_path}")"
  fi

  if [[ "$(printf '%s' "$missing" | jq 'length')" -eq 0 ]]; then
    smoke_log "sandbox preflight is ready"
  else
    smoke_log "sandbox preflight is missing $(printf '%s' "$missing" | jq 'length') requirement(s)"
  fi

  jq -n \
    --argjson enabled "$enabled" \
    --arg repo "${AGENDEV_SMOKE_REPO:-}" \
    --arg permission_user "${AGENDEV_SMOKE_PERMISSION_USER:-}" \
    --arg permission_level "${AGENDEV_SMOKE_PERMISSION_LEVEL:-write}" \
    --arg key_path "$key_path" \
    --argjson missing "$missing" '
    {
      enabled: $enabled,
      repo: (if $repo == "" then null else $repo end),
      permission_user: (if $permission_user == "" then null else $permission_user end),
      permission_level: $permission_level,
      key_path: (if $key_path == "" then null else $key_path end),
      missing: $missing,
      ready: ($missing | length == 0)
    }
  '
}

require_preflight() {
  local preflight
  preflight="$(preflight_json)"
  if [[ "$(printf '%s' "$preflight" | jq -r '.ready')" != "true" ]]; then
    printf '%s\n' "$preflight" >&2
    agendev::die "Live smoke preflight failed."
  fi
}

lifecycle_preflight_json() {
  local missing enabled key_path gh_ready owner repo_prefix visibility manifest_path claude_bin codex_bin gh_bin
  missing='[]'
  enabled=false
  key_path="$(smoke_key_path)"
  owner="$(smoke_repo_owner)"
  repo_prefix="$(smoke_repo_prefix)"
  visibility="$(smoke_repo_visibility)"
  manifest_path="$(smoke_manifest_path)"
  claude_bin="$(smoke_claude_bin)"
  codex_bin="$(smoke_codex_bin)"
  gh_bin="$(smoke_gh_bin)"
  gh_ready=false
  smoke_log "checking lifecycle preflight prerequisites"

  if [[ "${AGENDEV_SMOKE:-0}" == "1" ]]; then
    enabled=true
  else
    missing="$(append_missing "$missing" "Set AGENDEV_SMOKE=1 to enable live GitHub smoke tests.")"
  fi

  if [[ -z "$owner" ]]; then
    missing="$(append_missing "$missing" "Missing AGENDEV_SMOKE_REPO_OWNER.")"
  fi

  if [[ -z "${AGENDEV_SMOKE_APP_KEY:-}" ]]; then
    missing="$(append_missing "$missing" "Missing AGENDEV_SMOKE_APP_KEY.")"
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
  if ! command_exists node; then
    missing="$(append_missing "$missing" "Missing required command: node.")"
  fi
  if ! command_exists npm; then
    missing="$(append_missing "$missing" "Missing required command: npm.")"
  fi
  if ! command_exists "$claude_bin"; then
    missing="$(append_missing "$missing" "Claude CLI not found: ${claude_bin}.")"
  fi
  if ! command_exists "$codex_bin"; then
    missing="$(append_missing "$missing" "Codex CLI not found: ${codex_bin}.")"
  fi

  if command_exists "$gh_bin" && operator_auth_ready; then
    gh_ready=true
  else
    missing="$(append_missing "$missing" "Operator gh auth is not ready. Run gh auth login before lifecycle smoke.")"
  fi

  if [[ "$(printf '%s' "$missing" | jq 'length')" -eq 0 ]]; then
    smoke_log "lifecycle preflight is ready"
  else
    smoke_log "lifecycle preflight is missing $(printf '%s' "$missing" | jq 'length') requirement(s)"
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
    --arg codex_bin "$codex_bin" \
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
      codex_bin: $codex_bin,
      gh_bin: $gh_bin,
      missing: $missing,
      ready: ($missing | length == 0)
    }
  '
}

require_lifecycle_preflight() {
  local preflight
  preflight="$(lifecycle_preflight_json)"
  if [[ "$(printf '%s' "$preflight" | jq -r '.ready')" != "true" ]]; then
    printf '%s\n' "$preflight" >&2
    agendev::die "Live lifecycle smoke preflight failed."
  fi
}

bot_login() {
  printf '%s[bot]\n' "$(agendev::config_get '.identity.appSlug')"
}

issue_number_from_url() {
  local url="$1"
  printf '%s' "$url" | sed -n 's#.*/issues/\([0-9][0-9]*\).*#\1#p'
}

pr_number_from_url() {
  local url="$1"
  printf '%s' "$url" | sed -n 's#.*/pull/\([0-9][0-9]*\).*#\1#p'
}

write_identity_file() {
  local root="$1"
  mkdir -p "$root/.agendev"
  jq -n \
    --argjson appId "${AGENDEV_SMOKE_APP_ID}" \
    --argjson installationId "${AGENDEV_SMOKE_INSTALLATION_ID}" \
    --arg privateKeyPath "$(smoke_key_path)" '
    {
      appId: $appId,
      installationId: $installationId,
      privateKeyPath: $privateKeyPath
    }
  ' >"$root/.agendev/identity.json"
}

label_check_json() {
  local repo="$1"
  local existing
  existing="$(agendev::gh label list --repo "$repo" --limit 200 --json name)"
  jq -n \
    --argjson existing "$existing" \
    --argjson expected "$(agendev::label_keys_json)" '
    {
      expected: ($expected | to_entries | map(.value)),
      missing: (
        ($expected | to_entries | map(.value))
        - ($existing | map(.name))
      )
    }
  '
}

find_comment_author() {
  local repo="$1"
  local issue_number="$2"
  local body="$3"
  agendev::gh api "repos/${repo}/issues/${issue_number}/comments" | jq -r --arg body "$body" '
    map(select(.body == $body))
    | last
    | .user.login // empty
  '
}

default_branch() {
  local repo="$1"
  agendev::gh repo view "$repo" --json defaultBranchRef | jq -r '.defaultBranchRef.name'
}

copy_fixture_tree() {
  local source_dir="$1"
  local destination_dir="$2"
  mkdir -p "$destination_dir"
  cp -R "$source_dir/." "$destination_dir/"
}

seed_lifecycle_repo() {
  local destination_dir="$1"
  local fixture_dir
  fixture_dir="$(agendev::root)/test/fixtures/live_smoke_lifecycle_target"
  copy_fixture_tree "$fixture_dir" "$destination_dir"
  git init -b main "$destination_dir" >/dev/null
  git -C "$destination_dir" config user.name "agendev live smoke"
  git -C "$destination_dir" config user.email "agendev-smoke@example.com"
  git -C "$destination_dir" add .
  git -C "$destination_dir" commit -m "Seed live lifecycle smoke target" >/dev/null
}

create_managed_repo() {
  local target_dir="$1"
  local run_id="$2"
  local owner prefix visibility repo_name repo create_output url
  owner="$(smoke_repo_owner)"
  prefix="$(agendev::branch_slug "$(smoke_repo_prefix)")"
  visibility="$(smoke_repo_visibility)"
  repo_name="$(agendev::branch_slug "${prefix}-${run_id}")"
  repo="${owner}/${repo_name}"
  create_output="$(agendev::gh repo create "$repo" "--${visibility}" --source "$target_dir" --remote origin --push)"
  url="$(printf '%s\n' "$create_output" | tail -n1)"
  agendev::gh repo edit "$repo" --default-branch main --enable-auto-merge --enable-squash-merge --delete-branch-on-merge >/dev/null
  jq -n --arg repo "$repo" --arg url "$url" '{repo:$repo, url:$url}'
}

seed_lifecycle_issues() {
  local repo="$1"
  local fixture_file root issue_map issues template key title body priority complexity depends_json create_output issue_number issue_url args
  root="$(agendev::root)"
  fixture_file="$root/test/fixtures/live_smoke_lifecycle_issues.json"
  issue_map='{}'
  issues='[]'

  while IFS= read -r template; do
    [[ -n "$template" ]] || continue
    key="$(printf '%s' "$template" | jq -r '.key')"
    title="$(printf '%s' "$template" | jq -r '.title')"
    body="$(printf '%s' "$template" | jq -r '.body')"
    priority="$(printf '%s' "$template" | jq -r '.priority')"
    complexity="$(printf '%s' "$template" | jq -r '.estimated_complexity')"
    depends_json="$(printf '%s' "$template" | jq --argjson issue_map "$issue_map" '
      [(.depends_on_keys // [])[] | $issue_map[.]]
    ')"

    if [[ "$(printf '%s' "$depends_json" | jq '[.[] | select(. == null)] | length')" -ne 0 ]]; then
      agendev::die "Lifecycle issue template ${key} referenced an unresolved dependency."
    fi

    args=(
      "$root/scripts/gh-issue-queue.sh"
      create
      "$repo"
      "$title"
      "$body"
      --priority "$priority"
      --estimated-complexity "$complexity"
    )

    if [[ "$(printf '%s' "$depends_json" | jq 'length')" -gt 0 ]]; then
      args+=(--depends-on "$(printf '%s' "$depends_json" | jq -r 'join(",")')")
    fi

    create_output="$("${args[@]}")"
    issue_url="$(printf '%s' "$create_output" | jq -r '.url')"
    issue_number="$(issue_number_from_url "$issue_url")"
    [[ -n "$issue_number" ]] || agendev::die "Failed to parse seeded lifecycle issue number for ${key}."
    issue_map="$(printf '%s' "$issue_map" | jq --arg key "$key" --argjson number "$issue_number" '. + {($key): $number}')"
    issues="$(jq -n \
      --argjson issues "$issues" \
      --argjson template "$template" \
      --argjson issue_number "$issue_number" \
      --arg issue_url "$issue_url" '
      $issues + [
        ($template + {
          number: $issue_number,
          url: $issue_url,
          depends_on_numbers: []
        })
      ]
    ')"
  done < <(jq -c '.[]' "$fixture_file")

  printf '%s\n' "$issues" | jq --argjson issue_map "$issue_map" '
    map(. + {
      depends_on_numbers: [(.depends_on_keys // [])[] | $issue_map[.]]
    })
  '
}

copy_state_artifacts() {
  local target_dir="$1"
  local artifacts_dir="$2"
  if [[ -d "$target_dir/.agendev/state" ]]; then
    rm -rf "$artifacts_dir/state"
    mkdir -p "$artifacts_dir"
    cp -R "$target_dir/.agendev/state" "$artifacts_dir/state"
  fi
}

read_state_files_json() {
  local target_dir="$1"
  local state_dir
  state_dir="$target_dir/.agendev/state"
  if [[ ! -d "$state_dir" ]]; then
    printf '[]\n'
    return
  fi

  find "$state_dir" -maxdepth 1 -type f -name '*.json' | sort | while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    if [[ "$(basename "$file" .json)" =~ ^[0-9]+$ ]]; then
      jq -c '.' "$file"
    fi
  done | jq -s '.'
}

fetch_issue_statuses_json() {
  local repo="$1"
  local issues_json="$2"
  local statuses issue number
  statuses='[]'
  while IFS= read -r issue; do
    [[ -n "$issue" ]] || continue
    number="$(printf '%s' "$issue" | jq -r '.number')"
    statuses="$(jq -n \
      --argjson statuses "$statuses" \
      --argjson issue_status "$(agendev::gh issue view "$number" --repo "$repo" --json number,title,state,labels,url)" '
      $statuses + [$issue_status]
    ')"
  done < <(printf '%s' "$issues_json" | jq -c '.[]')
  printf '%s\n' "$statuses"
}

fetch_pr_statuses_json() {
  local repo="$1"
  agendev::gh pr list --repo "$repo" --state all --json number,title,state,isDraft,url,headRefName,baseRefName 2>/dev/null || printf '[]\n'
}

build_lifecycle_summary() {
  local repo="$1"
  local run_id="$2"
  local artifacts_dir="$3"
  local run_exit_json="$4"
  local seeded_issues_json="$5"
  local state_files_json="$6"
  local issue_statuses_json="$7"
  local pr_statuses_json="$8"
  local report_summary_json="$9"
  local failures_json="${10}"
  local checks_json="${11}"
  local manifest_path
  manifest_path="$(smoke_manifest_path)"

  jq -n \
    --arg repo "$repo" \
    --arg run_id "$run_id" \
    --arg artifacts_dir "$artifacts_dir" \
    --arg manifest_path "$manifest_path" \
    --argjson run_exit "$run_exit_json" \
    --argjson seeded "$seeded_issues_json" \
    --argjson states "$state_files_json" \
    --argjson issue_statuses "$issue_statuses_json" \
    --argjson prs "$pr_statuses_json" \
    --argjson report_summary "$report_summary_json" \
    --argjson explicit_failures "$failures_json" \
    --argjson checks "$checks_json" '
    def issue_result($seed):
      ($states | map(select(.issue == $seed.number)) | first) as $state
      | ($issue_statuses | map(select(.number == $seed.number)) | first) as $issue_status
      | {
          key: $seed.key,
          issue: $seed.number,
          title: $seed.title,
          depends_on: ($seed.depends_on_numbers // []),
          phase: ($state.phase // null),
          started_at: ($state.started_at // null),
          updated_at: ($state.updated_at // null),
          verdict: ($state.outcome.verdict // null),
          rounds_used: ($state.outcome.rounds_used // null),
          github_state: ($issue_status.state // null),
          github_labels: (($issue_status.labels // []) | map(.name)),
          url: ($issue_status.url // $seed.url)
        };
    ($seeded | map(issue_result(.))) as $issue_results
    | ($issue_results | sort_by(.started_at // "9999-99-99T99:99:99Z") | map(.issue)) as $actual_order
    | ($seeded | map(.number)) as $expected_order
    | (($prs | map(select((.state | ascii_upcase) == "OPEN")) | length)) as $open_prs
    | (($prs | map(select((.state | ascii_upcase) == "MERGED")) | length)) as $merged_prs
    | (($issue_results | map(select(.phase == "DONE")) | length)) as $completed_issues
    | (($issue_results | map(select(.phase == "DONE" and .rounds_used == 1)) | length)) as $one_shot_completed
    | (($actual_order | length) == ($expected_order | length) and $actual_order == $expected_order) as $queue_order_ok
    | ([
        if ($run_exit != null and $run_exit != 0) then ("agendev run exited with status " + ($run_exit | tostring)) else empty end,
        if ($completed_issues != ($expected_order | length)) then "Not all seeded issues reached DONE." else empty end,
        if ($one_shot_completed != ($expected_order | length)) then "Not all seeded issues completed in one round." else empty end,
        if ($queue_order_ok | not) then "Queue order did not match the seeded dependency order." else empty end,
        if ($open_prs != 0) then "Open PRs remained after the lifecycle run." else empty end
      ] + $explicit_failures) as $all_failures
    | {
        status: (if ($all_failures | length) == 0 then "ok" else "failed" end),
        mode: "lifecycle-eval",
        repo: $repo,
        run_id: $run_id,
        manifest_path: $manifest_path,
        artifacts_dir: $artifacts_dir,
        run_exit_code: $run_exit,
        checks: $checks,
        failures: $all_failures,
        lifecycle: {
          seeded_issues: ($expected_order | length),
          completed_issues: $completed_issues,
          all_issues_done: ($completed_issues == ($expected_order | length)),
          one_shot_completed: $one_shot_completed,
          one_shotable: ($one_shot_completed == ($expected_order | length)),
          queue_order_ok: $queue_order_ok,
          open_prs: $open_prs,
          merged_prs: $merged_prs,
          issue_numbers: $expected_order,
          issue_results: $issue_results,
          report_summary: $report_summary,
          prs: $prs
        }
      }
  '
}

run_smoke() {
  local root repo run_id tmpdir auth_root clone_dir labels_json issue_title issue_body issue_json issue_url issue_number
  local issue_comment_body permission_json current_branch branch smoke_file pr_title pr_json pr_url pr_number
  local pr_comment_file pr_comment_body issue_comment_author pr_comment_author summary_json
  root="$(agendev::root)"
  repo="${AGENDEV_SMOKE_REPO}"
  run_id="${AGENDEV_SMOKE_RUN_ID:-$(date -u +%Y%m%d%H%M%S)}"
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/agendev-live-smoke.XXXXXX")"
  auth_root="$tmpdir/auth"
  clone_dir="$tmpdir/repo"
  issue_number=""
  pr_number=""
  branch=""
  AGENDEV_SMOKE_CLEANUP_REPO="$repo"
  AGENDEV_SMOKE_CLEANUP_RUN_ID="$run_id"
  AGENDEV_SMOKE_CLEANUP_TMPDIR="$tmpdir"
  AGENDEV_SMOKE_CLEANUP_CLONE_DIR="$clone_dir"
  AGENDEV_SMOKE_CLEANUP_ISSUE_NUMBER=""
  AGENDEV_SMOKE_CLEANUP_PR_NUMBER=""
  AGENDEV_SMOKE_CLEANUP_BRANCH=""

  # shellcheck disable=SC2329
  cleanup() {
    smoke_log "cleaning up temporary sandbox resources"
    if [[ -n "${AGENDEV_SMOKE_CLEANUP_PR_NUMBER:-}" ]]; then
      smoke_log "closing PR #${AGENDEV_SMOKE_CLEANUP_PR_NUMBER} in ${AGENDEV_SMOKE_CLEANUP_REPO}"
      agendev::gh pr close "$AGENDEV_SMOKE_CLEANUP_PR_NUMBER" --repo "$AGENDEV_SMOKE_CLEANUP_REPO" --comment "Closing agendev live smoke PR ${AGENDEV_SMOKE_CLEANUP_RUN_ID}." >/dev/null 2>&1 || true
    fi
    if [[ -n "${AGENDEV_SMOKE_CLEANUP_BRANCH:-}" && -d "${AGENDEV_SMOKE_CLEANUP_CLONE_DIR:-}/.git" ]]; then
      smoke_log "deleting remote branch ${AGENDEV_SMOKE_CLEANUP_BRANCH}"
      git -C "$AGENDEV_SMOKE_CLEANUP_CLONE_DIR" push origin --delete "$AGENDEV_SMOKE_CLEANUP_BRANCH" >/dev/null 2>&1 || true
    fi
    if [[ -n "${AGENDEV_SMOKE_CLEANUP_ISSUE_NUMBER:-}" ]]; then
      smoke_log "closing issue #${AGENDEV_SMOKE_CLEANUP_ISSUE_NUMBER} in ${AGENDEV_SMOKE_CLEANUP_REPO}"
      agendev::gh issue close "$AGENDEV_SMOKE_CLEANUP_ISSUE_NUMBER" --repo "$AGENDEV_SMOKE_CLEANUP_REPO" --comment "Closing agendev live smoke issue ${AGENDEV_SMOKE_CLEANUP_RUN_ID}." >/dev/null 2>&1 || true
    fi
    smoke_log "removing temporary workspace ${AGENDEV_SMOKE_CLEANUP_TMPDIR}"
    rm -rf "$AGENDEV_SMOKE_CLEANUP_TMPDIR"
  }
  trap cleanup EXIT

  smoke_log "starting sandbox run for ${repo} with run_id=${run_id}"
  smoke_log "created temporary workspace ${tmpdir}"
  require_preflight
  smoke_log "sandbox preflight is ready"
  mkdir -p "$auth_root"
  export TARGET_ROOT="$auth_root"
  export AGENDEV_STATE_DIR="$auth_root/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  smoke_log "writing GitHub App identity under ${auth_root}/.agendev"
  write_identity_file "$auth_root"
  export AGENDEV_APP_KEY
  AGENDEV_APP_KEY="$(smoke_key_path)"
  export AGENDEV_FORCE_REFRESH_TOKEN=1
  smoke_log "minting a GitHub App installation token"
  eval "$("$root/scripts/gh-auth.sh" export-token)"
  smoke_log "cloning ${repo} into ${clone_dir}"
  git clone "https://x-access-token:${GH_TOKEN}@github.com/${repo}.git" "$clone_dir" >/dev/null 2>&1

  export TARGET_ROOT="$clone_dir"
  export REPO="$repo"
  export AGENDEV_STATE_DIR="$clone_dir/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  smoke_log "preparing cloned repo state in ${clone_dir}"
  write_identity_file "$clone_dir"

  export AGENDEV_SYMLINK_DIR="$tmpdir/bin"
  smoke_log "running scripts/setup.sh to bootstrap labels and local helpers"
  "$root/scripts/setup.sh"

  labels_json="$(label_check_json "$repo")"
  if [[ "$(printf '%s' "$labels_json" | jq -r '.missing | length')" -ne 0 ]]; then
    agendev::die "Missing expected labels after setup: $(printf '%s' "$labels_json" | jq -r '.missing | join(", ")')"
  fi
  smoke_log "verified expected labels in ${repo}"

  issue_title="agendev live smoke ${run_id}"
  issue_body="Live smoke validation issue for ${run_id}."
  smoke_log "creating sandbox issue '${issue_title}'"
  issue_json="$("$root/scripts/gh-issue-queue.sh" create "$repo" "$issue_title" "$issue_body" --priority 3 --estimated-complexity low)"
  issue_url="$(printf '%s' "$issue_json" | jq -r '.url')"
  issue_number="$(issue_number_from_url "$issue_url")"
  AGENDEV_SMOKE_CLEANUP_ISSUE_NUMBER="$issue_number"
  [[ -n "$issue_number" ]] || agendev::die "Failed to parse smoke issue number."
  smoke_log "created issue #${issue_number}: ${issue_url}"

  issue_comment_body="agendev live smoke issue comment ${run_id}"
  smoke_log "posting attribution check comment on issue #${issue_number}"
  agendev::gh issue comment "$issue_number" --repo "$repo" --body "$issue_comment_body" >/dev/null
  issue_comment_author="$(find_comment_author "$repo" "$issue_number" "$issue_comment_body")"
  [[ "$issue_comment_author" == "$(bot_login)" ]] || agendev::die "Issue comment author was ${issue_comment_author}, expected $(bot_login)."
  smoke_log "verified issue comment attribution as $(bot_login)"

  smoke_log "checking ${AGENDEV_SMOKE_PERMISSION_USER} has ${AGENDEV_SMOKE_PERMISSION_LEVEL:-write} access to ${repo}"
  permission_json="$("$root/scripts/gh-pr-lifecycle.sh" check-permission "$repo" "$AGENDEV_SMOKE_PERMISSION_USER" "${AGENDEV_SMOKE_PERMISSION_LEVEL:-write}")"
  current_branch="$(default_branch "$repo")"
  branch="agendev-smoke-${run_id}"
  AGENDEV_SMOKE_CLEANUP_BRANCH="$branch"

  smoke_log "creating branch ${branch} from ${current_branch}"
  git -C "$clone_dir" checkout -b "$branch" "origin/${current_branch}" >/dev/null 2>&1
  git -C "$clone_dir" config user.name "agendev live smoke"
  git -C "$clone_dir" config user.email "agendev-smoke@example.com"
  mkdir -p "$clone_dir/.agendev/smoke"
  smoke_file="$clone_dir/.agendev/smoke/${run_id}.md"
  printf 'agendev live smoke %s\n' "$run_id" >"$smoke_file"
  git -C "$clone_dir" add ".agendev/smoke/${run_id}.md"
  git -C "$clone_dir" commit -m "agendev live smoke ${run_id}" >/dev/null
  smoke_log "committed smoke marker ${smoke_file}"
  smoke_log "pushing branch ${branch}"
  git -C "$clone_dir" push origin "$branch" >/dev/null 2>&1

  pr_title="agendev live smoke ${run_id}"
  smoke_log "opening PR '${pr_title}' for issue #${issue_number}"
  pr_json="$("$root/scripts/gh-pr-lifecycle.sh" create "$repo" "$branch" "$issue_number" "$pr_title")"
  pr_url="$(printf '%s' "$pr_json" | jq -r '.url')"
  pr_number="$(pr_number_from_url "$pr_url")"
  AGENDEV_SMOKE_CLEANUP_PR_NUMBER="$pr_number"
  [[ -n "$pr_number" ]] || agendev::die "Failed to parse smoke PR number."
  smoke_log "created PR #${pr_number}: ${pr_url}"

  pr_comment_file="$tmpdir/pr-comment.md"
  pr_comment_body="agendev live smoke pr comment ${run_id}"
  printf '%s\n' "$pr_comment_body" >"$pr_comment_file"
  smoke_log "posting attribution check comment on PR #${pr_number}"
  "$root/scripts/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$pr_comment_file" >/dev/null
  pr_comment_author="$(find_comment_author "$repo" "$pr_number" "$pr_comment_body")"
  [[ "$pr_comment_author" == "$(bot_login)" ]] || agendev::die "PR comment author was ${pr_comment_author}, expected $(bot_login)."
  smoke_log "verified PR comment attribution as $(bot_login)"
  smoke_log "sandbox checks passed; structured summary will be emitted on stdout"

  summary_json="$(jq -n \
    --arg repo "$repo" \
    --arg run_id "$run_id" \
    --argjson issue_number "$issue_number" \
    --argjson pr_number "$pr_number" \
    --arg bot_login "$(bot_login)" \
    --argjson permission "$permission_json" '
    {
      status: "ok",
      repo: $repo,
      run_id: $run_id,
      issue_number: $issue_number,
      pr_number: $pr_number,
      bot_login: $bot_login,
      permission_check: $permission,
      checks: [
        "github_app_auth",
        "labels_present",
        "issue_created",
        "issue_comment_attribution",
        "permission_check",
        "pr_created",
        "pr_comment_attribution"
      ]
    }
  ')"
  printf '%s\n' "$summary_json"
}

run_lifecycle() {
  local root run_id tmpdir target_dir artifacts_dir repo repo_url repo_json init_log run_log seeded_issues_file
  local summary_file report_file state_files_json issue_statuses_json pr_statuses_json report_summary_json
  local labels_json seeded_issues_json issue_numbers_json run_exit run_exit_json failures_json checks_json summary_json
  root="$(agendev::root)"
  run_id="$(smoke_run_id)"
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/agendev-live-lifecycle.XXXXXX")"
  target_dir="$tmpdir/target"
  artifacts_dir="$(smoke_run_artifacts_dir "$run_id")"
  init_log="$artifacts_dir/init.log"
  run_log="$artifacts_dir/run.log"
  seeded_issues_file="$artifacts_dir/seeded-issues.json"
  summary_file="$artifacts_dir/summary.json"
  report_file="$artifacts_dir/report-summary.json"
  failures_json='[]'
  checks_json='[]'
  run_exit_json='null'
  AGENDEV_SMOKE_LIFECYCLE_CLEANUP_TMPDIR="$tmpdir"

  # shellcheck disable=SC2329
  cleanup() {
    if [[ -n "${AGENDEV_SMOKE_LIFECYCLE_CLEANUP_TMPDIR:-}" ]]; then
      smoke_log "removing temporary lifecycle workspace ${AGENDEV_SMOKE_LIFECYCLE_CLEANUP_TMPDIR}"
      rm -rf "$AGENDEV_SMOKE_LIFECYCLE_CLEANUP_TMPDIR"
    fi
  }
  trap cleanup EXIT

  smoke_log "starting lifecycle run with run_id=${run_id}"
  smoke_log "created temporary workspace ${tmpdir}"
  require_lifecycle_preflight
  smoke_log "lifecycle preflight is ready"
  mkdir -p "$artifacts_dir"
  smoke_log "artifacts will be written to ${artifacts_dir}"

  smoke_log "seeding local target repo from test/fixtures/live_smoke_lifecycle_target into ${target_dir}"
  seed_lifecycle_repo "$target_dir"
  smoke_log "creating managed repo from seeded target"
  repo_json="$(create_managed_repo "$target_dir" "$run_id")"
  repo="$(printf '%s' "$repo_json" | jq -r '.repo')"
  repo_url="$(printf '%s' "$repo_json" | jq -r '.url')"
  smoke_log "created managed repo ${repo}: ${repo_url}"
  manifest_record_repo "$repo" "$run_id" "$repo_url" "$artifacts_dir"
  checks_json="$(append_check "$checks_json" "managed_repo_created")"
  printf '%s\n' "$repo_json" >"$artifacts_dir/repo.json"
  smoke_log "recorded managed repo in $(smoke_manifest_path)"

  export AGENDEV_APP_KEY
  AGENDEV_APP_KEY="$(smoke_key_path)"
  export AGENDEV_SYMLINK_DIR="$tmpdir/bin"

  smoke_log "running agendev init; log -> ${init_log}"
  set +e
  (
    cd "$target_dir"
    "$root/bin/agendev" init
  ) >"$init_log" 2>&1
  run_exit="$?"
  set -e
  if [[ "$run_exit" -ne 0 ]]; then
    smoke_log "agendev init failed with exit code ${run_exit}"
    failures_json="$(append_missing "$failures_json" "agendev init failed. See ${init_log}.")"
  else
    smoke_log "agendev init completed successfully"
    checks_json="$(append_check "$checks_json" "repo_bootstrapped")"
    labels_json="$(label_check_json "$repo")"
    if [[ "$(printf '%s' "$labels_json" | jq -r '.missing | length')" -ne 0 ]]; then
      failures_json="$(append_missing "$failures_json" "Missing expected labels after agendev init: $(printf '%s' "$labels_json" | jq -r '.missing | join(", ")').")"
    else
      smoke_log "verified expected labels in ${repo}"
      checks_json="$(append_check "$checks_json" "labels_present")"
    fi
  fi

  seeded_issues_json='[]'
  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    smoke_log "seeding lifecycle issues from test/fixtures/live_smoke_lifecycle_issues.json"
    seeded_issues_json="$(seed_lifecycle_issues "$repo")"
    printf '%s\n' "$seeded_issues_json" >"$seeded_issues_file"
    smoke_log "seeded issues $(printf '%s' "$seeded_issues_json" | jq -r 'map("#\(.number)") | join(", ")')"
    checks_json="$(append_check "$checks_json" "issues_seeded")"

    smoke_log "running agendev eval; log -> ${run_log}"
    set +e
    (
      cd "$target_dir"
      export AGENDEV_FORCE_REFRESH_TOKEN=1
      "$root/bin/agendev" run
    ) >"$run_log" 2>&1
    run_exit="$?"
    set -e
    run_exit_json="$run_exit"
    smoke_log "agendev run exited with code ${run_exit}"
    checks_json="$(append_check "$checks_json" "lifecycle_run_invoked")"
  fi

  smoke_log "copying state artifacts into ${artifacts_dir}"
  copy_state_artifacts "$target_dir" "$artifacts_dir"
  state_files_json="$(read_state_files_json "$target_dir")"
  issue_numbers_json="$(printf '%s' "$seeded_issues_json" | jq '[.[].number]')"
  issue_statuses_json='[]'
  pr_statuses_json='[]'
  if [[ "$(printf '%s' "$seeded_issues_json" | jq 'length')" -gt 0 ]]; then
    issue_statuses_json="$(fetch_issue_statuses_json "$repo" "$seeded_issues_json")"
    printf '%s\n' "$issue_statuses_json" >"$artifacts_dir/issues.json"
    pr_statuses_json="$(fetch_pr_statuses_json "$repo")"
    printf '%s\n' "$pr_statuses_json" >"$artifacts_dir/prs.json"
  fi

  if [[ -d "$target_dir/.agendev/state" ]]; then
    report_summary_json="$(
      TARGET_ROOT="$target_dir" \
      AGENDEV_STATE_DIR="$target_dir/.agendev/state" \
      "$root/scripts/report.sh" summary
    )"
  else
    report_summary_json='{"issues":0,"pass":0,"fail":0,"caveats":0,"tokens":{"input":0,"cached_input":0,"output":0,"total":0},"average_rounds":0}'
  fi
  printf '%s\n' "$report_summary_json" >"$report_file"
  smoke_log "wrote report summary to ${report_file}"

  summary_json="$(build_lifecycle_summary \
    "$repo" \
    "$run_id" \
    "$artifacts_dir" \
    "$run_exit_json" \
    "$seeded_issues_json" \
    "$state_files_json" \
    "$issue_statuses_json" \
    "$pr_statuses_json" \
    "$report_summary_json" \
    "$failures_json" \
    "$checks_json"
  )"
  printf '%s\n' "$summary_json" >"$summary_file"
  smoke_log "wrote lifecycle summary to ${summary_file}"

  manifest_update_run_result \
    "$repo" \
    "$(printf '%s' "$summary_json" | jq -r '.status')" \
    "$issue_numbers_json" \
    "$(printf '%s' "$summary_json" | jq '.failures')"
  smoke_log "updated managed repo manifest with lifecycle result"
  smoke_log "lifecycle run complete; structured summary will be emitted on stdout"

  printf '%s\n' "$summary_json"
}

cleanup_lifecycle() {
  local selector_type="" selector_value="" selected_json deleted_json failed_json entry repo result
  deleted_json='[]'
  failed_json='[]'

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --repo)
        [[ $# -ge 2 ]] || agendev::die "--repo requires a value"
        selector_type="repo"
        selector_value="$2"
        shift 2
        ;;
      --run-id)
        [[ $# -ge 2 ]] || agendev::die "--run-id requires a value"
        selector_type="run-id"
        selector_value="$2"
        shift 2
        ;;
      --all)
        selector_type="all"
        selector_value=""
        shift
        ;;
      *)
        agendev::die "Usage: smoke-lifecycle.sh cleanup (--repo OWNER/REPO | --run-id ID | --all)"
        ;;
    esac
  done

  [[ -n "$selector_type" ]] || agendev::die "Provide --repo OWNER/REPO, --run-id ID, or --all."
  command_exists "$(smoke_gh_bin)" || agendev::die "GitHub CLI not found: $(smoke_gh_bin)"
  operator_auth_ready || agendev::die "Operator gh auth is not ready. Run gh auth login before lifecycle cleanup."

  selected_json="$(manifest_select_entries "$selector_type" "$selector_value")"
  if [[ "$(printf '%s' "$selected_json" | jq 'length')" -eq 0 ]]; then
    agendev::die "No matching managed lifecycle repos found to clean up."
  fi
  smoke_log "selected $(printf '%s' "$selected_json" | jq 'length') managed repo(s) for cleanup"

  while IFS= read -r entry; do
    [[ -n "$entry" ]] || continue
    repo="$(printf '%s' "$entry" | jq -r '.repo')"
    smoke_log "deleting managed repo ${repo}"
    if agendev::gh repo delete "$repo" --yes >/dev/null 2>&1; then
      manifest_mark_deleted "$repo"
      deleted_json="$(jq -n --argjson deleted "$deleted_json" --arg repo "$repo" '$deleted + [$repo]')"
      smoke_log "deleted managed repo ${repo}"
    else
      result="Failed to delete ${repo}. Confirm gh delete_repo scope and repo access."
      manifest_mark_cleanup_failure "$repo" "$result"
      failed_json="$(jq -n --argjson failed "$failed_json" --arg repo "$repo" --arg error "$result" '$failed + [{repo:$repo, error:$error}]')"
      smoke_log "$result"
    fi
  done < <(printf '%s' "$selected_json" | jq -c '.[]')
  smoke_log "cleanup finished: deleted $(printf '%s' "$deleted_json" | jq 'length'), failed $(printf '%s' "$failed_json" | jq 'length')"

  jq -n \
    --arg manifest_path "$(smoke_manifest_path)" \
    --arg selector_type "$selector_type" \
    --arg selector_value "$selector_value" \
    --argjson deleted "$deleted_json" \
    --argjson failed "$failed_json" '
    {
      status: (if ($failed | length) == 0 then "ok" else "partial" end),
      selector: {
        type: $selector_type,
        value: (if $selector_value == "" then null else $selector_value end)
      },
      manifest_path: $manifest_path,
      deleted: $deleted,
      failed: $failed
    }
  '
}
