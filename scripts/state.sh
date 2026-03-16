#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  state.sh save <issue-number> [--state-dir DIR]
  state.sh load <issue-number> [--state-dir DIR]
  state.sh record-mention <comment-id> [--state-dir DIR]
  state.sh has-mention <comment-id> [--state-dir DIR]
  state.sh extract-payload <codex-output-file>
  state.sh validate-payload <worktree> <base-sha> <codex-output-file>
EOF
}

state_dir_arg=""

parse_state_dir_arg() {
  if [[ "${1:-}" == "--state-dir" ]]; then
    [[ $# -ge 2 ]] || agendev::die "--state-dir requires a value"
    state_dir_arg="$2"
    shift 2
  fi
  printf '%s\n' "$*"
}

state_dir_resolved() {
  if [[ -n "$state_dir_arg" ]]; then
    printf '%s\n' "$state_dir_arg"
  else
    agendev::state_dir
  fi
}

state_file() {
  local issue="$1"
  printf '%s/%s.json\n' "$(state_dir_resolved)" "$issue"
}

validate_phase_transition() {
  local from="$1"
  local to="$2"

  [[ "$from" == "$to" ]] && return 0

  case "$from:$to" in
    INIT:DEVELOP|INIT:FINALIZE|INIT:FAILED|DEVELOP:REVIEW|DEVELOP:FAILED|REVIEW:DECIDE|REVIEW:FAILED|DECIDE:DEVELOP|DECIDE:FINALIZE|DECIDE:FAILED|FINALIZE:DONE|FINALIZE:FAILED)
      return 0
      ;;
    DONE:*|FAILED:*)
      agendev::die "Invalid transition from terminal phase $from to $to"
      ;;
    *)
      agendev::die "Invalid phase transition: $from -> $to"
      ;;
  esac
}

load_state_raw() {
  local issue="$1"
  local file
  file="$(state_file "$issue")"
  [[ -f "$file" ]] || agendev::die "State file not found for issue $issue"
  jq -e '.' "$file" 2>/dev/null || agendev::die "State file is corrupted for issue $issue"
}

save_state() {
  local issue="$1"
  local file current_phase current_started payload tmp now
  agendev::ensure_state_dir
  file="$(state_file "$issue")"
  payload="$(cat)"
  now="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

  printf '%s' "$payload" | jq -e --argjson issue "$issue" '. + {issue:$issue}' >/dev/null || agendev::die "State payload must be valid JSON"

  if [[ -f "$file" ]]; then
    current_phase="$(jq -r '.phase' "$file")"
    current_started="$(jq -r '.started_at // empty' "$file")"
    validate_phase_transition "$current_phase" "$(printf '%s' "$payload" | jq -r '.phase')"
  else
    current_started="$now"
  fi

  tmp="$(mktemp "$(state_dir_resolved)/.${issue}.XXXXXX")"
  printf '%s' "$payload" | jq \
    --argjson issue "$issue" \
    --arg started_at "$current_started" \
    --arg updated_at "$now" '
      . + {
        issue: $issue,
        started_at: (.started_at // $started_at),
        updated_at: $updated_at
      }
    ' >"$tmp"
  mv "$tmp" "$file"
  cat "$file"
}

load_state() {
  local issue="$1"
  load_state_raw "$issue"
}

mentions_file() {
  printf '%s/processed-mentions.json\n' "$(state_dir_resolved)"
}

read_mentions() {
  local file
  file="$(mentions_file)"
  if [[ ! -f "$file" ]]; then
    printf '[]\n'
    return
  fi
  jq -e '.' "$file" 2>/dev/null || agendev::die "Processed mentions file is corrupted"
}

record_mention() {
  local comment_id="$1"
  local file tmp
  agendev::ensure_state_dir
  file="$(mentions_file)"
  tmp="$(mktemp "$(state_dir_resolved)/.mentions.XXXXXX")"
  read_mentions | jq --argjson id "$comment_id" '
    if index($id) then . else . + [$id] end
  ' >"$tmp"
  mv "$tmp" "$file"
  cat "$file"
}

has_mention() {
  local comment_id="$1"
  if read_mentions | jq -e --argjson id "$comment_id" 'index($id) != null' >/dev/null; then
    printf 'true\n'
  else
    printf 'false\n'
    exit 1
  fi
}

case "${1:-}" in
  save)
    shift
    [[ $# -ge 1 ]] || { usage >&2; exit 1; }
    issue="$1"
    shift
    remaining="$(parse_state_dir_arg "$@")"
    # shellcheck disable=SC2086
    set -- $remaining
    [[ $# -eq 0 ]] || { usage >&2; exit 1; }
    save_state "$issue"
    ;;
  load)
    shift
    [[ $# -ge 1 ]] || { usage >&2; exit 1; }
    issue="$1"
    shift
    remaining="$(parse_state_dir_arg "$@")"
    # shellcheck disable=SC2086
    set -- $remaining
    [[ $# -eq 0 ]] || { usage >&2; exit 1; }
    load_state "$issue"
    ;;
  record-mention)
    shift
    [[ $# -ge 1 ]] || { usage >&2; exit 1; }
    comment_id="$1"
    shift
    remaining="$(parse_state_dir_arg "$@")"
    # shellcheck disable=SC2086
    set -- $remaining
    [[ $# -eq 0 ]] || { usage >&2; exit 1; }
    record_mention "$comment_id"
    ;;
  has-mention)
    shift
    [[ $# -ge 1 ]] || { usage >&2; exit 1; }
    comment_id="$1"
    shift
    remaining="$(parse_state_dir_arg "$@")"
    # shellcheck disable=SC2086
    set -- $remaining
    [[ $# -eq 0 ]] || { usage >&2; exit 1; }
    has_mention "$comment_id"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
