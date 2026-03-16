#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  live-smoke.sh preflight
  live-smoke.sh run
EOF
}

smoke_key_path() {
  local key_path="${AGENDEV_SMOKE_APP_KEY:-}"
  key_path="${key_path/#\~/$HOME}"
  printf '%s\n' "$key_path"
}

append_missing() {
  local missing_json="$1"
  local message="$2"
  jq -n --argjson missing "$missing_json" --arg message "$message" '$missing + [$message]'
}

preflight_json() {
  local missing enabled key_path
  missing='[]'
  enabled=false
  key_path="$(smoke_key_path)"

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

  cleanup() {
    if [[ -n "$pr_number" ]]; then
      agendev::gh pr close "$pr_number" --repo "$repo" --comment "Closing agendev live smoke PR ${run_id}." >/dev/null 2>&1 || true
    fi
    if [[ -n "$branch" && -d "$clone_dir/.git" ]]; then
      git -C "$clone_dir" push origin --delete "$branch" >/dev/null 2>&1 || true
    fi
    if [[ -n "$issue_number" ]]; then
      agendev::gh issue close "$issue_number" --repo "$repo" --comment "Closing agendev live smoke issue ${run_id}." >/dev/null 2>&1 || true
    fi
    rm -rf "$tmpdir"
  }
  trap cleanup EXIT

  require_preflight
  mkdir -p "$auth_root"
  export TARGET_ROOT="$auth_root"
  export AGENDEV_STATE_DIR="$auth_root/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  write_identity_file "$auth_root"
  export AGENDEV_APP_KEY
  AGENDEV_APP_KEY="$(smoke_key_path)"
  export AGENDEV_FORCE_REFRESH_TOKEN=1
  eval "$("$root/scripts/gh-auth.sh" export-token)"
  git clone "https://x-access-token:${GH_TOKEN}@github.com/${repo}.git" "$clone_dir" >/dev/null 2>&1

  export TARGET_ROOT="$clone_dir"
  export REPO="$repo"
  export AGENDEV_STATE_DIR="$clone_dir/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  write_identity_file "$clone_dir"

  export AGENDEV_SYMLINK_DIR="$tmpdir/bin"
  "$root/scripts/setup.sh"

  labels_json="$(label_check_json "$repo")"
  if [[ "$(printf '%s' "$labels_json" | jq -r '.missing | length')" -ne 0 ]]; then
    agendev::die "Missing expected labels after setup: $(printf '%s' "$labels_json" | jq -r '.missing | join(", ")')"
  fi

  issue_title="agendev live smoke ${run_id}"
  issue_body="Live smoke validation issue for ${run_id}."
  issue_json="$("$root/scripts/gh-issue-queue.sh" create "$repo" "$issue_title" "$issue_body" --priority 3 --estimated-complexity low)"
  issue_url="$(printf '%s' "$issue_json" | jq -r '.url')"
  issue_number="$(issue_number_from_url "$issue_url")"
  [[ -n "$issue_number" ]] || agendev::die "Failed to parse smoke issue number."

  issue_comment_body="agendev live smoke issue comment ${run_id}"
  agendev::gh issue comment "$issue_number" --repo "$repo" --body "$issue_comment_body" >/dev/null
  issue_comment_author="$(find_comment_author "$repo" "$issue_number" "$issue_comment_body")"
  [[ "$issue_comment_author" == "$(bot_login)" ]] || agendev::die "Issue comment author was ${issue_comment_author}, expected $(bot_login)."

  permission_json="$("$root/scripts/gh-pr-lifecycle.sh" check-permission "$repo" "$AGENDEV_SMOKE_PERMISSION_USER" "${AGENDEV_SMOKE_PERMISSION_LEVEL:-write}")"
  current_branch="$(default_branch "$repo")"
  branch="agendev-smoke-${run_id}"

  git -C "$clone_dir" checkout -b "$branch" "origin/${current_branch}" >/dev/null 2>&1
  git -C "$clone_dir" config user.name "agendev live smoke"
  git -C "$clone_dir" config user.email "agendev-smoke@example.com"
  mkdir -p "$clone_dir/.agendev/smoke"
  smoke_file="$clone_dir/.agendev/smoke/${run_id}.md"
  printf 'agendev live smoke %s\n' "$run_id" >"$smoke_file"
  git -C "$clone_dir" add ".agendev/smoke/${run_id}.md"
  git -C "$clone_dir" commit -m "agendev live smoke ${run_id}" >/dev/null
  git -C "$clone_dir" push origin "$branch" >/dev/null 2>&1

  pr_title="agendev live smoke ${run_id}"
  pr_json="$("$root/scripts/gh-pr-lifecycle.sh" create "$repo" "$branch" "$issue_number" "$pr_title")"
  pr_url="$(printf '%s' "$pr_json" | jq -r '.url')"
  pr_number="$(pr_number_from_url "$pr_url")"
  [[ -n "$pr_number" ]] || agendev::die "Failed to parse smoke PR number."

  pr_comment_file="$tmpdir/pr-comment.md"
  pr_comment_body="agendev live smoke pr comment ${run_id}"
  printf '%s\n' "$pr_comment_body" >"$pr_comment_file"
  "$root/scripts/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$pr_comment_file" >/dev/null
  pr_comment_author="$(find_comment_author "$repo" "$pr_number" "$pr_comment_body")"
  [[ "$pr_comment_author" == "$(bot_login)" ]] || agendev::die "PR comment author was ${pr_comment_author}, expected $(bot_login)."

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

case "${1:-}" in
  preflight)
    preflight_json
    ;;
  run)
    run_smoke
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
