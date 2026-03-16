#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  maintenance.sh derive-partitions <root>
  maintenance.sh start <repo>
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
  report-partition)
    [[ $# -eq 6 ]] || { usage >&2; exit 1; }
    report_partition "$2" "$3" "$4" "$5" "$6"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
