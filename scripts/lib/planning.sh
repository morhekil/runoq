#!/usr/bin/env bash

set -euo pipefail

runoq::merge_checklists() {
  local left="${1:-}"
  local right="${2:-}"
  {
    [[ -n "$left" ]] && printf '%s\n' "$left"
    [[ -n "$right" ]] && printf '%s\n' "$right"
    true
  } | awk 'NF { print }'
}

runoq::parse_verdict_block() {
  local path="$1"
  local verdict score checklist
  verdict="$(awk -F': ' '/^VERDICT:/ {print $2; exit}' "$path")"
  score="$(awk -F': ' '/^SCORE:/ {print $2; exit}' "$path")"
  checklist="$(awk 'found {print} /^CHECKLIST:$/ {found=1}' "$path")"

  [[ -n "$verdict" ]] || runoq::die "Invalid verdict block: missing VERDICT in $path"
  [[ -n "$score" ]] || runoq::die "Invalid verdict block: missing SCORE in $path"

  jq -cn \
    --arg verdict "$verdict" \
    --arg score "$score" \
    --arg checklist "$checklist" \
    '{verdict:$verdict, score:$score, checklist:$checklist}'
}

runoq::format_plan_proposal() {
  local proposal_path="$1"
  jq -r '
    "<!-- runoq:payload:plan-proposal -->\n" +
    (.items | to_entries | map(
      "### \(.key + 1). \(.value.title)\n" +
      "**Type:** \(.value.type)" +
      (if .value.priority then " · **Priority:** \(.value.priority)" else "" end) +
      "\n\n" +
      (.value.goal // "" | if . != "" then "> \(.)\n\n" else "" end) +
      ((.value.criteria // []) | if length > 0 then
        "**Acceptance criteria:**\n" + (map("- [ ] \(.)") | join("\n")) + "\n\n"
      else "" end) +
      ((.value.scope // []) | if length > 0 then
        "**Scope:**\n" + (map("- \(.)") | join("\n")) + "\n"
      else "" end)
    ) | join("\n---\n\n"))
  ' "$proposal_path"
}

runoq::extract_marked_json_block() {
  local source_file="$1"
  local marker="$2"
  awk -v marker="$marker" '
    $0 ~ marker {
      saw_marker = 1
      next
    }
    saw_marker && /^```/ {
      if (!in_block) {
        in_block = 1
        block = ""
        next
      }
      printf "%s", block
      exit
    }
    in_block {
      block = block $0 "\n"
    }
  ' "$source_file"
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
