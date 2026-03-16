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
  gh-pr-lifecycle.sh line-comment <repo> <pr-number> <file> <start-line> <end-line> <body>
  gh-pr-lifecycle.sh read-actionable <repo> <pr-number> <agent-handle>
  gh-pr-lifecycle.sh poll-mentions <repo> <agent-handle> [--since <timestamp>]
  gh-pr-lifecycle.sh check-permission <repo> <username> <required-level>
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

line_comment() {
  local repo="$1"
  local pr_number="$2"
  local file="$3"
  local start_line="$4"
  local end_line="$5"
  local body="$6"
  local head_sha args
  head_sha="$(agendev::gh pr view "$pr_number" --repo "$repo" --json headRefOid | jq -r '.headRefOid')"
  args=(
    "repos/${repo}/pulls/${pr_number}/comments"
    --method POST
    -f body="$body"
    -f path="$file"
    -F commit_id="$head_sha"
    -F line="$end_line"
    -F side=RIGHT
  )
  if [[ "$start_line" != "$end_line" ]]; then
    args+=(-F start_line="$start_line" -F start_side=RIGHT)
  fi
  agendev::gh api "${args[@]}" >/dev/null
  jq -n --arg path "$file" --argjson start "$start_line" --argjson end "$end_line" '{path:$path, start_line:$start, end_line:$end}'
}

read_actionable() {
  local repo="$1"
  local pr_number="$2"
  local handle="$3"
  local issue_comments review_comments
  issue_comments="$(agendev::gh api "repos/${repo}/issues/${pr_number}/comments")"
  review_comments="$(agendev::gh api "repos/${repo}/pulls/${pr_number}/comments")"

  jq -n \
    --arg handle "@${handle}" \
    --argjson issue_comments "$issue_comments" \
    --argjson review_comments "$review_comments" '
    (
      $issue_comments
      | map(
          select(.body | contains($handle))
          | select(.body | contains("agendev:payload") | not)
          | select(.body | contains("agendev:event") | not)
          | {
              id: .id,
              author: .user.login,
              body: .body,
              html_url: .html_url,
              comment_type: "issue"
            }
        )
    ) + (
      $review_comments
      | map({
          id: .id,
          author: .user.login,
          body: .body,
          html_url: .html_url,
          path: .path,
          comment_type: "review"
        })
    )
  '
}

poll_mentions() {
  local repo="$1"
  local handle="$2"
  local since="${3:-}"
  local issues open_items item_number endpoint comments comment mention_state_args
  open_items="$(agendev::gh api "repos/${repo}/issues?state=open&per_page=100")"

  while IFS= read -r item; do
    [[ -z "$item" ]] && continue
    item_number="$(printf '%s' "$item" | jq -r '.number')"
    endpoint="repos/${repo}/issues/${item_number}/comments"
    if [[ -n "$since" ]]; then
      endpoint="${endpoint}?since=${since}"
    fi
    comments="$(agendev::gh api "$endpoint")"

    while IFS= read -r comment; do
      [[ -z "$comment" ]] && continue
      comment_id="$(printf '%s' "$comment" | jq -r '.id')"
      if "$(agendev::root)/scripts/state.sh" has-mention "$comment_id" >/dev/null 2>&1; then
        continue
      fi
      if [[ "$(printf '%s' "$comment" | jq -r --arg handle "@${handle}" '
        (.body | contains($handle)) and
        (.body | contains("agendev:payload") | not) and
        (.body | contains("agendev:event") | not)
      ')" != "true" ]]; then
        continue
      fi

      jq -n \
        --argjson item "$item" \
        --argjson comment "$comment" '
        {
          comment_id: $comment.id,
          author: $comment.user.login,
          body: $comment.body,
          created_at: $comment.created_at,
          context_type: (if ($item | has("pull_request")) then "pr" else "issue" end),
          pr_number: (if ($item | has("pull_request")) then $item.number else null end),
          issue_number: (if ($item | has("pull_request")) then null else $item.number end)
        }
      '
    done < <(printf '%s' "$comments" | jq -c '.[]')
  done < <(printf '%s' "$open_items" | jq -c '.[]') | jq -s '.'
}

check_permission() {
  local repo="$1"
  local username="$2"
  local required="$3"
  local permission rank required_rank
  permission="$(agendev::gh api "repos/${repo}/collaborators/${username}/permission" | jq -r '.permission')"

  case "$permission" in
    admin) rank=3 ;;
    maintain|write) rank=2 ;;
    triage|read) rank=1 ;;
    *) rank=0 ;;
  esac

  case "$required" in
    admin) required_rank=3 ;;
    write) required_rank=2 ;;
    read) required_rank=1 ;;
    *) agendev::die "Unknown permission level: $required" ;;
  esac

  if (( rank >= required_rank )); then
    jq -n --arg username "$username" --arg permission "$permission" '{allowed:true, username:$username, permission:$permission}'
  else
    jq -n --arg username "$username" --arg permission "$permission" '{allowed:false, username:$username, permission:$permission}'
    exit 1
  fi
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
  line-comment)
    [[ $# -eq 7 ]] || { usage >&2; exit 1; }
    line_comment "$2" "$3" "$4" "$5" "$6" "$7"
    ;;
  read-actionable)
    [[ $# -eq 4 ]] || { usage >&2; exit 1; }
    read_actionable "$2" "$3" "$4"
    ;;
  poll-mentions)
    if [[ $# -eq 4 && "$4" == "--since" ]]; then
      usage >&2
      exit 1
    fi
    if [[ $# -eq 3 ]]; then
      poll_mentions "$2" "$3"
    elif [[ $# -eq 5 && "$4" == "--since" ]]; then
      poll_mentions "$2" "$3" "$5"
    else
      usage >&2
      exit 1
    fi
    ;;
  check-permission)
    [[ $# -eq 4 ]] || { usage >&2; exit 1; }
    check_permission "$2" "$3" "$4"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
