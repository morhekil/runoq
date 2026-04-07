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
  runoq::log "pr-lifecycle" "create_pr: repo=${repo} branch=${branch} issue=#${issue_number} title=\"${title}\""
  template="$(runoq::root)/templates/pr-template.md"
  tmp="$(mktemp "${TMPDIR:-/tmp}/runoq-pr-create.XXXXXX")"
  sed "s/ISSUE_NUMBER/${issue_number}/g" "$template" >"$tmp"
  result="$(runoq::gh pr create --repo "$repo" --draft --title "$title" --head "$branch" --body-file "$tmp")"
  rm -f "$tmp"
  number="$(printf '%s' "$result" | sed -n 's#.*/pull/\([0-9][0-9]*\).*#\1#p')"
  runoq::log "pr-lifecycle" "create_pr: result url=${result} number=${number:-null}"
  jq -n --arg url "$result" --argjson number "${number:-null}" '{url:$url, number:$number}'
}

comment_pr() {
  local repo="$1"
  local pr_number="$2"
  local comment_file="$3"
  runoq::log "pr-lifecycle" "comment_pr: repo=${repo} pr=#${pr_number} comment_file=${comment_file}"
  runoq::gh pr comment "$pr_number" --repo "$repo" --body-file "$comment_file" >/dev/null
  runoq::log "pr-lifecycle" "comment_pr: comment posted successfully on PR #${pr_number}"
  jq -n --argjson pr "$pr_number" '{commented:true, pr:$pr}'
}

update_summary() {
  local repo="$1"
  local pr_number="$2"
  local update_file="$3"
  runoq::log "pr-lifecycle" "update_summary: repo=${repo} pr=#${pr_number} update_file=${update_file}"
  local current_body_file summary_file attention_file body_file temp_body
  current_body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-pr-body.XXXXXX")"
  summary_file="$(mktemp "${TMPDIR:-/tmp}/runoq-pr-summary.XXXXXX")"
  attention_file="$(mktemp "${TMPDIR:-/tmp}/runoq-pr-attention.XXXXXX")"
  temp_body="$(mktemp "${TMPDIR:-/tmp}/runoq-pr-updated.XXXXXX")"

  runoq::gh pr view "$pr_number" --repo "$repo" --json body | jq -r '.body' >"$current_body_file"
  extract_replacement_block "$update_file" "<!-- runoq:summary:start -->" "<!-- runoq:summary:end -->" >"$summary_file"
  extract_replacement_block "$update_file" "<!-- runoq:attention:start -->" "<!-- runoq:attention:end -->" >"$attention_file"

  if [[ ! -s "$summary_file" ]]; then
    cat "$update_file" >"$summary_file"
  fi
  if [[ ! -s "$attention_file" ]]; then
    printf 'None.\n' >"$attention_file"
  fi

  replace_marker_block "$current_body_file" "<!-- runoq:summary:start -->" "<!-- runoq:summary:end -->" "$summary_file" "$temp_body"
  mv "$temp_body" "$current_body_file"
  temp_body="$(mktemp "${TMPDIR:-/tmp}/runoq-pr-updated.XXXXXX")"
  replace_marker_block "$current_body_file" "<!-- runoq:attention:start -->" "<!-- runoq:attention:end -->" "$attention_file" "$temp_body"
  mv "$temp_body" "$current_body_file"

  runoq::gh pr edit "$pr_number" --repo "$repo" --body-file "$current_body_file" >/dev/null
  runoq::log "pr-lifecycle" "update_summary: PR #${pr_number} body updated successfully"
  rm -f "$current_body_file" "$summary_file" "$attention_file"
  jq -n --argjson pr "$pr_number" '{updated:true, pr:$pr}'
}

finalize_pr() {
  local repo="$1"
  local pr_number="$2"
  local verdict="$3"
  runoq::log "pr-lifecycle" "finalize_pr: repo=${repo} pr=#${pr_number} verdict=${verdict}"
  local reviewer=""
  local assigned_reviewer=""
  shift 3
  if [[ "${1:-}" == "--reviewer" ]]; then
    reviewer="${2:-}"
  fi

  ready_pr() {
    local output status
    set +e
    output="$(runoq::gh pr ready "$pr_number" --repo "$repo" 2>&1)"
    status=$?
    set -e
    if [[ "$status" -eq 0 ]]; then
      return 0
    fi
    if [[ "$output" == *'already "ready for review"'* ]]; then
      runoq::log "pr-lifecycle" "ready_pr: already ready — $output"
      return 0
    fi
    runoq::log "pr-lifecycle" "ready_pr: failed — $output"
    return "$status"
  }

  case "$verdict" in
    auto-merge)
      local merge_output merge_status
      runoq::log "pr-lifecycle" "finalize_pr: marking PR #${pr_number} as ready for review"
      ready_pr
      set +e
      merge_output="$(runoq::gh pr merge "$pr_number" --repo "$repo" --auto --squash 2>&1)"
      merge_status=$?
      set -e
      if [[ "$merge_status" -eq 0 ]]; then
        runoq::log "pr-lifecycle" "finalize_pr: auto-merge enabled successfully for PR #${pr_number}"
      elif [[ "$merge_output" == *"Protected branch rules not configured for this branch"* ]] || [[ "$merge_output" == *"enablePullRequestAutoMerge"* ]]; then
        runoq::log "pr-lifecycle" "finalize_pr: auto-merge not available (${merge_output}), falling back to direct squash merge for PR #${pr_number}"
        runoq::gh pr merge "$pr_number" --repo "$repo" --squash --delete-branch --body "" >/dev/null
        runoq::log "pr-lifecycle" "finalize_pr: direct squash merge completed for PR #${pr_number}"
      else
        runoq::log "pr-lifecycle" "finalize_pr: merge failed — $merge_output"
        return "$merge_status"
      fi
      ;;
    needs-review)
      local edit_output edit_status
      runoq::log "pr-lifecycle" "finalize_pr: marking PR #${pr_number} as ready for review (needs-review verdict)"
      ready_pr
      if [[ -n "$reviewer" ]]; then
        set +e
        edit_output="$(runoq::gh pr edit "$pr_number" --repo "$repo" --add-reviewer "$reviewer" --add-assignee "$reviewer" 2>&1)"
        edit_status=$?
        set -e
        if [[ "$edit_status" -eq 0 ]]; then
          assigned_reviewer="$reviewer"
          runoq::log "pr-lifecycle" "finalize_pr: assigned reviewer=${reviewer} to PR #${pr_number}"
        else
          runoq::log "pr-lifecycle" "finalize_pr: failed to assign reviewer=${reviewer} — $edit_output"
        fi
      fi
      ;;
    *)
      runoq::die "Unknown finalize verdict: $verdict"
      ;;
  esac

  jq -n --argjson pr "$pr_number" --arg verdict "$verdict" --arg reviewer "$assigned_reviewer" '{
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
  runoq::log "pr-lifecycle" "line_comment: repo=${repo} pr=#${pr_number} file=${file} lines=${start_line}-${end_line}"
  local head_sha args
  head_sha="$(runoq::gh pr view "$pr_number" --repo "$repo" --json headRefOid | jq -r '.headRefOid')"
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
  runoq::gh api "${args[@]}" >/dev/null
  runoq::log "pr-lifecycle" "line_comment: posted line comment on ${file}:${start_line}-${end_line}"
  jq -n --arg path "$file" --argjson start "$start_line" --argjson end "$end_line" '{path:$path, start_line:$start, end_line:$end}'
}

read_actionable() {
  local repo="$1"
  local pr_number="$2"
  local handle="$3"
  runoq::log "pr-lifecycle" "read_actionable: repo=${repo} pr=#${pr_number} handle=${handle}"
  local issue_comments review_comments
  issue_comments="$(runoq::gh api "repos/${repo}/issues/${pr_number}/comments")"
  review_comments="$(runoq::gh api "repos/${repo}/pulls/${pr_number}/comments")"

  jq -n \
    --arg handle "@${handle}" \
    --argjson issue_comments "$issue_comments" \
    --argjson review_comments "$review_comments" '
    (
      $issue_comments
      | map(
          select(.body | contains($handle))
          | select(.body | contains("runoq:payload") | not)
          | select(.body | contains("runoq:event") | not)
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
  runoq::log "pr-lifecycle" "poll_mentions: repo=${repo} handle=${handle} since=${since:-<all>}"
  local issues open_items item_number endpoint comments comment mention_state_args
  open_items="$(runoq::gh api "repos/${repo}/issues?state=open&per_page=100")"

  while IFS= read -r item; do
    [[ -z "$item" ]] && continue
    item_number="$(printf '%s' "$item" | jq -r '.number')"
    endpoint="repos/${repo}/issues/${item_number}/comments"
    if [[ -n "$since" ]]; then
      endpoint="${endpoint}?since=${since}"
    fi
    comments="$(runoq::gh api "$endpoint")"

    while IFS= read -r comment; do
      [[ -z "$comment" ]] && continue
      comment_id="$(printf '%s' "$comment" | jq -r '.id')"
      if "$(runoq::root)/scripts/state.sh" has-mention "$comment_id" >/dev/null 2>&1; then
        continue
      fi
      if [[ "$(printf '%s' "$comment" | jq -r --arg handle "@${handle}" '
        (.body | contains($handle)) and
        (.body | contains("runoq:payload") | not) and
        (.body | contains("runoq:event") | not)
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
  runoq::log "pr-lifecycle" "check_permission: repo=${repo} username=${username} required=${required}"
  local permission rank required_rank
  permission="$(runoq::gh api "repos/${repo}/collaborators/${username}/permission" | jq -r '.permission')"

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
    *) runoq::die "Unknown permission level: $required" ;;
  esac

  if (( rank >= required_rank )); then
    runoq::log "pr-lifecycle" "check_permission: allowed=true user=${username} permission=${permission} required=${required}"
    jq -n --arg username "$username" --arg permission "$permission" '{allowed:true, username:$username, permission:$permission}'
  else
    runoq::log "pr-lifecycle" "check_permission: allowed=false user=${username} permission=${permission} required=${required}"
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
