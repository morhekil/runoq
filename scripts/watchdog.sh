#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  watchdog.sh [--timeout SECONDS] [--issue N] [--state-dir DIR] -- command [args...]
EOF
}

timeout_seconds="$(runoq::config_get '.stall.timeoutSeconds')"
issue_number=""
state_dir_override=""

write_stall_marker() {
  local issue="$1"
  local timeout="$2"
  shift 2
  local command_string="$*"
  local state_dir file tmp detected_at

  [[ -n "$issue" ]] || return 0

  if [[ -n "$state_dir_override" ]]; then
    state_dir="$state_dir_override"
  else
    state_dir="$(runoq::state_dir)"
  fi

  mkdir -p "$state_dir"
  file="$state_dir/$issue.json"
  tmp="$(mktemp "$state_dir/.${issue}.stall.XXXXXX")"
  detected_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

  if [[ -f "$file" ]] && jq -e '.' "$file" >/dev/null 2>&1; then
    jq \
      --arg detected_at "$detected_at" \
      --arg command "$command_string" \
      --argjson timeout "$timeout" '
      . + {
        updated_at: $detected_at,
        stall: {
          timed_out: true,
          timeout_seconds: $timeout,
          detected_at: $detected_at,
          exit_code: 124,
          command: $command,
          last_phase: (.phase // null),
          last_round: (.round // null)
        }
      }
    ' "$file" >"$tmp"
  else
    jq -n \
      --arg detected_at "$detected_at" \
      --arg command "$command_string" \
      --argjson timeout "$timeout" \
      --argjson issue "$issue" '
      {
        issue: $issue,
        updated_at: $detected_at,
        stall: {
          timed_out: true,
          timeout_seconds: $timeout,
          detected_at: $detected_at,
          exit_code: 124,
          command: $command
        }
      }
    ' >"$tmp"
  fi

  mv "$tmp" "$file"
}

# shellcheck disable=SC2329
cleanup() {
  rm -f "${watchdog_output_file:-}"
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --timeout)
        [[ $# -ge 2 ]] || runoq::die "--timeout requires a value"
        timeout_seconds="$2"
        shift 2
        ;;
      --issue)
        [[ $# -ge 2 ]] || runoq::die "--issue requires a value"
        issue_number="$2"
        shift 2
        ;;
      --state-dir)
        [[ $# -ge 2 ]] || runoq::die "--state-dir requires a value"
        state_dir_override="$2"
        shift 2
        ;;
      --)
        shift
        break
        ;;
      *)
        usage >&2
        exit 1
        ;;
    esac
  done

  [[ $# -ge 1 ]] || { usage >&2; exit 1; }
  [[ "$timeout_seconds" =~ ^[0-9]+$ ]] || runoq::die "--timeout must be an integer number of seconds"
  (( timeout_seconds > 0 )) || runoq::die "--timeout must be greater than zero"

  watchdog_command=("$@")
}

parse_args "$@"

watchdog_output_file="$(mktemp "${TMPDIR:-/tmp}/runoq-watchdog.XXXXXX")"
trap cleanup EXIT

"${watchdog_command[@]}" >"$watchdog_output_file" 2>&1 &
child_pid=$!
sent_bytes=0
last_activity="$(date +%s)"
timed_out=false

while true; do
  if [[ -f "$watchdog_output_file" ]]; then
    current_bytes="$(wc -c <"$watchdog_output_file" | tr -d '[:space:]')"
    if (( current_bytes > sent_bytes )); then
      dd if="$watchdog_output_file" bs=1 skip="$sent_bytes" count="$((current_bytes - sent_bytes))" status=none
      sent_bytes="$current_bytes"
      last_activity="$(date +%s)"
    fi
  fi

  if ! kill -0 "$child_pid" 2>/dev/null; then
    set +e
    wait "$child_pid"
    child_exit="$?"
    set -e
    break
  fi

  now="$(date +%s)"
  if (( now - last_activity >= timeout_seconds )); then
    timed_out=true
    kill "$child_pid" 2>/dev/null || true
    sleep 1
    if kill -0 "$child_pid" 2>/dev/null; then
      kill -9 "$child_pid" 2>/dev/null || true
    fi
    set +e
    wait "$child_pid"
    set -e
    child_exit=124
    break
  fi

  sleep 1
done

if [[ -f "$watchdog_output_file" ]]; then
  current_bytes="$(wc -c <"$watchdog_output_file" | tr -d '[:space:]')"
  if (( current_bytes > sent_bytes )); then
    dd if="$watchdog_output_file" bs=1 skip="$sent_bytes" count="$((current_bytes - sent_bytes))" status=none
  fi
fi

if [[ "$timed_out" == "true" ]]; then
  write_stall_marker "$issue_number" "$timeout_seconds" "${watchdog_command[@]}"
  printf 'runoq: watchdog stalled after %ss of inactivity\n' "$timeout_seconds" >&2
  exit 124
fi

exit "${child_exit:-0}"
