#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

ensure_identity() {
  local root repo expected_slug app_id installation_id installation_json installation_slug key_path identity_path
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

  installation_json="$(agendev::gh api "/repos/${repo}/installation")"
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
  existing="$(agendev::gh label list --repo "$repo" --limit 200 --json name | jq -r '.[].name')"
  while IFS= read -r label; do
    if ! grep -Fxq "$label" <<<"$existing"; then
      agendev::gh label create "$label" --repo "$repo" --color BFDADC --description "Managed by agendev" >/dev/null
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
  ensure_symlink
}

main "$@"
