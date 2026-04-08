#!/usr/bin/env bash

set -euo pipefail

# Trimmed common.sh — only functions used by smoke tests

_RUNOQ_BOT_TOKEN_INIT=0

runoq::die() {
  printf '%b%s%b\n' '\033[1;31m' "runoq: $*" '\033[0m' >&2
  exit 1
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
  if [[ -z "${GH_TOKEN:-}" && -z "${RUNOQ_NO_AUTO_TOKEN:-}" && "$_RUNOQ_BOT_TOKEN_INIT" -eq 0 ]]; then
    _RUNOQ_BOT_TOKEN_INIT=1
    runoq::_mint_bot_token 2>/dev/null || true
  fi
  local gh_bin="${GH_BIN:-gh}"
  "$gh_bin" "$@"
}

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

runoq::app_slug() {
  runoq::config_get '.identity.appSlug'
}

runoq::branch_slug() {
  local raw="$1"
  raw="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
  raw="$(printf '%s' "$raw" | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//; s/-+/-/g')"
  printf '%s\n' "${raw:-issue}"
}

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

runoq::label_keys_json() {
  jq '.labels' "$(runoq::config_path)"
}

runoq::all_state_labels() {
  runoq::label_keys_json | jq -r '.[]'
}

runoq::origin_url() {
  git -C "$(runoq::target_root)" remote get-url origin 2>/dev/null || runoq::die "No 'origin' remote found."
}

runoq::repo_from_remote() {
  local remote="${1:-}"
  if [[ -n "${RUNOQ_REPO:-}" ]]; then
    printf '%s\n' "$RUNOQ_REPO"
    return
  fi
  [[ -z "$remote" ]] && remote="$(runoq::origin_url)"
  case "$remote" in
    git@github.com:*)
      remote="${remote#git@github.com:}"; remote="${remote%.git}"; printf '%s\n' "$remote" ;;
    https://github.com/*|https://*@github.com/*)
      remote="${remote#https://}"; remote="${remote#*@}"; remote="${remote#github.com/}"; remote="${remote%.git}"; printf '%s\n' "$remote" ;;
    ssh://git@github.com/*)
      remote="${remote#ssh://git@github.com/}"; remote="${remote%.git}"; printf '%s\n' "$remote" ;;
    *) runoq::die "Origin remote is not a GitHub URL: $remote" ;;
  esac
}

runoq::repo() {
  runoq::repo_from_remote "$(runoq::origin_url)"
}

runoq::plan_file() {
  local project_config
  project_config="$(runoq::target_root)/runoq.json"
  [[ -f "$project_config" ]] || runoq::die "Missing project config: ${project_config}."
  jq -er '.plan | select(type == "string" and length > 0)' "$project_config" 2>/dev/null ||
    runoq::die "Invalid project config: ${project_config} is missing a non-empty .plan string."
}





