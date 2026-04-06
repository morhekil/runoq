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

runoq::metadata_value() {
  local body="$1"
  local key="$2"
  printf '%s\n' "$body" | awk -v key="$key" '
    /<!-- runoq:meta/ {in_meta=1; next}
    in_meta && /-->/ {exit}
    in_meta && index($0, key ":") == 1 {
      sub("^" key ":[[:space:]]*", "", $0)
      print $0
      exit
    }
  '
}

runoq::issue_type() {
  local body="$1"
  local value
  value="$(runoq::metadata_value "$body" "type")"
  printf '%s\n' "${value:-task}"
}

runoq::issue_parent_epic() {
  runoq::metadata_value "$1" "parent_epic"
}

runoq::issue_milestone_type() {
  runoq::metadata_value "$1" "milestone_type"
}
