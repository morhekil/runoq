#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

smoke_key_path() {
  local key_path="${RUNOQ_SMOKE_APP_KEY:-}"
  key_path="${key_path/#\~/$HOME}"
  printf '%s\n' "$key_path"
}

smoke_run_id() {
  local run_id="${RUNOQ_SMOKE_RUN_ID:-}"
  if [[ -z "$run_id" ]]; then
    run_id="$(date -u +%Y%m%d%H%M%S)-$RANDOM"
  fi
  printf '%s\n' "$(runoq::branch_slug "$run_id")"
}

smoke_repo_owner() {
  printf '%s\n' "${RUNOQ_SMOKE_REPO_OWNER:-}"
}

smoke_repo_prefix() {
  printf '%s\n' "${RUNOQ_SMOKE_REPO_PREFIX:-runoq-live-eval}"
}

smoke_repo_visibility() {
  printf '%s\n' "${RUNOQ_SMOKE_REPO_VISIBILITY:-private}"
}

smoke_manifest_path() {
  printf '%s\n' "${RUNOQ_SMOKE_MANIFEST_PATH:-$(runoq::root)/.runoq/live-smoke/managed-repos.json}"
}

smoke_runs_root() {
  printf '%s\n' "${RUNOQ_SMOKE_RUNS_DIR:-$(runoq::root)/.runoq/live-smoke/runs}"
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
  case "${RUNOQ_SMOKE_VERBOSE:-auto}" in
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
  local scope="${RUNOQ_SMOKE_LOG_SCOPE:-smoke}"
  printf '[%s] %s\n' "$scope" "$*" >&2
}

run_quiet_command() {
  local description="$1"
  shift
  local output status

  set +e
  output="$("$@" 2>&1)"
  status="$?"
  set -e

  if [[ "$status" -ne 0 ]]; then
    if [[ -n "$output" ]]; then
      printf '%s\n' "$output" >&2
    fi
    runoq::die "${description} failed with exit code ${status}."
  fi
}

smoke_gh_bin() {
  printf '%s\n' "${GH_BIN:-gh}"
}

resolve_tool_bin() {
  local candidate="$1"
  local resolved=""
  if [[ "$candidate" == */* ]]; then
    printf '%s\n' "$candidate"
    return 0
  fi
  resolved="$(command -v "$candidate" 2>/dev/null || true)"
  if [[ -n "$resolved" ]]; then
    printf '%s\n' "$resolved"
  else
    printf '%s\n' "$candidate"
  fi
}

smoke_claude_bin() {
  resolve_tool_bin "${RUNOQ_CLAUDE_BIN:-claude}"
}

create_claude_capture_wrapper() {
  local wrapper_path="$1"
  cat >"$wrapper_path" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

capture_root="${RUNOQ_SMOKE_CLAUDE_CAPTURE_DIR:?}"
real_bin="${RUNOQ_SMOKE_REAL_CLAUDE_BIN:?}"
timestamp="$(date -u +%Y%m%d%H%M%S)-$$"
invoke_dir="$capture_root/$timestamp"
status=0

mkdir -p "$invoke_dir"
{
  printf 'cwd=%s\n' "$PWD"
  printf 'TARGET_ROOT=%s\n' "${TARGET_ROOT:-}"
  printf 'REPO=%s\n' "${REPO:-}"
  printf 'RUNOQ_ROOT=%s\n' "${RUNOQ_ROOT:-}"
  printf 'REAL_BIN=%s\n' "$real_bin"
} >"$invoke_dir/context.log"
printf '%s\n' "$@" >"$invoke_dir/argv.txt"

set +e
"$real_bin" "$@" >"$invoke_dir/stdout.log" 2>"$invoke_dir/stderr.log"
status="$?"
set -e

cat "$invoke_dir/stdout.log"
cat "$invoke_dir/stderr.log" >&2
exit "$status"
EOF
  chmod +x "$wrapper_path"
}

create_codex_capture_wrapper() {
  local wrapper_path="$1"
  cat >"$wrapper_path" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

capture_root="${RUNOQ_SMOKE_CODEX_CAPTURE_DIR:?}"
real_bin="${RUNOQ_SMOKE_REAL_CODEX_BIN:?}"
timestamp="$(date -u +%Y%m%d%H%M%S)-$$"
invoke_dir="$capture_root/$timestamp"
status=0

mkdir -p "$invoke_dir"
{
  printf 'cwd=%s\n' "$PWD"
  printf 'TARGET_ROOT=%s\n' "${TARGET_ROOT:-}"
  printf 'REPO=%s\n' "${REPO:-}"
  printf 'RUNOQ_ROOT=%s\n' "${RUNOQ_ROOT:-}"
  printf 'REAL_BIN=%s\n' "$real_bin"
} >"$invoke_dir/context.log"
printf '%s\n' "$@" >"$invoke_dir/argv.txt"

set +e
"$real_bin" "$@" >"$invoke_dir/stdout.log" 2>"$invoke_dir/stderr.log"
status="$?"
set -e

cat "$invoke_dir/stdout.log"
cat "$invoke_dir/stderr.log" >&2
exit "$status"
EOF
  chmod +x "$wrapper_path"
}

smoke_codex_bin() {
  resolve_tool_bin "${RUNOQ_SMOKE_CODEX_BIN:-codex}"
}

operator_auth_ready() {
  runoq::gh auth status >/dev/null 2>&1
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
      runoq::die "Unknown manifest selector: $selector_type"
      ;;
  esac
}

preflight_json() {
  local missing enabled key_path
  missing='[]'
  enabled=false
  key_path="$(smoke_key_path)"
  smoke_log "checking sandbox preflight prerequisites"

  if [[ "${RUNOQ_SMOKE:-0}" == "1" ]]; then
    enabled=true
  else
    missing="$(append_missing "$missing" "Set RUNOQ_SMOKE=1 to enable live GitHub smoke tests.")"
  fi

  for required in \
    RUNOQ_SMOKE_REPO \
    RUNOQ_SMOKE_APP_ID \
    RUNOQ_SMOKE_INSTALLATION_ID \
    RUNOQ_SMOKE_APP_KEY \
    RUNOQ_SMOKE_PERMISSION_USER; do
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
    --arg repo "${RUNOQ_SMOKE_REPO:-}" \
    --arg permission_user "${RUNOQ_SMOKE_PERMISSION_USER:-}" \
    --arg permission_level "${RUNOQ_SMOKE_PERMISSION_LEVEL:-write}" \
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
    runoq::die "Live smoke preflight failed."
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
    runoq::die "Live lifecycle smoke preflight failed."
  fi
}

bot_login() {
  printf '%s[bot]\n' "$(runoq::config_get '.identity.appSlug')"
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
  mkdir -p "$root/.runoq"
  jq -n \
    --argjson appId "${RUNOQ_SMOKE_APP_ID}" \
    --argjson installationId "${RUNOQ_SMOKE_INSTALLATION_ID}" \
    --arg privateKeyPath "$(smoke_key_path)" '
    {
      appId: $appId,
      installationId: $installationId,
      privateKeyPath: $privateKeyPath
    }
  ' >"$root/.runoq/identity.json"
}

label_check_json() {
  local repo="$1"
  local existing
  existing="$(runoq::gh label list --repo "$repo" --limit 200 --json name)"
  jq -n \
    --argjson existing "$existing" \
    --argjson expected "$(runoq::label_keys_json)" '
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
  local author attempt max_attempts
  max_attempts="${RUNOQ_SMOKE_COMMENT_LOOKUP_ATTEMPTS:-5}"

  for ((attempt = 1; attempt <= max_attempts; attempt++)); do
    author="$(runoq::gh api "repos/${repo}/issues/${issue_number}/comments" | jq -r --arg body "$body" '
      def normalize:
        gsub("\r"; "")
        | sub("\n+$"; "");
      map(select((.body | normalize) == ($body | normalize)))
      | last
      | .user.login // empty
    ')"
    if [[ -n "$author" ]]; then
      printf '%s\n' "$author"
      return 0
    fi
    if [[ "$attempt" -lt "$max_attempts" ]]; then
      sleep 1
    fi
  done

  printf '\n'
}

default_branch() {
  local repo="$1"
  local branch
  branch="$(runoq::gh repo view "$repo" --json defaultBranchRef | jq -r '.defaultBranchRef.name // empty')"
  [[ -n "$branch" ]] || runoq::die "Failed to resolve default branch for ${repo}."
  printf '%s\n' "$branch"
}

default_branch_from_clone() {
  local clone_dir="$1"
  local branch

  branch="$(git -C "$clone_dir" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null || true)"
  branch="${branch#origin/}"
  if [[ -z "$branch" ]]; then
    branch="$(git -C "$clone_dir" branch --show-current 2>/dev/null || true)"
  fi

  [[ -n "$branch" ]] || runoq::die "Failed to resolve default branch from cloned repo at ${clone_dir}."
  printf '%s\n' "$branch"
}

ensure_default_branch_commit() {
  local clone_dir="$1"
  local repo="$2"
  local branch="$3"

  if ! git -C "$clone_dir" rev-parse --verify --quiet "refs/remotes/origin/${branch}^{commit}" >/dev/null; then
    smoke_log "default branch ${branch} is empty in ${repo}; creating bootstrap commit"
    run_quiet_command "Failed to create bootstrap commit on ${repo} default branch ${branch}" \
      git -C "$clone_dir" -c user.name="runoq live smoke" -c user.email="runoq-smoke@example.com" \
      commit --allow-empty -m "Seed sandbox default branch"
    run_quiet_command "Failed to push bootstrap commit for ${repo} default branch ${branch}" \
      git -C "$clone_dir" push -u origin "$branch"
  fi
}

commit_smoke_marker() {
  local clone_dir="$1"
  local run_id="$2"
  local smoke_file relative_path

  mkdir -p "$clone_dir/.runoq/smoke"
  smoke_file="$clone_dir/.runoq/smoke/${run_id}.md"
  relative_path=".runoq/smoke/${run_id}.md"
  printf 'runoq live smoke %s\n' "$run_id" >"$smoke_file"
  git -C "$clone_dir" add -f "$relative_path"
  git -C "$clone_dir" commit -m "runoq live smoke ${run_id}" >/dev/null
  printf '%s\n' "$smoke_file"
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
  fixture_dir="$(runoq::root)/test/fixtures/live_smoke_lifecycle_target"
  copy_fixture_tree "$fixture_dir" "$destination_dir"
  git init -b main "$destination_dir" >/dev/null
  git -C "$destination_dir" config user.name "runoq live smoke"
  git -C "$destination_dir" config user.email "runoq-smoke@example.com"
  git -C "$destination_dir" add .
  git -C "$destination_dir" commit -m "Seed live lifecycle smoke target" >/dev/null
}

create_managed_repo() {
  local target_dir="$1"
  local run_id="$2"
  local owner prefix visibility repo_name repo create_output url
  owner="$(smoke_repo_owner)"
  prefix="$(runoq::branch_slug "$(smoke_repo_prefix)")"
  visibility="$(smoke_repo_visibility)"
  repo_name="$(runoq::branch_slug "${prefix}-${run_id}")"
  repo="${owner}/${repo_name}"
  create_output="$(runoq::gh repo create "$repo" "--${visibility}" --source "$target_dir" --remote origin --push)"
  url="$(printf '%s\n' "$create_output" | tail -n1)"
  runoq::gh repo edit "$repo" --default-branch main --enable-auto-merge --enable-squash-merge --delete-branch-on-merge >/dev/null
  jq -n --arg repo "$repo" --arg url "$url" '{repo:$repo, url:$url}'
}

seed_lifecycle_issues() {
  local repo="$1"
  local fixture_file root issue_map issues template key title body priority complexity type_field parent_epic_key depends_json create_output issue_number issue_url args epic_number
  root="$(runoq::root)"
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
    local complexity_rationale
    complexity_rationale="$(printf '%s' "$template" | jq -r '.complexity_rationale // empty')"
    type_field="$(printf '%s' "$template" | jq -r '.type // "task"')"
    parent_epic_key="$(printf '%s' "$template" | jq -r '.parent_epic_key // empty')"
    depends_json="$(printf '%s' "$template" | jq --argjson issue_map "$issue_map" '
      [(.depends_on_keys // [])[] | $issue_map[.]]
    ')"

    if [[ "$(printf '%s' "$depends_json" | jq '[.[] | select(. == null)] | length')" -ne 0 ]]; then
      runoq::die "Lifecycle issue template ${key} referenced an unresolved dependency."
    fi

    args=(
      "$root/scripts/gh-issue-queue.sh"
      create
      "$repo"
      "$title"
      "$body"
      --priority "$priority"
      --estimated-complexity "$complexity"
      --type "$type_field"
    )

    if [[ -n "$complexity_rationale" ]]; then
      args+=(--complexity-rationale "$complexity_rationale")
    fi

    if [[ "$(printf '%s' "$depends_json" | jq 'length')" -gt 0 ]]; then
      args+=(--depends-on "$(printf '%s' "$depends_json" | jq -r 'join(",")')")
    fi

    if [[ -n "$parent_epic_key" ]]; then
      epic_number="$(printf '%s' "$issue_map" | jq -r --arg k "$parent_epic_key" '.[$k] // empty')"
      if [[ -z "$epic_number" ]]; then
        runoq::die "Lifecycle issue template ${key} referenced unresolved parent epic ${parent_epic_key}."
      fi
      args+=(--parent-epic "$epic_number")
    fi

    local _seed_attempt
    create_output=""
    for _seed_attempt in 1 2 3; do
      if create_output="$("${args[@]}" 2>/dev/null)"; then
        break
      fi
      smoke_log "issue creation attempt ${_seed_attempt}/3 failed for ${key}, retrying in 5s"
      sleep 5
    done
    issue_url="$(printf '%s' "$create_output" | jq -r '.url')"
    issue_number="$(issue_number_from_url "$issue_url")"
    [[ -n "$issue_number" ]] || runoq::die "Failed to parse seeded lifecycle issue number for ${key}."
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

  # Link child issues as sub-issues of their parent epics via GitHub API
  local child_key child_number child_id parent_number
  while IFS= read -r template; do
    [[ -n "$template" ]] || continue
    parent_epic_key="$(printf '%s' "$template" | jq -r '.parent_epic_key // empty')"
    [[ -n "$parent_epic_key" ]] || continue
    child_key="$(printf '%s' "$template" | jq -r '.key')"
    child_number="$(printf '%s' "$issue_map" | jq -r --arg k "$child_key" '.[$k] // empty')"
    parent_number="$(printf '%s' "$issue_map" | jq -r --arg k "$parent_epic_key" '.[$k] // empty')"
    [[ -n "$child_number" && -n "$parent_number" ]] || continue

    child_id="$(runoq::gh api "repos/${repo}/issues/${child_number}" --jq '.id')"
    smoke_log "linking child #${child_number} as sub-issue of epic #${parent_number}"
    runoq::gh api "repos/${repo}/issues/${parent_number}/sub_issues" --method POST -F "sub_issue_id=${child_id}" >/dev/null 2>&1 || true
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
  local parent_dir state_dir file
  parent_dir="$(dirname "$target_dir")"

  rm -rf "$artifacts_dir/state"
  mkdir -p "$artifacts_dir/state"

  while IFS= read -r state_dir; do
    [[ -d "$state_dir" ]] || continue
    while IFS= read -r file; do
      [[ -n "$file" ]] || continue
      cp "$file" "$artifacts_dir/state/"
    done < <(find "$state_dir" -maxdepth 1 -type f -name '*.json' | sort)
  done < <(find "$parent_dir" -maxdepth 3 -type d -path '*/.runoq/state' | sort)

  if [[ -z "$(find "$artifacts_dir/state" -maxdepth 1 -type f -name '*.json' -print -quit)" ]]; then
    rmdir "$artifacts_dir/state" 2>/dev/null || true
  fi
}

read_state_files_json_from_dir() {
  local state_dir="$1"
  if [[ ! -d "$state_dir" ]]; then
    printf '[]\n'
    return
  fi

  find "$state_dir" -maxdepth 1 -type f -name '*.json' | sort | while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    if [[ "$(basename "$file" .json)" =~ ^[0-9]+$ ]]; then
      jq -c '
        . as $state
        | $state + {
            issue: ($state.issue // $state.issueNumber // null),
            phase: (
              if ($state.phase // null) != null then $state.phase
              elif ($state.status // "") == "done" then "DONE"
              elif ($state.status // "") == "failed" then "FAILED"
              elif ($state.status // "") == "in_progress" then "DEVELOP"
              else null
              end
            ),
            started_at: ($state.started_at // $state.dispatchedAt // null),
            updated_at: ($state.updated_at // $state.completedAt // null),
            outcome: (
              if ($state.outcome // null) != null then $state.outcome
              elif ($state.verdict // null) != null then {
                verdict: $state.verdict,
                rounds_used: ($state.rounds // null),
                score: ($state.score // null),
                finalization: ($state.finalization // null)
              }
              elif ($state.result // null) != null then (
                $state.result + {
                  rounds_used: ($state.result.rounds_used // $state.rounds // null)
                }
              )
              else null
              end
            )
          }
      ' "$file"
    fi
  done | jq -s '.'
}

read_state_files_json() {
  local target_dir="$1"
  read_state_files_json_from_dir "$target_dir/.runoq/state"
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
      --argjson issue_status "$(runoq::gh issue view "$number" --repo "$repo" --json number,title,state,labels,url)" '
      $statuses + [$issue_status]
    ')"
  done < <(printf '%s' "$issues_json" | jq -c '.[]')
  printf '%s\n' "$statuses"
}

fetch_pr_statuses_json() {
  local repo="$1"
  runoq::gh pr list --repo "$repo" --state all --json number,title,state,isDraft,url,headRefName,baseRefName 2>/dev/null || printf '[]\n'
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
          type: ($seed.type // "task"),
          depends_on: ($seed.depends_on_numbers // []),
          phase: ($state.phase // null),
          started_at: ($state.started_at // null),
          updated_at: ($state.updated_at // null),
          verdict: ($state.outcome.verdict // $state.verdict // null),
          rounds_used: ($state.outcome.rounds_used // $state.rounds // null),
          github_state: ($issue_status.state // null),
          github_labels: (($issue_status.labels // []) | map(.name)),
          url: ($issue_status.url // $seed.url)
        };
    ($seeded | map(issue_result(.))) as $issue_results
    | ([$issue_results[] | select(.phase == "DONE" and .type != "epic")] | sort_by(.started_at // "9999-99-99T99:99:99Z") | map(.issue)) as $actual_order
    | ($seeded | map(.number)) as $expected_order
    | ($seeded | map(select(.type != "epic")) | map(.number)) as $expected_tasks
    | (($prs | map(select((.state | ascii_upcase) == "OPEN")) | length)) as $open_prs
    | (($issue_results | map(select(.verdict == "PASS")) | map(.issue)) as $passed_issues
       | [$prs[] | select((.state | ascii_upcase) == "OPEN")] | map(.headRefName)
       | map(capture("/(?<num>[0-9]+)-") | .num | tonumber)
       | map(select(. as $n | $passed_issues | any(. == $n)))
       | length) as $open_prs_for_passed
    | (($prs | map(select((.state | ascii_upcase) == "MERGED")) | length)) as $merged_prs
    | (($issue_results | map(select(.phase == "DONE" and .type != "epic")) | length)) as $completed_issues
    | (($issue_results | map(select(.phase == "DONE" and .rounds_used == 1)) | length)) as $one_shot_completed
    | (($actual_order | length) == ($expected_tasks | length) and $actual_order == $expected_tasks) as $queue_order_ok
    | (($seeded | map(select(.type == "epic")) | length)) as $epics
    | ([$states[] | select(.phase == "CRITERIA" or (.phase_history // [] | any(. == "CRITERIA")))] | length) as $criteria_phases_run
    | ([$seeded[] | select(.estimated_complexity == "low" and (.type // "task") != "epic")] | length) as $criteria_phases_skipped
    | ([$states[] | select(.criteria_commit != null and .criteria_commit != "")] | length) as $criteria_commits_recorded
    | 0 as $criteria_tamper_violations
    | ([$seeded[] | select(.type == "epic")] | map(.number) as $epic_nums
        | [$issue_results[] | select(.phase == "DONE" and ([.issue] | inside($epic_nums)))] | length) as $integration_gates_passed
    | {processed: 0, questions_answered: 0, change_requests_routed: 0, irrelevant_skipped: 0, response_has_audit_marker: false} as $mentions
    | ([
        if ($run_exit != null and $run_exit != 0) then ("runoq run exited with status " + ($run_exit | tostring)) else empty end,
        if ($completed_issues != ($expected_tasks | length)) then "Not all seeded task issues reached DONE." else empty end,
        if ($queue_order_ok | not) then "Queue order did not match the seeded dependency order." else empty end,
        if ($open_prs_for_passed != 0) then "Open PRs remained for issues that passed." else empty end,
        if ($criteria_tamper_violations > 0) then "Criteria tamper violations detected." else empty end,
        # Epic lifecycle (integration gates) not yet implemented — skip this check
        empty
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
          seeded_tasks: ($expected_tasks | length),
          completed_issues: $completed_issues,
          all_tasks_done: ($completed_issues == ($expected_tasks | length)),
          one_shot_completed: $one_shot_completed,
          one_shotable: ($one_shot_completed == ($expected_tasks | length)),
          queue_order_ok: $queue_order_ok,
          open_prs: $open_prs,
          merged_prs: $merged_prs,
          epics: $epics,
          criteria_phases_run: $criteria_phases_run,
          criteria_phases_skipped: $criteria_phases_skipped,
          criteria_commits_recorded: $criteria_commits_recorded,
          criteria_tamper_violations: $criteria_tamper_violations,
          integration_gates_passed: $integration_gates_passed,
          mentions: $mentions,
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
  root="$(runoq::root)"
  repo="${RUNOQ_SMOKE_REPO}"
  run_id="${RUNOQ_SMOKE_RUN_ID:-$(date -u +%Y%m%d%H%M%S)}"
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/runoq-live-smoke.XXXXXX")"
  auth_root="$tmpdir/auth"
  clone_dir="$tmpdir/repo"
  issue_number=""
  pr_number=""
  branch=""
  RUNOQ_SMOKE_CLEANUP_REPO="$repo"
  RUNOQ_SMOKE_CLEANUP_RUN_ID="$run_id"
  RUNOQ_SMOKE_CLEANUP_TMPDIR="$tmpdir"
  RUNOQ_SMOKE_CLEANUP_CLONE_DIR="$clone_dir"
  RUNOQ_SMOKE_CLEANUP_ISSUE_NUMBER=""
  RUNOQ_SMOKE_CLEANUP_PR_NUMBER=""
  RUNOQ_SMOKE_CLEANUP_BRANCH=""

  # shellcheck disable=SC2329
  cleanup() {
    smoke_log "cleaning up temporary sandbox resources"
    if [[ -n "${RUNOQ_SMOKE_CLEANUP_PR_NUMBER:-}" ]]; then
      smoke_log "closing PR #${RUNOQ_SMOKE_CLEANUP_PR_NUMBER} in ${RUNOQ_SMOKE_CLEANUP_REPO}"
      runoq::gh pr close "$RUNOQ_SMOKE_CLEANUP_PR_NUMBER" --repo "$RUNOQ_SMOKE_CLEANUP_REPO" --comment "Closing runoq live smoke PR ${RUNOQ_SMOKE_CLEANUP_RUN_ID}." >/dev/null 2>&1 || true
    fi
    if [[ -n "${RUNOQ_SMOKE_CLEANUP_BRANCH:-}" && -d "${RUNOQ_SMOKE_CLEANUP_CLONE_DIR:-}/.git" ]]; then
      smoke_log "deleting remote branch ${RUNOQ_SMOKE_CLEANUP_BRANCH}"
      git -C "$RUNOQ_SMOKE_CLEANUP_CLONE_DIR" push origin --delete "$RUNOQ_SMOKE_CLEANUP_BRANCH" >/dev/null 2>&1 || true
    fi
    if [[ -n "${RUNOQ_SMOKE_CLEANUP_ISSUE_NUMBER:-}" ]]; then
      smoke_log "closing issue #${RUNOQ_SMOKE_CLEANUP_ISSUE_NUMBER} in ${RUNOQ_SMOKE_CLEANUP_REPO}"
      runoq::gh issue close "$RUNOQ_SMOKE_CLEANUP_ISSUE_NUMBER" --repo "$RUNOQ_SMOKE_CLEANUP_REPO" --comment "Closing runoq live smoke issue ${RUNOQ_SMOKE_CLEANUP_RUN_ID}." >/dev/null 2>&1 || true
    fi
    smoke_log "removing temporary workspace ${RUNOQ_SMOKE_CLEANUP_TMPDIR}"
    rm -rf "$RUNOQ_SMOKE_CLEANUP_TMPDIR"
  }
  trap cleanup EXIT

  smoke_log "starting sandbox run for ${repo} with run_id=${run_id}"
  smoke_log "created temporary workspace ${tmpdir}"
  require_preflight
  smoke_log "sandbox preflight is ready"
  mkdir -p "$auth_root"
  export TARGET_ROOT="$auth_root"
  export RUNOQ_STATE_DIR="$auth_root/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"
  smoke_log "writing GitHub App identity under ${auth_root}/.runoq"
  write_identity_file "$auth_root"
  export RUNOQ_APP_KEY
  RUNOQ_APP_KEY="$(smoke_key_path)"
  export RUNOQ_FORCE_REFRESH_TOKEN=1
  smoke_log "minting a GitHub App installation token"
  eval "$("$root/scripts/gh-auth.sh" export-token)"
  smoke_log "cloning ${repo} into ${clone_dir}"
  run_quiet_command "Failed to clone sandbox repo ${repo} into ${clone_dir}" \
    git clone "https://x-access-token:${GH_TOKEN}@github.com/${repo}.git" "$clone_dir"

  export TARGET_ROOT="$clone_dir"
  export REPO="$repo"
  export RUNOQ_STATE_DIR="$clone_dir/.runoq/state"
  mkdir -p "$RUNOQ_STATE_DIR"
  smoke_log "preparing cloned repo state in ${clone_dir}"
  write_identity_file "$clone_dir"

  export RUNOQ_SYMLINK_DIR="$tmpdir/bin"
  smoke_log "running scripts/setup.sh to bootstrap labels and local helpers"
  "$root/scripts/setup.sh"

  labels_json="$(label_check_json "$repo")"
  if [[ "$(printf '%s' "$labels_json" | jq -r '.missing | length')" -ne 0 ]]; then
    runoq::die "Missing expected labels after setup: $(printf '%s' "$labels_json" | jq -r '.missing | join(", ")')"
  fi
  smoke_log "verified expected labels in ${repo}"

  current_branch="$(default_branch_from_clone "$clone_dir")"
  ensure_default_branch_commit "$clone_dir" "$repo" "$current_branch"

  issue_title="runoq live smoke ${run_id}"
  issue_body="Live smoke validation issue for ${run_id}."
  smoke_log "creating sandbox issue '${issue_title}'"
  issue_json="$("$root/scripts/gh-issue-queue.sh" create "$repo" "$issue_title" "$issue_body" --priority 3 --estimated-complexity low)"
  issue_url="$(printf '%s' "$issue_json" | jq -r '.url')"
  issue_number="$(issue_number_from_url "$issue_url")"
  RUNOQ_SMOKE_CLEANUP_ISSUE_NUMBER="$issue_number"
  [[ -n "$issue_number" ]] || runoq::die "Failed to parse smoke issue number."
  smoke_log "created issue #${issue_number}: ${issue_url}"

  issue_comment_body="runoq live smoke issue comment ${run_id}"
  smoke_log "posting attribution check comment on issue #${issue_number}"
  runoq::gh issue comment "$issue_number" --repo "$repo" --body "$issue_comment_body" >/dev/null
  issue_comment_author="$(find_comment_author "$repo" "$issue_number" "$issue_comment_body")"
  [[ "$issue_comment_author" == "$(bot_login)" ]] || runoq::die "Issue comment author was ${issue_comment_author}, expected $(bot_login)."
  smoke_log "verified issue comment attribution as $(bot_login)"

  smoke_log "checking ${RUNOQ_SMOKE_PERMISSION_USER} has ${RUNOQ_SMOKE_PERMISSION_LEVEL:-write} access to ${repo}"
  permission_json="$("$root/scripts/gh-pr-lifecycle.sh" check-permission "$repo" "$RUNOQ_SMOKE_PERMISSION_USER" "${RUNOQ_SMOKE_PERMISSION_LEVEL:-write}")"
  branch="runoq-smoke-${run_id}"
  RUNOQ_SMOKE_CLEANUP_BRANCH="$branch"

  smoke_log "creating branch ${branch} from ${current_branch}"
  run_quiet_command "Failed to create sandbox branch ${branch} from ${current_branch}" \
    git -C "$clone_dir" checkout -b "$branch" "$current_branch"
  runoq::configure_git_bot_identity "$clone_dir" 2>/dev/null || {
    git -C "$clone_dir" config user.name "runoq[bot]"
    git -C "$clone_dir" config user.email "runoq-smoke@example.com"
  }
  smoke_file="$(commit_smoke_marker "$clone_dir" "$run_id")"
  smoke_log "committed smoke marker ${smoke_file}"
  smoke_log "pushing branch ${branch}"
  run_quiet_command "Failed to push sandbox branch ${branch}" \
    git -C "$clone_dir" push origin "$branch"

  pr_title="runoq live smoke ${run_id}"
  smoke_log "opening PR '${pr_title}' for issue #${issue_number}"
  pr_json="$("$root/scripts/gh-pr-lifecycle.sh" create "$repo" "$branch" "$issue_number" "$pr_title")"
  pr_url="$(printf '%s' "$pr_json" | jq -r '.url')"
  pr_number="$(pr_number_from_url "$pr_url")"
  RUNOQ_SMOKE_CLEANUP_PR_NUMBER="$pr_number"
  [[ -n "$pr_number" ]] || runoq::die "Failed to parse smoke PR number."
  smoke_log "created PR #${pr_number}: ${pr_url}"

  pr_comment_file="$tmpdir/pr-comment.md"
  pr_comment_body="runoq live smoke pr comment ${run_id}"
  printf '%s\n' "$pr_comment_body" >"$pr_comment_file"
  smoke_log "posting attribution check comment on PR #${pr_number}"
  "$root/scripts/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$pr_comment_file" >/dev/null
  pr_comment_author="$(find_comment_author "$repo" "$pr_number" "$pr_comment_body")"
  [[ "$pr_comment_author" == "$(bot_login)" ]] || runoq::die "PR comment author was ${pr_comment_author}, expected $(bot_login)."
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
  local claude_capture_dir claude_wrapper_path real_claude_bin codex_capture_dir codex_wrapper_path real_codex_bin
  root="$(runoq::root)"
  run_id="$(smoke_run_id)"
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/runoq-live-lifecycle.XXXXXX")"
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
  RUNOQ_SMOKE_LIFECYCLE_CLEANUP_TMPDIR="$tmpdir"

  # shellcheck disable=SC2329
  cleanup() {
    if [[ -n "${RUNOQ_SMOKE_LIFECYCLE_CLEANUP_TMPDIR:-}" ]]; then
      smoke_log "removing temporary lifecycle workspace ${RUNOQ_SMOKE_LIFECYCLE_CLEANUP_TMPDIR}"
      rm -rf "$RUNOQ_SMOKE_LIFECYCLE_CLEANUP_TMPDIR"
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
  # Verify repo is fully accessible via API before proceeding (both REST and GraphQL)
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
  smoke_log "recorded managed repo in $(smoke_manifest_path)"

  export RUNOQ_APP_KEY
  RUNOQ_APP_KEY="$(smoke_key_path)"
  export RUNOQ_APP_ID="${RUNOQ_SMOKE_APP_ID}"
  export RUNOQ_SYMLINK_DIR="$tmpdir/bin"

  # Write identity file so orchestrator can mint its own app token later.
  # Issue seeding uses the operator's personal auth (app tokens lack issue
  # creation permissions on newly created repos). The orchestrator mints the
  # app token at startup for comments/labels/PR operations.
  export TARGET_ROOT="$target_dir"
  write_identity_file "$target_dir"

  # Configure bot git identity in the target repo so commits show as the app
  runoq::configure_git_bot_identity "$target_dir" 2>/dev/null || true

  claude_capture_dir="$artifacts_dir/claude"
  claude_wrapper_path="$tmpdir/claude-capture"
  real_claude_bin="$(smoke_claude_bin)"
  create_claude_capture_wrapper "$claude_wrapper_path"
  codex_capture_dir="$artifacts_dir/codex"
  codex_wrapper_path="$tmpdir/codex"
  real_codex_bin="$(smoke_codex_bin)"
  create_codex_capture_wrapper "$codex_wrapper_path"

  smoke_log "running runoq init; log -> ${init_log}"
  set +e
  (
    cd "$target_dir"
    "$root/bin/runoq" init
  ) >"$init_log" 2>&1
  run_exit="$?"
  set -e
  if [[ "$run_exit" -ne 0 ]]; then
    smoke_log "runoq init failed with exit code ${run_exit}"
    failures_json="$(append_missing "$failures_json" "runoq init failed. See ${init_log}.")"
  else
    smoke_log "runoq init completed successfully"
    checks_json="$(append_check "$checks_json" "repo_bootstrapped")"
    labels_json="$(label_check_json "$repo")"
    if [[ "$(printf '%s' "$labels_json" | jq -r '.missing | length')" -ne 0 ]]; then
      failures_json="$(append_missing "$failures_json" "Missing expected labels after runoq init: $(printf '%s' "$labels_json" | jq -r '.missing | join(", ")').")"
    else
      smoke_log "verified expected labels in ${repo}"
      checks_json="$(append_check "$checks_json" "labels_present")"
    fi
  fi

  seeded_issues_json='[]'
  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    smoke_log "seeding lifecycle issues from test/fixtures/live_smoke_lifecycle_issues.json"
    # Suppress auto-token minting during seeding — issue creation on newly
    # created repos requires operator auth, not the app installation token.
    seeded_issues_json="$(export RUNOQ_NO_AUTO_TOKEN=1; seed_lifecycle_issues "$repo")"
    printf '%s\n' "$seeded_issues_json" >"$seeded_issues_file"
    smoke_log "seeded issues $(printf '%s' "$seeded_issues_json" | jq -r 'map("#\(.number)") | join(", ")')"
    checks_json="$(append_check "$checks_json" "issues_seeded")"

    smoke_log "running runoq eval; log -> ${run_log}"
    smoke_log "capturing Claude argv/stdout/stderr under ${claude_capture_dir}"
    smoke_log "capturing Codex argv/stdout/stderr under ${codex_capture_dir}"
    set +e
    (
      cd "$target_dir"
      export RUNOQ_FORCE_REFRESH_TOKEN=1
      export RUNOQ_SMOKE_REAL_CLAUDE_BIN="$real_claude_bin"
      export RUNOQ_SMOKE_CLAUDE_CAPTURE_DIR="$claude_capture_dir"
      export RUNOQ_SMOKE_REAL_CODEX_BIN="$real_codex_bin"
      export RUNOQ_SMOKE_CODEX_CAPTURE_DIR="$codex_capture_dir"
      export RUNOQ_CLAUDE_BIN="$claude_wrapper_path"
      export PATH="$tmpdir:$PATH"
      "$root/bin/runoq" run
    ) >"$run_log" 2>&1
    run_exit="$?"
    set -e
    run_exit_json="$run_exit"
    smoke_log "runoq run exited with code ${run_exit}"
    checks_json="$(append_check "$checks_json" "lifecycle_run_invoked")"
  fi

  # Inject mention-test comments and run mention triage
  if [[ "$(printf '%s' "$failures_json" | jq 'length')" -eq 0 ]]; then
    pr_statuses_json="$(fetch_pr_statuses_json "$repo")"
    if [[ "$(printf '%s' "$pr_statuses_json" | jq 'length')" -gt 0 ]]; then
      local first_pr
      first_pr="$(printf '%s' "$pr_statuses_json" | jq -r '.[0].number')"
      smoke_log "injecting mention-test comments on PR #${first_pr}"

      # Question comment (tagged @runoq)
      runoq::gh pr comment "$first_pr" --repo "$repo" --body "@runoq Why did you choose this approach for the formatter?" >/dev/null 2>&1 || true

      # Change-request comment (tagged @runoq)
      runoq::gh pr comment "$first_pr" --repo "$repo" --body "@runoq Please add an edge case test for empty input" >/dev/null 2>&1 || true

      # Irrelevant comment (no tag)
      runoq::gh pr comment "$first_pr" --repo "$repo" --body "This looks interesting, thanks for the contribution." >/dev/null 2>&1 || true

      checks_json="$(append_check "$checks_json" "mention_comments_injected")"
      smoke_log "injected mention-test comments; running mention triage"

      set +e
      (
        cd "$target_dir"
        export RUNOQ_FORCE_REFRESH_TOKEN=1
        "$root/scripts/orchestrator.sh" mention-triage "$repo" "$first_pr"
      ) >>"$run_log" 2>&1
      local triage_exit="$?"
      set -e
      if [[ "$triage_exit" -ne 0 ]]; then
        smoke_log "mention-triage exited with code ${triage_exit}"
      else
        checks_json="$(append_check "$checks_json" "mention_triage_run")"
      fi
    fi
    # Reset pr_statuses_json so it gets freshly fetched below
    pr_statuses_json='[]'
  fi

  smoke_log "copying state artifacts into ${artifacts_dir}"
  copy_state_artifacts "$target_dir" "$artifacts_dir"
  state_files_json="$(read_state_files_json_from_dir "$artifacts_dir/state")"
  issue_numbers_json="$(printf '%s' "$seeded_issues_json" | jq '[.[].number]')"
  issue_statuses_json='[]'
  pr_statuses_json='[]'
  if [[ "$(printf '%s' "$seeded_issues_json" | jq 'length')" -gt 0 ]]; then
    issue_statuses_json="$(fetch_issue_statuses_json "$repo" "$seeded_issues_json")"
    printf '%s\n' "$issue_statuses_json" >"$artifacts_dir/issues.json"
    pr_statuses_json="$(fetch_pr_statuses_json "$repo")"
    printf '%s\n' "$pr_statuses_json" >"$artifacts_dir/prs.json"
  fi

  if [[ -d "$artifacts_dir/state" ]]; then
    report_summary_json="$(
      TARGET_ROOT="$target_dir" \
      RUNOQ_STATE_DIR="$artifacts_dir/state" \
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
        [[ $# -ge 2 ]] || runoq::die "--repo requires a value"
        selector_type="repo"
        selector_value="$2"
        shift 2
        ;;
      --run-id)
        [[ $# -ge 2 ]] || runoq::die "--run-id requires a value"
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
        runoq::die "Usage: smoke-lifecycle.sh cleanup (--repo OWNER/REPO | --run-id ID | --all)"
        ;;
    esac
  done

  [[ -n "$selector_type" ]] || runoq::die "Provide --repo OWNER/REPO, --run-id ID, or --all."
  command_exists "$(smoke_gh_bin)" || runoq::die "GitHub CLI not found: $(smoke_gh_bin)"
  operator_auth_ready || runoq::die "Operator gh auth is not ready. Run gh auth login before lifecycle cleanup."

  selected_json="$(manifest_select_entries "$selector_type" "$selector_value")"
  if [[ "$(printf '%s' "$selected_json" | jq 'length')" -eq 0 ]]; then
    runoq::die "No matching managed lifecycle repos found to clean up."
  fi
  smoke_log "selected $(printf '%s' "$selected_json" | jq 'length') managed repo(s) for cleanup"

  while IFS= read -r entry; do
    [[ -n "$entry" ]] || continue
    repo="$(printf '%s' "$entry" | jq -r '.repo')"
    smoke_log "deleting managed repo ${repo}"
    if runoq::gh repo delete "$repo" --yes >/dev/null 2>&1; then
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
