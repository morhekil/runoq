#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  maintenance.sh derive-partitions <root>
  maintenance.sh start <repo>
  maintenance.sh post-findings <repo> <tracking-issue> <findings-file>
  maintenance.sh triage <repo> <tracking-issue>
  maintenance.sh run <repo> <findings-file>
  maintenance.sh report-partition <repo> <tracking-issue> <partition-name> <score> <finding-count>
EOF
}

maintenance_state_file() {
  printf '%s/maintenance.json\n' "$(agendev::state_dir)"
}

gitignore_exclusions() {
  local root="$1"
  local file="$root/.gitignore"
  if [[ ! -f "$file" ]]; then
    printf '[]\n'
    return
  fi

  grep -Ev '^[[:space:]]*(#|$)' "$file" | jq -Rsc 'split("\n") | map(select(length > 0))'
}

tsconfig_file() {
  local root="$1"
  printf '%s/tsconfig.json\n' "$root"
}

tsconfig_json() {
  local root="$1"
  local file
  file="$(tsconfig_file "$root")"
  if [[ -f "$file" ]]; then
    cat "$file"
  else
    printf '{}\n'
  fi
}

derive_partitions() {
  local root="$1"
  local gitignore_json tsconfig exclusions partitions mode

  gitignore_json="$(gitignore_exclusions "$root")"
  tsconfig="$(tsconfig_json "$root")"
  exclusions="$(jq -n --argjson gitignore "$gitignore_json" --argjson tsconfig "$tsconfig" '
    reduce ($gitignore + ($tsconfig.exclude // []))[] as $entry
      ([]; if index($entry) then . else . + [$entry] end)
  ')"

  if [[ "$(printf '%s' "$tsconfig" | jq -r '(.references // []) | length')" -gt 0 ]]; then
    mode="references"
    partitions="$(printf '%s' "$tsconfig" | jq '
      (.references // [])
      | map({
          name: (.path | split("/") | last),
          path: .path
        })
      | sort_by(.path)
    ')"
  else
    mode="single-project"
    partitions="$(printf '%s' "$tsconfig" | jq '
      (.include // [])
      | map(split("/")[0])
      | map(select(length > 0))
      | unique
      | sort
      | map({
          name: .,
          path: .
        })
    ')"
  fi

  jq -n \
    --arg root "$root" \
    --arg mode "$mode" \
    --argjson exclusions "$exclusions" \
    --argjson partitions "$partitions" '
    {
      root: $root,
      mode: $mode,
      exclusions: $exclusions,
      partitions: $partitions
    }
  '
}

tracking_issue_body_file() {
  mktemp "${TMPDIR:-/tmp}/agendev-maintenance-body.XXXXXX"
}

tracking_issue_body() {
  local partitions_json="$1"
  local body_file commit_sha branch_name timestamp partitions_line
  body_file="$(tracking_issue_body_file)"
  commit_sha="$(git -C "$(agendev::target_root)" rev-parse HEAD)"
  branch_name="$(git -C "$(agendev::target_root)" branch --show-current)"
  timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  partitions_line="$(printf '%s' "$partitions_json" | jq -r '.partitions | map(.name) | join(", ")')"

  {
    echo "<!-- agendev:event -->"
    echo "Maintenance review started."
    echo
    printf 'Timestamp: %s\n' "$timestamp"
    printf 'Branch: %s\n' "$branch_name"
    printf 'Commit: %s\n' "$commit_sha"
    printf 'Partitions: %s\n' "$partitions_line"
  } >"$body_file"

  printf '%s\n' "$body_file"
}

create_tracking_issue() {
  local repo="$1"
  local partitions_json="$2"
  local title body_file result number label
  title="Maintenance review $(date -u +%Y-%m-%d)"
  body_file="$(tracking_issue_body "$partitions_json")"
  label="$(agendev::config_get '.labels.maintenanceReview')"
  result="$(agendev::gh issue create --repo "$repo" --title "$title" --body-file "$body_file" --label "$label")"
  rm -f "$body_file"
  number="$(printf '%s' "$result" | sed -n 's#.*/issues/\([0-9][0-9]*\).*#\1#p')"
  jq -n --arg title "$title" --arg url "$result" --argjson number "${number:-null}" '{title:$title, url:$url, number:$number}'
}

report_partition() {
  local repo="$1"
  local tracking_issue="$2"
  local partition_name="$3"
  local score="$4"
  local finding_count="$5"
  local body
  body="$(printf 'Partition %s reviewed. PERFECT-D score: %s. Findings: %s.' "$partition_name" "$score" "$finding_count")"
  agendev::gh issue comment "$tracking_issue" --repo "$repo" --body "$body" >/dev/null
}

save_maintenance_state() {
  local payload="$1"
  agendev::ensure_state_dir
  printf '%s\n' "$payload" | agendev::write_json_file "$(maintenance_state_file)"
}

load_maintenance_state() {
  local file
  file="$(maintenance_state_file)"
  [[ -f "$file" ]] || agendev::die "Maintenance state file not found"
  jq -e '.' "$file"
}

authorization_minimum_permission() {
  agendev::config_get '.authorization.minimumPermission'
}

authorization_deny_response() {
  agendev::config_get '.authorization.denyResponse'
}

finding_comment_body() {
  local finding_json="$1"
  local finding_id title dimension severity files description suggested_fix inflight_pr grouped
  finding_id="$(printf '%s' "$finding_json" | jq -r '.id')"
  title="$(printf '%s' "$finding_json" | jq -r '.title')"
  dimension="$(printf '%s' "$finding_json" | jq -r '.dimension // "unspecified"')"
  severity="$(printf '%s' "$finding_json" | jq -r '.severity // "medium"')"
  files="$(printf '%s' "$finding_json" | jq -r '(.files // []) | join(", ")')"
  description="$(printf '%s' "$finding_json" | jq -r '.description')"
  suggested_fix="$(printf '%s' "$finding_json" | jq -r '.suggested_fix')"
  inflight_pr="$(printf '%s' "$finding_json" | jq -r '.inflight_pr // empty')"
  grouped="$(printf '%s' "$finding_json" | jq -r '.grouped // false')"

  {
    printf 'Finding ID: %s\n' "$finding_id"
    printf 'Title: %s\n' "$title"
    printf 'Dimension: %s\n' "$dimension"
    printf 'Severity: %s\n' "$severity"
    if [[ -n "$files" ]]; then
      printf 'Files: %s\n' "$files"
    fi
    if [[ "$grouped" == "true" ]]; then
      echo "Grouped finding."
    fi
    printf '%s\n' "$description"
    printf 'Suggested fix: %s\n' "$suggested_fix"
    if [[ -n "$inflight_pr" ]]; then
      printf 'Note: this code is being modified in PR #%s.\n' "$inflight_pr"
    fi
  }
}

post_findings() {
  local repo="$1"
  local tracking_issue="$2"
  local findings_file="$3"
  local state_json findings_json finding

  state_json="$(load_maintenance_state)"
  findings_json="$(jq '
    {
      recurring_patterns: (.recurring_patterns // []),
      findings: (
        (.findings // [])
        | map(. + {
            status: "pending",
            priority: (.priority // 2)
          })
      )
    }
  ' "$findings_file")"

  while IFS= read -r finding; do
    [[ -n "$finding" ]] || continue
    agendev::gh issue comment "$tracking_issue" --repo "$repo" --body "$(finding_comment_body "$finding")" >/dev/null
  done < <(printf '%s' "$findings_json" | jq -c '.findings[]')

  state_json="$(jq -n \
    --argjson state "$state_json" \
    --argjson findings "$findings_json" '
    $state + {
      phase: "FINDINGS_POSTED",
      recurring_patterns: $findings.recurring_patterns,
      findings: $findings.findings
    }
  ')"
  save_maintenance_state "$state_json"

  jq -n \
    --argjson state "$state_json" '
    {
      tracking_issue: $state.tracking_issue,
      findings: $state.findings
    }
  '
}

tracking_issue_comments() {
  local repo="$1"
  local tracking_issue="$2"
  agendev::gh api "repos/${repo}/issues/${tracking_issue}/comments"
}

find_finding_id() {
  local body="$1"
  local state_json="$2"
  local finding_id
  while IFS= read -r finding_id; do
    [[ -n "$finding_id" ]] || continue
    if [[ "$body" == *"$finding_id"* ]]; then
      printf '%s\n' "$finding_id"
      return 0
    fi
  done < <(printf '%s' "$state_json" | jq -r '.findings[].id')
  return 1
}

issue_number_from_url() {
  local url="$1"
  printf '%s' "$url" | sed -n 's#.*/issues/\([0-9][0-9]*\).*#\1#p'
}

create_issue_from_finding() {
  local repo="$1"
  local finding_json="$2"
  local priority_override="${3:-}"
  local title description suggested_fix priority result url issue_number
  title="$(printf '%s' "$finding_json" | jq -r '.title')"
  description="$(printf '%s' "$finding_json" | jq -r '.description')"
  suggested_fix="$(printf '%s' "$finding_json" | jq -r '.suggested_fix')"
  priority="$(printf '%s' "$finding_json" | jq -r '.priority // 2')"
  if [[ -n "$priority_override" ]]; then
    priority="$priority_override"
  fi
  result="$("$(agendev::root)/scripts/gh-issue-queue.sh" create "$repo" "$title" "$(printf '%s\n\nSuggested fix: %s\n' "$description" "$suggested_fix")" --priority "$priority" --estimated-complexity medium)"
  url="$(printf '%s' "$result" | jq -r '.url')"
  issue_number="$(issue_number_from_url "$url")"
  jq -n --argjson issue "$issue_number" --arg url "$url" --arg priority "$priority" '{issue:$issue, url:$url, priority:$priority}'
}

update_finding_state() {
  local state_json="$1"
  local finding_id="$2"
  local status="$3"
  local filed_issue="${4:-}"
  local priority_override="${5:-}"
  jq -n \
    --argjson state "$state_json" \
    --arg finding_id "$finding_id" \
    --arg status "$status" \
    --arg filed_issue "$filed_issue" \
    --arg priority_override "$priority_override" '
    $state | .findings |= map(
      if .id == $finding_id then
        . + {
          status: $status
        }
        | if $filed_issue != "" then . + {filed_issue: ($filed_issue | tonumber)} else . end
        | if $priority_override != "" then . + {priority: ($priority_override | tonumber)} else . end
      else
        .
      end
    )
  '
}

triage() {
  local repo="$1"
  local tracking_issue="$2"
  local state_json comments required deny_mode processed comment
  local comment_id author body finding_id permission_status priority_override filed_json filed_issue message

  state_json="$(load_maintenance_state)"
  required="$(authorization_minimum_permission)"
  deny_mode="$(authorization_deny_response)"
  processed='[]'
  comments="$(tracking_issue_comments "$repo" "$tracking_issue")"

  while IFS= read -r comment; do
    [[ -n "$comment" ]] || continue
    comment_id="$(printf '%s' "$comment" | jq -r '.id')"
    if "$(agendev::root)/scripts/state.sh" has-mention "$comment_id" >/dev/null 2>&1; then
      continue
    fi
    body="$(printf '%s' "$comment" | jq -r '.body')"
    [[ "$body" == *"@agendev"* ]] || continue
    author="$(printf '%s' "$comment" | jq -r '.user.login')"

    set +e
    "$(agendev::root)/scripts/gh-pr-lifecycle.sh" check-permission "$repo" "$author" "$required" >/dev/null 2>&1
    permission_status="$?"
    set -e
    if [[ "$permission_status" -ne 0 ]]; then
      if [[ "$deny_mode" == "comment" ]]; then
        agendev::gh issue comment "$tracking_issue" --repo "$repo" --body "Permission denied for @${author}. Requires ${required} access to address @agendev mentions." >/dev/null
      fi
      "$(agendev::root)/scripts/state.sh" record-mention "$comment_id" >/dev/null
      processed="$(jq -n --argjson processed "$processed" --argjson id "$comment_id" '$processed + [$id]')"
      continue
    fi

    finding_id="$(find_finding_id "$body" "$state_json" || true)"
    [[ -n "$finding_id" ]] || continue

    if [[ "$body" == *"approve"* || "$body" == *"file this"* ]]; then
      priority_override=""
      if [[ "$body" == *"lower priority"* ]]; then
        priority_override="3"
      fi
      filed_json="$(create_issue_from_finding "$repo" "$(printf '%s' "$state_json" | jq -c --arg id "$finding_id" '.findings[] | select(.id == $id)')" "$priority_override")"
      filed_issue="$(printf '%s' "$filed_json" | jq -r '.issue')"
      if [[ -n "$priority_override" ]]; then
        message="Finding ${finding_id} approved with priority ${priority_override}. Filed as #${filed_issue}."
      else
        message="Finding ${finding_id} approved. Filed as #${filed_issue}."
      fi
      agendev::gh issue comment "$tracking_issue" --repo "$repo" --body "$message" >/dev/null
      state_json="$(update_finding_state "$state_json" "$finding_id" "approved" "$filed_issue" "$priority_override")"
    elif [[ "$body" == *"skip"* || "$body" == *"won't fix"* ]]; then
      agendev::gh issue comment "$tracking_issue" --repo "$repo" --body "Finding ${finding_id} declined." >/dev/null
      state_json="$(update_finding_state "$state_json" "$finding_id" "declined")"
    fi

    "$(agendev::root)/scripts/state.sh" record-mention "$comment_id" >/dev/null
    processed="$(jq -n --argjson processed "$processed" --argjson id "$comment_id" '$processed + [$id]')"
  done < <(printf '%s' "$comments" | jq -c '.[]')

  if [[ "$(printf '%s' "$state_json" | jq -r '(.findings // []) | all(.status != "pending")')" == "true" ]] &&
     [[ "$(printf '%s' "$state_json" | jq -r '(.recurring_patterns // []) | length')" -gt 0 ]]; then
    agendev::gh issue comment "$tracking_issue" --repo "$repo" --body "Recurring patterns: $(printf '%s' "$state_json" | jq -r '.recurring_patterns | join(", ")')." >/dev/null
  fi

  save_maintenance_state "$state_json"
  jq -n --argjson processed "$processed" '{processed: $processed}'
}

final_summary() {
  local state_json="$1"
  jq -n \
    --argjson state "$state_json" '
    {
      partitions_reviewed: ($state.partitions | length),
      findings_proposed: ($state.findings | length),
      approved: ($state.findings | map(select(.status == "approved")) | length),
      declined: ($state.findings | map(select(.status == "declined")) | length),
      issues_created: ($state.findings | map(select(has("filed_issue"))) | length),
      recurring_patterns: ($state.recurring_patterns // [])
    }
  '
}

complete_run() {
  local repo="$1"
  local tracking_issue="$2"
  local state_json summary_json message
  state_json="$(load_maintenance_state)"
  summary_json="$(final_summary "$state_json")"
  message="Maintenance review completed. Partitions reviewed: $(printf '%s' "$summary_json" | jq -r '.partitions_reviewed'). Findings proposed: $(printf '%s' "$summary_json" | jq -r '.findings_proposed'). Approved: $(printf '%s' "$summary_json" | jq -r '.approved'). Declined: $(printf '%s' "$summary_json" | jq -r '.declined'). Issues created: $(printf '%s' "$summary_json" | jq -r '.issues_created')."
  if [[ "$(printf '%s' "$summary_json" | jq -r '.recurring_patterns | length')" -gt 0 ]]; then
    message="${message} Recurring patterns: $(printf '%s' "$summary_json" | jq -r '.recurring_patterns | join(", ")')."
  fi
  agendev::gh issue comment "$tracking_issue" --repo "$repo" --body "$message" >/dev/null
  state_json="$(jq -n --argjson state "$state_json" --argjson summary "$summary_json" '
    $state + {
      phase: "COMPLETED",
      summary: $summary
    }
  ')"
  save_maintenance_state "$state_json"
  jq -n \
    --arg phase "COMPLETED" \
    --argjson tracking_issue "$tracking_issue" \
    --argjson summary "$summary_json" '
    {
      phase: $phase,
      tracking_issue: $tracking_issue,
      summary: $summary
    }
  '
}

run_maintenance() {
  local repo="$1"
  local findings_file="$2"
  local state_json tracking_issue phase

  if [[ -f "$(maintenance_state_file)" ]]; then
    state_json="$(load_maintenance_state)"
  else
    start_run "$repo" >/dev/null
    state_json="$(load_maintenance_state)"
  fi

  tracking_issue="$(printf '%s' "$state_json" | jq -r '.tracking_issue')"
  phase="$(printf '%s' "$state_json" | jq -r '.phase')"
  if [[ "$phase" == "COMPLETED" ]]; then
    jq -n \
      --arg phase "COMPLETED" \
      --argjson tracking_issue "$tracking_issue" \
      --argjson summary "$(printf '%s' "$state_json" | jq '.summary')" '
      {
        phase: $phase,
        tracking_issue: $tracking_issue,
        summary: $summary
      }
    '
    return 0
  fi

  if [[ "$phase" == "STARTED" ]]; then
    post_findings "$repo" "$tracking_issue" "$findings_file" >/dev/null
    state_json="$(load_maintenance_state)"
  fi

  phase="$(printf '%s' "$state_json" | jq -r '.phase')"
  if [[ "$phase" == "FINDINGS_POSTED" ]]; then
    triage "$repo" "$tracking_issue" >/dev/null
  fi

  complete_run "$repo" "$tracking_issue"
}

start_run() {
  local repo="$1"
  local partitions_json tracking_json tracking_issue
  partitions_json="$(derive_partitions "$(agendev::target_root)")"
  tracking_json="$(create_tracking_issue "$repo" "$partitions_json")"
  tracking_issue="$(printf '%s' "$tracking_json" | jq -r '.number')"

  while IFS= read -r partition_name; do
    [[ -n "$partition_name" ]] || continue
    report_partition "$repo" "$tracking_issue" "$partition_name" "pending" 0
  done < <(printf '%s' "$partitions_json" | jq -r '.partitions[].name')

  save_maintenance_state "$(jq -n \
    --arg phase "STARTED" \
    --argjson tracking_issue "$tracking_issue" \
    --argjson partitions "$partitions_json" '
    {
      phase: $phase,
      tracking_issue: $tracking_issue,
      partitions: $partitions.partitions
    }
  ')"

  jq -n \
    --argjson tracking_issue "$tracking_json" \
    --argjson partitions "$partitions_json" '
    {
      tracking_issue: $tracking_issue,
      partitions: $partitions.partitions
    }
  '
}

case "${1:-}" in
  derive-partitions)
    [[ $# -eq 2 ]] || { usage >&2; exit 1; }
    derive_partitions "$2"
    ;;
  start)
    [[ $# -eq 2 ]] || { usage >&2; exit 1; }
    start_run "$2"
    ;;
  post-findings)
    [[ $# -eq 4 ]] || { usage >&2; exit 1; }
    post_findings "$2" "$3" "$4"
    ;;
  triage)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    triage "$2" "$3"
    ;;
  run)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    run_maintenance "$2" "$3"
    ;;
  report-partition)
    [[ $# -eq 6 ]] || { usage >&2; exit 1; }
    report_partition "$2" "$3" "$4" "$5" "$6"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
