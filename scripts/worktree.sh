#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  worktree.sh create <issue-number> <title>
  worktree.sh remove <issue-number>
  worktree.sh inspect <issue-number>
  worktree.sh branch-name <issue-number> <title>
EOF
}

create_worktree() {
  local issue="$1"
  local title="$2"
  local target_root branch path base_ref
  target_root="$(agendev::target_root)"
  branch="$(agendev::branch_name "$issue" "$title")"
  path="$(agendev::worktree_path "$issue")"
  base_ref="$(agendev::default_branch_ref)"

  git -C "$target_root" fetch origin main
  if [[ -d "$path/.git" || -e "$path" ]]; then
    agendev::die "Worktree already exists: $path"
  fi
  git -C "$target_root" worktree add "$path" -b "$branch" "$base_ref" >/dev/null
  jq -n --arg branch "$branch" --arg path "$path" --arg base_ref "$base_ref" '{
    branch: $branch,
    worktree: $path,
    base_ref: $base_ref
  }'
}

remove_worktree() {
  local issue="$1"
  local target_root path
  target_root="$(agendev::target_root)"
  path="$(agendev::worktree_path "$issue")"
  if [[ ! -e "$path" ]]; then
    jq -n --arg worktree "$path" '{removed:false, worktree:$worktree}'
    return
  fi
  git -C "$target_root" worktree remove "$path" --force >/dev/null
  jq -n --arg worktree "$path" '{removed:true, worktree:$worktree}'
}

inspect_worktree() {
  local issue="$1"
  local path
  path="$(agendev::worktree_path "$issue")"
  jq -n --arg worktree "$path" --arg exists "$([[ -e "$path" ]] && echo true || echo false)" '{
    worktree: $worktree,
    exists: ($exists == "true")
  }'
}

case "${1:-}" in
  create)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    create_worktree "$2" "$3"
    ;;
  remove)
    [[ $# -eq 2 ]] || { usage >&2; exit 1; }
    remove_worktree "$2"
    ;;
  inspect)
    [[ $# -eq 2 ]] || { usage >&2; exit 1; }
    inspect_worktree "$2"
    ;;
  branch-name)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    agendev::branch_name "$2" "$3"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
