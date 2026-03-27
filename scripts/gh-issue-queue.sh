#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  gh-issue-queue.sh list <repo> <ready-label>
  gh-issue-queue.sh next <repo> <ready-label>
  gh-issue-queue.sh set-status <repo> <issue-number> <status>
  gh-issue-queue.sh create <repo> <title> <body> [--depends-on N,M] [--priority N] [--estimated-complexity value] [--type task|epic] [--parent-epic N]
  gh-issue-queue.sh epic-status <repo> <issue-number>
EOF
}

parse_metadata_body() {
  local body_file="$1"
  local block depends_line priority_line complexity_line type_line parent_epic_line
  local depends_json priority_json complexity_json type_json parent_epic_json valid
  block="$(awk '
    /<!-- runoq:meta/ { in_block = 1; next }
    in_block && /-->/ { exit }
    in_block { print }
  ' "$body_file")"

  if [[ -z "$block" ]]; then
    jq -n '{depends_on: [], priority: null, estimated_complexity: null, type: "task", parent_epic: null, metadata_present: false, metadata_valid: false}'
    return
  fi

  depends_line="$(printf '%s\n' "$block" | sed -n 's/^depends_on:[[:space:]]*//p' | head -n1)"
  priority_line="$(printf '%s\n' "$block" | sed -n 's/^priority:[[:space:]]*//p' | head -n1)"
  complexity_line="$(printf '%s\n' "$block" | sed -n 's/^estimated_complexity:[[:space:]]*//p' | head -n1)"
  type_line="$(printf '%s\n' "$block" | sed -n 's/^type:[[:space:]]*//p' | head -n1)"
  parent_epic_line="$(printf '%s\n' "$block" | sed -n 's/^parent_epic:[[:space:]]*//p' | head -n1)"

  valid=true
  if [[ -n "$depends_line" ]] && printf '%s' "$depends_line" | jq -e '.' >/dev/null 2>&1; then
    depends_json="$(printf '%s' "$depends_line")"
  else
    depends_json='[]'
    valid=false
  fi

  if [[ "$priority_line" =~ ^[0-9]+$ ]]; then
    priority_json="$priority_line"
  else
    priority_json='null'
    valid=false
  fi

  if [[ -n "$complexity_line" ]]; then
    complexity_json="$(jq -Rn --arg value "$complexity_line" '$value')"
  else
    complexity_json='null'
    valid=false
  fi

  if [[ "$type_line" == "epic" || "$type_line" == "task" ]]; then
    type_json="$(jq -Rn --arg value "$type_line" '$value')"
  else
    type_json='"task"'
  fi

  if [[ "$parent_epic_line" =~ ^[0-9]+$ ]]; then
    parent_epic_json="$parent_epic_line"
  else
    parent_epic_json='null'
  fi

  jq -n \
    --argjson depends_on "$depends_json" \
    --argjson priority "$priority_json" \
    --argjson estimated_complexity "$complexity_json" \
    --arg type_val "$(printf '%s' "$type_json" | jq -r '.')" \
    --argjson parent_epic "$parent_epic_json" \
    --argjson metadata_valid "$([[ "$valid" == true ]] && echo true || echo false)" '
    {
      depends_on: $depends_on,
      priority: $priority,
      estimated_complexity: $estimated_complexity,
      type: $type_val,
      parent_epic: $parent_epic,
      metadata_present: true,
      metadata_valid: $metadata_valid
    }
  '
}

list_issues() {
  local repo="$1"
  local ready_label="$2"
  local raw issue metadata body_file
  raw="$(runoq::gh issue list --repo "$repo" --label "$ready_label" --state open --limit 200 --json number,title,body,labels,url)"

  while IFS= read -r issue; do
    [[ -z "$issue" ]] && continue
    body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-issue-body.XXXXXX")"
    printf '%s' "$issue" | jq -r '.body // ""' >"$body_file"
    metadata="$(parse_metadata_body "$body_file")"
    rm -f "$body_file"
    jq -n \
      --argjson issue "$issue" \
      --argjson meta "$metadata" '
      {
        number: $issue.number,
        title: $issue.title,
        body: $issue.body,
        url: $issue.url,
        labels: ($issue.labels | map(.name)),
        depends_on: $meta.depends_on,
        priority: $meta.priority,
        estimated_complexity: $meta.estimated_complexity,
        type: $meta.type,
        parent_epic: $meta.parent_epic,
        metadata_present: $meta.metadata_present,
        metadata_valid: $meta.metadata_valid
      }
    '
  done < <(printf '%s' "$raw" | jq -c '.[]') | jq -s '.'
}

dependency_status() {
  local repo="$1"
  local dependency="$2"
  local done_label output
  done_label="$(runoq::config_get '.labels.done')"

  if ! output="$(runoq::gh issue view "$dependency" --repo "$repo" --json number,labels 2>/dev/null)"; then
    jq -n --argjson dependency "$dependency" '{
      dependency: $dependency,
      done: false,
      reason: ("missing dependency issue #" + ($dependency | tostring))
    }'
    return
  fi

  jq -n \
    --argjson dependency "$dependency" \
    --argjson issue "$output" \
    --arg done_label "$done_label" '
    if ($issue.labels | map(.name) | index($done_label)) then
      { dependency: $dependency, done: true, reason: null }
    else
      {
        dependency: $dependency,
        done: false,
        reason: ("dependency #" + ($dependency | tostring) + " is not runoq:done")
      }
    end
  '
}

next_issue() {
  local repo="$1"
  local ready_label="$2"
  local issues issue dependency dep_status blocked issue_with_status skipped
  issues="$(list_issues "$repo" "$ready_label")"
  skipped='[]'

  while IFS= read -r issue; do
    [[ -z "$issue" ]] && continue

    if [[ "$(printf '%s' "$issue" | jq -r '.type')" == "epic" ]]; then
      skipped="$(jq -n --argjson skipped "$skipped" --argjson issue "$issue" '$skipped + [$issue + {actionable: false, blocked_reasons: ["epic issues are not directly dispatchable"]}]')"
      continue
    fi

    blocked='[]'
    while IFS= read -r dependency; do
      [[ -z "$dependency" ]] && continue
      dep_status="$(dependency_status "$repo" "$dependency")"
      if [[ "$(printf '%s' "$dep_status" | jq -r '.done')" != "true" ]]; then
        blocked="$(jq -n --argjson blocked "$blocked" --argjson status "$dep_status" '$blocked + [$status.reason]')"
      fi
    done < <(printf '%s' "$issue" | jq -r '.depends_on[]?')

    issue_with_status="$(jq -n \
      --argjson issue "$issue" \
      --argjson blocked "$blocked" '
      $issue + {
        actionable: ($blocked | length == 0),
        blocked_reasons: $blocked
      }
    ')"

    if [[ "$(printf '%s' "$issue_with_status" | jq -r '.actionable')" == "true" ]]; then
      jq -n --argjson issue "$issue_with_status" --argjson skipped "$skipped" '{
        issue: $issue,
        skipped: $skipped
      }'
      return
    fi

    skipped="$(jq -n --argjson skipped "$skipped" --argjson issue "$issue_with_status" '$skipped + [$issue]')"
  done < <(printf '%s' "$issues" | jq -c 'sort_by((.priority // 999999), .number)[]')

  jq -n --argjson skipped "$skipped" '{
    issue: null,
    skipped: $skipped
  }'
}

epic_children_done() {
  local repo="$1"
  local issue_number="$2"
  local done_label sub_issues children pending all_done child_number child_labels

  done_label="$(runoq::config_get '.labels.done')"
  sub_issues="$(runoq::gh api "repos/${repo}/issues/${issue_number}/sub_issues" --paginate)"

  children="$(printf '%s' "$sub_issues" | jq -c '[.[] | .number]')"
  pending='[]'
  all_done=true

  while IFS= read -r child_number; do
    [[ -z "$child_number" ]] && continue
    child_labels="$(printf '%s' "$sub_issues" | jq -c --argjson num "$child_number" '[.[] | select(.number == $num) | .labels[]?.name] // []')"
    if ! printf '%s' "$child_labels" | jq -e --arg done_label "$done_label" 'index($done_label)' >/dev/null 2>&1; then
      pending="$(jq -n --argjson pending "$pending" --argjson child "$child_number" '$pending + [$child]')"
      all_done=false
    fi
  done < <(printf '%s' "$children" | jq -r '.[]')

  jq -n \
    --argjson all_done "$([[ "$all_done" == true ]] && echo true || echo false)" \
    --argjson children "$children" \
    --argjson pending "$pending" '
    {
      all_done: $all_done,
      children: $children,
      pending: $pending
    }
  '
}

case "${1:-}" in
  list)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    list_issues "$2" "$3"
    ;;
  next)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    next_issue "$2" "$3"
    ;;
  set-status)
    [[ $# -eq 4 ]] || { usage >&2; exit 1; }
    repo="$2"
    issue_number="$3"
    status="$4"
    new_label="$(runoq::label_for_status "$status")"
    current_labels="$(runoq::gh issue view "$issue_number" --repo "$repo" --json labels | jq -r '.labels[].name')"
    edit_args=()
    while IFS= read -r label; do
      [[ -z "$label" ]] && continue
      if [[ "$label" == runoq:* ]]; then
        edit_args+=(--remove-label "$label")
      fi
    done <<<"$current_labels"
    edit_args+=(--add-label "$new_label")
    runoq::gh issue edit "$issue_number" --repo "$repo" "${edit_args[@]}" >/dev/null
    jq -n --argjson issue "$issue_number" --arg status "$status" --arg label "$new_label" '{
      issue: $issue,
      status: $status,
      label: $label
    }'
    ;;
  create)
    [[ $# -ge 4 ]] || { usage >&2; exit 1; }
    repo="$2"
    title="$3"
    body="$4"
    shift 4
    depends_on='[]'
    priority='3'
    estimated_complexity='medium'
    issue_type='task'
    parent_epic=''
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --depends-on)
          depends_on="$(jq -cn --arg raw "${2:-}" '$raw | split(",") | map(select(length > 0) | tonumber)')"
          shift 2
          ;;
        --priority)
          priority="${2:-3}"
          shift 2
          ;;
        --estimated-complexity)
          estimated_complexity="${2:-medium}"
          shift 2
          ;;
        --type)
          issue_type="${2:-task}"
          shift 2
          ;;
        --parent-epic)
          parent_epic="${2:-}"
          shift 2
          ;;
        *)
          usage >&2
          exit 1
          ;;
      esac
    done

    body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-issue-create.XXXXXX.md")"
    {
      echo "<!-- runoq:meta"
      printf 'depends_on: %s\n' "$(printf '%s' "$depends_on" | jq -c '.')"
      printf 'priority: %s\n' "$priority"
      printf 'estimated_complexity: %s\n' "$estimated_complexity"
      printf 'type: %s\n' "$issue_type"
      if [[ -n "$parent_epic" ]]; then
        printf 'parent_epic: %s\n' "$parent_epic"
      fi
      echo "-->"
      echo
      printf '%s\n' "$body"
    } >"$body_file"

    ready_label="$(runoq::config_get '.labels.ready')"
    result="$(runoq::gh issue create --repo "$repo" --title "$title" --body-file "$body_file" --label "$ready_label")"
    rm -f "$body_file"

    # If parent_epic is set, link the new issue as a sub-issue of the parent epic
    if [[ -n "$parent_epic" ]]; then
      new_issue_number="$(printf '%s' "$result" | grep -oE '[0-9]+$')"
      child_node_id="$(runoq::gh api "repos/${repo}/issues/${new_issue_number}" --jq '.node_id')"
      runoq::gh api "repos/${repo}/issues/${parent_epic}/sub_issues" --method POST -f "sub_issue_id=${child_node_id}"
    fi

    jq -n --arg title "$title" --arg url "$result" '{title:$title, url:$url}'
    ;;
  epic-status)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    epic_children_done "$2" "$3"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
