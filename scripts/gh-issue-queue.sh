#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  gh-issue-queue.sh list <repo> <ready-label>
  gh-issue-queue.sh next <repo> <ready-label>
  gh-issue-queue.sh set-status <repo> <issue-number> <status>
  gh-issue-queue.sh create <repo> <title> <body> [--depends-on N,M] [--priority N] [--estimated-complexity value]
EOF
}

parse_metadata_body() {
  local body_file="$1"
  local block depends_line priority_line complexity_line depends_json priority_json complexity_json valid
  block="$(awk '
    /<!-- agendev:meta/ { in_block = 1; next }
    in_block && /-->/ { exit }
    in_block { print }
  ' "$body_file")"

  if [[ -z "$block" ]]; then
    jq -n '{depends_on: [], priority: null, estimated_complexity: null, metadata_present: false, metadata_valid: false}'
    return
  fi

  depends_line="$(printf '%s\n' "$block" | sed -n 's/^depends_on:[[:space:]]*//p' | head -n1)"
  priority_line="$(printf '%s\n' "$block" | sed -n 's/^priority:[[:space:]]*//p' | head -n1)"
  complexity_line="$(printf '%s\n' "$block" | sed -n 's/^estimated_complexity:[[:space:]]*//p' | head -n1)"

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

  jq -n \
    --argjson depends_on "$depends_json" \
    --argjson priority "$priority_json" \
    --argjson estimated_complexity "$complexity_json" \
    --argjson metadata_valid "$([[ "$valid" == true ]] && echo true || echo false)" '
    {
      depends_on: $depends_on,
      priority: $priority,
      estimated_complexity: $estimated_complexity,
      metadata_present: true,
      metadata_valid: $metadata_valid
    }
  '
}

list_issues() {
  local repo="$1"
  local ready_label="$2"
  local raw issue metadata body_file
  raw="$(agendev::gh issue list --repo "$repo" --label "$ready_label" --state open --limit 200 --json number,title,body,labels,url)"

  while IFS= read -r issue; do
    [[ -z "$issue" ]] && continue
    body_file="$(mktemp "${TMPDIR:-/tmp}/agendev-issue-body.XXXXXX")"
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
  done_label="$(agendev::config_get '.labels.done')"

  if ! output="$(agendev::gh issue view "$dependency" --repo "$repo" --json number,labels 2>/dev/null)"; then
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
        reason: ("dependency #" + ($dependency | tostring) + " is not agendev:done")
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

case "${1:-}" in
  list)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    list_issues "$2" "$3"
    ;;
  next)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    next_issue "$2" "$3"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
