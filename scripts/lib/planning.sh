#!/usr/bin/env bash

set -euo pipefail

runoq::_tick_fmt() {
  "$(runoq::root)/scripts/tick-fmt.sh" "$@"
}

runoq::merge_checklists() {
  runoq::_tick_fmt merge-checklists "${1:-}" "${2:-}"
}

runoq::parse_verdict_block() {
  runoq::_tick_fmt parse-verdict < "$1"
}

runoq::format_plan_proposal() {
  runoq::_tick_fmt format-proposal < "$1"
}

runoq::extract_marked_json_block() {
  runoq::_tick_fmt extract-json "$2" < "$1"
}

runoq::issue_type() {
  local labels_json="$1"
  if printf '%s' "$labels_json" | jq -e 'map(.name) | index("runoq:planning")' >/dev/null 2>&1; then
    printf '%s\n' "planning"
  elif printf '%s' "$labels_json" | jq -e 'map(.name) | index("runoq:adjustment")' >/dev/null 2>&1; then
    printf '%s\n' "adjustment"
  else
    printf '%s\n' "task"
  fi
}

runoq::issue_milestone_type() {
  local labels_json="$1"
  if printf '%s' "$labels_json" | jq -e 'map(.name) | index("runoq:discovery")' >/dev/null 2>&1; then
    printf '%s\n' "discovery"
  elif printf '%s' "$labels_json" | jq -e 'map(.name) | index("runoq:implementation")' >/dev/null 2>&1; then
    printf '%s\n' "implementation"
  else
    printf '%s\n' ""
  fi
}
