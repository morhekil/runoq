#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  plan-comment-handler.sh <repo> <issue-number> <plan-file>
EOF
}

add_reaction() {
  local comment_id="$1" content="$2"
  runoq::gh api graphql -f query="$(printf 'mutation { addReaction(input: {subjectId: "%s", content: %s}) { reaction { content } } }' "$comment_id" "$content")" >/dev/null 2>&1 || true
}

main() {
  [[ $# -eq 3 ]] || { usage >&2; exit 1; }
  local repo="$1" issue_number="$2" plan_file="$3"

  local issue_json unresponded_ids
  issue_json="$(runoq::gh issue view "$issue_number" --repo "$repo" --json number,title,body,comments)"
  unresponded_ids="$(printf '%s' "$issue_json" | "$(runoq::root)/scripts/tick-fmt.sh" find-unresponded-comments)"

  local count
  count="$(printf '%s' "$unresponded_ids" | jq 'length')"
  [[ "$count" -gt 0 ]] || exit 0

  # Gather all unresponded comment bodies for the agent
  local comment_body
  comment_body="$(printf '%s' "$issue_json" | jq -r --argjson ids "$unresponded_ids" '
    .comments // []
    | map(select(.id as $id | $ids | index($id)))
    | map(.body)
    | join("\n\n---\n\n")
  ')"

  # Mark all as picked up (eyes)
  local cid
  while IFS= read -r cid; do
    [[ -n "$cid" ]] && add_reaction "$cid" "EYES"
  done < <(printf '%s' "$unresponded_ids" | jq -r '.[]')

  local comments_path payload response_path claude_bin
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

  # Parse structured agent response
  local action_json action reply_text
  action_json="$("$(runoq::root)/scripts/tick-fmt.sh" parse-agent-response < "$response_path")"
  action="$(printf '%s' "$action_json" | jq -r '.action')"
  reply_text="$(printf '%s' "$action_json" | jq -r '.reply')"

  # Post reply comment
  local body_file
  body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-plan-comment.XXXXXX")"
  if ! printf '%s' "$reply_text" | grep -q 'runoq:event'; then
    printf '<!-- runoq:event -->\n\n' >"$body_file"
  fi
  printf '%s\n' "$reply_text" >>"$body_file"
  runoq::gh issue comment "$issue_number" --repo "$repo" --body-file "$body_file" >/dev/null

  # Dispatch side effects based on action
  case "$action" in
    change-request)
      local revised_proposal_json proposal_body_file current_body new_body_file review_type
      revised_proposal_json="$(printf '%s' "$action_json" | jq -c '.revised_proposal')"
      review_type="milestone"
      if printf '%s' "$issue_json" | jq -r '.body // ""' | grep -q 'Proposed tasks'; then
        review_type="task"
      fi
      proposal_body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-revised-body.XXXXXX")"
      jq -cn \
        --argjson proposal "$revised_proposal_json" \
        --arg review_type "$review_type" \
        '{proposal:$proposal, technical:{verdict:"",score:"",checklist:""}, product:{verdict:"",score:"",checklist:""}, review_type:$review_type}' \
        | "$(runoq::root)/scripts/tick-fmt.sh" proposal-comment-body >"$proposal_body_file"
      current_body="$(runoq::gh issue view "$issue_number" --repo "$repo" --json body --jq '.body // ""')"
      new_body_file="$(mktemp "${TMPDIR:-/tmp}/runoq-new-body.XXXXXX")"
      printf '%s' "$current_body" | "$(runoq::root)/scripts/tick-fmt.sh" replace-proposal-in-body "$proposal_body_file" >"$new_body_file"
      runoq::gh issue edit "$issue_number" --repo "$repo" --body-file "$new_body_file" >/dev/null
      runoq::info "updated issue body with revised proposal"
      ;;
    approve)
      local plan_approved_label
      plan_approved_label="$(runoq::config_get '.labels.planApproved')"
      runoq::gh issue edit "$issue_number" --repo "$repo" --add-label "$plan_approved_label" >/dev/null
      runoq::info "added plan-approved label"
      ;;
    question)
      ;;
  esac

  # Mark all as successfully responded (+1)
  while IFS= read -r cid; do
    [[ -n "$cid" ]] && add_reaction "$cid" "THUMBS_UP"
  done < <(printf '%s' "$unresponded_ids" | jq -r '.[]')

  printf 'Responded to comments on #%s\n' "$issue_number"
}

main "$@"
