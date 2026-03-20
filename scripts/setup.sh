#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

operator_gh() {
  local gh_bin="${GH_BIN:-gh}"
  env -u GH_TOKEN -u GITHUB_TOKEN "$gh_bin" "$@"
}

app_gh() {
  local jwt="$1"
  shift
  operator_gh api \
    -H "Authorization: Bearer ${jwt}" \
    -H "Accept: application/vnd.github+json" \
    "$@"
}

base64url() {
  openssl base64 -A | tr '+/' '-_' | tr -d '='
}

compact_json() {
  jq -cnj "$@"
}

mint_bootstrap_jwt() {
  local app_id="$1"
  local key_path="$2"
  local header payload now exp unsigned signature
  now="$(( $(date +%s) - 60 ))"
  exp="$((now + 540))"

  header="$(printf '{"alg":"RS256","typ":"JWT"}' | base64url)"
  payload="$(compact_json --argjson iat "$now" --argjson exp "$exp" --arg iss "$app_id" '{iat:$iat,exp:$exp,iss:$iss}' | base64url)"
  unsigned="${header}.${payload}"
  signature="$(printf '%s' "$unsigned" | openssl dgst -binary -sha256 -sign "$key_path" | base64url)"
  printf '%s.%s\n' "$unsigned" "$signature"
}

resolve_bootstrap_app_id() {
  local expected_slug="$1"
  if [[ -n "${AGENDEV_APP_ID:-}" ]]; then
    printf '%s\n' "$AGENDEV_APP_ID"
    return
  fi

  operator_gh api "/apps/${expected_slug}" --jq '.id' 2>/dev/null || agendev::die "Unable to resolve app ID for ${expected_slug}. For private GitHub Apps, set AGENDEV_APP_ID before running agendev init."
}

ensure_identity() {
  local root repo expected_slug app_id installation_id installation_json installation_slug key_path identity_path jwt
  root="$(agendev::target_root)"
  repo="$(agendev::repo)"
  expected_slug="$(agendev::config_get '.identity.appSlug')"
  identity_path="$root/.agendev/identity.json"

  mkdir -p "$root/.agendev" "$(agendev::state_dir)"
  if [[ -f "$identity_path" ]]; then
    jq -e '.appId and .installationId and .privateKeyPath' "$identity_path" >/dev/null 2>&1 && return
  fi

  key_path="${AGENDEV_APP_KEY:-$HOME/.agendev/app-key.pem}"
  [[ -f "${key_path/#\~/$HOME}" ]] || agendev::die "GitHub App private key not found at ${key_path/#\~/$HOME}. Set AGENDEV_APP_KEY or install the key before running agendev init."

  app_id="$(resolve_bootstrap_app_id "$expected_slug")"
  jwt="$(mint_bootstrap_jwt "$app_id" "${key_path/#\~/$HOME}")"
  installation_json="$(app_gh "$jwt" "/repos/${repo}/installation")"
  app_id="$(printf '%s' "$installation_json" | jq -er '.app_id')"
  installation_id="$(printf '%s' "$installation_json" | jq -er '.id')"
  installation_slug="$(printf '%s' "$installation_json" | jq -r '.app_slug // empty')"

  if [[ -n "$expected_slug" && -n "$installation_slug" && "$installation_slug" != "$expected_slug" ]]; then
    agendev::die "Repository installation app slug ${installation_slug} did not match configured identity.appSlug ${expected_slug}."
  fi

  jq -n \
    --argjson appId "$app_id" \
    --argjson installationId "$installation_id" \
    --arg privateKeyPath "$key_path" \
    '{appId:$appId, installationId:$installationId, privateKeyPath:$privateKeyPath}' \
    >"$identity_path"
}

ensure_labels() {
  local repo existing label
  repo="$(agendev::repo)"
  existing="$(operator_gh label list --repo "$repo" --limit 200 --json name | jq -r '.[].name')"
  while IFS= read -r label; do
    if ! grep -Fxq "$label" <<<"$existing"; then
      operator_gh label create "$label" --repo "$repo" --color BFDADC --description "Managed by agendev" >/dev/null
    fi
  done < <(agendev::all_state_labels)
}

ensure_package_json() {
  local target_root
  target_root="$(agendev::target_root)"
  if [[ ! -f "$target_root/package.json" ]]; then
    cat >"$target_root/package.json" <<'EOF'
{
  "name": "agendev-target",
  "private": true,
  "scripts": {
    "test": "echo \"No tests configured\"",
    "build": "echo \"No build configured\""
  }
}
EOF
  fi
}

ensure_directory() {
  local path="$1"
  if [[ -e "$path" && ! -d "$path" ]]; then
    agendev::die "Cannot create ${path}; path exists and is not a directory."
  fi
  mkdir -p "$path"
}

sync_claude_managed_file() {
  local source_path="$1"
  local destination_path="$2"

  ensure_directory "$(dirname "$destination_path")"

  if [[ -e "$destination_path" && ! -f "$destination_path" && ! -L "$destination_path" ]]; then
    agendev::die "Cannot update managed Claude file at ${destination_path}; path exists and is not a regular file."
  fi

  if [[ -f "$destination_path" && ! -L "$destination_path" ]] && cmp -s "$source_path" "$destination_path"; then
    return
  fi

  rm -f "$destination_path"
  cp "$source_path" "$destination_path"
}

ensure_claude_managed_tree() {
  local source_root="$1"
  local destination_root="$2"
  local source_path rel_path

  while IFS= read -r source_path; do
    [[ -n "$source_path" ]] || continue
    rel_path="${source_path#"$source_root"/}"
    sync_claude_managed_file "$source_path" "$destination_root/$rel_path"
  done < <(find "$source_root" -type f | LC_ALL=C sort)
}

ensure_claude_managed_files() {
  local agendev_root target_root
  agendev_root="$(agendev::root)"
  target_root="$(agendev::target_root)"

  ensure_directory "$target_root/.claude"
  ensure_directory "$target_root/.claude/agents"
  ensure_directory "$target_root/.claude/skills"

  ensure_claude_managed_tree "$agendev_root/.claude/agents" "$target_root/.claude/agents"
  ensure_claude_managed_tree "$agendev_root/.claude/skills" "$target_root/.claude/skills"
}

ensure_symlink() {
  local link_dir link_path target
  link_dir="${AGENDEV_SYMLINK_DIR:-/usr/local/bin}"
  link_path="${link_dir}/agendev"
  target="$(agendev::root)/bin/agendev"

  mkdir -p "$link_dir"
  if [[ -L "$link_path" ]]; then
    [[ "$(readlink "$link_path")" == "$target" ]] && return
    rm -f "$link_path"
  elif [[ -e "$link_path" ]]; then
    agendev::die "Cannot update ${link_path}; file exists and is not a symlink."
  fi
  ln -s "$target" "$link_path"
}

main() {
  mkdir -p "$(agendev::target_root)/.agendev" "$(agendev::state_dir)"
  ensure_identity
  ensure_labels
  ensure_package_json
  ensure_claude_managed_files
  ensure_symlink
}

main "$@"
