#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  mentions.sh process <repo> <agent-handle> [--since <timestamp>]
EOF
}

comment_file() {
  mktemp "${TMPDIR:-/tmp}/agendev-mention.XXXXXX"
}

post_pr_event() {
  local repo="$1"
  local pr_number="$2"
  local message="$3"
  local path
  path="$(comment_file)"
  {
    echo "<!-- agendev:event -->"
    echo "$message"
  } >"$path"
  "$(agendev::root)/scripts/gh-pr-lifecycle.sh" comment "$repo" "$pr_number" "$path" >/dev/null 2>&1 || true
  rm -f "$path"
}

post_issue_event() {
  local repo="$1"
  local issue_number="$2"
  local message="$3"
  local body
  body="$(printf '<!-- agendev:event -->\n%s\n' "$message")"
  agendev::gh issue comment "$issue_number" --repo "$repo" --body "$body" >/dev/null 2>&1 || true
}

required_permission() {
  agendev::config_get '.authorization.minimumPermission'
}

deny_response() {
  agendev::config_get '.authorization.denyResponse'
}

process_mentions() {
  local repo="$1"
  local handle="$2"
  local since="${3:-}"
  local mentions required deny_mode mention
  local author comment_id context_type pr_number issue_number permission_json permission_status permission action message

  required="$(required_permission)"
  deny_mode="$(deny_response)"
  if [[ -n "$since" ]]; then
    mentions="$("$(agendev::root)/scripts/gh-pr-lifecycle.sh" poll-mentions "$repo" "$handle" --since "$since")"
  else
    mentions="$("$(agendev::root)/scripts/gh-pr-lifecycle.sh" poll-mentions "$repo" "$handle")"
  fi

  while IFS= read -r mention; do
    [[ -n "$mention" ]] || continue
    author="$(printf '%s' "$mention" | jq -r '.author')"
    comment_id="$(printf '%s' "$mention" | jq -r '.comment_id')"
    context_type="$(printf '%s' "$mention" | jq -r '.context_type')"
    pr_number="$(printf '%s' "$mention" | jq -r '.pr_number // empty')"
    issue_number="$(printf '%s' "$mention" | jq -r '.issue_number // empty')"

    set +e
    permission_json="$("$(agendev::root)/scripts/gh-pr-lifecycle.sh" check-permission "$repo" "$author" "$required" 2>/dev/null)"
    permission_status="$?"
    set -e
    permission="$(printf '%s' "$permission_json" | jq -r '.permission // "none"')"

    action="process"
    if [[ "$permission_status" -ne 0 ]]; then
      if [[ "$deny_mode" == "comment" ]]; then
        action="deny"
        message="Permission denied for @${author}. Requires ${required} access to address @${handle} mentions."
        if [[ "$context_type" == "pr" ]]; then
          post_pr_event "$repo" "$pr_number" "$message"
        else
          post_issue_event "$repo" "$issue_number" "$message"
        fi
      else
        action="ignore"
      fi
    fi

    "$(agendev::root)/scripts/state.sh" record-mention "$comment_id" >/dev/null
    jq -n \
      --argjson mention "$mention" \
      --arg action "$action" \
      --arg permission "$permission" '
      {
        comment_id: $mention.comment_id,
        author: $mention.author,
        action: $action,
        permission: $permission,
        context_type: $mention.context_type,
        pr_number: $mention.pr_number,
        issue_number: $mention.issue_number
      }
    '
  done < <(printf '%s' "$mentions" | jq -c '.[]') | jq -s '.'
}

case "${1:-}" in
  process)
    if [[ $# -eq 3 ]]; then
      process_mentions "$2" "$3"
    elif [[ $# -eq 5 && "$4" == "--since" ]]; then
      process_mentions "$2" "$3" "$5"
    else
      usage >&2
      exit 1
    fi
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
