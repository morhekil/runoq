#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  gh-auth.sh export-token
  gh-auth.sh print-identity
EOF
}

identity_file() {
  printf '%s/.agendev/identity.json\n' "$(agendev::target_root)"
}

identity_json() {
  local file
  file="$(identity_file)"
  [[ -f "$file" ]] || agendev::die "No .agendev/identity.json found. Run 'agendev init' first."
  cat "$file"
}

resolve_key_path() {
  local key_path
  key_path="${AGENDEV_APP_KEY:-$(identity_json | jq -r '.privateKeyPath // empty')}"
  if [[ -z "$key_path" ]]; then
    agendev::die "No GitHub App private key configured. Run 'agendev init' first."
  fi
  key_path="${key_path/#\~/$HOME}"
  [[ -f "$key_path" ]] || agendev::die "GitHub App private key not found: $key_path"
  printf '%s\n' "$key_path"
}

base64url() {
  openssl base64 -A | tr '+/' '-_' | tr -d '='
}

mint_jwt() {
  local app_id key_path header payload now exp unsigned signature
  app_id="$(identity_json | jq -r '.appId')"
  key_path="$(resolve_key_path)"
  now="$(date +%s)"
  exp="$((now + 540))"

  header="$(printf '{"alg":"RS256","typ":"JWT"}' | base64url)"
  payload="$(jq -cn --argjson iat "$now" --argjson exp "$exp" --arg iss "$app_id" '{iat:$iat,exp:$exp,iss:$iss}' | base64url)"
  unsigned="${header}.${payload}"
  signature="$(printf '%s' "$unsigned" | openssl dgst -binary -sha256 -sign "$key_path" | base64url)"
  printf '%s.%s\n' "$unsigned" "$signature"
}

export_token() {
  local installation_id jwt response token
  if [[ -n "${GH_TOKEN:-}" && -z "${AGENDEV_FORCE_REFRESH_TOKEN:-}" ]]; then
    printf 'export GH_TOKEN=%q\n' "$GH_TOKEN"
    return
  fi

  if [[ -n "${AGENDEV_TEST_GH_TOKEN:-}" ]]; then
    printf 'export GH_TOKEN=%q\n' "$AGENDEV_TEST_GH_TOKEN"
    return
  fi

  installation_id="$(identity_json | jq -r '.installationId')"
  jwt="$(mint_jwt)"
  response="$(agendev::gh api \
    --method POST \
    "/app/installations/${installation_id}/access_tokens" \
    -H "Authorization: Bearer ${jwt}" \
    -H "Accept: application/vnd.github+json")"
  token="$(printf '%s' "$response" | jq -r '.token')"
  [[ -n "$token" && "$token" != "null" ]] || agendev::die "Failed to mint GitHub App installation token."
  printf 'export GH_TOKEN=%q\n' "$token"
}

case "${1:-}" in
  export-token)
    export_token
    ;;
  print-identity)
    identity_json | jq '.'
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
