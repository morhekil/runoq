#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  gh-pr-lifecycle.sh create <repo> <branch> <issue-number> <title>
  gh-pr-lifecycle.sh comment <repo> <pr-number> <comment-body-file>
  gh-pr-lifecycle.sh update-summary <repo> <pr-number> <summary-file>
  gh-pr-lifecycle.sh finalize <repo> <pr-number> <verdict> [--reviewer username]
EOF
}

replace_marker_block() {
  local source_file="$1"
  local start_marker="$2"
  local end_marker="$3"
  local replacement_file="$4"
  local output_file="$5"

  awk -v start="$start_marker" -v end="$end_marker" -v replacement="$replacement_file" '
    BEGIN {
      while ((getline line < replacement) > 0) {
        replacement_text = replacement_text line ORS
      }
      close(replacement)
    }
    {
      if ($0 == start) {
        print
        printf "%s", replacement_text
        in_block = 1
        next
      }
      if ($0 == end) {
        in_block = 0
        print
        next
      }
      if (!in_block) {
        print
      }
    }
  ' "$source_file" >"$output_file"
}

extract_replacement_block() {
  local source_file="$1"
  local start_marker="$2"
  local end_marker="$3"
  awk -v start="$start_marker" -v end="$end_marker" '
    $0 == start { in_block = 1; next }
    $0 == end { exit }
    in_block { print }
  ' "$source_file"
}

create_pr() {
  local repo="$1"
  local branch="$2"
  local issue_number="$3"
  local title="$4"
  local template tmp result number
  template="$(agendev::root)/templates/pr-template.md"
  tmp="$(mktemp "${TMPDIR:-/tmp}/agendev-pr-create.XXXXXX")"
  sed "s/ISSUE_NUMBER/${issue_number}/g" "$template" >"$tmp"
  result="$(agendev::gh pr create --repo "$repo" --draft --title "$title" --head "$branch" --body-file "$tmp")"
  rm -f "$tmp"
  number="$(printf '%s' "$result" | sed -n 's#.*/pull/\([0-9][0-9]*\).*#\1#p')"
  jq -n --arg url "$result" --argjson number "${number:-null}" '{url:$url, number:$number}'
}

comment_pr() {
  local repo="$1"
  local pr_number="$2"
  local comment_file="$3"
  agendev::gh pr comment "$pr_number" --repo "$repo" --body-file "$comment_file" >/dev/null
  jq -n --argjson pr "$pr_number" '{commented:true, pr:$pr}'
}

update_summary() {
  local repo="$1"
  local pr_number="$2"
  local update_file="$3"
  local current_body_file summary_file attention_file body_file temp_body
  current_body_file="$(mktemp "${TMPDIR:-/tmp}/agendev-pr-body.XXXXXX")"
  summary_file="$(mktemp "${TMPDIR:-/tmp}/agendev-pr-summary.XXXXXX")"
  attention_file="$(mktemp "${TMPDIR:-/tmp}/agendev-pr-attention.XXXXXX")"
  temp_body="$(mktemp "${TMPDIR:-/tmp}/agendev-pr-updated.XXXXXX")"

  agendev::gh pr view "$pr_number" --repo "$repo" --json body | jq -r '.body' >"$current_body_file"
  extract_replacement_block "$update_file" "<!-- agendev:summary:start -->" "<!-- agendev:summary:end -->" >"$summary_file"
  extract_replacement_block "$update_file" "<!-- agendev:attention:start -->" "<!-- agendev:attention:end -->" >"$attention_file"

  if [[ ! -s "$summary_file" ]]; then
    cat "$update_file" >"$summary_file"
  fi
  if [[ ! -s "$attention_file" ]]; then
    printf 'None.\n' >"$attention_file"
  fi

  replace_marker_block "$current_body_file" "<!-- agendev:summary:start -->" "<!-- agendev:summary:end -->" "$summary_file" "$temp_body"
  mv "$temp_body" "$current_body_file"
  temp_body="$(mktemp "${TMPDIR:-/tmp}/agendev-pr-updated.XXXXXX")"
  replace_marker_block "$current_body_file" "<!-- agendev:attention:start -->" "<!-- agendev:attention:end -->" "$attention_file" "$temp_body"
  mv "$temp_body" "$current_body_file"

  agendev::gh pr edit "$pr_number" --repo "$repo" --body-file "$current_body_file" >/dev/null
  rm -f "$current_body_file" "$summary_file" "$attention_file"
  jq -n --argjson pr "$pr_number" '{updated:true, pr:$pr}'
}

finalize_pr() {
  local repo="$1"
  local pr_number="$2"
  local verdict="$3"
  local reviewer=""
  shift 3
  if [[ "${1:-}" == "--reviewer" ]]; then
    reviewer="${2:-}"
  fi

  case "$verdict" in
    auto-merge)
      agendev::gh pr ready "$pr_number" --repo "$repo" >/dev/null
      agendev::gh pr merge "$pr_number" --repo "$repo" --auto --squash >/dev/null
      ;;
    needs-review)
      agendev::gh pr ready "$pr_number" --repo "$repo" >/dev/null
      if [[ -n "$reviewer" ]]; then
        agendev::gh pr edit "$pr_number" --repo "$repo" --add-reviewer "$reviewer" --add-assignee "$reviewer" >/dev/null
      fi
      ;;
    *)
      agendev::die "Unknown finalize verdict: $verdict"
      ;;
  esac

  jq -n --argjson pr "$pr_number" --arg verdict "$verdict" --arg reviewer "$reviewer" '{
    pr: $pr,
    verdict: $verdict,
    reviewer: $reviewer
  }'
}

case "${1:-}" in
  create)
    [[ $# -eq 5 ]] || { usage >&2; exit 1; }
    create_pr "$2" "$3" "$4" "$5"
    ;;
  comment)
    [[ $# -eq 4 ]] || { usage >&2; exit 1; }
    comment_pr "$2" "$3" "$4"
    ;;
  update-summary)
    [[ $# -eq 4 ]] || { usage >&2; exit 1; }
    update_summary "$2" "$3" "$4"
    ;;
  finalize)
    [[ $# -ge 4 ]] || { usage >&2; exit 1; }
    finalize_pr "$2" "$3" "$4" "${@:5}"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
