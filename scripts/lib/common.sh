#!/usr/bin/env bash

set -euo pipefail

runoq::die() {
  echo "runoq: $*" >&2
  exit 1
}

# Structured logging — only emits when RUNOQ_LOG is non-empty.
# Usage: runoq::log <prefix> <message>
runoq::log() {
  [[ -n "${RUNOQ_LOG:-}" ]] || return 0
  printf '[%s] %s\n' "$1" "$2" >&2
}

runoq::script_dir() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

runoq::root() {
  if [[ -n "${RUNOQ_ROOT:-}" ]]; then
    printf '%s\n' "$RUNOQ_ROOT"
    return
  fi
  runoq::script_dir
}

runoq::config_path() {
  if [[ -n "${RUNOQ_CONFIG:-}" ]]; then
    printf '%s\n' "$RUNOQ_CONFIG"
    return
  fi
  printf '%s/config/runoq.json\n' "$(runoq::root)"
}

runoq::require_cmd() {
  local cmd
  for cmd in "$@"; do
    command -v "$cmd" >/dev/null 2>&1 || runoq::die "Missing required command: $cmd"
  done
}

runoq::config_get() {
  local filter="$1"
  jq -er "$filter" "$(runoq::config_path)"
}

runoq::target_root() {
  if [[ -n "${TARGET_ROOT:-}" ]]; then
    printf '%s\n' "$TARGET_ROOT"
    return
  fi
  git rev-parse --show-toplevel 2>/dev/null || runoq::die "Run runoq from inside a git repository."
}

runoq::origin_url() {
  git -C "$(runoq::target_root)" remote get-url origin 2>/dev/null || runoq::die "No 'origin' remote found. runoq requires a GitHub-hosted repo."
}

runoq::repo_from_remote() {
  local remote="${1:-}"
  if [[ -n "${RUNOQ_REPO:-}" ]]; then
    printf '%s\n' "$RUNOQ_REPO"
    return
  fi

  if [[ -z "$remote" ]]; then
    remote="$(runoq::origin_url)"
  fi

  case "$remote" in
    git@github.com:*)
      remote="${remote#git@github.com:}"
      remote="${remote%.git}"
      printf '%s\n' "$remote"
      ;;
    https://github.com/*|https://*@github.com/*)
      remote="${remote#https://}"
      remote="${remote#*@}"
      remote="${remote#github.com/}"
      remote="${remote%.git}"
      printf '%s\n' "$remote"
      ;;
    ssh://git@github.com/*)
      remote="${remote#ssh://git@github.com/}"
      remote="${remote%.git}"
      printf '%s\n' "$remote"
      ;;
    *)
      runoq::die "Origin remote is not a GitHub URL: $remote"
      ;;
  esac
}

runoq::repo() {
  runoq::repo_from_remote "$(runoq::origin_url)"
}

runoq::state_dir() {
  if [[ -n "${RUNOQ_STATE_DIR:-}" ]]; then
    printf '%s\n' "$RUNOQ_STATE_DIR"
    return
  fi
  printf '%s/.runoq/state\n' "$(runoq::target_root)"
}

runoq::ensure_state_dir() {
  mkdir -p "$(runoq::state_dir)"
}

_RUNOQ_BOT_TOKEN_INIT=0

# Auto-mint a GitHub App installation token using JWT + curl.
# Uses curl (not gh) to avoid circular dependency with runoq::gh().
runoq::_mint_bot_token() {
  local identity_file
  identity_file="$(runoq::target_root 2>/dev/null)/.runoq/identity.json" || return 1
  [[ -f "$identity_file" ]] || return 1

  local app_id installation_id key_path
  app_id="$(jq -r '.appId' "$identity_file")"
  installation_id="$(jq -r '.installationId' "$identity_file")"
  key_path="${RUNOQ_APP_KEY:-$(jq -r '.privateKeyPath // empty' "$identity_file")}"
  key_path="${key_path/#\~/$HOME}"

  [[ -n "$app_id" && "$app_id" != "null" ]] || return 1
  [[ -n "$installation_id" && "$installation_id" != "null" ]] || return 1
  [[ -f "$key_path" ]] || return 1

  # Mint JWT
  local now exp header payload unsigned signature jwt
  now="$(date +%s)"
  exp="$((now + 540))"
  header="$(printf '{"alg":"RS256","typ":"JWT"}' | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
  payload="$(jq -cnj --argjson iat "$now" --argjson exp "$exp" --arg iss "$app_id" \
    '{iat:$iat,exp:$exp,iss:$iss}' | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
  unsigned="${header}.${payload}"
  signature="$(printf '%s' "$unsigned" | openssl dgst -binary -sha256 -sign "$key_path" \
    | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
  jwt="${unsigned}.${signature}"

  # Exchange JWT for installation token via curl (not gh, to avoid re-entry)
  local response token
  response="$(curl -sf -X POST \
    "https://api.github.com/app/installations/${installation_id}/access_tokens" \
    -H "Authorization: Bearer ${jwt}" \
    -H "Accept: application/vnd.github+json" 2>/dev/null)" || return 1
  token="$(printf '%s' "$response" | jq -r '.token // empty')"
  [[ -n "$token" ]] || return 1

  export GH_TOKEN="$token"
}

runoq::gh() {
  if [[ -z "${GH_TOKEN:-}" && "$_RUNOQ_BOT_TOKEN_INIT" -eq 0 ]]; then
    _RUNOQ_BOT_TOKEN_INIT=1
    runoq::_mint_bot_token 2>/dev/null || true
  fi
  local gh_bin="${GH_BIN:-gh}"
  "$gh_bin" "$@"
}

runoq::branch_slug() {
  local raw="$1"
  raw="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
  raw="$(printf '%s' "$raw" | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//; s/-+/-/g')"
  printf '%s\n' "${raw:-issue}"
}

runoq::branch_name() {
  local issue="$1"
  local title="$2"
  local prefix
  prefix="$(runoq::config_get '.branchPrefix')"
  printf '%s%s-%s\n' "$prefix" "$issue" "$(runoq::branch_slug "$title")"
}

runoq::worktree_path() {
  local issue="$1"
  local prefix target_root parent
  prefix="$(runoq::config_get '.worktreePrefix')"
  target_root="$(runoq::target_root)"
  parent="$(cd "$target_root/.." && pwd)"
  printf '%s/%s%s\n' "$parent" "$prefix" "$issue"
}

runoq::json_tmp() {
  mktemp "${TMPDIR:-/tmp}/runoq.XXXXXX"
}

runoq::write_json_file() {
  local path="$1"
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/runoq-write.XXXXXX")"
  cat >"$tmp"
  mv "$tmp" "$path"
}

runoq::label_keys_json() {
  jq '.labels' "$(runoq::config_path)"
}

runoq::all_state_labels() {
  runoq::label_keys_json | jq -r '.[]'
}

runoq::label_for_status() {
  local status="$1"
  jq -er --arg status "$status" '
    .labels[
      if $status == "ready" then "ready"
      elif $status == "in-progress" then "inProgress"
      elif $status == "done" then "done"
      elif $status == "needs-review" then "needsReview"
      elif $status == "blocked" then "blocked"
      else error("unknown status")
      end
    ]
  ' "$(runoq::config_path)" 2>/dev/null || runoq::die "Unknown status: $status"
}

runoq::default_branch_ref() {
  printf '%s\n' "${RUNOQ_BASE_REF:-origin/main}"
}

runoq::absolute_path() {
  local path="$1"
  if [[ "$path" = /* ]]; then
    printf '%s\n' "$path"
  else
    printf '%s/%s\n' "$(pwd)" "$path"
  fi
}

# ---------------------------------------------------------------------------
# Retry helper for eventual consistency
# ---------------------------------------------------------------------------

# Retry a command up to N times with a pause between attempts.
# Usage: runoq::retry <max_attempts> <pause_seconds> <command...>
# Returns the exit code of the last attempt.
runoq::retry() {
  local max_attempts="$1" pause="$2"
  shift 2
  local attempt=1
  while true; do
    if "$@"; then
      return 0
    fi
    if (( attempt >= max_attempts )); then
      runoq::log "retry" "all ${max_attempts} attempts failed for: $*"
      return 1
    fi
    runoq::log "retry" "attempt ${attempt}/${max_attempts} failed, retrying in ${pause}s: $*"
    sleep "$pause"
    attempt=$((attempt + 1))
  done
}

# ---------------------------------------------------------------------------
# Bot identity for git operations
# ---------------------------------------------------------------------------

# Returns the GitHub App ID from the identity file or RUNOQ_APP_ID env var.
runoq::app_id() {
  if [[ -n "${RUNOQ_APP_ID:-}" ]]; then
    printf '%s\n' "$RUNOQ_APP_ID"
    return
  fi
  local identity_file
  identity_file="$(runoq::target_root)/.runoq/identity.json"
  if [[ -f "$identity_file" ]]; then
    jq -r '.appId' "$identity_file"
  fi
}

# Returns the app slug from config (e.g. "runoq").
runoq::app_slug() {
  runoq::config_get '.identity.appSlug'
}

# Configure git user identity in a directory to match the GitHub App bot.
# Usage: runoq::configure_git_bot_identity <dir>
runoq::configure_git_bot_identity() {
  local dir="$1"
  local app_id slug
  slug="$(runoq::app_slug)"
  app_id="$(runoq::app_id)"
  [[ -n "$slug" ]] || return 0
  git -C "$dir" config user.name "${slug}[bot]"
  if [[ -n "$app_id" ]]; then
    git -C "$dir" config user.email "${app_id}+${slug}[bot]@users.noreply.github.com"
  fi
}

# Rewrite a remote to use HTTPS with the current GH_TOKEN so pushes
# are authenticated as the bot. No-op if GH_TOKEN is unset.
# Usage: runoq::configure_git_bot_remote <dir> <repo> [remote]
runoq::configure_git_bot_remote() {
  local dir="$1" repo="$2" remote="${3:-origin}"
  [[ -n "${GH_TOKEN:-}" ]] || return 0
  git -C "$dir" remote set-url "$remote" "https://x-access-token:${GH_TOKEN}@github.com/${repo}.git"
}
