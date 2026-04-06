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
  if [[ -n "${RUNOQ_APP_ID:-}" ]]; then
    printf '%s\n' "$RUNOQ_APP_ID"
    return
  fi

  operator_gh api "/apps/${expected_slug}" --jq '.id' 2>/dev/null || runoq::die "Unable to resolve app ID for ${expected_slug}. For private GitHub Apps, set RUNOQ_APP_ID before running runoq init."
}

ensure_identity() {
  local root repo expected_slug app_id installation_id installation_json installation_slug key_path identity_path jwt
  root="$(runoq::target_root)"
  repo="$(runoq::repo)"
  expected_slug="$(runoq::config_get '.identity.appSlug')"
  identity_path="$root/.runoq/identity.json"

  mkdir -p "$root/.runoq" "$(runoq::state_dir)"
  if [[ -f "$identity_path" ]]; then
    jq -e '.appId and .installationId and .privateKeyPath' "$identity_path" >/dev/null 2>&1 && return
  fi

  key_path="${RUNOQ_APP_KEY:-$HOME/.runoq/app-key.pem}"
  [[ -f "${key_path/#\~/$HOME}" ]] || runoq::die "GitHub App private key not found at ${key_path/#\~/$HOME}. Set RUNOQ_APP_KEY or install the key before running runoq init."

  app_id="$(resolve_bootstrap_app_id "$expected_slug")"
  jwt="$(mint_bootstrap_jwt "$app_id" "${key_path/#\~/$HOME}")"
  if ! installation_json="$(app_gh "$jwt" "/repos/${repo}/installation" 2>&1)"; then
    if [[ "$installation_json" == *"404"* || "$installation_json" == *"Not Found"* ]]; then
      runoq::die "GitHub App installation not found for ${repo}. Install the app on this repository, then rerun runoq init."
    fi
    runoq::die "Failed to resolve GitHub App installation for ${repo}: ${installation_json}"
  fi
  app_id="$(printf '%s' "$installation_json" | jq -er '.app_id')"
  installation_id="$(printf '%s' "$installation_json" | jq -er '.id')"
  installation_slug="$(printf '%s' "$installation_json" | jq -r '.app_slug // empty')"

  if [[ -n "$expected_slug" && -n "$installation_slug" && "$installation_slug" != "$expected_slug" ]]; then
    runoq::die "Repository installation app slug ${installation_slug} did not match configured identity.appSlug ${expected_slug}."
  fi

  jq -n \
    --argjson appId "$app_id" \
    --argjson installationId "$installation_id" \
    --arg privateKeyPath "$key_path" \
    '{appId:$appId, installationId:$installationId, privateKeyPath:$privateKeyPath}' \
    >"$identity_path"
}

ensure_labels() {
  local repo existing label delay attempt
  repo="$(runoq::repo)"
  delay="${RUNOQ_SETUP_LABEL_RETRY_DELAY_SECONDS:-3}"
  existing="$(operator_gh label list --repo "$repo" --limit 200 --json name | jq -r '.[].name')"
  while IFS= read -r label; do
    if ! grep -Fxq "$label" <<<"$existing"; then
      for attempt in 1 2 3; do
        if operator_gh label create "$label" --repo "$repo" --color BFDADC --description "Managed by runoq" >/dev/null; then
          break
        fi
        if [[ "$attempt" -eq 3 ]]; then
          return 1
        fi
        sleep "$delay"
      done
    fi
  done < <(runoq::all_state_labels)
}

ensure_package_json() {
  local target_root
  target_root="$(runoq::target_root)"
  if [[ ! -f "$target_root/package.json" ]]; then
    cat >"$target_root/package.json" <<'EOF'
{
  "name": "runoq-target",
  "private": true,
  "scripts": {
    "test": "echo \"No tests configured\"",
    "build": "echo \"No build configured\""
  }
}
EOF
    git -C "$target_root" add package.json
  fi
}

ensure_directory() {
  local path="$1"
  if [[ -e "$path" && ! -d "$path" ]]; then
    runoq::die "Cannot create ${path}; path exists and is not a directory."
  fi
  mkdir -p "$path"
}

sync_claude_managed_file() {
  local source_path="$1"
  local destination_path="$2"
  local resolved_destination

  ensure_directory "$(dirname "$destination_path")"

  if [[ -e "$destination_path" && ! -f "$destination_path" && ! -L "$destination_path" ]]; then
    runoq::die "Cannot update managed Claude file at ${destination_path}; path exists and is not a regular file."
  fi

  if [[ -L "$destination_path" ]]; then
    resolved_destination="$(readlink "$destination_path")"
    if [[ "$resolved_destination" == "$source_path" ]]; then
      return
    fi
  fi

  rm -f "$destination_path"
  ln -s "$source_path" "$destination_path"
}

ensure_claude_managed_tree() {
  local source_root="$1"
  local destination_root="$2"
  local source_path rel_path

  while IFS= read -r source_path; do
    [[ -n "$source_path" ]] || continue
    rel_path="${source_path#"$source_root"/}"
    sync_claude_managed_file "$source_path" "$destination_root/$rel_path"
  done < <(find "$source_root" \( -type f -o -type l \) | LC_ALL=C sort)
}

ensure_claude_managed_files() {
  local runoq_root target_root
  runoq_root="$(runoq::root)"
  target_root="$(runoq::target_root)"

  ensure_directory "$target_root/.claude"
  ensure_directory "$target_root/.claude/agents"
  ensure_directory "$target_root/.claude/skills"

  ensure_claude_managed_tree "$runoq_root/.claude/agents" "$target_root/.claude/agents"
  ensure_claude_managed_tree "$runoq_root/.claude/skills" "$target_root/.claude/skills"
}

ensure_gitignore() {
  local target_root gitignore
  target_root="$(runoq::target_root)"
  gitignore="$target_root/.gitignore"

  local entries=(".runoq/")
  local missing=()
  for entry in "${entries[@]}"; do
    if [[ ! -f "$gitignore" ]] || ! grep -Fxq "$entry" "$gitignore"; then
      missing+=("$entry")
    fi
  done

  if [[ ${#missing[@]} -eq 0 ]]; then
    return
  fi

  # Ensure trailing newline before appending
  if [[ -f "$gitignore" ]] && [[ -s "$gitignore" ]] && [[ "$(tail -c1 "$gitignore" | xxd -p)" != "0a" ]]; then
    printf '\n' >>"$gitignore"
  fi

  for entry in "${missing[@]}"; do
    printf '%s\n' "$entry" >>"$gitignore"
  done

  git -C "$target_root" add .gitignore
}

write_project_config() {
  local plan_path="$1"
  local target_root config_path tmp
  target_root="$(runoq::target_root)"
  config_path="$target_root/runoq.json"
  tmp="$(mktemp "${TMPDIR:-/tmp}/runoq-project-config.XXXXXX")"

  if [[ -f "$config_path" ]]; then
    jq --arg plan "$plan_path" '.plan = $plan' "$config_path" >"$tmp" ||
      runoq::die "Failed to update ${config_path}; expected valid JSON."
  else
    jq -n --arg plan "$plan_path" '{plan: $plan}' >"$tmp"
  fi

  mv "$tmp" "$config_path"
  git -C "$target_root" add runoq.json
}

ensure_symlink() {
  local link_dir link_path target
  link_dir="${RUNOQ_SYMLINK_DIR:-/usr/local/bin}"
  link_path="${link_dir}/runoq"
  target="$(runoq::root)/bin/runoq"

  if ! mkdir -p "$link_dir" 2>/dev/null || [[ ! -w "$link_dir" ]]; then
    printf 'Warning: %s is not writable. Set RUNOQ_SYMLINK_DIR to a writable directory on your PATH.\n' "$link_dir" >&2
    return 0
  fi
  if [[ -L "$link_path" ]]; then
    [[ "$(readlink "$link_path")" == "$target" ]] && return
    rm -f "$link_path"
  elif [[ -e "$link_path" ]]; then
    runoq::die "Cannot update ${link_path}; file exists and is not a symlink."
  fi
  ln -s "$target" "$link_path"
}

main() {
  local plan_path=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --plan)
        [[ $# -ge 2 ]] || runoq::die "Missing value for --plan."
        plan_path="$2"
        shift 2
        ;;
      *)
        runoq::die "Unknown option: $1"
        ;;
    esac
  done

  mkdir -p "$(runoq::target_root)/.runoq" "$(runoq::state_dir)"
  ensure_identity
  ensure_labels
  ensure_package_json
  ensure_claude_managed_files
  ensure_gitignore
  if [[ -n "$plan_path" ]]; then
    write_project_config "$plan_path"
  fi
  ensure_symlink
}

main "$@"
