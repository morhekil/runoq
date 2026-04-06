#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  plan-comment-handler.sh <repo> <issue-number> <plan-file>
EOF
}

main() {
  [[ $# -eq 3 ]] || { usage >&2; exit 1; }
  local repo="$1" issue_number="$2" plan_file="$3"

  local issue_json comment_body comments_path payload response_path body_file claude_bin
  issue_json="$(runoq::gh issue view "$issue_number" --repo "$repo" --json number,title,body,comments)"
  comment_body="$(printf '%s' "$issue_json" | jq -r '
    .comments // []
    | map(select((.author.login // "") != "runoq" and (.body // "" | contains("runoq:event") | not)))
    | last
    | .body // empty
  ')"
  [[ -n "$comment_body" ]] || exit 0

  comments_path="$(mktemp "${TMPDIR:-/tmp}/runoq-plan-comments.XXXXXX")"
  printf '%s' "$issue_json" | jq '.comments // []' >"$comments_path"
  payload="$(
    jq -cn \
      --arg repo "$repo" \
      --argjson issueNumber "$issue_number" \
      --arg planPath "$plan_file" \
      --arg issueTitle "$(printf '%s' "$issue_json" | jq -r '.title')" \
      --arg issueBody "$(printf '%s' "$issue_json" | jq -r '.body')" \
      --arg commentBody "$comment_body" \
      --arg commentsJsonPath "$comments_path" \
      '{repo:$repo, issueNumber:$issueNumber, planPath:$planPath, issueTitle:$issueTitle, issueBody:$issueBody, commentBody:$commentBody, commentsJsonPath:$commentsJsonPath}'
  )"

  claude_bin="${RUNOQ_CLAUDE_BIN:-claude}"
  runoq::captured_exec claude "$(runoq::target_root)" "$claude_bin" --agent plan-comment-responder --add-dir "$(runoq::root)" -- "$payload" >/dev/null
  response_path="$RUNOQ_LAST_CLAUDE_CAPTURE_DIR/response.txt"
  body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-plan-comment.XXXXXX")"
  if ! grep -q 'runoq:event' "$response_path"; then
    printf '<!-- runoq:event -->\n\n' >"$body_file"
  fi
  cat "$response_path" >>"$body_file"
  runoq::gh issue comment "$issue_number" --repo "$repo" --body-file "$body_file" >/dev/null
  printf 'Responded to comments on #%s\n' "$issue_number"
}

main "$@"
