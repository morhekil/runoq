#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RUNOQ_ROOT="${RUNOQ_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
export RUNOQ_ROOT

runtime_bin="${RUNOQ_RUNTIME_BIN:-}"
if [[ -n "$runtime_bin" ]]; then
  exec "$runtime_bin" "__worktree" "$@"
fi
go_bin="${RUNOQ_GO_BIN:-go}"
command -v "$go_bin" >/dev/null 2>&1 || {
  echo "runoq: Go toolchain not found: $go_bin" >&2
  exit 1
}
cd "$RUNOQ_ROOT"
exec "$go_bin" run "$RUNOQ_ROOT/cmd/runoq-runtime" "__worktree" "$@"

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
  target_root="$(runoq::target_root)"
  branch="$(runoq::branch_name "$issue" "$title")"
  path="$(runoq::worktree_path "$issue")"
  base_ref="$(runoq::default_branch_ref)"
  runoq::log "worktree" "create: source_ref=${base_ref} target_path=${path} branch=${branch}"

  git -C "$target_root" fetch origin main >/dev/null 2>&1
  if [[ -d "$path/.git" || -e "$path" ]]; then
    runoq::die "Worktree already exists: $path"
  fi
  git -C "$target_root" worktree add "$path" -b "$branch" "$base_ref" >/dev/null 2>&1
  runoq::configure_git_bot_identity "$path" 2>/dev/null || true
  jq -n --arg branch "$branch" --arg path "$path" --arg base_ref "$base_ref" '{
    branch: $branch,
    worktree: $path,
    base_ref: $base_ref
  }'
}

remove_worktree() {
  local issue="$1"
  local target_root path
  target_root="$(runoq::target_root)"
  path="$(runoq::worktree_path "$issue")"
  runoq::log "worktree" "remove: path=${path}"
  if [[ ! -e "$path" ]]; then
    jq -n --arg worktree "$path" '{removed:false, worktree:$worktree}'
    return
  fi
  git -C "$target_root" worktree remove "$path" --force >/dev/null 2>&1
  jq -n --arg worktree "$path" '{removed:true, worktree:$worktree}'
}

inspect_worktree() {
  local issue="$1"
  local path
  path="$(runoq::worktree_path "$issue")"
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
    runoq::branch_name "$2" "$3"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
