#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RUNOQ_ROOT="${RUNOQ_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
export RUNOQ_ROOT

runtime_bin="${RUNOQ_RUNTIME_BIN:-}"
if [[ -n "$runtime_bin" ]]; then
  exec "$runtime_bin" "__dispatch_safety" "$@"
fi
go_bin="${RUNOQ_GO_BIN:-go}"
command -v "$go_bin" >/dev/null 2>&1 || {
  echo "runoq: Go toolchain not found: $go_bin" >&2
  exit 1
}
cd "$RUNOQ_ROOT"
exec "$go_bin" run "$RUNOQ_ROOT/cmd/runoq-runtime" "__dispatch_safety" "$@"

# shellcheck source=./scripts/lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  dispatch-safety.sh reconcile <repo>
  dispatch-safety.sh eligibility <repo> <issue-number>
EOF
}

issue_comment() {
  local repo="$1"
  local issue_number="$2"
  local body="$3"
  runoq::gh issue comment "$issue_number" --repo "$repo" --body "$body" >/dev/null
}

pr_comment() {
  local repo="$1"
  local pr_number="$2"
  local body="$3"
  runoq::gh pr comment "$pr_number" --repo "$repo" --body "$body" >/dev/null
}

set_issue_status() {
  local repo="$1"
  local issue_number="$2"
  local status="$3"
  "$(runoq::root)/scripts/gh-issue-queue.sh" set-status "$repo" "$issue_number" "$status" >/dev/null
}

parse_issue_metadata() {
  local body_file="$1"
  local block depends_line depends_json

  block="$(awk '
    /<!-- runoq:meta/ { in_block = 1; next }
    in_block && /-->/ { exit }
    in_block { print }
  ' "$body_file")"

  if [[ -z "$block" ]]; then
    jq -n '{depends_on: []}'
    return
  fi

  depends_line="$(printf '%s\n' "$block" | sed -n 's/^depends_on:[[:space:]]*//p' | head -n1)"
  if [[ -n "$depends_line" ]] && printf '%s' "$depends_line" | jq -e '.' >/dev/null 2>&1; then
    depends_json="$(printf '%s' "$depends_line")"
  else
    depends_json='[]'
  fi

  jq -n --argjson depends_on "$depends_json" '{depends_on: $depends_on}'
}

state_dir_json_files() {
  local state_dir
  state_dir="$(runoq::state_dir)"
  [[ -d "$state_dir" ]] || return 0
  find "$state_dir" -maxdepth 1 -type f -name '*.json' | sort
}

active_state_issues_json() {
  local issues='[]' file basename state_json phase issue_number

  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    basename="$(basename "$file" .json)"
    [[ "$basename" =~ ^[0-9]+$ ]] || continue
    if ! state_json="$(jq -e '.' "$file" 2>/dev/null)"; then
      continue
    fi
    phase="$(printf '%s' "$state_json" | jq -r '.phase // empty')"
    [[ "$phase" != "DONE" && "$phase" != "FAILED" ]] || continue
    issue_number="$(printf '%s' "$state_json" | jq -r '.issue')"
    issues="$(jq -n --argjson issues "$issues" --argjson issue "$issue_number" '$issues + [$issue] | unique | sort')"
  done < <(state_dir_json_files)

  printf '%s\n' "$issues"
}

branch_is_pushed() {
  local branch="$1"
  [[ -n "$branch" ]] || return 1
  [[ -d "$(runoq::target_root)/.git" ]] || return 1
  [[ -n "$(git -C "$(runoq::target_root)" ls-remote --heads origin "$branch" 2>/dev/null)" ]]
}

resolve_open_pr_number() {
  local repo="$1"
  local pr_number="$2"
  local branch="$3"
  local pr_json

  if [[ -n "$pr_number" && "$pr_number" != "null" ]]; then
    if pr_json="$(runoq::gh pr view "$pr_number" --repo "$repo" --json number 2>/dev/null)"; then
      printf '%s\n' "$(printf '%s' "$pr_json" | jq -r '.number')"
      return 0
    fi
  fi

  if [[ -n "$branch" ]]; then
    pr_json="$(runoq::gh pr list --repo "$repo" --state open --head "$branch" --json number 2>/dev/null || printf '[]')"
    if [[ "$(printf '%s' "$pr_json" | jq -r '.[0].number // empty')" != "" ]]; then
      printf '%s\n' "$(printf '%s' "$pr_json" | jq -r '.[0].number')"
      return 0
    fi
  fi

  return 1
}

reconcile_state_file() {
  local repo="$1"
  local file="$2"
  local state_json issue_number phase round branch pr_number resumed_pr updated_at message action

  state_json="$(jq -e '.' "$file" 2>/dev/null)" || return 0
  phase="$(printf '%s' "$state_json" | jq -r '.phase // empty')"
  [[ "$phase" != "DONE" && "$phase" != "FAILED" ]] || return 0

  issue_number="$(printf '%s' "$state_json" | jq -r '.issue')"
  round="$(printf '%s' "$state_json" | jq -r '.round // 0')"
  branch="$(printf '%s' "$state_json" | jq -r '.branch // empty')"
  pr_number="$(printf '%s' "$state_json" | jq -r '.pr_number // empty')"
  updated_at="$(printf '%s' "$state_json" | jq -r '.updated_at // "unknown"')"

  if resumed_pr="$(resolve_open_pr_number "$repo" "$pr_number" "$branch")" && branch_is_pushed "$branch"; then
    message="Detected interrupted run from ${updated_at}. Previous phase: ${phase} round ${round}. Resuming."
    issue_comment "$repo" "$issue_number" "$message"
    pr_comment "$repo" "$resumed_pr" "$message"
    action="resume"
    jq -n \
      --argjson issue "$issue_number" \
      --argjson pr_number "$resumed_pr" \
      --arg action "$action" \
      --arg phase "$phase" \
      --argjson round "$round" '
      {
        issue: $issue,
        pr_number: $pr_number,
        action: $action,
        phase: $phase,
        round: $round
      }
    '
    return 0
  fi

  message="Detected interrupted run from ${updated_at}. Previous phase: ${phase} round ${round}. Marking for human review."
  set_issue_status "$repo" "$issue_number" "needs-review"
  issue_comment "$repo" "$issue_number" "$message"
  if [[ -n "${resumed_pr:-}" ]]; then
    pr_comment "$repo" "$resumed_pr" "$message"
  fi
  jq -n \
    --argjson issue "$issue_number" \
    --arg action "needs-review" \
    --arg phase "$phase" \
    --argjson round "$round" '
    {
      issue: $issue,
      action: $action,
      phase: $phase,
      round: $round
    }
  '
}

reconcile_stale_labels() {
  local repo="$1"
  local active_issues="$2"
  local in_progress_label issues_json actions issue_number message

  in_progress_label="$(runoq::label_for_status "in-progress")"
  issues_json="$(runoq::gh issue list --repo "$repo" --label "$in_progress_label" --state open --limit 200 --json number,title,labels)"
  actions='[]'

  while IFS= read -r issue; do
    [[ -n "$issue" ]] || continue
    issue_number="$(printf '%s' "$issue" | jq -r '.number')"
    if printf '%s' "$active_issues" | jq -e --argjson issue "$issue_number" 'index($issue) != null' >/dev/null; then
      continue
    fi

    set_issue_status "$repo" "$issue_number" "ready"
    message="Found stale runoq:in-progress label with no active run. Reset to runoq:ready."
    issue_comment "$repo" "$issue_number" "$message"
    actions="$(jq -n \
      --argjson actions "$actions" \
      --argjson issue "$issue_number" '
      $actions + [{issue: $issue, action: "reset-ready"}]
    ')"
  done < <(printf '%s' "$issues_json" | jq -c '.[]')

  printf '%s\n' "$actions"
}

reconcile() {
  local repo="$1"
  local actions active_issues file basename action_json stale_actions

  actions='[]'
  active_issues="$(active_state_issues_json)"

  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    basename="$(basename "$file" .json)"
    [[ "$basename" =~ ^[0-9]+$ ]] || continue
    action_json="$(reconcile_state_file "$repo" "$file")"
    if [[ -n "$action_json" ]]; then
      actions="$(jq -n --argjson actions "$actions" --argjson action "$action_json" '$actions + [$action]')"
    fi
  done < <(state_dir_json_files)

  stale_actions="$(reconcile_stale_labels "$repo" "$active_issues")"
  jq -n --argjson actions "$actions" --argjson stale "$stale_actions" '$actions + $stale'
}

has_acceptance_criteria() {
  local body_file="$1"
  grep -q '^## Acceptance Criteria' "$body_file"
}

dependency_reason() {
  local repo="$1"
  local dependency="$2"
  local done_label dependency_json
  done_label="$(runoq::config_get '.labels.done')"
  dependency_json="$(runoq::gh issue view "$dependency" --repo "$repo" --json labels)"

  if printf '%s' "$dependency_json" | jq -e --arg done_label "$done_label" '.labels | map(.name) | index($done_label) != null' >/dev/null; then
    return 1
  fi

  printf 'dependency #%s is not runoq:done\n' "$dependency"
}

open_pr_reason() {
  local repo="$1"
  local branch="$2"
  local pr_json pr_number
  pr_json="$(runoq::gh pr list --repo "$repo" --state open --head "$branch" --json number,url)"
  pr_number="$(printf '%s' "$pr_json" | jq -r '.[0].number // empty')"
  if [[ -n "$pr_number" ]]; then
    printf 'existing open PR #%s already tracks this issue\n' "$pr_number"
  fi
}

branch_has_conflicts() {
  local branch="$1"
  local target_root remote_sha merge_base
  target_root="$(runoq::target_root)"
  remote_sha="$(git -C "$target_root" ls-remote --heads origin "$branch" 2>/dev/null | awk '{print $1}' | head -n1)"
  [[ -n "$remote_sha" ]] || return 1

  git -C "$target_root" fetch origin main "$branch" >/dev/null 2>&1 || true
  merge_base="$(git -C "$target_root" merge-base "origin/main" "$remote_sha" 2>/dev/null || true)"
  [[ -n "$merge_base" ]] || return 1

  git -C "$target_root" merge-tree "$merge_base" "origin/main" "$remote_sha" | grep -q '<<<<<<<'
}

eligibility() {
  local repo="$1"
  local issue_number="$2"
  local issue_json body_file metadata branch reasons reason message

  issue_json="$(runoq::gh issue view "$issue_number" --repo "$repo" --json number,title,body,labels,url)"
  body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-eligibility.XXXXXX")"
  printf '%s' "$issue_json" | jq -r '.body // ""' >"$body_file"
  metadata="$(parse_issue_metadata "$body_file")"
  branch="$(runoq::branch_name "$issue_number" "$(printf '%s' "$issue_json" | jq -r '.title // ""')")"
  reasons='[]'

  if [[ -z "$(printf '%s' "$issue_json" | jq -r '.title // empty')" ]] || ! has_acceptance_criteria "$body_file"; then
    reasons="$(jq -n --argjson reasons "$reasons" '$reasons + ["missing acceptance criteria"]')"
  fi

  while IFS= read -r dependency; do
    [[ -n "$dependency" ]] || continue
    if reason="$(dependency_reason "$repo" "$dependency")"; then
      reasons="$(jq -n --argjson reasons "$reasons" --arg reason "$reason" '$reasons + [($reason | sub("\n$"; ""))]')"
    fi
  done < <(printf '%s' "$metadata" | jq -r '.depends_on[]?')

  if reason="$(open_pr_reason "$repo" "$branch")"; then
    if [[ -n "$reason" ]]; then
      reasons="$(jq -n --argjson reasons "$reasons" --arg reason "$reason" '$reasons + [($reason | sub("\n$"; ""))]')"
    fi
  fi

  if branch_has_conflicts "$branch"; then
    reasons="$(jq -n --argjson reasons "$reasons" --arg branch "$branch" '$reasons + [("branch " + $branch + " has unresolved conflicts with origin/main")]')"
  fi

  rm -f "$body_file"

  if [[ "$(printf '%s' "$reasons" | jq -r 'length')" -gt 0 ]]; then
    message="Skipped: $(printf '%s' "$reasons" | jq -r 'join("; ")')."
    issue_comment "$repo" "$issue_number" "$message"
    jq -n \
      --argjson issue "$issue_number" \
      --arg branch "$branch" \
      --argjson reasons "$reasons" '
      {
        allowed: false,
        issue: $issue,
        branch: $branch,
        reasons: $reasons
      }
    '
    exit 1
  fi

  jq -n \
    --argjson issue "$issue_number" \
    --arg branch "$branch" '
    {
      allowed: true,
      issue: $issue,
      branch: $branch,
      reasons: []
    }
  '
}

case "${1:-}" in
  reconcile)
    [[ $# -eq 2 ]] || { usage >&2; exit 1; }
    reconcile "$2"
    ;;
  eligibility)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    eligibility "$2" "$3"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
