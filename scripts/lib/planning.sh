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
    ["<!-- runoq:payload:plan-proposal -->"] +
    (.items | to_entries | map(
      "\(.key + 1). \(.value.title)\n   type: \(.value.type)\n   goal: \(.value.goal // "")\n   criteria: " +
      (((.value.criteria // []) | join("; ")))
    )) | join("\n")
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
